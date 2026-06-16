package plugin

import (
	"encoding/json"
	"testing"
)

func TestClassifySource(t *testing.T) {
	cases := []struct {
		in   string
		want SourceType
	}{
		{"/abs/local/path", SourceLocal},
		{"./relative/path", SourceLocal},
		{"git@github.com:owner/repo.git", SourceGit},
		{"https://github.com/owner/repo.git", SourceGit},
		{"https://github.com/owner/repo", SourceGit},
		{"git+https://example.com/repo", SourceGit},
		{"https://example.com/pkg.tar.gz", SourceHTTP},
		{"https://example.com/pkg.zip", SourceHTTP},
		{"https://example.com/pkg.tgz", SourceHTTP},
		{"http://example.com/pkg.tar", SourceHTTP},
	}
	for _, c := range cases {
		if got := ClassifySource(c.in); got != c.want {
			t.Errorf("ClassifySource(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAuthorUnmarshal(t *testing.T) {
	var a1 Author
	if err := json.Unmarshal([]byte(`"Alice"`), &a1); err != nil {
		t.Fatal(err)
	}
	if a1.Name != "Alice" {
		t.Errorf("string author: got %q", a1.Name)
	}

	var a2 Author
	if err := json.Unmarshal([]byte(`{"name":"Bob","email":"b@x.io"}`), &a2); err != nil {
		t.Fatal(err)
	}
	if a2.Name != "Bob" || a2.Email != "b@x.io" {
		t.Errorf("object author: got %+v", a2)
	}
}

func TestStringListUnmarshal(t *testing.T) {
	var s1 StringList
	if err := json.Unmarshal([]byte(`"./commands"`), &s1); err != nil {
		t.Fatal(err)
	}
	if len(s1) != 1 || s1[0] != "./commands" {
		t.Errorf("single: got %v", s1)
	}

	var s2 StringList
	if err := json.Unmarshal([]byte(`["./a","./b"]`), &s2); err != nil {
		t.Fatal(err)
	}
	if len(s2) != 2 {
		t.Errorf("array: got %v", s2)
	}
}

func TestEntrySourceUnmarshal(t *testing.T) {
	// 字符串形式
	var e1 MarketplaceEntry
	if err := json.Unmarshal([]byte(`{"name":"p","source":"./p1"}`), &e1); err != nil {
		t.Fatalf("string source: %v", err)
	}
	if src, ref := e1.Source.Locator(); src != "./p1" || ref != "" {
		t.Errorf("string locator = (%q,%q)", src, ref)
	}

	// 对象形式：{"source":"url","url":"...","ref":"..."}（superpowers 形态）
	var e2 MarketplaceEntry
	raw := `{"name":"sp","source":{"source":"url","url":"https://github.com/obra/superpowers.git","ref":"dev"}}`
	if err := json.Unmarshal([]byte(raw), &e2); err != nil {
		t.Fatalf("object source: %v", err)
	}
	if src, ref := e2.Source.Locator(); src != "https://github.com/obra/superpowers.git" || ref != "dev" {
		t.Errorf("object locator = (%q,%q)", src, ref)
	}

	// 对象形式：github owner/repo 简写
	var e3 MarketplaceEntry
	if err := json.Unmarshal([]byte(`{"name":"g","source":{"source":"github","repo":"owner/repo"}}`), &e3); err != nil {
		t.Fatalf("github source: %v", err)
	}
	if src, _ := e3.Source.Locator(); src != "https://github.com/owner/repo.git" {
		t.Errorf("github locator = %q", src)
	}

	// 整个市场清单（plugins 内为对象 source）能被解析
	var mm MarketplaceManifest
	manifest := `{"name":"m","plugins":[{"name":"sp","source":{"source":"url","url":"https://x/y.git"}}]}`
	if err := json.Unmarshal([]byte(manifest), &mm); err != nil {
		t.Fatalf("manifest with object source: %v", err)
	}
	if len(mm.Plugins) != 1 || mm.Plugins[0].Source.URL != "https://x/y.git" {
		t.Errorf("manifest plugins = %+v", mm.Plugins)
	}
}

func TestNormalizeSourceGitHubShorthand(t *testing.T) {
	cases := []struct {
		in   string
		want string
		typ  SourceType
	}{
		{"obra/superpowers-marketplace", "https://github.com/obra/superpowers-marketplace.git", SourceGit},
		{"owner/repo.git", "https://github.com/owner/repo.git", SourceGit},
		// 非简写：原样返回
		{"https://github.com/obra/superpowers-marketplace.git", "https://github.com/obra/superpowers-marketplace.git", SourceGit},
		{"git@github.com:owner/repo.git", "git@github.com:owner/repo.git", SourceGit},
		{"./local/dir", "./local/dir", SourceLocal},
		{"/abs/path", "/abs/path", SourceLocal},
		{"a/b/c", "a/b/c", SourceLocal}, // 多级路径不是 owner/repo 简写
		{"barename", "barename", SourceLocal},
	}
	for _, c := range cases {
		if got := NormalizeSource(c.in); got != c.want {
			t.Errorf("NormalizeSource(%q) = %q, want %q", c.in, got, c.want)
		}
		if got := ClassifySource(c.in); got != c.typ {
			t.Errorf("ClassifySource(%q) = %q, want %q", c.in, got, c.typ)
		}
	}
}

func TestManifestParse(t *testing.T) {
	raw := `{
		"name": "demo",
		"version": "1.2.0",
		"description": "d",
		"author": "Carol",
		"commands": "./cmds",
		"agents": ["./agents"],
		"hooks": "hooks/hooks.json",
		"mcpServers": ".mcp.json"
	}`
	var m Manifest
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if m.Name != "demo" || m.Version != "1.2.0" || m.Author.Name != "Carol" {
		t.Errorf("manifest: %+v", m)
	}
	if len(m.Commands) != 1 || m.Commands[0] != "./cmds" {
		t.Errorf("commands: %v", m.Commands)
	}
	if m.Hooks != "hooks/hooks.json" || m.MCPServers != ".mcp.json" {
		t.Errorf("hooks/mcp: %q %q", m.Hooks, m.MCPServers)
	}
}

func TestRegistryLifecycle(t *testing.T) {
	r := NewRegistry()

	r.AddMarketplace(&Marketplace{Name: "mp1", Type: SourceLocal, Source: "/x", LocalPath: "/x"})
	if _, ok := r.GetMarketplace("mp1"); !ok {
		t.Fatal("marketplace not found")
	}
	if len(r.Marketplaces()) != 1 {
		t.Fatal("expected 1 marketplace")
	}

	r.AddPlugin(&Plugin{Name: "p1", InstallPath: "/p1", Enabled: false})
	r.AddPlugin(&Plugin{Name: "p2", InstallPath: "/p2", Enabled: true})

	if len(r.Plugins()) != 2 {
		t.Fatal("expected 2 plugins")
	}
	if len(r.EnabledPlugins()) != 1 {
		t.Fatal("expected 1 enabled plugin")
	}

	if !r.SetEnabled("p1", true) {
		t.Fatal("SetEnabled failed")
	}
	if len(r.EnabledPlugins()) != 2 {
		t.Fatal("expected 2 enabled after enable")
	}

	if _, ok := r.RemovePlugin("p1"); !ok {
		t.Fatal("RemovePlugin failed")
	}
	if !r.RemoveMarketplace("mp1") {
		t.Fatal("RemoveMarketplace failed")
	}

	// Snapshot / Load 往返
	r.AddMarketplace(&Marketplace{Name: "mp2", Type: SourceGit, Source: "g"})
	st := r.Snapshot()
	r2 := NewRegistry()
	r2.Load(st)
	if _, ok := r2.GetMarketplace("mp2"); !ok {
		t.Fatal("snapshot/load lost marketplace")
	}
	if _, ok := r2.GetPlugin("p2"); !ok {
		t.Fatal("snapshot/load lost plugin")
	}
}
