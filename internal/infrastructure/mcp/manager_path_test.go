package mcpinfra

import (
	"os"
	"path/filepath"
	"testing"
)

// 同时存在 .claude/.mcp.json 与根 .mcp.json 时，后加载者覆盖（即 .claude/.mcp.json 主目录）。
// 这条测试锁定 .mcp.json 的标准位置已迁移到 .claude/ 下。
func TestLoadDefault_PrefersClaudeDirOverProjectRoot(t *testing.T) {
	tmp := t.TempDir()

	rootCfg := filepath.Join(tmp, ".mcp.json")
	if err := os.WriteFile(rootCfg, []byte(`{"mcpServers":{"echo":{"type":"stdio","command":"legacy"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	claudeDir := filepath.Join(tmp, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeCfg := filepath.Join(claudeDir, ".mcp.json")
	if err := os.WriteFile(claudeCfg, []byte(`{"mcpServers":{"echo":{"type":"stdio","command":"new"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	configs, err := LoadDefault(tmp)
	if err != nil {
		t.Fatalf("load default: %v", err)
	}
	var got string
	for _, c := range configs {
		if c.Name == "echo" {
			got = c.Command
		}
	}
	if got != "new" {
		t.Fatalf("expected .claude/.mcp.json to win, got command=%q", got)
	}
}

// 仅有 .claude/.mcp.json 也能正常加载（新项目骨架）。
func TestLoadDefault_LoadsFromClaudeDirOnly(t *testing.T) {
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(claudeDir, ".mcp.json")
	if err := os.WriteFile(cfg, []byte(`{"mcpServers":{"alpha":{"type":"stdio","command":"alpha"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	configs, err := LoadDefault(tmp)
	if err != nil {
		t.Fatalf("load default: %v", err)
	}
	if len(configs) != 1 || configs[0].Name != "alpha" || configs[0].Command != "alpha" {
		t.Fatalf("unexpected configs: %+v", configs)
	}
}
