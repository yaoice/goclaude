// Package team — planning domain types for Plan-then-Execute architecture.
//
// This file defines the types that enable a strict two-phase execution model
// for agent-teams: Planning → Execution, with enforced state transitions and
// re-plan-on-failure semantics.
package team

import (
	"errors"
	"fmt"
	"time"
)

// =============================================================================
// Team Phase — top-level lifecycle state
// =============================================================================

// TeamPhase 表示团队的当前执行阶段。
//
// 状态机：
//
//	Planning → Executing → Completed
//	     ↑          │
//	     └──────────┘ (task failure triggers re-plan)
type TeamPhase string

const (
	// PhasePlanning 团队正处于协同规划阶段。成员可以交流讨论、分析目标，
	// 但严禁修改文件或执行任务。
	PhasePlanning TeamPhase = "planning"

	// PhaseExecuting 计划已审批通过，团队正在按计划执行任务。
	PhaseExecuting TeamPhase = "executing"

	// PhaseCompleted 所有任务已完成，团队工作结束。
	PhaseCompleted TeamPhase = "completed"

	// PhaseFailed 团队遇到不可恢复的错误（如多次 re-plan 后仍失败）。
	// 需要人工干预或重建团队。
	PhaseFailed TeamPhase = "failed"

	// PhaseReplanning 执行中发现任务失败，正在重新规划。
	// 这是 Planning→Executing 循环中的瞬态。
	PhaseReplanning TeamPhase = "replanning"
)

// IsValid 报告 p 是否为已识别的阶段。
func (p TeamPhase) IsValid() bool {
	switch p {
	case PhasePlanning, PhaseExecuting, PhaseCompleted, PhaseFailed, PhaseReplanning:
		return true
	}
	return false
}

// CanTransitionTo 检查当前阶段能否转换到目标阶段。
func (p TeamPhase) CanTransitionTo(target TeamPhase) bool {
	if !target.IsValid() {
		return false
	}
	switch p {
	case PhasePlanning:
		return target == PhaseExecuting || target == PhaseFailed
	case PhaseExecuting:
		return target == PhaseCompleted || target == PhaseFailed || target == PhaseReplanning
	case PhaseReplanning:
		return target == PhasePlanning
	case PhaseCompleted, PhaseFailed:
		return false // 终态不可变
	default:
		return false
	}
}

// ExecutionAllowed 报告当前阶段是否允许执行任务。
func (p TeamPhase) ExecutionAllowed() bool {
	return p == PhaseExecuting
}

// PlanningAllowed 报告当前阶段是否允许规划活动。
func (p TeamPhase) PlanningAllowed() bool {
	return p == PhasePlanning || p == PhaseReplanning
}

// =============================================================================
// Plan Status — execution plan lifecycle
// =============================================================================

// PlanStatus 表示执行计划在审批流程中的状态。
type PlanStatus string

const (
	// PlanEmpty 尚未创建任何计划。
	PlanEmpty PlanStatus = "empty"

	// PlanDrafting 成员正在起草各自的任务提案。
	PlanDrafting PlanStatus = "drafting"

	// PlanSubmitted 计划已提交给 leader 审批。
	PlanSubmitted PlanStatus = "submitted"

	// PlanApproved leader 已批准该计划。
	PlanApproved PlanStatus = "approved"

	// PlanRejected leader 驳回了该计划，需要修改后重新提交。
	PlanRejected PlanStatus = "rejected"
)

func (s PlanStatus) IsValid() bool {
	switch s {
	case PlanEmpty, PlanDrafting, PlanSubmitted, PlanApproved, PlanRejected:
		return true
	}
	return false
}

// =============================================================================
// PlannedTask — a single task within the execution plan
// =============================================================================

// PlannedTask 是执行计划中的一条任务。与 SharedTask 不同，
// PlannedTask 是在规划阶段由团队成员协作创建的，包含估算和提议者信息。
type PlannedTask struct {
	// ID 任务稳定标识（格式 plan-task-<hex>）
	ID string `json:"id"`
	// Title 任务标题
	Title string `json:"title"`
	// Description 详细描述
	Description string `json:"description"`
	// ProposedBy 提议此任务的成员名
	ProposedBy string `json:"proposedBy"`
	// DependsOn 依赖的任务 ID 列表
	DependsOn []string `json:"dependsOn,omitempty"`
	// EstimatedComplexity 复杂度估算：low / medium / high
	EstimatedComplexity string `json:"estimatedComplexity,omitempty"`
}

// GenPlanTaskID 生成计划任务 ID。
func GenPlanTaskID() string {
	return "plan-task-" + GenTaskID()[5:] // 复用 task ID 的 hex 部分
}

// =============================================================================
// PlanAssignment — role assignment within the plan
// =============================================================================

// PlanAssignment 记录计划中任务与成员的分配关系。
type PlanAssignment struct {
	// TaskID 被分配的任务 ID
	TaskID string `json:"taskId"`
	// MemberName 负责执行的成员名
	MemberName string `json:"memberName"`
	// Role 成员在该任务中的角色
	Role string `json:"role,omitempty"`
}

// =============================================================================
// ExecutionPlan — the complete execution plan
// =============================================================================

// ExecutionPlan 是团队在 Planning Phase 协作产出的完整执行计划。
//
// 计划一旦被 leader 审批通过（Status=PlanApproved），即成为 Execution Phase
// 的唯一执行依据。团队成员必须严格按照此计划行事，不得超出计划范围。
type ExecutionPlan struct {
	// Objective 团队目标描述（由 leader 在规划开始前设定）
	Objective string `json:"objective"`

	// Tasks 计划中的所有任务
	Tasks []PlannedTask `json:"tasks"`

	// Assignments 任务到成员的分配
	Assignments []PlanAssignment `json:"assignments"`

	// CreatedAt 计划创建时间
	CreatedAt int64 `json:"createdAt"`

	// UpdatedAt 最后更新时间
	UpdatedAt int64 `json:"updatedAt"`

	// SubmittedAt 提交审批时间
	SubmittedAt int64 `json:"submittedAt,omitempty"`

	// ApprovedAt 审批通过时间
	ApprovedAt int64 `json:"approvedAt,omitempty"`

	// ApprovedBy 审批人
	ApprovedBy string `json:"approvedBy,omitempty"`

	// Status 计划审批状态
	Status PlanStatus `json:"status"`

	// RejectionReason leader 驳回计划时的原因说明
	RejectionReason string `json:"rejectionReason,omitempty"`
}

// GenPlanID 生成执行计划 ID。
func GenPlanID() string {
	return "plan-" + GenTaskID()[5:]
}

// Validate 校验执行计划的数据完整性。
//
// 规则：
//  1. Objective 不能为空
//  2. Tasks 至少包含一个任务
//  3. 所有 Assignments 引用的 TaskID 必须在 Tasks 中存在
//  4. 所有 Assignments 引用的 MemberName 不能为空
//  5. 所有 DependsOn 引用的 TaskID 必须在 Tasks 中存在
//  6. 不能有循环依赖
func (p *ExecutionPlan) Validate(memberNames []string) error {
	if p == nil {
		return errors.New("execution plan is nil")
	}
	if p.Objective == "" {
		return errors.New("plan objective is required")
	}
	if len(p.Tasks) == 0 {
		return errors.New("plan must contain at least one task")
	}

	// 构建 task ID 集合
	taskSet := make(map[string]bool, len(p.Tasks))
	for _, t := range p.Tasks {
		if t.ID == "" {
			return errors.New("task has empty ID")
		}
		if t.Title == "" {
			return fmt.Errorf("task %s has empty title", t.ID)
		}
		taskSet[t.ID] = true
	}

	// 校验 Assignments
	seenMemberTasks := make(map[string]int) // memberName -> count
	for _, a := range p.Assignments {
		if a.MemberName == "" {
			return errors.New("assignment has empty MemberName")
		}
		if !taskSet[a.TaskID] {
			return fmt.Errorf("assignment references unknown task %q", a.TaskID)
		}
		seenMemberTasks[a.MemberName]++
	}

	// 校验所有被分配成员在 memberNames 中
	if len(memberNames) > 0 {
		memberSet := make(map[string]bool, len(memberNames))
		for _, n := range memberNames {
			memberSet[n] = true
		}
		for _, a := range p.Assignments {
			if !memberSet[a.MemberName] {
				return fmt.Errorf("assignment references unknown member %q", a.MemberName)
			}
		}
	}

	// 校验依赖引用
	for _, t := range p.Tasks {
		for _, depID := range t.DependsOn {
			if !taskSet[depID] {
				return fmt.Errorf("task %s depends on unknown task %q", t.ID, depID)
			}
		}
	}

	// 检查循环依赖
	if err := p.checkCircularDeps(); err != nil {
		return err
	}

	return nil
}

// checkCircularDeps 用 DFS 检测计划中的循环依赖。
func (p *ExecutionPlan) checkCircularDeps() error {
	// 构建邻接表
	adj := make(map[string][]string, len(p.Tasks))
	for _, t := range p.Tasks {
		adj[t.ID] = t.DependsOn
	}

	// 对每个节点做 DFS
	visited := make(map[string]int) // 0=未访问, 1=访问中, 2=已完成

	var dfs func(id string) error
	dfs = func(id string) error {
		switch visited[id] {
		case 1:
			return fmt.Errorf("circular dependency detected involving task %q", id)
		case 2:
			return nil
		}
		visited[id] = 1
		for _, dep := range adj[id] {
			if err := dfs(dep); err != nil {
				return err
			}
		}
		visited[id] = 2
		return nil
	}

	for _, t := range p.Tasks {
		if err := dfs(t.ID); err != nil {
			return err
		}
	}
	return nil
}

// HasCyclicDependency 公开的循环依赖检查接口。
func (p *ExecutionPlan) HasCyclicDependency() bool {
	return p.checkCircularDeps() != nil
}

// ToSharedTasks 将计划任务转换为共享任务列表（用于导出到 File.Tasks）。
func (p *ExecutionPlan) ToSharedTasks() []SharedTask {
	now := time.Now().UnixMilli()
	tasks := make([]SharedTask, 0, len(p.Tasks))
	for _, pt := range p.Tasks {
		tasks = append(tasks, SharedTask{
			ID:          pt.ID,
			Title:       pt.Title,
			Description: pt.Description,
			Status:      SharedTaskPending,
			DependsOn:   pt.DependsOn,
			CreatedAt:   now,
			UpdatedAt:   now,
		})
	}
	return tasks
}

// =============================================================================
// Planning Messages — protocol between leader and members during planning
// =============================================================================

// PlanProposal 成员提交的任务提案。
type PlanProposal struct {
	// Proposer 提案人
	Proposer string `json:"proposer"`
	// Tasks 提案人建议的任务列表
	Tasks []PlannedTask `json:"tasks"`
	// Rationale 提案理由
	Rationale string `json:"rationale,omitempty"`
	// SubmittedAt 提交时间
	SubmittedAt int64 `json:"submittedAt"`
}

// =============================================================================
// Plan Validation Summary
// =============================================================================

// PlanValidationSummary 计划验证结果摘要。
type PlanValidationSummary struct {
	Valid           bool     `json:"valid"`
	TotalTasks      int      `json:"totalTasks"`
	TotalAssignments int     `json:"totalAssignments"`
	UnassignedTasks []string `json:"unassignedTasks,omitempty"`
	Errors          []string `json:"errors,omitempty"`
	Warnings        []string `json:"warnings,omitempty"`
}

// SummarizeValidation 生成验证摘要。
func SummarizeValidation(plan *ExecutionPlan, memberNames []string) PlanValidationSummary {
	summary := PlanValidationSummary{
		Valid:            true,
		TotalTasks:       len(plan.Tasks),
		TotalAssignments: len(plan.Assignments),
	}

	if plan == nil {
		summary.Valid = false
		summary.Errors = append(summary.Errors, "plan is nil")
		return summary
	}

	// 检查未分配任务
	assigned := make(map[string]bool)
	for _, a := range plan.Assignments {
		assigned[a.TaskID] = true
	}
	for _, t := range plan.Tasks {
		if !assigned[t.ID] {
			summary.UnassignedTasks = append(summary.UnassignedTasks, t.ID)
		}
	}
	if len(summary.UnassignedTasks) > 0 {
		summary.Warnings = append(summary.Warnings,
			fmt.Sprintf("%d task(s) unassigned", len(summary.UnassignedTasks)))
	}

	// 全面校验
	if err := plan.Validate(memberNames); err != nil {
		summary.Valid = false
		summary.Errors = append(summary.Errors, err.Error())
	}

	return summary
}

// =============================================================================
// ReplanContext — context for re-planning after failure
// =============================================================================

// ReplanContext 包含失败后重新规划所需的上下文。
type ReplanContext struct {
	// FailedTaskID 失败的任务 ID
	FailedTaskID string `json:"failedTaskId"`
	// FailedMember 执行失败的成员名
	FailedMember string `json:"failedMember"`
	// FailureReason 失败原因
	FailureReason string `json:"failureReason"`
	// CompletedTasks 在失败前已完成的任务 ID 列表
	CompletedTasks []string `json:"completedTasks,omitempty"`
	// PendingTasks 尚未开始的任务 ID 列表
	PendingTasks []string `json:"pendingTasks,omitempty"`
	// ReplanAttempt 当前是第几次 re-plan（从 1 开始）
	ReplanAttempt int `json:"replanAttempt"`
	// MaxReplanAttempts 最大允许的 re-plan 次数
	MaxReplanAttempts int `json:"maxReplanAttempts"`
}
