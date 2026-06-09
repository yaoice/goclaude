package application

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/anthropics/goclaude/pkg/domain/team"
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
//   run() → poll inbox → 收到 task_assign → executeTask() → report → poll …
//            ↑                                                  │
//            └──────────────────────────────────────────────────┘
//
// 收到 shutdown_request 时发送 shutdown_response 并退出。
type memberWorker struct {
	teamName   string
	memberName string
	agentType  string
	model      string

	teamSvc *TeamService
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

		default:
			w.logger.Debug("ignored message type",
				"from", msg.From,
				"type", string(msg.Type),
			)
		}
	}
}

// handleTaskAssign 处理 leader 下发的任务。
//
// 流程：
//  1. 登记 MemberWorking
//  2. 构建独立 Engine，执行子任务
//  3. 发送 task_result 给 leader
//  4. 恢复 MemberIdle
func (w *memberWorker) handleTaskAssign(msg team.Message) {
	w.logger.Info("received task",
		"task_id", msg.TaskID,
		"from", msg.From,
		"summary", msg.Summary,
	)

	w.setStatus(MemberWorking)

	// 构建执行上下文
	description := msg.Text
	if description == "" {
		description = msg.Summary
	}
	taskPrompt := fmt.Sprintf(
		"=== TASK ===\nTask ID: %s\nFrom: %s\nSubject: %s\n\nDescription:\n%s\n\n"+
			"Please execute this task now. You have access to file tools, bash, and team communication tools. "+
			"When you complete the task, your output will be sent back to the team leader automatically.",
		msg.TaskID, msg.From, msg.Summary, description,
	)

	// 获取 Definition 用于构建 Engine（优先用 worker 自身 agentType，回退到 team-worker）
	agentType := w.agentType
	def, ok := w.agentSvc.registry.Get(agentType)
	if !ok {
		w.logger.Warn("agent type not found, falling back to team-worker",
			"requested", agentType,
		)
		agentType = "team-worker"
		def, ok = w.agentSvc.registry.Get(agentType)
	}
	if !ok {
		w.logger.Error("team-worker agent not found", "agent_type", agentType)
		w.reportTaskFailure(msg.TaskID, fmt.Sprintf("agent type %q not found", w.agentType))
		w.setStatus(MemberIdle)
		return
	}

	// 执行子任务（用 recover 防止 provider 为 nil 时 panic）
	var result *RunResult
	var runErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				runErr = fmt.Errorf("engine panic: %v", r)
			}
		}()
		// 确定 MaxTurns 优先级：member worker 配置 > agent 定义 > 引擎默认
		maxTurns := w.maxTurns
		if maxTurns <= 0 {
			maxTurns = def.MaxTurns
		}

		result, runErr = w.agentSvc.Run(w.ctx, agentType, w.factory, RunOptions{
			Prompt:          taskPrompt,
			ParentSessionID: fmt.Sprintf("%s-%s", w.teamName, w.memberName),
			WorkingDir:      w.workingDir,
			ProjectRoot:     w.projectRoot,
			WorkspaceRoot:   w.workspaceRoot,
			DefaultModel:    w.model,
			MaxTurns:        maxTurns,
		})
	}()

	if runErr != nil {
		w.logger.Warn("task execution failed",
			"task_id", msg.TaskID,
			"error", runErr,
		)
		w.reportTaskFailure(msg.TaskID, runErr.Error())
		w.setStatus(MemberIdle)
		return
	}

	w.logger.Info("task completed",
		"task_id", msg.TaskID,
		"turns", result.TurnCount,
		"stop_reason", string(result.StopReason),
	)

	// 汇报成功
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

	// 发送关闭确认
	shutdownMsg := team.NewShutdownResponse(w.memberName, reqID, true, "acknowledged")
	_, err := w.teamSvc.Send(SendInput{
		TeamName:   w.teamName,
		From:       w.memberName,
		To:         msg.From,
		Structured: &shutdownMsg,
	})
	if err != nil {
		w.logger.Warn("send shutdown_response failed", "error", err)
	}
}

// handlePeerMessage 处理来自其他 worker 的普通消息。
//
// 当前 worker 处于 idle 时，启动一个 mini Engine 对话处理该消息；
// 如果正在忙，消息已被 ReadInbox(drain=true) 消费，记录日志由上层决定是否重投。
func (w *memberWorker) handlePeerMessage(msg team.Message) {
	w.logger.Info("received peer message",
		"from", msg.From,
		"summary", msg.Summary,
	)

	currentStatus := w.getStatus()
	if currentStatus != MemberIdle {
		w.logger.Debug("busy; peer message logged but not processed immediately",
			"from", msg.From,
			"current_status", currentStatus,
		)
		return
	}

	w.setStatus(MemberWorking)

	prompt := fmt.Sprintf(
		"=== MESSAGE FROM %s ===\nSubject: %s\n\n%s\n\n"+
			"Please respond to this message. Use send_message(to=%q, ...) to reply if needed. "+
			"After replying, you can continue with any tasks you are assigned.",
		msg.From, msg.Summary, msg.Text, msg.From,
	)

	// 确定 agentType（优先用 worker 自身的，回退到 team-worker）
	agentType := w.agentType
	if _, ok := w.agentSvc.registry.Get(agentType); !ok {
		agentType = "team-worker"
	}

	result, err := w.agentSvc.Run(w.ctx, agentType, w.factory, RunOptions{
		Prompt:          prompt,
		ParentSessionID: fmt.Sprintf("%s-%s", w.teamName, w.memberName),
		WorkingDir:      w.workingDir,
		ProjectRoot:     w.projectRoot,
		WorkspaceRoot:   w.workspaceRoot,
		DefaultModel:    w.model,
		MaxTurns:        10, // peer 消息对话限制较短轮数
	})

	if err != nil {
		w.logger.Warn("peer message handling failed", "from", msg.From, "error", err)
	} else {
		w.logger.Info("peer message handled",
			"from", msg.From,
			"turns", result.TurnCount,
		)
		_ = result // 结果中的 final text 是给模型的；send_message 已经由 Engine 发出
	}

	w.setStatus(MemberIdle)
}

// reportTaskSuccess 向 leader 汇报任务成功。
func (w *memberWorker) reportTaskSuccess(taskID, output string) {
	_, err := w.teamSvc.ReportTask(ReportTaskInput{
		TeamName: w.teamName,
		From:     w.memberName,
		TaskID:   taskID,
		Status:   team.TaskResolved,
		Summary:  fmt.Sprintf("task %s completed", taskID),
		Detail:   output,
	})
	if err != nil {
		w.logger.Warn("report task success failed", "task_id", taskID, "error", err)
	}
}

// reportTaskFailure 向 leader 汇报任务失败。
func (w *memberWorker) reportTaskFailure(taskID, errMsg string) {
	_, err := w.teamSvc.ReportTask(ReportTaskInput{
		TeamName: w.teamName,
		From:     w.memberName,
		TaskID:   taskID,
		Status:   team.TaskFailed,
		Summary:  fmt.Sprintf("task %s failed", taskID),
		Detail:   errMsg,
	})
	if err != nil {
		w.logger.Warn("report task failure failed", "task_id", taskID, "error", err)
	}
}

// cleanup 清理 member 在 team 中的状态。
func (w *memberWorker) cleanup() {
	// 标记为 inactive
	if err := w.teamSvc.SetMemberActive(w.teamName, w.memberName, false); err != nil {
		w.logger.Warn("cleanup: SetMemberActive failed", "error", err)
	}
	w.logger.Info("team member worker cleaned up")
}
