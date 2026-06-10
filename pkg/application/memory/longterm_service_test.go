package memory

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/goclaude/pkg/infrastructure/appconfig"
	"github.com/anthropics/goclaude/pkg/infrastructure/memory/sqlite"
)

// defaultTestConfig returns a test configuration with conservative defaults
func defaultTestConfig() appconfig.LongTermMemoryConfig {
	return appconfig.LongTermMemoryConfig{
		Enabled: true,
		Capture: appconfig.LongTermCaptureConfig{
			AutoCaptureTools:  true,
			MaxObservationSize: 8000,
			MinCaptureChars:    2,
		},
		Injection: appconfig.LongTermInjectionConfig{
			AutoInject:        true,
			MaxInjectTokens:   2000,
			SearchLimit:       10,
			MinRelevanceScore: 0.1,
		},
		Capacity: appconfig.LongTermCapacityConfig{
			MaxEntries:      10,
			MaxStorageBytes: 10 * 1024 * 1024,
		},
		Eviction: appconfig.LongTermEvictionConfig{
			Policy:        "priority",
			AutoSummarize: true,
			MinPriority:   5,
		},
		Expiration: appconfig.LongTermExpirationConfig{
			DefaultTTLDays:      7,
			LowPriorityTTLDays:  3,
			CleanupIntervalHours: 0,
		},
		Privacy: appconfig.LongTermPrivacyConfig{
			AutoExcludePatterns: true,
			StripPrivateTags:    true,
		},
	}
}

func newTestService(t *testing.T) *LongTermMemoryService {
	t.Helper()
	dir := t.TempDir()
	dbPath := dir + "/test.db"
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	t.Cleanup(func() { repo.Close() })

	logger := slog.New(slog.DiscardHandler)
	return NewLongTermMemoryService(repo, defaultTestConfig(), logger)
}

// ============================================================
// SaveObservation
// ============================================================

func TestService_SaveObservation_Basic(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	id, err := svc.SaveObservation(ctx, "sess-1", "Go 项目", "使用 Go 1.22 开发", SaveOptions{
		ObsType: "observation", Category: "project", Source: "auto_extract", Priority: 70,
	})
	if err != nil {
		t.Fatalf("SaveObservation: %v", err)
	}
	if id <= 0 {
		t.Error("expected positive ID")
	}

	items, err := svc.ListRecent(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Title != "Go 项目" {
		t.Errorf("title = %q", items[0].Title)
	}
}

func TestService_SaveObservation_Disabled(t *testing.T) {
	svc := newTestService(t)
	svc.cfg.Enabled = false
	ctx := context.Background()

	id, err := svc.SaveObservation(ctx, "s1", "T", "C", SaveOptions{})
	if err != nil {
		t.Fatalf("SaveObservation disabled: %v", err)
	}
	if id != 0 {
		t.Errorf("expected 0 when disabled, got %d", id)
	}
}

// ============================================================
// 隐私过滤
// ============================================================

func TestService_Privacy_StripPrivateTags(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	id, err := svc.SaveObservation(ctx, "s1", "Sensitive", "public info <private>secret token</private> end", SaveOptions{})
	if err != nil {
		t.Fatalf("SaveObservation: %v", err)
	}
	items, _ := svc.GetObservations(ctx, []int64{id})
	if len(items) != 1 {
		t.Fatal("item missing")
	}
	content := items[0].Content
	if strings.Contains(content, "secret token") {
		t.Errorf("private tag not stripped: %q", content)
	}
	if !strings.Contains(content, "public info") {
		t.Errorf("public info lost: %q", content)
	}
}

func TestService_Privacy_ExcludeSecrets(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// API key pattern should be excluded (匹配 sk-[A-Za-z0-9]{32,})
	id, err := svc.SaveObservation(ctx, "s1", "Keys", "sk-A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8s9t0", SaveOptions{})
	if err != nil {
		t.Fatalf("SaveObservation: %v", err)
	}
	if id != 0 {
		t.Error("API key content should be rejected (id=0)")
	}

	// Password pattern
	id, err = svc.SaveObservation(ctx, "s1", "Pass", "password = supersecret123", SaveOptions{})
	if err != nil {
		t.Fatalf("SaveObservation: %v", err)
	}
	if id != 0 {
		t.Error("password pattern content should be rejected")
	}

	// Safe content should pass
	id, err = svc.SaveObservation(ctx, "s1", "Safe", "Normal project discussion about Go 1.22", SaveOptions{})
	if err != nil {
		t.Fatalf("SaveObservation: %v", err)
	}
	if id <= 0 {
		t.Error("safe content should be accepted")
	}
}

func TestService_Privacy_AllEmpty(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	id, err := svc.SaveObservation(ctx, "s1", "Empty", "<private>everything</private>", SaveOptions{})
	if err != nil {
		t.Fatalf("SaveObservation: %v", err)
	}
	if id != 0 {
		t.Error("all-private content should return 0")
	}
}

// ============================================================
// BuildInjectionContext
// ============================================================

func TestService_BuildInjectionContext(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// 保存几条记忆
	svc.SaveObservation(ctx, "s1", "Go语言项目", "使用 Go 1.22 开发后端", SaveOptions{
		ObsType: "observation", Priority: 80,
	})
	svc.SaveObservation(ctx, "s2", "数据库配置", "PostgreSQL 连接池", SaveOptions{
		ObsType: "observation", Priority: 60,
	})

	inj, err := svc.BuildInjectionContext(ctx, "/project", "Go backend")
	if err != nil {
		t.Fatalf("BuildInjectionContext: %v", err)
	}
	if inj == "" {
		t.Error("expected injection context, got empty")
	}
	if !strings.Contains(inj, "<long-term-memory>") {
		t.Error("missing lt-memory tag")
	}
	if !strings.Contains(inj, "</long-term-memory>") {
		t.Error("missing closing tag")
	}
}

func TestService_BuildInjectionContext_Disabled(t *testing.T) {
	svc := newTestService(t)
	svc.cfg.Injection.AutoInject = false
	ctx := context.Background()

	inj, err := svc.BuildInjectionContext(ctx, "/proj", "query")
	if err != nil {
		t.Fatalf("BuildInjectionContext: %v", err)
	}
	if inj != "" {
		t.Errorf("expected empty when auto_inject=false, got %q", inj)
	}
}

func TestService_BuildInjectionContext_EmptyDB(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	inj, err := svc.BuildInjectionContext(ctx, "/proj", "unknown topic")
	if err != nil {
		t.Fatalf("BuildInjectionContext: %v", err)
	}
	if inj != "" {
		t.Errorf("expected empty with no matching memories, got %q", inj)
	}
}

// ============================================================
// Session Summary
// ============================================================

func TestService_SaveSessionSummary(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	err := svc.SaveSessionSummary(ctx, "sess-x", "/project", "Completed Go refactoring.", SessionStats{
		InputTokens:  500,
		OutputTokens: 300,
		TurnCount:    4,
		StartedAt:    time.Now(),
	})
	if err != nil {
		t.Fatalf("SaveSessionSummary: %v", err)
	}

	// 验证 summary 被保存为一条记忆
	items, _ := svc.ListRecent(ctx, 10)
	found := false
	for _, item := range items {
		if item.Type == "summary" && strings.Contains(item.Content, "Go refactoring") {
			found = true
		}
	}
	if !found {
		t.Error("session summary not found as memory")
	}
}

// ============================================================
// CRUD
// ============================================================

func TestService_DeleteMemory(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	id, _ := svc.SaveObservation(ctx, "s1", "DeleteMe", "content", SaveOptions{})
	if err := svc.DeleteMemory(ctx, id); err != nil {
		t.Fatalf("DeleteMemory: %v", err)
	}
	items, _ := svc.GetObservations(ctx, []int64{id})
	if len(items) != 0 {
		t.Error("item not deleted")
	}
}

func TestService_UpdateMemory(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	id, _ := svc.SaveObservation(ctx, "s1", "Orig", "orig", SaveOptions{})
	err := svc.UpdateMemory(ctx, id, "Updated", "new content", "reference", 90)
	if err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}
	items, _ := svc.GetObservations(ctx, []int64{id})
	if len(items) != 1 {
		t.Fatal("item missing after update")
	}
	if items[0].Title != "Updated" || items[0].Priority != 90 {
		t.Errorf("update not applied: title=%s priority=%d", items[0].Title, items[0].Priority)
	}
}

func TestService_UpdateMemory_NotFound(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	err := svc.UpdateMemory(ctx, 99999, "", "", "", 0)
	if err == nil {
		t.Error("expected error for non-existent memory")
	}
}

// ============================================================
// 容量管理
// ============================================================

func TestService_EnforceCapacity(t *testing.T) {
	svc := newTestService(t)
	svc.cfg.Capacity.MaxEntries = 5
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		svc.SaveObservation(ctx, "s1", "Item", "content", SaveOptions{Priority: i * 10})
	}

	c, _ := svc.repo.Count(ctx)
	if c > 5 {
		t.Errorf("capacity not enforced: count=%d, max=5", c)
	}
}

// ============================================================
// Start / Stop / Close
// ============================================================

func TestService_StartStop(t *testing.T) {
	svc := newTestService(t)
	svc.cfg.Expiration.CleanupIntervalHours = 1
	ctx := context.Background()

	svc.Start(ctx)
	if !svc.started {
		t.Error("service not started")
	}

	svc.Stop()
	if svc.started {
		t.Error("service not stopped")
	}
}

func TestService_Close(t *testing.T) {
	svc := newTestService(t)
	svc.Start(context.Background())

	if err := svc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
