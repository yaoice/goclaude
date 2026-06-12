package compact

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/yaoice/goclaude/pkg/domain/query"
)

// SummarizingCompactor 用 AI 模型生成结构化摘要的压缩器
//
// 实现策略（对齐 src/services/compact/compact.ts 的简化版）：
//  1. 保留对话开头（含原始用户需求）+ 末尾若干条消息
//  2. 把中间段送给 LLM 用专门的 SUMMARY_PROMPT 生成摘要
//  3. 摘要替换原中间段，得到 [head] + [boundary] + [summary] + [tail]
//  4. 任何步骤失败：fallback 到 TruncatingCompactor，保证不破坏对话
//
// 关键约束：压缩后保证不出现孤立 tool_use；若发生则把对应 assistant 消息纳入摘要范围。
type SummarizingCompactor struct {
	// Model 摘要使用的模型；空则沿用 messages 触发轮的模型
	Model string
	// MaxOutputTokens 摘要请求的输出预算
	MaxOutputTokens int
	// CustomInstructions 用户额外摘要指令（追加到 prompt 末尾）
	CustomInstructions string
	// HeadKeep / TailKeep 保留首尾消息数；零值用包内默认
	HeadKeep int
	TailKeep int
	// Tools 摘要请求时附带的工具定义（保持 prompt cache 一致）
	//
	// Anthropic prompt cache 的 key 包含 tools 列表；摘要请求保留与主对话一致的 tools
	// 才能命中缓存。prompt 中已要求模型不调用工具，避免 cache 浪费在工具调用上。
	Tools []query.ToolDefinition
	// Fallback 失败回退的压缩器；nil 则用 NewTruncatingCompactor()
	Fallback query.Compactor
	// Logger 日志
	Logger *slog.Logger
}

// NewSummarizingCompactor 构造 LLM 摘要压缩器
func NewSummarizingCompactor() *SummarizingCompactor {
	return &SummarizingCompactor{
		MaxOutputTokens: 4096,
		HeadKeep:        KeepHeadCount,
		TailKeep:        KeepTailCount,
	}
}

// Compact 实现 query.Compactor
func (c *SummarizingCompactor) Compact(ctx context.Context, messages []query.Message, provider query.AIProvider) ([]query.Message, error) {
	logger := c.logger()

	if len(messages) < MinMessagesToCompact {
		return messages, nil
	}

	head, mid, tail := c.partition(messages)
	if len(mid) < MinMidToCompact {
		return messages, nil // 压缩开销 > 节省
	}
	// 修正 tool_use/tool_result 切分：避免孤立 tool_use 在 LLM 输入中
	head, mid, tail = balanceToolPairs(head, mid, tail)

	summary, err := c.requestSummary(ctx, provider, mid)
	if err != nil {
		logger.Warn("LLM 摘要失败，回退 truncating 压缩", "error", err)
		return c.fallbackCompactor(logger).Compact(ctx, messages, provider)
	}

	out := assembleCompactResult(head, mid, summary, tail)

	// 安全网：防御性检查孤立 tool_use
	if hasOpenToolUse(out) {
		logger.Warn("压缩后仍有孤立 tool_use，回退 truncating 压缩")
		return c.fallbackCompactor(logger).Compact(ctx, messages, provider)
	}

	logger.Debug("LLM 摘要压缩完成",
		"input_messages", len(messages),
		"compacted_messages", len(mid),
		"output_messages", len(out),
		"summary_chars", len(summary),
	)
	return out, nil
}

// logger 返回已解析的 logger（nil 时用 slog.Default()）。
func (c *SummarizingCompactor) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

// fallbackCompactor 返回降级用的 TruncatingCompactor，复用相同的 head/tail 设置。
func (c *SummarizingCompactor) fallbackCompactor(logger *slog.Logger) query.Compactor {
	if c.Fallback != nil {
		return c.Fallback
	}
	return &TruncatingCompactor{
		HeadKeep: c.HeadKeep,
		TailKeep: c.TailKeep,
		Logger:   logger,
	}
}

// assembleCompactResult 组合压缩结果：[head] + [boundary] + [summary] + [tail]
func assembleCompactResult(head []query.Message, mid []query.Message, summary string, tail []query.Message) []query.Message {
	out := make([]query.Message, 0, len(head)+2+len(tail))
	out = append(out, head...)
	out = append(out, boundaryMessage(fmt.Sprintf("LLM summary of %d middle messages", len(mid))))
	out = append(out, summaryMessage(summary))
	out = append(out, tail...)
	return out
}

// partition 切分为 head / mid / tail（独立切片，避免共享底层数组）
func (c *SummarizingCompactor) partition(messages []query.Message) (head, mid, tail []query.Message) {
	headN := c.HeadKeep
	tailN := c.TailKeep
	if headN <= 0 {
		headN = KeepHeadCount
	}
	if tailN <= 0 {
		tailN = KeepTailCount
	}
	if headN+tailN >= len(messages) {
		out := make([]query.Message, len(messages))
		copy(out, messages)
		return out, nil, nil
	}
	head = make([]query.Message, headN)
	copy(head, messages[:headN])
	tail = make([]query.Message, tailN)
	copy(tail, messages[len(messages)-tailN:])
	mid = make([]query.Message, len(messages)-headN-tailN)
	copy(mid, messages[headN:len(messages)-tailN])
	return
}

// balanceToolPairs 调整 head/mid/tail 边界使 tool_use 与 tool_result 不被切散
//
// 算法：
//  1. 若 tail 起始处的某 tool_result 对应的 tool_use 在 mid 中 → 不动
//     （mid 内部完整 + tail 起 result 也合法的情况）
//  2. 若 mid 中存在 tool_use，其 result 在 tail 内 → 将该 result 一并并入 mid
//     （让摘要时看到完整对）
//
// 注：head 中跨界到 mid/tail 的 tool_use 一般不会出现（head 是用户最初消息），
// 即便出现也由 stripDanglingToolUses 在 truncating fallback 中处理。
func balanceToolPairs(head, mid, tail []query.Message) ([]query.Message, []query.Message, []query.Message) {
	// 拷贝到独立 all 切片，避免改写调用方传入的底层数组
	all := make([]query.Message, 0, len(head)+len(mid)+len(tail))
	all = append(all, head...)
	all = append(all, mid...)
	all = append(all, tail...)
	headEnd := len(head)
	tailStart := len(head) + len(mid)

	useIdx := make(map[string]int)
	resultIdx := make(map[string]int)
	for i, m := range all {
		for _, b := range m.Content {
			switch b.Type {
			case query.ContentTypeToolUse:
				if b.ToolUseID != "" {
					if _, ok := useIdx[b.ToolUseID]; !ok {
						useIdx[b.ToolUseID] = i
					}
				}
			case query.ContentTypeToolResult:
				if b.ToolResultID != "" {
					if _, ok := resultIdx[b.ToolResultID]; !ok {
						resultIdx[b.ToolResultID] = i
					}
				}
			}
		}
	}
	for id, useI := range useIdx {
		resI, ok := resultIdx[id]
		if !ok {
			continue
		}
		if useI >= headEnd && useI < tailStart && resI >= tailStart {
			if resI+1 > tailStart {
				tailStart = resI + 1
			}
		}
	}
	if tailStart > len(all) {
		tailStart = len(all)
	}
	if headEnd > tailStart {
		headEnd = tailStart
	}
	// 切片返回基于 all 的子段；调用方此后只读，不再有别名问题
	return all[:headEnd:headEnd], all[headEnd:tailStart:tailStart], all[tailStart:]
}

// requestSummary 调用 provider 的非流式 Send 获取摘要文本
func (c *SummarizingCompactor) requestSummary(ctx context.Context, provider query.AIProvider, mid []query.Message) (string, error) {
	if provider == nil {
		return "", fmt.Errorf("no provider configured")
	}
	system := []query.ContentBlock{
		{Type: query.ContentTypeText, Text: "You are a helpful AI assistant tasked with summarizing conversations."},
	}
	prompt := buildSummaryPrompt(c.CustomInstructions)
	// 把 mid 直接作为对话上下文，再追加一条 user 消息要求生成摘要
	msgs := append([]query.Message{}, mid...)
	msgs = append(msgs, query.NewTextMessage(query.RoleUser, prompt))

	maxTok := c.MaxOutputTokens
	if maxTok <= 0 {
		maxTok = 4096
	}
	params := &query.SendParams{
		Model:     c.Model, // 空时由 provider 用默认模型
		MaxTokens: maxTok,
		Messages:  msgs,
		System:    system,
		Tools:     c.Tools, // 保持 prompt cache 一致；prompt 中明确不调用工具
	}
	resp, _, err := provider.Send(ctx, params)
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", fmt.Errorf("nil response")
	}
	text := strings.TrimSpace(resp.GetTextContent())
	if text == "" {
		return "", fmt.Errorf("empty summary text")
	}
	// 优先抽取 <summary>...</summary>；不存在则用整体文本
	if extracted := extractSummaryBlock(text); extracted != "" {
		return extracted, nil
	}
	return text, nil
}

// extractSummaryBlock 从模型输出中抠出 <summary>...</summary>
func extractSummaryBlock(s string) string {
	const open = "<summary>"
	const close = "</summary>"
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:j])
}

// buildSummaryPrompt 构造摘要 prompt（src compactPrompt 的精简版）
func buildSummaryPrompt(customInstructions string) string {
	var b strings.Builder
	b.WriteString(`CRITICAL: Respond with TEXT ONLY. Do NOT call any tools.

Your task is to create a detailed summary of the conversation so far, paying close attention to the user's explicit requests and your previous actions.
This summary should be thorough in capturing technical details, code patterns, and architectural decisions that would be essential for continuing development work without losing context.

Wrap your final summary inside <summary>...</summary>. The summary should include the following sections:

1. Primary Request and Intent: Capture all of the user's explicit requests and intents in detail.
2. Key Technical Concepts: List all important technical concepts, technologies, and frameworks discussed.
3. Files and Code Sections: Enumerate specific files and code sections examined, modified, or created. Include short representative code snippets where helpful.
4. Errors and Fixes: List all errors encountered and how they were fixed; quote user feedback verbatim where relevant.
5. Problem Solving: Document problems solved and any ongoing troubleshooting efforts.
6. All User Messages: List ALL user messages that are not tool results.
7. Pending Tasks: Outline any pending tasks the user has explicitly asked you to work on.
8. Current Work: Describe in detail what was being worked on immediately before this summary request.
9. Optional Next Step: List the next concrete step that aligns with the user's most recent explicit request.

Return ONLY the <summary>...</summary> block; do not call any tools.`)
	if strings.TrimSpace(customInstructions) != "" {
		b.WriteString("\n\nAdditional Instructions:\n")
		b.WriteString(customInstructions)
	}
	return b.String()
}
