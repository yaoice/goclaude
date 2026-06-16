package shell

import (
	"reflect"
	"testing"
)

// 唯一前缀匹配应直接补全并补一个尾随空格。
func TestPrefixCompleter_UniqueMatch(t *testing.T) {
	p := NewSlashCompleter([]string{"/help", "/exit"}, nil)
	line, pos := p.Complete("/he", 3)
	if line != "/help " || pos != len(line) {
		t.Fatalf("got (%q,%d), want (%q,%d)", line, pos, "/help ", len("/help "))
	}
}

// 不以 `/` 开头的 token 不触发补全。
func TestPrefixCompleter_NonSlashToken(t *testing.T) {
	p := NewSlashCompleter([]string{"/help"}, nil)
	line, pos := p.Complete("he", 2)
	if line != "he" || pos != 2 {
		t.Fatalf("should not complete non-slash token: got (%q,%d)", line, pos)
	}
}

// Dynamic 源贡献的 skill 名应可被补全（构造补全器之后才注入）。
func TestPrefixCompleter_DynamicSkillCompletion(t *testing.T) {
	p := NewSlashCompleter([]string{"/help"}, nil)
	p.Dynamic = func() []string { return []string{"/deploy", "/dep-extra"} }

	line, pos := p.Complete("/deplo", 6)
	if line != "/deploy " || pos != len(line) {
		t.Fatalf("dynamic completion failed: got (%q,%d)", line, pos)
	}
}

// 静态命令与动态命令重复时，候选应去重。
func TestPrefixCompleter_DedupAcrossSources(t *testing.T) {
	var listed []string
	p := NewSlashCompleter([]string{"/skills", "/skill-a"}, func(c []string) { listed = c })
	p.Dynamic = func() []string { return []string{"/skill-a", "/skill-b"} }

	// "/skill" 是公共前缀，无法进一步补全 → 触发候选列表
	line, pos := p.Complete("/skill", 6)
	if line != "/skill" || pos != 6 {
		t.Fatalf("expected no change when listing candidates, got (%q,%d)", line, pos)
	}
	want := []string{"/skill-a", "/skill-b", "/skills"}
	if !reflect.DeepEqual(listed, want) {
		t.Fatalf("candidates = %v, want %v (deduped & sorted)", listed, want)
	}
}

// fakePluginMgr 用于 /plugin 参数补全测试。
type fakePluginMgr struct{ plugins []PluginInfo }

func (f *fakePluginMgr) ListPlugins() []PluginInfo                 { return f.plugins }
func (f *fakePluginMgr) ListMarketplaces() []PluginMarketplaceInfo { return nil }
func (f *fakePluginMgr) SearchPlugins(string) []PluginSearchHit    { return nil }
func (f *fakePluginMgr) EnablePlugin(string) error                 { return nil }
func (f *fakePluginMgr) DisablePlugin(string) error                { return nil }

// `/plugin ` 后按 Tab 应列出子命令；唯一前缀应直接补全。
func TestArgCompleter_PluginSubcommand(t *testing.T) {
	r := &REPL{}
	ac := &ArgCompleter{
		Commands: []string{"/plugin", "/plugins"},
		Args:     r.pluginArgCandidates,
	}
	// "/plugin en" → 唯一匹配 enable
	line, pos := ac.Complete("/plugin en", 10)
	if line != "/plugin enable " || pos != len(line) {
		t.Fatalf("got (%q,%d), want %q", line, pos, "/plugin enable ")
	}
}

// `/plugin enable <prefix>` 应补全插件名。
func TestArgCompleter_PluginName(t *testing.T) {
	r := &REPL{Plugins: &fakePluginMgr{plugins: []PluginInfo{
		{Name: "formatter"}, {Name: "linter"},
	}}}
	ac := &ArgCompleter{
		Commands: []string{"/plugin"},
		Args:     r.pluginArgCandidates,
	}
	line, pos := ac.Complete("/plugin enable form", 19)
	if line != "/plugin enable formatter " || pos != len(line) {
		t.Fatalf("got (%q,%d), want %q", line, pos, "/plugin enable formatter ")
	}
}

// 非 /plugin 命令不应被 ArgCompleter 处理。
func TestArgCompleter_IgnoresOtherCommands(t *testing.T) {
	r := &REPL{}
	ac := &ArgCompleter{Commands: []string{"/plugin"}, Args: r.pluginArgCandidates}
	line, pos := ac.Complete("/help en", 8)
	if line != "/help en" || pos != 8 {
		t.Fatalf("should ignore non-plugin command, got (%q,%d)", line, pos)
	}
}

// dynamicSlashNames 应同时纳入自定义命令与 skill 名/别名。
func TestREPL_DynamicSlashNames(t *testing.T) {
	cc := NewCustomCommands()
	r := &REPL{
		CustomCommands: cc,
		Skills: &fakeSkillMgr{skills: []SkillInfo{
			{Name: "review", Aliases: []string{"rv", "/cr"}},
		}},
	}
	got := r.dynamicSlashNames()
	has := func(s string) bool {
		for _, g := range got {
			if g == s {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"/review", "/rv", "/cr"} {
		if !has(want) {
			t.Fatalf("dynamicSlashNames()=%v missing %q", got, want)
		}
	}
}
