package shell

import (
	"strings"
	"testing"
)

// 工具图标映射：命令类走 IsCommand；mcp__ 走 🔨 + 去前缀名；搜索类走 Tool Search；未知走默认。
func TestTaskToolGlyph(t *testing.T) {
	if v := taskToolGlyph("bash"); !v.IsCommand {
		t.Fatalf("bash should be command, got %+v", v)
	}
	if v := taskToolGlyph("execute_command"); !v.IsCommand {
		t.Fatalf("execute_command should be command, got %+v", v)
	}
	if v := taskToolGlyph("mcp__tapd-openapi__list"); v.IsCommand || v.Icon != "🔨" || v.Label != "tapd-openapi" {
		t.Fatalf("mcp tool mapping wrong: %+v", v)
	}
	if v := taskToolGlyph("grep"); v.Label != "Tool Search" {
		t.Fatalf("grep should map to Tool Search, got %+v", v)
	}
	if v := taskToolGlyph("codebase_search"); v.Label != "Tool Search" {
		t.Fatalf("codebase_search should map to Tool Search, got %+v", v)
	}
	if v := taskToolGlyph("totally_unknown_tool"); v.Icon != "🔧" || v.Label != "totally_unknown_tool" {
		t.Fatalf("unknown tool default wrong: %+v", v)
	}
}

func TestMCPDisplayName(t *testing.T) {
	cases := map[string]string{
		"mcp__tapd-openapi__list": "tapd-openapi",
		"mcp__tapd-openapi":       "tapd-openapi",
		"plain":                   "plain",
	}
	for in, want := range cases {
		if got := mcpDisplayName(in); got != want {
			t.Errorf("mcpDisplayName(%q)=%q, want %q", in, got, want)
		}
	}
}

// renderTaskToolLine：命令样式含 ">_"、就绪态含折叠箭头 ∨、运行态含 ⋯、失败态含 ✗。
func TestRenderTaskToolLine(t *testing.T) {
	r := &REPL{useColor: false, useASCII: false}

	// 命令类：就绪态（含摘要 + ∨）
	cmd := r.renderTaskToolLine("bash", "ls -la", false, false, false)
	if !strings.Contains(cmd, ">_") || !strings.Contains(cmd, "ls -la") || !strings.Contains(cmd, "∨") {
		t.Fatalf("command ready line wrong: %q", cmd)
	}

	// 命令类沙箱：含 🔒
	box := r.renderTaskToolLine("bash", "go build", false, false, true)
	if !strings.Contains(box, "🔒") {
		t.Fatalf("sandbox marker missing: %q", box)
	}

	// 工具类：运行态含 ⋯
	run := r.renderTaskToolLine("mcp__tapd-openapi__list", "", true, false, false)
	if !strings.Contains(run, "🔨") || !strings.Contains(run, "tapd-openapi") || !strings.Contains(run, "⋯") {
		t.Fatalf("tool running line wrong: %q", run)
	}

	// 失败态含 ✗
	errLine := r.renderTaskToolLine("grep", "\"foo\"", false, true, false)
	if !strings.Contains(errLine, "✗") {
		t.Fatalf("error line should contain ✗: %q", errLine)
	}
}

// ASCII 兜底：不得出现 Unicode 折叠箭头/加载帧/锁标记。
func TestRenderTaskToolLineASCII(t *testing.T) {
	r := &REPL{useColor: false, useASCII: true}
	line := r.renderTaskToolLine("bash", "ls", false, false, false)
	if strings.ContainsAny(line, "∨⋯🔒") {
		t.Fatalf("ascii mode must not contain unicode glyphs: %q", line)
	}
	if !strings.Contains(line, "v") {
		t.Fatalf("ascii ready chevron should be 'v': %q", line)
	}
}

