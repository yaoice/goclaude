// Package compact 实现 Engine 上下文压缩
//
// 提供两类压缩器：
//   - SummarizingCompactor：调用 AIProvider.Send 让模型生成结构化摘要，
//     然后用单条 user 消息（含 <summary>...</summary>）替代被压缩的历史。
//   - TruncatingCompactor：纯本地实现，按 token 预算裁掉中段，保留首尾，
//     并修复孤立的 tool_use（避免送 API 时报错）。
//
// 对齐 src/services/compact/compact.ts 的核心语义（裁掉 hooks/cache-prefix/
// post-compact-cleanup 等扩展），且保持 Compactor 接口一致：
//
//	Compact(ctx, messages, provider) ([]Message, error)
//
// 调用方为 query.Engine：当 TokenBudget.ShouldCompact 触发时调用。
package compact

import (
	"fmt"
	"strings"

	"github.com/anthropics/goclaude/internal/domain/query"
)

// 公共常量 -----------------------------------------------------------

// CompactBoundaryTag 摘要消息的可见边界标记（用户检索/调试用）
const CompactBoundaryTag = "[compact-boundary]"

// MinMessagesToCompact 少于此数量直接不压缩（不值得，反而损失上下文）
//
// 该值仅作为快速短路；最终是否压缩还会在 partition 后根据 mid 实际长度判定，
// 见 MinMidToCompact。
const MinMessagesToCompact = 6

// MinMidToCompact 中段被压缩的消息少于此值时跳过压缩
//
// 当 mid 太短时，压缩成本（一次 LLM 摘要 + token 开销）超过节省，
// 直接放弃比走完流程更划算。
const MinMidToCompact = 3

// KeepHeadCount 始终保留对话开头的消息数量（通常含原始用户需求）
const KeepHeadCount = 1

// KeepTailCount 始终保留对话末尾的消息数量
const KeepTailCount = 4

// --- 公共工具 ------------------------------------------------------

// hasOpenToolUse 检查 messages 中是否存在没有 tool_result 配对的 tool_use
//
// 用于压缩前/后保证消息序列对 LLM API 合法。
func hasOpenToolUse(messages []query.Message) bool {
	pending := make(map[string]bool)
	for _, m := range messages {
		for _, b := range m.Content {
			switch b.Type {
			case query.ContentTypeToolUse:
				if b.ToolUseID != "" {
					pending[b.ToolUseID] = true
				}
			case query.ContentTypeToolResult:
				delete(pending, b.ToolResultID)
			}
		}
	}
	return len(pending) > 0
}

// CountTokens 估算消息序列的 token 数（沿用 Message.TokenCount 的粗算）
func CountTokens(messages []query.Message) int {
	total := 0
	for i := range messages {
		total += messages[i].TokenCount()
	}
	return total
}

// boundaryMessage 构造一条标记消息，标识"此处发生了压缩"
//
// 内容形如：[compact-boundary] Conversation summarized at <reason>.
// 它本身不被进一步压缩、也不会发给模型解析（模型只看摘要）。
func boundaryMessage(reason string) query.Message {
	return query.NewTextMessage(
		query.RoleUser,
		fmt.Sprintf("%s %s", CompactBoundaryTag, reason),
	)
}

// summaryMessage 构造一条携带摘要的 user 消息
func summaryMessage(summary string) query.Message {
	body := strings.TrimSpace(summary)
	return query.NewTextMessage(
		query.RoleUser,
		"<conversation-summary>\n"+body+"\n</conversation-summary>",
	)
}
