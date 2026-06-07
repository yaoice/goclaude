package skillinfra

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropics/goclaude/internal/domain/skill"
)

// 构建临时 skill 目录树
func writeSkill(t *testing.T, root, name, content string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillFile := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(skillFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return skillFile
}

func TestLoadFromDir_Basic(t *testing.T) {
	tmp := t.TempDir()
	writeSkill(t, tmp, "pdf", `---
description: 处理 PDF 文件
when_to_use: 当用户要求处理 .pdf 文件时
allowed-tools: [Read, Write]
---
# PDF Skill
This skill helps with PDF processing.
`)

	l := &Loader{HomeDir: tmp}
	skills, err := l.LoadFromDir(context.Background(), tmp, skill.SourceUser)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	s := skills[0]
	if s.Name != "pdf" {
		t.Errorf("name = %s", s.Name)
	}
	if s.Description != "处理 PDF 文件" {
		t.Errorf("description = %q", s.Description)
	}
	if s.WhenToUse != "当用户要求处理 .pdf 文件时" {
		t.Errorf("when_to_use = %q", s.WhenToUse)
	}
	if got := s.AllowedTools; len(got) != 2 || got[0] != "Read" || got[1] != "Write" {
		t.Errorf("allowed-tools = %v", got)
	}
	if !s.UserInvocable {
		t.Error("user-invocable should default to true")
	}
	if s.Source != skill.SourceUser {
		t.Errorf("source = %s", s.Source)
	}
}

func TestLoadFromDir_MissingSkillMd(t *testing.T) {
	tmp := t.TempDir()
	// 创建只有空目录的子目录
	_ = os.MkdirAll(filepath.Join(tmp, "empty"), 0o755)

	l := &Loader{}
	skills, err := l.LoadFromDir(context.Background(), tmp, skill.SourceUser)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(skills))
	}
}

func TestLoadFromDir_UserInvocableFalse(t *testing.T) {
	tmp := t.TempDir()
	writeSkill(t, tmp, "internal", `---
description: 内部 skill
user-invocable: false
---
body
`)
	l := &Loader{}
	skills, _ := l.LoadFromDir(context.Background(), tmp, skill.SourceUser)
	if len(skills) != 1 {
		t.Fatalf("expected 1, got %d", len(skills))
	}
	if skills[0].UserInvocable {
		t.Error("user-invocable should be false")
	}
	if !skills[0].IsHidden {
		t.Error("non-user-invocable should be hidden")
	}
}

func TestLoadFromDir_Paths(t *testing.T) {
	tmp := t.TempDir()
	writeSkill(t, tmp, "tsx-helper", `---
description: 帮助处理 tsx
paths:
  - src/**/*.tsx
  - "*.tsx"
---
body
`)
	l := &Loader{}
	skills, _ := l.LoadFromDir(context.Background(), tmp, skill.SourceUser)
	if len(skills) != 1 {
		t.Fatalf("expected 1, got %d", len(skills))
	}
	if got := skills[0].Paths; len(got) != 2 {
		t.Errorf("paths = %v", got)
	}
}

func TestLoadAll_Dedup(t *testing.T) {
	tmp := t.TempDir()
	user := filepath.Join(tmp, "user")
	project := filepath.Join(tmp, "project")
	_ = os.MkdirAll(user, 0o755)
	_ = os.MkdirAll(project, 0o755)

	writeSkill(t, user, "shared", `---
description: 来自 user
---
content
`)
	writeSkill(t, project, "different", `---
description: 来自 project
---
content
`)

	l := &Loader{HomeDir: tmp}
	skills, err := l.LoadAll(context.Background(), "", user, []string{project})
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 2 {
		t.Errorf("expected 2 unique skills, got %d", len(skills))
	}
}

// TestProjectSkillsDirs 验证从 cwd 向上的 skills 目录（新老目录各一份）。
func TestProjectSkillsDirs(t *testing.T) {
	l := &Loader{HomeDir: "/home/test"}
	got := l.ProjectSkillsDirs("/home/test/proj/subdir")

	// 期望从最近向上，每层新老各一份：
	// /home/test/proj/subdir/.goclaude/skills, .../subdir/.claude/skills,
	// /home/test/proj/.goclaude/skills,       .../proj/.claude/skills,
	// /home/test/.goclaude/skills,            .../test/.claude/skills
	if len(got) < 6 {
		t.Fatalf("expected at least 6 entries, got %d: %v", len(got), got)
	}
	want0 := filepath.Join("/home/test/proj/subdir", ".goclaude", "skills")
	if got[0] != want0 {
		t.Errorf("got[0] = %s want %s", got[0], want0)
	}
	want1 := filepath.Join("/home/test/proj/subdir", ".claude", "skills")
	if got[1] != want1 {
		t.Errorf("got[1] = %s want %s", got[1], want1)
	}
}
