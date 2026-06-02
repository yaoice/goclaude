package appconfig

import (
	"os"
	"path/filepath"
	"testing"
)

// 默认配置应开启 agent-teams（保持历史行为）。
func TestDefaultConfig_AgentTeamsEnabledByDefault(t *testing.T) {
	c := DefaultConfig()
	if !c.AgentTeams.Enabled {
		t.Errorf("agent_teams.enabled should default to true, got false")
	}
}

// YAML 可关闭 agent-teams（切换到单一 subagent 执行模式）。
func TestLoadFromPath_AgentTeamsDisable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(path, []byte("agent_teams:\n  enabled: false\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFromPath(path)
	if err != nil {
		t.Fatalf("LoadFromPath: %v", err)
	}
	if c.AgentTeams.Enabled {
		t.Errorf("agent_teams.enabled = true, want false after YAML override")
	}
}

// 未在 YAML 出现 agent_teams 段时应保留默认（true）。
func TestLoadFromPath_AgentTeamsMissingKeepsDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(path, []byte("api:\n  provider: anthropic\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFromPath(path)
	if err != nil {
		t.Fatalf("LoadFromPath: %v", err)
	}
	if !c.AgentTeams.Enabled {
		t.Errorf("agent_teams.enabled should stay true when omitted, got false")
	}
}
