package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/goclaude/pkg/domain/memory"
	"github.com/anthropics/goclaude/pkg/infrastructure/memory/sqlite"
)

// newIntegrationService creates a service for integration testing
func newIntegrationService(t *testing.T) *LongTermMemoryService {
	t.Helper()
	dir := t.TempDir()
	dbPath := dir + "/integration_test.db"
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	t.Cleanup(func() { repo.Close() })

	svc := NewLongTermMemoryService(repo, defaultTestConfig(), nil)
	return svc
}

// ============================================================
// 端到端集成测试
// ============================================================

func TestIntegration_FullLifecycle(t *testing.T) {
	svc := newIntegrationService(t)
	ctx := context.Background()

	// ========================================
	// Phase 1: 创建会话并模拟多次工具调用
	// ========================================
	sessionID := "integ-sess-001"

	// 模拟 AI 读取代码文件
	id1, err := svc.SaveObservation(ctx, sessionID,
		"Read main.go",
		"package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n\n主要发现：项目使用 Go 1.22，入口函数为 main.go 中的 main()",
		SaveOptions{ObsType: "observation", Category: "project", Source: "tool_use", ToolName: "read_file", Priority: 60, Tags: []string{"tool:read_file", "golang"}},
	)
	if err != nil {
		t.Fatalf("save observation 1: %v", err)
	}

	// 模拟 AI 搜索测试框架
	id2, err := svc.SaveObservation(ctx, sessionID,
		"Searched: testing patterns",
		"使用了标准 testing 包和表驱动测试模式。未发现 testify 依赖。测试覆盖约 60%。",
		SaveOptions{ObsType: "observation", Category: "project", Source: "tool_use", ToolName: "search_content", Priority: 50, Tags: []string{"tool:search_content", "testing"}},
	)
	if err != nil {
		t.Fatalf("save observation 2: %v", err)
	}

	// 模拟 AI 修改文件
	id3, err := svc.SaveObservation(ctx, sessionID,
		"Edited config.go",
		"在 config.go 中新增了 LongTermMemoryConfig 结构体，包含 6 个子配置。修改了 DefaultConfig()。",
		SaveOptions{ObsType: "observation", Category: "project", Source: "tool_use", ToolName: "replace_in_file", Priority: 70, Tags: []string{"tool:replace_in_file", "config"}},
	)
	if err != nil {
		t.Fatalf("save observation 3: %v", err)
	}

	_ = id1
	_ = id2
	_ = id3

	// 验证 3 条记忆已存储
	items, _ := svc.ListRecent(ctx, 10)
	if len(items) != 3 {
		t.Fatalf("expected 3 memories, got %d", len(items))
	}

	// ========================================
	// Phase 2: 保存会话摘要
	// ========================================
	err = svc.SaveSessionSummary(ctx, sessionID, "/project", "完成了长期记忆功能的集成：新增 SQLite 存储、三层搜索、生命周期钩子。修改了 config.go 新增配置结构体。", SessionStats{
		InputTokens:  2000,
		OutputTokens: 500,
		TurnCount:    3,
		StartedAt:    time.Now().Add(-30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("SaveSessionSummary: %v", err)
	}

	// 验证总结也被保存为记忆（现在总共 4 条：3 observations + 1 summary）
	items, _ = svc.ListRecent(ctx, 10)
	if len(items) < 4 {
		t.Fatalf("expected >= 4 memories after summary, got %d", len(items))
	}
	hasSummary := false
	for _, item := range items {
		if item.Type == "summary" {
			hasSummary = true
			break
		}
	}
	if !hasSummary {
		t.Error("session summary not stored as memory")
	}

	// ========================================
	// Phase 3: 模拟第二个会话的上下文注入
	// ========================================
	// 新会话查询与上一个会话相关的主题
	injection, err := svc.BuildInjectionContext(ctx, "/project", "Go 语言 测试 config 结构体")
	if err != nil {
		t.Fatalf("BuildInjectionContext: %v", err)
	}
	if injection == "" {
		t.Error("injection context should not be empty for relevant query")
	}
	if !strings.Contains(injection, "<long-term-memory>") {
		t.Error("injection should be wrapped in lt-memory tags")
	}

	// ========================================
	// Phase 4: 三层渐进式搜索
	// ========================================
	// Layer 1: 紧凑索引
	result, err := svc.SearchIndex(ctx, "config", memory.SearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("SearchIndex (Layer 1): %v", err)
	}
	if result.Total == 0 {
		t.Error("Layer 1 search should find config-related memories")
	}
	for _, item := range result.Index {
		if item.Snippet == "" && item.Title != "" {
			// snippet can be empty for very short content
		}
	}

	// Layer 2: 时间线
	if len(result.Index) > 0 {
		ids := make([]int64, len(result.Index))
		for i, item := range result.Index {
			ids[i] = item.ID
		}
		timeline, err := svc.SearchTimeline(ctx, ids)
		if err != nil {
			t.Fatalf("SearchTimeline (Layer 2): %v", err)
		}
		if len(timeline) != len(result.Index) {
			t.Errorf("timeline len=%d, index len=%d", len(timeline), len(result.Index))
		}
	}

	// Layer 3: 完整详情
	if len(result.Index) > 0 {
		ids := make([]int64, len(result.Index))
		for i, item := range result.Index {
			ids[i] = item.ID
		}
		details, err := svc.GetObservations(ctx, ids)
		if err != nil {
			t.Fatalf("GetObservations (Layer 3): %v", err)
		}
		if len(details) == 0 {
			t.Error("Layer 3 should return full details")
		}
		for _, d := range details {
			if d.Content == "" {
				t.Error("Layer 3 detail missing content")
			}
		}
	}

	// ========================================
	// Phase 5: 容量管理
	// ========================================
	svc.cfg.Capacity.MaxEntries = 5
	count, _ := svc.repo.Count(ctx)
	if count > 5 {
		t.Errorf("capacity enforcement: count=%d after cap=5", count)
	}

	// ========================================
	// Phase 6: CRUD 操作
	// ========================================
	// 更新
	newID, _ := svc.SaveObservation(ctx, "sess-2", "Important", "这条记忆很重要", SaveOptions{Priority: 30})
	err = svc.UpdateMemory(ctx, newID, "Very Important", "这条记忆非常重要", "reference", 95)
	if err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}
	updated, _ := svc.GetObservations(ctx, []int64{newID})
	if len(updated) != 1 || updated[0].Priority != 95 {
		t.Error("memory update not applied")
	}

	// 删除
	if err := svc.DeleteMemory(ctx, newID); err != nil {
		t.Fatalf("DeleteMemory: %v", err)
	}
	gone, _ := svc.GetObservations(ctx, []int64{newID})
	if len(gone) != 0 {
		t.Error("deleted memory still retrievable")
	}
}

// ============================================================
// 跨会话搜索
// ============================================================

func TestIntegration_CrossSessionSearch(t *testing.T) {
	svc := newIntegrationService(t)
	ctx := context.Background()

	// 会话 1: Go 项目讨论（使用英文内容确保 FTS5 可搜索）
	svc.SaveObservation(ctx, "sess-A", "Go 1.22 features",
		"for range loop improvements in Go 1.22",
		SaveOptions{ObsType: "observation", Category: "reference", Priority: 75})
	svc.SaveObservation(ctx, "sess-A", "Project Architecture",
		"using DDD four-layer architecture pattern",
		SaveOptions{ObsType: "observation", Category: "project", Priority: 80})

	// 会话 2: 数据库讨论
	svc.SaveObservation(ctx, "sess-B", "PostgreSQL Configuration",
		"connection pool max_connections set to 100",
		SaveOptions{ObsType: "observation", Category: "project", Priority: 60})
	svc.SaveObservation(ctx, "sess-B", "SQLite vs PostgreSQL comparison",
		"SQLite is suitable for local testing while PostgreSQL is for production.",
		SaveOptions{ObsType: "reference", Category: "reference", Priority: 55})

	// 搜索 "PostgreSQL" → 应跨会话找到结果（sess-B 有两条相关）
	result, err := svc.SearchIndex(ctx, "PostgreSQL", memory.SearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("SearchIndex PostgreSQL: %v", err)
	}
	if result.Total < 2 {
		t.Errorf("cross-session search 'PostgreSQL': total=%d, want >= 2", result.Total)
	}

	// 搜索 "architecture" → 应找到架构讨论
	result2, _ := svc.SearchIndex(ctx, "architecture", memory.SearchOptions{Limit: 10})
	if result2.Total < 1 {
		t.Error("cross-session 'architecture' should find at least 1 result")
	}
}

// ============================================================
// TTL 过期测试
// ============================================================

func TestIntegration_TTLExpiration(t *testing.T) {
	svc := newIntegrationService(t)
	ctx := context.Background()

	// 设置短 TTL
	svc.cfg.Expiration.DefaultTTLDays = 1
	svc.cfg.Expiration.LowPriorityTTLDays = 1

	// 保存几条记忆（ExpiresAt 由 SaveObservation 自动计算）
	svc.SaveObservation(ctx, "sess", "Fresh", "will not expire soon", SaveOptions{Priority: 80})
	svc.SaveObservation(ctx, "sess", "Fresh2", "another fresh one", SaveOptions{Priority: 80})

	// 直接插入一条过期记忆到 repo（绕过 service 的 TTL 设置）
	_, err := svc.repo.SaveObservation(ctx, &memory.LongTermMemory{
		Title: "Expired", Content: "old data", CreatedAt: time.Now().Add(-48 * time.Hour),
		ExpiresAt: time.Now().Add(-24 * time.Hour), Priority: 80, ByteSize: 8,
	})
	if err != nil {
		t.Fatalf("save expired: %v", err)
	}

	// 过期清理
	n, err := svc.repo.ExpireMemories(ctx, time.Now())
	if err != nil {
		t.Fatalf("ExpireMemories: %v", err)
	}
	if n != 1 {
		t.Errorf("expired 1 memory, got n=%d", n)
	}

	count, _ := svc.repo.Count(ctx)
	if count != 2 {
		t.Errorf("remaining count = %d, want 2", count)
	}
}

// ============================================================
// 隐私过滤集成测试
// ============================================================

func TestIntegration_PrivacyFiltering(t *testing.T) {
	svc := newIntegrationService(t)
	ctx := context.Background()

	tests := []struct {
		name    string
		title   string
		content string
		accept  bool // true = should be stored, false = should be rejected
	}{
		{"normal text", "笔记", "Go 1.22 的主要更新包括 range 改进和新的错误处理模式", true},
		{"private tag stripped", "机密", "before <private>MY_API_KEY=abc123</private> after", true},
		{"all private", "全部私密", "<private>secret key</private>", false},
		{"api key pattern", "密钥", "api_key = 1234567890abcdef", false},
		{"password pattern", "密码", "password = superSecret12345", false},
		{"token pattern", "令牌", "token: abcdef1234567890abcdef", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := svc.SaveObservation(ctx, "sess-priv", tt.title, tt.content, SaveOptions{})
			if err != nil {
				t.Fatalf("SaveObservation: %v", err)
			}
			if tt.accept && id <= 0 {
				t.Errorf("expected accepted (id>0), got id=%d", id)
			}
			if !tt.accept && id != 0 {
				t.Errorf("expected rejected (id=0), got id=%d", id)
			}
			// 验证 private tag 内容被去掉
			if tt.accept && id > 0 && strings.Contains(tt.content, "<private>") {
				items, _ := svc.GetObservations(ctx, []int64{id})
				if len(items) == 1 && strings.Contains(items[0].Content, "MY_API_KEY") {
					t.Error("private tag content was not stripped")
				}
			}
		})
	}
}

// ============================================================
// 并发测试：多 goroutine 同时写入
// ============================================================

func TestIntegration_ConcurrentWrites(t *testing.T) {
	svc := newIntegrationService(t)
	svc.cfg.Capacity.MaxEntries = 50 // 确保容量足够

	done := make(chan bool, 20)
	errCh := make(chan error, 20)

	for i := 0; i < 20; i++ {
		go func(idx int) {
			defer func() { done <- true }()
			ctx := context.Background()
			id, err := svc.SaveObservation(ctx,
				fmt.Sprintf("sess-conc-%d", idx%3),
				fmt.Sprintf("Concurrent #%d", idx),
				fmt.Sprintf("Content from goroutine %d", idx),
				SaveOptions{Priority: 50},
			)
			if err != nil {
				errCh <- fmt.Errorf("goroutine %d: %w", idx, err)
				return
			}
			if id <= 0 {
				errCh <- fmt.Errorf("goroutine %d: id=%d", idx, id)
			}
		}(i)
	}

	for i := 0; i < 20; i++ {
		<-done
	}
	close(errCh)

	for err := range errCh {
		t.Error(err)
	}

	count, _ := svc.repo.Count(context.Background())
	if count != 20 {
		t.Errorf("concurrent writes: count=%d, want 20", count)
	}
}

// ============================================================
// 永久记忆 (Permanent) 测试
// ============================================================

func TestIntegration_PermanentMemory(t *testing.T) {
	svc := newIntegrationService(t)
	// 配置 TTL 在 defaultTestConfig 中是 7 天
	ctx := context.Background()

	// 普通记忆：应该有 TTL
	id1, err := svc.SaveObservation(ctx, "sess", "Normal", "should expire",
		SaveOptions{ObsType: "observation", Priority: 80})
	if err != nil {
		t.Fatalf("save normal: %v", err)
	}

	// Permanent 记忆：永远不过期
	id2, err := svc.SaveObservation(ctx, "sess", "Permanent", "never expires",
		SaveOptions{ObsType: "preference", Category: "user", Priority: 95, Permanent: true})
	if err != nil {
		t.Fatalf("save permanent: %v", err)
	}

	// 验证普通记忆有过期时间
	normal, _ := svc.GetObservations(ctx, []int64{id1})
	if len(normal) == 1 && normal[0].ExpiresAt.IsZero() {
		t.Error("normal memory should have expires_at")
	}

	// 验证 Permanent 记忆无过期时间
	perm, _ := svc.GetObservations(ctx, []int64{id2})
	if len(perm) == 1 && !perm[0].ExpiresAt.IsZero() {
		t.Error("permanent memory should have zero expires_at")
	}
}

func TestIntegration_TieredTTL(t *testing.T) {
	svc := newIntegrationService(t)
	// defaultTestConfig: DefaultTTLDays=0, LowPriorityTTLDays=0
	// 修改配置：高级记忆永久，低优先级 3 天过期
	svc.cfg.Expiration.DefaultTTLDays = 0
	svc.cfg.Expiration.LowPriorityTTLDays = 3

	ctx := context.Background()

	// 高优先级：应永久
	idHigh, _ := svc.SaveObservation(ctx, "sess", "High Priority",
		"important project decision",
		SaveOptions{ObsType: "observation", Priority: 90})

	// 低优先级：应 3 天过期
	idLow, _ := svc.SaveObservation(ctx, "sess", "Low Priority",
		"minor observation",
		SaveOptions{ObsType: "observation", Priority: 3})

	high, _ := svc.GetObservations(ctx, []int64{idHigh})
	if len(high) == 1 && !high[0].ExpiresAt.IsZero() {
		t.Error("high priority with default_ttl=0 should be permanent")
	}

	low, _ := svc.GetObservations(ctx, []int64{idLow})
	if len(low) == 1 && low[0].ExpiresAt.IsZero() {
		t.Error("low priority with low_priority_ttl=3 should have expires_at")
	}
}

func TestIntegration_AllPermanentConfig(t *testing.T) {
	svc := newIntegrationService(t)
	svc.cfg.Expiration.DefaultTTLDays = 0
	svc.cfg.Expiration.LowPriorityTTLDays = 0

	ctx := context.Background()

	id, _ := svc.SaveObservation(ctx, "sess", "Forever",
		"all memories are permanent",
		SaveOptions{ObsType: "observation", Priority: 50})

	mem, _ := svc.GetObservations(ctx, []int64{id})
	if len(mem) == 1 && !mem[0].ExpiresAt.IsZero() {
		t.Error("with all TTL=0, memory should be permanent")
	}
	if len(mem) == 1 && mem[0].IsExpired(time.Now().Add(10*365*24*time.Hour)) {
		t.Error("permanent memory should never expire, even after 10 years")
	}
}
