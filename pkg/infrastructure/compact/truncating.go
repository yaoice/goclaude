package compact

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/anthropics/goclaude/pkg/domain/query"
)

// TruncatingCompactor 不依赖 LLM 的本地压缩器
//
// 策略：
//  1. 保留 head（前 N 条）+ tail（末 M 条）
//  2. 中间段被 [boundary]+占位 user 消息替代，写明被截掉的消息数量
//  3. 修复 tool_use 配对：若某 tool_use 被截、其 result 留在 tail，则把 result 也丢弃；
//     若 tail 中的 tool_result 找不到对应 tool_use，则该 tool_result 也丢弃
//
// 用作 SummarizingCompactor 的 fallback，或单独用于离线/无 API 场景。
type TruncatingCompactor struct {
	HeadKeep int
	TailKeep int
	Logger   *slog.Logger
}

// NewTruncatingCompactor 构造默认截断压缩器
func NewTruncatingCompactor() *TruncatingCompactor {
	return &TruncatingCompactor{
		HeadKeep: KeepHeadCount,
		TailKeep: KeepTailCount,
	}
}

// Compact 实现 query.Compactor
func (c *TruncatingCompactor) Compact(_ context.Context, messages []query.Message, _ query.AIProvider) ([]query.Message, error) {
	logger := c.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if len(messages) < MinMessagesToCompact {
		return messages, nil
	}

	headN := c.HeadKeep
	tailN := c.TailKeep
	if headN <= 0 {
		headN = KeepHeadCount
	}
	if tailN <= 0 {
		tailN = KeepTailCount
	}
	if headN+tailN >= len(messages) {
		return messages, nil
	}

	head := messages[:headN]
	mid := messages[headN : len(messages)-tailN]
	tail := messages[len(messages)-tailN:]
	// mid 太短不值得截断（少 1-2 条节省的 token 不够 boundary 自身的开销）
	if len(mid) < MinMidToCompact {
		return messages, nil
	}

	// 头/尾被切后修复 tool_use/tool_result 配对
	tail = stripDanglingToolResults(head, tail)
	head = stripDanglingToolUses(head, tail)

	out := make([]query.Message, 0, len(head)+2+len(tail))
	out = append(out, head...)
	out = append(out, boundaryMessage(fmt.Sprintf("Truncated %d middle messages (no LLM)", len(mid))))
	out = append(out, query.NewTextMessage(query.RoleUser,
		fmt.Sprintf("(prior %d messages omitted to fit context window)", len(mid))))
	out = append(out, tail...)

	logger.Debug("truncating 压缩完成",
		"input_messages", len(messages),
		"truncated", len(mid),
		"output_messages", len(out),
	)
	return out, nil
}

// stripDanglingToolResults 移除 tail 中那些找不到对应 tool_use 的 tool_result block
//
// 不删除整条消息：只删该消息中孤立的 tool_result block；如该消息被掏空则整条移除。
func stripDanglingToolResults(head, tail []query.Message) []query.Message {
	known := make(map[string]bool)
	for _, m := range head {
		for _, b := range m.Content {
			if b.Type == query.ContentTypeToolUse && b.ToolUseID != "" {
				known[b.ToolUseID] = true
			}
		}
	}
	out := make([]query.Message, 0, len(tail))
	for _, m := range tail {
		if m.Role != query.RoleUser {
			out = append(out, m)
			continue
		}
		filtered := make([]query.ContentBlock, 0, len(m.Content))
		for _, b := range m.Content {
			if b.Type == query.ContentTypeToolResult && !known[b.ToolResultID] {
				continue // drop dangling
			}
			filtered = append(filtered, b)
		}
		if len(filtered) == 0 {
			continue
		}
		m.Content = filtered
		out = append(out, m)
	}
	return out
}

// stripDanglingToolUses 移除 head 中没有对应 tool_result 在 tail 中的 tool_use block
//
// （head 通常包含原始用户问题，理论上很少有 tool_use；此函数用于安全网。）
func stripDanglingToolUses(head, tail []query.Message) []query.Message {
	resolved := make(map[string]bool)
	for _, m := range tail {
		for _, b := range m.Content {
			if b.Type == query.ContentTypeToolResult && b.ToolResultID != "" {
				resolved[b.ToolResultID] = true
			}
		}
	}
	out := make([]query.Message, 0, len(head))
	for _, m := range head {
		if m.Role != query.RoleAssistant {
			out = append(out, m)
			continue
		}
		filtered := make([]query.ContentBlock, 0, len(m.Content))
		for _, b := range m.Content {
			if b.Type == query.ContentTypeToolUse && !resolved[b.ToolUseID] {
				continue
			}
			filtered = append(filtered, b)
		}
		if len(filtered) == 0 {
			continue
		}
		m.Content = filtered
		out = append(out, m)
	}
	return out
}
