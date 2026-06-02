package application

import (
	"github.com/anthropics/goclaude/internal/domain/query"
)

// FilterIncompleteToolCalls 过滤掉只有 tool_use 但没对应 tool_result 的 assistant 消息。
//
// 对齐 src/tools/AgentTool/runAgent.ts:filterIncompleteToolCalls：
// 若把含未配对 tool_use 的消息发给 LLM，API 会直接拒绝；fork 时必须先做这步清理。
func FilterIncompleteToolCalls(messages []query.Message) []query.Message {
	// 收集所有已有结果的 tool_use_id
	withResult := make(map[string]bool)
	for _, m := range messages {
		if m.Role != query.RoleUser {
			continue
		}
		for _, b := range m.Content {
			if b.Type == query.ContentTypeToolResult && b.ToolResultID != "" {
				withResult[b.ToolResultID] = true
			}
		}
	}

	out := make([]query.Message, 0, len(messages))
	for _, m := range messages {
		if m.Role != query.RoleAssistant {
			out = append(out, m)
			continue
		}
		incomplete := false
		for _, b := range m.Content {
			if b.Type == query.ContentTypeToolUse && b.ToolUseID != "" && !withResult[b.ToolUseID] {
				incomplete = true
				break
			}
		}
		if !incomplete {
			out = append(out, m)
		}
	}
	return out
}
