package application

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/goclaude/pkg/domain/query"
)

// ExtractPathsFromInputs 从一组 tool input map 中收集可能的文件路径
//
// 对齐 src/skills/loadSkillsDir.ts:discoverSkillDirsForPaths 与
// activateConditionalSkillsForPaths 的输入：识别 file_read/file_write/file_edit/
// glob/grep 等常用工具的 path/file_path/paths/pattern 字段，转发给条件 skill 匹配。
func ExtractPathsFromInputs(inputs []map[string]interface{}) []string {
	if len(inputs) == 0 {
		return nil
	}
	var paths []string
	for _, in := range inputs {
		for _, k := range []string{"path", "file_path", "filepath"} {
			if v, ok := in[k]; ok {
				if s, ok := v.(string); ok && s != "" {
					paths = append(paths, s)
				}
			}
		}
		if v, ok := in["pattern"]; ok {
			if s, ok := v.(string); ok && s != "" {
				paths = append(paths, s)
			}
		}
		// 多文件操作：paths 数组
		if v, ok := in["paths"]; ok {
			if arr, ok := v.([]interface{}); ok {
				for _, x := range arr {
					if s, ok := x.(string); ok && s != "" {
						paths = append(paths, s)
					}
				}
			}
		}
	}
	return paths
}

// NewSkillActivationHook 构造一个 AfterToolHook，自动激活匹配的条件 skill
//
// 每轮工具执行结束后：
//  1. 从 tool_use blocks 解析路径
//  2. SkillService.ActivateForPaths 激活匹配的条件 skill
//  3. 渲染激活的 skill 内容作为 user 消息追加到对话历史
//  4. 若提供了 onActivate 回调，对每个激活的 skill 调用它（用于 UI 通知）
//
// 对齐 src commands.ts + skills/loadSkillsDir.ts 中条件 skill 的激活流。
func NewSkillActivationHook(svc *SkillService, sessionID, cwd string, onActivate func(name string)) query.AfterToolHook {
	return func(_ int, toolUses, _ []query.ContentBlock) []query.Message {
		if svc == nil || len(toolUses) == 0 {
			return nil
		}
		inputs := extractInputs(toolUses)
		paths := ExtractPathsFromInputs(inputs)
		if len(paths) == 0 {
			return nil
		}
		activated := svc.ActivateForPaths(paths, cwd)
		if len(activated) == 0 {
			return nil
		}
		extras := make([]query.Message, 0, len(activated))
		for _, name := range activated {
			content, ok := svc.Render(name, sessionID)
			if !ok {
				continue
			}
			text := fmt.Sprintf(
				"<system-reminder>Conditional skill activated: %q (matched recently accessed paths)</system-reminder>\n\n<skill name=%q>\n%s\n</skill>",
				name, name, strings.TrimSpace(content),
			)
			extras = append(extras, query.NewTextMessage(query.RoleUser, text))
			// 回调：通知 UI 层（如 REPL）有 skill 被激活
			if onActivate != nil {
				onActivate(name)
			}
		}
		return extras
	}
}

// extractInputs 从 tool_use blocks 提取 input map
func extractInputs(blocks []query.ContentBlock) []map[string]interface{} {
	inputs := make([]map[string]interface{}, 0, len(blocks))
	for _, b := range blocks {
		if b.Type != query.ContentTypeToolUse {
			continue
		}
		switch v := b.Input.(type) {
		case map[string]interface{}:
			inputs = append(inputs, v)
		case string:
			// 流式工具调用可能积累成 JSON 字符串
			var m map[string]interface{}
			if err := json.Unmarshal([]byte(v), &m); err == nil {
				inputs = append(inputs, m)
			}
		}
	}
	return inputs
}
