package cli

import (
	"testing"

	"github.com/anthropics/goclaude/pkg/domain/tool"
	"github.com/anthropics/goclaude/pkg/infrastructure/appconfig"
)

// 测试帮助：临时替换全局 loadedAppConfig，函数返回时自动恢复。
func withAppConfig(t *testing.T, mode string) {
	t.Helper()
	prev := loadedAppConfig
	cfg := appconfig.DefaultConfig()
	cfg.Permissions.Mode = mode
	loadedAppConfig = cfg
	t.Cleanup(func() { loadedAppConfig = prev })
}

// flag 优先级最高：传 true 时无视 yaml 配置。
func TestResolveInitialPermissionMode_FlagBeatsConfig(t *testing.T) {
	withAppConfig(t, "default")
	got := resolveInitialPermissionMode(true)
	if got != tool.PermissionModeBypass {
		t.Fatalf("flag should force bypass, got %v", got)
	}
}

// 没有 flag 时按 yaml 配置取值。
func TestResolveInitialPermissionMode_UsesConfig(t *testing.T) {
	cases := map[string]tool.PermissionMode{
		"":            tool.PermissionModeDefault,
		"default":     tool.PermissionModeDefault,
		"acceptEdits": tool.PermissionModeAcceptEdits,
		"plan":        tool.PermissionModePlan,
		"bypass":      tool.PermissionModeBypass,
	}
	for mode, want := range cases {
		t.Run("mode="+mode, func(t *testing.T) {
			withAppConfig(t, mode)
			got := resolveInitialPermissionMode(false)
			if got != want {
				t.Fatalf("mode=%q → got %v, want %v", mode, got, want)
			}
		})
	}
}

// yaml 配置为非法值时回退到安全默认（Default）+ 不报错。
func TestResolveInitialPermissionMode_InvalidConfigFallsBackToDefault(t *testing.T) {
	withAppConfig(t, "ask-once-per-day")
	got := resolveInitialPermissionMode(false)
	if got != tool.PermissionModeDefault {
		t.Fatalf("invalid config must fall back to Default, got %v", got)
	}
}

// 环境变量解析的字面量与别名集；大小写/分隔符不敏感。
func TestParsePermissionMode_KnownValues(t *testing.T) {
	cases := []struct {
		in   string
		want tool.PermissionMode
	}{
		{"", tool.PermissionModeDefault},
		{"default", tool.PermissionModeDefault},
		{"DEFAULT", tool.PermissionModeDefault},
		{"acceptEdits", tool.PermissionModeAcceptEdits},
		{"accept-edits", tool.PermissionModeAcceptEdits},
		{"accept_edits", tool.PermissionModeAcceptEdits},
		{"AUTO_EDIT", tool.PermissionModeAcceptEdits},
		{"plan", tool.PermissionModePlan},
		{" Plan ", tool.PermissionModePlan},
		{"bypass", tool.PermissionModeBypass},
		{"BYPASS", tool.PermissionModeBypass},
		{"skip", tool.PermissionModeBypass},
		{"yolo", tool.PermissionModeBypass},
	}
	for _, c := range cases {
		got, ok := parsePermissionMode(c.in)
		if !ok {
			t.Fatalf("parsePermissionMode(%q): expected ok=true", c.in)
		}
		if got != c.want {
			t.Errorf("parsePermissionMode(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// 错值：返回 (Default, false) 让上层走 Warn 路径，而不是 panic 或锁定用户。
func TestParsePermissionMode_RejectsUnknown(t *testing.T) {
	for _, bad := range []string{"foo", "ask", "true", "1", "off", "permissive"} {
		got, ok := parsePermissionMode(bad)
		if ok {
			t.Errorf("parsePermissionMode(%q) should be unknown, got %v ok=true", bad, got)
		}
		if got != tool.PermissionModeDefault {
			t.Errorf("parsePermissionMode(%q) on error must fall back to Default, got %v", bad, got)
		}
	}
}
