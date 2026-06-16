package shell

import (
	"context"
	"strings"
	"testing"
)

// fakeSkillInvoker 实现 SkillManager + SkillInvoker，用于测试 /<skill> 触发。
type fakeSkillInvoker struct {
	invs map[string]SkillInvocation
}

func (f *fakeSkillInvoker) List() []SkillInfo { return nil }
func (f *fakeSkillInvoker) Render(name string) (string, bool) {
	inv, ok := f.invs[name]
	return inv.Body, ok
}
func (f *fakeSkillInvoker) Invoke(name, args string) (SkillInvocation, bool) {
	inv, ok := f.invs[name]
	if !ok {
		return SkillInvocation{}, false
	}
	if args != "" {
		inv.Body = inv.Body + "\nARGS:" + args
	}
	return inv, true
}

func TestTryInvokeSkill_NonForkInjectsPrompt(t *testing.T) {
	r := &REPL{useColor: false}
	r.Skills = &fakeSkillInvoker{invs: map[string]SkillInvocation{
		"reviewer": {Name: "reviewer", Body: "You are a reviewer", UserInvocable: true},
	}}
	exp, handled := r.tryInvokeSkill("reviewer", "check this")
	if !handled {
		t.Fatal("expected handled=true")
	}
	if !strings.Contains(exp, "You are a reviewer") || !strings.Contains(exp, "ARGS:check this") {
		t.Fatalf("expected rendered body injected, got %q", exp)
	}
}

func TestTryInvokeSkill_NotASkill(t *testing.T) {
	r := &REPL{useColor: false}
	r.Skills = &fakeSkillInvoker{invs: map[string]SkillInvocation{}}
	_, handled := r.tryInvokeSkill("nope", "")
	if handled {
		t.Fatal("unknown skill should not be handled")
	}
}

func TestTryInvokeSkill_NotUserInvocable(t *testing.T) {
	r := &REPL{useColor: false}
	r.Skills = &fakeSkillInvoker{invs: map[string]SkillInvocation{
		"hidden": {Name: "hidden", Body: "x", UserInvocable: false},
	}}
	exp, handled := r.tryInvokeSkill("hidden", "")
	if !handled || exp != "" {
		t.Fatalf("non-invocable skill should be handled with no prompt, got handled=%v exp=%q", handled, exp)
	}
}

func TestTryInvokeSkill_ForkRunsAgent(t *testing.T) {
	r := &REPL{useColor: false}
	var gotAgent, gotPrompt string
	r.RunSkillFork = func(ctx context.Context, agentType, prompt string) (string, error) {
		gotAgent = agentType
		gotPrompt = prompt
		return "fork output", nil
	}
	r.Skills = &fakeSkillInvoker{invs: map[string]SkillInvocation{
		"deep": {Name: "deep", Body: "do deep work", Fork: true, Agent: "Explore", UserInvocable: true},
	}}
	exp, handled := r.tryInvokeSkill("deep", "")
	if !handled || exp != "" {
		t.Fatalf("fork skill should be handled inline, got handled=%v exp=%q", handled, exp)
	}
	if gotAgent != "Explore" || !strings.Contains(gotPrompt, "do deep work") {
		t.Fatalf("fork runner got agent=%q prompt=%q", gotAgent, gotPrompt)
	}
}

func TestTryInvokeSkill_ForkDefaultAgentFallback(t *testing.T) {
	r := &REPL{useColor: false}
	var gotAgent string
	r.RunSkillFork = func(ctx context.Context, agentType, prompt string) (string, error) {
		gotAgent = agentType
		return "ok", nil
	}
	r.Skills = &fakeSkillInvoker{invs: map[string]SkillInvocation{
		"f": {Name: "f", Body: "b", Fork: true, Agent: "", UserInvocable: true},
	}}
	r.tryInvokeSkill("f", "")
	if gotAgent != "general-purpose" {
		t.Fatalf("expected default agent general-purpose, got %q", gotAgent)
	}
}

func TestTryInvokeSkill_ForkNoRunnerFallsBackToPrompt(t *testing.T) {
	r := &REPL{useColor: false}
	r.RunSkillFork = nil
	r.Skills = &fakeSkillInvoker{invs: map[string]SkillInvocation{
		"f": {Name: "f", Body: "body-content", Fork: true, UserInvocable: true},
	}}
	exp, handled := r.tryInvokeSkill("f", "")
	if !handled || !strings.Contains(exp, "body-content") {
		t.Fatalf("no-runner fork should fall back to prompt injection, got handled=%v exp=%q", handled, exp)
	}
}

// fakePluginManager 实现 PluginManager。
type fakePluginManager struct {
	plugins []PluginInfo
	mkts    []PluginMarketplaceInfo
	hits    []PluginSearchHit
	toggled map[string]bool
}

func (f *fakePluginManager) ListPlugins() []PluginInfo                 { return f.plugins }
func (f *fakePluginManager) ListMarketplaces() []PluginMarketplaceInfo { return f.mkts }
func (f *fakePluginManager) SearchPlugins(string) []PluginSearchHit    { return f.hits }
func (f *fakePluginManager) EnablePlugin(name string) error {
	if f.toggled == nil {
		f.toggled = map[string]bool{}
	}
	f.toggled[name] = true
	return nil
}
func (f *fakePluginManager) DisablePlugin(name string) error {
	if f.toggled == nil {
		f.toggled = map[string]bool{}
	}
	f.toggled[name] = false
	return nil
}

func TestRenderPluginCmd_Overview(t *testing.T) {
	r := &REPL{useColor: false}
	r.Plugins = &fakePluginManager{
		plugins: []PluginInfo{{Name: "demo", Version: "0.1.0", Description: "d", Enabled: true}},
		mkts:    []PluginMarketplaceInfo{{Name: "mkt", Type: "local", Source: "/x", PluginCount: 2}},
	}
	out := r.renderPluginCmd(nil)
	if !strings.Contains(out, "demo") || !strings.Contains(out, "enabled") {
		t.Fatalf("overview missing plugin: %q", out)
	}
	if !strings.Contains(out, "mkt") || !strings.Contains(out, "local") {
		t.Fatalf("overview missing marketplace: %q", out)
	}
}

func TestRenderPluginCmd_EnableDisable(t *testing.T) {
	r := &REPL{useColor: false}
	fm := &fakePluginManager{}
	r.Plugins = fm
	if out := r.renderPluginCmd([]string{"enable", "demo"}); !strings.Contains(out, "已启用") {
		t.Fatalf("enable output: %q", out)
	}
	if !fm.toggled["demo"] {
		t.Fatal("enable not propagated")
	}
	if out := r.renderPluginCmd([]string{"disable", "demo"}); !strings.Contains(out, "已禁用") {
		t.Fatalf("disable output: %q", out)
	}
	if fm.toggled["demo"] {
		t.Fatal("disable not propagated")
	}
}

func TestRenderPluginCmd_NoService(t *testing.T) {
	r := &REPL{useColor: false}
	if out := r.renderPluginCmd(nil); !strings.Contains(out, "插件服务未启用") {
		t.Fatalf("expected disabled notice, got %q", out)
	}
}
