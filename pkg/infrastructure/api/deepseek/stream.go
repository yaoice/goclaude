package deepseek

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/yaoice/goclaude/pkg/domain/query"
)

// parseSSEStream 解析 OpenAI/DeepSeek 风格的 SSE 流，并转换为领域 StreamEvent
//
// 协议特点：
//   - 每个事件是一行 "data: {json}"
//   - 终止标记 "data: [DONE]"
//   - 推理链增量出现在 choices[0].delta.reasoning_content（deepseek-reasoner）
//   - 文本增量出现在 choices[0].delta.content
//   - 工具调用通过 choices[0].delta.tool_calls 流式传递（arguments 为字符串增量）
//   - 最后一个 chunk 可能携带 usage 字段
//
// 转换策略：将"OpenAI 整段流"折叠为 Anthropic 风格的细粒度事件序列：
//
//	MessageStart -> [BlockStart -> BlockDelta* -> BlockStop]+ -> MessageDelta(StopReason) -> MessageStop
func (c *Client) parseSSEStream(ctx context.Context, body io.ReadCloser, events chan<- query.StreamEvent) {
	defer close(events)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	// 提高 buffer 容量，避免长 chunk 截断
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	state := newStreamState()

	// 发送 MessageStart 事件
	if !sendEvent(ctx, events, query.StreamEvent{
		Type: query.EventMessageStart,
		Message: &query.Message{
			Role: query.RoleAssistant,
		},
	}) {
		return
	}

	for scanner.Scan() {
		// 第1层保障：context 取消时 body 已被 client.go 关闭，
		// scanner.Scan() 会返回 false。此处作为第2层兜底，
		// 避免 scanner 在 body 关闭和下一轮读取之间的窗口期未及时退出。
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := scanner.Text()
		if line == "" {
			continue
		}
		// SSE 注释/事件名等非 data 行
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			break
		}

		var chunk StreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			c.logger.Warn("DeepSeek SSE 解析失败", "err", err, "payload", payload)
			continue
		}

		// 最后一个 chunk 可能只有 usage
		if chunk.Usage != nil {
			state.usage = convertUsage(chunk.Usage)
		}

		if len(chunk.Choices) == 0 {
			continue
		}
		ch := chunk.Choices[0]

		// 处理 delta
		if ch.Delta != nil {
			if !state.handleDelta(ctx, events, ch.Delta) {
				return
			}
		}

		// 处理终止
		if ch.FinishReason != "" {
			state.finishReason = mapFinishReason(ch.FinishReason)
		}
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) {
		sendEvent(ctx, events, query.StreamEvent{
			Type:  query.EventError,
			Error: err,
		})
		return
	}

	// 关闭任何仍未关闭的 block
	state.flushBlocks(ctx, events)

	// 发送 MessageDelta（含 stop_reason 和 usage）
	sendEvent(ctx, events, query.StreamEvent{
		Type:       query.EventMessageDelta,
		StopReason: state.finishReason,
		Usage:      state.usage,
	})

	// 发送 MessageStop
	sendEvent(ctx, events, query.StreamEvent{
		Type: query.EventMessageStop,
	})
}

// streamState SSE 解析状态机
//
// 维护：当前文本块索引、各工具调用的索引到 (块索引, 累积参数) 映射
// DeepSeek R1 流式顺序：reasoning_content → content → tool_calls
type streamState struct {
	nextBlockIdx int

	// 推理/思考块状态（deepseek-reasoner 的 reasoning_content）
	thinkingBlockIdx     int
	thinkingBlockStarted bool

	// 文本块状态
	textBlockIdx     int // 当前激活的文本块索引（-1 表示未开启）
	textBlockStarted bool

	// 工具调用：DeepSeek index -> 我方 block index
	toolBlocks map[int]*toolBlockState

	finishReason query.StopReason
	usage        *query.Usage
}

type toolBlockState struct {
	blockIdx  int
	id        string
	name      string
	argBuffer strings.Builder
	started   bool
}

func newStreamState() *streamState {
	return &streamState{
		thinkingBlockIdx: -1,
		textBlockIdx:     -1,
		toolBlocks:       make(map[int]*toolBlockState),
	}
}

// handleDelta 处理一个 delta，按需发出 BlockStart/BlockDelta/BlockStop 事件
//
// DeepSeek R1 流式顺序：reasoning_content → content → tool_calls
// 各块之间自动互斥关闭（上一个块结束时关闭再开始下一个）。
func (s *streamState) handleDelta(ctx context.Context, events chan<- query.StreamEvent, d *Delta) bool {
	// 推理/思考增量（deepseek-reasoner 的 reasoning_content）
	if d.ReasoningContent != "" {
		// 思考块不应与文本/工具并存；若前一个块仍在进行则先关闭
		if s.textBlockStarted {
			if !sendEvent(ctx, events, query.StreamEvent{
				Type:  query.EventContentBlockStop,
				Index: s.textBlockIdx,
			}) {
				return false
			}
			s.textBlockStarted = false
			s.textBlockIdx = -1
		}
		if !s.thinkingBlockStarted {
			s.thinkingBlockIdx = s.nextBlockIdx
			s.nextBlockIdx++
			s.thinkingBlockStarted = true
			if !sendEvent(ctx, events, query.StreamEvent{
				Type:  query.EventContentBlockStart,
				Index: s.thinkingBlockIdx,
				ContentBlock: &query.ContentBlock{
					Type: query.ContentTypeThinking,
				},
			}) {
				return false
			}
		}
		if !sendEvent(ctx, events, query.StreamEvent{
			Type:  query.EventContentBlockDelta,
			Index: s.thinkingBlockIdx,
			Delta: &query.DeltaContent{
				Type:     query.ContentTypeThinking,
				Thinking: d.ReasoningContent,
			},
		}) {
			return false
		}
	}

	// 文本增量
	if d.Content != "" {
		// 若思考块仍在进行，先关闭
		if s.thinkingBlockStarted {
			if !sendEvent(ctx, events, query.StreamEvent{
				Type:  query.EventContentBlockStop,
				Index: s.thinkingBlockIdx,
			}) {
				return false
			}
			s.thinkingBlockStarted = false
			s.thinkingBlockIdx = -1
		}
		if !s.textBlockStarted {
			s.textBlockIdx = s.nextBlockIdx
			s.nextBlockIdx++
			s.textBlockStarted = true
			if !sendEvent(ctx, events, query.StreamEvent{
				Type:  query.EventContentBlockStart,
				Index: s.textBlockIdx,
				ContentBlock: &query.ContentBlock{
					Type: query.ContentTypeText,
				},
			}) {
				return false
			}
		}
		if !sendEvent(ctx, events, query.StreamEvent{
			Type:  query.EventContentBlockDelta,
			Index: s.textBlockIdx,
			Delta: &query.DeltaContent{
				Type: query.ContentTypeText,
				Text: d.Content,
			},
		}) {
			return false
		}
	}

	// 工具调用增量
	for _, tc := range d.ToolCalls {
		state, ok := s.toolBlocks[tc.Index]
		if !ok {
			// 收到新工具调用：先关闭仍开启的文本块与思考块
			if s.thinkingBlockStarted {
				if !sendEvent(ctx, events, query.StreamEvent{
					Type:  query.EventContentBlockStop,
					Index: s.thinkingBlockIdx,
				}) {
					return false
				}
				s.thinkingBlockStarted = false
				s.thinkingBlockIdx = -1
			}
			if s.textBlockStarted {
				if !sendEvent(ctx, events, query.StreamEvent{
					Type:  query.EventContentBlockStop,
					Index: s.textBlockIdx,
				}) {
					return false
				}
				s.textBlockStarted = false
				s.textBlockIdx = -1
			}
			state = &toolBlockState{
				blockIdx: s.nextBlockIdx,
			}
			s.nextBlockIdx++
			s.toolBlocks[tc.Index] = state
		}

		// 首个增量通常带 id 和 name
		if tc.ID != "" {
			state.id = tc.ID
		}
		if tc.Function.Name != "" {
			state.name = tc.Function.Name
		}

		// 当 id 与 name 已就绪时，发出 BlockStart
		if !state.started && state.id != "" && state.name != "" {
			state.started = true
			if !sendEvent(ctx, events, query.StreamEvent{
				Type:  query.EventContentBlockStart,
				Index: state.blockIdx,
				ContentBlock: &query.ContentBlock{
					Type:      query.ContentTypeToolUse,
					ToolUseID: state.id,
					ToolName:  state.name,
				},
			}) {
				return false
			}
		}

		// arguments 增量
		if tc.Function.Arguments != "" {
			state.argBuffer.WriteString(tc.Function.Arguments)
			if state.started {
				if !sendEvent(ctx, events, query.StreamEvent{
					Type:  query.EventContentBlockDelta,
					Index: state.blockIdx,
					Delta: &query.DeltaContent{
						Type:        query.ContentTypeToolUse,
						PartialJSON: tc.Function.Arguments,
					},
				}) {
					return false
				}
			}
		}
	}
	return true
}

// flushBlocks 关闭所有仍未关闭的内容块
func (s *streamState) flushBlocks(ctx context.Context, events chan<- query.StreamEvent) {
	if s.thinkingBlockStarted {
		sendEvent(ctx, events, query.StreamEvent{
			Type:  query.EventContentBlockStop,
			Index: s.thinkingBlockIdx,
		})
		s.thinkingBlockStarted = false
	}
	if s.textBlockStarted {
		sendEvent(ctx, events, query.StreamEvent{
			Type:  query.EventContentBlockStop,
			Index: s.textBlockIdx,
		})
		s.textBlockStarted = false
	}
	for _, state := range s.toolBlocks {
		if state.started {
			sendEvent(ctx, events, query.StreamEvent{
				Type:  query.EventContentBlockStop,
				Index: state.blockIdx,
			})
		}
	}
}

// sendEvent 安全发送事件，遵守 ctx 取消
func sendEvent(ctx context.Context, ch chan<- query.StreamEvent, ev query.StreamEvent) bool {
	select {
	case <-ctx.Done():
		return false
	case ch <- ev:
		return true
	}
}
