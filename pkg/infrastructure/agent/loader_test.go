package agentinfra

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropics/goclaude/pkg/domain/agent"
)

func TestBuiltInAgents(t *testing.T) {
	agents := BuiltInAgents()
	if len(agents) < 2 {
		t.Fatalf("expected at least 2 built-in agents, got %d", len(agents))
	}
	names := map[string]bool{}
	for _, a := range agents {
		if a.AgentType == "" {
			t.Errorf("agent has empty type: %+v", a)
		}
		if a.SystemPrompt == "" {
			t.Errorf("agent %s has empty system prompt", a.AgentType)
		}
		if a.Source != agent.SourceBuiltIn {
			t.Errorf("agent %s should be built-in, got %s", a.AgentType, a.Source)
		}
		names[a.AgentType] = true
	}
	if !names["Explore"] {
		t.Error("Explore agent missing")
	}
	if !names["Plan"] {
		t.Error("Plan agent missing")
	}
	if !names["general-purpose"] {
		t.Error("general-purpose agent missing")
	}
}

func TestLoadFromDir(t *testing.T) {
	tmp := t.TempDir()
	// 合法 agent
	must(t, os.WriteFile(filepath.Join(tmp, "reviewer.md"), []byte(`---
name: code-reviewer
description: Reviews code changes for correctness and style
tools: [Read, Grep]
model: inherit
---
You are a code review specialist. Analyze diffs carefully.
`), 0o644))
	// 没有 name 的会被跳过
	must(t, os.WriteFile(filepath.Join(tmp, "bad.md"), []byte(`---
description: missing name
---
body
`), 0o644))
	// 非 .md 文件
	must(t, os.WriteFile(filepath.Join(tmp, "readme.txt"), []byte("ignored"), 0o644))

	l := &Loader{}
	defs, err := l.LoadFromDir(context.Background(), tmp, agent.SourceUser)
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(defs))
	}
	d := defs[0]
	if d.AgentType != "code-reviewer" {
		t.Errorf("got %q", d.AgentType)
	}
	if len(d.Tools) != 2 {
		t.Errorf("tools = %v", d.Tools)
	}
	if d.Model != "inherit" {
		t.Errorf("model = %q", d.Model)
	}
	if d.Source != agent.SourceUser {
		t.Errorf("source = %s", d.Source)
	}
}

func TestLoadFromDir_MissingDir(t *testing.T) {
	l := &Loader{}
	defs, err := l.LoadFromDir(context.Background(), "/nonexistent/path/xyz", agent.SourceUser)
	if err != nil {
		t.Fatalf("missing dir should not error, got %v", err)
	}
	if defs != nil {
		t.Errorf("expected nil, got %v", defs)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
