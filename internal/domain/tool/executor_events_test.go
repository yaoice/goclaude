package tool

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// recordingListener 记录发布到 ToolEventListener 的事件序列。
type recordingListener struct {
	mu     sync.Mutex
	events []ToolEvent
}

func (l *recordingListener) HandleToolEvent(ev ToolEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, ev)
}

func (l *recordingListener) snapshot() []ToolEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]ToolEvent, len(l.events))
	copy(out, l.events)
	return out
}

// 期望：每次工具执行都会发布两条事件：start + finish；finish 携带 status/elapsed。
func TestExecutor_PublishesStartAndFinishEvents(t *testing.T) {
	registry := NewRegistry()
	registry.MustRegister(newMockTool("read_one", true, true))

	exec := NewExecutor(registry, 4, nil)
	listener := &recordingListener{}
	exec.SetToolEventListener(listener)

	_, err := exec.Execute(context.Background(), []ExecutionRequest{{
		ToolUseID: "u1", ToolName: "read_one", Input: map[string]interface{}{},
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := listener.snapshot()
	if len(got) != 2 {
		t.Fatalf("expected 2 events (start+finish), got %d: %+v", len(got), got)
	}
	if got[0].Phase != ToolPhaseStart || got[0].ToolName != "read_one" || got[0].ToolUseID != "u1" {
		t.Fatalf("first event should be start for read_one/u1, got %+v", got[0])
	}
	if got[1].Phase != ToolPhaseFinish || got[1].Status != ToolStatusSuccess {
		t.Fatalf("second event should be successful finish, got %+v", got[1])
	}
	if got[1].Elapsed < 0 {
		t.Fatalf("finish event elapsed must be >= 0, got %v", got[1].Elapsed)
	}
}

// 失败时事件 status = error，且 ErrorMessage 非空。
func TestExecutor_FinishEvent_ReportsErrorStatus(t *testing.T) {
	registry := NewRegistry()
	bad := newMockTool("bad_writer", false, false)
	bad.callFn = func(ctx context.Context, input Input, toolCtx *UseContext) (*Result, error) {
		return nil, errors.New("kaboom")
	}
	registry.MustRegister(bad)

	exec := NewExecutor(registry, 4, nil)
	listener := &recordingListener{}
	exec.SetToolEventListener(listener)

	_, err := exec.Execute(context.Background(), []ExecutionRequest{{
		ToolUseID: "u1", ToolName: "bad_writer", Input: map[string]interface{}{},
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	events := listener.snapshot()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	finish := events[1]
	if finish.Phase != ToolPhaseFinish || finish.Status != ToolStatusError {
		t.Fatalf("expected error finish, got %+v", finish)
	}
	if finish.ErrorMessage == "" {
		t.Fatalf("error finish must include ErrorMessage")
	}
}

// 并发场景每个 ToolUseID 也必须严格 start→finish 配对。
func TestExecutor_ConcurrentToolEvents_PairedByID(t *testing.T) {
	registry := NewRegistry()
	for _, name := range []string{"r0", "r1", "r2"} {
		mt := newMockTool(name, true, true)
		mt.callFn = func(ctx context.Context, input Input, toolCtx *UseContext) (*Result, error) {
			time.Sleep(5 * time.Millisecond)
			return NewResult("ok"), nil
		}
		registry.MustRegister(mt)
	}

	exec := NewExecutor(registry, 4, nil)
	listener := &recordingListener{}
	exec.SetToolEventListener(listener)

	requests := []ExecutionRequest{
		{ToolUseID: "a", ToolName: "r0", Input: map[string]interface{}{}},
		{ToolUseID: "b", ToolName: "r1", Input: map[string]interface{}{}},
		{ToolUseID: "c", ToolName: "r2", Input: map[string]interface{}{}},
	}
	if _, err := exec.Execute(context.Background(), requests); err != nil {
		t.Fatalf("execute: %v", err)
	}

	events := listener.snapshot()
	if len(events) != 6 {
		t.Fatalf("expected 6 events (3 start+finish), got %d", len(events))
	}

	// 每个 ID 必须看到一对 start 在 finish 之前。
	starts := map[string]int{}
	for i, ev := range events {
		switch ev.Phase {
		case ToolPhaseStart:
			starts[ev.ToolUseID] = i
		case ToolPhaseFinish:
			startIdx, ok := starts[ev.ToolUseID]
			if !ok {
				t.Fatalf("finish event for %s without prior start", ev.ToolUseID)
			}
			if startIdx >= i {
				t.Fatalf("start must precede finish for %s", ev.ToolUseID)
			}
			delete(starts, ev.ToolUseID)
		}
	}
	if len(starts) != 0 {
		t.Fatalf("missing finish events for: %v", starts)
	}
}
