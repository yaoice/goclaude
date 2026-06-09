// Package tools - team 协同相关工具。
//
// 本文件实现 5 个工具，组合在一起复刻 src 端 agent-teams 子系统的最小可用面：
//
//   - TeamCreate：leader 开一个新 team
//   - TeamDelete：leader 清理 team（默认拒绝在仍有活跃成员时清理）
//   - SendMessage：向 teammate（或 "*" 广播）投递消息（含结构化消息）
//   - ListPeers：列出当前 team 的所有成员
//   - ReadInbox：拉取自己的未读消息（drain 模式标记已读）
//
// 与 src 工具的差异（已知简化）：
//   - 不 spawn teammate 进程：5 个工具只负责协调；多个 goclaude 实例
//     join 同一 team 即可互通
//   - 不维护 React/Ink AppState；team_name + sender_name 改由工具入参显式提供
//
// 这些工具都是 IsConcurrencySafe=true、走 PermissionAllow（与 src 一致：
// agent-teams 通讯路径不应被权限弹窗阻塞）。
package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/anthropics/goclaude/pkg/application"
	"github.com/anthropics/goclaude/pkg/domain/team"
	"github.com/anthropics/goclaude/pkg/domain/tool"
)

// ----- 共用 -----

// teamToolBase 抽出 5 个工具共用的字段：service + 当前 leader 的身份。
//
// senderName / teamName 在交互式 REPL 中由 CLI 启动时注入；不在 team 上下文
// 时这两个字段为空，工具会要求模型/调用方在 input 里显式传 from/team_name。
type teamToolBase struct {
	service     *application.TeamService
	defaultTeam string // 启动时 join 的 team；可为空
	defaultFrom string // 启动时使用的 agent name；可为空
	// session 共享会话追踪器（可为 nil）；team_create / auto_setup_team 在成功后
	// 通过它登记"本会话是某 team 的 leader"，供上层 REPL 自动处理 leader inbox。
	session *TeamSession
}

func (b *teamToolBase) team(input tool.Input) string {
	if v := input.GetString("team_name"); v != "" {
		return v
	}
	return b.defaultTeam
}
func (b *teamToolBase) from(input tool.Input) string {
	if v := input.GetString("from"); v != "" {
		return v
	}
	return b.defaultFrom
}

// genRequestID 生成 16 字节十六进制 ID，用于 shutdown / plan_approval 配对。
func genRequestID() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}

// ----- TeamCreate -----

// TeamCreateTool 让 leader 创建一个新 team。对齐 src/tools/TeamCreateTool。
type TeamCreateTool struct{ teamToolBase }

// NewTeamCreateTool 构造。defaultTeam/defaultFrom 给后续工具用，本工具不读。
func NewTeamCreateTool(svc *application.TeamService, defaultTeam, defaultFrom string) *TeamCreateTool {
	return &TeamCreateTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*TeamCreateTool) Name() string      { return "team_create" }
func (*TeamCreateTool) Aliases() []string { return []string{"TeamCreate"} }
func (*TeamCreateTool) Description() string {
	return "Create a new team to coordinate multiple agents. Returns the team name and the lead agent ID. Other goclaude instances can then join this team to exchange messages with you."
}
func (*TeamCreateTool) IsEnabled() bool                     { return true }
func (*TeamCreateTool) IsReadOnly(_ tool.Input) bool        { return false }
func (*TeamCreateTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *TeamCreateTool) Prompt() string                    { return t.Description() }

func (*TeamCreateTool) ValidateInput(input tool.Input) error {
	if input.GetString("team_name") == "" {
		return errors.New("team_name is required")
	}
	return nil
}

func (*TeamCreateTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name": map[string]interface{}{
				"type":        "string",
				"description": "Name for the new team (used as a directory under ~/.goclaude/teams/).",
			},
			"description": map[string]interface{}{
				"type":        "string",
				"description": "Optional team purpose / goal.",
			},
			"agent_type": map[string]interface{}{
				"type":        "string",
				"description": "Optional role of the team lead (e.g. 'researcher', 'reviewer').",
			},
		},
		"required": []string{"team_name"},
	}
}

func (*TeamCreateTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *TeamCreateTool) Call(_ context.Context, input tool.Input, uc *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}
	name := input.GetString("team_name")
	in := application.CreateTeamInput{
		Name:          name,
		Description:   input.GetString("description"),
		LeadAgentType: input.GetString("agent_type"),
		LeadCwd:       application.CurrentCwd(),
	}
	f, err := t.service.CreateTeam(in)
	if errors.Is(err, application.ErrTeamExists) {
		return tool.NewErrorResult(fmt.Sprintf("team %q already exists; pick a new name or call team_delete first", name)), nil
	}
	if err != nil {
		return tool.NewErrorResult("create team: " + err.Error()), nil
	}
	// 登记"本会话是该 team 的 leader"，让上层每轮自动处理 leader inbox。
	// SetLeader 异步触发 OnTeamCreated → TeamEngine.SpawnMembers，
	// 由 TeamEngine 负责创建 team workspace（稳定路径，无时间戳）。
	t.session.SetLeader(f.Name)
	return tool.NewResult(jsonOut(map[string]interface{}{
		"team_name":     f.Name,
		"lead_agent_id": f.LeadAgentID,
		"members":       len(f.Members),
	})), nil
}

// ----- TeamDelete -----

// TeamDeleteTool 清理某个 team 的目录与 inbox。对齐 src/tools/TeamDeleteTool。
type TeamDeleteTool struct{ teamToolBase }

func NewTeamDeleteTool(svc *application.TeamService, defaultTeam, defaultFrom string) *TeamDeleteTool {
	return &TeamDeleteTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*TeamDeleteTool) Name() string      { return "team_delete" }
func (*TeamDeleteTool) Aliases() []string { return []string{"TeamDelete"} }
func (*TeamDeleteTool) Description() string {
	return "Delete a team and clean up its directory. Refuses to delete if the team still has active non-leader members; pass force=true to override."
}
func (*TeamDeleteTool) IsEnabled() bool                     { return true }
func (*TeamDeleteTool) IsReadOnly(_ tool.Input) bool        { return false }
func (*TeamDeleteTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *TeamDeleteTool) Prompt() string                    { return t.Description() }

func (t *TeamDeleteTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required (or join a team first)")
	}
	return nil
}

func (*TeamDeleteTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name": map[string]interface{}{
				"type":        "string",
				"description": "Team to delete; falls back to the team this session joined.",
			},
			"force": map[string]interface{}{
				"type":        "boolean",
				"description": "Skip the active-members safety check.",
			},
		},
	}
}

func (*TeamDeleteTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *TeamDeleteTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}
	name := t.team(input)
	deleted, err := t.service.DeleteTeam(name, application.DeleteTeamOptions{Force: input.GetBool("force")})
	if errors.Is(err, application.ErrTeamHasActiveMembers) {
		return tool.NewErrorResult(err.Error() + "; ask them to shut down or pass force=true"), nil
	}
	if err != nil {
		return tool.NewErrorResult("delete team: " + err.Error()), nil
	}
	if !deleted {
		return tool.NewResult(jsonOut(map[string]interface{}{
			"team_name": name, "deleted": false, "message": "team not found",
		})), nil
	}
	return tool.NewResult(jsonOut(map[string]interface{}{
		"team_name": name, "deleted": true,
	})), nil
}

// ----- SendMessage -----

// SendMessageTool 把消息投递到 teammate 的 inbox。
//
// 对齐 src/tools/SendMessageTool/SendMessageTool.ts：
//   - to="*" → 广播
//   - message.type="shutdown_request" / "shutdown_response" / "plan_approval_response"
//     → 走结构化消息
//   - 其它 → 普通文本，要求传 summary
type SendMessageTool struct{ teamToolBase }

func NewSendMessageTool(svc *application.TeamService, defaultTeam, defaultFrom string) *SendMessageTool {
	return &SendMessageTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*SendMessageTool) Name() string      { return "send_message" }
func (*SendMessageTool) Aliases() []string { return []string{"SendMessage"} }
func (*SendMessageTool) Description() string {
	return "Send a message to a teammate by name (or '*' to broadcast). Supports plain-text messages (require a 5-10 word summary) and structured messages: shutdown_request, shutdown_response{request_id, approve}, plan_approval_response{request_id, approve}."
}
func (*SendMessageTool) IsEnabled() bool                     { return true }
func (*SendMessageTool) IsReadOnly(_ tool.Input) bool        { return false }
func (*SendMessageTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *SendMessageTool) Prompt() string                    { return t.Description() }

func (t *SendMessageTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required (or join a team first)")
	}
	if t.from(input) == "" {
		return errors.New("from is required (or set defaultFrom on the tool)")
	}
	to := input.GetString("to")
	if to == "" {
		return errors.New("to is required (use '*' for broadcast)")
	}
	mtype := input.GetString("type")
	switch mtype {
	case "", string(team.MessageText), string(team.MessageBroadcast):
		if input.GetString("content") == "" {
			return errors.New("content is required for text messages")
		}
		if input.GetString("summary") == "" {
			return errors.New("summary is required for text messages (5-10 words)")
		}
	case string(team.MessageShutdownReq):
		// 不需要更多字段；reason 可选
	case string(team.MessageShutdownResp), string(team.MessagePlanApprovalResp):
		if input.GetString("request_id") == "" {
			return errors.New("request_id is required for shutdown_response / plan_approval_response")
		}
		if _, ok := input["approve"]; !ok {
			return errors.New("approve (boolean) is required for shutdown_response / plan_approval_response")
		}
	case string(team.MessagePlanApprovalReq):
		if input.GetString("plan_text") == "" && input.GetString("content") == "" {
			return errors.New("plan_text (or content) is required for plan_approval_request")
		}
	case string(team.MessageIdle):
		// 无必填字段；summary 可选
	default:
		return fmt.Errorf("unknown type %q (allowed: text, broadcast, shutdown_request, shutdown_response, plan_approval_request, plan_approval_response, idle_notification)", mtype)
	}
	return nil
}

func (*SendMessageTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name": map[string]interface{}{"type": "string"},
			"from":      map[string]interface{}{"type": "string", "description": "Sender agent name. Defaults to this session's identity."},
			"to":        map[string]interface{}{"type": "string", "description": "Recipient agent name, or '*' for broadcast."},
			"type": map[string]interface{}{
				"type": "string",
				"enum": []string{
					"text", "broadcast",
					"shutdown_request", "shutdown_response",
					"plan_approval_request", "plan_approval_response",
					"idle_notification",
				},
				"default": "text",
			},
			"summary":    map[string]interface{}{"type": "string", "description": "5-10 word preview (text/broadcast/idle only)"},
			"content":    map[string]interface{}{"type": "string", "description": "Message body (text/broadcast only)"},
			"request_id": map[string]interface{}{"type": "string", "description": "Correlation ID (response messages only)"},
			"approve":    map[string]interface{}{"type": "boolean", "description": "Approve flag (response messages only)"},
			"reason":     map[string]interface{}{"type": "string"},
			"plan_text":  map[string]interface{}{"type": "string", "description": "Plan body for plan_approval_request"},
		},
		"required": []string{"to"},
	}
}

func (*SendMessageTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *SendMessageTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}

	// 解析 @mention 语法
	to := input.GetString("to")
	content := input.GetString("content")

	// 如果 content 中包含 @mention，自动提取收件人
	if to == "" && content != "" {
		mentions := parseMentions(content)
		if len(mentions) > 0 {
			to = mentions[0] // 取第一个 @mention 作为收件人
			// 从 content 中移除 @mention（可选）
			// content = removeMentions(content)
		}
	}

	send := application.SendInput{
		TeamName: t.team(input),
		From:     t.from(input),
		To:       to,
		Summary:  input.GetString("summary"),
		Text:     content,
	}
	mtype := input.GetString("type")
	switch mtype {
	case string(team.MessageShutdownReq):
		// auto-generate request_id 若调用方没给——便于响应方引用
		rid := input.GetString("request_id")
		if rid == "" {
			rid = genRequestID()
		}
		msg := team.NewShutdownRequest(send.From, rid, input.GetString("reason"))
		send.Structured = &msg
	case string(team.MessageShutdownResp):
		approve := input.GetBool("approve")
		msg := team.NewShutdownResponse(send.From, input.GetString("request_id"), approve, input.GetString("reason"))
		send.Structured = &msg
	case string(team.MessagePlanApprovalReq):
		rid := input.GetString("request_id")
		if rid == "" {
			rid = genRequestID()
		}
		planText := input.GetString("plan_text")
		if planText == "" {
			planText = input.GetString("content")
		}
		msg := team.NewPlanApprovalRequest(send.From, rid, planText, input.GetString("summary"))
		send.Structured = &msg
	case string(team.MessagePlanApprovalResp):
		approve := input.GetBool("approve")
		msg := team.NewPlanApprovalResponse(send.From, input.GetString("request_id"), approve, input.GetString("reason"))
		send.Structured = &msg
	case string(team.MessageIdle):
		msg := team.NewIdleNotification(send.From, input.GetString("summary"))
		send.Structured = &msg
	}

	res, err := t.service.Send(send)
	if errors.Is(err, application.ErrTeamNotFound) || errors.Is(err, application.ErrMemberNotFound) {
		return tool.NewErrorResult(err.Error()), nil
	}
	if err != nil {
		return tool.NewErrorResult("send: " + err.Error()), nil
	}
	out := map[string]interface{}{
		"success":    true,
		"recipients": res.Recipients,
		"team_name":  send.TeamName,
		"message_id": res.MessageID,
	}
	if send.Structured != nil {
		out["type"] = string(send.Structured.Type)
		if send.Structured.RequestID != "" {
			out["request_id"] = send.Structured.RequestID
		}
		if send.Structured.TaskID != "" {
			out["task_id"] = send.Structured.TaskID
		}
	}
	return tool.NewResult(jsonOut(out)), nil
}

// ----- ListPeers -----

// ListPeersTool 列出当前 team 的全部成员。对齐 src ListPeersTool。
type ListPeersTool struct{ teamToolBase }

func NewListPeersTool(svc *application.TeamService, defaultTeam, defaultFrom string) *ListPeersTool {
	return &ListPeersTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*ListPeersTool) Name() string      { return "list_peers" }
func (*ListPeersTool) Aliases() []string { return []string{"ListPeers"} }
func (*ListPeersTool) Description() string {
	return "List members of a team (name, agent_type, model, active status, joined_at). Useful before send_message to verify the recipient name."
}
func (*ListPeersTool) IsEnabled() bool                     { return true }
func (*ListPeersTool) IsReadOnly(_ tool.Input) bool        { return true }
func (*ListPeersTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *ListPeersTool) Prompt() string                    { return t.Description() }

func (t *ListPeersTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required")
	}
	return nil
}

func (*ListPeersTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name": map[string]interface{}{"type": "string"},
		},
	}
}

func (*ListPeersTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *ListPeersTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}
	f, err := t.service.GetTeam(t.team(input))
	if err != nil {
		return tool.NewErrorResult("get team: " + err.Error()), nil
	}
	if f == nil {
		return tool.NewErrorResult("team not found: " + t.team(input)), nil
	}
	peers := make([]map[string]interface{}, 0, len(f.Members))
	for _, m := range f.Members {
		entry := map[string]interface{}{
			"name":       m.Name,
			"agent_id":   m.AgentID,
			"agent_type": m.AgentType,
			"model":      m.Model,
			"active":     m.IsActive,
			"joined_at":  m.JoinedAt,
		}
		if m.Status != "" {
			entry["status"] = string(m.Status)
		}
		if m.SessionID != "" {
			entry["session_id"] = m.SessionID
		}
		if m.LastHeartbeatAt != 0 {
			entry["last_heartbeat_at"] = m.LastHeartbeatAt
		}
		peers = append(peers, entry)
	}
	return tool.NewResult(jsonOut(map[string]interface{}{
		"team_name": f.Name,
		"members":   peers,
	})), nil
}

// ----- ReadInbox -----

// ReadInboxTool 让 agent 拉取自己的未读消息。
//
// 对齐 src useInboxPoller / readUnreadMessages 流程（但本工具不是后台轮询，
// 由模型按需主动调用——更适合 goclaude 当前的 REPL 单线程模型）。
type ReadInboxTool struct{ teamToolBase }

func NewReadInboxTool(svc *application.TeamService, defaultTeam, defaultFrom string) *ReadInboxTool {
	return &ReadInboxTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*ReadInboxTool) Name() string      { return "read_inbox" }
func (*ReadInboxTool) Aliases() []string { return []string{"ReadInbox"} }
func (*ReadInboxTool) Description() string {
	return "Read unread messages from your inbox. By default marks them as read (drain mode); pass peek=true to view without marking."
}
func (*ReadInboxTool) IsEnabled() bool                     { return true }
func (*ReadInboxTool) IsReadOnly(_ tool.Input) bool        { return false }
func (*ReadInboxTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *ReadInboxTool) Prompt() string                    { return t.Description() }

func (t *ReadInboxTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required (or join a team first)")
	}
	if t.from(input) == "" {
		return errors.New("agent name is required (set 'from' or session default)")
	}
	return nil
}

func (*ReadInboxTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name": map[string]interface{}{"type": "string"},
			"from":      map[string]interface{}{"type": "string", "description": "The agent name whose inbox to read."},
			"peek":      map[string]interface{}{"type": "boolean", "description": "If true, do not mark messages as read."},
			"since":     map[string]interface{}{"type": "integer", "description": "Unix-milli cursor; only messages with timestamp > since are returned. Implies peek=true."},
			"limit":     map[string]interface{}{"type": "integer", "description": "Maximum number of messages to return (default 100)."},
		},
	}
}

func (*ReadInboxTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *ReadInboxTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}
	since := int64(input.GetInt("since"))
	limit := input.GetInt("limit")
	if limit <= 0 {
		limit = 100
	}
	teamName := t.team(input)
	agent := t.from(input)

	var msgs []team.Message
	var err error
	drain := !input.GetBool("peek") && since == 0
	if since > 0 {
		// since 模式始终是只读视图
		msgs, err = t.service.ReadInboxSince(teamName, agent, since)
	} else {
		msgs, err = t.service.ReadInbox(teamName, agent, drain)
	}
	if err != nil {
		return tool.NewErrorResult("read inbox: " + err.Error()), nil
	}
	if len(msgs) > limit {
		msgs = msgs[:limit]
	}
	rendered := make([]map[string]interface{}, 0, len(msgs))
	for _, m := range msgs {
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
		rendered = append(rendered, entry)
	}
	return tool.NewResult(jsonOut(map[string]interface{}{
		"team_name": teamName,
		"agent":     agent,
		"drained":   drain,
		"since":     since,
		"count":     len(rendered),
		"messages":  rendered,
	})), nil
}
