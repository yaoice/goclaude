package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistorySearchPrev(t *testing.T) {
	h := NewHistory("", 0)
	h.Append("git status")
	h.Append("git commit -m 'fix bug'")
	h.Append("ls -la")
	h.Append("git log --oneline")

	// 搜索 "git"：从尾部往前 → 应命中 "git log --oneline"
	hit, idx, ok := h.SearchPrev("git", h.Len())
	if !ok || hit != "git log --oneline" || idx != 3 {
		t.Fatalf("first hit: %q idx=%d ok=%v", hit, idx, ok)
	}
	// 再往上一条
	hit, idx, ok = h.SearchPrev("git", idx)
	if !ok || hit != "git commit -m 'fix bug'" || idx != 1 {
		t.Fatalf("second hit: %q idx=%d ok=%v", hit, idx, ok)
	}
	// 再往上一条
	hit, idx, ok = h.SearchPrev("git", idx)
	if !ok || hit != "git status" || idx != 0 {
		t.Fatalf("third hit: %q idx=%d ok=%v", hit, idx, ok)
	}
	// 没有更早的
	_, _, ok = h.SearchPrev("git", idx)
	if ok {
		t.Fatalf("expected no more matches")
	}
	// 大小写不敏感
	hit, _, ok = h.SearchPrev("LOG", h.Len())
	if !ok || hit != "git log --oneline" {
		t.Fatalf("case-insensitive failed: %q ok=%v", hit, ok)
	}
	// 空 query → ok=false
	if _, _, ok := h.SearchPrev("", h.Len()); ok {
		t.Fatalf("empty query should not match")
	}
}

// ---- 自定义命令 ----

func TestCustomCommands_LoadAndRender(t *testing.T) {
	dir := t.TempDir()
	cmdDir := filepath.Join(dir, ".goclaude", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cmdDir, "git"), 0o755); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		filepath.Join(cmdDir, "summary.md"): `---
description: Summarize the codebase
argument-hint: "[topic]"
arguments: topic
---
请总结一下 $topic 相关的内容。原始：$ARGUMENTS`,
		filepath.Join(cmdDir, "git", "commit.md"): `---
description: Generate commit message
---
请基于改动生成 commit message：$ARGUMENTS`,
		filepath.Join(cmdDir, "noop.md"): `Just a body without frontmatter, args=$1`,
	}
	for p, c := range files {
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cc := NewCustomCommands()
	cc.LoadDefaults(dir)

	names := cc.Names()
	if len(names) != 3 {
		t.Fatalf("loaded %d cmds, want 3: %v", len(names), names)
	}

	// summary：命名参数 + ARGUMENTS
	s, ok := cc.Get("summary")
	if !ok {
		t.Fatal("summary not found")
	}
	out := s.Render("DDD")
	if !strings.Contains(out, "请总结一下 DDD 相关的内容") {
		t.Fatalf("named arg substitution failed: %q", out)
	}
	if !strings.Contains(out, "原始：DDD") {
		t.Fatalf("ARGUMENTS substitution failed: %q", out)
	}

	// git:commit
	g, ok := cc.Get("git:commit")
	if !ok {
		t.Fatal("git:commit not found")
	}
	if g.Description != "Generate commit message" {
		t.Fatalf("description=%q", g.Description)
	}
	out = g.Render("fix: typo")
	if !strings.Contains(out, "请基于改动生成 commit message：fix: typo") {
		t.Fatalf("git:commit render: %q", out)
	}

	// noop：无 frontmatter；$1 = 第二个位置参数；无 ARGUMENTS placeholder 但有 $1 → 不应追加
	n, ok := cc.Get("noop")
	if !ok {
		t.Fatal("noop not found")
	}
	out = n.Render("hello world")
	if !strings.Contains(out, "args=world") {
		t.Fatalf("$1 substitution failed (expect args=world for index 1): %q", out)
	}
	if strings.Contains(out, "ARGUMENTS:") {
		t.Fatalf("should not append ARGUMENTS when $1 was substituted: %q", out)
	}
}

func TestSubstituteArguments_AppendWhenNoPlaceholder(t *testing.T) {
	got := substituteArguments("just text", "foo bar", true, nil)
	if !strings.Contains(got, "ARGUMENTS: foo bar") {
		t.Fatalf("expected appended ARGUMENTS, got %q", got)
	}
	// 不 append 模式
	got = substituteArguments("just text", "foo bar", false, nil)
	if strings.Contains(got, "ARGUMENTS:") {
		t.Fatalf("should not append: %q", got)
	}
	// 已含占位符 → 不 append
	got = substituteArguments("$ARGUMENTS here", "x", true, nil)
	if strings.Contains(got, "ARGUMENTS:") {
		t.Fatalf("should not append (placeholder present): %q", got)
	}
}

func TestParseArguments_Quotes(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"foo bar baz", []string{"foo", "bar", "baz"}},
		{`foo "hello world" baz`, []string{"foo", "hello world", "baz"}},
		{"foo  'q  s' baz", []string{"foo", "q  s", "baz"}},
		{"", nil},
	}
	for _, c := range cases {
		got := parseArguments(c.in)
		if !equalStrings(got, c.want) {
			t.Errorf("parseArguments(%q)=%v want %v", c.in, got, c.want)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
