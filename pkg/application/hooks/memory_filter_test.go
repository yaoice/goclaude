package hooks

import (
	"strings"
	"testing"
	"time"

	"github.com/anthropics/goclaude/pkg/domain/memory"
)

// ============================================================
// 1. FilterEntry — 单条过滤
// ============================================================

func TestMemoryFilterService_FilterEntry_ExcludeSecret(t *testing.T) {
	svc := NewMemoryFilterService(DefaultMemoryFilterConfig())

	entry := &memory.MemoryEntry{
		Title:   "DB Secrets",
		Content: "PASSWORD = supersecret123",
		Source:  "agent_note",
	}
	keep, result := svc.FilterEntry(entry)

	if keep {
		t.Fatal("password entry should be excluded by builtin rule")
	}
	if result.MatchedRule != "exclude-secrets" {
		t.Fatalf("expected exclude-secrets, got %q", result.MatchedRule)
	}
}

func TestMemoryFilterService_FilterEntry_KeepNormal(t *testing.T) {
	svc := NewMemoryFilterService(DefaultMemoryFilterConfig())

	entry := &memory.MemoryEntry{
		Title:   "Project Info",
		Content: "Using PostgreSQL 15 with Docker Compose",
		Source:  "agent_note",
	}
	keep, _ := svc.FilterEntry(entry)

	if !keep {
		t.Fatal("normal entry should be kept")
	}
}

func TestMemoryFilterService_FilterEntry_BoostUserDirective(t *testing.T) {
	svc := NewMemoryFilterService(DefaultMemoryFilterConfig())

	entry := &memory.MemoryEntry{
		Title:    "Important",
		Content:  "Always use make test before commit",
		Source:   "user_directive",
		Priority: 50,
	}
	_, result := svc.FilterEntry(entry)

	if result.Priority < 50 {
		t.Fatalf("user directive should be boosted, priority is %d", result.Priority)
	}
}

// ============================================================
// 2. FilterBatch — 批量过滤
// ============================================================

func TestMemoryFilterService_FilterBatch(t *testing.T) {
	svc := NewMemoryFilterService(DefaultMemoryFilterConfig())

	entries := []*memory.MemoryEntry{
		{Title: "Secret", Content: "API_KEY=sk-12345", Source: "auto_extract"},
		{Title: "Normal", Content: "CI uses GitHub Actions", Source: "auto_extract"},
		{Title: "Also Secret", Content: "TOKEN=ghp_abcdef", Source: "agent_note"},
		{Title: "Config", Content: "config yaml path", Source: "agent_note"},
	}

	kept, filtered := svc.FilterBatch(entries)

	if len(kept) != 2 && len(kept) != 3 {
		t.Fatalf("expected 2-3 kept entries, got %d kept, %d filtered", len(kept), len(filtered))
	}
	for _, f := range filtered {
		t.Logf("filtered: %s by rule %s", f.Title, f.MatchedRule)
	}
}

// ============================================================
// 3. ScoreRelevance — 相关性评分
// ============================================================

func TestMemoryFilterService_ScoreRelevance_ExactMatch(t *testing.T) {
	cfg := DefaultMemoryFilterConfig()
	cfg.ContextKeywords = []string{"PostgreSQL", "Docker"}
	svc := NewMemoryFilterService(cfg)

	entry := &memory.MemoryEntry{
		Title:   "DB Config",
		Content: "We use PostgreSQL 15 in Docker containers",
	}
	score := svc.ScoreRelevance(entry)

	if score < 0.3 {
		t.Fatalf("relevance should be >= 0.3 for exact keyword match, got %.2f", score)
	}
}

func TestMemoryFilterService_ScoreRelevance_NoMatch(t *testing.T) {
	cfg := DefaultMemoryFilterConfig()
	cfg.ContextKeywords = []string{"PostgreSQL", "Docker"}
	svc := NewMemoryFilterService(cfg)

	entry := &memory.MemoryEntry{
		Title:   "Frontend",
		Content: "React uses JSX syntax",
	}
	score := svc.ScoreRelevance(entry)

	if score > 0.2 {
		t.Fatalf("relevance should be low for no keyword match, got %.2f", score)
	}
}

func TestMemoryFilterService_ScoreRelevance_EmptyKeywords(t *testing.T) {
	svc := NewMemoryFilterService(DefaultMemoryFilterConfig())

	entry := &memory.MemoryEntry{Title: "X", Content: "X"}
	score := svc.ScoreRelevance(entry)

	if score != 0.5 {
		t.Fatalf("should return neutral 0.5 when no context keywords, got %.2f", score)
	}
}

// ============================================================
// 4. Capacity Management — 容量管理
// ============================================================

func TestMemoryFilterService_CheckCapacity_UnderLimit(t *testing.T) {
	svc := NewMemoryFilterService(DefaultMemoryFilterConfig())

	entries := []*EntryWithMeta{
		{MemoryEntry: &memory.MemoryEntry{ID: "1", Title: "A", Content: "B", ByteSize: 100}},
		{MemoryEntry: &memory.MemoryEntry{ID: "2", Title: "C", Content: "D", ByteSize: 100}},
	}

	status := svc.CheckCapacity(entries)
	if status.NeedsEviction {
		t.Fatal("should not need eviction when under limit")
	}
}

func TestMemoryFilterService_CheckCapacity_OverLimit(t *testing.T) {
	cfg := DefaultMemoryFilterConfig()
	cfg.MaxEntries = 2
	svc := NewMemoryFilterService(cfg)

	entries := make([]*EntryWithMeta, 10)
	for i := range entries {
		entries[i] = &EntryWithMeta{
			MemoryEntry: &memory.MemoryEntry{ID: "id", Title: "T", Content: "C", ByteSize: 100},
		}
	}

	status := svc.CheckCapacity(entries)
	if !status.NeedsEviction {
		t.Fatal("should need eviction when over limit")
	}
}

func TestMemoryFilterService_Evict_Priority(t *testing.T) {
	cfg := DefaultMemoryFilterConfig()
	cfg.MaxEntries = 3
	cfg.EvictionPolicy = "priority"
	cfg.MinPriorityScore = 0
	svc := NewMemoryFilterService(cfg)

	now := time.Now()
	entries := []*EntryWithMeta{
		{MemoryEntry: &memory.MemoryEntry{ID: "h1", Title: "H1", Content: "H", Priority: 90, UpdatedAt: now, ByteSize: 10}},
		{MemoryEntry: &memory.MemoryEntry{ID: "m1", Title: "M1", Content: "M", Priority: 50, UpdatedAt: now, ByteSize: 10}},
		{MemoryEntry: &memory.MemoryEntry{ID: "m2", Title: "M2", Content: "M", Priority: 50, UpdatedAt: now, ByteSize: 10}},
		{MemoryEntry: &memory.MemoryEntry{ID: "l1", Title: "L1", Content: "L", Priority: 5, UpdatedAt: now, ByteSize: 10}},
		{MemoryEntry: &memory.MemoryEntry{ID: "l2", Title: "L2", Content: "L", Priority: 5, UpdatedAt: now, ByteSize: 10}},
	}

	kept, _ := svc.Evict(entries, now)

	if len(kept) != 3 {
		t.Fatalf("should keep 3 entries, got %d", len(kept))
	}
	// 最高优先级的必须在保留列表
	for _, k := range kept {
		if k.ID == "h1" {
			return // found!
		}
	}
	t.Fatal("high priority entry h1 should be kept")
}

func TestMemoryFilterService_Evict_BelowMinPriority(t *testing.T) {
	cfg := DefaultMemoryFilterConfig()
	cfg.MaxEntries = 10
	cfg.MinPriorityScore = 10
	svc := NewMemoryFilterService(cfg)

	now := time.Now()
	entries := []*EntryWithMeta{
		{MemoryEntry: &memory.MemoryEntry{ID: "l1", Title: "L", Content: "L", Priority: 3, UpdatedAt: now, ByteSize: 10}},
		{MemoryEntry: &memory.MemoryEntry{ID: "l2", Title: "L", Content: "L", Priority: 3, UpdatedAt: now, ByteSize: 10}},
	}

	kept, evicted := svc.Evict(entries, now)

	if len(evicted) != 2 {
		t.Fatalf("should evict entries below min priority, kept=%d evicted=%d", len(kept), len(evicted))
	}
}

// ============================================================
// 5. SummarizeEvicted — 摘要压缩
// ============================================================

func TestSummarizeEvicted(t *testing.T) {
	now := time.Now()
	evicted := []*EntryWithMeta{
		{MemoryEntry: &memory.MemoryEntry{Title: "DB Config", Content: "PostgreSQL 15", Tags: []string{"config"}}},
		{MemoryEntry: &memory.MemoryEntry{Title: "CI Setup", Content: "GitHub Actions", Tags: []string{"ci"}}},
	}

	summary := SummarizeEvicted(evicted, now)
	if summary == nil {
		t.Fatal("should produce summary")
	}
	if !strings.Contains(summary.Content, "DB Config") {
		t.Fatalf("summary should contain title, got %q", summary.Content)
	}
	if !strings.Contains(summary.Content, "CI Setup") {
		t.Fatalf("summary should contain all titles, got %q", summary.Content)
	}
}

func TestSummarizeEvicted_Empty(t *testing.T) {
	summary := SummarizeEvicted(nil, time.Now())
	if summary != nil {
		t.Fatal("should return nil for empty evicted list")
	}
}

// ============================================================
// 6. ProcessFull — 全流程集成
// ============================================================

func TestProcessFull_Integration(t *testing.T) {
	cfg := DefaultMemoryFilterConfig()
	cfg.MaxEntries = 4
	cfg.ContextKeywords = []string{"PostgreSQL", "Docker", "make test"}
	cfg.EvictionPolicy = "priority"
	svc := NewMemoryFilterService(cfg)

	entries := []*memory.MemoryEntry{
		{Title: "DB Config", Content: "PostgreSQL 15 Docker", Source: "user_directive", Category: "project"},
		{Title: "CI", Content: "GitHub Actions with make test", Source: "agent_note", Category: "project"},
		{Title: "Secret!!", Content: "PASSWORD=hunter2", Source: "auto_extract"},
		{Title: "Low Priority", Content: "temporary note", Source: "auto_extract", Priority: 5},
		{Title: "Low Priority 2", Content: "another temp", Source: "auto_extract", Priority: 5},
		{Title: "Deploy", Content: "deploy to production", Source: "agent_note", Category: "reference"},
	}

	result := svc.ProcessFull(entries)

	// Secret should be filtered
	if len(result.Filtered) == 0 {
		t.Fatal("should have filtered entries")
	}

	// Should respect max entries
	if len(result.Kept) > cfg.MaxEntries {
		t.Fatalf("should not exceed max entries, kept=%d", len(result.Kept))
	}

	// High priority + relevant items should be in kept
	foundDB := false
	for _, k := range result.Kept {
		if strings.Contains(k.Title, "DB Config") {
			foundDB = true
			break
		}
	}
	if !foundDB {
		t.Fatal("DB Config should be kept (user directive + keyword match)")
	}

	t.Logf("Kept: %d, Filtered: %d, Evicted: %d, HasSummary: %v",
		len(result.Kept), len(result.Filtered), len(result.Evicted), result.Summary != nil)
	t.Logf("Capacity: %+v", result.Capacity)
}
