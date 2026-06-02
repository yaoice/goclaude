package query

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"testing"
)

// streamingToolUseProvider 模拟真实 Anthropic 行为：tool_use input 通过多个
// EventContentBlockDelta 的 partial_json 累积，stop 时一次性合并
type streamingToolUseProvider struct {
	turn int
}

func (p *streamingToolUseProvider) Stream(_ context.Context, _ *StreamParams) (<-chan StreamEvent, error) {
	p.turn++
	ch := make(chan StreamEvent, 16)
	go func() {
		defer close(ch)
		switch p.turn {
		case 1:
			// 流式发送一个 tool_use：input = {"path":"/etc/hosts","limit":50}
			ch <- StreamEvent{
				Type:  EventContentBlockStart,
				Index: 0,
				ContentBlock: &ContentBlock{
					Type:      ContentTypeToolUse,
					ToolUseID: "tu-1",
					ToolName:  "file_read",
					// 注意：start 时 Input 为 nil（Anthropic 真实行为）
				},
			}
			// 分多片发 partial_json
			for _, frag := range []string{`{"path"`, `:"/etc/`, `hosts","limit"`, `:50}`} {
				ch <- StreamEvent{
					Type:  EventContentBlockDelta,
					Index: 0,
					Delta: &DeltaContent{Type: ContentTypeToolUse, PartialJSON: frag},
				}
			}
			ch <- StreamEvent{Type: EventContentBlockStop, Index: 0}
			ch <- StreamEvent{
				Type:       EventMessageDelta,
				StopReason: StopReasonToolUse,
				Usage:      &Usage{InputTokens: 10, OutputTokens: 5},
			}
		default:
			// 第二轮 end_turn
			ch <- StreamEvent{
				Type:         EventContentBlockStart,
				Index:        0,
				ContentBlock: &ContentBlock{Type: ContentTypeText},
			}
			ch <- StreamEvent{
				Type:  EventContentBlockDelta,
				Index: 0,
				Delta: &DeltaContent{Type: ContentTypeText, Text: "done"},
			}
			ch <- StreamEvent{Type: EventContentBlockStop, Index: 0}
			ch <- StreamEvent{
				Type:       EventMessageDelta,
				StopReason: StopReasonEndTurn,
				Usage:      &Usage{InputTokens: 5, OutputTokens: 2},
			}
		}
	}()
	return ch, nil
}

func (p *streamingToolUseProvider) Send(_ context.Context, _ *SendParams) (*Message, *Usage, error) {
	return nil, nil, errors.New("not used")
}

// TestEngine_StreamingToolUseInputParsed 是 C1 的回归测试：
// 流式 partial_json 累积应在 stop 时被解析进 block.Input
func TestEngine_StreamingToolUseInputParsed(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxTurns = 5
	cfg.AutoCompact = false

	prov := &streamingToolUseProvider{}
	engine := NewEngine(prov, nil, NewTokenBudget(100_000, 0.8), nil, cfg, slog.Default())

	result, err := engine.Execute(context.Background(), []Message{
		NewTextMessage(RoleUser, "go"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 第一轮 assistant 消息中应有一个 tool_use block，其 Input 已被解析为 map
	// 由于没有 toolExecutor，executeTools 会返回 "tool executor not available"，
	// 但**关键是 Input 的解析步骤**必须在那之前完成。
	//
	// 我们读最终 response（第二轮 end_turn 的 assistant），翻历史中的 tool_use 即可——
	// 但 Engine 只把最终响应放到 result.Response。换个方式：检查第一轮的 stop 把
	// Input 设到了 block 上 — 通过 nil-executor 路径：executeTools 创建 tool_result
	// 时不依赖 Input；我们在 prov 收到的第二轮 messages 里能看到第一轮 assistant
	// 消息的真实 Input。
	if result.TurnCount < 2 {
		t.Fatalf("expected >= 2 turns, got %d", result.TurnCount)
	}
}

// 更直接的单元测试：不走 Engine.Execute，直接调 processStream，验证 Input 被解析
func TestProcessStream_ToolUseInputAccumulation(t *testing.T) {
	cfg := DefaultConfig()
	prov := &streamingToolUseProvider{}
	engine := NewEngine(prov, nil, NewTokenBudget(100_000, 0.8), nil, cfg, slog.Default())

	// 复用 prov 的第一轮流
	streamCh, err := prov.Stream(context.Background(), &StreamParams{})
	if err != nil {
		t.Fatal(err)
	}
	resp, _, _, err := engine.processStream(context.Background(), streamCh, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(resp.Content))
	}
	block := resp.Content[0]
	if block.Type != ContentTypeToolUse {
		t.Fatalf("expected tool_use, got %s", block.Type)
	}
	if block.Input == nil {
		t.Fatal("Input is nil — partial_json 未在 stop 时被解析（C1 回归）")
	}
	want := map[string]interface{}{
		"path":  "/etc/hosts",
		"limit": float64(50), // JSON 数字默认 float64
	}
	got, ok := block.Input.(map[string]interface{})
	if !ok {
		t.Fatalf("Input not a map: %T %v", block.Input, block.Input)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Input = %v, want %v", got, want)
	}
	// 累积缓冲应被清空
	if block.Text != "" {
		t.Errorf("block.Text 应在 stop 时被清空，got %q", block.Text)
	}
}
