package hooks

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	memoryapp "github.com/yaoice/goclaude/pkg/application/memory"
	"github.com/yaoice/goclaude/pkg/domain/hook"
	"github.com/yaoice/goclaude/pkg/infrastructure/appconfig"
	"github.com/yaoice/goclaude/pkg/infrastructure/memory/sqlite"
)

func newTestHooks(t *testing.T) *MemoryLifecycleHooks {
	t.Helper()
	dir := t.TempDir()
	dbPath := dir + "/test.db"
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	t.Cleanup(func() { repo.Close() })

	cfg := appconfig.LongTermMemoryConfig{
		Enabled: true,
		Capture: appconfig.LongTermCaptureConfig{
			AutoCaptureTools:   true,
			MaxObservationSize: 8000,
			MinCaptureChars:    5,
		},
		Injection: appconfig.LongTermInjectionConfig{
			AutoInject:        true,
			MaxInjectTokens:   2000,
			SearchLimit:       10,
			MinRelevanceScore: 0.1,
		},
		Capacity: appconfig.LongTermCapacityConfig{
			MaxEntries:      100,
			MaxStorageBytes: 10 * 1024 * 1024,
		},
		Eviction: appconfig.LongTermEvictionConfig{
			MinPriority: 5,
		},
		Expiration: appconfig.LongTermExpirationConfig{
			DefaultTTLDays:       90,
			LowPriorityTTLDays:   30,
			CleanupIntervalHours: 0,
		},
		Privacy: appconfig.LongTermPrivacyConfig{
			AutoExcludePatterns: true,
			StripPrivateTags:    true,
		},
	}
	logger := slog.New(slog.DiscardHandler)
	svc := memoryapp.NewLongTermMemoryService(repo, cfg, logger)
	return NewMemoryLifecycleHooks(svc, cfg, logger, "/test/project")
}

// ============================================================
// RegisterAll
// ============================================================

func TestMemoryLifecycleHooks_RegisterAll(t *testing.T) {
	hooks := newTestHooks(t)
	reg := hook.NewRegistry(nil)

	hooks.RegisterAll(reg)

	if reg.Count(hook.EventSessionStart) != 1 {
		t.Errorf("SessionStart handlers = %d, want 1", reg.Count(hook.EventSessionStart))
	}
	if reg.Count(hook.EventPostToolUse) != 1 {
		t.Errorf("PostToolUse handlers = %d, want 1", reg.Count(hook.EventPostToolUse))
	}
	if reg.Count(hook.EventSessionEnd) != 1 {
		t.Errorf("SessionEnd handlers = %d, want 1", reg.Count(hook.EventSessionEnd))
	}
	if reg.Count(hook.EventUserPromptSubmit) != 1 {
		t.Errorf("UserPromptSubmit handlers = %d, want 1", reg.Count(hook.EventUserPromptSubmit))
	}
}

func TestMemoryLifecycleHooks_RegisterAll_NilRegistry(t *testing.T) {
	hooks := newTestHooks(t)
	hooks.RegisterAll(nil) // 不应 panic
}

// ============================================================
// PostToolUse handler
// ============================================================

func TestPostToolUseHandler_CapturesToolResult(t *testing.T) {
	hooks := newTestHooks(t)
	handler := hooks.PostToolUseHandler()
	ctx := context.Background()

	hookCtx := &hook.Context{
		SessionID: "sess-test",
		ToolName:  "read_file",
		Extra: map[string]interface{}{
			"result":   "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}",
			"filePath": "/project/main.go",
		},
	}

	res, err := handler(ctx, hookCtx)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res != nil {
		t.Error("PostToolUse should return nil result (async save)")
	}
	// 注意：异步保存需要等 goroutine 完成
	// 这里只验证不 panic/error
}

func TestPostToolUseHandler_IgnoresShortResult(t *testing.T) {
	hooks := newTestHooks(t)
	hooks.cfg.Capture.MinCaptureChars = 100
	handler := hooks.PostToolUseHandler()
	ctx := context.Background()

	hookCtx := &hook.Context{
		SessionID: "s",
		ToolName:  "read_file",
		Extra:     map[string]interface{}{"result": "ok"},
	}

	_, err := handler(ctx, hookCtx)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
}

func TestPostToolUseHandler_Disabled(t *testing.T) {
	hooks := newTestHooks(t)
	hooks.cfg.Capture.AutoCaptureTools = false
	handler := hooks.PostToolUseHandler()
	ctx := context.Background()

	hookCtx := &hook.Context{
		SessionID: "s",
		ToolName:  "read_file",
		Extra:     map[string]interface{}{"result": "long enough result content here"},
	}

	res, err := handler(ctx, hookCtx)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res != nil {
		t.Error("disabled capture should return nil")
	}
}

// ============================================================
// UserPromptSubmit handler
// ============================================================

func TestUserPromptSubmitHandler(t *testing.T) {
	hooks := newTestHooks(t)
	handler := hooks.UserPromptSubmitHandler()
	ctx := context.Background()

	hookCtx := &hook.Context{
		SessionID: "sess",
		Extra:     map[string]interface{}{"prompt": "How do I deploy Go apps?"},
	}

	res, err := handler(ctx, hookCtx)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res != nil {
		t.Error("UserPromptSubmit should return nil")
	}
	if hooks.LastQuery() != "How do I deploy Go apps?" {
		t.Errorf("LastQuery = %q", hooks.LastQuery())
	}
}

// ============================================================
// 辅助函数测试
// ============================================================

func TestExtractResult(t *testing.T) {
	tests := []struct {
		name   string
		extra  map[string]interface{}
		expect string
	}{
		{"result key", map[string]interface{}{"result": "hello"}, "hello"},
		{"output key", map[string]interface{}{"output": "world"}, "world"},
		{"content key", map[string]interface{}{"content": "content"}, "content"},
		{"no known key", map[string]interface{}{"unknown": "x"}, ""},
		{"nil map", nil, ""},
		{"empty map", map[string]interface{}{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractResult(tt.extra)
			if got != tt.expect {
				t.Errorf("extractResult = %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestBuildObsTitle(t *testing.T) {
	tests := []struct {
		toolName string
		extra    map[string]interface{}
		contains string
	}{
		{"read_file", map[string]interface{}{"file_path": "/path/to/file.go"}, "/path/to/file.go"},
		{"write_to_file", map[string]interface{}{"path": "/out.txt"}, "/out.txt"},
		{"execute_command", map[string]interface{}{"command": "go build"}, "go build"},
		{"search_content", map[string]interface{}{"pattern": "func.*Test"}, "func.*Test"},
		{"web_search", map[string]interface{}{"query": "golang FTS5"}, "golang FTS5"},
		{"unknown_tool", map[string]interface{}{"path": "x"}, ""},
		{"read_file", map[string]interface{}{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			got := buildObsTitle(tt.toolName, tt.extra)
			if tt.contains == "" && got != "" {
				t.Errorf("expected empty for %s, got %q", tt.toolName, got)
			}
			if tt.contains != "" && !strings.Contains(got, tt.contains) {
				t.Errorf("title %q does not contain %q", got, tt.contains)
			}
		})
	}
}

func TestBuildObsTitle_ToolInput(t *testing.T) {
	extra := map[string]interface{}{
		"tool_input": map[string]interface{}{
			"filePath": "/nested/path.go",
		},
	}
	title := buildObsTitle("read_file", extra)
	if !strings.Contains(title, "/nested/path.go") {
		t.Errorf("title from tool_input: %q", title)
	}
}

func TestExtractSessionStats(t *testing.T) {
	tests := []struct {
		name   string
		extra  map[string]interface{}
		tokens int
		turns  int
	}{
		{"with fields", map[string]interface{}{"input_tokens": 500, "output_tokens": 300, "turn_count": 4}, 500, 4},
		{"nil", nil, 0, 0},
		{"empty", map[string]interface{}{}, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stats := extractSessionStats(tt.extra)
			if stats.InputTokens != tt.tokens {
				t.Errorf("InputTokens = %d, want %d", stats.InputTokens, tt.tokens)
			}
			if stats.TurnCount != tt.turns {
				t.Errorf("TurnCount = %d, want %d", stats.TurnCount, tt.turns)
			}
		})
	}
}

func TestToInt(t *testing.T) {
	tests := []struct {
		input interface{}
		want  int
	}{
		{int(42), 42},
		{int64(42), 42},
		{float64(3.14), 3},
		{"99", 99},
		{"not a number", 0},
		{nil, 0},
		{true, 0},
	}
	for _, tt := range tests {
		got := toInt(tt.input)
		if got != tt.want {
			t.Errorf("toInt(%v) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestExtractStringOk(t *testing.T) {
	if v, ok := extractStringOk(nil, "x"); ok || v != "" {
		t.Error("nil map should return empty")
	}
	if v, ok := extractStringOk(map[string]interface{}{"a": "b"}, "a"); !ok || v != "b" {
		t.Error("should return b")
	}
	if v, ok := extractStringOk(map[string]interface{}{"a": 123}, "a"); ok || v != "" {
		t.Error("non-string value should return empty")
	}
}

func TestNewMemoryLifecycleHooks_NilLogger(t *testing.T) {
	hooks := NewMemoryLifecycleHooks(nil, appconfig.LongTermMemoryConfig{}, nil, "/proj")
	if hooks == nil {
		t.Fatal("NewMemoryLifecycleHooks returned nil")
	}
	if hooks.logger == nil {
		t.Error("logger should default to slog.Default()")
	}
}
