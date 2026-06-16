package application

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePluginFixture 在 dir 下构造一个最小可用插件，返回插件根目录。
func writePluginFixture(t *testing.T, dir, name string, withContribs bool) string {
	t.Helper()
	root := filepath.Join(dir, name)
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".claude-plugin/plugin.json", `{"name":"`+name+`","version":"0.1.0","description":"demo plugin"}`)
	if withContribs {
		write("commands/hello.md", "---\ndescription: say hello\n---\nHello from plugin")
		write("skills/greeter/SKILL.md", "---\nname: greeter\ndescription: greet\n---\nGreet the user")
		write("agents/helper.md", "---\nname: helper\ndescription: helps\n---\nYou help.")
		write("hooks/hooks.json", `{"hooks":{}}`)
		write(".mcp.json", `{"mcpServers":{}}`)
	}
	return root
}

func TestPluginService_InstallEnableDisableUninstall(t *testing.T) {
	base := filepath.Join(t.TempDir(), "plugins")
	srcDir := t.TempDir()
	root := writePluginFixture(t, srcDir, "demo", true)

	svc := NewPluginService(base, nil)
	ctx := context.Background()

	p, err := svc.Install(ctx, root)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if p.Name != "demo" || !p.Enabled {
		t.Fatalf("installed plugin = %+v", p)
	}

	// 安装后应在持久化目录之内
	if _, err := os.Stat(filepath.Join(p.InstallPath, ".claude-plugin", "plugin.json")); err != nil {
		t.Fatalf("plugin not copied: %v", err)
	}

	// 贡献解析
	contribs := svc.Contributions()
	if len(contribs) != 1 {
		t.Fatalf("expected 1 contribution set, got %d", len(contribs))
	}
	c := contribs[0]
	if len(c.CommandDirs) != 1 || len(c.SkillDirs) != 1 || len(c.AgentDirs) != 1 {
		t.Errorf("dirs: %+v", c)
	}
	if len(c.HookFiles) != 1 || len(c.MCPConfigFiles) != 1 {
		t.Errorf("files: %+v", c)
	}

	// 禁用后无贡献
	if err := svc.Disable("demo"); err != nil {
		t.Fatal(err)
	}
	if len(svc.Contributions()) != 0 {
		t.Fatal("disabled plugin should contribute nothing")
	}
	// 重新启用
	if err := svc.Enable("demo"); err != nil {
		t.Fatal(err)
	}
	if len(svc.Contributions()) != 1 {
		t.Fatal("re-enabled plugin should contribute")
	}

	// 持久化：新建 service 从磁盘重建
	svc2 := NewPluginService(base, nil)
	if err := svc2.Load(ctx); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := svc2.Registry().GetPlugin("demo"); !ok {
		t.Fatal("plugin lost after reload")
	}
	if len(svc2.Contributions()) != 1 {
		t.Fatal("contributions lost after reload")
	}

	// 卸载
	if err := svc.Uninstall(ctx, "demo"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, ok := svc.Registry().GetPlugin("demo"); ok {
		t.Fatal("plugin still registered after uninstall")
	}
	if _, err := os.Stat(p.InstallPath); !os.IsNotExist(err) {
		t.Fatal("plugin dir not removed")
	}
}

func TestPluginService_SearchPlugins(t *testing.T) {
	base := filepath.Join(t.TempDir(), "plugins")
	mktRoot := t.TempDir()

	if err := os.MkdirAll(filepath.Join(mktRoot, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"localmkt","plugins":[` +
		`{"name":"formatter","source":"./formatter","description":"格式化代码"},` +
		`{"name":"linter","source":"./linter","description":"静态检查工具"}` +
		`]}`
	if err := os.WriteFile(filepath.Join(mktRoot, ".claude-plugin", "marketplace.json"),
		[]byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writePluginFixture(t, mktRoot, "formatter", false)
	writePluginFixture(t, mktRoot, "linter", false)

	svc := NewPluginService(base, nil)
	ctx := context.Background()
	if _, err := svc.AddMarketplace(ctx, mktRoot); err != nil {
		t.Fatalf("add marketplace: %v", err)
	}

	// 空查询返回全部条目（按名排序）
	all := svc.SearchPlugins("")
	if len(all) != 2 || all[0].Name != "formatter" || all[1].Name != "linter" {
		t.Fatalf("search all = %+v", all)
	}
	if all[0].Marketplace != "localmkt" || all[0].Installed {
		t.Errorf("hit meta = %+v", all[0])
	}

	// 按名称匹配
	if hits := svc.SearchPlugins("lint"); len(hits) != 1 || hits[0].Name != "linter" {
		t.Errorf("search by name = %+v", hits)
	}
	// 按描述匹配（中文子串）
	if hits := svc.SearchPlugins("检查"); len(hits) != 1 || hits[0].Name != "linter" {
		t.Errorf("search by description = %+v", hits)
	}
	// 无匹配
	if hits := svc.SearchPlugins("nope"); len(hits) != 0 {
		t.Errorf("search no match = %+v", hits)
	}

	// 安装后再检索应标记 Installed
	if _, err := svc.Install(ctx, "formatter@localmkt"); err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, h := range svc.SearchPlugins("formatter") {
		if h.Name == "formatter" && !h.Installed {
			t.Errorf("formatter should be marked installed: %+v", h)
		}
	}
}

func TestPluginService_MarketplaceInstall(t *testing.T) {
	base := filepath.Join(t.TempDir(), "plugins")
	mktRoot := t.TempDir()

	// 市场根：marketplace.json + 内嵌插件 demo/
	if err := os.MkdirAll(filepath.Join(mktRoot, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mktRoot, ".claude-plugin", "marketplace.json"),
		[]byte(`{"name":"localmkt","plugins":[{"name":"demo","source":"./demo","description":"d"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writePluginFixture(t, mktRoot, "demo", false)

	svc := NewPluginService(base, nil)
	ctx := context.Background()

	m, err := svc.AddMarketplace(ctx, mktRoot)
	if err != nil {
		t.Fatalf("add marketplace: %v", err)
	}
	if m.Name != "localmkt" || len(m.Entries) != 1 {
		t.Fatalf("marketplace = %+v", m)
	}

	p, err := svc.Install(ctx, "demo@localmkt")
	if err != nil {
		t.Fatalf("install from marketplace: %v", err)
	}
	if p.Name != "demo" || p.Marketplace != "localmkt" {
		t.Fatalf("installed = %+v", p)
	}

	if err := svc.RemoveMarketplace("localmkt"); err != nil {
		t.Fatalf("remove marketplace: %v", err)
	}
	if len(svc.ListMarketplaces()) != 0 {
		t.Fatal("marketplace not removed")
	}
}

// 裸插件名（不带 @market）应跨已添加市场按名解析并安装。
func TestPluginService_InstallByBareName(t *testing.T) {
	base := filepath.Join(t.TempDir(), "plugins")
	mktRoot := t.TempDir()

	if err := os.MkdirAll(filepath.Join(mktRoot, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mktRoot, ".claude-plugin", "marketplace.json"),
		[]byte(`{"name":"localmkt","plugins":[{"name":"demo","source":"./demo","description":"d"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writePluginFixture(t, mktRoot, "demo", false)

	svc := NewPluginService(base, nil)
	ctx := context.Background()
	if _, err := svc.AddMarketplace(ctx, mktRoot); err != nil {
		t.Fatalf("add marketplace: %v", err)
	}

	// 仅给裸名，不带市场后缀
	p, err := svc.Install(ctx, "demo")
	if err != nil {
		t.Fatalf("install by bare name: %v", err)
	}
	if p.Name != "demo" || p.Marketplace != "localmkt" {
		t.Fatalf("installed = %+v", p)
	}
}

// 裸名在任何市场都找不到时，应给出引导性错误而非"本地来源不存在"。
func TestPluginService_InstallByBareName_NotFound(t *testing.T) {
	svc := NewPluginService(filepath.Join(t.TempDir(), "plugins"), nil)
	_, err := svc.Install(context.Background(), "nope-plugin")
	if err == nil {
		t.Fatal("expected error for unknown bare name")
	}
	if !strings.Contains(err.Error(), "未找到插件") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIsBarePluginName(t *testing.T) {
	cases := []struct {
		ref  string
		want bool
	}{
		{"minimax-skills", true},
		{"demo", true},
		{"./local-dir", false},
		{"/abs/path", false},
		{"~/x", false},
		{"https://x/y.git", false},
		{"git@github.com:o/r.git", false},
	}
	for _, c := range cases {
		if got := isBarePluginName(c.ref); got != c.want {
			t.Errorf("isBarePluginName(%q) = %v, want %v", c.ref, got, c.want)
		}
	}
}

func TestParsePluginRef(t *testing.T) {
	cases := []struct {
		ref  string
		name string
		mkt  string
		ok   bool
	}{
		{"demo@localmkt", "demo", "localmkt", true},
		{"git@github.com:o/r.git", "", "", false},
		{"/abs/path", "", "", false},
		{"https://x/y.git", "", "", false},
		{"name@", "", "", false},
		{"@mkt", "", "", false},
	}
	for _, c := range cases {
		name, mkt, ok := parsePluginRef(c.ref)
		if ok != c.ok || name != c.name || mkt != c.mkt {
			t.Errorf("parsePluginRef(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.ref, name, mkt, ok, c.name, c.mkt, c.ok)
		}
	}
}
