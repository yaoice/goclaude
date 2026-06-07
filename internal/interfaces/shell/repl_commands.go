package shell

import (
	"context"
	"fmt"
	"strings"
)

// 本文件聚合 REPL 的本地 slash 命令分发（handleLocalCommand）及其辅助。
// 从 repl.go 拆出以提升可读性；逻辑保持不变。

// handleLocalCommand 处理 / 开头的本地命令
//
// 返回值：
//   - exit:           true → 退出 shell
//   - expandedPrompt: 非空 → 调用方应把它作为 user prompt 发给 LLM
//     （用于自定义 prompt-类命令）
func (r *REPL) handleLocalCommand(line string) (exit bool, expandedPrompt string) {
	rest := strings.TrimSpace(line)
	cmd := rest
	args := ""
	if i := strings.IndexAny(rest, " \t"); i > 0 {
		cmd = rest[:i]
		args = strings.TrimSpace(rest[i+1:])
	}
	switch cmd {
	case "/exit", "/quit", "/q":
		r.writeOut("再见！\r\n")
		return true, ""
	case "/help", "/?":
		// 整块渲染交给 printHelp：包含 builtin / custom / shortcuts 三大节，
		// 风格对齐 src/components/HelpV2 的 Tabs 信息架构。
		r.printHelp()
	case "/clear", "/reset":
		r.mu.Lock()
		n := len(r.messages)
		r.messages = r.messages[:0]
		r.mu.Unlock()
		r.writeOut(r.colorize(fmt.Sprintf("（已清空 %d 条对话历史）\r\n", n), colorDim))
	case "/redraw":
		r.writeOut("\x1b[2J\x1b[H")
		r.printBanner()
	case "/history":
		if r.History != nil {
			for i, h := range r.History.Snapshot() {
				r.writeOut(fmt.Sprintf("%4d  %s\r\n", i+1, h))
			}
		}
	case "/messages":
		r.mu.Lock()
		for i, m := range r.messages {
			preview := previewMessage(m)
			r.writeOut(fmt.Sprintf("  [%d] %s: %s\r\n", i, m.Role, preview))
		}
		r.mu.Unlock()
	case "/model":
		if args != "" {
			r.Model = args
			r.writeOut(r.colorize(fmt.Sprintf("（模型切换为 %s 仅当前会话标识，引擎内模型不变；如需切换请重启）\r\n", r.Model), colorDim))
		} else {
			r.writeOut(fmt.Sprintf("当前模型：%s\r\n", r.Model))
		}
	case "/pwd":
		r.writeOut(r.WorkDir + "\r\n")
	case "/permissions":
		r.printPermissionsOverview()
	case "/env":
		// 列出关键 env + 来源（dotenv/settingsenv/shell）。
		// 不打印任何值（仅 set/unset + 路径），避免 API key 泄漏到截图/日志。
		r.printEnvOverview()
	case "/cost", "/usage":
		r.handleCostCmd()
	case "/compact":
		r.writeOut(r.colorize(
			"（auto-compact 由引擎托管；当前会话不支持手动触发）\r\n", colorDim))
	case "/skills":
		// 无参数 → 启动 SkillsDialog（与 src `/skills` 行为对齐）
		// 带参数 → 走原 readline 输出，便于脚本/调试（go 端独有）
		if args == "" {
			r.ShowSkillsDialog()
		} else {
			r.handleSkillsCmd(strings.Fields(args))
		}
	case "/agents":
		if args == "" {
			r.ShowAgentsDialog()
		} else {
			r.handleAgentsCmd(strings.Fields(args))
		}
	case "/mcp":
		if args == "" {
			r.ShowMcpDialog()
		} else {
			r.handleMcpCmd(strings.Fields(args))
		}
	case "/tools":
		r.handleToolsCmd(strings.Fields(args))
	case "/teams":
		r.handleTeamsCmd(strings.Fields(args))
	default:
		// 尝试自定义 prompt-类命令
		if r.CustomCommands != nil {
			if cc, ok := r.CustomCommands.Get(strings.TrimPrefix(cmd, "/")); ok {
				expanded := cc.Render(args)
				if strings.TrimSpace(expanded) == "" {
					r.writeOut(r.colorize(
						fmt.Sprintf("（自定义命令 %s 渲染结果为空）\r\n", cmd), colorYellow))
					return false, ""
				}
				return false, expanded
			}
		}
		r.writeOut(r.colorize(fmt.Sprintf("未知命令 %s（输入 /help 查看）\r\n", cmd), colorYellow))
	}
	return false, ""
}

// handleCostCmd 显示当前会话的 token 统计（/cost / /usage）
func (r *REPL) handleCostCmd() {
	r.mu.Lock()
	n := len(r.messages)
	r.mu.Unlock()
	r.writeOut(r.colorize(fmt.Sprintf("会话消息数：%d\r\n", n), colorCyan))
	r.writeOut(r.colorize(
		"（详细 token / cost 由 Engine 在 verbose 模式下输出；用 -v 启动可见每轮统计）\r\n",
		colorDim))
}

// ----------------- 带参子命令（readline 文本模式） -----------------
//
// 这些是 /skills /agents /mcp /tools 的"带参数"分支：与全屏 Dialog（Show*Dialog）
// 互补，便于脚本/调试时以纯文本查看详情。无参时由 handleLocalCommand 路由到
// 对应的全屏 Dialog；带参时落到这里。
//
// 输出风格与包内其它渲染保持一致：未启用对应服务时给一行 dim/yellow 提示；
// 列表项用 "  name  描述（截断）" 两列；详情体把 \n 规范成 \r\n（原始模式终端）。

// handleSkillsCmd 处理 `/skills <name>`：渲染指定 skill 的正文。
func (r *REPL) handleSkillsCmd(args []string) { r.writeOut(r.renderSkillsCmd(args)) }

// renderSkillsCmd 是 handleSkillsCmd 的纯函数实现，便于单测。
func (r *REPL) renderSkillsCmd(args []string) string {
	if r.Skills == nil {
		return r.colorize("（skill 服务未启用）\r\n", colorYellow)
	}
	var sb strings.Builder
	if len(args) == 0 {
		for _, s := range r.Skills.List() {
			desc := s.Description
			if desc == "" {
				desc = s.WhenToUse
			}
			sb.WriteString("  " + r.colorize(s.Name, colorCyan) + "  " +
				r.colorize(truncOneLine(desc, 60), colorDim) + "\r\n")
		}
		return sb.String()
	}
	name := args[0]
	if body, ok := r.Skills.Render(name); ok {
		return strings.ReplaceAll(strings.TrimRight(body, "\n"), "\n", "\r\n") + "\r\n"
	}
	return r.colorize(fmt.Sprintf("未找到 skill：%s\r\n", name), colorYellow)
}

// handleAgentsCmd 处理 `/agents <type>`：显示指定 agent 的元信息与 system prompt。
func (r *REPL) handleAgentsCmd(args []string) { r.writeOut(r.renderAgentsCmd(args)) }

// renderAgentsCmd 是 handleAgentsCmd 的纯函数实现，便于单测。
func (r *REPL) renderAgentsCmd(args []string) string {
	if r.Agents == nil {
		return r.colorize("（agent 服务未启用）\r\n", colorYellow)
	}
	var sb strings.Builder
	if len(args) == 0 {
		for _, a := range r.Agents.List() {
			sb.WriteString("  " + r.colorize(a.AgentType, colorCyan) + "  " +
				r.colorize(truncOneLine(a.WhenToUse, 60), colorDim) + "\r\n")
		}
		return sb.String()
	}
	t := args[0]
	a, ok := r.Agents.Get(t)
	if !ok {
		return r.colorize(fmt.Sprintf("未找到 agent：%s\r\n", t), colorYellow)
	}
	sb.WriteString(r.colorize(fmt.Sprintf("agent: %s\r\n", a.AgentType), colorCyan))
	if a.Model != "" {
		sb.WriteString(r.colorize(fmt.Sprintf("model: %s\r\n", a.Model), colorDim))
	}
	if len(a.Tools) > 0 {
		sb.WriteString(r.colorize("tools: "+strings.Join(a.Tools, ", ")+"\r\n", colorDim))
	}
	if a.WhenToUse != "" {
		sb.WriteString(r.colorize("when:  "+truncOneLine(a.WhenToUse, 100)+"\r\n", colorDim))
	}
	if a.SystemPrompt != "" {
		sb.WriteString("\r\n" + strings.ReplaceAll(strings.TrimRight(a.SystemPrompt, "\n"), "\n", "\r\n") + "\r\n")
	}
	return sb.String()
}

// handleMcpCmd 处理 `/mcp tools|status`：列出 MCP 工具或服务器连接状态。
func (r *REPL) handleMcpCmd(args []string) { r.writeOut(r.renderMcpCmd(args)) }

// renderMcpCmd 是 handleMcpCmd 的纯函数实现，便于单测。
func (r *REPL) renderMcpCmd(args []string) string {
	if r.MCP == nil {
		return r.colorize("（MCP 未启用）\r\n", colorYellow)
	}
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}
	var sb strings.Builder
	switch sub {
	case "tools":
		tools, err := r.MCP.Tools(context.Background())
		if err != nil {
			return r.colorize(fmt.Sprintf("获取 MCP 工具失败：%v\r\n", err), colorRed)
		}
		if len(tools) == 0 {
			return r.colorize("（无 MCP 工具）\r\n", colorDim)
		}
		for _, t := range tools {
			sb.WriteString("  " + r.colorize(t.Name, colorCyan) + "  " +
				r.colorize(truncOneLine(t.Description, 60), colorDim) + "\r\n")
		}
	default: // "status" 及其它
		statuses := r.MCP.Statuses()
		if len(statuses) == 0 {
			return r.colorize("（未配置 MCP 服务器）\r\n", colorDim)
		}
		for _, s := range statuses {
			state := r.colorize("connected", colorGreen)
			if !s.Connected {
				state = r.colorize("disconnected", colorRed)
				if s.Error != "" {
					state += r.colorize("  "+truncOneLine(s.Error, 60), colorDim)
				}
			}
			sb.WriteString("  " + s.Name + "  " + state + "\r\n")
		}
	}
	return sb.String()
}

// handleToolsCmd 处理 `/tools [name]`：无参列出所有已注册工具；带名显示其描述/schema。
func (r *REPL) handleToolsCmd(args []string) { r.writeOut(r.renderToolsCmd(args)) }

// renderToolsCmd 是 handleToolsCmd 的纯函数实现，便于单测。
func (r *REPL) renderToolsCmd(args []string) string {
	if r.Tools == nil {
		return r.colorize("（工具注册表未注入）\r\n", colorYellow)
	}
	var sb strings.Builder
	if len(args) == 0 {
		for _, name := range r.Tools.Names() {
			desc := ""
			if info, ok := r.Tools.Describe(name); ok {
				desc = info.Description
			}
			sb.WriteString("  " + r.colorize(name, colorCyan) + "  " +
				r.colorize(truncOneLine(desc, 60), colorDim) + "\r\n")
		}
		return sb.String()
	}
	name := args[0]
	info, ok := r.Tools.Describe(name)
	if !ok {
		return r.colorize(fmt.Sprintf("未找到 tool：%s\r\n", name), colorYellow)
	}
	sb.WriteString(r.colorize(fmt.Sprintf("tool: %s\r\n", info.Name), colorCyan))
	if info.Description != "" {
		sb.WriteString(strings.ReplaceAll(strings.TrimRight(info.Description, "\n"), "\n", "\r\n") + "\r\n")
	}
	return sb.String()
}

// handleTeamsCmd 处理 `/teams [name]`：无参列出所有 teams；带名显示指定 team 详情。
func (r *REPL) handleTeamsCmd(args []string) { r.writeOut(r.renderTeamsCmd(args)) }

// renderTeamsCmd 是 handleTeamsCmd 的纯函数实现，便于单测。
func (r *REPL) renderTeamsCmd(args []string) string {
	if r.Teams == nil {
		return r.colorize("（team 服务未启用）\r\n", colorYellow)
	}
	var sb strings.Builder
	if len(args) == 0 {
		teams := r.Teams.List()
		if len(teams) == 0 {
			return r.colorize("（暂无团队）\r\n", colorDim)
		}
		for _, t := range teams {
			info := fmt.Sprintf("%d members", t.MemberCount)
			if t.TaskCount > 0 {
				info += fmt.Sprintf(" · %d tasks", t.TaskCount)
			}
			sb.WriteString("  " + r.colorize(t.Name, colorCyan) + "  " +
				r.colorize(info, colorDim) + "\r\n")
		}
		return sb.String()
	}
	name := args[0]
	t, ok := r.Teams.Get(name)
	if !ok {
		return r.colorize(fmt.Sprintf("未找到 team：%s\r\n", name), colorYellow)
	}
	sb.WriteString(r.colorize(fmt.Sprintf("team:        %s\r\n", t.Name), colorCyan))
	if t.Description != "" {
		sb.WriteString(r.colorize(fmt.Sprintf("description: %s\r\n", t.Description), colorDim))
	}
	sb.WriteString(r.colorize(fmt.Sprintf("members:     %d\r\n", t.MemberCount), colorDim))
	sb.WriteString(r.colorize(fmt.Sprintf("tasks:       %d\r\n", t.TaskCount), colorDim))
	return sb.String()
}
