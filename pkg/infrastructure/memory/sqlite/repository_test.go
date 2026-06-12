package sqlite

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yaoice/goclaude/pkg/domain/memory"
)

// newTestRepo creates a SQLite Repository backed by a temp file, auto-cleaned.
func newTestRepo(t *testing.T) *Repository {
	t.Helper()
	dir := t.TempDir()
	dbPath := dir + "/test.db"
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	t.Cleanup(func() { repo.Close() })
	return repo
}

// ============================================================
// CRUD
// ============================================================

func TestRepository_SaveAndGet(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	mem := &memory.LongTermMemory{
		SessionID: "s1", Type: "observation", Title: "Test Obs",
		Content: "Go backend development.", Category: "project",
		Source: "tool_use", ToolName: "read_file", Priority: 70,
		Tags: "golang", ByteSize: 24, CreatedAt: time.Now(), LastAccessed: time.Now(),
	}

	id, err := repo.SaveObservation(ctx, mem)
	if err != nil {
		t.Fatalf("SaveObservation: %v", err)
	}
	if id <= 0 {
		t.Errorf("id = %d, want > 0", id)
	}

	got, err := repo.GetObservation(ctx, id)
	if err != nil {
		t.Fatalf("GetObservation: %v", err)
	}
	if got == nil {
		t.Fatal("GetObservation returned nil")
	}
	if got.Title != mem.Title {
		t.Errorf("Title = %q, want %q", got.Title, mem.Title)
	}
	if got.Priority != 70 {
		t.Errorf("Priority = %d, want 70", got.Priority)
	}
}

func TestRepository_GetNotFound(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	got, _ := repo.GetObservation(ctx, 99999)
	if got != nil {
		t.Error("expected nil for non-existent ID")
	}
}

func TestRepository_GetObservations_Batch(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	var ids []int64
	for i := 0; i < 5; i++ {
		id, err := repo.SaveObservation(ctx, &memory.LongTermMemory{
			Title:     fmt.Sprintf("M%d", i),
			Content:   fmt.Sprintf("C%d", i),
			CreatedAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
		ids = append(ids, id)
	}

	items, err := repo.GetObservations(ctx, ids[:3])
	if err != nil {
		t.Fatalf("GetObservations: %v", err)
	}
	if len(items) != 3 {
		t.Errorf("got %d items, want 3", len(items))
	}
}

func TestRepository_GetObservations_Empty(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	items, _ := repo.GetObservations(ctx, nil)
	if len(items) != 0 {
		t.Error("expected empty for nil input")
	}
}

func TestRepository_UpdateObservation(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	id, _ := repo.SaveObservation(ctx, &memory.LongTermMemory{
		Title: "Original", Content: "orig", CreatedAt: time.Now(),
	})

	err := repo.UpdateObservation(ctx, &memory.LongTermMemory{
		ID: id, Title: "Updated", Content: "new", Category: "reference",
		Priority: 90, Tags: "updated", ByteSize: 10,
	})
	if err != nil {
		t.Fatalf("UpdateObservation: %v", err)
	}

	got, _ := repo.GetObservation(ctx, id)
	if got.Title != "Updated" || got.Priority != 90 {
		t.Errorf("update not applied: title=%s priority=%d", got.Title, got.Priority)
	}
}

func TestRepository_DeleteObservation(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	id, _ := repo.SaveObservation(ctx, &memory.LongTermMemory{
		Title: "Del", Content: "me", CreatedAt: time.Now(),
	})

	if err := repo.DeleteObservation(ctx, id); err != nil {
		t.Fatalf("DeleteObservation: %v", err)
	}
	got, _ := repo.GetObservation(ctx, id)
	if got != nil {
		t.Error("GetObservation after delete should be nil")
	}
}

func TestRepository_RecordAccess(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	id, _ := repo.SaveObservation(ctx, &memory.LongTermMemory{
		Title: "Access", Content: "test", CreatedAt: time.Now(),
	})

	for i := 0; i < 3; i++ {
		repo.RecordAccess(ctx, id)
	}
	got, _ := repo.GetObservation(ctx, id)
	if got.AccessCount != 3 {
		t.Errorf("AccessCount = %d, want 3", got.AccessCount)
	}
}

// ============================================================
// FTS5 搜索
// ============================================================

func TestRepository_SearchIndex_FTS5(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	data := []*memory.LongTermMemory{
		{Title: "Go语言项目", Content: "使用Go 1.22开发后端", Category: "project", Priority: 80, CreatedAt: time.Now(), LastAccessed: time.Now()},
		{Title: "Python脚本", Content: "数据处理脚本", Category: "reference", Priority: 50, CreatedAt: time.Now(), LastAccessed: time.Now()},
		{Title: "Go测试", Content: "表驱动测试", Category: "project", Priority: 70, CreatedAt: time.Now(), LastAccessed: time.Now()},
		{Title: "数据库配置", Content: "PostgreSQL连接池", Category: "project", Priority: 60, CreatedAt: time.Now(), LastAccessed: time.Now()},
	}
	for _, m := range data {
		repo.SaveObservation(ctx, m)
	}

	result, err := repo.SearchIndex(ctx, "Go", memory.SearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("SearchIndex Go: %v", err)
	}
	if result.Total < 2 {
		t.Errorf("search 'Go' total = %d, want >= 2", result.Total)
	}

	result2, err := repo.SearchIndex(ctx, "PostgreSQL", memory.SearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("SearchIndex PostgreSQL: %v", err)
	}
	if result2.Total < 1 {
		t.Error("search 'PostgreSQL' should find at least 1 result")
	}

	result3, err := repo.SearchIndex(ctx, "Python", memory.SearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("SearchIndex Python: %v", err)
	}
	if result3.Total < 1 {
		t.Error("search 'Python' should find at least 1 result")
	}
}

func TestRepository_SearchIndex_TypeFilter(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	repo.SaveObservation(ctx, &memory.LongTermMemory{
		Title: "Obs", Content: "Go observation", Type: "observation", CreatedAt: time.Now(), LastAccessed: time.Now(),
	})
	repo.SaveObservation(ctx, &memory.LongTermMemory{
		Title: "Sum", Content: "Go summary", Type: "summary", CreatedAt: time.Now(), LastAccessed: time.Now(),
	})

	result, err := repo.SearchIndex(ctx, "Go", memory.SearchOptions{Type: "summary", Limit: 10})
	if err != nil {
		t.Fatalf("SearchIndex with type filter: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("type-filtered total = %d, want 1", result.Total)
	}
	if len(result.Index) > 0 && result.Index[0].Type != "summary" {
		t.Error("type filter not applied")
	}
}

func TestRepository_SearchIndex_CategoryFilter(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	repo.SaveObservation(ctx, &memory.LongTermMemory{
		Title: "P1", Content: "Go project", Category: "project", CreatedAt: time.Now(), LastAccessed: time.Now(),
	})
	repo.SaveObservation(ctx, &memory.LongTermMemory{
		Title: "U1", Content: "Go user pref", Category: "user", CreatedAt: time.Now(), LastAccessed: time.Now(),
	})

	result, err := repo.SearchIndex(ctx, "Go", memory.SearchOptions{Category: "user", Limit: 10})
	if err != nil {
		t.Fatalf("SearchIndex with category filter: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("category-filtered total = %d, want 1", result.Total)
	}
	if len(result.Index) > 0 && result.Index[0].Category != "user" {
		t.Error("category filter not applied")
	}
}

func TestRepository_SearchIndex_LimitAndOffset(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		repo.SaveObservation(ctx, &memory.LongTermMemory{
			Title: fmt.Sprintf("Go Item %d", i), Content: fmt.Sprintf("Content %d", i),
			CreatedAt: time.Now(), LastAccessed: time.Now(),
		})
	}

	result, err := repo.SearchIndex(ctx, "Go", memory.SearchOptions{Limit: 5})
	if err != nil {
		t.Fatalf("SearchIndex limit: %v", err)
	}
	if len(result.Index) > 5 {
		t.Errorf("limit not enforced: got %d items", len(result.Index))
	}
}

func TestRepository_SearchTimeline(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	id1, _ := repo.SaveObservation(ctx, &memory.LongTermMemory{
		Title: "T1", Content: "c1", SessionID: "sess-a", CreatedAt: time.Now(),
	})
	id2, _ := repo.SaveObservation(ctx, &memory.LongTermMemory{
		Title: "T2", Content: "c2", SessionID: "sess-a", CreatedAt: time.Now(),
	})

	items, err := repo.SearchTimeline(ctx, []int64{id1, id2})
	if err != nil {
		t.Fatalf("SearchTimeline: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("timeline len = %d, want 2", len(items))
	}
	for _, item := range items {
		if item.Content == "" {
			t.Error("timeline item missing content")
		}
	}
}

// ============================================================
// 会话管理
// ============================================================

func TestRepository_SaveAndGetSession(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	s := &memory.LongTermSession{
		ID: "sess-test", WorkingDir: "/tmp", ProjectRoot: "/project",
		Model: "claude", Summary: "test session", InputTokens: 100,
		OutputTokens: 200, TurnCount: 3, StartedAt: time.Now(),
	}
	if err := repo.SaveSession(ctx, s); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	sessions, err := repo.GetRecentSessions(ctx, 5)
	if err != nil {
		t.Fatalf("GetRecentSessions: %v", err)
	}
	found := false
	for _, sess := range sessions {
		if sess.ID == "sess-test" {
			found = true
			if sess.Summary != "test session" {
				t.Errorf("summary = %s", sess.Summary)
			}
		}
	}
	if !found {
		t.Error("session not found")
	}
}

func TestRepository_UpdateSessionEnd(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	repo.SaveSession(ctx, &memory.LongTermSession{
		ID: "sess-end", StartedAt: time.Now(),
	})

	ended := time.Now()
	if err := repo.UpdateSessionEnd(ctx, "sess-end", "final summary", ended); err != nil {
		t.Fatalf("UpdateSessionEnd: %v", err)
	}

	sessions, _ := repo.GetRecentSessions(ctx, 1)
	if len(sessions) != 1 {
		t.Fatal("expected 1 session")
	}
	if sessions[0].Summary != "final summary" {
		t.Errorf("summary = %s", sessions[0].Summary)
	}
}

func TestRepository_GetRecentSessions_Limit(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		repo.SaveSession(ctx, &memory.LongTermSession{
			ID: fmt.Sprintf("s-%d", i), StartedAt: time.Now().Add(-time.Duration(i) * time.Hour),
		})
	}

	sessions, _ := repo.GetRecentSessions(ctx, 3)
	if len(sessions) > 3 {
		t.Errorf("GetRecentSessions limit not enforced: got %d", len(sessions))
	}
}

// ============================================================
// 容量管理
// ============================================================

func TestRepository_Count(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	n, _ := repo.Count(ctx)
	if n != 0 {
		t.Errorf("initial count = %d, want 0", n)
	}

	for i := 0; i < 5; i++ {
		repo.SaveObservation(ctx, &memory.LongTermMemory{
			Title: fmt.Sprintf("T%d", i), Content: "c", CreatedAt: time.Now(),
		})
	}

	n, _ = repo.Count(ctx)
	if n != 5 {
		t.Errorf("count = %d, want 5", n)
	}
}

func TestRepository_TotalBytes(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	repo.SaveObservation(ctx, &memory.LongTermMemory{
		Title: "Hello", Content: "World!", ByteSize: 12, CreatedAt: time.Now(),
	})
	repo.SaveObservation(ctx, &memory.LongTermMemory{
		Title: "Foo", Content: "Bar", ByteSize: 6, CreatedAt: time.Now(),
	})

	total, _ := repo.TotalBytes(ctx)
	if total != 18 {
		t.Errorf("TotalBytes = %d, want 18", total)
	}
}

func TestRepository_ExpireMemories(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	// 插入3条：1 已过期，1 将来过期，1 无过期
	repo.SaveObservation(ctx, &memory.LongTermMemory{
		Title: "expired", Content: "old", CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	})
	repo.SaveObservation(ctx, &memory.LongTermMemory{
		Title: "future", Content: "new", CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(24 * time.Hour),
	})
	repo.SaveObservation(ctx, &memory.LongTermMemory{
		Title: "never", Content: "forever", CreatedAt: time.Now(),
	})

	n, err := repo.ExpireMemories(ctx, time.Now())
	if err != nil {
		t.Fatalf("ExpireMemories: %v", err)
	}
	if n != 1 {
		t.Errorf("expired count = %d, want 1", n)
	}

	c, _ := repo.Count(ctx)
	if c != 2 {
		t.Errorf("remaining count = %d, want 2", c)
	}
}

func TestRepository_EvictByScore(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		repo.SaveObservation(ctx, &memory.LongTermMemory{
			Title: fmt.Sprintf("M%d", i), Content: "c",
			Priority: i * 10, CreatedAt: time.Now(),
		})
	}

	n, err := repo.EvictByScore(ctx, 5)
	if err != nil {
		t.Fatalf("EvictByScore: %v", err)
	}
	if n != 5 {
		t.Errorf("evicted = %d, want 5", n)
	}

	c, _ := repo.Count(ctx)
	if c != 5 {
		t.Errorf("remaining count = %d, want 5", c)
	}
}

func TestRepository_EvictByLRU(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	for i := 0; i < 8; i++ {
		id, _ := repo.SaveObservation(ctx, &memory.LongTermMemory{
			Title: fmt.Sprintf("M%d", i), Content: "c", CreatedAt: time.Now(),
		})
		// 前面几个访问多次
		if i < 4 {
			repo.RecordAccess(ctx, id)
		}
	}

	n, err := repo.EvictByLRU(ctx, 4)
	if err != nil {
		t.Fatalf("EvictByLRU: %v", err)
	}
	if n != 4 {
		t.Errorf("evicted = %d, want 4", n)
	}
}

func TestRepository_Vacuum(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	for i := 0; i < 20; i++ {
		repo.SaveObservation(ctx, &memory.LongTermMemory{
			Title: fmt.Sprintf("V%d", i), Content: strings.Repeat("x", 100), CreatedAt: time.Now(),
		})
	}
	// 删除一些产生碎片
	for i := 0; i < 10; i++ {
		repo.DeleteObservation(ctx, int64(i+1))
	}

	if err := repo.Vacuum(ctx); err != nil {
		t.Fatalf("Vacuum: %v", err)
	}
}

func TestRepository_Close(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// 二次 close 应该无副作用
	if err := repo.Close(); err != nil {
		t.Logf("second Close returned: %v (non-fatal)", err)
	}
}
