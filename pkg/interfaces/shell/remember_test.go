package shell

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	memory "github.com/anthropics/goclaude/pkg/domain/memory"
)

// ---------- mock MemoryManager ----------

type mockMemoryManager struct {
	mu      sync.Mutex
	entries []memory.EntryItem
	nextID  int
}

func newMockMemoryManager() *mockMemoryManager {
	return &mockMemoryManager{}
}

func (m *mockMemoryManager) AppendEntry(ctx context.Context, title, content, category string) (*memory.EntryItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	id := fmt.Sprintf("mock%012d", m.nextID)
	entry := memory.EntryItem{
		ID:       id,
		Title:    title,
		Content:  content,
		Category: category,
	}
	m.entries = append(m.entries, entry)
	return &entry, nil
}

func (m *mockMemoryManager) DeleteEntry(ctx context.Context, id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, e := range m.entries {
		if strings.HasPrefix(e.ID, id) {
			m.entries = append(m.entries[:i], m.entries[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

func (m *mockMemoryManager) ListEntries(ctx context.Context) ([]memory.EntryItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]memory.EntryItem, len(m.entries))
	copy(result, m.entries)
	return result, nil
}

func (m *mockMemoryManager) SearchEntries(ctx context.Context, keyword string) ([]memory.EntryItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched []memory.EntryItem
	for _, e := range m.entries {
		if strings.Contains(strings.ToLower(e.Title), strings.ToLower(keyword)) ||
			strings.Contains(strings.ToLower(e.Content), strings.ToLower(keyword)) {
			matched = append(matched, e)
		}
	}
	return matched, nil
}

func (m *mockMemoryManager) GetEntrypointContent(ctx context.Context) (string, error) {
	return "", nil
}

// ---------- /remember tests ----------

func TestHandleLocalCommand_Remember_NoArgs(t *testing.T) {
	r := &REPL{useColor: false, Memory: newMockMemoryManager()}

	exit, expanded := r.handleLocalCommand("/remember")
	if exit {
		t.Fatal("/remember should not cause exit")
	}
	if expanded != "" {
		t.Fatal("/remember without args should not return expanded prompt")
	}
}

func TestHandleLocalCommand_Remember_WithArgs(t *testing.T) {
	mgr := newMockMemoryManager()
	r := &REPL{useColor: false, Memory: mgr}

	exit, expanded := r.handleLocalCommand("/remember 项目使用 Go 1 开发")
	if exit {
		t.Fatal("/remember should not cause exit")
	}
	if expanded != "" {
		t.Fatal("/remember should not return expanded prompt (local command)")
	}

	entries, _ := mgr.ListEntries(context.Background())
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Title != "项目使用 Go 1 开发" {
		t.Errorf("title = %q", entries[0].Title)
	}
}

func TestHandleLocalCommand_Remember_NoMemorySvc(t *testing.T) {
	r := &REPL{useColor: false, Memory: nil}

	exit, expanded := r.handleLocalCommand("/remember 测试")
	if exit {
		t.Fatal("/remember should not cause exit")
	}
	if expanded != "" {
		t.Fatal("should not return expanded prompt")
	}
}

func TestHandleLocalCommand_Remember_Exact(t *testing.T) {
	r := &REPL{useColor: false, Memory: newMockMemoryManager()}

	exit, _ := r.handleLocalCommand("/remember 测试")
	if exit {
		t.Fatal("/remember should not exit")
	}

	// /rememberx 不匹配 — 走 default 分支（未知命令）
	exit2, _ := r.handleLocalCommand("/rememberx")
	if exit2 {
		t.Fatal("/rememberx should not exit")
	}
}

// ---------- /memory tests ----------

func TestHandleLocalCommand_Memory_List(t *testing.T) {
	mgr := newMockMemoryManager()
	r := &REPL{useColor: false, Memory: mgr}

	_, _ = mgr.AppendEntry(context.Background(), "条目1", "内容1", "user")
	_, _ = mgr.AppendEntry(context.Background(), "条目2", "内容2", "project")

	exit, _ := r.handleLocalCommand("/memory list")
	if exit {
		t.Fatal("/memory list should not exit")
	}

	entries, _ := mgr.ListEntries(context.Background())
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestHandleLocalCommand_Memory_List_Empty(t *testing.T) {
	r := &REPL{useColor: false, Memory: newMockMemoryManager()}

	exit, _ := r.handleLocalCommand("/memory list")
	if exit {
		t.Fatal("/memory list should not exit")
	}
}

func TestHandleLocalCommand_Memory_Add(t *testing.T) {
	mgr := newMockMemoryManager()
	r := &REPL{useColor: false, Memory: mgr}

	exit, _ := r.handleLocalCommand("/memory add 项目技术栈 | Go 1.22 + PostgreSQL")
	if exit {
		t.Fatal("/memory add should not exit")
	}

	entries, _ := mgr.ListEntries(context.Background())
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Title != "项目技术栈" {
		t.Errorf("title = %q", entries[0].Title)
	}
}

func TestHandleLocalCommand_Memory_Del(t *testing.T) {
	mgr := newMockMemoryManager()
	r := &REPL{useColor: false, Memory: mgr}

	entry, _ := mgr.AppendEntry(context.Background(), "测试条目", "测试内容", "user")

	exit, _ := r.handleLocalCommand("/memory del " + entry.ID[:8])
	if exit {
		t.Fatal("/memory del should not exit")
	}

	entries, _ := mgr.ListEntries(context.Background())
	if len(entries) != 0 {
		t.Fatal("entry should be deleted")
	}
}

func TestHandleLocalCommand_Memory_Del_NotFound(t *testing.T) {
	r := &REPL{useColor: false, Memory: newMockMemoryManager()}

	exit, _ := r.handleLocalCommand("/memory del nonexistent")
	if exit {
		t.Fatal("/memory del should not exit")
	}
}

func TestHandleLocalCommand_Memory_Search(t *testing.T) {
	mgr := newMockMemoryManager()
	r := &REPL{useColor: false, Memory: mgr}

	_, _ = mgr.AppendEntry(context.Background(), "Go语言", "使用Go 1.22开发", "project")
	_, _ = mgr.AppendEntry(context.Background(), "Python脚本", "数据处理用Python", "reference")

	exit, _ := r.handleLocalCommand("/memory search Go")
	if exit {
		t.Fatal("/memory search should not exit")
	}

	results, _ := mgr.SearchEntries(context.Background(), "Go")
	if len(results) != 1 {
		t.Fatalf("expected 1 search result, got %d", len(results))
	}
	if results[0].Title != "Go语言" {
		t.Errorf("expected 'Go语言', got %q", results[0].Title)
	}
}

func TestHandleLocalCommand_Memory_NoSvc(t *testing.T) {
	r := &REPL{useColor: false, Memory: nil}

	exit, _ := r.handleLocalCommand("/memory list")
	if exit {
		t.Fatal("/memory list should not exit")
	}
}

// ---------- extractTitle tests ----------

func TestExtractTitle_WithPeriod(t *testing.T) {
	title := extractTitle("项目使用 Go 1.22 开发。数据库使用 PostgreSQL。")
	if title != "项目使用 Go 1.22 开发" {
		t.Errorf("title = %q", title)
	}
}

func TestExtractTitle_Short(t *testing.T) {
	title := extractTitle("简短标题")
	if title != "简短标题" {
		t.Errorf("title = %q", title)
	}
}

func TestExtractTitle_WithNewline(t *testing.T) {
	title := extractTitle("第一行标题\n第二行内容")
	if title != "第一行标题" {
		t.Errorf("title = %q", title)
	}
}

// ---------- parseAddArgs tests ----------

func TestParseAddArgs_WithPipe(t *testing.T) {
	title, content, category := parseAddArgs("项目技术栈 | Go 1.22 + PostgreSQL")
	if title != "项目技术栈" {
		t.Errorf("title = %q", title)
	}
	if content != "Go 1.22 + PostgreSQL" {
		t.Errorf("content = %q", content)
	}
	if category != "user" {
		t.Errorf("category = %q", category)
	}
}

func TestParseAddArgs_WithCategory(t *testing.T) {
	title, content, category := parseAddArgs("API规范 | 使用/api/v1前缀 | reference")
	if title != "API规范" {
		t.Errorf("title = %q", title)
	}
	if content != "使用/api/v1前缀" {
		t.Errorf("content = %q", content)
	}
	if category != "reference" {
		t.Errorf("category = %q", category)
	}
}

func TestParseAddArgs_NoPipe(t *testing.T) {
	title, content, _ := parseAddArgs("这是一个标题")
	if title != "这是一个标题" {
		t.Errorf("title = %q", title)
	}
	if content != "这是一个标题" {
		t.Errorf("content = %q", content)
	}
}

// ---------- Legacy buildRememberPrompt ----------

func TestBuildRememberPrompt(t *testing.T) {
	prompt := buildRememberPrompt("")

	if strings.TrimSpace(prompt) == "" {
		t.Fatal("buildRememberPrompt returned empty string")
	}

	checks := []string{"Memory Review", "Gather all memory", "Classify each"}
	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestBuildRememberPrompt_WithArgs(t *testing.T) {
	prompt := buildRememberPrompt("项目用 Go 1.22")

	if !strings.Contains(prompt, "Memory Review") {
		t.Error("prompt missing 'Memory Review'")
	}
	if !strings.Contains(prompt, "Additional context from user") {
		t.Error("prompt missing 'Additional context from user'")
	}
}

func TestBuildRememberPrompt_WhitespaceArgs(t *testing.T) {
	prompt := buildRememberPrompt("   ")
	if strings.Contains(prompt, "Additional context from user") {
		t.Error("whitespace-only args should not append Additional context section")
	}
}
