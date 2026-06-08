package agent

import "testing"

func TestRegistry_PriorityMerge(t *testing.T) {
	r := NewRegistry()
	// built-in 先注册
	r.Register(&Definition{
		AgentType: "Explore",
		Source:    SourceBuiltIn,
		WhenToUse: "built-in",
	})
	// 用户级覆盖
	r.Register(&Definition{
		AgentType: "Explore",
		Source:    SourceUser,
		WhenToUse: "user",
	})
	got, ok := r.Get("Explore")
	if !ok {
		t.Fatal("not found")
	}
	if got.WhenToUse != "user" {
		t.Errorf("expected user override, got %q", got.WhenToUse)
	}

	// 项目级覆盖
	r.Register(&Definition{
		AgentType: "Explore",
		Source:    SourceProject,
		WhenToUse: "project",
	})
	got, _ = r.Get("Explore")
	if got.WhenToUse != "project" {
		t.Errorf("expected project override, got %q", got.WhenToUse)
	}

	// 反向：built-in 不能覆盖 project
	r.Register(&Definition{
		AgentType: "Explore",
		Source:    SourceBuiltIn,
		WhenToUse: "built-in-2",
	})
	got, _ = r.Get("Explore")
	if got.WhenToUse != "project" {
		t.Errorf("project should still win, got %q", got.WhenToUse)
	}
}

func TestRegistry_All(t *testing.T) {
	r := NewRegistry()
	r.Register(&Definition{AgentType: "a", Source: SourceBuiltIn})
	r.Register(&Definition{AgentType: "b", Source: SourceUser})
	if got := len(r.All()); got != 2 {
		t.Errorf("expected 2, got %d", got)
	}
}

func TestDefinition_ResolvedSystemPrompt(t *testing.T) {
	d := &Definition{
		SystemPrompt: "static",
		GetSystemPrompt: func() string {
			return "dynamic"
		},
	}
	if got := d.ResolvedSystemPrompt(); got != "dynamic" {
		t.Errorf("expected dynamic, got %q", got)
	}

	d2 := &Definition{SystemPrompt: "static"}
	if got := d2.ResolvedSystemPrompt(); got != "static" {
		t.Errorf("expected static, got %q", got)
	}
}
