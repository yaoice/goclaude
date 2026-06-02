package query

import (
	"context"
	"testing"
)

// mockProvider 模拟AI Provider（用于单元测试）
type mockProvider struct {
	streamFn func(ctx context.Context, params *StreamParams) (<-chan StreamEvent, error)
	sendFn   func(ctx context.Context, params *SendParams) (*Message, *Usage, error)
}

func (m *mockProvider) Stream(ctx context.Context, params *StreamParams) (<-chan StreamEvent, error) {
	if m.streamFn != nil {
		return m.streamFn(ctx, params)
	}
	ch := make(chan StreamEvent)
	close(ch)
	return ch, nil
}

func (m *mockProvider) Send(ctx context.Context, params *SendParams) (*Message, *Usage, error) {
	if m.sendFn != nil {
		return m.sendFn(ctx, params)
	}
	return &Message{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentTypeText, Text: "ok"}}}, &Usage{}, nil
}

func TestEngine_Execute_SimpleResponse(t *testing.T) {
	// 模拟一个简单的文本响应（无工具调用）
	provider := &mockProvider{
		streamFn: func(ctx context.Context, params *StreamParams) (<-chan StreamEvent, error) {
			ch := make(chan StreamEvent, 10)
			go func() {
				defer close(ch)
				ch <- StreamEvent{Type: EventContentBlockStart, Index: 0, ContentBlock: &ContentBlock{Type: ContentTypeText}}
				ch <- StreamEvent{Type: EventContentBlockDelta, Index: 0, Delta: &DeltaContent{Type: ContentTypeText, Text: "Hello!"}}
				ch <- StreamEvent{Type: EventContentBlockStop, Index: 0}
				ch <- StreamEvent{Type: EventMessageDelta, StopReason: StopReasonEndTurn, Usage: &Usage{InputTokens: 10, OutputTokens: 5}}
			}()
			return ch, nil
		},
	}

	budget := NewTokenBudget(200000, 0.8)
	config := DefaultConfig()
	engine := NewEngine(provider, nil, budget, nil, config, nil)

	messages := []Message{NewTextMessage(RoleUser, "Hi")}
	events := make(chan StreamEvent, 100)

	result, err := engine.Execute(context.Background(), messages, events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
	if result.Response == nil {
		t.Fatal("response should not be nil")
	}
	if result.Response.GetTextContent() != "Hello!" {
		t.Errorf("expected 'Hello!', got %q", result.Response.GetTextContent())
	}
	if result.StopReason != StopReasonEndTurn {
		t.Errorf("expected end_turn, got %q", result.StopReason)
	}
	if result.TurnCount != 1 {
		t.Errorf("expected 1 turn, got %d", result.TurnCount)
	}
}

func TestEngine_Execute_ContextCancellation(t *testing.T) {
	// 测试context取消能中断查询循环
	provider := &mockProvider{
		streamFn: func(ctx context.Context, params *StreamParams) (<-chan StreamEvent, error) {
			ch := make(chan StreamEvent)
			go func() {
				defer close(ch)
				// 等待context取消
				<-ctx.Done()
			}()
			return ch, nil
		},
	}

	budget := NewTokenBudget(200000, 0.8)
	engine := NewEngine(provider, nil, budget, nil, DefaultConfig(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	messages := []Message{NewTextMessage(RoleUser, "Hi")}
	events := make(chan StreamEvent, 100)

	// 立即取消
	cancel()

	_, err := engine.Execute(ctx, messages, events)
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

func TestTokenBudget_ShouldCompact(t *testing.T) {
	budget := NewTokenBudget(100000, 0.8)

	// 初始状态不应触发压缩
	if budget.ShouldCompact() {
		t.Error("should not compact initially")
	}

	// 使用量低于阈值
	budget.RecordUsage(&Usage{InputTokens: 50000, OutputTokens: 1000})
	if budget.ShouldCompact() {
		t.Error("should not compact at 50%")
	}

	// 使用量超过阈值
	budget.RecordUsage(&Usage{InputTokens: 85000, OutputTokens: 2000})
	if !budget.ShouldCompact() {
		t.Error("should compact at 85%")
	}

	// 重置后不应触发
	budget.Reset()
	if budget.ShouldCompact() {
		t.Error("should not compact after reset")
	}
}

func TestTokenBudget_RemainingTokens(t *testing.T) {
	budget := NewTokenBudget(100000, 0.8)

	if budget.RemainingTokens() != 100000 {
		t.Errorf("expected 100000, got %d", budget.RemainingTokens())
	}

	budget.RecordUsage(&Usage{InputTokens: 30000})
	if budget.RemainingTokens() != 70000 {
		t.Errorf("expected 70000, got %d", budget.RemainingTokens())
	}
}

func TestMessage_GetTextContent(t *testing.T) {
	msg := Message{
		Role: RoleAssistant,
		Content: []ContentBlock{
			{Type: ContentTypeText, Text: "Hello "},
			{Type: ContentTypeToolUse, ToolName: "test"},
			{Type: ContentTypeText, Text: "World"},
		},
	}

	text := msg.GetTextContent()
	if text != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", text)
	}
}

func TestMessage_HasToolUse(t *testing.T) {
	tests := []struct {
		name     string
		content  []ContentBlock
		expected bool
	}{
		{"no tool use", []ContentBlock{{Type: ContentTypeText, Text: "hi"}}, false},
		{"has tool use", []ContentBlock{{Type: ContentTypeToolUse, ToolName: "bash"}}, true},
		{"mixed", []ContentBlock{{Type: ContentTypeText}, {Type: ContentTypeToolUse}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := Message{Content: tt.content}
			if msg.HasToolUse() != tt.expected {
				t.Errorf("expected %v", tt.expected)
			}
		})
	}
}
