package shell

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/yaoice/goclaude/pkg/domain/query"
)

// 本文件聚合 REPL 的终端渲染原语：prompt 构造、banner、助手头部、
// 颜色常量与 colorize 包装、消息预览。
//
// 配色哲学：
//   - 主色调：品蓝(38;5;75) + 青绿(38;5;78) 营造科技感
//   - 成功：翠绿(38;5;78)  失败：珊瑚红(38;5;203)  警告：琥珀(38;5;214)
//   - 主文字：亮白(1;37)  次文字：暗灰(38;5;245)  最弱：(38;5;239)
//   - 装饰性元素：使用 256 色中间色调，避免过亮刺眼

func (r *REPL) makePrompt() string {
	if !r.useColor {
		return "> "
	}
	tag := ""
	switch r.PermissionMode {
	case "acceptEdits":
		tag = colorWarn + "✎" + colorReset + " "
	case "bypass":
		tag = colorError + "🙋" + colorReset + " "
	case "plan":
		tag = colorMagenta + "◇" + colorReset + " "
	}
	return tag + colorPrompt + "❯" + colorReset + " "
}

func (r *REPL) printBanner() {
	var sb strings.Builder

	// 顶部分隔线
	sb.WriteString(r.colorize("  ┌─────────────────────────────────────────────────\r\n", colorBorder))

	// 标题
	sb.WriteString(r.colorize("  │ ", colorBorder))
	sb.WriteString(r.colorize("◆ GoClaude", colorBrand))
	sb.WriteString(r.colorize("  Interactive Agent Shell\r\n", colorSubtle))

	// 分隔线
	sb.WriteString(r.colorize("  ├─────────────────────────────────────────────────\r\n", colorBorder))

	// 信息行
	sb.WriteString(r.colorize("  │ ", colorBorder))
	sb.WriteString(r.colorize("  provider ", colorLabel))
	sb.WriteString(r.colorize(r.Provider, colorValue))
	sb.WriteString(r.colorize("  ·  model ", colorLabel))
	sb.WriteString(r.colorize(r.Model, colorValue) + "\r\n")

	sb.WriteString(r.colorize("  │ ", colorBorder))
	sb.WriteString(r.colorize("  cwd      ", colorLabel))
	sb.WriteString(r.colorize(r.WorkDir, colorValue) + "\r\n")

	if branch := gitBranch(r.WorkDir); branch != "" {
		sb.WriteString(r.colorize("  │ ", colorBorder))
		sb.WriteString(r.colorize("  branch   ", colorLabel))
		sb.WriteString(r.colorize(branch, colorBranchName) + "\r\n")
	}

	// 权限模式
	sb.WriteString(r.colorize("  │ ", colorBorder))
	sb.WriteString(r.colorize("  perms    ", colorLabel))
	switch r.PermissionMode {
	case "", "default":
		sb.WriteString(r.colorize("default", colorValue))
		sb.WriteString(r.colorize("  Shift-Tab to cycle", colorMuted) + "\r\n")
	case "bypass":
		sb.WriteString(r.colorize("bypass", colorWarn))
		sb.WriteString(r.colorize("  ⚠ all tools auto-approved", colorWarn) + "\r\n")
	default:
		sb.WriteString(r.colorize(r.PermissionMode, colorValue) + "\r\n")
	}

	// 底部分隔线
	sb.WriteString(r.colorize("  └─────────────────────────────────────────────────\r\n", colorBorder))

	// 快捷键提示
	sb.WriteString(r.colorize("    /help", colorHint) + r.colorize(" 命令  ", colorMuted))
	sb.WriteString(r.colorize("!", colorHint) + r.colorize(" shell  ", colorMuted))
	sb.WriteString(r.colorize("Esc", colorHint) + r.colorize(" 取消  ", colorMuted))
	sb.WriteString(r.colorize("Ctrl+D", colorHint) + r.colorize(" 退出", colorMuted))
	sb.WriteString("\r\n\r\n")

	r.writeOut(sb.String())
}

// gitBranch 返回当前 git 分支；不在仓库或失败时返回 ""
func gitBranch(cwd string) string {
	cmd := exec.Command("git", "-C", cwd, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(out))
	if s == "HEAD" {
		c := exec.Command("git", "-C", cwd, "rev-parse", "--short", "HEAD")
		o, err := c.Output()
		if err != nil {
			return ""
		}
		return "(" + strings.TrimSpace(string(o)) + ")"
	}
	return s
}

func (r *REPL) printAssistantHeader() {
	r.writeOut("\r\n")
	r.writeOut(r.colorize("🤖 ", colorBrand) + r.colorize("GoClaude", colorAssistantName) + "\r\n")
}

func (r *REPL) writeOut(s string) {
	_, _ = os.Stdout.Write([]byte(s))
	_ = os.Stdout.Sync()
}

// ────────────────── 色彩系统（256 色 + 基础 SGR）──────────────────
//
// 分层设计：语义色名 → ANSI 转义序列。修改配色只需改这里。
const (
	// 基础控制
	colorReset = "\x1b[0m"
	colorBold  = "\x1b[1m"

	// 品牌色 / 主色调
	colorBrand  = "\x1b[1;38;5;75m" // 亮品蓝（标题/Logo）
	colorPrompt = "\x1b[1;38;5;75m" // 提示符

	// 语义色
	colorSuccess = "\x1b[38;5;78m"  // 翠绿：成功
	colorError   = "\x1b[38;5;203m" // 珊瑚红：错误/失败
	colorWarn    = "\x1b[38;5;214m" // 琥珀：警告
	colorInfo    = "\x1b[38;5;75m"  // 品蓝：信息

	// 文字层级
	colorPrimary = "\x1b[1;37m"     // 主文字（亮白粗体）
	colorValue   = "\x1b[37m"       // 值文字（白色）
	colorSubtle  = "\x1b[38;5;245m" // 次要文字
	colorMuted   = "\x1b[38;5;239m" // 最弱文字
	colorLabel   = "\x1b[38;5;245m" // 标签（暗灰）

	// 装饰/结构
	colorBorder     = "\x1b[38;5;240m" // 边框线
	colorRail       = "\x1b[38;5;240m" // 竖线导轨
	colorHint       = "\x1b[38;5;75m"  // 快捷键高亮
	colorBranchName = "\x1b[38;5;114m" // Git 分支名（草绿）
	colorMagenta    = "\x1b[38;5;177m" // 紫色

	// 工具执行相关
	colorToolName   = "\x1b[1;38;5;75m" // 工具名（粗品蓝）
	colorToolStatus = "\x1b[38;5;78m"   // 工具状态（成功绿）
	colorElapsed    = "\x1b[38;5;245m"  // 耗时数字
	colorStepNum    = "\x1b[38;5;75m"   // 步骤编号
	colorOutput     = "\x1b[38;5;252m"  // 标准输出内容
	colorErrOutput  = "\x1b[38;5;203m"  // 错误输出内容

	// 沙箱执行相关
	colorSandboxCmd  = "\x1b[1;38;5;255m" // 沙箱内实际命令（亮白粗体，醒目）
	colorSandboxTag  = "\x1b[38;5;245m"   // 沙箱标记（暗灰不干扰）
	colorErrorBorder = "\x1b[38;5;203m"   // 错误框边界（珊瑚红）
	colorStreaming   = "\x1b[38;5;252m"   // 流式实时输出（浅色）

	// 助手相关
	colorAssistantName = "\x1b[1;38;5;255m" // 助手名（亮白粗体）

	// 兼容旧代码的别名（逐步迁移）
	colorDim    = "\x1b[38;5;245m"
	colorRed    = "\x1b[38;5;203m"
	colorGreen  = "\x1b[38;5;78m"
	colorYellow = "\x1b[38;5;214m"
	colorCyan   = "\x1b[38;5;75m"
)

func (r *REPL) colorize(s, color string) string {
	if !r.useColor {
		return s
	}
	return color + s + colorReset
}

// ────────────────── 工具执行实时明细输出 ──────────────────

// renderToolOutputVerbose 展示工具输出的详细内容，区分标准输出和错误输出。
//
// 标准输出使用浅色，错误输出使用红色高亮，每行带编号，视觉边条区分层级。
func (r *REPL) renderToolOutputVerbose(text, lineColor string, maxLines int) {
	if text == "" || maxLines <= 0 {
		return
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	shown := len(lines)
	truncated := 0
	if shown > maxLines {
		truncated = shown - maxLines
		shown = maxLines
	}

	isErr := lineColor == colorRed || lineColor == colorError || lineColor == colorErrOutput
	rail := r.colorize("      "+r.gl().panelRail+" ", colorRail)

	for i := 0; i < shown; i++ {
		l := strings.TrimRight(lines[i], "\r")
		lineNum := r.colorize(fmt.Sprintf("%2d", i+1), colorMuted)
		if isErr {
			r.writeOut(rail + lineNum + " " + r.colorize(l, colorErrOutput) + "\r\n")
		} else {
			r.writeOut(rail + lineNum + " " + r.colorize(l, colorOutput) + "\r\n")
		}
	}
	if truncated > 0 {
		more := fmt.Sprintf("      %s  ▸ %d lines hidden · use --verbose for full output", r.gl().panelRail, truncated)
		r.writeOut(r.colorize(more, colorMuted) + "\r\n")
	}
}

// renderToolStep 渲染一个工具执行步骤的实时状态行。
//
// 格式：  [步骤] ● 工具名  摘要  ─── 状态  耗时
func (r *REPL) renderToolStep(step int, toolName, summary, status string, elapsed time.Duration, isErr bool) string {
	var sb strings.Builder

	// 步骤编号
	if step > 0 {
		sb.WriteString(r.colorize(fmt.Sprintf("  [%d] ", step), colorStepNum))
	} else {
		sb.WriteString("  ")
	}

	// 状态指示器
	if isErr {
		sb.WriteString(r.colorize("✗ ", colorError))
	} else if status == "running" {
		sb.WriteString(r.colorize("◌ ", colorInfo))
	} else {
		sb.WriteString(r.colorize("✓ ", colorSuccess))
	}

	// 工具名
	sb.WriteString(r.colorize(toolName, colorToolName))

	// 摘要
	if summary != "" {
		sb.WriteString("  " + r.colorize(summary, colorSubtle))
	}

	// 耗时
	if elapsed > 0 {
		sb.WriteString("  " + r.colorize(formatElapsedCompact(elapsed), colorElapsed))
	}

	return sb.String()
}

// formatElapsedCompact 紧凑耗时格式（无前导空格）
func formatElapsedCompact(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%.1fm", d.Minutes())
}

func previewMessage(m query.Message) string {
	var sb strings.Builder
	for _, b := range m.Content {
		switch b.Type {
		case query.ContentTypeText:
			sb.WriteString(b.Text)
		case query.ContentTypeToolUse:
			fmt.Fprintf(&sb, "[tool_use %s]", b.ToolName)
		case query.ContentTypeToolResult:
			sb.WriteString("[tool_result]")
		}
	}
	s := sb.String()
	if len(s) > 80 {
		s = s[:77] + "..."
	}
	return s
}
