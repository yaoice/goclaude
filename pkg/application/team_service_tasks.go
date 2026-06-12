package application

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/yaoice/goclaude/pkg/domain/team"
)

// =============================================================================
// 共享任务列表 CRUD（CodeBuddy 官方 agent-teams 核心功能）
//
// 本文件聚合 TeamService 的共享任务列表读写、认领、团队状态摘要，以及
// 自然语言建队的便利封装（AutoSetupTeam / AutoAssignTask / NotifyTaskAssigned）。
// 从 team_service.go 拆出以提升可读性；逻辑保持不变。
// =============================================================================

// CreateTask 在团队任务列表中创建一个新任务。
func (s *TeamService) CreateTask(teamName string, task team.SharedTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.Store.Read(teamName)
	if err != nil {
		return err
	}
	if f == nil {
		return ErrTeamNotFound
	}
	if task.ID == "" {
		task.ID = team.GenTaskID()
	}
	if task.CreatedAt == 0 {
		now := time.Now().UnixMilli()
		task.CreatedAt = now
		task.UpdatedAt = now
	}
	f.Tasks = append(f.Tasks, task)
	return s.Store.Write(f)
}

// UpdateTask 更新团队任务列表中指定 ID 的任务。
func (s *TeamService) UpdateTask(teamName, taskID string, update func(*team.SharedTask)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.Store.Read(teamName)
	if err != nil {
		return err
	}
	if f == nil {
		return ErrTeamNotFound
	}
	for i := range f.Tasks {
		if f.Tasks[i].ID == taskID {
			update(&f.Tasks[i])
			now := time.Now().UnixMilli()
			if f.Tasks[i].UpdatedAt == 0 {
				f.Tasks[i].UpdatedAt = now
			}
			return s.Store.Write(f)
		}
	}
	return fmt.Errorf("task %q not found in team %q", taskID, teamName)
}

// UpsertTask 插入或更新任务（按 ID 匹配）。
func (s *TeamService) UpsertTask(teamName string, task team.SharedTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.Store.Read(teamName)
	if err != nil {
		return err
	}
	if f == nil {
		return ErrTeamNotFound
	}
	now := time.Now().UnixMilli()
	for i := range f.Tasks {
		if f.Tasks[i].ID == task.ID {
			// 保留未更新的字段
			if task.Title != "" {
				f.Tasks[i].Title = task.Title
			}
			if task.Description != "" {
				f.Tasks[i].Description = task.Description
			}
			if task.AssignedTo != "" {
				f.Tasks[i].AssignedTo = task.AssignedTo
			}
			if task.Status != "" {
				f.Tasks[i].Status = task.Status
			}
			if task.Result != "" {
				// 同步 worker 汇报的结果摘要，让共享任务列表反映真实执行产出，
				// 形成"分配 → 执行 → 结果回流"的完整反馈闭环。
				f.Tasks[i].Result = task.Result
			}
			f.Tasks[i].UpdatedAt = now
			return s.Store.Write(f)
		}
	}
	task.CreatedAt = now
	task.UpdatedAt = now
	f.Tasks = append(f.Tasks, task)
	return s.Store.Write(f)
}

// ListTasks 返回团队任务列表，可选按状态过滤。
func (s *TeamService) ListTasks(teamName string, statusFilter ...team.SharedTaskStatus) ([]team.SharedTask, error) {
	f, err := s.Store.Read(teamName)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, ErrTeamNotFound
	}
	if len(statusFilter) == 0 {
		return f.Tasks, nil
	}
	var out []team.SharedTask
	for _, t := range f.Tasks {
		for _, f := range statusFilter {
			if t.Status == f {
				out = append(out, t)
				break
			}
		}
	}
	return out, nil
}

// GetTask 按 ID 获取单个任务。
func (s *TeamService) GetTask(teamName, taskID string) (*team.SharedTask, error) {
	f, err := s.Store.Read(teamName)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, ErrTeamNotFound
	}
	for i := range f.Tasks {
		if f.Tasks[i].ID == taskID {
			return &f.Tasks[i], nil
		}
	}
	return nil, fmt.Errorf("task %q not found", taskID)
}

// ClaimTask 让指定成员认领一个 pending 任务（自主认领）。
func (s *TeamService) ClaimTask(teamName, taskID, memberName string) error {
	return s.UpdateTask(teamName, taskID, func(t *team.SharedTask) {
		t.AssignedTo = memberName
		t.Status = team.SharedTaskWorking
	})
}

// ClaimAnyTask 让指定成员认领任意一个 pending 任务（自动选择）。
// 返回被认领的任务 ID；如果没有可认领的任务，返回空字符串和 nil error。
func (s *TeamService) ClaimAnyTask(teamName, memberName string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.Store.Read(teamName)
	if err != nil {
		return "", err
	}
	if f == nil {
		return "", ErrTeamNotFound
	}

	// 寻找一个 pending 且未分配的任务
	for i := range f.Tasks {
		if f.Tasks[i].Status == team.SharedTaskPending && f.Tasks[i].AssignedTo == "" {
			// 检查依赖是否都满足
			depsOk := true
			for _, depID := range f.Tasks[i].DependsOn {
				depTask, err := s.GetTask(teamName, depID)
				if err != nil || depTask.Status != team.SharedTaskCompleted {
					depsOk = false
					break
				}
			}

			if depsOk {
				f.Tasks[i].AssignedTo = memberName
				f.Tasks[i].Status = team.SharedTaskWorking
				f.Tasks[i].UpdatedAt = time.Now().UnixMilli()

				if err := s.Store.Write(f); err != nil {
					return "", err
				}
				return f.Tasks[i].ID, nil
			}
		}
	}

	return "", nil // 没有可认领的任务
}

// TeamStatus 返回团队整体状态摘要。
type TeamStatus struct {
	TeamName       string
	MemberCount    int
	ActiveMembers  int
	TaskStats      map[team.SharedTaskStatus]int
	PendingTasks   int
	WorkingTasks   int
	CompletedTasks int
	BlockedTasks   int
	Members        []MemberStatusInfo
}

// MemberStatusInfo 是成员状态信息。
type MemberStatusInfo struct {
	Name          string
	Status        team.MemberStatus
	IsActive      bool
	LastHeartbeat int64
}

// GetTeamStatus 获取团队整体状态摘要。
func (s *TeamService) GetTeamStatus(teamName string) (*TeamStatus, error) {
	f, err := s.Store.Read(teamName)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, ErrTeamNotFound
	}

	status := &TeamStatus{
		TeamName:       teamName,
		MemberCount:    len(f.Members),
		TaskStats:      make(map[team.SharedTaskStatus]int),
		PendingTasks:   0,
		WorkingTasks:   0,
		CompletedTasks: 0,
		BlockedTasks:   0,
		Members:        make([]MemberStatusInfo, 0, len(f.Members)),
	}

	// 统计任务状态
	for _, task := range f.Tasks {
		status.TaskStats[task.Status]++
		switch task.Status {
		case team.SharedTaskPending:
			status.PendingTasks++
		case team.SharedTaskWorking:
			status.WorkingTasks++
		case team.SharedTaskCompleted:
			status.CompletedTasks++
		case team.SharedTaskBlocked:
			status.BlockedTasks++
		}
	}

	// 统计成员状态
	now := time.Now()
	for _, m := range f.Members {
		info := MemberStatusInfo{
			Name:     m.Name,
			Status:   m.Status,
			IsActive: m.IsActive,
		}

		// 检查心跳是否过期
		if m.LastHeartbeatAt > 0 {
			info.LastHeartbeat = m.LastHeartbeatAt
			lastBeat := time.UnixMilli(m.LastHeartbeatAt)
			if now.Sub(lastBeat) > team.HeartbeatStaleAfter {
				info.IsActive = false // 心跳过期，标记为非活跃
			}
		}

		if m.IsActive && info.IsActive {
			status.ActiveMembers++
		}

		status.Members = append(status.Members, info)
	}

	return status, nil
}

// DeleteTask 从团队任务列表中删除指定 ID 的任务。
func (s *TeamService) DeleteTask(teamName, taskID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.Store.Read(teamName)
	if err != nil {
		return err
	}
	if f == nil {
		return ErrTeamNotFound
	}
	for i := range f.Tasks {
		if f.Tasks[i].ID == taskID {
			f.Tasks = append(f.Tasks[:i], f.Tasks[i+1:]...)
			return s.Store.Write(f)
		}
	}
	return fmt.Errorf("task %q not found", taskID)
}

// AutoSetupTeamInput 是 AutoSetupTeam 的输入参数。
type AutoSetupTeamInput struct {
	// TeamName 团队名称
	TeamName string
	// LeadAgentID leader 的 agent ID
	LeadAgentID string
	// Members 要自动添加的成员列表（name -> agent_type）
	Members map[string]string
	// Tasks 要自动创建的任务列表
	Tasks []team.SharedTask
}

// AutoSetupTeam 一次性完成：创建团队 + 添加成员 + 创建任务。
// 这是对 CodeBuddy 文档中"自然语言创建团队"的简化实现。
func (s *TeamService) AutoSetupTeam(input AutoSetupTeamInput) error {
	// 1. 创建团队
	createInput := CreateTeamInput{
		Name:          input.TeamName,
		LeadAgentType: "leader",
		LeadCwd:       ".",
	}
	_, err := s.CreateTeam(createInput)
	if err != nil {
		return fmt.Errorf("create team: %w", err)
	}

	// 2. 添加成员
	for name, agentType := range input.Members {
		joinInput := JoinTeamInput{
			TeamName:  input.TeamName,
			AgentName: name,
			AgentType: agentType,
			Cwd:       ".",
		}
		_, _, err := s.JoinTeam(joinInput)
		if err != nil {
			// JoinTeam 内部已处理"成员已存在"的 re-activate 逻辑，
			// 此处任何 error 均为真正的失败（Team 不存在/IO 错误等）。
			return fmt.Errorf("add member %s: %w", name, err)
		}
	}

	// 3. 创建任务
	for _, task := range input.Tasks {
		if task.ID == "" {
			task.ID = team.GenTaskID()
		}
		if task.CreatedAt == 0 {
			now := time.Now().UnixMilli()
			task.CreatedAt = now
			task.UpdatedAt = now
		}
		if err := s.CreateTask(input.TeamName, task); err != nil {
			return fmt.Errorf("create task %s: %w", task.ID, err)
		}
		// 闭合调度回路：自动建队时若任务已带 assigned_to，主动通知被分配者，
		// 避免成员对"自己被指派的任务"一无所知。失败仅记日志，不阻断建队。
		if task.AssignedTo != "" {
			_ = s.NotifyTaskAssigned(input.TeamName, task.ID, task.Title, task.Description, task.AssignedTo, team.LeaderName)
		}
	}

	s.logger.Info("team auto-setup completed", "team", input.TeamName, "members", len(input.Members), "tasks", len(input.Tasks))
	return nil
}

// AutoAssignTask 自动将 pending 任务分配给空闲成员。
// 返回被分配的任务 ID 和成员名；如果没有可分配的任务或空闲成员，返回空字符串。
func (s *TeamService) AutoAssignTask(teamName string) (taskID, memberName string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.Store.Read(teamName)
	if err != nil {
		return "", "", err
	}
	if f == nil {
		return "", "", ErrTeamNotFound
	}

	// 1. 找到所有空闲成员（status=idle 且 IsActive=true）
	var idleMembers []string
	for _, m := range f.Members {
		if m.Name == team.LeaderName {
			continue // 跳过 leader
		}
		if m.IsActive && m.Status == team.StatusIdle {
			idleMembers = append(idleMembers, m.Name)
		}
	}

	if len(idleMembers) == 0 {
		return "", "", nil // 没有空闲成员
	}

	// 2. 找到一个可分配的 pending 任务
	for i := range f.Tasks {
		if f.Tasks[i].Status == team.SharedTaskPending && f.Tasks[i].AssignedTo == "" {
			// 检查依赖是否都满足
			depsOk := true
			for _, depID := range f.Tasks[i].DependsOn {
				depTask, err := s.GetTask(teamName, depID)
				if err != nil || depTask.Status != team.SharedTaskCompleted {
					depsOk = false
					break
				}
			}

			if depsOk {
				// 分配给第一个空闲成员
				f.Tasks[i].AssignedTo = idleMembers[0]
				f.Tasks[i].Status = team.SharedTaskWorking
				f.Tasks[i].UpdatedAt = time.Now().UnixMilli()

				if err := s.Store.Write(f); err != nil {
					return "", "", err
				}

				return f.Tasks[i].ID, idleMembers[0], nil
			}
		}
	}

	return "", "", nil // 没有可分配的任务
}

// NotifyTaskAssigned best-effort 把一条 task_assign 消息推送到被分配者的 inbox，
// 闭合"共享任务列表 → inbox"的调度回路。
//
// 这是本次重构的核心：在改造前，create_task(assigned_to=X) / auto_assign_task /
// update_task 只会修改 config.json 的任务列表，被分配者 X **完全不会被通知**，
// 只能盲目轮询 list_tasks 才能发现自己被指派了任务。对齐 src 端
// spawnTeammate / assignTask 把任务推送进 teammate mailbox 的语义后，
// 三套调度机制（消息式 / 共享列表 / 自动分配）都会主动"唤醒"被分配者。
//
// 语义约定：
//   - assignedTo 或 taskID 为空        → no-op（返回 nil）。
//   - assignedBy 为空                  → 默认 team.LeaderName（最常见的派活者）。
//   - 自分配（assignedTo == assignedBy）→ 跳过（认领者已经知情，无需再唤醒自己）。
//   - 投递失败仅返回 error 供调用方记日志；调用方**不应**因通知失败而让
//     主操作（创建/更新任务）失败——通知是尽力而为的增强，不是关键路径。
func (s *TeamService) NotifyTaskAssigned(teamName, taskID, title, description, assignedTo, assignedBy string) error {
	if strings.TrimSpace(assignedTo) == "" || strings.TrimSpace(taskID) == "" {
		return nil
	}
	if strings.TrimSpace(assignedBy) == "" {
		assignedBy = team.LeaderName
	}
	if assignedTo == assignedBy {
		return nil
	}
	msg := team.NewTaskAssign(assignedBy, taskID, title, description)
	if _, err := s.Send(SendInput{
		TeamName:   teamName,
		From:       assignedBy,
		To:         assignedTo,
		Structured: &msg,
	}); err != nil {
		s.logger.Debug("notify task assignment failed (best-effort)",
			slog.String("team", teamName),
			slog.String("task_id", taskID),
			slog.String("assigned_to", assignedTo),
			slog.Any("err", err),
		)
		return err
	}
	s.logger.Info("task assignment notified",
		slog.String("team", teamName),
		slog.String("task_id", taskID),
		slog.String("assigned_to", assignedTo),
		slog.String("assigned_by", assignedBy),
	)
	return nil
}
