// Package tools - team_tasks 提供 4 个任务列表管理工具：
//
//   - CreateTaskTool     在团队任务列表中创建一个新任务
//   - UpdateTaskTool     更新团队任务列表中指定任务的状态/分配/描述
//   - ListTasksTool      列出团队任务列表（可选按状态过滤）
//   - GetTaskTool        查看单个任务的详细信息
//   - ClaimTaskTool      成员自主认领一个 pending 任务
//   - DeleteTaskTool     从任务列表中删除指定任务
//
// 这些工具实现 CodeBuddy 官方 agent-teams 文档中的"共享任务列表"核心功能，
// 与 message-based 的 assign_task/report_task 配合实现完整的任务分配闭环。
package tools

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/anthropics/goclaude/pkg/application"
	"github.com/anthropics/goclaude/pkg/domain/team"
	"github.com/anthropics/goclaude/pkg/domain/tool"
)

// =============================================================================
// CreateTaskTool
// =============================================================================

// CreateTaskTool 在团队任务列表中创建一个新任务。
type CreateTaskTool struct{ teamToolBase }

func NewCreateTaskTool(svc *application.TeamService, defaultTeam, defaultFrom string) *CreateTaskTool {
	return &CreateTaskTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*CreateTaskTool) Name() string      { return "create_task" }
func (*CreateTaskTool) Aliases() []string { return []string{"CreateTask"} }
func (*CreateTaskTool) Description() string {
	return "Create a new task in the team's shared task list. Returns the task_id. Use this instead of just send_message for proper task tracking."
}
func (*CreateTaskTool) IsEnabled() bool                     { return true }
func (*CreateTaskTool) IsReadOnly(_ tool.Input) bool        { return false }
func (*CreateTaskTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *CreateTaskTool) Prompt() string                    { return t.Description() }

func (t *CreateTaskTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required (or join a team first)")
	}
	if input.GetString("title") == "" {
		return errors.New("title is required (5-10 word task title)")
	}
	return nil
}

func (*CreateTaskTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name":   map[string]interface{}{"type": "string"},
			"from":        map[string]interface{}{"type": "string", "description": "Creator agent name. Defaults to this session's identity."},
			"task_id":     map[string]interface{}{"type": "string", "description": "Stable task id; auto-generated if omitted."},
			"title":       map[string]interface{}{"type": "string", "description": "5-10 word task title."},
			"description": map[string]interface{}{"type": "string", "description": "Full task description / requirements."},
			"assigned_to": map[string]interface{}{"type": "string", "description": "Member name to assign to (optional; leave empty for unassigned)."},
			"depends_on":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Task IDs this task depends on (optional)."},
		},
		"required": []string{"title"},
	}
}

func (*CreateTaskTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *CreateTaskTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}
	taskID := input.GetString("task_id")
	if taskID == "" {
		taskID = team.GenTaskID()
	}
	now := time.Now().UnixMilli()
	st := team.SharedTaskPending
	if input.GetString("assigned_to") != "" {
		st = team.SharedTaskWorking
	}

	// 处理 depends_on 数组
	var dependsOn []string
	if raw, ok := input["depends_on"]; ok {
		dependsOn = toStringSlice(raw)
	}

	task := team.SharedTask{
		ID:          taskID,
		Title:       input.GetString("title"),
		Description: input.GetString("description"),
		Status:      st,
		AssignedTo:  input.GetString("assigned_to"),
		DependsOn:   dependsOn,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := t.service.CreateTask(t.team(input), task); err != nil {
		if errors.Is(err, application.ErrTeamNotFound) || errors.Is(err, application.ErrMemberNotFound) {
			return tool.NewErrorResult(err.Error()), nil
		}
		return tool.NewErrorResult("create_task: " + err.Error()), nil
	}
	// 闭合调度回路：若任务已指派给某成员，主动把 task_assign 推送到其 inbox，
	// 使被分配者被"唤醒"而非盲目轮询。失败仅忽略（best-effort）。
	notified := false
	if at := task.AssignedTo; at != "" {
		if err := t.service.NotifyTaskAssigned(t.team(input), taskID, task.Title, task.Description, at, t.from(input)); err == nil {
			notified = true
		}
	}
	return tool.NewResult(jsonOut(map[string]interface{}{
		"success":   true,
		"team_name": t.team(input),
		"task_id":   taskID,
		"status":    string(st),
		"notified":  notified,
	})), nil
}

// =============================================================================
// UpdateTaskTool
// =============================================================================

// UpdateTaskTool 更新团队任务列表中指定任务。
type UpdateTaskTool struct{ teamToolBase }

func NewUpdateTaskTool(svc *application.TeamService, defaultTeam, defaultFrom string) *UpdateTaskTool {
	return &UpdateTaskTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*UpdateTaskTool) Name() string      { return "update_task" }
func (*UpdateTaskTool) Aliases() []string { return []string{"UpdateTask"} }
func (*UpdateTaskTool) Description() string {
	return "Update a task in the team's shared task list by task_id. Can update status, assigned_to, title, description, result."
}
func (*UpdateTaskTool) IsEnabled() bool                     { return true }
func (*UpdateTaskTool) IsReadOnly(_ tool.Input) bool        { return false }
func (*UpdateTaskTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *UpdateTaskTool) Prompt() string                    { return t.Description() }

func (t *UpdateTaskTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required")
	}
	if input.GetString("task_id") == "" {
		return errors.New("task_id is required")
	}
	st := team.SharedTaskStatus(input.GetString("status"))
	if input.GetString("status") != "" && !st.IsValid() {
		return fmt.Errorf("invalid status %q (allowed: pending, working, completed, blocked)", st)
	}
	return nil
}

func (*UpdateTaskTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name":   map[string]interface{}{"type": "string"},
			"from":        map[string]interface{}{"type": "string"},
			"task_id":     map[string]interface{}{"type": "string"},
			"title":       map[string]interface{}{"type": "string"},
			"description": map[string]interface{}{"type": "string"},
			"status":      map[string]interface{}{"type": "string", "enum": []string{"pending", "working", "completed", "blocked"}},
			"assigned_to": map[string]interface{}{"type": "string"},
			"result":      map[string]interface{}{"type": "string", "description": "Task result summary (for completed tasks)."},
		},
		"required": []string{"task_id"},
	}
}

func (*UpdateTaskTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *UpdateTaskTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}
	taskID := input.GetString("task_id")
	st := team.SharedTaskStatus(input.GetString("status"))
	updated := false
	err := t.service.UpdateTask(t.team(input), taskID, func(tsk *team.SharedTask) {
		if input.GetString("title") != "" {
			tsk.Title = input.GetString("title")
			updated = true
		}
		if input.GetString("description") != "" {
			tsk.Description = input.GetString("description")
			updated = true
		}
		if input.GetString("status") != "" {
			tsk.Status = st
			updated = true
		}
		if _, ok := input["assigned_to"]; ok {
			tsk.AssignedTo = input.GetString("assigned_to")
			updated = true
		}
		if input.GetString("result") != "" {
			tsk.Result = input.GetString("result")
			updated = true
		}
		if updated {
			tsk.UpdatedAt = time.Now().UnixMilli()
		}
	})
	if err != nil {
		if errors.Is(err, application.ErrTeamNotFound) || errors.Is(err, application.ErrMemberNotFound) {
			return tool.NewErrorResult(err.Error()), nil
		}
		return tool.NewErrorResult("update_task: " + err.Error()), nil
	}
	// 若本次更新设置了非空 assigned_to，把任务推送到新被分配者的 inbox。
	// 标题/描述优先取本次入参，缺省则回读当前任务，保证通知内容可读。
	notified := false
	if at := input.GetString("assigned_to"); at != "" {
		title, desc := input.GetString("title"), input.GetString("description")
		if title == "" {
			if tk, gerr := t.service.GetTask(t.team(input), taskID); gerr == nil && tk != nil {
				title, desc = tk.Title, tk.Description
			}
		}
		if err := t.service.NotifyTaskAssigned(t.team(input), taskID, title, desc, at, t.from(input)); err == nil {
			notified = true
		}
	}
	return tool.NewResult(jsonOut(map[string]interface{}{
		"success":   true,
		"team_name": t.team(input),
		"task_id":   taskID,
		"updated":   updated,
		"notified":  notified,
	})), nil
}

// =============================================================================
// ListTasksTool
// =============================================================================

// ListTasksTool 列出团队任务列表（可选按状态过滤）。
type ListTasksTool struct{ teamToolBase }

func NewListTasksTool(svc *application.TeamService, defaultTeam, defaultFrom string) *ListTasksTool {
	return &ListTasksTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*ListTasksTool) Name() string      { return "list_tasks" }
func (*ListTasksTool) Aliases() []string { return []string{"ListTasks", "tasks"} }
func (*ListTasksTool) Description() string {
	return "List tasks in the team's shared task list. Optionally filter by status: pending, working, completed, blocked. Also shows dependency info."
}
func (*ListTasksTool) IsEnabled() bool                     { return true }
func (*ListTasksTool) IsReadOnly(_ tool.Input) bool        { return true }
func (*ListTasksTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *ListTasksTool) Prompt() string                    { return t.Description() }

func (t *ListTasksTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required")
	}
	st := team.SharedTaskStatus(input.GetString("status"))
	if input.GetString("status") != "" && !st.IsValid() {
		return fmt.Errorf("invalid status %q (allowed: pending, working, completed, blocked)", st)
	}
	return nil
}

func (*ListTasksTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name": map[string]interface{}{"type": "string"},
			"status":    map[string]interface{}{"type": "string", "enum": []string{"pending", "working", "completed", "blocked"}},
		},
	}
}

func (*ListTasksTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *ListTasksTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}
	var filter []team.SharedTaskStatus
	if s := input.GetString("status"); s != "" {
		filter = append(filter, team.SharedTaskStatus(s))
	}
	tasks, err := t.service.ListTasks(t.team(input), filter...)
	if err != nil {
		if errors.Is(err, application.ErrTeamNotFound) {
			return tool.NewErrorResult(err.Error()), nil
		}
		return tool.NewErrorResult("list_tasks: " + err.Error()), nil
	}
	rendered := make([]map[string]interface{}, 0, len(tasks))
	for _, tk := range tasks {
		entry := map[string]interface{}{
			"id":          tk.ID,
			"title":       tk.Title,
			"status":      string(tk.Status),
			"assigned_to": tk.AssignedTo,
		}
		if tk.Description != "" {
			entry["description"] = tk.Description
		}
		if len(tk.DependsOn) > 0 {
			entry["depends_on"] = tk.DependsOn
		}
		if tk.Result != "" {
			entry["result"] = tk.Result
		}
		rendered = append(rendered, entry)
	}
	return tool.NewResult(jsonOut(map[string]interface{}{
		"team_name": t.team(input),
		"count":     len(rendered),
		"tasks":     rendered,
	})), nil
}

// =============================================================================
// GetTaskTool
// =============================================================================

// GetTaskTool 查看单个任务的详细信息。
type GetTaskTool struct{ teamToolBase }

func NewGetTaskTool(svc *application.TeamService, defaultTeam, defaultFrom string) *GetTaskTool {
	return &GetTaskTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*GetTaskTool) Name() string      { return "get_task" }
func (*GetTaskTool) Aliases() []string { return []string{"GetTask"} }
func (*GetTaskTool) Description() string {
	return "Get details of a single task by task_id from the team's shared task list."
}
func (*GetTaskTool) IsEnabled() bool                     { return true }
func (*GetTaskTool) IsReadOnly(_ tool.Input) bool        { return true }
func (*GetTaskTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *GetTaskTool) Prompt() string                    { return t.Description() }

func (t *GetTaskTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required")
	}
	if input.GetString("task_id") == "" {
		return errors.New("task_id is required")
	}
	return nil
}

func (*GetTaskTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name": map[string]interface{}{"type": "string"},
			"task_id":   map[string]interface{}{"type": "string"},
		},
		"required": []string{"task_id"},
	}
}

func (*GetTaskTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *GetTaskTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}
	tk, err := t.service.GetTask(t.team(input), input.GetString("task_id"))
	if err != nil {
		if errors.Is(err, application.ErrTeamNotFound) || errors.Is(err, application.ErrMemberNotFound) {
			return tool.NewErrorResult(err.Error()), nil
		}
		return tool.NewErrorResult("get_task: " + err.Error()), nil
	}
	entry := map[string]interface{}{
		"id":          tk.ID,
		"title":       tk.Title,
		"description": tk.Description,
		"status":      string(tk.Status),
		"assigned_to": tk.AssignedTo,
		"depends_on":  tk.DependsOn,
		"result":      tk.Result,
		"created_at":  tk.CreatedAt,
		"updated_at":  tk.UpdatedAt,
	}
	return tool.NewResult(jsonOut(map[string]interface{}{
		"team_name": t.team(input),
		"task":      entry,
	})), nil
}

// =============================================================================
// ClaimTaskTool
// =============================================================================

// ClaimTaskTool 成员自主认领一个 pending 任务。
type ClaimTaskTool struct{ teamToolBase }

func NewClaimTaskTool(svc *application.TeamService, defaultTeam, defaultFrom string) *ClaimTaskTool {
	return &ClaimTaskTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*ClaimTaskTool) Name() string      { return "claim_task" }
func (*ClaimTaskTool) Aliases() []string { return []string{"ClaimTask"} }
func (*ClaimTaskTool) Description() string {
	return "Claim a pending/unassigned task. Automatically sets status to working and assigned_to to you. Use list_tasks status=pending to find available tasks."
}
func (*ClaimTaskTool) IsEnabled() bool                     { return true }
func (*ClaimTaskTool) IsReadOnly(_ tool.Input) bool        { return false }
func (*ClaimTaskTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *ClaimTaskTool) Prompt() string                    { return t.Description() }

func (t *ClaimTaskTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required")
	}
	if input.GetString("task_id") == "" {
		return errors.New("task_id is required")
	}
	if t.from(input) == "" {
		return errors.New("from is required (or set defaultFrom on the tool)")
	}
	return nil
}

func (*ClaimTaskTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name": map[string]interface{}{"type": "string"},
			"from":      map[string]interface{}{"type": "string"},
			"task_id":   map[string]interface{}{"type": "string"},
		},
		"required": []string{"task_id"},
	}
}

func (*ClaimTaskTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *ClaimTaskTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}
	if err := t.service.ClaimTask(t.team(input), input.GetString("task_id"), t.from(input)); err != nil {
		if errors.Is(err, application.ErrTeamNotFound) || errors.Is(err, application.ErrMemberNotFound) {
			return tool.NewErrorResult(err.Error()), nil
		}
		return tool.NewErrorResult("claim_task: " + err.Error()), nil
	}
	return tool.NewResult(jsonOut(map[string]interface{}{
		"success":    true,
		"team_name":  t.team(input),
		"task_id":    input.GetString("task_id"),
		"claimed_by": t.from(input),
	})), nil
}

// =============================================================================
// ClaimAnyTaskTool
// =============================================================================

// ClaimAnyTaskTool 让成员认领任意一个 pending 且未分配的任务（自动选择）。
type ClaimAnyTaskTool struct{ teamToolBase }

func NewClaimAnyTaskTool(svc *application.TeamService, defaultTeam, defaultFrom string) *ClaimAnyTaskTool {
	return &ClaimAnyTaskTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*ClaimAnyTaskTool) Name() string      { return "claim_any_task" }
func (*ClaimAnyTaskTool) Aliases() []string { return []string{"ClaimAnyTask", "claim_any"} }
func (*ClaimAnyTaskTool) Description() string {
	return "Claim any available pending task from the team's shared task list. Automatically selects a task for you. Use this when you want to help but don't care which task to work on."
}
func (*ClaimAnyTaskTool) IsEnabled() bool                     { return true }
func (*ClaimAnyTaskTool) IsReadOnly(_ tool.Input) bool        { return false }
func (*ClaimAnyTaskTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *ClaimAnyTaskTool) Prompt() string                    { return t.Description() }

func (t *ClaimAnyTaskTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required")
	}
	if t.from(input) == "" {
		return errors.New("from is required (or set defaultFrom on the tool)")
	}
	return nil
}

func (*ClaimAnyTaskTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name": map[string]interface{}{"type": "string"},
			"from":      map[string]interface{}{"type": "string"},
		},
	}
}

func (*ClaimAnyTaskTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *ClaimAnyTaskTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}
	taskID, err := t.service.ClaimAnyTask(t.team(input), t.from(input))
	if err != nil {
		if errors.Is(err, application.ErrTeamNotFound) || errors.Is(err, application.ErrMemberNotFound) {
			return tool.NewErrorResult(err.Error()), nil
		}
		return tool.NewErrorResult("claim_any_task: " + err.Error()), nil
	}
	if taskID == "" {
		return tool.NewResult(jsonOut(map[string]interface{}{
			"success":   false,
			"team_name": t.team(input),
			"message":   "No available tasks to claim (all tasks are either completed, working, or blocked by dependencies)",
		})), nil
	}
	return tool.NewResult(jsonOut(map[string]interface{}{
		"success":    true,
		"team_name":  t.team(input),
		"task_id":    taskID,
		"claimed_by": t.from(input),
		"message":    "Successfully claimed a task. Use get_task to see details.",
	})), nil
}

// =============================================================================
// DeleteTaskTool
// =============================================================================

// DeleteTaskTool 从任务列表中删除指定任务（leader 专用）。
type DeleteTaskTool struct{ teamToolBase }

func NewDeleteTaskTool(svc *application.TeamService, defaultTeam, defaultFrom string) *DeleteTaskTool {
	return &DeleteTaskTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*DeleteTaskTool) Name() string      { return "delete_task" }
func (*DeleteTaskTool) Aliases() []string { return []string{"DeleteTask"} }
func (*DeleteTaskTool) Description() string {
	return "Delete a task from the team's shared task list. Typically used by the leader after a task is no longer needed."
}
func (*DeleteTaskTool) IsEnabled() bool                     { return true }
func (*DeleteTaskTool) IsReadOnly(_ tool.Input) bool        { return false }
func (*DeleteTaskTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *DeleteTaskTool) Prompt() string                    { return t.Description() }

func (t *DeleteTaskTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required")
	}
	if input.GetString("task_id") == "" {
		return errors.New("task_id is required")
	}
	return nil
}

func (*DeleteTaskTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name": map[string]interface{}{"type": "string"},
			"from":      map[string]interface{}{"type": "string"},
			"task_id":   map[string]interface{}{"type": "string"},
		},
		"required": []string{"task_id"},
	}
}

func (*DeleteTaskTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *DeleteTaskTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}
	if err := t.service.DeleteTask(t.team(input), input.GetString("task_id")); err != nil {
		if errors.Is(err, application.ErrTeamNotFound) || errors.Is(err, application.ErrMemberNotFound) {
			return tool.NewErrorResult(err.Error()), nil
		}
		return tool.NewErrorResult("delete_task: " + err.Error()), nil
	}
	return tool.NewResult(jsonOut(map[string]interface{}{
		"success":   true,
		"team_name": t.team(input),
		"task_id":   input.GetString("task_id"),
	})), nil
}

// toStringSlice 将 interface{} 转为 []string。
func toStringSlice(v interface{}) []string {
	if v == nil {
		return nil
	}
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
