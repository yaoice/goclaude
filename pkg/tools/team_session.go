package tools

import "sync"

// TeamSession 记录"当前 goclaude 会话在 team 中的活动身份"，作为 team 工具组
// 与上层运行时（REPL / run 子命令）之间的共享指针。
//
// 它解决一个具体的调度问题：team 名通常在运行时由 team_create / auto_setup_team
// **动态创建**，而启动时的 --team-name / GOCLAUDE_TEAM_NAME flag/env 并不知道
// 这个名字。上层（leader REPL）需要据此在每轮开始时自动处理 inbox
// （TeamService.ProcessLeaderInbox），把 teammate 的进展同步进共享任务列表并
// 注入到对话上下文。TeamSession 由工具在创建/加入 team 时更新，由上层读取，
// 从而让原本"只在测试里被调用"的 ProcessLeaderInbox 在真实运行时生效。
//
// 并发安全：team 工具均 IsConcurrencySafe，可能被主 executor 并发调用，故所有
// 字段访问都受 sync.RWMutex 保护。所有方法对 nil receiver 安全（未注入时退化为
// no-op），让不关心 team 的调用方无需做 nil 判断。
type TeamSession struct {
	mu       sync.RWMutex
	teamName string
	isLeader bool
}

// NewTeamSession 构造一个空会话（尚未加入任何 team）。
func NewTeamSession() *TeamSession { return &TeamSession{} }

// SetLeader 标记本会话为某 team 的 leader（由 team_create / auto_setup_team 调用）。
func (s *TeamSession) SetLeader(teamName string) {
	if s == nil || teamName == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.teamName = teamName
	s.isLeader = true
}

// SetMember 标记本会话以非 leader 身份加入某 team（由 lifecycle 自动 join 时调用）。
func (s *TeamSession) SetMember(teamName string) {
	if s == nil || teamName == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.teamName = teamName
}

// LeaderTeam 返回本会话作为 leader 所属的 team 名；非 leader 或未加入时返回 ""。
//
// 上层据此判断"本轮是否需要为 leader 自动处理 inbox"。
func (s *TeamSession) LeaderTeam() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.isLeader {
		return s.teamName
	}
	return ""
}

// TeamName 返回本会话当前关联的 team 名（无论 leader 与否）。
func (s *TeamSession) TeamName() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.teamName
}

// sessionAttacher 由所有内嵌 teamToolBase 的工具隐式实现，
// RegisterTeamTools 用它把共享 TeamSession 注入每个工具。
type sessionAttacher interface {
	attachSession(*TeamSession)
}

// attachSession 把共享会话指针注入 teamToolBase；由 RegisterTeamTools 统一调用。
func (b *teamToolBase) attachSession(s *TeamSession) { b.session = s }
