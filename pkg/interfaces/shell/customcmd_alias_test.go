package shell

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCustomCommands_AliasesAndPluginDir(t *testing.T) {
	dir := t.TempDir()
	userCmdDir := filepath.Join(dir, ".goclaude", "commands")
	if err := os.MkdirAll(userCmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 带别名的项目命令
	cmd := `---
description: Run the deploy flow
aliases: ["dp", "ship"]
---
执行部署：$ARGUMENTS`
	if err := os.WriteFile(filepath.Join(userCmdDir, "deploy.md"), []byte(cmd), 0o644); err != nil {
		t.Fatal(err)
	}

	cc := NewCustomCommands()
	cc.LoadDefaults(dir)

	// 规范名可取
	if _, ok := cc.Get("deploy"); !ok {
		t.Fatal("deploy not found")
	}
	// 别名可取，且指向同一命令
	c1, ok := cc.Get("dp")
	if !ok {
		t.Fatal("alias dp not resolved")
	}
	c2, _ := cc.Get("ship")
	if c1 == nil || c2 == nil || c1.Name != "deploy" || c2.Name != "deploy" {
		t.Fatalf("aliases should resolve to deploy: %+v %+v", c1, c2)
	}
	// 别名应出现在补全列表中
	slash := cc.SlashNames()
	hasAlias := false
	for _, s := range slash {
		if s == "/dp" {
			hasAlias = true
		}
	}
	if !hasAlias {
		t.Fatalf("alias should appear in SlashNames: %v", slash)
	}

	// 插件命令目录合并（source=plugin）
	pluginCmdDir := filepath.Join(t.TempDir(), "plugin-commands")
	if err := os.MkdirAll(pluginCmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginCmdDir, "greet.md"),
		[]byte("---\ndescription: greet\n---\nhello $ARGUMENTS"), 0o644); err != nil {
		t.Fatal(err)
	}
	cc.LoadPluginDir(pluginCmdDir)
	g, ok := cc.Get("greet")
	if !ok {
		t.Fatal("plugin command greet not loaded")
	}
	if g.Source != "plugin" {
		t.Fatalf("plugin command source = %q, want plugin", g.Source)
	}
}
