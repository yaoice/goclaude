package shell

import (
	"strings"
	"testing"
	"time"

	"github.com/anthropics/goclaude/pkg/domain/tool"
)

// HandleToolEvent 必须把 finish 事件缓存供后续 consume 使用，
// 同一 ToolUseID 第一次 consume 取得耗时尾巴、第二次返回 false。
func TestREPL_HandleToolEvent_BuffersFinishForConsume(t *testing.T) {
	r := &REPL{useColor: false}
	r.HandleToolEvent(tool.ToolEvent{
		Phase: tool.ToolPhaseStart, ToolName: "glob", ToolUseID: "id-1",
	})
	r.HandleToolEvent(tool.ToolEvent{
		Phase: tool.ToolPhaseFinish, ToolName: "glob", ToolUseID: "id-1",
		Status: tool.ToolStatusSuccess, Elapsed: 12 * time.Millisecond,
	})

	tail, ok := r.consumeToolFinish("id-1")
	if !ok {
		t.Fatalf("expected finish event to be buffered for id-1")
	}
	if !strings.Contains(tail, "12ms") {
		t.Fatalf("expected elapsed in tail, got %q", tail)
	}

	if _, ok := r.consumeToolFinish("id-1"); ok {
		t.Fatal("second consume should return false (event already taken)")
	}
}

// 错误事件时 consume 必须把错误摘要拼到尾巴，便于一行内显示。
func TestREPL_HandleToolEvent_ErrorIncludesMessage(t *testing.T) {
	r := &REPL{useColor: false}
	r.HandleToolEvent(tool.ToolEvent{
		Phase: tool.ToolPhaseFinish, ToolName: "bash", ToolUseID: "x",
		Status: tool.ToolStatusError, Elapsed: 5 * time.Millisecond,
		ErrorMessage: "exit status 1: missing file",
	})
	tail, ok := r.consumeToolFinish("x")
	if !ok {
		t.Fatal("expected finish event for x")
	}
	if !strings.Contains(tail, "5ms") {
		t.Fatalf("missing elapsed in tail %q", tail)
	}
	if !strings.Contains(tail, "exit status 1") {
		t.Fatalf("missing error summary in tail %q", tail)
	}
}

// 未提供 ToolUseID 时 consume 安全返回 false（不能 panic）。
func TestREPL_ConsumeToolFinish_EmptyID(t *testing.T) {
	r := &REPL{useColor: false}
	if _, ok := r.consumeToolFinish(""); ok {
		t.Fatal("empty id must yield ok=false")
	}
}

// REPL 必须实现 tool.ToolEventListener 接口（编译期断言已写在 tool_render.go 中，
// 这里以行为测试保证 nil 接收者也不 panic，便于安全注入）。
func TestREPL_HandleToolEvent_NilSafe(t *testing.T) {
	var r *REPL
	r.HandleToolEvent(tool.ToolEvent{Phase: tool.ToolPhaseStart})
}
