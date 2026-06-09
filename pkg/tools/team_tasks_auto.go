// Package tools - team_tasks_auto 提供"自然语言建队"的两个便利工具：
//
//   - AutoSetupTeamTool   一键创建团队、添加成员、创建任务
//   - AutoAssignTaskTool  自动将 pending 任务分配给空闲成员
//
// 从 team_tasks.go 拆出以提升可读性；逻辑保持不变。
package tools

import (
	"context"
	"errors"

	"github.com/anthropics/goclaude/pkg/application"
	"github.com/anthropics/goclaude/pkg/domain/team"
	"github.com/anthropics/goclaude/pkg/domain/tool"
)

// =============================================================================
// AutoSetupTeamTool
// =============================================================================

// AutoSetupTeamTool 一键创建团队、添加成员、创建任务。
// 这是对 CodeBuddy 文档中"自然语言创建团队"的简化实现。
type AutoSetupTeamTool struct{ teamToolBase }

func NewAutoSetupTeamTool(svc *application.TeamService, defaultTeam, defaultFrom string) *AutoSetupTeamTool {
	return &AutoSetupTeamTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*AutoSetupTeamTool) Name() string      { return "auto_setup_team" }
func (*AutoSetupTeamTool) Aliases() []string { return []string{"AutoSetupTeam", "setup_team"} }
func (*AutoSetupTeamTool) Description() string {
	return `Automatically set up a team with members and tasks in one go.
	
	WHEN TO USE: Call this tool AFTER parse_team_intent returns success=true.
	The parse_team_intent tool returns a "tool_input" field that contains the exact input for this tool.
	
	Input format:
	- team_name: (required) Name of the team
	- from: (required) Leader agent name (usually "team-lead")
	- members: (optional) Map of "member_name" -> "agent_type" (e.g., {"alice": "researcher", "bob": "coder"})
	- tasks: (optional) Array of task objects with "title", "description", "assigned_to" (e.g., [{"title": "Implement login", "description": "Create login API", "assigned_to": "alice"}])
	
	Example workflow:
	1. User says: "创建团队 Alpha Squad，成员有 alice(researcher) 和 bob(coder)"
	2. Call parse_team_intent with text="创建团队 Alpha Squad..."
	3. Returns: {"success": true, "tool_input": {"team_name": "Alpha Squad", "from": "team-lead", "members": {"alice": "researcher", "bob": "coder"}}}
	4. Call auto_setup_team with the tool_input
	
	After setup, verify with:
	- list_peers: Show all team members
	- list_tasks: Show all tasks
	
	This tool simplifies the 'natural language team creation' workflow.`
}
func (*AutoSetupTeamTool) IsEnabled() bool                     { return true }
func (*AutoSetupTeamTool) IsReadOnly(_ tool.Input) bool        { return false }
func (*AutoSetupTeamTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *AutoSetupTeamTool) Prompt() string                    { return t.Description() }

func (t *AutoSetupTeamTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required")
	}
	if t.from(input) == "" {
		return errors.New("from is required (leader identity)")
	}
	// members 和 tasks 是可选的，如果不提供则只创建空团队
	return nil
}

func (*AutoSetupTeamTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name": map[string]interface{}{"type": "string"},
			"from":      map[string]interface{}{"type": "string"},
			"members":   map[string]interface{}{"type": "object", "description": "Map of member_name -> agent_type"},
			"tasks":     map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "object"}},
		},
		"required": []string{"team_name"},
	}
}

func (*AutoSetupTeamTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *AutoSetupTeamTool) Call(_ context.Context, input tool.Input, uc *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}

	// 构建 AutoSetupTeamInput
	setupInput := application.AutoSetupTeamInput{
		TeamName:    t.team(input),
		LeadAgentID: t.from(input),
		Members:     make(map[string]string),
		Tasks:       make([]team.SharedTask, 0),
	}

	// 解析 members
	if raw, ok := input["members"]; ok {
		if m, ok := raw.(map[string]interface{}); ok {
			for name, typ := range m {
				if s, ok := typ.(string); ok {
					setupInput.Members[name] = s
				}
			}
		}
	}

	// 解析 tasks
	if raw, ok := input["tasks"]; ok {
		if arr, ok := raw.([]interface{}); ok {
			for _, item := range arr {
				if m, ok := item.(map[string]interface{}); ok {
					task := team.SharedTask{
						Title:       getStringFromMap(m, "title"),
						Description: getStringFromMap(m, "description"),
						Status:      team.SharedTaskPending,
					}
					if id := getStringFromMap(m, "id"); id != "" {
						task.ID = id
					}
					if assignedTo := getStringFromMap(m, "assigned_to"); assignedTo != "" {
						task.AssignedTo = assignedTo
						task.Status = team.SharedTaskWorking
					}
					// 解析 depends_on
					if deps, ok := m["depends_on"]; ok {
						task.DependsOn = toStringSlice(deps)
					}
					setupInput.Tasks = append(setupInput.Tasks, task)
				}
			}
		}
	}

	// 执行自动设置
	if err := t.service.AutoSetupTeam(setupInput); err != nil {
		return tool.NewErrorResult("auto_setup_team: " + err.Error()), nil
	}

	// 登记 leader 身份，使上层 REPL 能为这个动态创建的 team 自动处理 inbox。
	// SetLeader 会异步触发 OnTeamCreated → TeamEngine.SpawnMembers，
	// 由 TeamEngine 负责创建 team workspace。
	t.session.SetLeader(setupInput.TeamName)

	out := map[string]interface{}{
		"success":   true,
		"team_name": setupInput.TeamName,
		"members":   len(setupInput.Members),
		"tasks":     len(setupInput.Tasks),
		"message":   "Team set up successfully. Members are starting in the background. Use list_peers and list_tasks to verify.",
	}
	return tool.NewResult(jsonOut(out)), nil
}

// getStringFromMap 从 map 中安全获取字符串值。
func getStringFromMap(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// =============================================================================
// AutoAssignTaskTool
// =============================================================================

// AutoAssignTaskTool 自动将 pending 任务分配给空闲成员。
type AutoAssignTaskTool struct{ teamToolBase }

func NewAutoAssignTaskTool(svc *application.TeamService, defaultTeam, defaultFrom string) *AutoAssignTaskTool {
	return &AutoAssignTaskTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*AutoAssignTaskTool) Name() string      { return "auto_assign_task" }
func (*AutoAssignTaskTool) Aliases() []string { return []string{"AutoAssignTask"} }
func (*AutoAssignTaskTool) Description() string {
	return "Automatically assign a pending task to an idle member. Returns the task_id and assigned member. Use this to distribute work without manual assignment."
}
func (*AutoAssignTaskTool) IsEnabled() bool                     { return true }
func (*AutoAssignTaskTool) IsReadOnly(_ tool.Input) bool        { return false }
func (*AutoAssignTaskTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *AutoAssignTaskTool) Prompt() string                    { return t.Description() }

func (t *AutoAssignTaskTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required")
	}
	return nil
}

func (*AutoAssignTaskTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name": map[string]interface{}{"type": "string"},
		},
	}
}

func (*AutoAssignTaskTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *AutoAssignTaskTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}
	taskID, memberName, err := t.service.AutoAssignTask(t.team(input))
	if err != nil {
		if errors.Is(err, application.ErrTeamNotFound) {
			return tool.NewErrorResult(err.Error()), nil
		}
		return tool.NewErrorResult("auto_assign_task: " + err.Error()), nil
	}
	if taskID == "" {
		return tool.NewResult(jsonOut(map[string]interface{}{
			"success":   false,
			"team_name": t.team(input),
			"message":   "No pending tasks to assign or no idle members available.",
		})), nil
	}
	// 自动分配后主动通知被选中的空闲成员，使其立即得知新任务。
	notified := false
	title, desc := "", ""
	if tk, gerr := t.service.GetTask(t.team(input), taskID); gerr == nil && tk != nil {
		title, desc = tk.Title, tk.Description
	}
	if err := t.service.NotifyTaskAssigned(t.team(input), taskID, title, desc, memberName, team.LeaderName); err == nil {
		notified = true
	}
	return tool.NewResult(jsonOut(map[string]interface{}{
		"success":     true,
		"team_name":   t.team(input),
		"task_id":     taskID,
		"assigned_to": memberName,
		"notified":    notified,
		"message":     "Task automatically assigned to idle member.",
	})), nil
}
