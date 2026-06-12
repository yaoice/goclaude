package shell

import (
	"fmt"
	"sort"
	"strings"
)

// 本文件聚合 REPL 的 /help 与 /permissions 帮助系统：命令清单、快捷键、
// 权限模式说明及其渲染。与 src/components/HelpV2 的信息架构对齐。
// 从 repl.go 拆出以提升可读性；逻辑保持不变。

// builtinCommandNames 返回**所有可用于 Tab 补全**的内置 slash 命令名（含别名）。
//
// 注意：与 /help 中显示的清单不同——/help 只展示规范名，避免噪音；而补全希望
// 用户敲 "/qu" → /quit、敲 "/?"→/? 都能命中，所以这里把 case 中所有 alias
// 全都列出。两者数据源不同步是有意为之（与 src 中 builtInCommandNames + Tabs
// 渲染分离的处理一致）。
func builtinCommandNames() []string {
	canonical := make([]string, 0, len(builtinCommands))
	for _, c := range builtinCommands {
		canonical = append(canonical, c.name)
	}
	// 别名：保持与 handleLocalCommand 中 case 分支一一对应
	aliases := []string{"/quit", "/q", "/reset", "/usage", "/?", "/workflows"}
	return append(canonical, aliases...)
}

// helpCommand 描述 /help 渲染时一条命令的元信息。
//
// 与 src/components/HelpV2/Commands.tsx 中 Command 的 label/description 对齐：
//   - name 形如 "/help"（带斜杠）
//   - argHint 可选，例如 "/model [name]" 中的 "[name]"
//   - desc   一句话描述（不要句号收尾，与 src 风格一致）
type helpCommand struct {
	name    string
	argHint string
	desc    string
}

// builtinCommands 是 /help 中内置命令清单。
//
// 顺序设计：
//   - 这里写的是源数据顺序；真正展示前会按字母排序（对齐 src Commands.tsx 的 sort）。
//   - aliases 不单列（如 /quit、/q、/?），用 "或" 在描述里点出，避免清单重复。
var builtinCommands = []helpCommand{
	{name: "/help", argHint: "", desc: "Show this help (also: /?)"},
	{name: "/exit", argHint: "", desc: "Exit the shell (also: /quit, /q)"},
	{name: "/clear", argHint: "", desc: "Clear conversation history (also: /reset)"},
	{name: "/redraw", argHint: "", desc: "Clear screen and reprint banner"},
	{name: "/history", argHint: "", desc: "Show input history"},
	{name: "/messages", argHint: "", desc: "Show current conversation messages"},
	{name: "/model", argHint: "[name]", desc: "Show or set model label (engine model is fixed per session)"},
	{name: "/pwd", argHint: "", desc: "Print working directory"},
	{name: "/workspace", argHint: "[path]", desc: "Show or set workspace output directory"},
	{name: "/cost", argHint: "", desc: "Show token usage summary (also: /usage)"},
	{name: "/compact", argHint: "", desc: "Auto-compact status (managed by engine)"},
	{name: "/permissions", argHint: "", desc: "Show / cycle permission mode (Shift-Tab)"},
	{name: "/env", argHint: "", desc: "Show effective env vars and their sources (.env + settings.json)"},
	{name: "/skills", argHint: "[name]", desc: "Open skills dialog; with name shows skill body"},
	{name: "/agents", argHint: "[type]", desc: "Open agents dialog; with type shows agent prompt"},
	{name: "/mcp", argHint: "[tools|status]", desc: "Open MCP dialog; with arg shows servers/tools"},
	{name: "/tools", argHint: "[name]", desc: "List registered tools; with name shows schema"},
	{name: "/teams", argHint: "[name]", desc: "List agent teams; with name shows team detail"},
	{name: "/workflow", argHint: "[list|plan|run|status|cancel] [args]", desc: "Manage workflows: list, plan (AI-generate), run, status, cancel"},
	{name: "/remember", argHint: "", desc: "Directly persist a memory to MEMORY.md (auto-extracts title)"},
	{name: "/memory", argHint: "list|add|del|search [args]", desc: "Manage memories: list, add, delete by ID, search"},
	{name: "/enhance-prompt", argHint: "<text>", desc: "Enhance a prompt via AI; Ctrl+G to toggle original/enhanced"},
}

// helpShortcut 描述 /help 中一条快捷键。
type helpShortcut struct {
	keys string
	desc string
}

// helpShortcuts 与 src General.tsx 中 PromptInputHelpMenu 对齐的常用快捷键集合。
var helpShortcuts = []helpShortcut{
	{"Tab", "Complete slash command; show candidates on multi-match"},
	{"Shift-Tab", "Cycle permission mode: default → acceptEdits → plan → bypass"},
	{"Shift-Enter", "Insert newline (also: Alt-Enter, trailing \\)"},
	{"↑ / ↓", "History navigation; cursor up/down inside multi-line"},
	{"← / →, Ctrl-A/E", "Move cursor; line start / end"},
	{"Alt-← / →", "Jump by word"},
	{"Ctrl-W", "Delete previous word"},
	{"Ctrl-U / Ctrl-K", "Delete to line start / end"},
	{"Ctrl-L", "Clear screen"},
	{"Ctrl-O", "Open transcript fullscreen viewer"},
	{"Ctrl-G", "Toggle between original and enhanced prompt versions"},
	{"Ctrl-R", "Reverse incremental history search"},
	{"Ctrl-X Ctrl-E", "Edit current input in $EDITOR"},
	{"Esc", "Cancel current generation; clear input when idle"},
	{"Ctrl-C", "Interrupt generation; clear input when idle"},
	{"Ctrl-D", "Exit on empty line"},
}

// helpPermissionMode 描述一种权限模式，用于 /help 与 /permissions 共享数据源。
//
// 与 src 中 PermissionModeConfigs（permission cycle 顺序：default → acceptEdits
// → plan → bypass）严格对齐，避免两个文档源漂移。
type helpPermissionMode struct {
	name string
	desc string
}

// helpPermissionModes 是按 cycle 顺序排列的 4 种模式描述。
var helpPermissionModes = []helpPermissionMode{
	{"default", "Ask before each write tool (Edit / Write / Bash etc.)"},
	{"acceptEdits", "Auto-approve edit tools; other writes still ask"},
	{"plan", "Read-only: refuse all writes (great for planning)"},
	{"bypass", "Skip all checks  ⚠ unsafe; only with --dangerously-skip-permissions / GOCLAUDE_PERMISSION_MODE=bypass"},
}

// printPermissionsOverview 渲染 /permissions 的输出：当前模式 + 4 种模式总览
// + 切换 / 跳过的两条捷径。
//
// 解决用户痛点："为何每次都触发 Permission required，默认都开不行吗"：
//   - 把 4 种模式的取舍并列展示，让用户自己评估"我可以接受 acceptEdits 吗？"
//   - 把 GOCLAUDE_PERMISSION_MODE 与 --dangerously-skip-permissions 显式列出，
//     让用户知道有持久化通道，而不必每次重启都点 [s] Allow always
func (r *REPL) printPermissionsOverview() {
	var sb strings.Builder
	current := r.PermissionMode
	if current == "" {
		current = "default"
	}
	sb.WriteString(r.colorize("Current permission mode: ", colorCyan))
	sb.WriteString(r.colorize(current, colorAccent))
	sb.WriteString("\r\n")

	sb.WriteString("\r\n")
	sb.WriteString(r.colorize("▌ Modes (Shift-Tab cycles in this order)", colorCyan))
	sb.WriteString("\r\n")
	leftW := 0
	for _, m := range helpPermissionModes {
		if w := visibleWidth(m.name); w > leftW {
			leftW = w
		}
	}
	leftW += 4
	for _, m := range helpPermissionModes {
		marker := "  "
		if m.name == current {
			marker = r.colorize("→ ", colorAccent)
		}
		sb.WriteString(marker)
		sb.WriteString(r.helpRow(m.name, m.desc, leftW))
	}

	sb.WriteString("\r\n")
	sb.WriteString(r.colorize("▌ Skip prompts persistently", colorCyan))
	sb.WriteString("\r\n")
	for _, row := range []helpShortcut{
		{"--dangerously-skip-permissions", "CLI flag: start in bypass mode (per-launch)"},
		{"GOCLAUDE_PERMISSION_MODE=bypass", "Env var: bypass on every launch (write to shell rc)"},
		{"GOCLAUDE_PERMISSION_MODE=acceptEdits", "Env var: less aggressive, only auto-approve edits"},
		{"[s] Allow always (this session)", "Pop-up choice: remembers tool name until REPL exits"},
	} {
		sb.WriteString(r.helpRow(row.keys, row.desc, 36))
	}
	sb.WriteString("\r\n")
	r.writeOut(sb.String())
}

// printHelp 把 renderHelp 的结果写入 stdout。
//
// 真正的渲染逻辑在 renderHelp（返回 string），便于单测在不抓 stdout 的情况下
// 断言文本内容。
func (r *REPL) printHelp() {
	r.writeOut(r.renderHelp())
}

// renderHelp 渲染 /help 的完整内容并以 string 返回。
//
// 信息架构对齐 src/components/HelpV2：
//   - General：一句产品介绍 + Input syntax 子节
//   - Commands：内置 slash 命令（按字母序）
//   - Custom：从 ~/.claude/commands、<cwd>/.claude/commands 加载的自定义命令
//   - Shortcuts：键盘快捷键
//
// 渲染原则：
//   - 不画 ASCII box（中英混排时框线永远对不齐）；用 cyan section header + dim 描述区分
//   - 每节内部用统一 leftWidth，按可视宽度（visibleWidth，已处理 CJK / ANSI）右补空格
//   - 颜色由 r.useColor 决定；NO_COLOR / 非 TTY 下退化成纯文本，便于 expect 与 grep
//   - 行结束用 \r\n，以便在原始模式终端正确换行
func (r *REPL) renderHelp() string {
	var sb strings.Builder

	writeSection := func(title string) {
		sb.WriteString("\r\n")
		sb.WriteString(r.colorize("▌ "+title, colorCyan))
		sb.WriteString("\r\n")
	}

	// 第一节：General（产品定位 + 输入语法）
	sb.WriteString(r.colorize("GoClaude", colorCyan))
	sb.WriteString(r.colorize(
		" — understands your codebase, edits with permission,",
		colorDim))
	sb.WriteString("\r\n")
	sb.WriteString(r.colorize(
		"           executes tools, and coordinates agents from this terminal.",
		colorDim))
	sb.WriteString("\r\n")

	writeSection("Input syntax")
	for _, row := range []helpShortcut{
		{"plain text", "Send to the LLM (multi-turn context preserved)"},
		{"/command", "Run a local slash command (not sent to the LLM)"},
		{"!cmd", "Run a local shell command (not sent to the LLM)"},
		{"!!text", "Escape: send '!text' as a normal prompt"},
	} {
		sb.WriteString(r.helpRow(row.keys, row.desc, 18))
	}

	// 第二节：Commands（内置）
	writeSection("Commands")
	cmds := append([]helpCommand(nil), builtinCommands...)
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].name < cmds[j].name })
	leftW := commandColumnWidth(cmds)
	for _, c := range cmds {
		left := c.name
		if c.argHint != "" {
			left += " " + c.argHint
		}
		sb.WriteString(r.helpRow(left, c.desc, leftW))
	}

	// 第三节：Permission modes（与 /permissions 共享 helpPermissionModes 数据源）
	//
	// 把这一节放在 Commands 之后是有意为之：用户在看完命令清单时，立刻看到
	// "可以怎么少弹窗"，避免他重复触发 Permission required 后再去查 /permissions。
	writeSection("Permission modes (Shift-Tab cycles)")
	pmLeftW := 0
	for _, m := range helpPermissionModes {
		if w := visibleWidth(m.name); w > pmLeftW {
			pmLeftW = w
		}
	}
	pmLeftW += 4
	for _, m := range helpPermissionModes {
		sb.WriteString(r.helpRow(m.name, m.desc, pmLeftW))
	}

	// 第四节：Configuration sources（让用户知道环境变量可以放哪些位置）
	//
	// 历史痛点："GOCLAUDE_PERMISSION_MODE 这些环境变量能否配置文件设置"——
	// 答案是肯定的（.env / settings.json 都行），但用户不一定知道。
	// 把三种方式并列在 /help 里展示，避免每次都重新查文档。
	writeSection("Configuration sources (env vars + flags)")
	for _, row := range []helpShortcut{
		{"shell export", "Highest priority (per-terminal); e.g. export GOCLAUDE_PERMISSION_MODE=acceptEdits"},
		{".env / .env.local", "Project-level; auto-loaded from cwd and walked up"},
		{"~/.claude/.env", "User-global key=value file"},
		{"~/.claude/settings.json", "User-global JSON; set under \"env\": { ... }"},
		{".claude/settings.json", "Project-level JSON (team-shared)"},
		{".claude/settings.local.json", "Project-level JSON (personal, gitignored)"},
		{"--env-file <path>", "Per-launch override (highest priority)"},
	} {
		sb.WriteString(r.helpRow(row.keys, row.desc, 30))
	}

	// 第五节：Custom commands（仅当存在时展示）
	if r.CustomCommands != nil {
		names := r.CustomCommands.Names()
		if len(names) > 0 {
			writeSection(fmt.Sprintf("Custom commands (%d)", len(names)))
			// 收集后排序，与 src 行为一致
			ccs := make([]helpCommand, 0, len(names))
			for _, n := range names {
				cc, ok := r.CustomCommands.Get(n)
				if !ok {
					continue
				}
				desc := cc.Description
				if desc == "" {
					desc = "(no description)"
				}
				if cc.Source != "" {
					// 与 src formatDescriptionWithSource 对齐：尾部追加 "(source)"
					desc = fmt.Sprintf("%s  (%s)", desc, cc.Source)
				}
				ccs = append(ccs, helpCommand{
					name:    "/" + n,
					argHint: cc.ArgumentHint,
					desc:    desc,
				})
			}
			sort.Slice(ccs, func(i, j int) bool { return ccs[i].name < ccs[j].name })
			ccLeftW := commandColumnWidth(ccs)
			for _, c := range ccs {
				left := c.name
				if c.argHint != "" {
					left += " " + c.argHint
				}
				sb.WriteString(r.helpRow(left, c.desc, ccLeftW))
			}
		}
	}

	// 第四节：Shortcuts
	writeSection("Shortcuts")
	scLeftW := shortcutColumnWidth(helpShortcuts)
	for _, s := range helpShortcuts {
		sb.WriteString(r.helpRow(s.keys, s.desc, scLeftW))
	}

	// Footer：对齐 src HelpV2 底部的 "For more help / dismiss" 提示
	sb.WriteString("\r\n")
	sb.WriteString(r.colorize(
		"For more help: https://github.com/anthropics/claude-code",
		colorDim))
	sb.WriteString("\r\n")

	return sb.String()
}

// NotifySkillActivated 条件 skill 被自动激活时的视觉通知。
// 由 cli 层赋值为 OnSkillActivate 回调。
func (r *REPL) NotifySkillActivated(name string) {
	r.writeOut(r.colorize(
		fmt.Sprintf("  ⚡ skill activated: %s (conditional)\r\n", name),
		colorCyan,
	))
}

// helpRow 渲染一行 "left  description"，确保 left 列按可视宽度对齐。
//
// 与旧 helpRow 不同：
//   - leftWidth 由调用方根据本节内容动态算出（commandColumnWidth / shortcutColumnWidth），
//     避免不同节之间长度差异过大造成空白浪费
//   - left 用 dim 不再着色（与 src Commands.tsx 一致：命令名是普通色，描述才 dim）
//     —— 这里我们做相反取舍：goclaude 终端无法用粗体可靠区分，于是命令名用 colorAccent
//     色（与 banner 标题色一致），描述用 colorDim
func (r *REPL) helpRow(left, right string, leftWidth int) string {
	pad := leftWidth - visibleWidth(left)
	if pad < 2 {
		pad = 2
	}
	return "  " +
		r.colorize(left, colorAccent) +
		strings.Repeat(" ", pad) +
		r.colorize(right, colorDim) +
		"\r\n"
}

// commandColumnWidth 返回 cmds 中最长 "name argHint" 的可视宽度（+ 2 列空隙）。
// 用 visibleWidth 处理 CJK / ANSI，避免简单 len() 在中文混排时漂移。
func commandColumnWidth(cmds []helpCommand) int {
	max := 0
	for _, c := range cmds {
		left := c.name
		if c.argHint != "" {
			left += " " + c.argHint
		}
		if w := visibleWidth(left); w > max {
			max = w
		}
	}
	// 至少留 4 列描述前的空白
	return max + 4
}

// shortcutColumnWidth 与 commandColumnWidth 类似，但用于 keys 列。
func shortcutColumnWidth(shortcuts []helpShortcut) int {
	max := 0
	for _, s := range shortcuts {
		if w := visibleWidth(s.keys); w > max {
			max = w
		}
	}
	return max + 4
}
