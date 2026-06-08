package mcpinfra

import (
	"os"
	"path/filepath"
	"testing"
)

// 同时存在 .goclaude/.mcp.json、.claude/.mcp.json 与根 .mcp.json 时，
// 加载顺序为 .goclaude/.mcp.json 最后 → 优先级最高。
func TestLoadDefault_PrefersClaudeDirOverProjectRoot(t *testing.T) {
	tmp := t.TempDir()

	rootCfg := filepath.Join(tmp, ".mcp.json")
	if err := os.WriteFile(rootCfg, []byte(`{"mcpServers":{"echo":{"type":"stdio","command":"root"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// .claude (legacy)
	legacyDir := filepath.Join(tmp, ".claude")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, ".mcp.json"),
		[]byte(`{"mcpServers":{"echo":{"type":"stdio","command":"legacy"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// .goclaude (primary)
	primaryDir := filepath.Join(tmp, ".goclaude")
	if err := os.MkdirAll(primaryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(primaryDir, ".mcp.json"),
		[]byte(`{"mcpServers":{"echo":{"type":"stdio","command":"new"}}}`), 0o644); err != nil {
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
		t.Fatalf("expected .goclaude/.mcp.json to win, got command=%q", got)
	}
}

// 仅有 .goclaude/.mcp.json 也能正常加载（新项目骨架）。
func TestLoadDefault_LoadsFromClaudeDirOnly(t *testing.T) {
	tmp := t.TempDir()
	primaryDir := filepath.Join(tmp, ".goclaude")
	if err := os.MkdirAll(primaryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(primaryDir, ".mcp.json")
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
