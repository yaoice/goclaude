// Package tools - team planning tools for Plan-then-Execute architecture.
//
// This file implements the tool interface for Planning Phase operations:
//   - InitiatePlanningTool: Start planning phase with objective
//   - CollectProposalTool: Members submit task proposals
//   - ApprovePlanTool: Leader approves and transitions to Executing
//   - RejectPlanTool: Leader rejects plan with feedback
//   - InitiateReplanTool: Pause execution, return to Planning
//   - GetPlanTool: Retrieve current execution plan
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/yaoice/goclaude/pkg/application"
	"github.com/yaoice/goclaude/pkg/domain/team"
	"github.com/yaoice/goclaude/pkg/domain/tool"
)

// =============================================================================
// InitiatePlanningTool
// =============================================================================

// InitiatePlanningTool 启动 Planning Phase，广播目标给所有成员。
type InitiatePlanningTool struct{ teamToolBase }

func NewInitiatePlanningTool(svc *application.TeamService, defaultTeam, defaultFrom string) *InitiatePlanningTool {
	return &InitiatePlanningTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*InitiatePlanningTool) Name() string      { return "initiate_planning" }
func (*InitiatePlanningTool) Aliases() []string { return []string{"InitiatePlanning"} }
func (*InitiatePlanningTool) Description() string {
	return `Start the Planning Phase for a team. Only the team leader can call this.

WHEN TO USE: Immediately after creating a team (auto_setup_team), to begin collaborative planning.

This tool:
1. Sets the team phase to "planning"
2. Creates an initial execution plan with the given objective
3. Broadcasts the objective to all team members

Members will then review the objective and submit their proposals via collect_proposal.
NO task execution is allowed during the Planning Phase.

Required: team_name, objective (team goal description)`
}
func (*InitiatePlanningTool) IsEnabled() bool                     { return true }
func (*InitiatePlanningTool) IsReadOnly(_ tool.Input) bool        { return false }
func (*InitiatePlanningTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *InitiatePlanningTool) Prompt() string                    { return t.Description() }

func (t *InitiatePlanningTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required")
	}
	if input.GetString("objective") == "" {
		return errors.New("objective is required")
	}
	return nil
}

func (*InitiatePlanningTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name": map[string]interface{}{
				"type":        "string",
				"description": "Name of the team to plan for.",
			},
			"objective": map[string]interface{}{
				"type":        "string",
				"description": "The team's goal/objective to decompose and plan.",
			},
		},
		"required": []string{"team_name", "objective"},
	}
}

func (*InitiatePlanningTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *InitiatePlanningTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}

	in := application.InitiatePlanningInput{
		TeamName:   t.team(input),
		Objective:  input.GetString("objective"),
		LeaderName: t.from(input),
	}

	f, err := t.service.InitiatePlanning(in)
	if err != nil {
		return tool.NewErrorResult("initiate planning: " + err.Error()), nil
	}

	// 广播 plan_consolidate 给所有非 leader 成员
	members := f.NonLeaderMembers()
	planText, _ := application.SerializePlan(f.Plan)
	broadcasted := 0
	for _, m := range members {
		msg := team.NewPlanConsolidate(team.LeaderName, planText,
			fmt.Sprintf("planning: %s", truncateObj(f.Plan.Objective, 50)),
		)
		_, err := t.service.Send(application.SendInput{
			TeamName:   in.TeamName,
			From:       team.LeaderName,
			To:         m.Name,
			Structured: &msg,
		})
		if err == nil {
			broadcasted++
		}
	}

	return tool.NewResult(jsonOut(map[string]interface{}{
		"success":          true,
		"team_name":        in.TeamName,
		"phase":            string(team.PhasePlanning),
		"objective":        in.Objective,
		"members_notified": broadcasted,
		"plan_id":          f.Plan.Tasks, // placeholder — plan has no explicit ID yet
		"next_action":      "Members will review and submit proposals via collect_proposal. Wait for all members to submit, then call approve_plan.",
	})), nil
}

// truncateObj is a helper for truncating strings in output display.
func truncateObj(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// =============================================================================
// CollectProposalTool
// =============================================================================

// CollectProposalTool 成员提交任务提案。
type CollectProposalTool struct{ teamToolBase }

func NewCollectProposalTool(svc *application.TeamService, defaultTeam, defaultFrom string) *CollectProposalTool {
	return &CollectProposalTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*CollectProposalTool) Name() string      { return "collect_proposal" }
func (*CollectProposalTool) Aliases() []string { return []string{"CollectProposal"} }
func (*CollectProposalTool) Description() string {
	return `Submit a task proposal during the Planning Phase. Team members use this to propose tasks for the execution plan.

WHEN TO USE: During the Planning Phase, after reviewing the team objective (plan_consolidate message).

Input: 
- tasks: array of {title, description, depends_on (optional), estimated_complexity (optional)}
- rationale (optional): explanation of your proposed approach

This tool can be called multiple times to incrementally build the plan.
Only works when team is in Planning or Replanning Phase.`
}
func (*CollectProposalTool) IsEnabled() bool                     { return true }
func (*CollectProposalTool) IsReadOnly(_ tool.Input) bool        { return false }
func (*CollectProposalTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *CollectProposalTool) Prompt() string                    { return t.Description() }

func (t *CollectProposalTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required")
	}
	tasksRaw := input["tasks"]
	if tasksRaw == nil {
		return errors.New("tasks is required (array of task proposals)")
	}
	return nil
}

func (*CollectProposalTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name": map[string]interface{}{
				"type":        "string",
				"description": "Name of the team.",
			},
			"from": map[string]interface{}{
				"type":        "string",
				"description": "Your agent name (the proposer).",
			},
			"tasks": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"title":               map[string]interface{}{"type": "string"},
						"description":         map[string]interface{}{"type": "string"},
						"dependsOn":           map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
						"estimatedComplexity": map[string]interface{}{"type": "string", "enum": []string{"low", "medium", "high"}},
					},
				},
			},
			"rationale": map[string]interface{}{
				"type":        "string",
				"description": "Why you propose these tasks and how they fit the team objective.",
			},
		},
		"required": []string{"tasks"},
	}
}

func (*CollectProposalTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *CollectProposalTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}

	memberName := t.from(input)
	teamName := t.team(input)

	// 解析 tasks 数组
	tasksRaw := input["tasks"]
	taskList, ok := tasksRaw.([]interface{})
	if !ok {
		return tool.NewErrorResult("tasks must be a JSON array of task objects"), nil
	}

	proposal := team.PlanProposal{
		Proposer:  memberName,
		Rationale: input.GetString("rationale"),
	}

	for _, item := range taskList {
		taskMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		pt := team.PlannedTask{
			ID:                  team.GenPlanTaskID(),
			Title:               getString(taskMap, "title"),
			Description:         getString(taskMap, "description"),
			ProposedBy:          memberName,
			EstimatedComplexity: getString(taskMap, "estimatedComplexity"),
		}

		// 解析 depends_on
		if deps, ok := taskMap["dependsOn"].([]interface{}); ok {
			for _, d := range deps {
				if ds, ok := d.(string); ok {
					pt.DependsOn = append(pt.DependsOn, ds)
				}
			}
		}

		proposal.Tasks = append(proposal.Tasks, pt)
	}

	if err := t.service.CollectProposal(teamName, memberName, proposal); err != nil {
		return tool.NewErrorResult("collect proposal: " + err.Error()), nil
	}

	return tool.NewResult(jsonOut(map[string]interface{}{
		"success":     true,
		"team_name":   teamName,
		"proposer":    memberName,
		"tasks_count": len(proposal.Tasks),
		"message":     "Proposal submitted. The leader will review and consolidate all proposals before approving the plan.",
	})), nil
}

// getString safely extracts a string from a map.
func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// =============================================================================
// ApprovePlanTool
// =============================================================================

// ApprovePlanTool leader 审批并激活执行计划。
type ApprovePlanTool struct{ teamToolBase }

func NewApprovePlanTool(svc *application.TeamService, defaultTeam, defaultFrom string) *ApprovePlanTool {
	return &ApprovePlanTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*ApprovePlanTool) Name() string      { return "approve_plan" }
func (*ApprovePlanTool) Aliases() []string { return []string{"ApprovePlan"} }
func (*ApprovePlanTool) Description() string {
	return `Validate and approve the execution plan, transitioning the team from Planning to Executing Phase. Only the team leader can call this.

WHEN TO USE: After all members have submitted proposals and you've consolidated them into a coherent plan.

This tool:
1. Validates plan integrity (no circular deps, all tasks assigned, etc.)
2. Sets the plan status to approved
3. Transitions team phase to "executing"
4. Exports planned tasks to the shared task list
5. Notifies assigned members

Required: team_name, assignments (array of {taskId, memberName})

After approval, use start_execution to dispatch tasks to members.`
}
func (*ApprovePlanTool) IsEnabled() bool                     { return true }
func (*ApprovePlanTool) IsReadOnly(_ tool.Input) bool        { return false }
func (*ApprovePlanTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *ApprovePlanTool) Prompt() string                    { return t.Description() }

func (t *ApprovePlanTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required")
	}
	assignmentsRaw := input["assignments"]
	if assignmentsRaw == nil {
		return errors.New("assignments is required (array of {taskId, memberName, role?})")
	}
	return nil
}

func (*ApprovePlanTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name": map[string]interface{}{
				"type": "string",
			},
			"assignments": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"taskId":     map[string]interface{}{"type": "string"},
						"memberName": map[string]interface{}{"type": "string"},
						"role":       map[string]interface{}{"type": "string"},
					},
				},
			},
		},
		"required": []string{"assignments"},
	}
}

func (*ApprovePlanTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *ApprovePlanTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}

	// 解析 assignments
	assignmentsRaw := input["assignments"]
	assignList, ok := assignmentsRaw.([]interface{})
	if !ok {
		return tool.NewErrorResult("assignments must be a JSON array"), nil
	}

	var assignments []team.PlanAssignment
	for _, item := range assignList {
		am, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		assignments = append(assignments, team.PlanAssignment{
			TaskID:     getString(am, "taskId"),
			MemberName: getString(am, "memberName"),
			Role:       getString(am, "role"),
		})
	}

	in := application.ApprovePlanInput{
		TeamName:    t.team(input),
		LeaderName:  t.from(input),
		Assignments: assignments,
	}

	f, err := t.service.ApprovePlan(in)
	if err != nil {
		return tool.NewErrorResult("approve plan: " + err.Error()), nil
	}

	// 序列化计划给 leader 查看
	planJSON, _ := json.MarshalIndent(f.Plan, "", "  ")

	return tool.NewResult(jsonOut(map[string]interface{}{
		"success":           true,
		"team_name":         in.TeamName,
		"phase":             string(f.Phase),
		"plan_status":       string(f.Plan.Status),
		"tasks_count":       len(f.Plan.Tasks),
		"assignments_count": len(f.Plan.Assignments),
		"plan":              string(planJSON),
		"next_action":       "Call start_execution to dispatch approved tasks to assigned members.",
	})), nil
}

// =============================================================================
// RejectPlanTool
// =============================================================================

// RejectPlanTool leader 驳回计划。
type RejectPlanTool struct{ teamToolBase }

func NewRejectPlanTool(svc *application.TeamService, defaultTeam, defaultFrom string) *RejectPlanTool {
	return &RejectPlanTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*RejectPlanTool) Name() string      { return "reject_plan" }
func (*RejectPlanTool) Aliases() []string { return []string{"RejectPlan"} }
func (*RejectPlanTool) Description() string {
	return `Reject the current execution plan with a reason. The team stays in Planning Phase and members can revise their proposals. Only the team leader can call this.

Required: team_name, reason (detailed explanation of why the plan was rejected)`
}
func (*RejectPlanTool) IsEnabled() bool                     { return true }
func (*RejectPlanTool) IsReadOnly(_ tool.Input) bool        { return false }
func (*RejectPlanTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *RejectPlanTool) Prompt() string                    { return t.Description() }

func (t *RejectPlanTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required")
	}
	if input.GetString("reason") == "" {
		return errors.New("reason is required (explain why the plan was rejected)")
	}
	return nil
}

func (*RejectPlanTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name": map[string]interface{}{"type": "string"},
			"reason":    map[string]interface{}{"type": "string", "description": "Why the plan was rejected. Members will see this feedback."},
		},
		"required": []string{"reason"},
	}
}

func (*RejectPlanTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *RejectPlanTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}

	in := application.RejectPlanInput{
		TeamName:   t.team(input),
		LeaderName: t.from(input),
		Reason:     input.GetString("reason"),
	}

	_, err := t.service.RejectPlan(in)
	if err != nil {
		return tool.NewErrorResult("reject plan: " + err.Error()), nil
	}

	return tool.NewResult(jsonOut(map[string]interface{}{
		"success":   true,
		"team_name": in.TeamName,
		"phase":     string(team.PhasePlanning),
		"message":   "Plan rejected. Members have been notified to revise their proposals.",
	})), nil
}

// =============================================================================
// StartExecutionTool
// =============================================================================

// StartExecutionTool 向被分配成员派发任务。
type StartExecutionTool struct{ teamToolBase }

func NewStartExecutionTool(svc *application.TeamService, defaultTeam, defaultFrom string) *StartExecutionTool {
	return &StartExecutionTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*StartExecutionTool) Name() string      { return "start_execution" }
func (*StartExecutionTool) Aliases() []string { return []string{"StartExecution"} }
func (*StartExecutionTool) Description() string {
	return `Dispatch approved tasks to assigned team members, starting the execution. Only call this after approve_plan.

This tool sends task_assign messages to each member with their assigned tasks from the approved plan.
Members will then begin executing their tasks.

Required: team_name`
}
func (*StartExecutionTool) IsEnabled() bool                     { return true }
func (*StartExecutionTool) IsReadOnly(_ tool.Input) bool        { return false }
func (*StartExecutionTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *StartExecutionTool) Prompt() string                    { return t.Description() }

func (t *StartExecutionTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required")
	}
	return nil
}

func (*StartExecutionTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name": map[string]interface{}{"type": "string"},
		},
		"required": []string{"team_name"},
	}
}

func (*StartExecutionTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *StartExecutionTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}

	teamName := t.team(input)

	f, err := t.service.GetTeam(teamName)
	if err != nil {
		return tool.NewErrorResult("get team: " + err.Error()), nil
	}

	if f.Phase != team.PhaseExecuting {
		return tool.NewErrorResult(fmt.Sprintf("team is in %q phase, not executing. Approve the plan first.", f.Phase)), nil
	}

	if f.Plan == nil || f.Plan.Status != team.PlanApproved {
		return tool.NewErrorResult("plan is not approved. Call approve_plan first."), nil
	}

	// 向每个被分配的成员发送 task_assign
	dispatched := 0
	for _, a := range f.Plan.Assignments {
		// 查找任务详情
		var taskTitle, taskDesc string
		for _, pt := range f.Plan.Tasks {
			if pt.ID == a.TaskID {
				taskTitle = pt.Title
				taskDesc = pt.Description
				break
			}
		}

		msg := team.NewTaskAssign(team.LeaderName, a.TaskID, taskTitle, taskDesc)
		_, err := t.service.Send(application.SendInput{
			TeamName:   teamName,
			From:       team.LeaderName,
			To:         a.MemberName,
			Structured: &msg,
		})
		if err != nil {
			// best-effort: continue dispatching remaining tasks
		} else {
			dispatched++
		}
	}

	return tool.NewResult(jsonOut(map[string]interface{}{
		"success":           true,
		"team_name":         teamName,
		"phase":             string(f.Phase),
		"tasks_dispatched":  dispatched,
		"total_assignments": len(f.Plan.Assignments),
		"message":           fmt.Sprintf("Dispatched %d tasks. Members will begin execution. Monitor with get_team_status.", dispatched),
	})), nil
}

// =============================================================================
// InitiateReplanTool
// =============================================================================

// InitiateReplanTool 暂停执行，返回 Planning Phase。
type InitiateReplanTool struct{ teamToolBase }

func NewInitiateReplanTool(svc *application.TeamService, defaultTeam, defaultFrom string) *InitiateReplanTool {
	return &InitiateReplanTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*InitiateReplanTool) Name() string      { return "initiate_replan" }
func (*InitiateReplanTool) Aliases() []string { return []string{"InitiateReplan"} }
func (*InitiateReplanTool) Description() string {
	return `Pause execution and return to the Planning Phase after a task failure. Only the team leader can call this.

WHEN TO USE: When a task fails during execution and the error is non-recoverable within the current plan.

This tool:
1. Sets team phase to "replanning" then "planning"
2. Preserves completed task results
3. Marks the failed task as blocked
4. Broadcasts replan context to all members for revision

Required: team_name, failed_task_id, failed_member, failure_reason

After this, members will submit revised proposals via collect_proposal.`
}
func (*InitiateReplanTool) IsEnabled() bool                     { return true }
func (*InitiateReplanTool) IsReadOnly(_ tool.Input) bool        { return false }
func (*InitiateReplanTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *InitiateReplanTool) Prompt() string                    { return t.Description() }

func (t *InitiateReplanTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required")
	}
	if input.GetString("failed_task_id") == "" {
		return errors.New("failed_task_id is required")
	}
	if input.GetString("failed_member") == "" {
		return errors.New("failed_member is required")
	}
	if input.GetString("failure_reason") == "" {
		return errors.New("failure_reason is required")
	}
	return nil
}

func (*InitiateReplanTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name":      map[string]interface{}{"type": "string"},
			"failed_task_id": map[string]interface{}{"type": "string", "description": "ID of the task that failed."},
			"failed_member":  map[string]interface{}{"type": "string", "description": "Name of the member whose task failed."},
			"failure_reason": map[string]interface{}{"type": "string", "description": "Detailed explanation of the failure."},
		},
		"required": []string{"failed_task_id", "failed_member", "failure_reason"},
	}
}

func (*InitiateReplanTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *InitiateReplanTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}

	in := application.InitiateReplanInput{
		TeamName:      t.team(input),
		FailedTaskID:  input.GetString("failed_task_id"),
		FailedMember:  input.GetString("failed_member"),
		FailureReason: input.GetString("failure_reason"),
	}

	f, err := t.service.InitiateReplan(in)
	if err != nil {
		return tool.NewErrorResult("initiate replan: " + err.Error()), nil
	}

	return tool.NewResult(jsonOut(map[string]interface{}{
		"success":      true,
		"team_name":    in.TeamName,
		"phase":        string(f.Phase),
		"replan_count": f.ReplanCount,
		"message":      "Replan initiated. Members have been notified. They should submit revised proposals via collect_proposal.",
		"next_action":  "Wait for members to submit revised proposals, then call approve_plan again.",
	})), nil
}

// =============================================================================
// GetPlanTool
// =============================================================================

// GetPlanTool 获取当前执行计划。
type GetPlanTool struct{ teamToolBase }

func NewGetPlanTool(svc *application.TeamService, defaultTeam, defaultFrom string) *GetPlanTool {
	return &GetPlanTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*GetPlanTool) Name() string      { return "get_plan" }
func (*GetPlanTool) Aliases() []string { return []string{"GetPlan"} }
func (*GetPlanTool) Description() string {
	return `Retrieve the current execution plan for a team. Shows plan objective, tasks, assignments, status, and validation summary.

Use this to review the plan before approving or to check current state during replanning.`
}
func (*GetPlanTool) IsEnabled() bool                     { return true }
func (*GetPlanTool) IsReadOnly(_ tool.Input) bool        { return true }
func (*GetPlanTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *GetPlanTool) Prompt() string                    { return t.Description() }

func (t *GetPlanTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required")
	}
	return nil
}

func (*GetPlanTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name": map[string]interface{}{"type": "string"},
		},
	}
}

func (*GetPlanTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *GetPlanTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}

	teamName := t.team(input)
	plan, err := t.service.GetExecutionPlan(teamName)
	if err != nil {
		return tool.NewErrorResult("get plan: " + err.Error()), nil
	}

	if plan == nil {
		return tool.NewResult(jsonOut(map[string]interface{}{
			"success":   true,
			"team_name": teamName,
			"has_plan":  false,
			"message":   "No plan exists yet. Call initiate_planning to start the Planning Phase.",
		})), nil
	}

	// 获取成员列表用于验证摘要
	f, _ := t.service.GetTeam(teamName)
	var memberNames []string
	if f != nil {
		for _, m := range f.NonLeaderMembers() {
			memberNames = append(memberNames, m.Name)
		}
	}

	summary := team.SummarizeValidation(plan, memberNames)

	// 格式化输出
	planJSON, _ := json.MarshalIndent(plan, "", "  ")

	return tool.NewResult(jsonOut(map[string]interface{}{
		"success":   true,
		"team_name": teamName,
		"plan":      string(planJSON),
		"validation": map[string]interface{}{
			"valid":             summary.Valid,
			"total_tasks":       summary.TotalTasks,
			"total_assignments": summary.TotalAssignments,
			"unassigned_tasks":  summary.UnassignedTasks,
			"errors":            summary.Errors,
			"warnings":          summary.Warnings,
		},
	})), nil
}
