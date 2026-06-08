package appconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfig_HasReasonableDefaults(t *testing.T) {
	c := DefaultConfig()
	if c.API.Provider == "" || c.API.Model == "" {
		t.Errorf("API defaults missing: %+v", c.API)
	}
	if c.API.MaxTokens <= 0 {
		t.Errorf("MaxTokens must be positive: %d", c.API.MaxTokens)
	}
	if _, ok := c.Providers["deepseek"]; !ok {
		t.Errorf("deepseek provider missing in defaults")
	}
	if c.Permissions.Mode != "default" {
		t.Errorf("safe default permission mode: got %q", c.Permissions.Mode)
	}
}

func TestLoadFromPath_OverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	content := `
api:
  provider: anthropic
  model: claude-sonnet-4-20250514
  max_tokens: 16384
  temperature: 0.7
providers:
  deepseek:
    base_url: https://proxy.example.com
    timeout: 60s
  anthropic:
    base_url: https://api.anthropic.com
permissions:
  mode: acceptEdits
mcp:
  enabled: false
  connect_timeout: 5s
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFromPath(path)
	if err != nil {
		t.Fatalf("LoadFromPath: %v", err)
	}
	if c.API.Provider != "anthropic" {
		t.Errorf("provider = %q", c.API.Provider)
	}
	if c.API.MaxTokens != 16384 {
		t.Errorf("max_tokens = %d", c.API.MaxTokens)
	}
	if c.API.Temperature != 0.7 {
		t.Errorf("temperature = %v", c.API.Temperature)
	}
	if c.Providers["deepseek"].BaseURL != "https://proxy.example.com" {
		t.Errorf("deepseek base_url = %q", c.Providers["deepseek"].BaseURL)
	}
	if c.Providers["deepseek"].Timeout != 60*time.Second {
		t.Errorf("deepseek timeout = %v", c.Providers["deepseek"].Timeout)
	}
	if c.Permissions.Mode != "acceptEdits" {
		t.Errorf("permission mode = %q", c.Permissions.Mode)
	}
	if c.MCP.Enabled != false {
		t.Errorf("mcp.enabled = %v", c.MCP.Enabled)
	}
}

func TestLoadFromPath_MissingFieldsKeepDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	// 只覆盖 api.provider；其它一律保留 defaults
	if err := os.WriteFile(path, []byte("api:\n  provider: anthropic\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFromPath(path)
	if err != nil {
		t.Fatalf("LoadFromPath: %v", err)
	}
	if c.API.Provider != "anthropic" {
		t.Errorf("provider = %q", c.API.Provider)
	}
	// max_tokens 没改 → 保留 defaults
	if c.API.MaxTokens != DefaultConfig().API.MaxTokens {
		t.Errorf("max_tokens lost defaults: %d", c.API.MaxTokens)
	}
}

func TestLoad_ProjectOverridesUser(t *testing.T) {
	tmpHome := t.TempDir()
	tmpProj := t.TempDir()
	t.Setenv("HOME", tmpHome)

	userCfg := filepath.Join(tmpHome, ".goclaude", "config.yaml")
	_ = os.MkdirAll(filepath.Dir(userCfg), 0755)
	_ = os.WriteFile(userCfg, []byte("api:\n  model: user-model\n  temperature: 0.1\n"), 0644)

	projCfg := filepath.Join(tmpProj, ".goclaude.yaml")
	_ = os.WriteFile(projCfg, []byte("api:\n  model: proj-model\n"), 0644)

	c, err := Load(tmpProj)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// 项目覆盖 model
	if c.API.Model != "proj-model" {
		t.Errorf("model = %q (want proj-model)", c.API.Model)
	}
	// temperature 仅 user 设置过，项目没动 → 应保留 user 的 0.1
	if c.API.Temperature != 0.1 {
		t.Errorf("temperature = %v (want user-set 0.1)", c.API.Temperature)
	}
	// LoadedFrom 至少包含 user 与 project
	joined := strings.Join(c.LoadedFrom, "|")
	if !strings.Contains(joined, "config.yaml") || !strings.Contains(joined, ".goclaude.yaml") {
		t.Errorf("LoadedFrom = %v", c.LoadedFrom)
	}
}
