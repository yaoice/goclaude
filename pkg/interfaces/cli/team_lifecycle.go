package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/goclaude/pkg/application"
	teamdomain "github.com/anthropics/goclaude/pkg/domain/team"
	"github.com/anthropics/goclaude/pkg/tools"
)

// startTeamLifecycle 在主进程启动时把当前会话作为 team member 注册，
// 并在后台周期性发送心跳；返回的 cleanup 应在进程退出前调用。
//
// 行为：
//   - rt.TeamName == "" || rt.AgentName == ""              → no-op，cleanup 是空函数。
//   - rt.AgentName == teamdomain.LeaderName 或 "team-lead" → 假定 team_create 已经
//                                                            把 leader 加进去，仅起心跳。
//   - 其它情况                                              → 调用 svc.JoinTeam 自动加入；
//                                                            team 不存在时打 warn，不阻塞。
//
// cleanup 行为：
//   - 取消心跳 goroutine
//   - 把成员标记为 IsActive=false / Status=idle（让 leader 即时感知退出）
//   - 不会自动 LeaveTeam（保留 inbox 历史给 leader 复盘；与 src 一致）
//
// 心跳间隔：30s。失败时仅 debug 日志，不影响 REPL 主循环。
func startTeamLifecycle(
	ctx context.Context,
	svc *application.TeamService,
	rt tools.TeamRuntime,
	logger *slog.Logger,
) (cleanup func()) {
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With(slog.String("component", "team_lifecycle"))

	if svc == nil || rt.TeamName == "" || rt.AgentName == "" {
		return func() {}
	}

	// leader 名常量比对：team_create 时已经把 leader 加进去了，这里跳过 join
	isLeader := rt.AgentName == teamdomain.LeaderName

	if !isLeader {
		_, _, err := svc.JoinTeam(application.JoinTeamInput{
			TeamName:  rt.TeamName,
			AgentName: rt.AgentName,
			AgentType: flagTeamRole,
			Cwd:       application.CurrentCwd(),
		})
		if err != nil {
			if errors.Is(err, application.ErrTeamNotFound) {
				logger.Warn("auto-join skipped: team not found; only team_create tool will work",
					slog.String("team", rt.TeamName),
					slog.String("agent", rt.AgentName),
				)
			} else {
				logger.Warn("auto-join failed; continuing without team membership",
					slog.String("team", rt.TeamName),
					slog.String("agent", rt.AgentName),
					slog.Any("err", err),
				)
			}
			// 即便 join 失败也返回 cleanup（用于关闭 ctx），不起心跳
			return func() {}
		}
		logger.Info("joined team",
			slog.String("team", rt.TeamName),
			slog.String("agent", rt.AgentName),
		)
	}

	// 心跳 goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := svc.Heartbeat(rt.TeamName, rt.AgentName); err != nil {
					logger.Debug("heartbeat failed",
						slog.String("team", rt.TeamName),
						slog.String("agent", rt.AgentName),
						slog.Any("err", err),
					)
				}
			}
		}
	}()

	cleanup = func() {
		wg.Wait()
		// 退出时把自己标记为 idle，让 leader 立即看到（避免阻塞 team_delete）
		if err := svc.SetMemberActive(rt.TeamName, rt.AgentName, false); err != nil &&
			!errors.Is(err, application.ErrMemberNotFound) &&
			!errors.Is(err, application.ErrTeamNotFound) {
			logger.Debug("cleanup: SetMemberActive(false) failed",
				slog.String("team", rt.TeamName),
				slog.String("agent", rt.AgentName),
				slog.Any("err", err),
			)
		} else {
			logger.Debug("team lifecycle cleanup done",
				slog.String("team", rt.TeamName),
				slog.String("agent", rt.AgentName),
			)
		}
	}
	return cleanup
}

// makeLeaderInboxHook 构造 REPL 的 OnBeforeTurn 钩子：当本会话是某 team 的 leader
// 时，每轮提交前自动调用 ProcessLeaderInbox，把 teammate 的最新进展同步进共享任务
// 列表（已在 service 内完成），并把可读摘要作为团队上下文返回，注入到对话中。
//
// 这把过去"只在测试里被调用"的 ProcessLeaderInbox 接入了真实运行时，使
// "任务分配 → worker 执行 → 状态回流到 leader"的闭环真正成立。
//
// sess 为 nil 或本会话不是任何 team 的 leader 时返回空串（钩子退化为 no-op）。
func makeLeaderInboxHook(svc *application.TeamService, sess *tools.TeamSession, logger *slog.Logger) func() string {
	if logger == nil {
		logger = slog.Default()
	}
	return func() string {
		if svc == nil || sess == nil {
			return ""
		}
		teamName := sess.LeaderTeam()
		if teamName == "" {
			return ""
		}
		msgs, err := svc.ProcessLeaderInbox(teamName)
		if err != nil {
			logger.Debug("ProcessLeaderInbox failed",
				slog.String("team", teamName), slog.Any("err", err))
			return ""
		}
		if len(msgs) == 0 {
			return ""
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "[团队 %q 的最新动态 — 收到 %d 条来自协作者的消息]\n", teamName, len(msgs))
		for _, m := range msgs {
			if line := formatTeamMessage(m); line != "" {
				sb.WriteString("- " + line + "\n")
			}
		}
		sb.WriteString("（以上为团队成员的自动同步消息，请据此推进协调；若需回复某成员，使用 send_message。）")
		return sb.String()
	}
}

// formatTeamMessage 把一条 inbox 消息渲染成 leader 可读的一行摘要。
func formatTeamMessage(m teamdomain.Message) string {
	from := m.From
	if from == "" {
		from = "(unknown)"
	}
	switch m.Type {
	case teamdomain.MessageTaskResult:
		desc := firstNonEmpty(m.Summary, m.Text)
		status := string(m.TaskStatus)
		if status == "" {
			status = "update"
		}
		return fmt.Sprintf("%s 汇报任务 %s（%s）：%s", from, m.TaskID, status, desc)
	case teamdomain.MessageIdle:
		return fmt.Sprintf("%s 已进入空闲状态：%s", from, m.Summary)
	case teamdomain.MessageShutdownResp:
		approved := "拒绝"
		if m.Approve != nil && *m.Approve {
			approved = "同意"
		}
		return fmt.Sprintf("%s 对关闭请求的响应：%s %s", from, approved, m.Reason)
	case teamdomain.MessagePlanApprovalReq:
		return fmt.Sprintf("%s 请求评审计划：%s", from, m.Summary)
	case teamdomain.MessageBroadcast:
		return fmt.Sprintf("%s（广播）：%s", from, firstNonEmpty(m.Summary, m.Text))
	default:
		return fmt.Sprintf("%s：%s", from, firstNonEmpty(m.Summary, m.Text))
	}
}
