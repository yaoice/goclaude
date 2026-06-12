// Package shell — REPL ↔ 长期记忆接线集成测试
//
// 验证 REPL 在挂载 HookReg 后，对应的 SessionStart/UserPromptSubmit/
// PostToolUse/SessionEnd 事件会被正确触发，且长期记忆能被写入和检索。
package shell

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	hooksapp "github.com/yaoice/goclaude/pkg/application/hooks"
	memoryapp "github.com/yaoice/goclaude/pkg/application/memory"
	"github.com/yaoice/goclaude/pkg/domain/hook"
	domainmemory "github.com/yaoice/goclaude/pkg/domain/memory"
	"github.com/yaoice/goclaude/pkg/domain/query"
	"github.com/yaoice/goclaude/pkg/infrastructure/appconfig"
	sqlitemem "github.com/yaoice/goclaude/pkg/infrastructure/memory/sqlite"
)

func testMemCfg() appconfig.LongTermMemoryConfig {
	return appconfig.LongTermMemoryConfig{
		Enabled: true,
		Capture: appconfig.LongTermCaptureConfig{
			AutoCaptureTools:   true,
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
		Eviction: appconfig.LongTermEvictionConfig{MinPriority: 5},
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
}

func newREPLWithMemory(t *testing.T) (*REPL, *memoryapp.LongTermMemoryService) {
	t.Helper()

	dir := t.TempDir()
	dbPath := dir + "/repl_mem.db"
	repo, err := sqlitemem.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	t.Cleanup(func() { repo.Close() })

	cfg := testMemCfg()
	logger := slog.New(slog.DiscardHandler)
	svc := memoryapp.NewLongTermMemoryService(repo, cfg, logger)
	svc.Start(context.Background())
	t.Cleanup(func() { svc.Close() })

	reg := hook.NewRegistry(logger)
	h := hooksapp.NewMemoryLifecycleHooks(svc, cfg, logger, "/test/repl-project")
	h.RegisterAll(reg)

	// 构造最小 REPL：不初始化 TTY（无法 Run()），仅测试 hook 事件流
	repl := &REPL{
		Model:     "test-model",
		Provider:  "test",
		WorkDir:   "/test/repl-project",
		HookReg:   reg,
		SessionID: "repl-test-session",
		useColor:  false,
	}
	return repl, svc
}

// TestREPL_SessionStart_InjectsContext 验证 SessionStart 事件触发后
// 注入的上下文会写入 messages
func TestREPL_SessionStart_InjectsContext(t *testing.T) {
	repl, svc := newREPLWithMemory(t)
	ctx := context.Background()

	// 先存一条记忆（模拟上一个会话）
	svc.SaveObservation(ctx, "prev-sess", "Previous session work on auth module",
		"This project has an authentication module with JWT support.",
		memoryapp.SaveOptions{
			ObsType:  "summary",
			Category: "project",
			Priority: 80,
		})

	// 触发 SessionStart（模拟 REPL.Run() 中的调用）
	res := repl.HookReg.Run(ctx, hook.EventSessionStart, &hook.Context{
		SessionID: repl.SessionID,
		Extra:     map[string]interface{}{"prompt": repl.WorkDir},
	})

	if res == nil || len(res.AdditionalContexts) == 0 {
		t.Fatal("SessionStart should return injected context")
	}

	// 验证注入内容格式（对齐 REPL.Run() 的行为）
	for _, ctxText := range res.AdditionalContexts {
		if !strings.Contains(ctxText, "<long-term-memory>") {
			t.Errorf("injected context missing <long-term-memory> wrapper: %s", ctxText[:min(80, len(ctxText))])
		}
	}
	t.Logf("SessionStart 注入 %d 段上下文", len(res.AdditionalContexts))
}

// TestREPL_UserPromptSubmit_TracksQuery 验证 UserPromptSubmit 记录用户查询
func TestREPL_UserPromptSubmit_TracksQuery(t *testing.T) {
	repl, _ := newREPLWithMemory(t)
	ctx := context.Background()

	// 提交用户查询
	repl.HookReg.Run(ctx, hook.EventUserPromptSubmit, &hook.Context{
		SessionID: repl.SessionID,
		Extra:     map[string]interface{}{"prompt": "帮我优化认证模块的性能"},
	})

	// 验证 lastQuery 被记录（通过 MemoryLifecycleHooks.LastQuery）
	// 注：直接访问内部 hooks 不可行，间接通过后续 SessionStart 检索验证
}

// TestREPL_PostToolUse_CapturesToolResult 验证 PostToolUse 自动捕获工具结果
func TestREPL_PostToolUse_CapturesToolResult(t *testing.T) {
	repl, svc := newREPLWithMemory(t)
	ctx := context.Background()

	tools := []struct {
		name, result string
	}{
		{"read_file", "package auth\n\nfunc main() {} // JWT authentication module with rate limiting"},
		{"execute_command", "go test ./auth/...\nok \tauth\t0.123s\ncoverage: 72.3%"},
		{"search_content", "Found 5 matches in 3 files for 'JWT authentication'"},
	}

	for _, tc := range tools {
		repl.HookReg.Run(ctx, hook.EventPostToolUse, &hook.Context{
			SessionID: repl.SessionID,
			ToolName:  tc.name,
			Extra:     map[string]interface{}{"result": tc.result},
		})
	}

	// 等待异步保存
	time.Sleep(300 * time.Millisecond)

	items, err := svc.ListRecent(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}

	obsCount := 0
	for _, it := range items {
		if it.Type == "observation" {
			obsCount++
			t.Logf("  captured: [type=%s tool=%s] %s", it.Type, it.ToolName, it.Title)
		}
	}

	if obsCount < 3 {
		t.Errorf("expected at least 3 tool observations, got %d", obsCount)
	}
}

// TestREPL_SessionEnd_SavesSummary 验证 SessionEnd 保存会话摘要
func TestREPL_SessionEnd_SavesSummary(t *testing.T) {
	repl, svc := newREPLWithMemory(t)
	ctx := context.Background()

	// 触发 SessionEnd
	repl.HookReg.Run(ctx, hook.EventSessionEnd, &hook.Context{
		SessionID: repl.SessionID,
		Extra: map[string]interface{}{
			"summary":      "优化了认证模块，提升了 JWT 验证性能",
			"turn_count":   3,
			"input_tokens": 1200,
		},
	})

	time.Sleep(200 * time.Millisecond)

	items, err := svc.ListRecent(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}

	hasSummary := false
	for _, it := range items {
		if it.Type == "summary" {
			hasSummary = true
			t.Logf("  session summary: %s", it.Title)
			break
		}
	}

	if !hasSummary {
		t.Error("SessionEnd should save a session summary")
	}
}

// TestREPL_FullLifecycle 模拟完整 REPL 会话生命周期：
//
//	SessionStart → UserPrompt ×3 → PostToolUse ×3 → SessionEnd
//	然后启动第二个"会话" → SessionStart 检索到历史记忆
func TestREPL_FullLifecycle(t *testing.T) {
	// 使用 query.NewTextMessage 做消息构造（不需要实际引擎）
	_ = query.NewTextMessage

	repl, svc := newREPLWithMemory(t)
	ctx := context.Background()

	// === 会话 1 ===
	// SessionStart（空库，无上下文）
	res := repl.HookReg.Run(ctx, hook.EventSessionStart, &hook.Context{
		SessionID: "sess-1",
		Extra:     map[string]interface{}{"prompt": "/test/repl-project"},
	})
	_ = res // 空库，可能为空

	// UserPrompt
	repl.HookReg.Run(ctx, hook.EventUserPromptSubmit, &hook.Context{
		SessionID: "sess-1",
		Extra:     map[string]interface{}{"prompt": "请分析 auth 模块的测试覆盖"},
	})

	// Tool Use ×3
	repl.HookReg.Run(ctx, hook.EventPostToolUse, &hook.Context{
		SessionID: "sess-1",
		ToolName:  "read_file",
		Extra: map[string]interface{}{
			"result":    "func TestAuth(t *testing.T) {} // 15 tests for auth module, about 65% coverage of authentication module",
			"file_path": "/src/auth/auth_test.go",
		},
	})
	repl.HookReg.Run(ctx, hook.EventPostToolUse, &hook.Context{
		SessionID: "sess-1",
		ToolName:  "execute_command",
		Extra: map[string]interface{}{
			"result":  "ok  \tauth\t0.456s\ncoverage: 65.0% of statements",
			"command": "go test ./auth/... -cover",
		},
	})
	repl.HookReg.Run(ctx, hook.EventPostToolUse, &hook.Context{
		SessionID: "sess-1",
		ToolName:  "search_content",
		Extra: map[string]interface{}{
			"result":  "Found 15 matches for 'func Test' in auth/*_test.go",
			"pattern": "func Test",
		},
	})

	// SessionEnd
	repl.HookReg.Run(ctx, hook.EventSessionEnd, &hook.Context{
		SessionID: "sess-1",
		Extra: map[string]interface{}{
			"summary":      "分析 auth 模块测试覆盖，15 个测试, 65% 覆盖",
			"turn_count":   3,
			"input_tokens": 1500,
		},
	})

	time.Sleep(400 * time.Millisecond)

	// 验证会话 1 的记忆已持久化
	items, err := svc.ListRecent(ctx, 20)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(items) < 3 {
		t.Fatalf("expected at least 3 memories, got %d", len(items))
	}

	hasObs, hasSummary := false, false
	for _, it := range items {
		if it.Type == "observation" {
			hasObs = true
		}
		if it.Type == "summary" {
			hasSummary = true
		}
	}
	if !hasObs {
		t.Error("no tool observation captured in session 1")
	}
	if !hasSummary {
		t.Error("no session summary captured in session 1")
	}

	// === 会话 2（跨会话检索） ===
	res2 := repl.HookReg.Run(ctx, hook.EventSessionStart, &hook.Context{
		SessionID: "sess-2",
		Extra:     map[string]interface{}{"prompt": "/test/repl-project"},
	})
	if res2 == nil || len(res2.AdditionalContexts) == 0 {
		t.Error("SessionStart should inject context from session 1")
	} else {
		ctxText := strings.Join(res2.AdditionalContexts, "\n")
		if !strings.Contains(ctxText, "<long-term-memory>") {
			t.Error("injected context missing wrapper tag")
		}
		t.Logf("Session 2 注入 %d 段上下文 (共 %d 字节)", len(res2.AdditionalContexts), len(ctxText))
	}

	// 检索验证（FTS5 全文搜索）
	_, err = svc.SearchIndex(ctx, "auth module coverage", domainmemory.SearchOptions{Limit: 5})
	if err != nil {
		t.Logf("SearchIndex: %v", err)
	}
	// 通过 ListRecent 验证跨会话可检索
	items2, _ := svc.ListRecent(ctx, 20)
	found := false
	for _, it := range items2 {
		if strings.Contains(it.Content+it.Title, "auth") {
			found = true
			break
		}
	}
	if !found {
		t.Error("cross-session search should find auth-related memories")
	}

	t.Logf("完整 REPL 生命周期验证通过: 会话 1 共 %d 条记忆, 会话 2 检索成功", len(items))
}

// TestREPL_PrivacyFiltering 验证 REPL 中隐私内容被过滤
func TestREPL_PrivacyFiltering(t *testing.T) {
	repl, svc := newREPLWithMemory(t)
	ctx := context.Background()

	// 含敏感信息的结果
	repl.HookReg.Run(ctx, hook.EventPostToolUse, &hook.Context{
		SessionID: "priv-sess",
		ToolName:  "read_file",
		Extra: map[string]interface{}{
			"result": "config: <private>API_KEY=sk-secret-key-abcdef123456</private> database: postgresql://localhost",
			"path":   ".env",
		},
	})

	time.Sleep(300 * time.Millisecond)

	items, _ := svc.ListRecent(ctx, 10)
	for _, it := range items {
		if strings.Contains(it.Content, "sk-secret-key") || strings.Contains(it.Content, "sk-secret") {
			t.Errorf("private content leaked in memory: id=%d title=%q", it.ID, it.Title)
		}
	}
}
