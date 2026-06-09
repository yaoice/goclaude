package shell

import (
	"context"
	"fmt"
	"strings"

	memory "github.com/anthropics/goclaude/pkg/domain/memory"
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
	case "/workspace":
		r.handleWorkspaceCmd(args)
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
	case "/workflow", "/workflows":
		r.handleWorkflowCmd(strings.Fields(args))
	case "/remember":
		// /remember <内容>：将用户输入直接持久化写入 MEMORY.md。
		// 无参数时提示用法；有参数时提取第一句作为标题，完整内容作为正文写入。
		r.writeOut(r.handleRememberCmd(args))
	case "/memory":
		r.writeOut(r.handleMemoryCmd(args))
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

// handleWorkspaceCmd 处理 /workspace 命令。
//
// 无参数：显示当前 workspace 路径。
// 带参数：动态切换到指定路径（支持绝对、~/、相对路径）。
func (r *REPL) handleWorkspaceCmd(args string) {
	args = strings.TrimSpace(args)
	if args == "" {
		// 显示当前路径：回调返回空值表示查看诉求
		if r.OnWorkspaceSet != nil {
			if resolved, _ := r.OnWorkspaceSet(""); resolved != "" {
				r.writeOut(r.colorize(fmt.Sprintf("workspace: %s\r\n", resolved), colorCyan))
				r.writeOut(r.colorize("  设置新路径: /workspace <path>\r\n", colorDim))
				return
			}
		}
		r.writeOut(r.colorize("用法: /workspace <path>  — 设置产物输出目录\r\n", colorCyan))
		r.writeOut(r.colorize("支持绝对路径、~/ 和相对路径。路径不存在时自动创建。\r\n", colorDim))
		r.writeOut(r.colorize("示例: /workspace /tmp/my-output   /workspace ~/projects/out\r\n", colorDim))
		r.writeOut(r.colorize("不带参数: /workspace  — 显示当前路径\r\n", colorDim))
		return
	}

	if r.OnWorkspaceSet == nil {
		r.writeOut(r.colorize("（workspace 动态切换未启用；请通过 --workspace flag 在启动时指定）\r\n", colorYellow))
		return
	}

	resolved, err := r.OnWorkspaceSet(args)
	if err != nil {
		r.writeOut(r.colorize(fmt.Sprintf("设置 workspace 失败: %v\r\n", err), colorRed))
		return
	}
	r.writeOut(r.colorize(fmt.Sprintf("✓ workspace 已设置为: %s\r\n", resolved), colorGreen))
	r.writeOut(r.colorize("  后续所有文件输出将写入此目录\r\n", colorDim))
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

// buildRememberPrompt 构建 /remember 指令的 pre-built prompt。
//
// 对齐上游 src/skills/bundled/remember.ts 的核心逻辑：
//   - 无参数：AI 扫描所有记忆层并生成分类报告（Promotions / Cleanup / Ambiguous / No action）
//   - 带参数：作为 Additional context 追加到 prompt，例如 /remember 项目用 Go 1.22
//
// 只读取不修改，等用户确认后再执行。
func buildRememberPrompt(args string) string {
	prompt := `# Memory Review

Review my memory system and produce a clear report of proposed changes.

## Steps

### 1. Gather all memory layers
Read the auto-memory entrypoint (MEMORY.md via file_read) and project-level CLAUDE.md / CLAUDE.local.md (if they exist). Your auto-memory is already in context.

### 2. Classify each entry
For each substantive entry, determine the best destination:

| Destination         | What belongs there                          |
|---------------------|----------------------------------------------|
| MEMORY.md (keep)   | Working notes, temporary context            |
| CLAUDE.md          | Team-wide project conventions & instructions |
| CLAUDE.local.md    | Personal preferences (not shared)            |
| Remove             | Duplicates, outdated, contradicted           |

### 3. Present the report
Output grouped by action type:
1. **Promotions** — entries to move, with destination + rationale
2. **Cleanup** — duplicates/outdated/conflicts to resolve
3. **Ambiguous** — entries needing user input
4. **No action** — entries that stay

## Rules
- Present ALL proposals BEFORE making any changes
- Do NOT modify files without explicit user approval
- If auto-memory is empty, say so and offer to review project CLAUDE.md`

	// 对齐上游：args 非空时追加 Additional context
	if strings.TrimSpace(args) != "" {
		prompt += "\n\n## Additional context from user\n\n" + args
	}

	return prompt
}

// ---------- /remember：直接持久化写入 MEMORY.md ----------

// handleRememberCmd 处理 /remember <内容> 命令，返回渲染输出。
//
// 无参数：提示用法
// 有参数：以第一句（遇第一个句号/换行/不超过60字符）为标题，完整内容为正文，
//
//	写入 MEMORY.md。写入成功时回显确认信息。
func (r *REPL) handleRememberCmd(args string) string {
	content := strings.TrimSpace(args)
	if content == "" {
		return r.colorize("用法: /remember <记忆内容>  — 将内容持久化写入 MEMORY.md\r\n", colorYellow) +
			r.colorize("示例: /remember 项目使用 Go 1.22 开发，数据库为 PostgreSQL\r\n", colorDim)
	}

	if r.Memory == nil {
		return r.colorize("（记忆服务未启用）\r\n", colorYellow)
	}

	// 提取标题：第一句（遇 。.！!？?\n 截断，最长 60 字符）
	title := extractTitle(content)

	ctx := context.Background()
	entry, err := r.Memory.AppendEntry(ctx, title, content, "user")
	if err != nil {
		return r.colorize(fmt.Sprintf("写入失败: %v\r\n", err), colorRed)
	}

	return r.colorize(fmt.Sprintf("✓ 已记住 (id=%s): %s\r\n", entry.ID[:8], truncOneLine(title, 50)), colorGreen) +
		r.colorize("  查看所有记忆: /memory list\r\n", colorDim)
}

// extractTitle 从一段文本中提取标题（第一句，最长 60 字符）
func extractTitle(text string) string {
	// 找到第一个句子分隔符
	// 中英文句号/问号/感叹号/换行作为句子结束标志
	sentenceEnds := []string{"。", "！", "？", "\n"}
	minIdx := len(text)
	for _, sep := range sentenceEnds {
		if idx := strings.Index(text, sep); idx >= 0 && idx < minIdx {
			minIdx = idx
		}
	}

	// ". " 仅在非数字上下文时作为句子分隔（避免 "Go 1.22" 这种分割）
	if dotIdx := strings.Index(text, ". "); dotIdx >= 0 && dotIdx < minIdx {
		// 检查 ". " 前是否有数字（如 1.22）
		if dotIdx > 0 {
			prev := text[dotIdx-1]
			if !(prev >= '0' && prev <= '9') {
				minIdx = dotIdx
			}
		} else {
			minIdx = dotIdx
		}
	}

	title := text
	if minIdx < len(text) {
		title = text[:minIdx]
	}

	// 截断至 60 字符
	runes := []rune(title)
	if len(runes) > 60 {
		title = string(runes[:60]) + "…"
	}

	return strings.TrimSpace(title)
}

// ---------- /memory：记忆条目 CRUD ----------

// handleMemoryCmd 处理 /memory <subcommand> 命令，返回渲染输出。
//
// 子命令：
//   - /memory list              列出所有已存储的记忆
//   - /memory add <标题> | <内容>  新增一条记忆
//   - /memory del <id>           按 ID 删除一条记忆
//   - /memory search <关键词>    搜索记忆
func (r *REPL) handleMemoryCmd(args string) string {
	if r.Memory == nil {
		return r.colorize("（记忆服务未启用）\r\n", colorYellow)
	}

	parts := strings.Fields(args)
	if len(parts) == 0 {
		return r.renderMemoryUsage()
	}

	sub := parts[0]
	rest := ""
	if len(parts) > 1 {
		rest = strings.Join(parts[1:], " ")
	}

	ctx := context.Background()

	switch sub {
	case "list":
		return r.renderMemoryList(ctx)
	case "add":
		return r.renderMemoryAdd(ctx, rest)
	case "del", "delete", "rm", "remove":
		return r.renderMemoryDelete(ctx, rest)
	case "search", "find":
		return r.renderMemorySearch(ctx, rest)
	default:
		return r.colorize(fmt.Sprintf("未知子命令 %s\r\n", sub), colorYellow) +
			r.renderMemoryUsage()
	}
}

func (r *REPL) renderMemoryUsage() string {
	return r.colorize("用法:\r\n", colorCyan) +
		r.colorize("  /memory list          列出所有记忆\r\n", colorDim) +
		r.colorize("  /memory add <标题>|<内容>  新增记忆\r\n", colorDim) +
		r.colorize("  /memory del <id>      删除指定记忆\r\n", colorDim) +
		r.colorize("  /memory search <关键词>   搜索记忆\r\n", colorDim) +
		r.colorize("\r\n快捷方式: /remember <内容>  快速添加（自动提取标题）\r\n", colorDim)
}

func (r *REPL) renderMemoryList(ctx context.Context) string {
	entries, err := r.Memory.ListEntries(ctx)
	if err != nil {
		return r.colorize(fmt.Sprintf("读取失败: %v\r\n", err), colorRed)
	}

	if len(entries) == 0 {
		return r.colorize("（暂无记忆，使用 /remember 添加）\r\n", colorDim)
	}

	memory.SortEntriesByTime(entries)

	var sb strings.Builder
	sb.WriteString(r.colorize(fmt.Sprintf("共 %d 条记忆:\r\n", len(entries)), colorCyan))
	for _, e := range entries {
		shortID := e.ID[:8]
		catTag := r.colorize(fmt.Sprintf("[%s]", e.Category), colorInfo)
		timeStr := e.CreatedAt.Local().Format("01-02 15:04")
		fmt.Fprintf(&sb, "  %s %s %s  %s\r\n",
			r.colorize(shortID, colorAccent),
			catTag,
			r.colorize(timeStr, colorDim),
			r.colorize(truncOneLine(e.Title, 50), colorGreen),
		)
	}
	return sb.String()
}

func (r *REPL) renderMemoryAdd(ctx context.Context, args string) string {
	if strings.TrimSpace(args) == "" {
		return r.colorize("用法: /memory add <标题> | <内容>\r\n", colorYellow) +
			r.colorize("示例: /memory add 项目技术栈 | 使用 Go 1.22 + PostgreSQL\r\n", colorDim)
	}

	title, content, category := parseAddArgs(args)

	entry, err := r.Memory.AppendEntry(ctx, title, content, category)
	if err != nil {
		return r.colorize(fmt.Sprintf("添加失败: %v\r\n", err), colorRed)
	}

	return r.colorize(
		fmt.Sprintf("✓ 已添加 (id=%s category=%s): %s\r\n",
			entry.ID[:8], entry.Category, truncOneLine(entry.Title, 50)),
		colorGreen)
}

// parseAddArgs 解析 /memory add 的参数
// 格式: <标题> | <内容> [| <分类>]
// 若无 | 分隔，则整个参数作为标题，内容同标题
func parseAddArgs(args string) (title, content, category string) {
	pipeIdx := strings.Index(args, "|")
	if pipeIdx < 0 {
		title = strings.TrimSpace(args)
		content = title
		category = "user"
		return
	}

	title = strings.TrimSpace(args[:pipeIdx])
	rest := strings.TrimSpace(args[pipeIdx+1:])

	pipe2Idx := strings.Index(rest, "|")
	if pipe2Idx >= 0 {
		content = strings.TrimSpace(rest[:pipe2Idx])
		category = strings.TrimSpace(rest[pipe2Idx+1:])
	} else {
		content = rest
		category = "user"
	}

	if title == "" {
		title = extractTitle(content)
	}
	if content == "" {
		content = title
	}
	return
}

func (r *REPL) renderMemoryDelete(ctx context.Context, id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return r.colorize("用法: /memory del <id>\r\n", colorYellow)
	}

	deleted, err := r.Memory.DeleteEntry(ctx, id)
	if err != nil {
		return r.colorize(fmt.Sprintf("删除失败: %v\r\n", err), colorRed)
	}

	if deleted {
		return r.colorize(fmt.Sprintf("✓ 已删除记忆 %s\r\n", id), colorGreen)
	}
	return r.colorize(fmt.Sprintf("未找到记忆 %s\r\n", id), colorYellow)
}

func (r *REPL) renderMemorySearch(ctx context.Context, keyword string) string {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return r.colorize("用法: /memory search <关键词>\r\n", colorYellow)
	}

	entries, err := r.Memory.SearchEntries(ctx, keyword)
	if err != nil {
		return r.colorize(fmt.Sprintf("搜索失败: %v\r\n", err), colorRed)
	}

	if len(entries) == 0 {
		return r.colorize(fmt.Sprintf("未找到匹配 \"%s\" 的记忆\r\n", keyword), colorDim)
	}

	memory.SortEntriesByTime(entries)

	var sb strings.Builder
	sb.WriteString(r.colorize(fmt.Sprintf("搜索 \"%s\" — %d 条结果:\r\n", keyword, len(entries)), colorCyan))
	for _, e := range entries {
		shortID := e.ID[:8]
		catTag := r.colorize(fmt.Sprintf("[%s]", e.Category), colorInfo)
		timeStr := e.CreatedAt.Local().Format("01-02 15:04")

		fmt.Fprintf(&sb, "  %s %s %s  %s\r\n",
			r.colorize(shortID, colorAccent),
			catTag,
			r.colorize(timeStr, colorDim),
			r.colorize(truncOneLine(e.Title, 50), colorGreen),
		)
		preview := highlightMatch(e.Content, keyword, 80)
		if preview != "" {
			sb.WriteString("    " + r.colorize(preview, colorDim) + "\r\n")
		}
	}
	return sb.String()
}

// highlightMatch 在文本中截取包含关键词的片段
func highlightMatch(text, keyword string, maxLen int) string {
	lower := strings.ToLower(text)
	kw := strings.ToLower(keyword)
	idx := strings.Index(lower, kw)
	if idx < 0 {
		return truncOneLine(text, maxLen)
	}

	start := idx - 20
	if start < 0 {
		start = 0
	}
	end := idx + len(kw) + 60
	if end > len(text) {
		end = len(text)
	}

	preview := text[start:end]
	if start > 0 {
		preview = "…" + preview
	}
	if end < len(text) {
		preview = preview + "…"
	}

	return preview
}
