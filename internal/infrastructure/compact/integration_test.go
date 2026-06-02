package compact

import (
	"context"
	"log/slog"
	"testing"

	"github.com/anthropics/goclaude/internal/domain/query"
)

// 记录 Engine 调用 Compactor 的次数，确保 ShouldCompact 被尊重
type spyCompactor struct {
	calls int
}

func (s *spyCompactor) Compact(_ context.Context, msgs []query.Message, _ query.AIProvider) ([]query.Message, error) {
	s.calls++
	// 简单返回最后两条
	if len(msgs) <= 2 {
		return msgs, nil
	}
	return msgs[len(msgs)-2:], nil
}

// 这个测试构造 Engine 模拟一次「触发压缩」流程：
//
//   - Provider 在第一轮就用 high InputTokens 触发 budget.ShouldCompact
//   - 第二轮 Engine 应当调用 Compactor，把消息压缩后再继续
//   - 第二轮 Provider 返回 end_turn，循环结束
type budgetTriggerProvider struct {
	turn int
	// 上次 Stream 时记录的 messages 数量
	lastMsgCount int
}

func (p *budgetTriggerProvider) Stream(_ context.Context, params *query.StreamParams) (<-chan query.StreamEvent, error) {
	p.turn++
	p.lastMsgCount = len(params.Messages)
	ch := make(chan query.StreamEvent, 4)
	go func() {
		defer close(ch)
		ch <- query.StreamEvent{
			Type:  query.EventContentBlockStart,
			Index: 0,
			ContentBlock: &query.ContentBlock{
				Type: query.ContentTypeText,
			},
		}
		text := "hello"
		ch <- query.StreamEvent{
			Type:  query.EventContentBlockDelta,
			Index: 0,
			Delta: &query.DeltaContent{Type: query.ContentTypeText, Text: text},
		}
		ch <- query.StreamEvent{Type: query.EventContentBlockStop, Index: 0}
		// 第一轮上报极高的 input tokens 触发压缩
		usage := &query.Usage{InputTokens: 100, OutputTokens: 5}
		if p.turn == 1 {
			usage.InputTokens = 90_000 // > 80% of 100k → ShouldCompact = true
		}
		ch <- query.StreamEvent{
			Type:       query.EventMessageDelta,
			StopReason: query.StopReasonEndTurn,
			Usage:      usage,
		}
	}()
	return ch, nil
}

func (p *budgetTriggerProvider) Send(_ context.Context, _ *query.SendParams) (*query.Message, *query.Usage, error) {
	return nil, nil, nil
}

func TestEngine_AutoCompactTriggersCompactor(t *testing.T) {
	// 用 budget=100k, threshold=0.8 → 输入 token 超过 80k 时 ShouldCompact 返回 true
	budget := query.NewTokenBudget(100_000, 0.8)
	spy := &spyCompactor{}
	cfg := query.DefaultConfig()
	cfg.MaxTurns = 5
	cfg.AutoCompact = true

	prov := &budgetTriggerProvider{}
	engine := query.NewEngine(prov, nil, budget, spy, cfg, slog.Default())

	msgs := []query.Message{
		query.NewTextMessage(query.RoleUser, "hi"),
	}
	_, err := engine.Execute(context.Background(), msgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 因为第一轮 end_turn 会直接 return（没有 tool_use），不会进入下一循环
	// 修正：spy 在第一轮**之前**被调用？不对，ShouldCompact 在循环顶部检查。
	// 第一轮 ShouldCompact=false（因为还没记录 usage），不会调用 spy。
	// 第一轮结束时记录了 usage（90k）；但因 stop=end_turn 直接返回，
	// **不会进入第二轮**，spy.calls 应该为 0。
	if spy.calls != 0 {
		t.Errorf("end_turn 应直接返回，不应触发压缩，spy.calls=%d", spy.calls)
	}
}

// 这个版本：第一轮强制返回 tool_use（让循环继续），第二轮才能体现压缩
type budgetThenToolProvider struct {
	turn int
}

func (p *budgetThenToolProvider) Stream(_ context.Context, _ *query.StreamParams) (<-chan query.StreamEvent, error) {
	p.turn++
	ch := make(chan query.StreamEvent, 8)
	go func() {
		defer close(ch)
		switch p.turn {
		case 1:
			// 触发 tool_use 让循环继续
			ch <- query.StreamEvent{
				Type:  query.EventContentBlockStart,
				Index: 0,
				ContentBlock: &query.ContentBlock{
					Type:      query.ContentTypeToolUse,
					ToolUseID: "t1",
					ToolName:  "missing-tool",
				},
			}
			ch <- query.StreamEvent{Type: query.EventContentBlockStop, Index: 0}
			ch <- query.StreamEvent{
				Type:       query.EventMessageDelta,
				StopReason: query.StopReasonToolUse,
				Usage:      &query.Usage{InputTokens: 90_000, OutputTokens: 5}, // 触发 ShouldCompact
			}
		default:
			// 第二轮 end_turn
			ch <- query.StreamEvent{
				Type:         query.EventContentBlockStart,
				Index:        0,
				ContentBlock: &query.ContentBlock{Type: query.ContentTypeText},
			}
			ch <- query.StreamEvent{
				Type:  query.EventContentBlockDelta,
				Index: 0,
				Delta: &query.DeltaContent{Type: query.ContentTypeText, Text: "done"},
			}
			ch <- query.StreamEvent{Type: query.EventContentBlockStop, Index: 0}
			ch <- query.StreamEvent{
				Type:       query.EventMessageDelta,
				StopReason: query.StopReasonEndTurn,
				Usage:      &query.Usage{InputTokens: 100, OutputTokens: 5},
			}
		}
	}()
	return ch, nil
}

func (p *budgetThenToolProvider) Send(_ context.Context, _ *query.SendParams) (*query.Message, *query.Usage, error) {
	return nil, nil, nil
}

func TestEngine_AutoCompactTriggeredByBudget(t *testing.T) {
	budget := query.NewTokenBudget(100_000, 0.8)
	spy := &spyCompactor{}
	cfg := query.DefaultConfig()
	cfg.MaxTurns = 5
	cfg.AutoCompact = true

	prov := &budgetThenToolProvider{}
	// nil executor → tool 执行会得到 "tool executor not available" 但仍会作为 tool_result 加入历史
	engine := query.NewEngine(prov, nil, budget, spy, cfg, slog.Default())

	_, err := engine.Execute(context.Background(), []query.Message{
		query.NewTextMessage(query.RoleUser, "go"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if spy.calls != 1 {
		t.Errorf("expected Compactor called once, got %d", spy.calls)
	}
}

func TestEngine_AutoCompactDisabled(t *testing.T) {
	budget := query.NewTokenBudget(100_000, 0.8)
	spy := &spyCompactor{}
	cfg := query.DefaultConfig()
	cfg.MaxTurns = 5
	cfg.AutoCompact = false // 关闭压缩

	prov := &budgetThenToolProvider{}
	engine := query.NewEngine(prov, nil, budget, spy, cfg, slog.Default())

	_, err := engine.Execute(context.Background(), []query.Message{
		query.NewTextMessage(query.RoleUser, "go"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if spy.calls != 0 {
		t.Errorf("AutoCompact=false 时不应调用 Compactor，got %d", spy.calls)
	}
}
