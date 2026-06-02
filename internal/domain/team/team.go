// Package team 定义 agent 协同（team / mailbox）的领域模型。
//
// 与 src/utils/swarm/teamHelpers.ts + src/utils/teammateMailbox.ts 行为对齐
// （简化版：去除 tmux/iTerm2 backend、UI dialog、worktree、plan-mode 流程，
// 只保留协调基础设施）。
//
// 核心概念：
//   - Team：一个由 leader 创建的协同空间，磁盘上对应 ~/.goclaude/teams/<name>/
//   - Member：team 中的一个 agent（含 leader 自己）
//   - Mailbox：每个 member 一个收件箱文件，其它 member 可投递消息
//   - Message：普通文本 / broadcast / 三类结构化（shutdown_request /
//     shutdown_response / plan_approval_response）
//
// 当前版本不负责"启动 teammate 进程"——只提供存盘 + 消息传递机制。
// 多个 goclaude 实例 join 同一个 team 即可互相通信。
package team

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// LeaderName 是 team leader 的固定 agent 名（与 src TEAM_LEAD_NAME 对齐）。
const LeaderName = "team-lead"

// HeartbeatStaleAfter 是判定 LastHeartbeatAt 过期的默认阈值。
//
// 与 src `useTeammateHeartbeat` 对齐——超过此时长未刷新的成员会被
// `ActiveNonLeaderCount` 视为已死，避免崩溃的 worker 永久阻塞 team_delete。
const HeartbeatStaleAfter = 90 * time.Second

// MessageType 表示消息的逻辑类型。
type MessageType string

const (
	// MessageText 普通文本消息（默认）
	MessageText MessageType = "text"
	// MessageBroadcast 广播给团队所有成员（不含发送者）
	MessageBroadcast MessageType = "broadcast"
	// MessageShutdownReq 请求接收方主动退出
	MessageShutdownReq MessageType = "shutdown_request"
	// MessageShutdownResp 对 ShutdownRequest 的响应
	MessageShutdownResp MessageType = "shutdown_response"
	// MessagePlanApprovalReq plan-mode 计划评审请求（worker → leader）
	MessagePlanApprovalReq MessageType = "plan_approval_request"
	// MessagePlanApprovalResp 对 plan-mode 计划的批准响应
	MessagePlanApprovalResp MessageType = "plan_approval_response"
	// MessageIdle worker 进入 idle 时通知 leader（避免 leader 轮询 IsActive）
	MessageIdle MessageType = "idle_notification"
	// MessageTaskAssign leader → worker 派发任务
	MessageTaskAssign MessageType = "task_assign"
	// MessageTaskResult worker → leader 汇报任务进展或结果
	MessageTaskResult MessageType = "task_result"
)

// IsValid 报告 t 是否为已识别的消息类型。
func (t MessageType) IsValid() bool {
	switch t {
	case MessageText, MessageBroadcast,
		MessageShutdownReq, MessageShutdownResp,
		MessagePlanApprovalReq, MessagePlanApprovalResp,
		MessageIdle,
		MessageTaskAssign, MessageTaskResult:
		return true
	}
	return false
}

// MemberStatus 比单一 IsActive bool 更细的状态枚举。
//
// 兼容字段：保留 IsActive 作为"是否非 idle"的快速布尔；Status 提供
// 业务级别的细粒度（idle/working/blocked/error/done）。
type MemberStatus string

const (
	StatusIdle    MemberStatus = "idle"
	StatusWorking MemberStatus = "working"
	StatusBlocked MemberStatus = "blocked"
	StatusError   MemberStatus = "error"
	StatusDone    MemberStatus = "done"
)

// IsValid 报告 s 是否为已识别状态。
func (s MemberStatus) IsValid() bool {
	switch s {
	case StatusIdle, StatusWorking, StatusBlocked, StatusError, StatusDone:
		return true
	}
	return false
}

// TaskStatus 是 task_result.status 的合法值。
type TaskStatus string

const (
	TaskAccepted TaskStatus = "accepted"
	TaskWorking  TaskStatus = "working"
	TaskBlocked  TaskStatus = "blocked"
	TaskResolved TaskStatus = "resolved"
	TaskFailed   TaskStatus = "failed"
)

// IsValid 报告 t 是否为已识别 task 状态。
func (t TaskStatus) IsValid() bool {
	switch t {
	case TaskAccepted, TaskWorking, TaskBlocked, TaskResolved, TaskFailed:
		return true
	}
	return false
}

// =============================================================================
// 团队共享任务列表（与 src 端 team task list 对齐）
// =============================================================================

// SharedTaskStatus 是团队共享任务列表中任务的状态（CodeBuddy 官方文档定义）。
type SharedTaskStatus string

const (
	// SharedTaskPending 待处理（未分配或已分配但未开始）
	SharedTaskPending SharedTaskStatus = "pending"
	// SharedTaskWorking 进行中（已有成员认领并开始工作）
	SharedTaskWorking SharedTaskStatus = "working"
	// SharedTaskCompleted 已完成（成员已汇报完成）
	SharedTaskCompleted SharedTaskStatus = "completed"
	// SharedTaskBlocked 已阻塞（依赖任务未完成）
	SharedTaskBlocked SharedTaskStatus = "blocked"
)

// IsValid 报告 s 是否为已识别的共享任务状态。
func (s SharedTaskStatus) IsValid() bool {
	switch s {
	case SharedTaskPending, SharedTaskWorking, SharedTaskCompleted, SharedTaskBlocked:
		return true
	}
	return false
}

// SharedTask 是团队共享任务列表中的一条任务。
//
// 与 CodeBuddy 官方文档对齐：所有成员共享同一份任务列表，
// 支持状态管理、成员分配、依赖关系。
type SharedTask struct {
	// ID 任务稳定标识（创建时生成，格式 task-<hex>）
	ID string `json:"id"`
	// Title 任务标题（5-10 词，对应 assign_task 的 subject）
	Title string `json:"title"`
	// Description 任务详细描述（可选）
	Description string `json:"description,omitempty"`
	// Status 任务状态：pending / working / completed / blocked
	Status SharedTaskStatus `json:"status"`
	// AssignedTo 被分配的成员名（空 = 未分配，可被自主认领）
	AssignedTo string `json:"assignedTo,omitempty"`
	// DependsOn 依赖的任务 ID 列表；所有依赖完成后 Status 才从 blocked → pending
	DependsOn []string `json:"dependsOn,omitempty"`
	// CreatedAt 创建时间（unix milli）
	CreatedAt int64 `json:"createdAt"`
	// UpdatedAt 最后更新时间（unix milli）
	UpdatedAt int64 `json:"updatedAt"`
	// Result 任务结果摘要（成员 report_task 后填写）
	Result string `json:"result,omitempty"`
}

// GenTaskID 生成任务 ID（格式 task-<8字节十六进制>）。
func GenTaskID() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("task-%d", time.Now().UnixNano())
	}
	return "task-" + hex.EncodeToString(buf[:])
}

// Member 是 team 的一个成员。
//
// 字段对齐 src/utils/swarm/teamHelpers.ts TeamFile.members 的核心子集
// （省略 tmuxPaneId / worktreePath / backendType / planModeRequired 等
// 仅在 src 端 UI/外部进程场景才用到的字段）。
type Member struct {
	// AgentID 形如 "<name>@<team>"，全局唯一
	AgentID string `json:"agentId"`
	// Name 成员展示名（teammate 维度唯一；leader 固定为 LeaderName）
	Name string `json:"name"`
	// AgentType 角色（如 "researcher" / "test-runner"），可选
	AgentType string `json:"agentType,omitempty"`
	// Model 此成员推理使用的模型；空表示继承 team default
	Model string `json:"model,omitempty"`
	// JoinedAt 加入时间（unix milli）
	JoinedAt int64 `json:"joinedAt"`
	// Cwd 成员的工作目录
	Cwd string `json:"cwd,omitempty"`
	// IsActive false 表示成员当前空闲（已结束本轮）；true/缺省 表示正在干活
	IsActive bool `json:"isActive"`
	// Status 业务级状态（idle/working/blocked/error/done）；空 = 兼容旧格式
	Status MemberStatus `json:"status,omitempty"`
	// SessionID 该成员当前 goclaude 会话 ID；进程重启后会变
	SessionID string `json:"sessionId,omitempty"`
	// LastHeartbeatAt unix milli；0 = 从未心跳；用于判定僵尸成员
	LastHeartbeatAt int64 `json:"lastHeartbeatAt,omitempty"`
}

// IsStale 报告该成员是否在 staleAfter 时长内未刷新过心跳。
//
// 仅当 LastHeartbeatAt > 0 时才参与判断；为 0（旧记录或从不心跳）视为不过期，
// 避免对未升级的客户端造成意外清理。
func (m *Member) IsStale(now time.Time, staleAfter time.Duration) bool {
	if m == nil || m.LastHeartbeatAt == 0 {
		return false
	}
	return now.Sub(time.UnixMilli(m.LastHeartbeatAt)) > staleAfter
}

// File 表示磁盘上 ~/.goclaude/teams/<name>/config.json 的内容。
//
// JSON tag 与 src TeamFile 兼容（关键字段同名）以便人工排错时容易对照。
type File struct {
	// Name team 名（用户原始输入；持久化路径用 SanitizeName 后的版本）
	Name string `json:"name"`
	// Description 可选，团队目标说明
	Description string `json:"description,omitempty"`
	// CreatedAt unix milli
	CreatedAt int64 `json:"createdAt"`
	// LeadAgentID 即 leader 的 AgentID
	LeadAgentID string `json:"leadAgentId"`
	// LeadSessionID 创建 team 的 goclaude 会话 ID（discovery 用）
	LeadSessionID string `json:"leadSessionId,omitempty"`
	// Members 含 leader 在内的全部成员
	Members []Member `json:"members"`
	// Tasks 团队共享任务列表（对齐 CodeBuddy 官方 agent-teams 文档）
	Tasks []SharedTask `json:"tasks,omitempty"`
}

// Message 是 mailbox 中的一条消息。
//
// 与 src/utils/teammateMailbox.ts TeammateMessage 对齐，并把结构化消息
// （shutdown / plan_approval / task_*）的判别字段就地展开，简化 Go 端解析。
type Message struct {
	// ID 稳定消息 ID（16 字节十六进制）；用于 ack / reply / 去重
	ID string `json:"id,omitempty"`
	// From 发送方 agent 名（不是 AgentID，便于人类阅读）
	From string `json:"from"`
	// Type 消息类型；为空时按 MessageText 处理
	Type MessageType `json:"type,omitempty"`
	// Summary 5-10 词预览（src 在 UI 中显示）
	Summary string `json:"summary,omitempty"`
	// Text 文本正文（MessageText / MessageBroadcast / 结构化消息的人类描述）
	Text string `json:"text,omitempty"`
	// RequestID 关联的 request_id（仅 shutdown_response / plan_approval_response 用）
	RequestID string `json:"requestId,omitempty"`
	// ReplyTo 引用某条消息的 ID（可选，用于 task_result 引用 task_assign）
	ReplyTo string `json:"replyTo,omitempty"`
	// Approve shutdown_response / plan_approval_response 的批准位
	Approve *bool `json:"approve,omitempty"`
	// Reason / Feedback 拒绝/批准时的可选理由
	Reason string `json:"reason,omitempty"`
	// TaskID 任务消息（task_assign / task_result）的稳定标识
	TaskID string `json:"taskId,omitempty"`
	// TaskStatus task_result 中的状态（accepted/working/blocked/resolved/failed）
	TaskStatus TaskStatus `json:"taskStatus,omitempty"`
	// PlanText plan_approval_request 中的待审计划全文
	PlanText string `json:"planText,omitempty"`
	// Timestamp unix milli
	Timestamp int64 `json:"timestamp"`
	// Read 是否已被收件方拉取过
	Read bool `json:"read"`
}

// genMessageID 生成 16 字节十六进制 ID。
//
// 失败回退为时间戳字符串，绝不返回空（保证 Message.ID 始终唯一可比）。
func genMessageID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("ts-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}

// stamp 给消息打上 ID 与时间戳（构造函数共用）。
func stamp(m Message) Message {
	if m.ID == "" {
		m.ID = genMessageID()
	}
	if m.Timestamp == 0 {
		m.Timestamp = time.Now().UnixMilli()
	}
	return m
}

// NewTextMessage 构造一条普通文本消息（未读，已设时间戳）。
func NewTextMessage(from, summary, text string) Message {
	return stamp(Message{From: from, Type: MessageText, Summary: summary, Text: text})
}

// NewBroadcast 构造一条广播消息。
func NewBroadcast(from, summary, text string) Message {
	return stamp(Message{From: from, Type: MessageBroadcast, Summary: summary, Text: text})
}

// NewShutdownRequest 构造一条请求接收方关闭的消息。
func NewShutdownRequest(from, requestID, reason string) Message {
	return stamp(Message{From: from, Type: MessageShutdownReq, RequestID: requestID, Reason: reason})
}

// NewShutdownResponse 构造对 shutdown_request 的响应。
func NewShutdownResponse(from, requestID string, approve bool, reason string) Message {
	return stamp(Message{
		From: from, Type: MessageShutdownResp, RequestID: requestID,
		Approve: &approve, Reason: reason,
	})
}

// NewPlanApprovalRequest 构造一条 plan-mode 计划评审请求（worker → leader）。
func NewPlanApprovalRequest(from, requestID, planText, summary string) Message {
	return stamp(Message{
		From: from, Type: MessagePlanApprovalReq, RequestID: requestID,
		Summary: summary, PlanText: planText,
	})
}

// NewPlanApprovalResponse 构造 plan-mode 计划批准响应。
func NewPlanApprovalResponse(from, requestID string, approve bool, feedback string) Message {
	return stamp(Message{
		From: from, Type: MessagePlanApprovalResp, RequestID: requestID,
		Approve: &approve, Reason: feedback,
	})
}

// NewIdleNotification 构造 worker → leader 的 idle 通知。
func NewIdleNotification(from, summary string) Message {
	return stamp(Message{From: from, Type: MessageIdle, Summary: summary})
}

// NewTaskAssign 构造 leader → worker 的任务派发消息。
func NewTaskAssign(from, taskID, subject, description string) Message {
	return stamp(Message{
		From: from, Type: MessageTaskAssign, TaskID: taskID,
		Summary: subject, Text: description,
	})
}

// NewTaskResult 构造 worker → leader 的任务结果汇报消息。
//
// taskID 必填；replyTo 可选（一般是 task_assign 的 Message.ID，用于
// 精确关联同一任务的多条进度更新）。
func NewTaskResult(from, taskID, replyTo string, status TaskStatus, summary, detail string) Message {
	return stamp(Message{
		From: from, Type: MessageTaskResult, TaskID: taskID, ReplyTo: replyTo,
		TaskStatus: status, Summary: summary, Text: detail,
	})
}

// ----- 命名规范化 -----

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// SanitizeName 把 team 名规范化用于磁盘路径。
//
// 规则（与 src `sanitizeName` 对齐）：非字母数字字符替换为 "-"，整体 lowercase。
// 空字符串或全非字母数字时返回 "default" 兜底。
func SanitizeName(name string) string {
	s := nonAlnum.ReplaceAllString(name, "-")
	s = strings.Trim(s, "-")
	s = strings.ToLower(s)
	if s == "" {
		return "default"
	}
	return s
}

// SanitizeAgent 把 agent 名规范化用于 inbox 文件名。
//
// 规则：与 SanitizeName 一致（保持简单、可逆性不重要——inbox 路径只做存放）。
func SanitizeAgent(name string) string {
	return SanitizeName(name)
}

// FormatAgentID 拼接 agentName@teamName 形式的全局 ID。
// 与 src `formatAgentId` 对齐。
func FormatAgentID(agentName, teamName string) string {
	return fmt.Sprintf("%s@%s", agentName, SanitizeName(teamName))
}

// ----- File 相关查询/校验辅助 -----

// FindMemberByName 在 members 中按 name 查找；找不到返回 nil。
func (f *File) FindMemberByName(name string) *Member {
	for i := range f.Members {
		if f.Members[i].Name == name {
			return &f.Members[i]
		}
	}
	return nil
}

// FindMemberByAgentID 按 agentId 查找。
func (f *File) FindMemberByAgentID(agentID string) *Member {
	for i := range f.Members {
		if f.Members[i].AgentID == agentID {
			return &f.Members[i]
		}
	}
	return nil
}

// NonLeaderMembers 返回排除 leader 的成员副本。
func (f *File) NonLeaderMembers() []Member {
	out := make([]Member, 0, len(f.Members))
	for _, m := range f.Members {
		if m.Name != LeaderName {
			out = append(out, m)
		}
	}
	return out
}

// ActiveNonLeaderCount 返回当前非 leader、IsActive=true、且心跳未过期的成员数。
//
// 用于 TeamDelete 拒绝清理仍有活跃成员的 team（与 src 一致）。
// 心跳过期的成员视为已死，不计入活跃数——避免崩溃 worker 永久阻塞清理。
func (f *File) ActiveNonLeaderCount() int {
	now := time.Now()
	n := 0
	for i := range f.Members {
		m := &f.Members[i]
		if m.Name == LeaderName || !m.IsActive {
			continue
		}
		if m.IsStale(now, HeartbeatStaleAfter) {
			continue
		}
		n++
	}
	return n
}

// Validate 检查 File 不变量（创建/写入前调用）。
func (f *File) Validate() error {
	if f == nil {
		return errors.New("nil team file")
	}
	if strings.TrimSpace(f.Name) == "" {
		return errors.New("team name is empty")
	}
	if strings.TrimSpace(f.LeadAgentID) == "" {
		return errors.New("leadAgentId is empty")
	}
	if len(f.Members) == 0 {
		return errors.New("team must have at least the leader")
	}
	seen := map[string]struct{}{}
	for _, m := range f.Members {
		if m.AgentID == "" || m.Name == "" {
			return fmt.Errorf("member missing agentId/name: %+v", m)
		}
		if _, dup := seen[m.AgentID]; dup {
			return fmt.Errorf("duplicate member agentId: %s", m.AgentID)
		}
		seen[m.AgentID] = struct{}{}
		if m.Status != "" && !m.Status.IsValid() {
			return fmt.Errorf("member %s: invalid status %q", m.Name, m.Status)
		}
	}
	return nil
}
