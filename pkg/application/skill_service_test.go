package application

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropics/goclaude/pkg/domain/skill"
)

func TestSkillService_RenderWith_AllVariables(t *testing.T) {
	svc := NewSkillService(slog.Default())
	svc.RegisterBundled(&skill.Skill{
		Name:      "demo",
		Content:   "session=${CLAUDE_SESSION_ID} home=${CLAUDE_USER_HOME} proj=${CLAUDE_PROJECT_DIR} cwd=${CLAUDE_CWD} args=$ARGS",
		IsEnabled: true,
	})

	got, ok := svc.RenderWith("demo", RenderContext{
		SessionID:  "sess-1",
		UserHome:   "/home/u",
		ProjectDir: "/p",
		Cwd:        "/p/sub",
		Args:       "alpha beta",
	})
	if !ok {
		t.Fatal("not found")
	}
	want := "session=sess-1 home=/home/u proj=/p cwd=/p/sub args=alpha beta"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSkillService_RenderWith_BackwardCompat(t *testing.T) {
	svc := NewSkillService(slog.Default())
	svc.RegisterBundled(&skill.Skill{
		Name:      "x",
		Content:   "id=${CLAUDE_SESSION_ID}",
		IsEnabled: true,
	})
	got, ok := svc.Render("x", "abc") // 旧 API
	if !ok || got != "id=abc" {
		t.Errorf("got %q ok=%v", got, ok)
	}
}

func TestSkillService_RenderWith_SkillRootPrefix(t *testing.T) {
	tmp := t.TempDir()
	svc := NewSkillService(slog.Default())
	svc.RegisterBundled(&skill.Skill{
		Name:      "p",
		Content:   "use ${CLAUDE_SKILL_DIR}/data",
		SkillRoot: tmp,
		IsEnabled: true,
	})
	got, _ := svc.RenderWith("p", RenderContext{})
	if !strings.HasPrefix(got, "Base directory for this skill: ") {
		t.Errorf("missing base prefix: %q", got)
	}
	if !strings.Contains(got, filepath.ToSlash(tmp)+"/data") {
		t.Errorf("CLAUDE_SKILL_DIR 未替换: %q", got)
	}
}

func TestSkillService_ActivateForPaths(t *testing.T) {
	svc := NewSkillService(slog.Default())
	svc.Registry().RegisterConditional(&skill.Skill{
		Name:      "react",
		Paths:     []string{"src/**/*.tsx"},
		Content:   "react helper",
		IsEnabled: true,
	})

	cwd := "/proj"
	// 不匹配
	if got := svc.ActivateForPaths([]string{"/proj/main.go"}, cwd); len(got) != 0 {
		t.Errorf("should not activate for .go file: %v", got)
	}
	// 匹配
	got := svc.ActivateForPaths([]string{"/proj/src/comp/Foo.tsx"}, cwd)
	if len(got) != 1 || got[0] != "react" {
		t.Errorf("expected [react], got %v", got)
	}
	// 已激活后再调用不应重复
	got2 := svc.ActivateForPaths([]string{"/proj/src/comp/Bar.tsx"}, cwd)
	if len(got2) != 0 {
		t.Errorf("re-activation should not return: %v", got2)
	}
}

// 占位防 unused import
var _ = context.Background
