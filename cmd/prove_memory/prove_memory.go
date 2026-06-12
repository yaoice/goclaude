//go:build ignore
// +build ignore

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	hooksapp "github.com/yaoice/goclaude/pkg/application/hooks"
	memoryapp "github.com/yaoice/goclaude/pkg/application/memory"
	"github.com/yaoice/goclaude/pkg/domain/hook"
	domainmemory "github.com/yaoice/goclaude/pkg/domain/memory"
	"github.com/yaoice/goclaude/pkg/infrastructure/appconfig"
	sqlitemem "github.com/yaoice/goclaude/pkg/infrastructure/memory/sqlite"
)

func main() {
	dir, _ := os.MkdirTemp("", "ltm-prove-*")
	defer os.RemoveAll(dir)

	dbPath := dir + "/ltm.db"
	fmt.Println("========================================")
	fmt.Println(" 长期记忆端到端验证")
	fmt.Println("========================================")
	fmt.Printf(" DB: %s\n", dbPath)

	repo, _ := sqlitemem.NewRepository(dbPath)
	defer repo.Close()

	cfg := appconfig.LongTermMemoryConfig{
		Enabled: true,
		Capture: appconfig.LongTermCaptureConfig{
			AutoCaptureTools: true, MaxObservationSize: 8000, MinCaptureChars: 10,
		},
		Injection: appconfig.LongTermInjectionConfig{
			AutoInject: true, MaxInjectTokens: 2000, SearchLimit: 10, MinRelevanceScore: 0.1,
		},
		Capacity: appconfig.LongTermCapacityConfig{MaxEntries: 100, MaxStorageBytes: 10 * 1024 * 1024},
		Eviction: appconfig.LongTermEvictionConfig{MinPriority: 5},
		Expiration: appconfig.LongTermExpirationConfig{
			DefaultTTLDays: 90, LowPriorityTTLDays: 30, CleanupIntervalHours: 0,
		},
		Privacy: appconfig.LongTermPrivacyConfig{AutoExcludePatterns: true, StripPrivateTags: true},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))
	svc := memoryapp.NewLongTermMemoryService(repo, cfg, logger)
	svc.Start(context.Background())
	defer svc.Close()

	reg := hook.NewRegistry(logger)
	h := hooksapp.NewMemoryLifecycleHooks(svc, cfg, logger, "/my-go-project")
	h.RegisterAll(reg)

	ctx := context.Background()

	// ═══════════════════════════════════════════
	// 会话 1：正常编程会话
	// ═══════════════════════════════════════════
	fmt.Println("\n── 会话 1 开始 ──")

	// 1. SessionStart（空库，无注入）
	reg.Run(ctx, hook.EventSessionStart, &hook.Context{
		SessionID: "sess-001",
		Extra:     map[string]interface{}{"prompt": "/my-go-project"},
	})

	// 2. 用户提交问题
	reg.Run(ctx, hook.EventUserPromptSubmit, &hook.Context{
		SessionID: "sess-001",
		Extra:     map[string]interface{}{"prompt": "帮我实现一个基于 JWT 的用户认证模块"},
	})

	// 3. AI 调用工具
	reg.Run(ctx, hook.EventPostToolUse, &hook.Context{
		SessionID: "sess-001", ToolName: "read_file",
		Extra: map[string]interface{}{
			"result":    "// handler/auth.go — 空文件，尚未实现认证逻辑\npackage auth\nfunc AuthMiddleware() {}",
			"file_path": "/my-go-project/handler/auth.go",
		},
	})
	reg.Run(ctx, hook.EventPostToolUse, &hook.Context{
		SessionID: "sess-001", ToolName: "write_to_file",
		Extra: map[string]interface{}{
			"result":      "Wrote auth/handler.go (45 lines) — JWT 中间件 + token 验证",
			"target_file": "/my-go-project/auth/handler.go",
		},
	})
	reg.Run(ctx, hook.EventPostToolUse, &hook.Context{
		SessionID: "sess-001", ToolName: "execute_command",
		Extra: map[string]interface{}{
			"result":  "go test ./auth/... -cover\nok\tauth\t0.234s\tcoverage: 72.5%",
			"command": "go test ./auth/... -cover",
		},
	})

	// 4. 会话结束
	reg.Run(ctx, hook.EventSessionEnd, &hook.Context{
		SessionID: "sess-001",
		Extra: map[string]interface{}{
			"summary":    "实现了 JWT 认证模块，包含 token 生成、验证中间件、单元测试，覆盖率 72.5%",
			"turn_count": 3, "input_tokens": 1200, "output_tokens": 450,
		},
	})
	fmt.Println("  会话 1 结束")

	time.Sleep(300 * time.Millisecond)

	// 验证持久化
	items, _ := svc.ListRecent(ctx, 50)
	fmt.Printf("\n  → 持久化了 %d 条记忆:\n", len(items))
	for _, it := range items {
		fmt.Printf("    [id=%d type=%-12s] %s\n", it.ID, it.Type, it.Title)
	}

	// ═══════════════════════════════════════════
	// 会话 2：第二天——新会话能否检索到历史
	// ═══════════════════════════════════════════
	fmt.Println("\n── 会话 2 开始（第二天）──")

	res := reg.Run(ctx, hook.EventSessionStart, &hook.Context{
		SessionID: "sess-002",
		Extra:     map[string]interface{}{"prompt": "/my-go-project"},
	})

	if res == nil || len(res.AdditionalContexts) == 0 {
		fmt.Println("\n  ❌ SessionStart 没有注入任何上下文")
		os.Exit(1)
	}

	fmt.Printf("\n  → SessionStart 注入了 %d 段上下文:\n", len(res.AdditionalContexts))
	for i, c := range res.AdditionalContexts {
		fmt.Printf("\n  --- 上下文 %d ---\n%s\n", i+1, c)
	}

	// FTS5 搜索验证
	result, _ := svc.SearchIndex(ctx, "JWT auth", domainmemory.SearchOptions{Limit: 5})
	fmt.Printf("\n  → 关键词 \"JWT auth\" 搜索命中 %d 条:\n", len(result.Index))
	for _, r := range result.Index {
		fmt.Printf("    [id=%d] %s\n", r.ID, r.Title)
	}

	// ═══════════════════════════════════════════
	// 结论
	// ═══════════════════════════════════════════
	fmt.Println("\n========================================")
	hasInjection := strings.Contains(
		strings.Join(res.AdditionalContexts, "\n"), "long-term-memory",
	)
	allOk := len(items) >= 3 && hasInjection && len(result.Index) > 0

	if allOk {
		fmt.Println(" ✅ 长期记忆有效")
		fmt.Println("    会话 1 的 4 条记忆 → 持久化到 SQLite+FTS5")
		fmt.Println("    会话 2 SessionStart → 自动检索并注入 <long-term-memory> 上下文")
		fmt.Println("    关键词搜索 → FTS5 命中历史记忆")
	} else {
		fmt.Println(" ❌ 验证失败")
		os.Exit(1)
	}
	fmt.Println("========================================")
}
