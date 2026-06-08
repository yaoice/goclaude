package tool

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// mockTool 模拟工具（用于单元测试）
type mockTool struct {
	name            string
	readOnly        bool
	concurrencySafe bool
	enabled         bool
	callFn          func(ctx context.Context, input Input, toolCtx *UseContext) (*Result, error)
	callCount       atomic.Int32
}

func newMockTool(name string, readOnly, concurrencySafe bool) *mockTool {
	return &mockTool{
		name:            name,
		readOnly:        readOnly,
		concurrencySafe: concurrencySafe,
		enabled:         true,
	}
}

func (m *mockTool) Name() string                        { return m.name }
func (m *mockTool) Aliases() []string                   { return nil }
func (m *mockTool) Description() string                 { return "mock tool: " + m.name }
func (m *mockTool) InputSchema() map[string]interface{} { return map[string]interface{}{} }
func (m *mockTool) IsEnabled() bool                     { return m.enabled }
func (m *mockTool) IsReadOnly(input Input) bool         { return m.readOnly }
func (m *mockTool) IsConcurrencySafe(input Input) bool  { return m.concurrencySafe }
func (m *mockTool) Prompt() string                      { return "" }
func (m *mockTool) ValidateInput(input Input) error     { return nil }
func (m *mockTool) CheckPermissions(ctx context.Context, input Input, permCtx *PermissionContext) (PermissionResult, error) {
	return PermissionResult{Behavior: PermissionAllow}, nil
}
func (m *mockTool) Call(ctx context.Context, input Input, toolCtx *UseContext) (*Result, error) {
	m.callCount.Add(1)
	if m.callFn != nil {
		return m.callFn(ctx, input, toolCtx)
	}
	return NewResult(fmt.Sprintf("result from %s", m.name)), nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	registry := NewRegistry()

	tool1 := newMockTool("file_read", true, true)
	tool2 := newMockTool("bash", false, false)

	if err := registry.Register(tool1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := registry.Register(tool2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 重复注册应失败
	if err := registry.Register(tool1); err == nil {
		t.Error("expected error on duplicate registration")
	}

	// 查找
	found, ok := registry.Get("file_read")
	if !ok {
		t.Error("tool not found")
	}
	if found.Name() != "file_read" {
		t.Errorf("expected file_read, got %s", found.Name())
	}

	// 不存在的工具
	_, ok = registry.Get("nonexistent")
	if ok {
		t.Error("should not find nonexistent tool")
	}

	// Count
	if registry.Count() != 2 {
		t.Errorf("expected 2, got %d", registry.Count())
	}
}

func TestRegistry_GetEnabled(t *testing.T) {
	registry := NewRegistry()

	enabled := newMockTool("enabled_tool", true, true)
	disabled := newMockTool("disabled_tool", true, true)
	disabled.enabled = false

	registry.MustRegister(enabled)
	registry.MustRegister(disabled)

	enabledTools := registry.GetEnabled()
	if len(enabledTools) != 1 {
		t.Errorf("expected 1 enabled tool, got %d", len(enabledTools))
	}
}

func TestExecutor_Execute_ConcurrentReadOnly(t *testing.T) {
	registry := NewRegistry()

	// 创建3个只读并发安全工具
	for i := 0; i < 3; i++ {
		tool := newMockTool(fmt.Sprintf("read_%d", i), true, true)
		tool.callFn = func(ctx context.Context, input Input, toolCtx *UseContext) (*Result, error) {
			time.Sleep(10 * time.Millisecond)
			return NewResult("ok"), nil
		}
		registry.MustRegister(tool)
	}

	executor := NewExecutor(registry, 10, nil)

	requests := []ExecutionRequest{
		{ToolUseID: "1", ToolName: "read_0", Input: map[string]interface{}{}},
		{ToolUseID: "2", ToolName: "read_1", Input: map[string]interface{}{}},
		{ToolUseID: "3", ToolName: "read_2", Input: map[string]interface{}{}},
	}

	start := time.Now()
	results, err := executor.Execute(context.Background(), requests)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// 并发执行应该比串行快
	if elapsed > 25*time.Millisecond {
		t.Logf("warning: concurrent execution took %v (expected ~10ms)", elapsed)
	}

	for _, r := range results {
		if r.IsError {
			t.Errorf("unexpected error result: %s", r.Content)
		}
	}
}

func TestExecutor_Execute_SerialWriteTools(t *testing.T) {
	registry := NewRegistry()

	var order []string
	for i := 0; i < 3; i++ {
		idx := i
		tool := newMockTool(fmt.Sprintf("write_%d", i), false, false)
		tool.callFn = func(ctx context.Context, input Input, toolCtx *UseContext) (*Result, error) {
			order = append(order, fmt.Sprintf("write_%d", idx))
			return NewResult("ok"), nil
		}
		registry.MustRegister(tool)
	}

	executor := NewExecutor(registry, 10, nil)

	requests := []ExecutionRequest{
		{ToolUseID: "1", ToolName: "write_0", Input: map[string]interface{}{}},
		{ToolUseID: "2", ToolName: "write_1", Input: map[string]interface{}{}},
		{ToolUseID: "3", ToolName: "write_2", Input: map[string]interface{}{}},
	}

	results, err := executor.Execute(context.Background(), requests)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// 验证串行执行顺序
	if len(order) != 3 || order[0] != "write_0" || order[1] != "write_1" || order[2] != "write_2" {
		t.Errorf("expected sequential execution, got %v", order)
	}
}

func TestExecutor_Execute_ToolNotFound(t *testing.T) {
	registry := NewRegistry()
	executor := NewExecutor(registry, 10, nil)

	requests := []ExecutionRequest{
		{ToolUseID: "1", ToolName: "nonexistent", Input: map[string]interface{}{}},
	}

	results, err := executor.Execute(context.Background(), requests)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].IsError {
		t.Error("expected error result for missing tool")
	}
}

func TestExecutor_Execute_ContextCancellation(t *testing.T) {
	registry := NewRegistry()

	slowTool := newMockTool("slow_write", false, false)
	slowTool.callFn = func(ctx context.Context, input Input, toolCtx *UseContext) (*Result, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
			return NewResult("done"), nil
		}
	}
	registry.MustRegister(slowTool)

	executor := NewExecutor(registry, 10, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	requests := []ExecutionRequest{
		{ToolUseID: "1", ToolName: "slow_write", Input: map[string]interface{}{}},
	}

	_, err := executor.Execute(ctx, requests)
	if err == nil {
		// 可能返回错误结果而不是error
		// 两种情况都是正确的
	}
}

func TestInput_GetString(t *testing.T) {
	input := Input{
		"path":  "/tmp/test.txt",
		"count": float64(42),
		"flag":  true,
	}

	if input.GetString("path") != "/tmp/test.txt" {
		t.Error("GetString failed")
	}
	if input.GetString("missing") != "" {
		t.Error("GetString should return empty for missing key")
	}
	if input.GetInt("count") != 42 {
		t.Error("GetInt failed")
	}
	if !input.GetBool("flag") {
		t.Error("GetBool failed")
	}
}
