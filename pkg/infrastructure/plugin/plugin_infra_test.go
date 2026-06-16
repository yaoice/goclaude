package plugininfra

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yaoice/goclaude/pkg/domain/plugin"
)

func TestValidateHost_RejectsInternal(t *testing.T) {
	SetAllowInternalHosts(false)
	bad := []string{
		"127.0.0.1", "10.1.2.3", "192.168.0.1", "172.16.5.5",
		"9.9.9.9", "11.0.0.1", "21.0.0.1", "30.0.0.1", "169.254.1.1",
	}
	for _, h := range bad {
		if err := validateHost(h); err == nil {
			t.Errorf("expected %q to be rejected", h)
		}
	}
}

func TestValidateHost_AllowsPublic(t *testing.T) {
	SetAllowInternalHosts(false)
	good := []string{"8.8.8.8", "1.1.1.1"}
	for _, h := range good {
		if err := validateHost(h); err != nil {
			t.Errorf("expected %q to be allowed, got %v", h, err)
		}
	}
}

func TestValidateHost_AllowOverride(t *testing.T) {
	SetAllowInternalHosts(true)
	defer SetAllowInternalHosts(false)
	if err := validateHost("10.0.0.1"); err != nil {
		t.Errorf("override should allow internal host: %v", err)
	}
}

func TestExtractHost(t *testing.T) {
	cases := map[string]string{
		"https://github.com/owner/repo.git": "github.com",
		"git@github.com:owner/repo.git":     "github.com",
		"http://example.com/a.zip":          "example.com",
	}
	for in, want := range cases {
		if got := extractHost(in); got != want {
			t.Errorf("extractHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInstallLocal_AndContributions(t *testing.T) {
	src := t.TempDir()
	// 构造一个插件目录
	mustWrite(t, filepath.Join(src, ".claude-plugin", "plugin.json"), `{"name":"demo","version":"0.1.0","description":"d"}`)
	mustWrite(t, filepath.Join(src, "commands", "hello.md"), "say hello")
	mustWrite(t, filepath.Join(src, "skills", "s1", "SKILL.md"), "---\nname: s1\n---\nbody")
	mustWrite(t, filepath.Join(src, "agents", "a1.md"), "---\nname: a1\ndescription: d\n---\nsp")
	mustWrite(t, filepath.Join(src, "hooks", "hooks.json"), `{"hooks":{}}`)
	mustWrite(t, filepath.Join(src, ".mcp.json"), `{"mcpServers":{}}`)

	dest := filepath.Join(t.TempDir(), "demo")
	inst := NewInstaller()
	root, err := inst.Install(context.Background(), src, dest)
	if err != nil {
		t.Fatalf("install local: %v", err)
	}

	loader := NewMarketplaceLoader(inst)
	m, err := loader.ParseManifest(root)
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if m.Name != "demo" {
		t.Errorf("manifest name = %q", m.Name)
	}

	c := ResolveContributions(root, m)
	if len(c.CommandDirs) != 1 || len(c.AgentDirs) != 1 || len(c.SkillDirs) != 1 {
		t.Errorf("contributions dirs: %+v", c)
	}
	if len(c.HookFiles) != 1 || len(c.MCPConfigFiles) != 1 {
		t.Errorf("contributions files: %+v", c)
	}
}

func TestExtractZip_RejectsZipSlip(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "evil.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("../escape.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("pwned"))
	zw.Close()
	f.Close()

	dest := filepath.Join(dir, "out")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := extractZip(archive, dest); err == nil {
		t.Fatal("expected zip-slip to be rejected")
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.txt")); err == nil {
		t.Fatal("zip-slip wrote outside dest")
	}
}

func TestExtractZip_Normal(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "ok.zip")
	f, _ := os.Create(archive)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("plugin/.claude-plugin/plugin.json")
	_, _ = w.Write([]byte(`{"name":"z"}`))
	zw.Close()
	f.Close()

	dest := filepath.Join(dir, "out")
	_ = os.MkdirAll(dest, 0o755)
	if err := extractZip(archive, dest); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "plugin", ".claude-plugin", "plugin.json")); err != nil {
		t.Fatalf("expected extracted file: %v", err)
	}
}

func TestMarketplaceLoader_Local(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".claude-plugin", "marketplace.json"),
		`{"name":"mymkt","plugins":[{"name":"p1","source":"./p1","description":"first"}]}`)

	loader := NewMarketplaceLoader(nil)
	m, err := loader.Load(context.Background(), root, t.TempDir())
	if err != nil {
		t.Fatalf("load marketplace: %v", err)
	}
	if m.Name != "mymkt" || m.Type != plugin.SourceLocal {
		t.Errorf("marketplace = %+v", m)
	}
	if len(m.Entries) != 1 || m.Entries[0].Name != "p1" {
		t.Errorf("entries = %+v", m.Entries)
	}
}

func TestStore_RoundTrip(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "plugins"))

	loaded, err := st.Load()
	if err != nil {
		t.Fatalf("load empty: %v", err)
	}
	if len(loaded.Plugins) != 0 {
		t.Fatal("expected empty state")
	}

	state := &plugin.State{
		Marketplaces: []*plugin.Marketplace{{Name: "mkt", Type: plugin.SourceLocal, Source: "/s", LocalPath: "/s"}},
		Plugins:      []*plugin.Plugin{{Name: "p", InstallPath: "/p", Enabled: true}},
	}
	if err := st.Save(state); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := st.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(got.Plugins) != 1 || got.Plugins[0].Name != "p" || !got.Plugins[0].Enabled {
		t.Errorf("roundtrip plugins = %+v", got.Plugins)
	}
	if len(got.Marketplaces) != 1 || got.Marketplaces[0].Name != "mkt" {
		t.Errorf("roundtrip marketplaces = %+v", got.Marketplaces)
	}
}

func TestStore_SanitizeName(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "plugins"))
	dir := st.InstalledDir("../../etc/passwd")
	if filepath.Base(filepath.Dir(dir)) != "installed" {
		t.Errorf("installed dir should stay under installed/: %s", dir)
	}
	if got := filepath.Base(dir); got == ".." || got == "passwd" && filepath.IsAbs(got) {
		t.Errorf("unsafe name leaked: %s", got)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
