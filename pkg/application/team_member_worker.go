package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/yaoice/goclaude/pkg/domain/team"
)

// MemberWorkerStatus 表示 team member goroutine 的当前状态。
type MemberWorkerStatus string

const (
	MemberIdle    MemberWorkerStatus = "idle"
	MemberWorking MemberWorkerStatus = "working"
	MemberBlocked MemberWorkerStatus = "blocked"
	MemberDone    MemberWorkerStatus = "done"
)

// memberWorker 是一个在独立 goroutine 中运行的 team member。
//
// 生命周期：
//
//	run() → poll inbox → 收到 task_assign → executeTask() → report → poll …
//	         ↑                                                  │
//	         └──────────────────────────────────────────────────┘
//
// 收到 shutdown_request 时发送 shutdown_response 并退出。
type memberWorker struct {
	teamName   string
	memberName string
	agentType  string
	model      string

	teamSvc  *TeamService
	agentSvc *AgentService
	factory  AgentEngineFactory
	logger   *slog.Logger

	mu     sync.Mutex
	status MemberWorkerStatus

	// 生命周期控制
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	// pollInterval inbox 轮询间隔
	pollInterval time.Duration
	// taskTimeout 单个任务执行超时
	taskTimeout time.Duration
	// maxTurns 单个任务最大轮数（0 表示用 agent 定义或引擎默认值）
	maxTurns int

	// 产物输出路径
	workspaceRoot string // team 共享 workspace 根目录
	projectRoot   string // 项目根目录
	workingDir    string // 工作目录
}

// newMemberWorker 创建一个 memberWorker；不启动 goroutine。
func newMemberWorker(
	teamName, memberName, agentType, model string,
	teamSvc *TeamService,
	agentSvc *AgentService,
	factory AgentEngineFactory,
	logger *slog.Logger,
) *memberWorker {
	ctx, cancel := context.WithCancel(context.Background())
	return &memberWorker{
		teamName:      teamName,
		memberName:    memberName,
		agentType:     agentType,
		model:         model,
		teamSvc:       teamSvc,
		agentSvc:      agentSvc,
		factory:       factory,
		logger:        logger.With(slog.String("team", teamName), slog.String("member", memberName)),
		ctx:           ctx,
		cancel:        cancel,
		done:          make(chan struct{}),
		status:        MemberIdle,
		pollInterval:  5 * time.Second,
		taskTimeout:   5 * time.Minute,
		workspaceRoot: "",
		projectRoot:   "",
		workingDir:    "",
	}
}

// start 在独立 goroutine 中启动 member 主循环。
func (w *memberWorker) start() {
	go w.run()
}

// stop 发送取消信号并等待 goroutine 退出（带超时）。
func (w *memberWorker) stop(timeout time.Duration) error {
	w.cancel()
	select {
	case <-w.done:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("member %s/%s did not exit within %v", w.teamName, w.memberName, timeout)
	}
}

// getStatus 返回当前状态（线程安全）。
func (w *memberWorker) getStatus() MemberWorkerStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

// setStatus 更新内部状态并同步到 team 文件。
func (w *memberWorker) setStatus(s MemberWorkerStatus) {
	w.mu.Lock()
	w.status = s
	w.mu.Unlock()

	// 同步到 team 文件
	teamStatus := team.StatusWorking
	switch s {
	case MemberIdle:
		teamStatus = team.StatusIdle
	case MemberWorking:
		teamStatus = team.StatusWorking
	case MemberBlocked:
		teamStatus = team.StatusBlocked
	case MemberDone:
		teamStatus = team.StatusDone
	}
	if err := w.teamSvc.SetMemberStatus(w.teamName, w.memberName, teamStatus); err != nil {
		w.logger.Warn("set member status failed", "status", s, "error", err)
	}
}

// heartbeat 刷新成员心跳时间戳。
func (w *memberWorker) heartbeat() {
	if err := w.teamSvc.Heartbeat(w.teamName, w.memberName); err != nil {
		// 团队或成员已被删除 → 自行退出
		if errors.Is(err, ErrTeamNotFound) || errors.Is(err, ErrMemberNotFound) {
			w.logger.Debug("team gone, shutting down worker")
			w.cancel()
			return
		}
		w.logger.Warn("heartbeat failed", "error", err)
	}
}

// run 是 member 的主循环，运行在独立 goroutine 中。
func (w *memberWorker) run() {
	defer close(w.done)

	w.logger.Info("team member worker started",
		"agent_type", w.agentType,
		"model", w.model,
	)

	// 1. 确保已加入 team（幂等）
	if err := w.ensureJoined(); err != nil {
		w.logger.Warn("join team failed, continuing", "error", err)
	}

	// 2. 登记初始状态
	w.setStatus(MemberIdle)
	w.heartbeat()

	// 3. 主循环：轮询 inbox
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			w.logger.Info("team member worker shutting down")
			w.setStatus(MemberDone)
			w.cleanup()
			return

		case <-ticker.C:
			w.heartbeat()
			w.processInbox()
		}
	}
}

// ensureJoined 确保 member 在 team 中有记录（幂等）。
func (w *memberWorker) ensureJoined() error {
	_, _, err := w.teamSvc.JoinTeam(JoinTeamInput{
		TeamName:  w.teamName,
		AgentName: w.memberName,
		AgentType: w.agentType,
	})
	if err != nil {
		// 如果 team 不存在或已加入，记录但不阻塞
		return err
	}
	return nil
}

// processInbox 检查并处理 inbox 中的消息。
func (w *memberWorker) processInbox() {
	msgs, err := w.teamSvc.ReadInbox(w.teamName, w.memberName, true) // drain=true
	if err != nil {
		// 团队或成员已被删除 → 自行退出
		if errors.Is(err, ErrTeamNotFound) || errors.Is(err, ErrMemberNotFound) {
			w.logger.Debug("team gone, shutting down worker")
			w.cancel()
			return
		}
		w.logger.Warn("read inbox failed", "error", err)
		return
	}
	if len(msgs) == 0 {
		return
	}

	for _, msg := range msgs {
		// 检查是否已收到退出信号
		select {
		case <-w.ctx.Done():
			return
		default:
		}

		switch msg.Type {
		case team.MessageTaskAssign:
			// Plan-then-Execute gate: 仅在 Executing Phase 允许执行
			if !w.isExecutionPhase() {
				w.logger.Debug("task_assign received but team is not in executing phase, skipping",
					"task_id", msg.TaskID,
					"team", w.teamName,
				)
				w.sendRejection(msg, "team is not in executing phase; plan must be approved first")
				continue
			}
			w.handleTaskAssign(msg)

		case team.MessageShutdownReq:
			w.handleShutdownRequest(msg)
			return // 退出主循环

		case team.MessageText, team.MessageBroadcast:
			w.handlePeerMessage(msg)

		case team.MessageTaskResult:
			// 来自其他 worker 的任务结果，可作为参考但不自动触发动作
			w.logger.Debug("received task_result from peer",
				"from", msg.From,
				"task_id", msg.TaskID,
				"status", string(msg.TaskStatus),
			)

		// Planning Phase messages
		case team.MessagePlanConsolidate:
			w.handlePlanConsolidate(msg)

		case team.MessagePlanFeedback:
			w.handlePlanFeedback(msg)

		case team.MessagePlanPropose:
			w.logger.Debug("received plan_propose from peer",
				"from", msg.From,
				"task_id", msg.TaskID,
			)

		default:
			w.logger.Debug("ignored message type",
				"from", msg.From,
				"type", string(msg.Type),
			)
		}
	}
}

// resolveAgentType 返回 worker 应使用的 agentType。
//
// 优先级：worker 自身 agentType → 回退到 "team-worker"。
// 返回空字符串表示不可恢复错误（team-worker 也不存在）。
func (w *memberWorker) resolveAgentType() (string, bool) {
	if _, ok := w.agentSvc.registry.Get(w.agentType); ok {
		return w.agentType, true
	}
	w.logger.Warn("agent type not found, falling back to team-worker", "requested", w.agentType)
	if _, ok := w.agentSvc.registry.Get("team-worker"); ok {
		return "team-worker", true
	}
	return "", false
}

// runSubagent 带 panic recover 的 agentSvc.Run 薄封装，用于 worker 内部子任务执行。
func (w *memberWorker) runSubagent(agentType string, maxTurns int, prompt string) (result *RunResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("engine panic: %v", r)
		}
	}()
	return w.agentSvc.Run(w.ctx, agentType, w.factory, RunOptions{
		Prompt:          prompt,
		ParentSessionID: fmt.Sprintf("%s-%s", w.teamName, w.memberName),
		WorkingDir:      w.workingDir,
		ProjectRoot:     w.projectRoot,
		WorkspaceRoot:   w.workspaceRoot,
		DefaultModel:    w.model,
		MaxTurns:        maxTurns,
	})
}

// handleTaskAssign 处理 leader 下发的任务。
//
// 流程：
//  1. 登记 MemberWorking → 2. 构建独立 Engine 执行子任务
//  3. 发送 task_result 给 leader → 4. 恢复 MemberIdle
func (w *memberWorker) handleTaskAssign(msg team.Message) {
	w.logger.Info("received task", "task_id", msg.TaskID, "from", msg.From, "summary", msg.Summary)
	w.setStatus(MemberWorking)

	agentType, ok := w.resolveAgentType()
	if !ok {
		w.logger.Error("team-worker agent not found")
		w.reportTaskFailure(msg.TaskID, fmt.Sprintf("agent type %q not found", w.agentType))
		w.setStatus(MemberIdle)
		return
	}

	// 构造任务 prompt；description 为空时回退到 summary
	desc := msg.Text
	if desc == "" {
		desc = msg.Summary
	}
	taskPrompt := fmt.Sprintf(
		"%s\n\n=== TASK ===\nTask ID: %s\nFrom: %s\nSubject: %s\n\nDescription:\n%s\n\n"+
			"Please execute this task now. You have access to file tools, bash, and team communication tools. "+
			"When you complete the task, your output will be sent back to the team leader automatically.",
		w.buildContext(),
		msg.TaskID, msg.From, msg.Summary, desc,
	)

	// 确定 MaxTurns 优先级：worker 配置 > agent 定义 > 引擎默认
	maxTurns := w.maxTurns
	if maxTurns <= 0 {
		if def, ok := w.agentSvc.registry.Get(agentType); ok {
			maxTurns = def.MaxTurns
		}
	}

	result, runErr := w.runSubagent(agentType, maxTurns, taskPrompt)
	if runErr != nil {
		w.logger.Warn("task execution failed", "task_id", msg.TaskID, "error", runErr)
		w.reportTaskFailure(msg.TaskID, runErr.Error())
		w.setStatus(MemberIdle)
		return
	}

	w.logger.Info("task completed", "task_id", msg.TaskID, "turns", result.TurnCount, "stop_reason", string(result.StopReason))
	w.reportTaskSuccess(msg.TaskID, result.FinalText)
	w.setStatus(MemberIdle)
}

// handleShutdownRequest 处理关闭请求。
func (w *memberWorker) handleShutdownRequest(msg team.Message) {
	w.logger.Info("received shutdown request", "from", msg.From, "request_id", msg.RequestID)

	reqID := msg.RequestID
	if reqID == "" {
		reqID = fmt.Sprintf("shutdown-%d", time.Now().UnixNano())
	}

	shutdownMsg := team.NewShutdownResponse(w.memberName, reqID, true, "acknowledged")
	if _, err := w.teamSvc.Send(SendInput{
		TeamName:   w.teamName,
		From:       w.memberName,
		To:         msg.From,
		Structured: &shutdownMsg,
	}); err != nil {
		w.logger.Warn("send shutdown_response failed", "error", err)
	}
}

// handlePeerMessage 处理来自其他 worker 的普通消息。
//
// 仅在 idle 状态下启动 mini Engine 处理；正在忙时记录日志跳过。
// 注入 team context，解决 worker 不知自己所在 team/role/objective 的问题。
func (w *memberWorker) handlePeerMessage(msg team.Message) {
	w.logger.Info("received peer message", "from", msg.From, "summary", msg.Summary)

	if w.getStatus() != MemberIdle {
		w.logger.Debug("busy; peer message skipped", "from", msg.From, "current_status", w.getStatus())
		return
	}

	w.setStatus(MemberWorking)
	agentType, ok := w.resolveAgentType()
	if !ok {
		w.setStatus(MemberIdle)
		return
	}

	// 注入 team context，让 worker 知道自己是谁、在哪个 team、目标是什么
	ctxBlock := w.buildContext()

	prompt := fmt.Sprintf(
		`%s

=== MESSAGE FROM %s ===
Subject: %s

%s

=== YOUR TASK ===
Please respond to this message. Use send_message(to=%q, ...) to reply if needed.

IMPORTANT: If this is a planning-related message, review the team context above.
- You are in the %s phase — use collect_proposal to submit task proposals.
- DO NOT execute any tasks in Planning Phase without explicit task_assign.
- DO NOT explore the filesystem looking for team info — all context is provided above.`,
		ctxBlock, msg.From, msg.Summary, msg.Text, msg.From, w.teamPhase(),
	)

	// 规划阶段需要足够轮次让 worker 理解上下文并生成提案
	maxTurns := 30
	if w.maxTurns > 0 && w.maxTurns < maxTurns {
		maxTurns = w.maxTurns
	}

	result, err := w.runSubagent(agentType, maxTurns, prompt)
	if err != nil {
		w.logger.Warn("peer message handling failed", "from", msg.From, "error", err)
	} else {
		w.logger.Info("peer message handled", "from", msg.From, "turns", result.TurnCount)
		_ = result // send_message 已由 Engine 发出，final text 仅给模型消费
	}
	w.setStatus(MemberIdle)
}

// reportTaskSuccess 向 leader 汇报任务成功。
func (w *memberWorker) reportTaskSuccess(taskID, output string) {
	w.reportTask(team.TaskResolved, taskID, output)
}

// reportTaskFailure 向 leader 汇报任务失败。
func (w *memberWorker) reportTaskFailure(taskID, errMsg string) {
	w.reportTask(team.TaskFailed, taskID, errMsg)
}

// reportTask 统一的任务结果汇报逻辑。
func (w *memberWorker) reportTask(status team.TaskStatus, taskID, detail string) {
	summary := fmt.Sprintf("task %s %s", taskID, status)
	if _, err := w.teamSvc.ReportTask(ReportTaskInput{
		TeamName: w.teamName,
		From:     w.memberName,
		TaskID:   taskID,
		Status:   status,
		Summary:  summary,
		Detail:   detail,
	}); err != nil {
		w.logger.Warn("report task failed", "task_id", taskID, "status", status, "error", err)
	}
}

// isExecutionPhase 检查团队当前是否处于执行阶段。
// 所有 task execution 必须通过此门控。
func (w *memberWorker) isExecutionPhase() bool {
	allowed, err := w.teamSvc.IsExecutionAllowed(w.teamName)
	if err != nil {
		w.logger.Warn("phase check failed", "error", err)
		return false
	}
	return allowed
}

// sendRejection 向 leader 发送任务拒绝通知。
func (w *memberWorker) sendRejection(msg team.Message, reason string) {
	if _, err := w.teamSvc.ReportTask(ReportTaskInput{
		TeamName: w.teamName,
		From:     w.memberName,
		TaskID:   msg.TaskID,
		Status:   team.TaskFailed,
		Summary:  fmt.Sprintf("task %s rejected", msg.TaskID),
		Detail:   reason,
	}); err != nil {
		w.logger.Warn("send rejection failed", "error", err)
	}
}

// handlePlanConsolidate 处理 leader 分发的合并计划。
//
// Worker 应：
//  1. 评审计划（验证自己的任务是否可行）
//  2. 如需调整，通过 send_message(Type=plan_propose) 提交修订
//  3. 如果同意，设置 status=done 告知 leader 已阅读
func (w *memberWorker) handlePlanConsolidate(msg team.Message) {
	w.logger.Info("received consolidated plan from leader", "from", msg.From)

	if w.getStatus() != MemberIdle {
		w.logger.Debug("busy; plan_consolidate deferred for later review")
		return
	}

	w.setStatus(MemberWorking)
	defer w.setStatus(MemberIdle)

	agentType, ok := w.resolveAgentType()
	if !ok {
		return
	}

	planPrompt := fmt.Sprintf(
		`%s

=== PLAN REVIEW ===
The team leader has shared a consolidated execution plan for your review.

Plan:
%s

=== YOUR TASK ===
1. Review the plan carefully. Identify tasks assigned to you (%s).
2. Assess feasibility: Do you have the right tools? Are estimates realistic?
3. If you agree with the plan: send a brief acknowledgment back to the leader via send_message.
4. If you have concerns: submit revised proposals via send_message with type="plan_propose"
   containing your suggested changes.

DO NOT start executing any tasks yet. The team is still in the Planning Phase.
Your output will be reviewed before execution begins.`,
		w.buildContext(), msg.PlanText, w.memberName,
	)

	result, err := w.runSubagent(agentType, 30, planPrompt)
	if err != nil {
		w.logger.Warn("plan review failed", "error", err)
		return
	}
	w.logger.Info("plan review completed", "turns", result.TurnCount)
}

// handlePlanFeedback 处理 leader 的反馈（包括驳回通知、re-plan 通知）。
func (w *memberWorker) handlePlanFeedback(msg team.Message) {
	w.logger.Info("received plan feedback from leader", "from", msg.From)

	if w.getStatus() != MemberIdle {
		w.logger.Debug("busy; plan_feedback deferred for later review")
		return
	}

	w.setStatus(MemberWorking)
	defer w.setStatus(MemberIdle)

	agentType, ok := w.resolveAgentType()
	if !ok {
		return
	}

	// 检查是否包含 re-plan 信息
	isReplan := strings.Contains(msg.Text, "REPLAN REQUEST")

	actionType := "revise the plan"
	if isReplan {
		actionType = "help re-plan (a task has failed during execution)"
	}

	feedbackPrompt := fmt.Sprintf(
		`%s

=== PLAN FEEDBACK ===
The team leader has sent feedback about the execution plan.

Feedback:
%s

=== YOUR TASK ===
This is %s notification. 

If it's a rejection:
- Review the rejection reason and address the concerns.
- Submit updated proposals via send_message(type=plan_propose) with your revised tasks.
- Mark your status as done when you've updated your proposals.

If it's a re-plan request:
- A task has failed during execution. 
- Review the failure reason and completed/pending task context.
- Propose alternative approaches or task adjustments via send_message(type=plan_propose).
- Consider if the failed task should be decomposed differently.

DO NOT execute any tasks yet — we are returning to the Planning Phase.`,
		w.buildContext(),
		msg.Text, actionType,
	)

	result, err := w.runSubagent(agentType, 30, feedbackPrompt)
	if err != nil {
		w.logger.Warn("plan feedback handling failed", "error", err)
		return
	}
	w.logger.Info("plan feedback handled", "turns", result.TurnCount)
}

// buildContext 为 worker 构建 team/phase/project 上下文字符串。
//
// 解决 worker 在 Planning Phase 中不知自己所在 team、role、objective 的问题。
// 每次调用时实时读取 team 文件，保证信息最新。
func (w *memberWorker) buildContext() string {
	f, err := w.teamSvc.GetTeam(w.teamName)
	if err != nil || f == nil {
		return fmt.Sprintf("Team: %s\nMember: %s\nRole: %s",
			w.teamName, w.memberName, w.agentType)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== TEAM CONTEXT ===\n"))
	sb.WriteString(fmt.Sprintf("Team: %s\n", w.teamName))
	sb.WriteString(fmt.Sprintf("Your Name: %s\n", w.memberName))
	sb.WriteString(fmt.Sprintf("Your Role: %s\n", w.agentType))
	sb.WriteString(fmt.Sprintf("Current Phase: %s\n\n", f.Phase))

	if f.Plan != nil && f.Plan.Objective != "" {
		sb.WriteString(fmt.Sprintf("=== TEAM OBJECTIVE ===\n%s\n\n", f.Plan.Objective))
	}

	sb.WriteString("=== TEAM MEMBERS ===\n")
	for _, m := range f.Members {
		sb.WriteString(fmt.Sprintf("  - %s (%s) [status=%s]\n", m.Name, m.AgentType, m.Status))
	}

	if w.workspaceRoot != "" {
		sb.WriteString(fmt.Sprintf("\n=== WORKSPACE ===\n%s\n", w.workspaceRoot))
	}

	sb.WriteString("\nYou are a team worker in a multi-agent team. ")
	sb.WriteString("Use send_message to communicate with other members. ")
	sb.WriteString("Use collect_proposal to submit your task proposals during Planning Phase.\n")

	return sb.String()
}

// teamPhase 返回当前 team 的 phase 字符串（带容错）。
func (w *memberWorker) teamPhase() string {
	f, err := w.teamSvc.GetTeam(w.teamName)
	if err != nil || f == nil {
		return "unknown"
	}
	return string(f.Phase)
}

// cleanup 清理 member 在 team 中的状态。
func (w *memberWorker) cleanup() {
	// 标记为 inactive
	if err := w.teamSvc.SetMemberActive(w.teamName, w.memberName, false); err != nil {
		w.logger.Warn("cleanup: SetMemberActive failed", "error", err)
	}
	w.logger.Info("team member worker cleaned up")
}
