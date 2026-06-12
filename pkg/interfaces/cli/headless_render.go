// Package cli - headless_render.go 为非交互式（`goclaude run`）路径提供
// 美观的工具/subagent 进度渲染。
//
// 输出走 stderr，与主输出内容物理分离，颜色自适应 TTY。
//
// 渲染风格：
//
//	[1] ◌ web_fetch  https://example.com
//	[1] ✓ web_fetch  42ms
//	┏━ coder · sonnet ━━━━━━━━━━━━━━━━━
//	┃  Code Generation
//	┗━ ✓ done · 1.2s · 5 steps ━━━━━━━━
package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yaoice/goclaude/pkg/application"
	"github.com/yaoice/goclaude/pkg/domain/tool"
)

var (
	_ tool.ToolEventListener            = (*headlessRender)(nil)
	_ application.SubagentEventListener = (*headlessRender)(nil)
)

// headlessRender 进度渲染器（goclaude run 用）。
type headlessRender struct {
	out     io.Writer
	color   bool
	mu      sync.Mutex
	stepSeq int // 全局工具步骤计数器
}

// newHeadlessRender 创建渲染器；out=nil 取 os.Stderr。
func newHeadlessRender(out io.Writer) *headlessRender {
	if out == nil {
		out = os.Stderr
	}
	return &headlessRender{
		out:   out,
		color: isStderrTTY() && os.Getenv("NO_COLOR") == "",
	}
}

// HandleToolEvent 渲染工具执行实时明细。
//
// 风格：
//
//	[N] ◌ <tool_name>  <input_summary>           # start：步骤+工具名+摘要
//	[N] ◌ bash  🔒 <actual_command>              # start：沙箱命令（折叠 bwrap 参数）
//	[N] ✓ <tool_name>  <elapsed>                 # finish 成功
//	[N] ✗ <tool_name>  <elapsed>  <error_msg>    # finish 失败（红色高亮）
func (h *headlessRender) HandleToolEvent(ev tool.ToolEvent) {
	if h == nil {
		return
	}
	switch ev.Phase {
	case tool.ToolPhaseStart:
		h.mu.Lock()
		h.stepSeq++
		step := h.stepSeq
		h.mu.Unlock()

		stepLabel := h.tint(fmt.Sprintf("  [%d] ", step), prettyStepNum)
		line := stepLabel +
			h.tint("◌ ", prettyCyan) +
			h.tint(ev.ToolName, prettyToolName)

		// 沙箱命令美化：折叠 bwrap 参数，高亮实际命令
		summary := ev.InputSummary
		sandboxed := false
		if isBashToolName(ev.ToolName) && summary != "" {
			if formatted, ok := formatHeadlessSandboxSummary(summary); ok {
				summary = formatted
				sandboxed = true
			}
		}
		if sandboxed {
			line += h.tint(" 🔒", prettyDim)
		}
		if summary != "" {
			summaryColor := prettyDim
			if sandboxed {
				summaryColor = prettyBright
			}
			line += "  " + h.tint(summary, summaryColor)
		}
		h.writeLine(line)

	case tool.ToolPhaseFinish:
		h.mu.Lock()
		step := h.stepSeq
		h.mu.Unlock()

		stepLabel := h.tint(fmt.Sprintf("  [%d] ", step), prettyStepNum)
		var mark, color, status string
		if ev.Status == tool.ToolStatusError {
			mark, color, status = "✗", prettyRed, ev.ToolName
		} else {
			mark, color, status = "✓", prettyGreen, ev.ToolName
		}
		line := stepLabel +
			h.tint(mark+" ", color) +
			h.tint(status, prettyToolName) +
			"  " + h.tint(formatHeadlessElapsed(ev.Elapsed), prettyElapsed)
		if ev.Status == tool.ToolStatusError && ev.ErrorMessage != "" {
			// 过滤 bwrap 噪音，提取有意义的错误信息
			errSummary := extractMeaningfulError(ev.ErrorMessage, 70)
			line += "  " + h.tint(errSummary, prettyRed)
		}
		h.writeLine(line)
	}
}

// isBashToolName 判断工具名是否为 bash 类执行工具
func isBashToolName(name string) bool {
	switch strings.ToLower(name) {
	case "bash", "shell", "exec", "run", "execute_command":
		return true
	}
	return false
}

// formatHeadlessSandboxSummary 美化沙箱命令的摘要显示（headless 模式）。
// 检测 bwrap 命令格式，提取实际执行的命令。
func formatHeadlessSandboxSummary(summary string) (string, bool) {
	if !isBwrapLikeCommand(summary) {
		return summary, false
	}
	actual := extractInnerCommand(summary)
	if actual != "" {
		return truncateOneLine(actual, 80), true
	}
	return summary, false
}

// isBwrapLikeCommand 检测命令字符串是否包含 bwrap 沙箱特征
func isBwrapLikeCommand(cmd string) bool {
	if strings.HasPrefix(cmd, "bwrap ") {
		return true
	}
	if strings.Contains(cmd, "--die-with-parent") && strings.Contains(cmd, " -- ") {
		return true
	}
	return false
}

// extractInnerCommand 从 bwrap 命令中提取内部实际命令
func extractInnerCommand(cmd string) string {
	sepIdx := strings.Index(cmd, " -- ")
	if sepIdx < 0 {
		return ""
	}
	after := strings.TrimSpace(cmd[sepIdx+4:])

	// 去掉 bash -c 前缀
	for _, prefix := range []string{
		"/usr/bin/bash -c ",
		"/bin/bash -c ",
		"bash -c ",
	} {
		if strings.HasPrefix(after, prefix) {
			result := strings.TrimSpace(after[len(prefix):])
			result = strings.TrimPrefix(result, "\"")
			result = strings.TrimSuffix(result, "\"")
			result = strings.TrimPrefix(result, "'")
			result = strings.TrimSuffix(result, "'")
			return strings.TrimSpace(result)
		}
	}
	return after
}

// extractMeaningfulError 从错误信息中提取有意义的部分，过滤 bwrap 噪音
func extractMeaningfulError(s string, max int) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")

	// 过滤 bwrap 内部噪音行
	var meaningful []string
	for _, l := range lines {
		tl := strings.TrimSpace(l)
		if tl == "" || strings.HasPrefix(tl, "[bwrap]") || strings.HasPrefix(tl, "bwrap:") {
			continue
		}
		meaningful = append(meaningful, tl)
	}

	if len(meaningful) == 0 {
		return extractErrorLine(s, max)
	}

	// 找包含错误关键字的行
	for _, l := range meaningful {
		low := strings.ToLower(l)
		if strings.Contains(low, "error") ||
			strings.Contains(low, "fatal") ||
			strings.Contains(low, "panic") ||
			strings.Contains(low, "failed") {
			return truncateOneLine(l, max)
		}
	}
	return truncateOneLine(meaningful[0], max)
}

// HandleSubagentEvent 渲染 subagent 生命周期。
//
// 风格：
//
//	┏━ <type> · <model> ━━━━━━━━━━━━━━━━━
//	┃  <phase>
//	┗━ ✓ done · 1.2s · N steps ━━━━━━━━━
func (h *headlessRender) HandleSubagentEvent(ev application.SubagentEvent) {
	if h == nil {
		return
	}
	model := ev.Model
	if model == "" {
		model = "(inherit)"
	}
	switch ev.Phase {
	case application.SubagentPhaseStart:
		line := h.tint("  ┏━ ", prettyBorder) +
			h.tint(ev.AgentType, prettyToolName) +
			h.tint(" · "+model, prettyDim) +
			" " + h.tint(strings.Repeat("━", 30), prettyBorder)
		h.writeLine(line)

	case application.SubagentPhaseProgress:
		line := h.tint("  ┃  ", prettyBorder) +
			h.tint(ev.AgentType, prettyCyan)
		if ev.Turns > 0 {
			line += h.tint(fmt.Sprintf("  turn %d", ev.Turns), prettyDim)
		}
		if ev.LastTool != "" {
			line += "  " + h.tint(ev.LastTool, prettyDim)
		}
		if ev.LastToolDetail != "" {
			line += "  " + h.tint(ev.LastToolDetail, prettyDim)
		}
		h.writeLine(line)

	case application.SubagentPhaseFinish:
		var mark, color string
		if ev.Status == application.SubagentStatusError {
			mark, color = "✗ failed", prettyRed
		} else {
			mark, color = "✓ done", prettyGreen
		}
		line := h.tint("  ┗━ ", color) +
			h.tint(mark, color)
		if ev.Elapsed > 0 {
			line += h.tint(" · "+formatHeadlessElapsed(ev.Elapsed), prettyElapsed)
		}
		if ev.Turns > 0 {
			line += h.tint(fmt.Sprintf(" · %d steps", ev.Turns), prettyDim)
		}
		if ev.Status == application.SubagentStatusError && ev.ErrorMessage != "" {
			line += "  " + h.tint(truncateOneLine(ev.ErrorMessage, 60), prettyRed)
		}
		if ev.Status == application.SubagentStatusSuccess && ev.ResultPreview != "" {
			line += "  " + h.tint(ev.ResultPreview, prettyDim)
		}
		line += " " + h.tint(strings.Repeat("━", 15), color)
		h.writeLine(line)
	}
}

// 色彩常量集中定义在 prettylog.go 中（同包共享），此处不再重复定义。

func (h *headlessRender) tint(s, color string) string {
	if !h.color || color == "" {
		return s
	}
	return color + s + prettyReset
}

func (h *headlessRender) writeLine(s string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, _ = io.WriteString(h.out, s+"\r\n")
}

// formatHeadlessElapsed 耗时格式化。
func formatHeadlessElapsed(d time.Duration) string {
	switch {
	case d <= 0:
		return "-"
	case d < time.Millisecond:
		return fmt.Sprintf("%dµs", d.Microseconds())
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return fmt.Sprintf("%.1fm", d.Minutes())
	}
}

// truncateOneLine 把多行文本压成单行并截到指定长度。
func truncateOneLine(s string, max int) string {
	if max <= 0 {
		return ""
	}
	out := make([]rune, 0, len(s))
	prevSpace := false
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			r = ' '
		}
		if r == ' ' {
			if prevSpace {
				continue
			}
			prevSpace = true
		} else {
			prevSpace = false
		}
		out = append(out, r)
	}
	trimmed := []rune(string(out))
	start, end := 0, len(trimmed)
	for start < end && trimmed[start] == ' ' {
		start++
	}
	for end > start && trimmed[end-1] == ' ' {
		end--
	}
	trimmed = trimmed[start:end]
	if len(trimmed) <= max {
		return string(trimmed)
	}
	if max <= 3 {
		return string(trimmed[:max])
	}
	return string(trimmed[:max-1]) + "…"
}

// extractErrorLine 提取错误文本中最具代表性的一行。
func extractErrorLine(s string, max int) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	best := lines[0]
	for _, l := range lines {
		tl := strings.TrimSpace(l)
		if tl == "" {
			continue
		}
		low := strings.ToLower(tl)
		if strings.Contains(low, "error") ||
			strings.Contains(low, "fatal") ||
			strings.Contains(low, "panic") ||
			strings.Contains(low, "failed") {
			best = tl
			break
		}
	}
	return truncateOneLine(best, max)
}
