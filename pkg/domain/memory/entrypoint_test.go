package memory

import (
	"context"
	"strings"
	"testing"
	"time"
)

// mockRepo 用于测试的内存仓库
type mockRepo struct {
	files map[string]string
}

func newMockRepo() *mockRepo {
	return &mockRepo{files: make(map[string]string)}
}

func (r *mockRepo) Load(ctx context.Context, path string) (*Memory, error)       { return nil, nil }
func (r *mockRepo) Save(ctx context.Context, m *Memory) error                     { return nil }
func (r *mockRepo) Exists(ctx context.Context, path string) bool                  { return false }
func (r *mockRepo) ReadDir(ctx context.Context, path string, recursive bool) ([]DirEntry, error) {
	return nil, nil
}
func (r *mockRepo) MkdirAll(ctx context.Context, path string) error { return nil }
func (r *mockRepo) Stat(ctx context.Context, path string) (FileInfo, error) {
	return FileInfo{}, nil
}
func (r *mockRepo) RealPath(ctx context.Context, path string) (string, error) { return path, nil }
func (r *mockRepo) ReadFile(ctx context.Context, path string) (string, error) {
	return r.files[path], nil
}
func (r *mockRepo) WriteFile(ctx context.Context, path string, content string) error {
	r.files[path] = content
	return nil
}

func TestParseEntries_Empty(t *testing.T) {
	mgr := NewEntrypointManager(newMockRepo(), "/tmp/mem")
	entries := mgr.ParseEntries("")
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestParseEntries_Single(t *testing.T) {
	mgr := NewEntrypointManager(newMockRepo(), "/tmp/mem")
	raw := "# Auto Memory\n\n<!-- MEMORY_ENTRY id=abcd1234abcd1234 category=project created=2025-06-08T19:30:00Z -->\n## 项目技术栈\nGo 1.22 + PostgreSQL"

	entries := mgr.ParseEntries(raw)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	e := entries[0]
	if e.ID != "abcd1234abcd1234" {
		t.Errorf("id = %q", e.ID)
	}
	if e.Title != "项目技术栈" {
		t.Errorf("title = %q", e.Title)
	}
	if e.Content != "Go 1.22 + PostgreSQL" {
		t.Errorf("content = %q", e.Content)
	}
	if e.Category != "project" {
		t.Errorf("category = %q", e.Category)
	}
}

func TestParseEntries_Multiple(t *testing.T) {
	mgr := NewEntrypointManager(newMockRepo(), "/tmp/mem")
	raw := `# Auto Memory

<!-- MEMORY_ENTRY id=1111111111111111 category=project created=2025-06-08T19:30:00Z -->
## 技术栈
Go 1.22

<!-- MEMORY_ENTRY id=2222222222222222 category=reference created=2025-06-08T19:31:00Z -->
## API前缀
/api/v1`

	entries := mgr.ParseEntries(raw)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if entries[0].ID != "1111111111111111" {
		t.Errorf("first id = %q", entries[0].ID)
	}
	if entries[1].ID != "2222222222222222" {
		t.Errorf("second id = %q", entries[1].ID)
	}
}

func TestParseEntries_NoEntries(t *testing.T) {
	mgr := NewEntrypointManager(newMockRepo(), "/tmp/mem")
	raw := "# Auto Memory\n\n(empty)"

	entries := mgr.ParseEntries(raw)
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestAppendEntry(t *testing.T) {
	repo := newMockRepo()
	mgr := NewEntrypointManager(repo, "/tmp/mem")

	entry, err := mgr.AppendEntry(context.Background(), "测试标题", "测试内容", "user")
	if err != nil {
		t.Fatal(err)
	}

	if entry.ID == "" {
		t.Fatal("entry ID should not be empty")
	}
	if entry.Title != "测试标题" {
		t.Errorf("title = %q", entry.Title)
	}
	if entry.Category != "user" {
		t.Errorf("category = %q", entry.Category)
	}

	// 验证写入内容
	content := repo.files["/tmp/mem/MEMORY.md"]
	if !strings.Contains(content, "测试标题") {
		t.Error("content missing title")
	}
	if !strings.Contains(content, "测试内容") {
		t.Error("content missing body")
	}
	if !strings.Contains(content, entry.ID) {
		t.Error("content missing entry ID")
	}
}

func TestAppendEntry_DefaultCategory(t *testing.T) {
	repo := newMockRepo()
	mgr := NewEntrypointManager(repo, "/tmp/mem")

	entry, err := mgr.AppendEntry(context.Background(), "标题", "内容", "")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Category != "user" {
		t.Errorf("default category should be 'user', got %q", entry.Category)
	}
}

func TestAppendEntry_EmptyTitle(t *testing.T) {
	repo := newMockRepo()
	mgr := NewEntrypointManager(repo, "/tmp/mem")

	_, err := mgr.AppendEntry(context.Background(), "", "content", "user")
	if err == nil {
		t.Fatal("expected error for empty title")
	}
}

func TestDeleteEntry(t *testing.T) {
	repo := newMockRepo()
	mgr := NewEntrypointManager(repo, "/tmp/mem")

	// 先追加一条
	entry, err := mgr.AppendEntry(context.Background(), "要删除的", "内容", "user")
	if err != nil {
		t.Fatal(err)
	}

	// 删除它
	deleted, err := mgr.DeleteEntry(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("expected deleted=true")
	}

	// 验证已删除
	entries, err := mgr.ListEntries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.ID == entry.ID {
			t.Error("entry still exists after delete")
		}
	}
}

func TestDeleteEntry_NotFound(t *testing.T) {
	repo := newMockRepo()
	mgr := NewEntrypointManager(repo, "/tmp/mem")

	deleted, err := mgr.DeleteEntry(context.Background(), "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if deleted {
		t.Fatal("expected deleted=false for non-existent ID")
	}
}

func TestSearchEntries(t *testing.T) {
	repo := newMockRepo()
	mgr := NewEntrypointManager(repo, "/tmp/mem")

	_, _ = mgr.AppendEntry(context.Background(), "Go语言", "使用Go 1.22开发", "project")
	_, _ = mgr.AppendEntry(context.Background(), "数据库", "使用PostgreSQL", "reference")
	_, _ = mgr.AppendEntry(context.Background(), "部署", "使用Docker部署", "project")

	// 搜索 "Go"
	results, err := mgr.SearchEntries(context.Background(), "Go")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for 'Go', got %d", len(results))
	}
	if results[0].Title != "Go语言" {
		t.Errorf("wrong entry: %q", results[0].Title)
	}

	// 搜索 "PostgreSQL"（匹配内容）
	results, err = mgr.SearchEntries(context.Background(), "PostgreSQL")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for 'PostgreSQL', got %d", len(results))
	}

	// 搜索不存在的
	results, err = mgr.SearchEntries(context.Background(), "Python")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for 'Python', got %d", len(results))
	}
}

func TestBuildContextSection(t *testing.T) {
	repo := newMockRepo()
	mgr := NewEntrypointManager(repo, "/tmp/mem")

	// 空文件 → 空上下文
	section, err := mgr.BuildContextSection(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if section != "" {
		t.Errorf("expected empty section, got %q", section)
	}

	// 写入内容
	_, _ = mgr.AppendEntry(context.Background(), "测试", "内容", "user")

	section, err = mgr.BuildContextSection(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(section, "auto-memory") {
		t.Error("section should contain auto-memory tag")
	}
	if !strings.Contains(section, "测试") {
		t.Error("section should contain entry content")
	}
}

func TestSortEntriesByTime(t *testing.T) {
	now := time.Now()
	entries := []EntryItem{
		{ID: "1", CreatedAt: now.Add(-1 * time.Hour)},
		{ID: "2", CreatedAt: now},
		{ID: "3", CreatedAt: now.Add(-2 * time.Hour)},
	}

	SortEntriesByTime(entries)

	if entries[0].ID != "2" {
		t.Error("most recent should be first")
	}
	if entries[2].ID != "3" {
		t.Error("oldest should be last")
	}
}

func TestFilterEntriesByCategory(t *testing.T) {
	entries := []EntryItem{
		{ID: "1", Category: "project"},
		{ID: "2", Category: "user"},
		{ID: "3", Category: "project"},
	}

	filtered := FilterEntriesByCategory(entries, "project")
	if len(filtered) != 2 {
		t.Fatalf("expected 2 project entries, got %d", len(filtered))
	}

	filtered = FilterEntriesByCategory(entries, "reference")
	if len(filtered) != 0 {
		t.Fatalf("expected 0 reference entries, got %d", len(filtered))
	}
}
