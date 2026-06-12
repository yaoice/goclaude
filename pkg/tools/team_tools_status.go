// Package tools - team_tools_status 提供团队状态查询与自然语言意图解析工具，
// 并集中包内共享的小工具（jsonOut / parseMentions）。
//
//   - GetTeamStatusTool   获取团队整体状态摘要（成员/任务统计）
//   - ParseTeamIntentTool 解析自然语言中的建队意图，产出 auto_setup_team 入参
//
// 从 team_tools.go 拆出以提升可读性；逻辑保持不变。
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/yaoice/goclaude/pkg/application"
	"github.com/yaoice/goclaude/pkg/domain/tool"
)

// =============================================================================
// GetTeamStatusTool
// =============================================================================

// GetTeamStatusTool 获取团队整体状态摘要。
type GetTeamStatusTool struct{ teamToolBase }

func NewGetTeamStatusTool(svc *application.TeamService, defaultTeam, defaultFrom string) *GetTeamStatusTool {
	return &GetTeamStatusTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*GetTeamStatusTool) Name() string      { return "get_team_status" }
func (*GetTeamStatusTool) Aliases() []string { return []string{"GetTeamStatus", "team_status"} }
func (*GetTeamStatusTool) Description() string {
	return "Get a summary of the team's overall status, including member count, active members, task statistics, and individual member statuses. Useful for leaders to monitor team progress."
}
func (*GetTeamStatusTool) IsEnabled() bool                     { return true }
func (*GetTeamStatusTool) IsReadOnly(_ tool.Input) bool        { return true }
func (*GetTeamStatusTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *GetTeamStatusTool) Prompt() string                    { return t.Description() }

func (t *GetTeamStatusTool) ValidateInput(input tool.Input) error {
	if t.team(input) == "" {
		return errors.New("team_name is required")
	}
	return nil
}

func (*GetTeamStatusTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"team_name": map[string]interface{}{"type": "string"},
		},
	}
}

func (*GetTeamStatusTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *GetTeamStatusTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("team service not configured"), nil
	}
	status, err := t.service.GetTeamStatus(t.team(input))
	if err != nil {
		if errors.Is(err, application.ErrTeamNotFound) {
			return tool.NewErrorResult(err.Error()), nil
		}
		return tool.NewErrorResult("get_team_status: " + err.Error()), nil
	}

	// 构建输出
	output := map[string]interface{}{
		"team_name":       status.TeamName,
		"member_count":    status.MemberCount,
		"active_members":  status.ActiveMembers,
		"pending_tasks":   status.PendingTasks,
		"working_tasks":   status.WorkingTasks,
		"completed_tasks": status.CompletedTasks,
		"blocked_tasks":   status.BlockedTasks,
		"task_stats":      status.TaskStats,
	}

	// 添加成员详情
	members := make([]map[string]interface{}, 0, len(status.Members))
	for _, m := range status.Members {
		info := map[string]interface{}{
			"name":      m.Name,
			"status":    string(m.Status),
			"is_active": m.IsActive,
		}
		if m.LastHeartbeat > 0 {
			info["last_heartbeat"] = m.LastHeartbeat
		}
		members = append(members, info)
	}
	output["members"] = members

	return tool.NewResult(jsonOut(output)), nil
}

// ----- helpers -----

// jsonOut 编码为缩进 JSON；编码失败时返回 fmt.Sprintf 兜底。
func jsonOut(v interface{}) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%+v", v)
	}
	// 防御：单行/无空白；大多数模型对 JSON 敏感的程度差异不大，indent 易读
	_ = strings.TrimSpace
	return string(b)
}

// parseMentions 从文本中解析 @mention 语法，返回被 @ 的成员名列表。
// 支持格式：@member_name 或 @member-name
func parseMentions(text string) []string {
	var mentions []string
	// 简单的正则：@后跟一个或多个字母、数字、下划线或连字符
	re := regexp.MustCompile(`@([a-zA-Z0-9_-]+)`)
	matches := re.FindAllStringSubmatch(text, -1)
	for _, m := range matches {
		if len(m) > 1 {
			mentions = append(mentions, m[1])
		}
	}
	return mentions
}

// =============================================================================
// ParseTeamIntentTool
// =============================================================================

// ParseTeamIntentTool 解析自然语言中的团队创建意图。
// 这是实现"自然语言触发"的关键工具。
type ParseTeamIntentTool struct{ teamToolBase }

func NewParseTeamIntentTool(svc *application.TeamService, defaultTeam, defaultFrom string) *ParseTeamIntentTool {
	return &ParseTeamIntentTool{teamToolBase{service: svc, defaultTeam: defaultTeam, defaultFrom: defaultFrom}}
}

func (*ParseTeamIntentTool) Name() string      { return "parse_team_intent" }
func (*ParseTeamIntentTool) Aliases() []string { return []string{"ParseTeamIntent"} }
func (*ParseTeamIntentTool) Description() string {
	return `Parse natural language input to extract team setup intent (team name, members, tasks). 
	
	WHEN TO USE: Call this tool IMMEDIATELY when the user's message contains phrases like:
	- Chinese: "创建团队", "建团队", "新建 team", "建立团队"
	- English: "create team", "setup team", "create a team", "setup a team"
	- Any request implying multi-agent coordination with team/members/tasks
	
	This tool extracts structured data (team_name, members map, tasks array) that can be passed to auto_setup_team.
	
	Returns: {"success": true, "tool_input": {...}, "next_action": "Call auto_setup_team with the tool_input"}
	         {"success": false, "message": "No team setup intent detected"}`
}
func (*ParseTeamIntentTool) IsEnabled() bool                     { return true }
func (*ParseTeamIntentTool) IsReadOnly(_ tool.Input) bool        { return true }
func (*ParseTeamIntentTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *ParseTeamIntentTool) Prompt() string                    { return t.Description() }

func (t *ParseTeamIntentTool) ValidateInput(input tool.Input) error {
	if input.GetString("text") == "" {
		return errors.New("text is required (the natural language input to parse)")
	}
	return nil
}

func (*ParseTeamIntentTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"text": map[string]interface{}{"type": "string", "description": "Natural language input to parse for team setup intent"},
		},
		"required": []string{"text"},
	}
}

func (*ParseTeamIntentTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *ParseTeamIntentTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	text := input.GetString("text")
	if text == "" {
		return tool.NewErrorResult("text is required"), nil
	}

	intent := application.ParseTeamSetupIntent(text)
	if intent == nil {
		return tool.NewResult(jsonOut(map[string]interface{}{
			"success": false,
			"message": "No team setup intent detected in the input text.",
		})), nil
	}

	// 转换为工具输入格式
	toolInput := intent.ToToolInput()

	return tool.NewResult(jsonOut(map[string]interface{}{
		"success":     true,
		"intent":      intent,
		"tool_input":  toolInput,
		"next_action": "Call auto_setup_team with the tool_input to create the team.",
	})), nil
}
