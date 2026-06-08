package shell

// sandbox_display.go —— 沙箱执行过程的美化输出。
//
// 核心职责：
//   1. 识别 bash 工具执行中沙箱包装的命令，折叠冗长的 bwrap 底层参数
//   2. 提取并高亮展示 `-c` 后的实际执行命令主体
//   3. 用清晰的视觉分层展示：[步骤] 状态图标 命令摘要 → 结果
//   4. 错误信息用醒目的红色框强调，不被参数细节淹没
//
// 设计原则：
//   - 默认折叠：只在 verbose 模式或 GOCLAUDE_DEBUG_SANDBOX=1 时展示完整 bwrap 参数
//   - 语义提取：解析 `bash -c "..."` 模式，只显示用户关心的实际命令
//   - 错误突出：fetch failed / error / panic 等关键错误单独高亮展示

import (
	"fmt"
	"strings"
)

// sandboxCommandInfo 从工具摘要中解析出的沙箱命令信息
type sandboxCommandInfo struct {
	// IsSandboxed 是否为沙箱包装的命令
	IsSandboxed bool
	// ActualCommand 实际执行的命令主体（去除沙箱包装后）
	ActualCommand string
	// CommandPreview 命令预览（截断到适合显示的长度）
	CommandPreview string
}

// parseSandboxCommand 从 bash 工具的命令参数中解析沙箱信息。
//
// bwrap 典型格式：
//   bwrap --die-with-parent --unshare-pid ... -- /usr/bin/bash -c "actual command"
//
// 直接 bash 格式：
//   some command here
func parseSandboxCommand(command string, maxPreview int) sandboxCommandInfo {
	if command == "" {
		return sandboxCommandInfo{}
	}

	// 检测是否是通过沙箱执行的命令（bwrap 格式）
	// bwrap 命令的特征：包含 "--die-with-parent" 或 "bwrap" 前缀
	if isBwrapCommand(command) {
		actual := extractBwrapInnerCommand(command)
		if actual != "" {
			return sandboxCommandInfo{
				IsSandboxed:    true,
				ActualCommand:  actual,
				CommandPreview: limitRunes(actual, maxPreview),
			}
		}
	}

	// 非沙箱命令，直接返回
	return sandboxCommandInfo{
		IsSandboxed:    false,
		ActualCommand:  command,
		CommandPreview: limitRunes(command, maxPreview),
	}
}

// isBwrapCommand 判断命令字符串是否看起来像 bwrap 包装的命令
func isBwrapCommand(cmd string) bool {
	// bwrap 命令通常以 "bwrap " 开头，或包含 "--die-with-parent" 等特征参数
	if strings.HasPrefix(cmd, "bwrap ") {
		return true
	}
	// 有时 extractToolSummary 提取的是完整的 bash -c 后面的命令，
	// 判断特征：同时包含 --die-with-parent 和 -- /usr/bin/bash
	if strings.Contains(cmd, "--die-with-parent") && strings.Contains(cmd, "--") {
		return true
	}
	return false
}

// extractBwrapInnerCommand 从 bwrap 完整命令行中提取 `-- /usr/bin/bash -c "..."` 中的实际命令
func extractBwrapInnerCommand(cmd string) string {
	// 寻找 "-- " 分隔符后面的内容
	// bwrap 格式：bwrap [options...] -- /usr/bin/bash -c "command"
	sepIdx := strings.Index(cmd, " -- ")
	if sepIdx < 0 {
		return ""
	}
	after := strings.TrimSpace(cmd[sepIdx+4:])

	// 提取 bash -c 后面的命令
	return extractBashCCommand(after)
}

// extractBashCCommand 从 "bash -c command" 或 "/usr/bin/bash -c command" 中提取实际命令
func extractBashCCommand(s string) string {
	// 去掉 bash/sh 路径前缀
	s = strings.TrimSpace(s)
	for _, prefix := range []string{
		"/usr/bin/bash -c ",
		"/bin/bash -c ",
		"/usr/bin/sh -c ",
		"/bin/sh -c ",
		"bash -c ",
		"sh -c ",
	} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimSpace(s[len(prefix):])
			// 去掉外层引号
			s = strings.TrimPrefix(s, "\"")
			s = strings.TrimSuffix(s, "\"")
			s = strings.TrimPrefix(s, "'")
			s = strings.TrimSuffix(s, "'")
			return strings.TrimSpace(s)
		}
	}
	return s
}

// renderSandboxStepStart 渲染沙箱命令的启动行。
//
// 对于沙箱命令，显示：
//   [N] ◌ bash  🔒 actual_command_here
//
// 对于普通 bash 命令：
//   [N] ◌ bash  command_here
func (r *REPL) renderSandboxStepStart(step int, toolName, command string) string {
	var sb strings.Builder

	// 步骤编号
	sb.WriteString(r.colorize(fmt.Sprintf("  [%d] ", step), colorStepNum))

	// 状态指示器（运行中）
	sb.WriteString(r.colorize("◌ ", colorInfo))

	// 工具名
	sb.WriteString(r.colorize(toolName, colorToolName))

	info := parseSandboxCommand(command, r.termWidth()-30)
	if info.IsSandboxed {
		// 沙箱标记 + 实际命令高亮
		sb.WriteString(r.colorize(" 🔒", colorMuted))
		sb.WriteString("  " + r.colorize(info.CommandPreview, colorSandboxCmd))
	} else if info.CommandPreview != "" {
		sb.WriteString("  " + r.colorize(info.CommandPreview, colorSubtle))
	} else {
		sb.WriteString(r.colorize("  …", colorMuted))
	}

	return sb.String()
}

// renderSandboxResult 渲染沙箱命令执行的结果行。
//
// 成功：      ✓ done  1.2s
// 失败：      ✗ fetch failed: connection timeout  1.2s
//             ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌
//             │ Error: fetch failed
//             │ connection reset by peer
//             ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌
func (r *REPL) renderSandboxError(errText string, maxLines int) {
	if errText == "" || maxLines <= 0 {
		return
	}

	lines := strings.Split(strings.TrimRight(errText, "\n"), "\n")

	// 过滤掉 bwrap 相关的噪音行
	meaningful := filterSandboxNoise(lines)
	if len(meaningful) == 0 {
		return
	}

	shown := len(meaningful)
	truncated := 0
	if shown > maxLines {
		truncated = shown - maxLines
		shown = maxLines
	}

	// 错误框的上边界
	borderLine := strings.Repeat("╌", 40)
	r.writeOut(r.colorize("      "+borderLine, colorErrorBorder) + "\r\n")

	// 错误内容
	for i := 0; i < shown; i++ {
		line := strings.TrimRight(meaningful[i], "\r")
		r.writeOut(r.colorize("      │ ", colorErrorBorder) + r.colorize(line, colorErrOutput) + "\r\n")
	}

	if truncated > 0 {
		r.writeOut(r.colorize(fmt.Sprintf("      │ … +%d lines", truncated), colorMuted) + "\r\n")
	}

	// 错误框的下边界
	r.writeOut(r.colorize("      "+borderLine, colorErrorBorder) + "\r\n")
}

// filterSandboxNoise 过滤掉 bwrap/沙箱相关的噪音输出行，保留有意义的错误信息
func filterSandboxNoise(lines []string) []string {
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// 过滤 bwrap 内部信息
		if strings.HasPrefix(trimmed, "[bwrap]") {
			continue
		}
		if strings.HasPrefix(trimmed, "bwrap:") {
			continue
		}
		// 保留有意义的行
		result = append(result, trimmed)
	}
	return result
}

// isSandboxedBashCommand 判断 bash 工具的命令输入是否经过沙箱执行
// （由 REPL 在工具调用渲染时使用，决定是否显示沙箱标记）
func isSandboxedBashCommand(inputSummary string) bool {
	return isBwrapCommand(inputSummary)
}

// formatSandboxBashSummary 美化 bash 工具的命令摘要显示。
//
// 当检测到沙箱命令格式时，提取实际命令并加 🔒 标记；
// 否则原样返回。
func formatSandboxBashSummary(toolName, summary string, maxRunes int) (formatted string, sandboxed bool) {
	if toolName != "bash" && toolName != "shell" && toolName != "exec" &&
		toolName != "run" && toolName != "execute_command" {
		return summary, false
	}

	info := parseSandboxCommand(summary, maxRunes)
	if info.IsSandboxed {
		return info.CommandPreview, true
	}
	return summary, false
}
