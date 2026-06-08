package memory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestService_DirByScope(t *testing.T) {
	s := &Service{HomeDir: "/home/u", Cwd: "/proj"}
	cases := map[Scope]string{
		ScopeUser:    filepath.Join("/home/u/.goclaude/agent-memory/Reviewer"),
		ScopeProject: filepath.Join("/proj/.goclaude/agent-memory/Reviewer"),
		ScopeLocal:   filepath.Join("/proj/.goclaude/agent-memory-local/Reviewer"),
	}
	for scope, want := range cases {
		got := s.Dir("Reviewer", scope)
		if got != want {
			t.Errorf("scope=%s: got %s want %s", scope, got, want)
		}
	}
}

func TestService_DirsIncludeBothBases(t *testing.T) {
	s := &Service{HomeDir: "/home/u", Cwd: "/proj"}
	got := s.Dirs("Reviewer", ScopeUser)
	want := []string{
		filepath.Join("/home/u/.goclaude/agent-memory/Reviewer"),
		filepath.Join("/home/u/.claude/agent-memory/Reviewer"),
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Dirs got %v want %v", got, want)
	}
}

func TestService_SanitizeColon(t *testing.T) {
	s := &Service{HomeDir: "/h", Cwd: "/c"}
	got := s.Dir("plugin:agent", ScopeProject)
	want := filepath.Join("/c/.goclaude/agent-memory/plugin-agent")
	if got != want {
		t.Errorf("got %s want %s", got, want)
	}
}

func TestService_WriteAndLoad(t *testing.T) {
	tmp := t.TempDir()
	s := NewService(tmp, tmp)

	if err := s.Write("MyAgent", ScopeUser, "decisions.md", "- chose A over B\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.Write("MyAgent", ScopeUser, "context.md", "the project is about X\n"); err != nil {
		t.Fatal(err)
	}

	got, err := s.LoadPrompt("MyAgent", ScopeUser)
	if err != nil {
		t.Fatal(err)
	}
	// 排序后 context.md 应在 decisions.md 之前
	if !contains(got, "## context") || !contains(got, "## decisions") {
		t.Errorf("missing sections: %q", got)
	}
	if firstIndex(got, "## context") > firstIndex(got, "## decisions") {
		t.Error("expected sorted order: context before decisions")
	}
}

// 写入应优先 .goclaude；但若用户已有 .claude 目录则继续写到 .claude（兼容）。
func TestService_WriteDir_CompatRespectsExistingClaude(t *testing.T) {
	tmp := t.TempDir()
	// 预先创建 .claude 子目录，模拟存量用户
	legacy := filepath.Join(tmp, ".claude", "agent-memory", "MyAgent")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewService(tmp, tmp)
	if got := s.WriteDir("MyAgent", ScopeUser); got != legacy {
		t.Errorf("compat: got %q want %q", got, legacy)
	}

	// 全新 agentType（无任何旧目录） → 应写 .goclaude
	want := filepath.Join(tmp, ".goclaude", "agent-memory", "Brand-New")
	if got := s.WriteDir("Brand-New", ScopeUser); got != want {
		t.Errorf("fresh: got %q want %q", got, want)
	}
}

// LoadPrompt 同时合并 .goclaude 与 .claude 的内容；同名文件 .goclaude 优先。
func TestService_LoadPrompt_DualRead(t *testing.T) {
	tmp := t.TempDir()
	gfDir := filepath.Join(tmp, ".goclaude", "agent-memory", "Mix")
	clDir := filepath.Join(tmp, ".claude", "agent-memory", "Mix")
	if err := os.MkdirAll(gfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(clDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gfDir, "a.md"), []byte("from-goclaude"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clDir, "a.md"), []byte("from-claude"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clDir, "b.md"), []byte("only-in-claude"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewService(tmp, tmp)
	got, err := s.LoadPrompt("Mix", ScopeUser)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(got, "from-goclaude") {
		t.Errorf(".goclaude file missing: %q", got)
	}
	if contains(got, "from-claude") {
		t.Errorf(".goclaude should override .claude for same name: %q", got)
	}
	if !contains(got, "only-in-claude") {
		t.Errorf(".claude-only file should still be loaded: %q", got)
	}
}

func TestService_Write_RejectTraversal(t *testing.T) {
	tmp := t.TempDir()
	s := NewService(tmp, tmp)
	cases := []string{"../escape.md", "sub/file.md", "..\\windows.md"}
	for _, c := range cases {
		if err := s.Write("Agent", ScopeUser, c, "x"); err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
}

func TestService_LoadPrompt_MissingDir(t *testing.T) {
	s := NewService("/no/such", "/no/such")
	got, err := s.LoadPrompt("X", ScopeUser)
	if err != nil {
		t.Errorf("missing dir should not error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func contains(s, sub string) bool {
	return firstIndex(s, sub) >= 0
}

func firstIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
