package shell

// task_ui.go —— 交互终端中"任务执行界面"的视觉构件。
//
// 目标：让 REPL 的任务执行过程在终端里呈现出与产品聊天界面一致的观感：
//   - 顶部助手头部
//   - 「深度思考」思考态标签
//   - 命令以 `>_ <command>` 提示符样式呈现，工具调用以「图标 + 名称 + 折叠箭头 ∨」呈现
//   - 运行中（加载动画）、成功（折叠 ∨）、失败（✗ 红色）三种反馈状态
//   - 底部生成态加载动画
//
// 本文件只包含"纯函数 + 自包含 spinner"，不改变任何业务逻辑，便于单测与复用。

import (
	"strings"
	"time"
)

// ────────────────── 任务界面专用配色 ──────────────────
//
// 与 repl_render.go 的语义色系统互补：这里聚焦"任务执行界面"的氛围色，
// 取深色主题下偏柔和的中间调，避免过亮刺眼，贴近截图观感。
const (
	colorPanel      = "\x1b[38;5;240m"   // 头部面板边线（暗灰）
	colorFooter     = "\x1b[38;5;245m"   // 底部状态文字（次要灰）
	colorSpinner    = "\x1b[38;5;75m"    // 加载动画帧（品蓝）
	colorThinkLabel = "\x1b[38;5;244m"   // 「深度思考」标签（暗灰）
	colorCmdPrompt  = "\x1b[1;38;5;78m"  // 命令提示符 >_（粗翠绿）
	colorCmdText    = "\x1b[38;5;252m"   // 命令正文（亮灰）
	colorChevron    = "\x1b[38;5;240m"   // 折叠箭头 ∨（暗灰）
	colorToolIcon   = "\x1b[38;5;75m"    // 工具图标（品蓝）
	colorToolLabel  = "\x1b[1;38;5;255m" // 工具/命令标签（亮白粗体）
)

// spinnerFrames 加载动画帧（Braille 点阵，平滑旋转观感）。
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinnerFramesASCII 非 UTF-8 终端的兜底动画帧。
var spinnerFramesASCII = []string{"|", "/", "-", "\\"}

// ────────────────── 工具图标映射 ──────────────────

// taskToolVisual 工具在任务界面中的视觉呈现信息。
type taskToolVisual struct {
	// IsCommand 是否为命令行执行类工具（用 `>_ <command>` 提示符样式呈现）。
	IsCommand bool
	// Icon 工具图标（emoji），IsCommand 为 true 时不使用。
	Icon string
	// Label 工具显示名（IsCommand 为 true 时即"命令所属工具"，通常不展示）。
	Label string
}

// taskToolGlyph 把工具名映射为任务界面的图标与标签。
//
// 设计对齐截图：
//   - bash / shell 等执行类      → 命令提示符样式 `>_ <command>`
//   - mcp__* 外部工具            → 🔨 + 去前缀后的工具名
//   - 搜索类（grep/glob/...）     → 📄 Tool Search
//   - 读/写/编辑/网络/Agent 等   → 对应语义图标 + 简洁标签
//   - 未识别工具                  → 🔧 + 原始名
func taskToolGlyph(name string) taskToolVisual {
	key := toolKey(name)
	switch key {
	case "bash", "shell", "exec", "run", "execute_command", "command":
		return taskToolVisual{IsCommand: true, Label: name}
	}

	if strings.HasPrefix(key, "mcp__") {
		return taskToolVisual{Icon: "🔨", Label: mcpDisplayName(name)}
	}

	switch key {
	case "grep", "search_content", "glob", "search_file", "codebase_search":
		return taskToolVisual{Icon: "📄", Label: "Tool Search"}
	case "web_search", "websearch":
		return taskToolVisual{Icon: "🌐", Label: "Web Search"}
	case "web_fetch", "webfetch":
		return taskToolVisual{Icon: "🌐", Label: "Web Fetch"}
	case "read", "file_read", "read_file":
		return taskToolVisual{Icon: "📖", Label: "Read"}
	case "write", "file_write", "write_file", "write_to_file", "create":
		return taskToolVisual{Icon: "📝", Label: "Write"}
	case "edit", "file_edit", "str_replace", "strreplace", "replace_in_file",
		"multiedit", "multi_edit", "apply_patch", "notebook_edit", "notebookedit":
		return taskToolVisual{Icon: "✏️", Label: "Edit"}
	case "ls", "list_dir", "listdir":
		return taskToolVisual{Icon: "📂", Label: "List"}
	case "todo_write", "todowrite":
		return taskToolVisual{Icon: "✅", Label: "Todo"}
	case "agent", "task":
		return taskToolVisual{Icon: "🤖", Label: "Agent"}
	case "skill":
		return taskToolVisual{Icon: "⚡", Label: "Skill"}
	case "delete_file", "deletefile":
		return taskToolVisual{Icon: "🗑️", Label: "Delete"}
	}
	return taskToolVisual{Icon: "🔧", Label: name}
}

// mcpDisplayName 去掉 mcp__ 前缀，把 server__tool 折叠为更可读的展示名。
//
// 例：mcp__tapd-openapi__list  → tapd-openapi
//
//	mcp__tapd-openapi       → tapd-openapi
func mcpDisplayName(name string) string {
	s := strings.TrimSpace(name)
	if !strings.HasPrefix(strings.ToLower(s), "mcp__") {
		return s
	}
	s = s[len("mcp__"):]
	// server__tool → 取 server 作为主展示名（更贴近截图的 "tapd-openapi"）
	if i := strings.Index(s, "__"); i > 0 {
		return s[:i]
	}
	return s
}

// renderTaskToolLine 构造一行"工具/命令"在任务界面中的呈现文本（不含换行）。
//
// 三种尾部状态指示，呼应截图：
//   - running=true       → 加载指示 ⋯（运行中）
//   - isErr=true         → ✗（失败，红色）
//   - 其余（就绪/完成）  → ∨（折叠箭头，可展开细节）
//
// 命令类工具以 `>_ <command>` 提示符样式呈现；其它工具以「图标 + 标签 + 摘要」呈现。
// sandboxed=true 时在命令前加 🔒 标记。
func (r *REPL) renderTaskToolLine(name, summary string, running, isErr, sandboxed bool) string {
	vis := taskToolGlyph(name)

	chevron, runGlyph, lock, cross := "∨", "⋯", "🔒", "✗"
	if r.useASCII {
		chevron, runGlyph, lock, cross = "v", "...", "[sandbox]", "x"
	}

	var sb strings.Builder
	sb.WriteString("  ")

	if vis.IsCommand {
		// 命令提示符样式：>_ <command>
		sb.WriteString(r.colorize(">_ ", colorCmdPrompt))
		body := summary
		if body == "" {
			body = runGlyph
		}
		if sandboxed {
			body = lock + " " + body
		}
		sb.WriteString(r.colorize(body, colorCmdText))
	} else {
		icon := vis.Icon
		if r.useASCII {
			icon = "*"
		}
		sb.WriteString(r.colorize(icon+" ", colorToolIcon))
		sb.WriteString(r.colorize(vis.Label, colorToolLabel))
		if summary != "" {
			sb.WriteString("  " + r.colorize(summary, colorSubtle))
		}
	}

	switch {
	case isErr:
		sb.WriteString("  " + r.colorize(cross, colorError))
	case running:
		sb.WriteString("  " + r.colorize(runGlyph, colorMuted))
	default:
		sb.WriteString("  " + r.colorize(chevron, colorChevron))
	}
	return sb.String()
}

// ────────────────── 加载动画 spinner ──────────────────
//
// waitSpinner 在"等待首个流式事件"期间于底部单行原位刷新一个加载动画，
// 形如：⠋ 生成回复中…
//
// 安全性：spinner 仅在该等待窗口内运行，此时没有任何其它输出写入 stdout，
// 因此不存在与流式渲染的光标竞态；首个事件到达即 Stop（清行后光标回到行首），
// 后续渲染从干净的行首开始，完全不受影响。无色 / 非 TTY 环境下不启用。

type waitSpinner struct {
	r    *REPL
	stop chan struct{}
	done chan struct{}
}

// startWaitSpinner 启动底部加载动画；label 为动态文案（每帧重新取值）。
// 返回 nil 表示当前环境不适合动画（无色/非 TTY），调用方对 nil 调用 Stop 安全。
func (r *REPL) startWaitSpinner(label func() string) *waitSpinner {
	if r == nil || !r.useColor {
		return nil
	}
	sp := &waitSpinner{r: r, stop: make(chan struct{}), done: make(chan struct{})}
	frames := spinnerFrames
	if r.useASCII {
		frames = spinnerFramesASCII
	}
	go func() {
		defer close(sp.done)
		ticker := time.NewTicker(90 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		draw := func() {
			frame := frames[i%len(frames)]
			r.writeOut("\r\x1b[2K" +
				r.colorize(frame+" ", colorSpinner) +
				r.colorize(label(), colorFooter))
		}
		draw()
		for {
			select {
			case <-sp.stop:
				return
			case <-ticker.C:
				i++
				draw()
			}
		}
	}()
	return sp
}

// Stop 停止动画并清除当前行（幂等：可安全多次调用、对 nil 调用）。
func (sp *waitSpinner) Stop() {
	if sp == nil {
		return
	}
	select {
	case <-sp.stop:
		// 已停止
		return
	default:
		close(sp.stop)
	}
	<-sp.done
	sp.r.writeOut("\r\x1b[2K")
}
