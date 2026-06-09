package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/anthropics/goclaude/pkg/domain/team"
)

// 本文件聚合 TeamService 的消息投递与收件箱：Send/广播展开、ReadInbox、
// 等待未读，以及 task_assign / task_result 便利封装与 leader inbox 副作用处理。
// 从 team_service.go 拆出以提升可读性；逻辑保持不变。

// SendInput 发送消息的入参。
type SendInput struct {
	TeamName string
	From     string // 发送方 name（必填）
	To       string // 接收方 name；"*" 表示 broadcast
	Summary  string
	Text     string
	// Structured 非空时用于结构化消息；优先级高于 Text。
	Structured *team.Message
}

// SendResult 发送结果。
type SendResult struct {
	Recipients []string // 实际投递的 agent name 列表（Broadcast 展开后）
	MessageID  string   // 投递消息的稳定 ID（用于 ack / replyTo）
}

// Send 把消息投递到 To 的 inbox（或全部非发送方成员）。
//
// 校验：sender 必须存在于 team 中；To="*" 时展开为非发送方全员。
// 同一条消息会以相同的 ID 写入所有收件人的 inbox（便于追踪）。
func (s *TeamService) Send(in SendInput) (*SendResult, error) {
	if in.From == "" {
		return nil, errors.New("from is required")
	}
	if in.To == "" {
		return nil, errors.New("to is required (use '*' for broadcast)")
	}
	f, err := s.requireTeam(in.TeamName)
	if err != nil {
		return nil, err
	}
	if f.FindMemberByName(in.From) == nil {
		return nil, fmt.Errorf("%w: sender %q not in team", ErrMemberNotFound, in.From)
	}

	// 展开收件人（To="*" → 非发送方全员）
	var recipients []string
	if in.To == "*" {
		for _, m := range f.Members {
			if m.Name != in.From {
				recipients = append(recipients, m.Name)
			}
		}
	} else {
		if f.FindMemberByName(in.To) == nil {
			return nil, fmt.Errorf("%w: recipient %q not in team", ErrMemberNotFound, in.To)
		}
		recipients = []string{in.To}
	}

	// 构建消息体：structured 消息优先，否则基于 Text/Summary 构造
	msg := s.buildMessage(in, recipients)
	msg.Timestamp = time.Now().UnixMilli()

	for _, r := range recipients {
		if err := s.Mailbox.Append(in.TeamName, r, msg); err != nil {
			return nil, fmt.Errorf("deliver to %s: %w", r, err)
		}
	}
	s.logger.Debug("message sent",
		slog.String("team", in.TeamName), slog.String("from", in.From), slog.String("to", in.To),
		slog.String("type", string(msg.Type)), slog.String("msg_id", msg.ID), slog.Int("recipients", len(recipients)))
	return &SendResult{Recipients: recipients, MessageID: msg.ID}, nil
}

// buildMessage 基于 SendInput 构建待投递的消息体。
func (s *TeamService) buildMessage(in SendInput, recipients []string) team.Message {
	var msg team.Message
	if in.Structured != nil {
		msg = *in.Structured
		msg.From = in.From
	} else {
		msg.From = in.From
		msg.Text = in.Text
		msg.Summary = in.Summary
		if in.To == "*" {
			msg.Type = team.MessageBroadcast
		} else {
			msg.Type = team.MessageText
		}
	}
	if msg.ID == "" {
		msg.ID = team.NewTextMessage("", "", "").ID // 借用 stamp 生成唯一 ID
	}
	return msg
}

// ReadInbox 拉取并标记某成员所有未读消息。
//
// drain=false 时仅查看（不修改 read 标记）；true 时按消费语义清空未读。
func (s *TeamService) ReadInbox(teamName, agentName string, drain bool) ([]team.Message, error) {
	if _, _, err := s.requireTeamAndMember(teamName, agentName); err != nil {
		return nil, err
	}
	if drain {
		msgs, err := s.Mailbox.DrainUnread(teamName, agentName)
		if err == nil && len(msgs) > 0 {
			s.logger.Debug("inbox drained",
				slog.String("team", teamName), slog.String("agent", agentName), slog.Int("count", len(msgs)))
		}
		return msgs, err
	}
	return s.Mailbox.ReadUnread(teamName, agentName)
}

// ReadInboxSince 按时间戳游标返回 inbox 历史片段（不修改 read 标记）。
//
// sinceMs <= 0 时返回 inbox 全部消息。用于 leader 复盘时分页拉取。
func (s *TeamService) ReadInboxSince(teamName, agentName string, sinceMs int64) ([]team.Message, error) {
	if _, _, err := s.requireTeamAndMember(teamName, agentName); err != nil {
		return nil, err
	}
	return s.Mailbox.ReadSince(teamName, agentName, sinceMs)
}

// WaitForUnread 阻塞等待 agent 的 inbox 出现未读，或 ctx 取消 / 超时。
//
// 命中后立即按 drain 模式清空未读。timeout<=0 时仅受 ctx 控制。
func (s *TeamService) WaitForUnread(ctx context.Context, teamName, agentName string, timeout time.Duration) ([]team.Message, error) {
	if _, _, err := s.requireTeamAndMember(teamName, agentName); err != nil {
		return nil, err
	}
	return s.Mailbox.WaitForUnread(ctx, teamName, agentName, timeout)
}

// AssignTaskInput 分配任务的入参。
type AssignTaskInput struct {
	TeamName    string
	From        string // 必须是 leader 名（一般是 LeaderName）
	To          string // 受派 worker 名
	TaskID      string // 必填；调用方负责生成（推荐 short uuid）
	Subject     string
	Description string
}

// AssignTask 是 Send 的便利包装，固定 type=task_assign。
//
// 不强制要求 from 必须是 LeaderName——支持 worker → worker 派活，但会记日志便于审计。
func (s *TeamService) AssignTask(in AssignTaskInput) (*SendResult, error) {
	if strings.TrimSpace(in.TaskID) == "" {
		return nil, errors.New("task_id is required")
	}
	if strings.TrimSpace(in.Subject) == "" {
		return nil, errors.New("subject is required")
	}
	msg := team.NewTaskAssign(in.From, in.TaskID, in.Subject, in.Description)
	res, err := s.Send(SendInput{
		TeamName:   in.TeamName,
		From:       in.From,
		To:         in.To,
		Structured: &msg,
	})
	if err != nil {
		return nil, err
	}
	s.logger.Info("task assigned",
		slog.String("team", in.TeamName),
		slog.String("from", in.From),
		slog.String("to", in.To),
		slog.String("task_id", in.TaskID),
	)
	return res, nil
}

// ReportTaskInput 汇报任务的入参。
type ReportTaskInput struct {
	TeamName string
	From     string // worker 名
	To       string // 一般为 LeaderName；为空时默认投给 leader
	TaskID   string
	ReplyTo  string // 可选：原 task_assign 的 Message.ID
	Status   team.TaskStatus
	Summary  string
	Detail   string
}

// ReportTask 是 Send 的便利包装，固定 type=task_result。To 为空时自动用 LeaderName。
func (s *TeamService) ReportTask(in ReportTaskInput) (*SendResult, error) {
	if !in.Status.IsValid() {
		return nil, fmt.Errorf("invalid task status %q", in.Status)
	}
	if strings.TrimSpace(in.TaskID) == "" {
		return nil, errors.New("task_id is required")
	}
	to := in.To
	if to == "" {
		to = team.LeaderName
	}
	msg := team.NewTaskResult(in.From, in.TaskID, in.ReplyTo, in.Status, in.Summary, in.Detail)
	res, err := s.Send(SendInput{
		TeamName:   in.TeamName,
		From:       in.From,
		To:         to,
		Structured: &msg,
	})
	if err != nil {
		return nil, err
	}
	s.logger.Info("task reported",
		slog.String("team", in.TeamName),
		slog.String("from", in.From),
		slog.String("task_id", in.TaskID),
		slog.String("status", string(in.Status)),
	)
	return res, nil
}

// ProcessLeaderInbox 扫描 leader 的未读消息并执行协议级副作用：
//
//   - shutdown_response{approve:true} → SetMemberActive(sender, false)
//   - idle_notification               → SetMemberStatus(sender, idle)
//   - task_assign / task_result       → 同步到共享任务列表
//
// 其它消息原样返回供 leader agent 阅读。返回的列表保持原始顺序，
// 包含已被处理的协议消息（让 leader 知道发生了什么）。
// 调用方一般在 leader REPL 每次 turn 开头调一次。
func (s *TeamService) ProcessLeaderInbox(teamName string) ([]team.Message, error) {
	msgs, err := s.ReadInbox(teamName, team.LeaderName, true)
	if err != nil {
		return nil, err
	}
	for _, m := range msgs {
		s.processLeaderMessage(teamName, m)
	}
	return msgs, nil
}

// processLeaderMessage 对单条 leader inbox 消息执行协议级副作用。
func (s *TeamService) processLeaderMessage(teamName string, m team.Message) {
	switch m.Type {
	case team.MessageShutdownResp:
		s.handleShutdownResp(teamName, m)
	case team.MessageIdle:
		s.handleIdle(teamName, m)
	case team.MessageTaskAssign:
		s.syncTaskFromAssign(teamName, m)
	case team.MessageTaskResult:
		s.syncTaskFromResult(teamName, m)
	}
}

// handleShutdownResp 处理 worker 的 shutdown 确认：将 worker 标记为 inactive。
func (s *TeamService) handleShutdownResp(teamName string, m team.Message) {
	if m.Approve == nil || !*m.Approve || m.From == "" {
		return
	}
	if err := s.SetMemberActive(teamName, m.From, false); err != nil &&
		!errors.Is(err, ErrMemberNotFound) {
		s.logger.Warn("process shutdown_response: SetMemberActive failed",
			slog.String("team", teamName), slog.String("agent", m.From), slog.Any("err", err))
	}
}

// handleIdle 处理 worker 的空闲通知：将 worker 状态置为 idle。
func (s *TeamService) handleIdle(teamName string, m team.Message) {
	if m.From == "" || m.From == team.LeaderName {
		return
	}
	if err := s.SetMemberStatus(teamName, m.From, team.StatusIdle); err != nil &&
		!errors.Is(err, ErrMemberNotFound) {
		s.logger.Warn("process idle: SetMemberStatus failed",
			slog.String("team", teamName), slog.String("agent", m.From), slog.Any("err", err))
	}
}

// syncTaskFromAssign 将 task_assign 消息同步到共享任务列表（新建 pending 任务）。
func (s *TeamService) syncTaskFromAssign(teamName string, m team.Message) {
	if m.TaskID == "" {
		return
	}
	_ = s.UpsertTask(teamName, team.SharedTask{
		ID: m.TaskID, Title: m.Summary, Description: m.Text,
		Status: team.SharedTaskPending, AssignedTo: m.From,
		CreatedAt: m.Timestamp, UpdatedAt: m.Timestamp,
	})
}

// syncTaskFromResult 将 task_result 消息的结果状态同步到共享任务列表。
func (s *TeamService) syncTaskFromResult(teamName string, m team.Message) {
	if m.TaskID == "" {
		return
	}
	status := sharedTaskStatusFrom(m.TaskStatus)
	_ = s.UpsertTask(teamName, team.SharedTask{
		ID: m.TaskID, Status: status, Result: m.Text, UpdatedAt: m.Timestamp,
	})
}

// sharedTaskStatusFrom 将 member 任务状态映射到共享任务列表状态。
func sharedTaskStatusFrom(ts team.TaskStatus) team.SharedTaskStatus {
	switch ts {
	case team.TaskWorking:
		return team.SharedTaskWorking
	case team.TaskResolved:
		return team.SharedTaskCompleted
	case team.TaskFailed:
		return team.SharedTaskBlocked
	default:
		return team.SharedTaskPending
	}
}
