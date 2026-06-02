package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/goclaude/internal/application"
	"github.com/anthropics/goclaude/internal/domain/tool"
)

// newRenderWithBuf 创建一个写到内存 buf 的渲染器，并强制关闭颜色，
// 让断言聚焦在结构与字面量上而不是 ANSI 转义序列。
func newRenderWithBuf() (*headlessRender, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return &headlessRender{out: buf, color: false}, buf
}

// 工具 start 行：[N] ◌ + 工具名。
func TestHeadlessRender_ToolStart_RendersSingleLine(t *testing.T) {
	r, buf := newRenderWithBuf()
	r.HandleToolEvent(tool.ToolEvent{
		Phase:     tool.ToolPhaseStart,
		ToolName:  "web_fetch",
		ToolUseID: "u1",
	})
	out := buf.String()
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("want exactly 1 line, got: %q", out)
	}
	for _, want := range []string{"◌", "web_fetch", "[1]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
}

// 工具 finish 成功：[N] ✓ <tool> <elapsed>。
func TestHeadlessRender_ToolFinishOK_IncludesElapsed(t *testing.T) {
	r, buf := newRenderWithBuf()
	r.HandleToolEvent(tool.ToolEvent{
		Phase:    tool.ToolPhaseFinish,
		ToolName: "web_fetch",
		Status:   tool.ToolStatusSuccess,
		Elapsed:  42 * time.Millisecond,
	})
	out := buf.String()
	for _, want := range []string{"✓", "web_fetch", "42ms"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
}

// 工具 finish 失败：[N] ✗ <tool> + 截断后的错误信息。
func TestHeadlessRender_ToolFinishError_IncludesTruncatedError(t *testing.T) {
	r, buf := newRenderWithBuf()
	r.HandleToolEvent(tool.ToolEvent{
		Phase:        tool.ToolPhaseFinish,
		ToolName:     "bash",
		Status:       tool.ToolStatusError,
		Elapsed:      120 * time.Millisecond,
		ErrorMessage: "permission denied: /etc/shadow\nstack trace ...",
	})
	out := buf.String()
	for _, want := range []string{"✗", "bash", "permission denied"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
	// 多行错误必须被压成单行
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("error message must be folded into single line, got: %q", out)
	}
}

// subagent start 行：┏━ <type> · <model>。
func TestHeadlessRender_SubagentStart_FallbacksInheritModel(t *testing.T) {
	r, buf := newRenderWithBuf()
	r.HandleSubagentEvent(application.SubagentEvent{
		Phase:     application.SubagentPhaseStart,
		AgentType: "general-purpose",
		Model:     "", // 空模型应回退展示 (inherit)
	})
	out := buf.String()
	for _, want := range []string{"┏━", "general-purpose", "(inherit)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
}

// subagent finish 行：┗━ ✓ done · elapsed · N steps。
func TestHeadlessRender_SubagentFinishOK_IncludesTurns(t *testing.T) {
	r, buf := newRenderWithBuf()
	r.HandleSubagentEvent(application.SubagentEvent{
		Phase:     application.SubagentPhaseFinish,
		AgentType: "general-purpose",
		Status:    application.SubagentStatusSuccess,
		Elapsed:   1200 * time.Millisecond,
		Turns:     5,
	})
	out := buf.String()
	for _, want := range []string{"┗━", "done", "5 steps"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
}

// Finish 携带 ResultPreview 时应渲染在尾部。
func TestHeadlessRender_SubagentFinishOK_IncludesResultPreview(t *testing.T) {
	r, buf := newRenderWithBuf()
	r.HandleSubagentEvent(application.SubagentEvent{
		Phase:         application.SubagentPhaseFinish,
		AgentType:     "Explore",
		Status:        application.SubagentStatusSuccess,
		Elapsed:       2 * time.Second,
		Turns:         3,
		ResultPreview: "Found 3 matches in src/",
	})
	out := buf.String()
	if !strings.Contains(out, "Found 3 matches in src/") {
		t.Fatalf("missing result preview in %q", out)
	}
}

// Progress 阶段渲染：┃  <type>  turn N  <last_tool>
func TestHeadlessRender_SubagentProgress_RendersHeartbeat(t *testing.T) {
	r, buf := newRenderWithBuf()
	r.HandleSubagentEvent(application.SubagentEvent{
		Phase:     application.SubagentPhaseProgress,
		AgentType: "Explore",
		Turns:     2,
		LastTool:  "grep",
	})
	out := buf.String()
	for _, want := range []string{"┃", "Explore", "turn 2", "grep"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("progress must be single line; got %q", out)
	}
}

// Progress 阶段无 LastTool 时应优雅忽略（不打印空字段）。
func TestHeadlessRender_SubagentProgress_NoTool(t *testing.T) {
	r, buf := newRenderWithBuf()
	r.HandleSubagentEvent(application.SubagentEvent{
		Phase:     application.SubagentPhaseProgress,
		AgentType: "Plan",
		Turns:     1,
	})
	out := buf.String()
	if !strings.Contains(out, "turn 1") {
		t.Fatalf("missing turn marker in %q", out)
	}
	// 不应出现尾部 nbsp 后跟空串等异常
	if strings.HasSuffix(strings.TrimRight(out, "\n"), "  ") {
		t.Fatalf("progress line must not end with stray spaces: %q", out)
	}
}

// 单行渲染的输出不得含有 stdlib `log` 默认时间戳前缀（用户痛点）：
// 旧版本 `2026/05/25 01:10:42 INFO ...` 必须彻底消失。
func TestHeadlessRender_NoStdlibLogPrefix(t *testing.T) {
	r, buf := newRenderWithBuf()
	r.HandleToolEvent(tool.ToolEvent{
		Phase: tool.ToolPhaseStart, ToolName: "glob",
	})
	r.HandleSubagentEvent(application.SubagentEvent{
		Phase: application.SubagentPhaseStart, AgentType: "Explore", Model: "haiku",
	})
	out := buf.String()
	for _, banned := range []string{"INFO ", "WARN ", "2026/", "2025/", "tool=", "agent_id="} {
		if strings.Contains(out, banned) {
			t.Fatalf("forbidden token %q leaked into render: %q", banned, out)
		}
	}
}

// truncateOneLine：边界覆盖（空串、短串、长串、含换行）。
func TestTruncateOneLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"hello", "hello"},
		{"  hello\nworld  ", "hello world"},
		{strings.Repeat("a", 100), strings.Repeat("a", 79) + "…"},
	}
	for _, c := range cases {
		got := truncateOneLine(c.in, 80)
		if got != c.want {
			t.Fatalf("truncateOneLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
