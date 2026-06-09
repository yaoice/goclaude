package application

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/anthropics/goclaude/pkg/domain/team"
)

// 本文件聚合 TeamService 的成员生命周期：建队、加入/重新激活、活跃/状态切换、
// 心跳、退出、删除与列举。从 team_service.go 拆出以提升可读性；逻辑保持不变。

// CreateTeamInput 创建 team 的入参。
type CreateTeamInput struct {
	Name          string // 用户原始输入，sanitize 后入盘
	Description   string
	LeadAgentType string // leader 角色名（默认 LeaderName）
	LeadModel     string
	LeadCwd       string
	LeadSessionID string
}

// CreateTeam 创建一个新 team；leader 自动作为第一个 member。
//
// 错误：
//   - 同名 team 已存在 → 返回 ErrTeamExists（调用方可生成新 slug 重试）
//   - 校验失败 → 返回带原因的 error
func (s *TeamService) CreateTeam(in CreateTeamInput) (*team.File, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, errors.New("team name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, err := s.Store.Read(in.Name); err != nil {
		s.logger.Error("create_team: read existing failed", slog.String("team", in.Name), slog.Any("err", err))
		return nil, err
	} else if existing != nil {
		return nil, ErrTeamExists
	}

	leadID := team.FormatAgentID(team.LeaderName, in.Name)
	now := time.Now().UnixMilli()
	f := &team.File{
		Name:          in.Name,
		Description:   in.Description,
		CreatedAt:     now,
		LeadAgentID:   leadID,
		LeadSessionID: in.LeadSessionID,
		Phase:         team.PhasePlanning, // Plan-then-Execute: start in Planning
		MaxReplanAttempts: 3,              // default: max 3 replans
		Members: []team.Member{
			{
				AgentID:         leadID,
				Name:            team.LeaderName,
				AgentType:       nonEmpty(in.LeadAgentType, team.LeaderName),
				Model:           in.LeadModel,
				JoinedAt:        now,
				Cwd:             in.LeadCwd,
				IsActive:        true,
				Status:          team.StatusWorking,
				SessionID:       in.LeadSessionID,
				LastHeartbeatAt: now,
			},
		},
	}
	if err := s.Store.Write(f); err != nil {
		s.logger.Error("create_team: write failed", slog.String("team", in.Name), slog.Any("err", err))
		return nil, err
	}
	s.logger.Info("team created", slog.String("team", in.Name), slog.String("lead_agent_id", leadID))
	return f, nil
}

// JoinTeamInput 加入 team 的入参。
type JoinTeamInput struct {
	TeamName  string
	AgentName string // 必填；leader 不能用此 API 加入（leader 由 CreateTeam 自动建）
	AgentType string
	Model     string
	Cwd       string
	SessionID string
}

// JoinTeam 让一个新 agent 加入已有 team。
//
// 重复加入（同 name）会被识别为"重新激活"——保留旧 AgentID，更新 IsActive=true、
// 刷新 JoinedAt 与心跳，避免 inbox 被孤立。
func (s *TeamService) JoinTeam(in JoinTeamInput) (*team.File, *team.Member, error) {
	if in.AgentName == "" || in.AgentName == team.LeaderName {
		return nil, nil, fmt.Errorf("invalid agent name %q for JoinTeam", in.AgentName)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.requireTeam(in.TeamName)
	if err != nil {
		return nil, nil, err
	}

	now := time.Now().UnixMilli()

	// 已存在 → 重新激活
	if existing := f.FindMemberByName(in.AgentName); existing != nil {
		existing.IsActive = true
		existing.Status = team.StatusWorking
		existing.JoinedAt = now
		existing.LastHeartbeatAt = now
		applyNonEmpty(&existing.Cwd, in.Cwd)
		applyNonEmpty(&existing.Model, in.Model)
		applyNonEmpty(&existing.AgentType, in.AgentType)
		applyNonEmpty(&existing.SessionID, in.SessionID)
		if err := s.Store.Write(f); err != nil {
			return nil, nil, err
		}
		s.logger.Info("agent rejoined team", slog.String("team", in.TeamName), slog.String("agent", in.AgentName))
		return f, existing, nil
	}

	// 新成员
	m := team.Member{
		AgentID:         team.FormatAgentID(in.AgentName, in.TeamName),
		Name:            in.AgentName,
		AgentType:       in.AgentType,
		Model:           in.Model,
		JoinedAt:        now,
		Cwd:             in.Cwd,
		IsActive:        true,
		Status:          team.StatusWorking,
		SessionID:       in.SessionID,
		LastHeartbeatAt: now,
	}
	f.Members = append(f.Members, m)
	if err := s.Store.Write(f); err != nil {
		return nil, nil, err
	}
	s.logger.Info("agent joined team",
		slog.String("team", in.TeamName), slog.String("agent", in.AgentName), slog.String("agent_type", in.AgentType))
	return f, &m, nil
}

// SetMemberActive 把某成员标记为活跃/空闲。
//
// 成员/team 不存在时返回 ErrTeamNotFound / ErrMemberNotFound。
func (s *TeamService) SetMemberActive(teamName, memberName string, active bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, m, err := s.requireTeamAndMember(teamName, memberName)
	if err != nil {
		return err
	}
	if m.IsActive == active {
		return nil
	}
	m.IsActive = active
	// active ↔ idle 联动
	if !active && (m.Status == "" || m.Status == team.StatusWorking) {
		m.Status = team.StatusIdle
	} else if active && m.Status == team.StatusIdle {
		m.Status = team.StatusWorking
	}
	m.LastHeartbeatAt = time.Now().UnixMilli()
	if err := s.Store.Write(f); err != nil {
		return err
	}
	s.logger.Debug("member active toggled",
		slog.String("team", teamName), slog.String("agent", memberName), slog.Bool("active", active))
	return nil
}

// SetMemberStatus 设置成员的细粒度状态（idle/working/blocked/error/done）。
//
// IsActive 由 status 派生：working/blocked → true，其它 → false。
func (s *TeamService) SetMemberStatus(teamName, memberName string, status team.MemberStatus) error {
	if !status.IsValid() {
		return fmt.Errorf("invalid status %q", status)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	f, m, err := s.requireTeamAndMember(teamName, memberName)
	if err != nil {
		return err
	}
	m.Status = status
	m.IsActive = status == team.StatusWorking || status == team.StatusBlocked
	m.LastHeartbeatAt = time.Now().UnixMilli()
	if err := s.Store.Write(f); err != nil {
		return err
	}
	s.logger.Debug("member status set",
		slog.String("team", teamName), slog.String("agent", memberName), slog.String("status", string(status)))
	return nil
}

// Heartbeat 仅刷新 LastHeartbeatAt（不改 IsActive / Status）。
//
// 用于 worker 后台 goroutine 周期性证明"我还活着"。
// 为减少磁盘 IO，调用方应自行做节流（建议 ≥ 30s 一次）。
func (s *TeamService) Heartbeat(teamName, memberName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, m, err := s.requireTeamAndMember(teamName, memberName)
	if err != nil {
		return err
	}
	m.LastHeartbeatAt = time.Now().UnixMilli()
	return s.Store.Write(f)
}

// LeaveTeam 把成员从 team 中移除（不删除 inbox 文件，方便 leader 复盘）。
func (s *TeamService) LeaveTeam(teamName, memberName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, _, err := s.requireTeamAndMember(teamName, memberName)
	if err != nil {
		return err
	}
	// 从 Members 切片中移除；requireTeamAndMember 已确认 member 存在
	f, _ := s.Store.Read(teamName)
	for i, m := range f.Members {
		if m.Name == memberName {
			f.Members = append(f.Members[:i], f.Members[i+1:]...)
			break
		}
	}
	if err := s.Store.Write(f); err != nil {
		return err
	}
	s.logger.Info("member left team",
		slog.String("team", teamName), slog.String("agent", memberName))
	return nil
}

// DeleteTeamOptions 控制清理行为。
type DeleteTeamOptions struct {
	// Force true 时跳过"仍有活跃成员"检查（CLI 显式 --force 时使用）
	Force bool
}

// DeleteTeam 清理 team 目录。
//
// 默认：若仍有非 leader 且 IsActive=true、心跳未过期的成员，
// 返回 ErrTeamHasActiveMembers。
func (s *TeamService) DeleteTeam(teamName string, opt DeleteTeamOptions) (deleted bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.Store.Read(teamName)
	if err != nil {
		return false, err
	}
	if f == nil {
		return false, nil
	}
	if !opt.Force {
		if n := f.ActiveNonLeaderCount(); n > 0 {
			return false, fmt.Errorf("%w: %d active member(s)", ErrTeamHasActiveMembers, n)
		}
	}
	deleted, err = s.Store.Delete(teamName)
	if err != nil {
		s.logger.Error("delete team failed", slog.String("team", teamName), slog.Any("err", err))
		return deleted, err
	}
	if deleted {
		s.logger.Info("team deleted", slog.String("team", teamName), slog.Bool("force", opt.Force))
	}
	return deleted, nil
}

// ListTeams 列出磁盘上所有 team 名（已 sanitize 后的目录名）。
func (s *TeamService) ListTeams() ([]string, error) {
	return s.Store.List()
}

// GetTeam 读取 team file。team 不存在返回 (nil, nil)。
func (s *TeamService) GetTeam(teamName string) (*team.File, error) {
	return s.Store.Read(teamName)
}

// --- 内部 helpers ---

// requireTeam 读取 team file，不存在则返回 ErrTeamNotFound。
func (s *TeamService) requireTeam(name string) (*team.File, error) {
	f, err := s.Store.Read(name)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, ErrTeamNotFound
	}
	return f, nil
}

// requireTeamAndMember 读取 team 并查找指定成员；不存在时返回对应哨兵错误。
func (s *TeamService) requireTeamAndMember(teamName, memberName string) (*team.File, *team.Member, error) {
	f, err := s.requireTeam(teamName)
	if err != nil {
		return nil, nil, err
	}
	m := f.FindMemberByName(memberName)
	if m == nil {
		return nil, nil, ErrMemberNotFound
	}
	return f, m, nil
}

// applyNonEmpty 仅在 val 非空时更新 target。
func applyNonEmpty(target *string, val string) {
	if val != "" {
		*target = val
	}
}
