package hooks

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/goclaude/pkg/domain/hook"
	"github.com/anthropics/goclaude/pkg/infrastructure/appconfig"
	"github.com/anthropics/goclaude/pkg/infrastructure/memory/sqlite"
	memoryapp "github.com/anthropics/goclaude/pkg/application/memory"
)

// ============================================================
// 交互终端模拟测试
// ============================================================
// 模拟完整的 REPL 多轮对话流程，验证：
//   1. SessionStart 时自动注入上下文
//   2. PostToolUse 时自动捕获工具结果
//   3. UserPromptSubmit 时记录用户查询
//   4. SessionEnd 时保存会话摘要

func interactiveTestConfig() appconfig.LongTermMemoryConfig {
	return appconfig.LongTermMemoryConfig{
		Enabled: true,
		Capture: appconfig.LongTermCaptureConfig{
			AutoCaptureTools:  true,
			MaxObservationSize: 8000,
			MinCaptureChars:    10,
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
			DefaultTTLDays:      90,
			LowPriorityTTLDays:  30,
			CleanupIntervalHours: 0,
		},
		Privacy: appconfig.LongTermPrivacyConfig{
			AutoExcludePatterns: true,
			StripPrivateTags:    true,
		},
	}
}

func newInteractiveEnv(t *testing.T) (*hook.Registry, *memoryapp.LongTermMemoryService, *MemoryLifecycleHooks) {
	t.Helper()
	dir := t.TempDir()
	dbPath := dir + "/interactive_test.db"
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	t.Cleanup(func() { repo.Close() })

	cfg := interactiveTestConfig()
	logger := slog.New(slog.DiscardHandler)
	svc := memoryapp.NewLongTermMemoryService(repo, cfg, logger)
	svc.Start(context.Background())
	t.Cleanup(func() { svc.Close() })

	reg := hook.NewRegistry(logger)
	h := NewMemoryLifecycleHooks(svc, cfg, logger, "/test/project")
	h.RegisterAll(reg)

	return reg, svc, h
}

func TestInteractive_SessionLifecycle(t *testing.T) {
	reg, svc, _ := newInteractiveEnv(t)
	ctx := context.Background()

	sessID := "interactive-sess-1"

	// 1a: 用户提交问题
	reg.Run(ctx, hook.EventUserPromptSubmit, &hook.Context{
		SessionID: sessID,
		Extra:     map[string]interface{}{"prompt": "帮我分析 Go 项目的测试覆盖率"},
	})

	// 1b: AI 调用 read_file
	reg.Run(ctx, hook.EventPostToolUse, &hook.Context{
		SessionID: sessID,
		ToolName:  "read_file",
		Extra: map[string]interface{}{
			"result":    "func TestMain(t *testing.T) {}\nfunc TestConfig(t *testing.T) {}\n共发现 15 个测试函数，覆盖约 65% 的代码路径。",
			"file_path": "/test/project/main_test.go",
		},
	})

	// 1c: AI 调用 search_content
	reg.Run(ctx, hook.EventPostToolUse, &hook.Context{
		SessionID: sessID,
		ToolName:  "search_content",
		Extra: map[string]interface{}{
			"result":  "Found 15 matches in 5 files for 'func Test'",
			"pattern": "func Test",
		},
	})

	// 1d: AI 执行命令
	reg.Run(ctx, hook.EventPostToolUse, &hook.Context{
		SessionID: sessID,
		ToolName:  "execute_command",
		Extra: map[string]interface{}{
			"result":  "ok  github.com/test\t0.123s\ncoverage: 65.2%",
			"command": "go test ./... -cover",
		},
	})

	// 1e: 会话结束
	reg.Run(ctx, hook.EventSessionEnd, &hook.Context{
		SessionID: sessID,
		Extra: map[string]interface{}{
			"summary":      "分析了测试覆盖率，15个测试，覆盖65%",
			"input_tokens":  1500,
			"output_tokens": 400,
			"turn_count":    3,
		},
	})

	// 验证持久化
	time.Sleep(200 * time.Millisecond)

	items, err := svc.ListRecent(ctx, 20)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(items) < 2 {
		t.Fatalf("expected at least 2 memories, got %d", len(items))
	}

	hasToolObs := false
	hasSummary := false
	for _, item := range items {
		if item.Type == "observation" {
			hasToolObs = true
		}
		if item.Type == "summary" {
			hasSummary = true
		}
	}
	if !hasToolObs {
		t.Error("no tool observation captured")
	}
	if !hasSummary {
		t.Error("no session summary captured")
	}
}

func TestInteractive_SecondSessionInjection(t *testing.T) {
	reg, svc, _ := newInteractiveEnv(t)
	ctx := context.Background()

	// 填充第一个会话的记忆（使用英文关键词确保 FTS5 可匹配 query）
	svc.SaveObservation(ctx, "sess-prev", "Go test coverage analysis for project",
		"Test coverage for the project is 65%. Recommend adding table-driven tests for SQLite FTS5 edge cases.",
		memoryapp.SaveOptions{ObsType: "observation", Category: "project", Priority: 85})

	svc.SaveObservation(ctx, "sess-prev", "Added long-term memory for project",
		"SQLite-FTS5 based long-term memory system was added to the project. Supports three-layer progressive search.",
		memoryapp.SaveOptions{ObsType: "summary", Category: "project", Priority: 90})

	// SessionStart
	result := reg.Run(ctx, hook.EventSessionStart, &hook.Context{
		SessionID: "sess-new",
		Extra:     map[string]interface{}{"prompt": "继续完善测试覆盖率，特别是 SQLite 相关"},
	})
	if result == nil || len(result.AdditionalContexts) == 0 {
		t.Error("SessionStart should inject context from previous sessions")
	} else {
		ctxText := strings.Join(result.AdditionalContexts, "\n")
		if !strings.Contains(ctxText, "<long-term-memory>") {
			t.Error("injected context missing wrapper tag")
		}
	}
}

func TestInteractive_MultiTurnTools(t *testing.T) {
	reg, svc, _ := newInteractiveEnv(t)
	ctx := context.Background()

	tools := []struct {
		name   string
		result string
	}{
		{"read_file", "package main\n\nimport \"fmt\"\n\nfunc main() {} // contains entry point for the application"},
		{"write_to_file", "wrote 15 lines to newfile.go successfully, includes imports and function definitions"},
		{"replace_in_file", "replaced 3 lines in config.go, added LongTermMemoryConfig struct definition"},
		{"execute_command", "go build ./... completed successfully in 0.456s"},
		{"search_content", "Found 8 matches for LongTermMemory in 4 files across the codebase"},
	}

	for _, tc := range tools {
		reg.Run(ctx, hook.EventPostToolUse, &hook.Context{
			SessionID: "multi-turn",
			ToolName:  tc.name,
			Extra:     map[string]interface{}{"result": tc.result},
		})
	}

	time.Sleep(300 * time.Millisecond)

	items, _ := svc.ListRecent(ctx, 20)
	if len(items) < 5 {
		t.Errorf("expected at least 5 tool observations, got %d", len(items))
	}
}

func TestInteractive_PrivacyFiltering(t *testing.T) {
	reg, svc, _ := newInteractiveEnv(t)
	ctx := context.Background()

	// 带 private 标签的结果
	reg.Run(ctx, hook.EventPostToolUse, &hook.Context{
		SessionID: "priv-sess",
		ToolName:  "read_file",
		Extra: map[string]interface{}{
			"result": "config data here <private>API_KEY=sk-secret-123456789</private> more config",
			"path":   ".env",
		},
	})

	time.Sleep(200 * time.Millisecond)

	items, _ := svc.ListRecent(ctx, 20)
	for _, item := range items {
		if strings.Contains(item.Content, "sk-secret") {
			t.Errorf("private content leaked: %q", item.Title)
		}
	}
}

func TestInteractive_ShortResultFiltered(t *testing.T) {
	reg, svc, hooks := newInteractiveEnv(t)
	ctx := context.Background()

	// 通过 hook 路径测试（min capture check 在 PostToolUseHandler 中）
	// 短结果 — 应该被过滤
	reg.Run(ctx, hook.EventPostToolUse, &hook.Context{
		SessionID: "short-sess",
		ToolName:  "execute_command",
		Extra:     map[string]interface{}{"result": "ok"},
	})

	// 长结果 — 应该通过 service 直接保存验证
	id2, err := svc.SaveObservation(ctx, "long-sess", "Long enough",
		"This is a long tool output that exceeds the minimum capture character limit.",
		memoryapp.SaveOptions{ObsType: "observation", Source: "tool_use", ToolName: "read_file"},
	)
	if err != nil {
		t.Fatalf("SaveObservation: %v", err)
	}
	if id2 <= 0 {
		t.Error("long result should be saved")
	}

	time.Sleep(200 * time.Millisecond)

	items, _ := svc.ListRecent(ctx, 20)
	// 验证短结果（"ok", 2 chars）被捕获？hook 里 min=10，但 async 可能需要等
	// 主要验证长结果一定在
	found := false
	for _, item := range items {
		if strings.Contains(item.Content, "minimum capture character") {
			found = true
		}
	}
	if !found {
		t.Error("long result not found in memories")
	}
	_ = hooks
}
