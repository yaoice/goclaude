package shell

import (
	"strings"
	"testing"
)

// fakeLookup 给 collectEnvSources 注入受测的来源索引，避免依赖真实 dotenv/settingsenv 状态。
func fakeLookup(m map[string]string) envSourceLookup {
	return func(name string) string { return m[name] }
}

// 已 set 且在 dotenv 里有记录 → From=路径。
func TestCollectEnvSources_SetWithKnownSource(t *testing.T) {
	t.Setenv("GOCLAUDE_PERMISSION_MODE", "acceptEdits")
	srcs := collectEnvSources(
		[]string{"GOCLAUDE_PERMISSION_MODE"},
		fakeLookup(map[string]string{"GOCLAUDE_PERMISSION_MODE": "/tmp/.env"}),
	)
	if len(srcs) != 1 {
		t.Fatalf("want 1 status, got %d", len(srcs))
	}
	if !srcs[0].Set {
		t.Error("Set should be true")
	}
	if srcs[0].From != "/tmp/.env" {
		t.Errorf("From = %q, want /tmp/.env", srcs[0].From)
	}
}

// 已 set 但无来源记录 → 标 "shell or --env-file"。
func TestCollectEnvSources_SetWithoutSourceLabeledShell(t *testing.T) {
	t.Setenv("GOCLAUDE_USE_BUILTIN_GREP", "1")
	srcs := collectEnvSources(
		[]string{"GOCLAUDE_USE_BUILTIN_GREP"},
		fakeLookup(nil), // 没人记录过它 → 必然来自 shell / flag
	)
	if !srcs[0].Set {
		t.Fatal("Set should be true")
	}
	if srcs[0].From != "shell or --env-file" {
		t.Errorf("From = %q, want shell or --env-file", srcs[0].From)
	}
}

// 未 set → Set=false, From=空。
func TestCollectEnvSources_UnsetVar(t *testing.T) {
	const key = "GOCLAUDE_TEST_NEVER_SET_XYZ"
	_ = key
	srcs := collectEnvSources([]string{key}, fakeLookup(nil))
	if srcs[0].Set {
		t.Errorf("should be unset, got Set=true")
	}
	if srcs[0].From != "" {
		t.Errorf("From should be empty for unset, got %q", srcs[0].From)
	}
}

// 多变量按字母序输出（与 /env 输出稳定性契约）。
func TestCollectEnvSources_AlphaSorted(t *testing.T) {
	srcs := collectEnvSources(
		[]string{"ZZZ_LAST", "AAA_FIRST", "MMM_MID"},
		fakeLookup(nil),
	)
	want := []string{"AAA_FIRST", "MMM_MID", "ZZZ_LAST"}
	for i, w := range want {
		if srcs[i].Name != w {
			t.Errorf("srcs[%d] = %s, want %s", i, srcs[i].Name, w)
		}
	}
}

// isSensitiveEnv：API key / token / secret / password 都视为敏感。
func TestIsSensitiveEnv(t *testing.T) {
	cases := map[string]bool{
		"DEEPSEEK_API_KEY":          true,
		"ANTHROPIC_API_KEY":         true,
		"GITHUB_TOKEN":              true,
		"SOME_SECRET":               true,
		"DB_PASSWORD":               true,
		"GOCLAUDE_PERMISSION_MODE":  false,
		"GOCLAUDE_USE_BUILTIN_GREP": false,
		"GOCLAUDE_TEAM_NAME":        false,
		"PATH":                      false,
	}
	for name, want := range cases {
		if got := isSensitiveEnv(name); got != want {
			t.Errorf("isSensitiveEnv(%q) = %v, want %v", name, got, want)
		}
	}
}

// renderEnvOverview 必须包含 "set"、"unset"、对齐——并且**绝不**打印任何环境变量的值。
func TestRenderEnvOverview_NeverPrintsValues(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-super-secret-do-not-leak-12345")
	t.Setenv("GOCLAUDE_PERMISSION_MODE", "acceptEdits")

	r := &REPL{useColor: false}
	statuses := collectEnvSources(
		[]string{"DEEPSEEK_API_KEY", "GOCLAUDE_PERMISSION_MODE", "GOCLAUDE_TEAM_NAME"},
		fakeLookup(map[string]string{
			"DEEPSEEK_API_KEY":         "/home/u/.claude/.env",
			"GOCLAUDE_PERMISSION_MODE": "/proj/.env",
		}),
	)
	text := r.renderEnvOverview(statuses)

	// 核心安全契约：变量值绝不出现在输出里
	if strings.Contains(text, "sk-super-secret-do-not-leak-12345") {
		t.Fatalf("env value leaked into /env output:\n%s", text)
	}
	if strings.Contains(text, "acceptEdits") {
		// 这个是非敏感但我们也保持纯粹：永远不打印值
		t.Fatalf("env value leaked into /env output (non-sensitive but still policy):\n%s", text)
	}

	// 应包含 set 状态、来源路径与敏感标识
	for _, want := range []string{
		"DEEPSEEK_API_KEY", "set (hidden)", "/home/u/.claude/.env",
		"GOCLAUDE_PERMISSION_MODE", "/proj/.env",
		"GOCLAUDE_TEAM_NAME", "unset",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in /env output:\n%s", want, text)
		}
	}
}

// CRLF + 行对齐：与 /help 一致，便于 raw 模式 PTY。
func TestRenderEnvOverview_UsesCRLF(t *testing.T) {
	r := &REPL{useColor: false}
	text := r.renderEnvOverview(collectEnvSources(
		[]string{"GOCLAUDE_PERMISSION_MODE"}, fakeLookup(nil)))
	if strings.Contains(text, "\n") && !strings.Contains(text, "\r\n") {
		t.Fatalf("/env must use CRLF")
	}
	// 不应有裸 \n（除嵌入 \r\n 中）
	stripped := strings.ReplaceAll(text, "\r\n", "")
	if strings.Contains(stripped, "\n") {
		t.Fatalf("/env contains bare LF")
	}
}

// trackedEnvVars 必须包含核心运行时开关——这是用户排错的入口，
// 漏一个就少一个观察点。
func TestTrackedEnvVars_CoversRuntimeKnobs(t *testing.T) {
	must := []string{
		"GOCLAUDE_PERMISSION_MODE",
		"GOCLAUDE_USE_BUILTIN_GREP",
		"DEEPSEEK_API_KEY",
		"ANTHROPIC_API_KEY",
	}
	have := map[string]bool{}
	for _, n := range trackedEnvVars {
		have[n] = true
	}
	for _, m := range must {
		if !have[m] {
			t.Errorf("trackedEnvVars missing %q", m)
		}
	}
}

// stripANSI 去掉 ANSI 转义序列。
func TestStripANSI(t *testing.T) {
	cases := map[string]string{
		"":                            "",
		"hello":                       "hello",
		"\x1b[36mcyan\x1b[0m":         "cyan",
		"\x1b[1;33mbold yellow\x1b[0m": "bold yellow",
		// 单字节 ESC X
		"a\x1bXb": "ab",
	}
	for in, want := range cases {
		got := stripANSI(in)
		if got != want {
			t.Errorf("stripANSI(%q) = %q, want %q", in, got, want)
		}
	}
}
