package cli

import (
	"os"
	"testing"

	"github.com/spf13/cobra"
)

// resolveAgentTeamsEnabled 路由逻辑：
//   - 显式传 --agent-teams 时优先级最高
//   - 否则检查环境变量 GOCLAUDE_AGENT_TEAMS
//   - 最后回退到 YAML（默认 true）
func TestResolveAgentTeamsEnabled(t *testing.T) {
	newCmd := func() *cobra.Command {
		c := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
		c.Flags().BoolVar(&flagAgentTeams, "agent-teams", true, "")
		return c
	}

	t.Run("flag not set falls back to config default (true)", func(t *testing.T) {
		os.Unsetenv("GOCLAUDE_AGENT_TEAMS")
		c := newCmd()
		if err := c.ParseFlags(nil); err != nil {
			t.Fatal(err)
		}
		if !resolveAgentTeamsEnabled(c) {
			t.Errorf("expected true (config default) when flag absent")
		}
	})

	t.Run("explicit --agent-teams=false disables", func(t *testing.T) {
		os.Unsetenv("GOCLAUDE_AGENT_TEAMS")
		c := newCmd()
		if err := c.ParseFlags([]string{"--agent-teams=false"}); err != nil {
			t.Fatal(err)
		}
		if resolveAgentTeamsEnabled(c) {
			t.Errorf("expected false when --agent-teams=false")
		}
	})

	t.Run("explicit --agent-teams=true enables", func(t *testing.T) {
		os.Unsetenv("GOCLAUDE_AGENT_TEAMS")
		c := newCmd()
		if err := c.ParseFlags([]string{"--agent-teams=true"}); err != nil {
			t.Fatal(err)
		}
		if !resolveAgentTeamsEnabled(c) {
			t.Errorf("expected true when --agent-teams=true")
		}
	})

	t.Run("env GOCLAUDE_AGENT_TEAMS=false disables when flag absent", func(t *testing.T) {
		os.Setenv("GOCLAUDE_AGENT_TEAMS", "false")
		defer os.Unsetenv("GOCLAUDE_AGENT_TEAMS")
		c := newCmd()
		if err := c.ParseFlags(nil); err != nil {
			t.Fatal(err)
		}
		if resolveAgentTeamsEnabled(c) {
			t.Errorf("expected false when env=false and flag absent")
		}
	})

	t.Run("env GOCLAUDE_AGENT_TEAMS=true enables when flag absent", func(t *testing.T) {
		os.Setenv("GOCLAUDE_AGENT_TEAMS", "true")
		defer os.Unsetenv("GOCLAUDE_AGENT_TEAMS")
		c := newCmd()
		if err := c.ParseFlags(nil); err != nil {
			t.Fatal(err)
		}
		if !resolveAgentTeamsEnabled(c) {
			t.Errorf("expected true when env=true and flag absent")
		}
	})

	t.Run("env GOCLAUDE_AGENT_TEAMS=0 disables", func(t *testing.T) {
		os.Setenv("GOCLAUDE_AGENT_TEAMS", "0")
		defer os.Unsetenv("GOCLAUDE_AGENT_TEAMS")
		c := newCmd()
		if err := c.ParseFlags(nil); err != nil {
			t.Fatal(err)
		}
		if resolveAgentTeamsEnabled(c) {
			t.Errorf("expected false when env=0")
		}
	})

	t.Run("env GOCLAUDE_AGENT_TEAMS=1 enables", func(t *testing.T) {
		os.Setenv("GOCLAUDE_AGENT_TEAMS", "1")
		defer os.Unsetenv("GOCLAUDE_AGENT_TEAMS")
		c := newCmd()
		if err := c.ParseFlags(nil); err != nil {
			t.Fatal(err)
		}
		if !resolveAgentTeamsEnabled(c) {
			t.Errorf("expected true when env=1")
		}
	})

	t.Run("flag overrides env (flag=true, env=false)", func(t *testing.T) {
		os.Setenv("GOCLAUDE_AGENT_TEAMS", "false")
		defer os.Unsetenv("GOCLAUDE_AGENT_TEAMS")
		c := newCmd()
		if err := c.ParseFlags([]string{"--agent-teams=true"}); err != nil {
			t.Fatal(err)
		}
		if !resolveAgentTeamsEnabled(c) {
			t.Errorf("expected true: flag should override env")
		}
	})

	t.Run("flag overrides env (flag=false, env=true)", func(t *testing.T) {
		os.Setenv("GOCLAUDE_AGENT_TEAMS", "true")
		defer os.Unsetenv("GOCLAUDE_AGENT_TEAMS")
		c := newCmd()
		if err := c.ParseFlags([]string{"--agent-teams=false"}); err != nil {
			t.Fatal(err)
		}
		if resolveAgentTeamsEnabled(c) {
			t.Errorf("expected false: flag should override env")
		}
	})
}
