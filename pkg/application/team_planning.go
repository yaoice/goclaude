// Package application — team planning orchestration for Plan-then-Execute architecture.
//
// This file implements the Planning Phase lifecycle:
//  1. InitiatePlanning: Set team phase to Planning, broadcast objective to members
//  2. CollectProposals: Members submit task proposals
//  3. ConsolidatePlan: Leader merges proposals into unified execution plan
//  4. ValidateAndApprove: Leader validates plan integrity, transitions to Executing
//  5. Replan: On task failure, pause execution and return to Planning
package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/yaoice/goclaude/pkg/domain/team"
)

// =============================================================================
// Planning Errors
// =============================================================================

var (
	ErrPlanNotApproved     = errors.New("execution plan has not been approved")
	ErrPhaseNotPlanning    = errors.New("team is not in planning phase")
	ErrPhaseNotExecuting   = errors.New("team is not in executing phase")
	ErrNotLeader           = errors.New("only the team leader can perform this action")
	ErrMaxReplansExceeded  = errors.New("maximum replan attempts exceeded")
	ErrExecutionInProgress = errors.New("cannot modify plan while execution is in progress")
	ErrInvalidTransition   = errors.New("invalid phase transition")
)

// =============================================================================
// Planning Phase Initiation
// =============================================================================

// InitiatePlanningInput 启动 Planning Phase 的入参。
type InitiatePlanningInput struct {
	TeamName   string
	Objective  string // 团队目标描述
	LeaderName string // 发起规划的 leader
}

// InitiatePlanning 将团队切换到 Planning Phase，广播目标给所有成员。
//
// 此方法：
//  1. 校验 team 存在且 leader 匹配
//  2. 创建初始 ExecutionPlan（Status=PlanDrafting）
//  3. 将 Phase 设为 PhasePlanning
//  4. 向所有非 leader 成员广播 plan_consolidate 消息（含目标描述）
func (s *TeamService) InitiatePlanning(in InitiatePlanningInput) (*team.File, error) {
	if strings.TrimSpace(in.TeamName) == "" {
		return nil, fmt.Errorf("team name is required")
	}
	if strings.TrimSpace(in.Objective) == "" {
		return nil, fmt.Errorf("objective is required for planning")
	}

	s.mu.Lock()

	f, err := s.requireTeam(in.TeamName)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}

	// 仅 leader 可以发起规划
	if in.LeaderName != "" && in.LeaderName != team.LeaderName {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %s is not the leader", ErrNotLeader, in.LeaderName)
	}

	// 创建初始计划
	now := time.Now().UnixMilli()
	f.Plan = &team.ExecutionPlan{
		Objective:   in.Objective,
		Tasks:       make([]team.PlannedTask, 0),
		Assignments: make([]team.PlanAssignment, 0),
		CreatedAt:   now,
		UpdatedAt:   now,
		Status:      team.PlanDrafting,
	}

	// 切换阶段
	f.Phase = team.PhasePlanning

	if err := s.Store.Write(f); err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("write team: %w", err)
	}

	// 收集广播所需数据（在释放锁之前）
	objective := in.Objective
	memberNames := make([]string, 0)
	memberRoles := make(map[string]string)
	for _, m := range f.NonLeaderMembers() {
		memberNames = append(memberNames, m.Name)
		if m.AgentType != "" {
			memberRoles[m.Name] = m.AgentType
		}
	}

	s.mu.Unlock()

	// 构建计划摘要文本
	var memberList strings.Builder
	for _, name := range memberNames {
		role := memberRoles[name]
		if role == "" {
			role = "team-worker"
		}
		memberList.WriteString(fmt.Sprintf("  - %s (%s)\n", name, role))
	}

	planSummary := fmt.Sprintf(
		`Team: %s
Phase: PLANNING
Objective: %s

Members:
%s
=== YOUR ROLE ===
You are a team member in the Planning Phase. Your job is to:
1. Review the objective above
2. Decompose the objective into concrete, well-defined tasks
3. For each task you propose, specify: title, description, estimated complexity (low/medium/high), and dependencies
4. Submit your proposals via collect_proposal or send_message(type=plan_propose)

DO NOT start executing any tasks yet. The team is still in the Planning Phase.
Only after the leader approves the consolidated plan will Execution begin.`,
		in.TeamName, objective,
		memberList.String(),
	)

	// 广播 plan_consolidate 消息给所有成员（在释放锁之后，因为 Send 也要获取锁）
	for _, memberName := range memberNames {
		msg := team.NewPlanConsolidate(team.LeaderName, planSummary,
			fmt.Sprintf("planning started: %s", truncateString(objective, 60)),
		)
		_, _ = s.Send(SendInput{
			TeamName:   in.TeamName,
			From:       team.LeaderName,
			To:         memberName,
			Structured: &msg,
		})
	}

	s.logger.Info("planning phase initiated",
		slog.String("team", in.TeamName),
		slog.String("objective", in.Objective),
		slog.Int("notified_members", len(memberNames)),
	)

	return f, nil
}

// =============================================================================
// Proposal Collection
// =============================================================================

// CollectProposal 成员提交自己的任务提案。
//
// 提案会被合并到团队执行计划中。每个成员可以多次调用此方法来增量提交任务。
func (s *TeamService) CollectProposal(teamName, memberName string, proposal team.PlanProposal) error {
	if strings.TrimSpace(teamName) == "" {
		return fmt.Errorf("team name is required")
	}
	if memberName == "" {
		return fmt.Errorf("member name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.requireTeam(teamName)
	if err != nil {
		return err
	}

	// 阶段检查：只有在 Planning / Replanning 阶段才能提交提案
	if !f.Phase.PlanningAllowed() {
		return fmt.Errorf("%w: current phase is %q", ErrPhaseNotPlanning, f.Phase)
	}

	if f.Plan == nil {
		return errors.New("team has no execution plan; initiate planning first")
	}

	now := time.Now().UnixMilli()

	// 合并提案中的任务到计划中
	for _, pt := range proposal.Tasks {
		if pt.ID == "" {
			pt.ID = team.GenPlanTaskID()
		}
		if pt.ProposedBy == "" {
			pt.ProposedBy = memberName
		}

		// 检查是否已存在同 ID 任务（更新）或追加
		found := false
		for i := range f.Plan.Tasks {
			if f.Plan.Tasks[i].ID == pt.ID {
				f.Plan.Tasks[i] = pt
				found = true
				break
			}
		}
		if !found {
			f.Plan.Tasks = append(f.Plan.Tasks, pt)
		}
	}

	f.Plan.UpdatedAt = now
	if err := s.Store.Write(f); err != nil {
		return fmt.Errorf("write team: %w", err)
	}

	s.logger.Info("proposal collected",
		slog.String("team", teamName),
		slog.String("member", memberName),
		slog.Int("tasks_submitted", len(proposal.Tasks)),
	)

	return nil
}

// =============================================================================
// Plan Consolidation & Approval
// =============================================================================

// ApprovePlanInput 审批计划的入参。
type ApprovePlanInput struct {
	TeamName    string
	LeaderName  string                // 审批人（必须为 LeaderName）
	Assignments []team.PlanAssignment // 任务到成员的分配
}

// ApprovePlan 验证并批准执行计划，将团队切换到 Executing Phase。
//
// 审批流程：
//  1. 校验 team 存在且操作者为 leader
//  2. 校验当前阶段为 Planning / Replanning
//  3. 执行计划完整性验证（Validate）
//  4. 将 Assignments 写入计划
//  5. 将 Plan.Status 设为 PlanApproved
//  6. 将 Phase 切换为 PhaseExecuting
//  7. 将计划任务导出为 SharedTask 列表
//  8. 通知所有被分配成员
func (s *TeamService) ApprovePlan(in ApprovePlanInput) (*team.File, error) {
	if strings.TrimSpace(in.TeamName) == "" {
		return nil, fmt.Errorf("team name is required")
	}
	if in.LeaderName != "" && in.LeaderName != team.LeaderName {
		return nil, fmt.Errorf("%w: %s is not the leader", ErrNotLeader, in.LeaderName)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.requireTeam(in.TeamName)
	if err != nil {
		return nil, err
	}

	// 阶段检查
	if !f.Phase.PlanningAllowed() {
		return nil, fmt.Errorf("%w: current phase is %q", ErrPhaseNotPlanning, f.Phase)
	}

	if f.Plan == nil {
		return nil, errors.New("team has no execution plan")
	}

	// 收集所有非 leader 成员名
	memberNames := make([]string, 0)
	for _, m := range f.NonLeaderMembers() {
		memberNames = append(memberNames, m.Name)
	}

	// 写入分配
	f.Plan.Assignments = in.Assignments
	f.Plan.Status = team.PlanSubmitted

	// 验证计划
	if err := f.Plan.Validate(memberNames); err != nil {
		f.Plan.Status = team.PlanRejected
		f.Plan.RejectionReason = err.Error()
		f.Plan.UpdatedAt = time.Now().UnixMilli()
		_ = s.Store.Write(f)
		s.logger.Warn("plan validation failed",
			slog.String("team", in.TeamName),
			slog.String("error", err.Error()),
		)
		return nil, fmt.Errorf("plan validation failed: %w", err)
	}

	// 验证分配覆盖
	assignedTasks := make(map[string]bool)
	for _, a := range in.Assignments {
		assignedTasks[a.TaskID] = true
	}
	for _, t := range f.Plan.Tasks {
		if !assignedTasks[t.ID] {
			return nil, fmt.Errorf("task %q is not assigned to any member", t.ID)
		}
	}

	// 通过审批
	now := time.Now().UnixMilli()
	f.Plan.Status = team.PlanApproved
	f.Plan.ApprovedAt = now
	f.Plan.ApprovedBy = team.LeaderName
	f.Plan.UpdatedAt = now

	// 切换阶段
	if !f.Phase.CanTransitionTo(team.PhaseExecuting) {
		return nil, fmt.Errorf("%w: cannot transition from %q to %q",
			ErrInvalidTransition, f.Phase, team.PhaseExecuting)
	}
	f.Phase = team.PhaseExecuting

	// 导出 SharedTasks
	f.Tasks = f.Plan.ToSharedTasks()

	if err := s.Store.Write(f); err != nil {
		return nil, fmt.Errorf("write team: %w", err)
	}

	// 通知所有被分配的成员
	for _, a := range f.Plan.Assignments {
		_ = s.NotifyTaskAssigned(in.TeamName, a.TaskID, "", "", a.MemberName, team.LeaderName)
	}

	s.logger.Info("plan approved, execution phase started",
		slog.String("team", in.TeamName),
		slog.Int("tasks", len(f.Plan.Tasks)),
		slog.Int("assignments", len(f.Plan.Assignments)),
	)

	return f, nil
}

// RejectPlanInput 驳回计划的入参。
type RejectPlanInput struct {
	TeamName   string
	LeaderName string
	Reason     string // 驳回原因
}

// RejectPlan 驳回执行计划，团队停留在 Planning Phase。
func (s *TeamService) RejectPlan(in RejectPlanInput) (*team.File, error) {
	if strings.TrimSpace(in.TeamName) == "" {
		return nil, fmt.Errorf("team name is required")
	}
	if in.Reason == "" {
		return nil, fmt.Errorf("rejection reason is required")
	}

	s.mu.Lock()

	f, err := s.requireTeam(in.TeamName)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}

	if !f.Phase.PlanningAllowed() {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: current phase is %q", ErrPhaseNotPlanning, f.Phase)
	}

	if f.Plan == nil {
		s.mu.Unlock()
		return nil, errors.New("team has no execution plan to reject")
	}

	f.Plan.Status = team.PlanRejected
	f.Plan.RejectionReason = in.Reason
	f.Plan.UpdatedAt = time.Now().UnixMilli()

	if err := s.Store.Write(f); err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("write team: %w", err)
	}

	// 收集非 leader 成员列表（在释放锁之前）
	memberNames := make([]string, 0)
	for _, m := range f.NonLeaderMembers() {
		memberNames = append(memberNames, m.Name)
	}
	rejectionReason := in.Reason

	s.mu.Unlock()

	// 广播驳回原因给所有成员（在释放锁之后，因为 Send 也要获取锁）
	for _, memberName := range memberNames {
		msg := team.NewPlanFeedback(team.LeaderName, memberName,
			rejectionReason,
			fmt.Sprintf("plan rejected: %s", truncateString(rejectionReason, 80)),
		)
		_, _ = s.Send(SendInput{
			TeamName:   in.TeamName,
			From:       team.LeaderName,
			To:         memberName,
			Structured: &msg,
		})
	}

	s.logger.Info("plan rejected", slog.String("team", in.TeamName))
	return f, nil
}

// =============================================================================
// Phase Transitions
// =============================================================================

// TransitionPhase 执行 phase 转换（带校验）。
func (s *TeamService) TransitionPhase(teamName string, toPhase team.TeamPhase) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.requireTeam(teamName)
	if err != nil {
		return err
	}

	if !f.Phase.CanTransitionTo(toPhase) {
		return fmt.Errorf("%w: %q → %q is not allowed",
			ErrInvalidTransition, f.Phase, toPhase)
	}

	f.Phase = toPhase
	return s.Store.Write(f)
}

// MarkCompleted 将所有任务标记为完成，团队进入 Completed Phase。
func (s *TeamService) MarkCompleted(teamName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.requireTeam(teamName)
	if err != nil {
		return err
	}

	if f.Phase != team.PhaseExecuting {
		return fmt.Errorf("%w: expected %q, got %q",
			ErrPhaseNotExecuting, team.PhaseExecuting, f.Phase)
	}

	// 检查所有任务是否完成
	for _, t := range f.Tasks {
		if t.Status != team.SharedTaskCompleted {
			return fmt.Errorf("cannot complete: task %q is still %q", t.ID, t.Status)
		}
	}

	f.Phase = team.PhaseCompleted
	if err := s.Store.Write(f); err != nil {
		return err
	}

	s.logger.Info("team completed", slog.String("team", teamName))
	return nil
}

// =============================================================================
// Replan — Pause Execution and Return to Planning
// =============================================================================

// InitiateReplanInput 发起 re-plan 的入参。
type InitiateReplanInput struct {
	TeamName      string
	FailedTaskID  string
	FailedMember  string
	FailureReason string
}

// InitiateReplan 暂停执行，返回 Planning Phase，让团队重新规划。
//
// 流程：
//  1. 将 Phase 切换到 PhaseReplanning
//  2. 保留已完成任务的结果
//  3. 构建 ReplanContext
//  4. 广播 re-plan 通知给所有成员
//  5. 将 Phase 切换到 PhasePlanning
func (s *TeamService) InitiateReplan(in InitiateReplanInput) (*team.File, error) {
	if strings.TrimSpace(in.TeamName) == "" {
		return nil, fmt.Errorf("team name is required")
	}

	s.mu.Lock()

	f, err := s.requireTeam(in.TeamName)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}

	if f.Phase != team.PhaseExecuting {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: expected %q, got %q",
			ErrPhaseNotExecuting, team.PhaseExecuting, f.Phase)
	}

	// 检查 re-plan 上限
	f.ReplanCount++
	if f.MaxReplanAttempts > 0 && f.ReplanCount > f.MaxReplanAttempts {
		f.Phase = team.PhaseFailed
		_ = s.Store.Write(f)
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %d/%d attempts",
			ErrMaxReplansExceeded, f.ReplanCount, f.MaxReplanAttempts)
	}

	// 进入 Replanning 瞬态
	f.Phase = team.PhaseReplanning

	// 收集上下文
	completedTasks := make([]string, 0)
	pendingTasks := make([]string, 0)
	for _, t := range f.Tasks {
		switch t.Status {
		case team.SharedTaskCompleted:
			completedTasks = append(completedTasks, t.ID)
		case team.SharedTaskPending, team.SharedTaskWorking, team.SharedTaskBlocked:
			pendingTasks = append(pendingTasks, t.ID)
		}
	}

	replanCtx := team.ReplanContext{
		FailedTaskID:      in.FailedTaskID,
		FailedMember:      in.FailedMember,
		FailureReason:     in.FailureReason,
		CompletedTasks:    completedTasks,
		PendingTasks:      pendingTasks,
		ReplanAttempt:     f.ReplanCount,
		MaxReplanAttempts: f.MaxReplanAttempts,
	}

	// 重置计划：清空旧任务列表（已完成任务保留在 f.Tasks 中），
	// 保留 Objective 和 Assignments 框架，供新规划周期使用。
	if f.Plan != nil {
		f.Plan.Status = team.PlanDrafting
		f.Plan.Tasks = make([]team.PlannedTask, 0)
		f.Plan.Assignments = make([]team.PlanAssignment, 0)
	}

	// 将失败任务标记为 blocked
	for i := range f.Tasks {
		if f.Tasks[i].ID == in.FailedTaskID {
			f.Tasks[i].Status = team.SharedTaskBlocked
			f.Tasks[i].UpdatedAt = time.Now().UnixMilli()
			break
		}
	}

	if err := s.Store.Write(f); err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("write team: %w", err)
	}

	// 切换到 Planning Phase
	f.Phase = team.PhasePlanning
	if err := s.Store.Write(f); err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("write team phase: %w", err)
	}

	// 收集广播所需数据（释放锁前）
	memberNames := make([]string, 0)
	for _, m := range f.NonLeaderMembers() {
		memberNames = append(memberNames, m.Name)
	}
	replanMsg := buildReplanMessage(replanCtx)
	replanSummary := fmt.Sprintf("replan needed: %s", truncateString(in.FailureReason, 60))
	replanCount := f.ReplanCount

	s.mu.Unlock()

	// 广播 re-plan 通知（释放锁后，因为 Send 也要获取锁）
	for _, memberName := range memberNames {
		msg := team.NewPlanFeedback(team.LeaderName, memberName,
			replanMsg,
			replanSummary,
		)
		_, _ = s.Send(SendInput{
			TeamName:   in.TeamName,
			From:       team.LeaderName,
			To:         memberName,
			Structured: &msg,
		})
	}

	s.logger.Info("replan initiated",
		slog.String("team", in.TeamName),
		slog.String("failed_task", in.FailedTaskID),
		slog.Int("replan_attempt", replanCount),
	)

	return f, nil
}

// =============================================================================
// Plan Retrieval
// =============================================================================

// GetExecutionPlan 获取团队的执行计划。
func (s *TeamService) GetExecutionPlan(teamName string) (*team.ExecutionPlan, error) {
	f, err := s.GetTeam(teamName)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, ErrTeamNotFound
	}
	return f.Plan, nil
}

// IsExecutionAllowed 检查团队当前是否允许执行任务。
func (s *TeamService) IsExecutionAllowed(teamName string) (bool, error) {
	f, err := s.GetTeam(teamName)
	if err != nil {
		return false, err
	}
	if f == nil {
		return false, ErrTeamNotFound
	}
	return f.Phase.ExecutionAllowed(), nil
}

// =============================================================================
// Helpers
// =============================================================================

// truncateString 截断字符串到指定长度。
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// buildReplanMessage 构建 re-plan 通知消息。
func buildReplanMessage(ctx team.ReplanContext) string {
	var sb strings.Builder
	sb.WriteString("=== REPLAN REQUEST ===\n\n")
	sb.WriteString(fmt.Sprintf("Failed Task: %s\n", ctx.FailedTaskID))
	sb.WriteString(fmt.Sprintf("Failed Member: %s\n", ctx.FailedMember))
	sb.WriteString(fmt.Sprintf("Reason: %s\n\n", ctx.FailureReason))
	if len(ctx.CompletedTasks) > 0 {
		sb.WriteString(fmt.Sprintf("Completed Tasks (%d): %s\n",
			len(ctx.CompletedTasks), strings.Join(ctx.CompletedTasks, ", ")))
	}
	if len(ctx.PendingTasks) > 0 {
		sb.WriteString(fmt.Sprintf("Pending Tasks (%d): %s\n",
			len(ctx.PendingTasks), strings.Join(ctx.PendingTasks, ", ")))
	}
	sb.WriteString(fmt.Sprintf("\nReplan Attempt: %d", ctx.ReplanAttempt))
	if ctx.MaxReplanAttempts > 0 {
		sb.WriteString(fmt.Sprintf("/%d", ctx.MaxReplanAttempts))
	}
	sb.WriteString("\n\nPlease review the failed task and submit revised proposals.")
	sb.WriteString("\nUse collect_proposal to submit your updated task plan.")
	return sb.String()
}

// SerializePlan 序列化计划为 JSON 字符串（用于消息传递）。
func SerializePlan(plan *team.ExecutionPlan) (string, error) {
	if plan == nil {
		return "", errors.New("plan is nil")
	}
	b, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal plan: %w", err)
	}
	return string(b), nil
}

// DeserializePlan 从 JSON 字符串反序列化计划。
func DeserializePlan(data string) (*team.ExecutionPlan, error) {
	if strings.TrimSpace(data) == "" {
		return nil, errors.New("plan data is empty")
	}
	var plan team.ExecutionPlan
	if err := json.Unmarshal([]byte(data), &plan); err != nil {
		return nil, fmt.Errorf("unmarshal plan: %w", err)
	}
	return &plan, nil
}
