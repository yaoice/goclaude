package tools

import (
	"context"
	"fmt"

	"github.com/anthropics/goclaude/internal/application"
	"github.com/anthropics/goclaude/internal/domain/tool"
)

// SkillTool 让 LLM 主动加载 skill 全文
//
// 与 src/tools/SkillTool/SkillTool.ts 对齐：
//   - wire name 为 "Skill"，与 src `SKILL_TOOL_NAME` 一致
//   - 输入 name（必填）+ args（可选，透传给 ${ARGS}）
//   - 返回 skill body（已做占位符替换，例如 ${CLAUDE_PROJECT_DIR}/${ARGS} 等）
//   - 找不到 skill 时返回 IsError 结果，让模型可以自我纠偏
//
// 与 SkillService 的关系：本工具是"模型侧"入口，src 中还有"用户侧" slash 命令
// 入口（`/<skill-name>`）由 customcmd.go 的 user-defined commands 链路兼顾，
// 两者互补。
type SkillTool struct {
	service *application.SkillService
	cwd     string
	// sessionID 当前会话 ID（用于 ${CLAUDE_SESSION_ID} 占位符）；可为空
	sessionID string
}

// NewSkillTool 构造 SkillTool
//
// service 不能为 nil；调用 Call 时如果 service 为 nil 会返回错误结果。
func NewSkillTool(svc *application.SkillService, cwd, sessionID string) *SkillTool {
	return &SkillTool{service: svc, cwd: cwd, sessionID: sessionID}
}

func (t *SkillTool) Name() string {
	return "Skill"
}

// Aliases 兼容旧/同义名：避免不同 prompt 风格的模型用 "skill" / "skill_load"
// 时调不到。注意 Anthropic 工具名通常大驼峰，所以 "Skill" 是首选。
func (t *SkillTool) Aliases() []string {
	return []string{"skill", "skill_load"}
}

func (t *SkillTool) Description() string {
	return "Load the full body of a registered skill into the conversation. " +
		"The skill list is provided in the system prompt; pass the skill's `name` " +
		"as input and the tool returns the rendered prompt that you should follow. " +
		"Use this when a user request matches a skill's whenToUse description."
}

func (t *SkillTool) IsEnabled() bool                     { return true }
func (t *SkillTool) IsReadOnly(_ tool.Input) bool        { return true }
func (t *SkillTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *SkillTool) Prompt() string                      { return t.Description() }

func (t *SkillTool) ValidateInput(input tool.Input) error {
	if input.GetString("name") == "" {
		return fmt.Errorf("name is required")
	}
	return nil
}

func (t *SkillTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{
				"type": "string",
				"description": "Skill name as advertised in the system prompt " +
					"(e.g. 'commit-message', 'review-pr'). Aliases are accepted.",
			},
			"args": map[string]interface{}{
				"type": "string",
				"description": "Optional arguments string passed via ${ARGS} placeholder",
			},
		},
		"required": []string{"name"},
	}
}

// CheckPermissions skill 加载是只读操作（仅注入 prompt 文本），不需要询问。
func (t *SkillTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

// Call 渲染并返回 skill body
//
// 错误处理：
//   - service 未注入 → IsError 结果（不应在线上发生，仅防御）
//   - skill 不存在 → IsError 结果，错误信息中列出可用 skill 名（前 20 个），
//     让模型能立即自我纠正
func (t *SkillTool) Call(_ context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("SkillTool 未注入 SkillService"), nil
	}
	name := input.GetString("name")
	args := input.GetString("args")

	body, ok := t.service.RenderWith(name, application.RenderContext{
		SessionID:  t.sessionID,
		ProjectDir: t.cwd,
		Cwd:        t.cwd,
		Args:       args,
	})
	if !ok {
		// 列出最多前 20 个可用 skill，提高模型纠偏能力
		all := t.service.List()
		hint := ""
		if len(all) > 0 {
			max := 20
			if len(all) < max {
				max = len(all)
			}
			names := make([]string, 0, max)
			for i := 0; i < max; i++ {
				names = append(names, all[i].Name)
			}
			hint = fmt.Sprintf(" Available: %v", names)
		}
		return tool.NewErrorResult(fmt.Sprintf("skill %q not found.%s", name, hint)), nil
	}
	// 防御：skill 存在但 body 为空（极少见，但避免给模型一段空字符串）
	if body == "" {
		return tool.NewErrorResult(fmt.Sprintf("skill %q has empty body", name)), nil
	}
	out := tool.NewResult(body)
	out.WithMetadata("skill_name", name)
	return out, nil
}
