package hook

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestRegistry_RunNoHandlers(t *testing.T) {
	r := NewRegistry(nil)
	res := r.Run(context.Background(), EventSubagentStart, &Context{})
	if res == nil {
		t.Fatal("nil result")
	}
	if len(res.AdditionalContexts) != 0 || res.Block {
		t.Errorf("empty registry should return empty result")
	}
}

func TestRegistry_OrderAndAccumulation(t *testing.T) {
	r := NewRegistry(nil)
	r.Register(EventSubagentStart, func(_ context.Context, _ *Context) (*Result, error) {
		return &Result{AdditionalContexts: []string{"first"}}, nil
	})
	r.Register(EventSubagentStart, func(_ context.Context, _ *Context) (*Result, error) {
		return &Result{AdditionalContexts: []string{"second", "third"}}, nil
	})

	res := r.Run(context.Background(), EventSubagentStart, &Context{})
	want := []string{"first", "second", "third"}
	if len(res.AdditionalContexts) != len(want) {
		t.Fatalf("got %v want %v", res.AdditionalContexts, want)
	}
	for i := range want {
		if res.AdditionalContexts[i] != want[i] {
			t.Errorf("got %q want %q at %d", res.AdditionalContexts[i], want[i], i)
		}
	}
}

func TestRegistry_BlockShortCircuits(t *testing.T) {
	r := NewRegistry(nil)
	var thirdCalled atomic.Int32
	r.Register(EventPreToolUse, func(_ context.Context, _ *Context) (*Result, error) {
		return &Result{AdditionalContexts: []string{"a"}}, nil
	})
	r.Register(EventPreToolUse, func(_ context.Context, _ *Context) (*Result, error) {
		return &Result{Block: true, BlockReason: "denied"}, nil
	})
	r.Register(EventPreToolUse, func(_ context.Context, _ *Context) (*Result, error) {
		thirdCalled.Add(1)
		return nil, nil
	})

	res := r.Run(context.Background(), EventPreToolUse, &Context{})
	if !res.Block || res.BlockReason != "denied" {
		t.Errorf("expected block: %+v", res)
	}
	if thirdCalled.Load() != 0 {
		t.Error("third handler should not run after block")
	}
	if len(res.AdditionalContexts) != 1 || res.AdditionalContexts[0] != "a" {
		t.Errorf("first hook's contexts should still be returned: %v", res.AdditionalContexts)
	}
}

func TestRegistry_ErrorIsolation(t *testing.T) {
	r := NewRegistry(nil)
	var laterCalled atomic.Int32

	// 1) 报错 handler
	r.Register(EventSubagentStart, func(_ context.Context, _ *Context) (*Result, error) {
		return nil, errors.New("boom")
	})
	// 2) panic handler
	r.Register(EventSubagentStart, func(_ context.Context, _ *Context) (*Result, error) {
		panic("kaboom")
	})
	// 3) 正常 handler 必须仍被执行
	r.Register(EventSubagentStart, func(_ context.Context, _ *Context) (*Result, error) {
		laterCalled.Add(1)
		return &Result{AdditionalContexts: []string{"ok"}}, nil
	})

	res := r.Run(context.Background(), EventSubagentStart, &Context{})
	if laterCalled.Load() != 1 {
		t.Error("third handler must still execute after earlier failures")
	}
	if len(res.AdditionalContexts) != 1 || res.AdditionalContexts[0] != "ok" {
		t.Errorf("got %v", res.AdditionalContexts)
	}
}
