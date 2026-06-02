// Package tools - team_tools_advanced 提供 5 个进阶 team 工具：
//
//   - AssignTaskTool       leader → worker 派发任务（task_assign 协议消息）
//   - ReportTaskTool       worker → leader 汇报任务结果（task_result）
//   - WaitForMessageTool   阻塞等待 inbox 出现未读，避免空转轮询
//   - SetMemberStatusTool  显式设置自身 / 同伴的 idle/working/blocked/error/done 状态
//   - HeartbeatTool        刷新成员的 LastHeartbeatAt，证明"还活着"
//
// 这些工具与 team_tools.go 的 5 个基础工具配合，实现"任务分配 + 状态同步"
// 完整闭环。所有工具都是 IsConcurrencySafe=true、走 PermissionAllow，与 src
// agent-teams 的协议工具一致。
package tools

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/anthropics/goclaude/internal/application"
	"github.com/anthropics/goclaude/internal/domain/team"
	"github.com/anthropics/goclaude/internal/domain/tool"
)

// ----- AssignTaskTool -----

// AssignTaskTool 让 leader（或任意成员）向另一个成员派发结构化任务。
//
// 对齐 src `task_assignment` 消息协议：携带 taskId/subject/description 与
// 发送方 agent 名，由 worker 端 read_inbox 解析后落到本地任务系统。
type AssignTaskTool struct{ teamToolBase }

// NewAssignTaskTool 构造。
func NewAssignTaskTool(svc *application.TeamService, defaultTeam, defaultFrom string) *AssignTaskTool {
	return &AssignTaskTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*AssignTaskTool) Name() string      { return "assign_task" }
func (*AssignTaskTool) Aliases() []string { return []string{"AssignTask"} }
func (*AssignTaskTool) Description() string {
	return "Assign a structured task to a teammate (delivers a task_assign message). Returns the task_id (auto-generated if omitted) and the message_id for follow-up."
}
func (*AssignTaskTool) IsEnabled() bool                     { return true }
func (*AssignTaskTool) IsReadOnly(_ tool.Input) bool        { return false }
func (*AssignTaskTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *AssignTaskTool) Prompt() string                    { return t.Description() }

func (t *AssignTaskTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required (or join a team first)")
	}
	if t.from(input) == "" {
		return errors.New("from is required")
	}
	if input.GetString("to") == "" {
		return errors.New("to is required")
	}
	if input.GetString("subject") == "" {
		return errors.New("subject is required (5-10 word task title)")
	}
	return nil
}

func (*AssignTaskTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name":   map[string]interface{}{"type": "string"},
			"from":        map[string]interface{}{"type": "string"},
			"to":          map[string]interface{}{"type": "string", "description": "Worker agent name."},
			"task_id":     map[string]interface{}{"type": "string", "description": "Stable task id; auto-generated if omitted."},
			"subject":     map[string]interface{}{"type": "string", "description": "5-10 word task title."},
			"description": map[string]interface{}{"type": "string", "description": "Full task body / requirements."},
		},
		"required": []string{"to", "subject"},
	}
}

func (*AssignTaskTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *AssignTaskTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}
	taskID := input.GetString("task_id")
	if taskID == "" {
		taskID = "task-" + genRequestID()
	}
	res, err := t.service.AssignTask(application.AssignTaskInput{
		TeamName:    t.team(input),
		From:        t.from(input),
		To:          input.GetString("to"),
		TaskID:      taskID,
		Subject:     input.GetString("subject"),
		Description: input.GetString("description"),
	})
	if err != nil {
		if errors.Is(err, application.ErrTeamNotFound) || errors.Is(err, application.ErrMemberNotFound) {
			return tool.NewErrorResult(err.Error()), nil
		}
		return tool.NewErrorResult("assign_task: " + err.Error()), nil
	}
	return tool.NewResult(jsonOut(map[string]interface{}{
		"success":    true,
		"team_name":  t.team(input),
		"task_id":    taskID,
		"message_id": res.MessageID,
		"recipients": res.Recipients,
	})), nil
}

// ----- ReportTaskTool -----

// ReportTaskTool 让 worker 汇报任务进展或最终结果。
//
// 对齐 src `task_result` 协议消息（accepted/working/blocked/resolved/failed）。
type ReportTaskTool struct{ teamToolBase }

func NewReportTaskTool(svc *application.TeamService, defaultTeam, defaultFrom string) *ReportTaskTool {
	return &ReportTaskTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*ReportTaskTool) Name() string      { return "report_task" }
func (*ReportTaskTool) Aliases() []string { return []string{"ReportTask"} }
func (*ReportTaskTool) Description() string {
	return "Report task progress or completion to the leader (delivers a task_result message). status must be one of: accepted, working, blocked, resolved, failed."
}
func (*ReportTaskTool) IsEnabled() bool                     { return true }
func (*ReportTaskTool) IsReadOnly(_ tool.Input) bool        { return false }
func (*ReportTaskTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *ReportTaskTool) Prompt() string                    { return t.Description() }

func (t *ReportTaskTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required")
	}
	if t.from(input) == "" {
		return errors.New("from is required")
	}
	if input.GetString("task_id") == "" {
		return errors.New("task_id is required")
	}
	status := team.TaskStatus(input.GetString("status"))
	if !status.IsValid() {
		return fmt.Errorf("invalid status %q (allowed: accepted, working, blocked, resolved, failed)", status)
	}
	return nil
}

func (*ReportTaskTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name": map[string]interface{}{"type": "string"},
			"from":      map[string]interface{}{"type": "string"},
			"to":        map[string]interface{}{"type": "string", "description": "Recipient name; defaults to team-lead."},
			"task_id":   map[string]interface{}{"type": "string"},
			"reply_to":  map[string]interface{}{"type": "string", "description": "Original task_assign message id (optional)."},
			"status": map[string]interface{}{
				"type": "string",
				"enum": []string{"accepted", "working", "blocked", "resolved", "failed"},
			},
			"summary": map[string]interface{}{"type": "string", "description": "5-10 word progress summary."},
			"detail":  map[string]interface{}{"type": "string", "description": "Detailed update / output."},
		},
		"required": []string{"task_id", "status"},
	}
}

func (*ReportTaskTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *ReportTaskTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}
	res, err := t.service.ReportTask(application.ReportTaskInput{
		TeamName: t.team(input),
		From:     t.from(input),
		To:       input.GetString("to"),
		TaskID:   input.GetString("task_id"),
		ReplyTo:  input.GetString("reply_to"),
		Status:   team.TaskStatus(input.GetString("status")),
		Summary:  input.GetString("summary"),
		Detail:   input.GetString("detail"),
	})
	if err != nil {
		if errors.Is(err, application.ErrTeamNotFound) || errors.Is(err, application.ErrMemberNotFound) {
			return tool.NewErrorResult(err.Error()), nil
		}
		return tool.NewErrorResult("report_task: " + err.Error()), nil
	}
	return tool.NewResult(jsonOut(map[string]interface{}{
		"success":    true,
		"team_name":  t.team(input),
		"task_id":    input.GetString("task_id"),
		"status":     input.GetString("status"),
		"message_id": res.MessageID,
		"recipients": res.Recipients,
	})), nil
}

// ----- WaitForMessageTool -----

// WaitForMessageTool 让模型阻塞等待 inbox 出现未读消息。
//
// 命中后立即按 drain 模式清空未读并返回。timeout 默认 30 秒，硬上限 5 分钟
// （避免模型把自己锁死）。对应 src `useInboxPoller` 的"等待协作回复"语义。
type WaitForMessageTool struct{ teamToolBase }

func NewWaitForMessageTool(svc *application.TeamService, defaultTeam, defaultFrom string) *WaitForMessageTool {
	return &WaitForMessageTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*WaitForMessageTool) Name() string      { return "wait_for_message" }
func (*WaitForMessageTool) Aliases() []string { return []string{"WaitForMessage"} }
func (*WaitForMessageTool) Description() string {
	return "Block until at least one unread message arrives in your inbox (or timeout). Returns the drained messages just like read_inbox does. Useful for leaders waiting on worker replies without polling."
}
func (*WaitForMessageTool) IsEnabled() bool                     { return true }
func (*WaitForMessageTool) IsReadOnly(_ tool.Input) bool        { return false }
func (*WaitForMessageTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *WaitForMessageTool) Prompt() string                    { return t.Description() }

func (t *WaitForMessageTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required")
	}
	if t.from(input) == "" {
		return errors.New("from is required")
	}
	return nil
}

func (*WaitForMessageTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name":          map[string]interface{}{"type": "string"},
			"from":               map[string]interface{}{"type": "string"},
			"timeout_seconds":    map[string]interface{}{"type": "integer", "description": "Max wait seconds (default 30, max 300)."},
		},
	}
}

func (*WaitForMessageTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *WaitForMessageTool) Call(ctx context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}
	timeoutSec := input.GetInt("timeout_seconds")
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	if timeoutSec > 300 {
		timeoutSec = 300
	}
	timeout := time.Duration(timeoutSec) * time.Second

	teamName := t.team(input)
	agent := t.from(input)
	msgs, err := t.service.WaitForUnread(ctx, teamName, agent, timeout)
	if err != nil {
		// 超时是"业务正常"——返回空消息而不是 IsError，让模型决定是否再等。
		if errors.Is(err, context.DeadlineExceeded) ||
			errors.Is(err, context.Canceled) ||
			err.Error() == "wait for unread: timeout" {
			return tool.NewResult(jsonOut(map[string]interface{}{
				"team_name":     teamName,
				"agent":         agent,
				"timed_out":     true,
				"count":         0,
				"messages":      []map[string]interface{}{},
				"timeout_used":  timeoutSec,
			})), nil
		}
		if errors.Is(err, application.ErrTeamNotFound) || errors.Is(err, application.ErrMemberNotFound) {
			return tool.NewErrorResult(err.Error()), nil
		}
		return tool.NewErrorResult("wait_for_message: " + err.Error()), nil
	}
	rendered := make([]map[string]interface{}, 0, len(msgs))
	for _, m := range msgs {
		rendered = append(rendered, renderMessage(m))
	}
	return tool.NewResult(jsonOut(map[string]interface{}{
		"team_name":    teamName,
		"agent":        agent,
		"timed_out":    false,
		"count":        len(rendered),
		"messages":     rendered,
		"timeout_used": timeoutSec,
	})), nil
}

// renderMessage 是 ReadInbox 与 WaitForMessage 共用的消息序列化。
func renderMessage(m team.Message) map[string]interface{} {
	entry := map[string]interface{}{
		"from":      m.From,
		"type":      string(m.Type),
		"timestamp": m.Timestamp,
	}
	if m.ID != "" {
		entry["id"] = m.ID
	}
	if m.Summary != "" {
		entry["summary"] = m.Summary
	}
	if m.Text != "" {
		entry["text"] = m.Text
	}
	if m.RequestID != "" {
		entry["request_id"] = m.RequestID
	}
	if m.ReplyTo != "" {
		entry["reply_to"] = m.ReplyTo
	}
	if m.Approve != nil {
		entry["approve"] = *m.Approve
	}
	if m.Reason != "" {
		entry["reason"] = m.Reason
	}
	if m.TaskID != "" {
		entry["task_id"] = m.TaskID
	}
	if m.TaskStatus != "" {
		entry["task_status"] = string(m.TaskStatus)
	}
	if m.PlanText != "" {
		entry["plan_text"] = m.PlanText
	}
	return entry
}

// ----- SetMemberStatusTool -----

// SetMemberStatusTool 让 agent 显式声明自己的状态（idle/working/blocked/error/done）。
//
// 默认操作 from 自己；leader 也可以传 target 改其他成员的状态（用于 cleanup 卡住的 worker）。
type SetMemberStatusTool struct{ teamToolBase }

func NewSetMemberStatusTool(svc *application.TeamService, defaultTeam, defaultFrom string) *SetMemberStatusTool {
	return &SetMemberStatusTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*SetMemberStatusTool) Name() string      { return "set_status" }
func (*SetMemberStatusTool) Aliases() []string { return []string{"SetStatus"} }
func (*SetMemberStatusTool) Description() string {
	return "Set the status of a team member (defaults to yourself). Allowed: idle, working, blocked, error, done."
}
func (*SetMemberStatusTool) IsEnabled() bool                     { return true }
func (*SetMemberStatusTool) IsReadOnly(_ tool.Input) bool        { return false }
func (*SetMemberStatusTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *SetMemberStatusTool) Prompt() string                    { return t.Description() }

func (t *SetMemberStatusTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required")
	}
	if t.from(input) == "" && input.GetString("target") == "" {
		return errors.New("either from or target is required")
	}
	st := team.MemberStatus(input.GetString("status"))
	if !st.IsValid() {
		return fmt.Errorf("invalid status %q (allowed: idle, working, blocked, error, done)", st)
	}
	return nil
}

func (*SetMemberStatusTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name": map[string]interface{}{"type": "string"},
			"from":      map[string]interface{}{"type": "string"},
			"target":    map[string]interface{}{"type": "string", "description": "Member to update; defaults to from."},
			"status": map[string]interface{}{
				"type": "string",
				"enum": []string{"idle", "working", "blocked", "error", "done"},
			},
		},
		"required": []string{"status"},
	}
}

func (*SetMemberStatusTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *SetMemberStatusTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}
	target := input.GetString("target")
	if target == "" {
		target = t.from(input)
	}
	teamName := t.team(input)
	status := team.MemberStatus(input.GetString("status"))
	if err := t.service.SetMemberStatus(teamName, target, status); err != nil {
		if errors.Is(err, application.ErrTeamNotFound) || errors.Is(err, application.ErrMemberNotFound) {
			return tool.NewErrorResult(err.Error()), nil
		}
		return tool.NewErrorResult("set_status: " + err.Error()), nil
	}
	return tool.NewResult(jsonOut(map[string]interface{}{
		"success":   true,
		"team_name": teamName,
		"target":    target,
		"status":    string(status),
	})), nil
}

// ----- HeartbeatTool -----

// HeartbeatTool 刷新自己（或指定成员）的 LastHeartbeatAt。
//
// 设计目的：worker 在长任务运行中，模型层面调一次 heartbeat 让 leader
// 看到"这个成员还活着"。后台 goroutine 也会自动周期性调 service.Heartbeat。
type HeartbeatTool struct{ teamToolBase }

func NewHeartbeatTool(svc *application.TeamService, defaultTeam, defaultFrom string) *HeartbeatTool {
	return &HeartbeatTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*HeartbeatTool) Name() string      { return "heartbeat" }
func (*HeartbeatTool) Aliases() []string { return []string{"Heartbeat"} }
func (*HeartbeatTool) Description() string {
	return "Refresh your last_heartbeat_at timestamp so the team leader sees you as alive. No-op if you are not joined to a team."
}
func (*HeartbeatTool) IsEnabled() bool                     { return true }
func (*HeartbeatTool) IsReadOnly(_ tool.Input) bool        { return false }
func (*HeartbeatTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *HeartbeatTool) Prompt() string                    { return t.Description() }

func (t *HeartbeatTool) ValidateInput(_ tool.Input) error { return nil }

func (*HeartbeatTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name": map[string]interface{}{"type": "string"},
			"from":      map[string]interface{}{"type": "string"},
		},
	}
}

func (*HeartbeatTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *HeartbeatTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}
	teamName := t.team(input)
	from := t.from(input)
	if teamName == "" || from == "" {
		// 不算错误；优雅退化（与 src 静默 skip 一致）。
		return tool.NewResult(jsonOut(map[string]interface{}{
			"skipped": true,
			"reason":  "not joined to any team",
		})), nil
	}
	if err := t.service.Heartbeat(teamName, from); err != nil {
		if errors.Is(err, application.ErrTeamNotFound) || errors.Is(err, application.ErrMemberNotFound) {
			return tool.NewErrorResult(err.Error()), nil
		}
		return tool.NewErrorResult("heartbeat: " + err.Error()), nil
	}
	return tool.NewResult(jsonOut(map[string]interface{}{
		"success":   true,
		"team_name": teamName,
		"agent":     from,
		"at":        time.Now().UnixMilli(),
	})), nil
}
