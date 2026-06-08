package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/anthropics/goclaude/pkg/application"
	"github.com/anthropics/goclaude/pkg/domain/memory"
	"github.com/anthropics/goclaude/pkg/domain/query"
)

// idleInterruptWindow 空闲态连续两次 Ctrl+C 判定为"退出"的时间窗口
const idleInterruptWindow = 2 * time.Second

// MemoryManager 记忆入口文件管理接口（/remember /memory 命令依赖此接口）。
// 由 cli 层注入 application.MemoryService 作为实现。
type MemoryManager interface {
	AppendEntry(ctx context.Context, title, content, category string) (*memory.EntryItem, error)
	DeleteEntry(ctx context.Context, id string) (bool, error)
	ListEntries(ctx context.Context) ([]memory.EntryItem, error)
	SearchEntries(ctx context.Context, keyword string) ([]memory.EntryItem, error)
	GetEntrypointContent(ctx context.Context) (string, error)
}

// REPL 交互式 shell 主循环
//
// 职责：
//   - 维护多轮对话上下文（messages 列表）
//   - 调用 ReadLine 读取一行用户输入
//   - 把 / 开头的视为本地命令（/help /clear /history /exit ...）
//   - 其它输入封装为 user message 交给 QueryEngine.Execute
//   - 实时流式打印 assistant 文本到 stdout
//   - Ctrl+C 在 "生成中" 时取消当前查询并保留 REPL；空闲时提示再次按下退出
//   - Ctrl+D 在空行时优雅退出
//
// 实现按职责拆分到多个同包文件：
//   - repl.go             核心：结构体、构造、主循环 Run、单轮查询 runOnce
//   - repl_commands.go    本地 slash 命令分发
//   - repl_interaction.go `!` bash 模式、权限弹窗、ask_user、信号处理
//   - repl_render.go      终端渲染原语、颜色、banner
//   - repl_help.go        /help 与 /permissions 帮助系统
type REPL struct {
	// 必填依赖
	Engine *query.Engine
	Term   *Terminal
	Editor *Editor

	// 历史记录（可选）
	History *History

	// 显示给用户的元信息
	Model    string
	Provider string
	WorkDir  string

	// Verbose 控制是否在每轮回复后打印 turns/stop/elapsed/tokens 状态行
	Verbose bool

	// PermissionMode 当前权限模式（仅显示用；底层切换由 OnPermissionModeChange 注入）
	PermissionMode string

	// OnPermissionModeChange 用户按下 Shift+Tab 切换权限模式时回调
	// 回调返回切换后的新模式名（用于显示）；nil 时不允许切换
	OnPermissionModeChange func() string

	// PauseInput 临时离开原始模式（如 AskUser 工具需要从 stdin 读一行）
	// fn 执行期间，shell 不会读键盘；fn 结束自动恢复
	pauseInputMu sync.Mutex

	// 管理服务（可选）：注入后才能使用 /skills /agents /mcp /tools /teams /workflows
	Skills    SkillManager
	Agents    AgentManager
	MCP       MCPManager
	Tools     ToolRegistryView
	Teams     TeamManager
	Workflows WorkflowManager
	Memory    MemoryManager // 记忆入口管理器（/remember /memory）

	// CustomCommands 用户/项目自定义 prompt-类 slash 命令注册表
	// 由 NewREPL 自动从 ~/.claude/commands/ 与 <cwd>/.claude/commands/ 加载
	CustomCommands *CustomCommands

	// PermissionDialog 工具权限弹窗（每次写入类工具调用前确认）
	// 自动构造；外部可读取 IsAlwaysAllowed 等
	PermissionDialog *PermissionDialog

	// 系统提示（可选；当不为空时附在消息列表最前面注入到 Engine.Config 已处理，无需此处携带）
	// 这里仅保存若需要回显。

	// 退出钩子（可选）
	OnExit func()

	// OnSkillActivate 条件 skill 被自动激活时的回调（由 cli 层注入）
	// 在 REPL 终端打印一行 "⚡ skill activated: <name>" 通知用户
	OnSkillActivate func(name string)

	// OnBeforeTurn 在每次向 LLM 提交查询前调用（可选；由 cli 层注入）。
	//
	// 主要用途：team leader 每轮自动处理 inbox（ProcessLeaderInbox），把 teammate
	// 的任务进展同步进共享任务列表，并把可读摘要作为团队上下文返回。返回的非空
	// 字符串会被注入到本轮用户输入之前，让模型即时看到协作者的最新状态，
	// 形成"任务分配 → 执行 → 状态回流"的闭环。nil 或返回空串时跳过。
	OnBeforeTurn func() string

	// 内部：会话状态
	mu       sync.Mutex
	messages []query.Message

	// generating 标记当前是否处于"生成中"（决定 Ctrl+C 语义）
	generating atomic.Bool

	// activeCancel 当前查询的 cancel 函数（仅 generating=true 时有效）
	activeCancel context.CancelFunc

	// wantsExit 由 maybeOfferExit 在两次快速 Ctrl+C 时置 true，
	// 通知主循环在下一次收到 ErrInterrupted 时执行优雅退出。
	wantsExit atomic.Bool

	// lastIdleInterrupt 记录上次空闲态 Ctrl+C 的时间，用于两次连按退出判定
	lastIdleInterrupt time.Time

	// 输出色彩
	useColor bool

	// useASCII 终端不支持 UTF-8 时为 true，渲染切到 ASCII 兜底字形（见 style.go）
	useASCII bool

	// toolEvents 缓存 Executor 派发的工具事件（耗时/状态），
	// 由 *REPL.HandleToolEvent 写入；流式渲染 tool_result 时 consume 拼接到行尾。
	toolEvents *toolEventBuffer

	// streamStates 跟踪文件写入工具的实时流式输出状态（按 ToolUseID 索引）
	streamStates map[string]*streamingState

	// subagentMu / subagentTrackers 跟踪每个 subagent 的阶段化进度（按 AgentID 分桶）。
	// subagent 在独立 goroutine 中派发事件，故需独立互斥保护，避免与主流程输出竞态。
	subagentMu       sync.Mutex
	subagentTrackers map[string]*subagentTracker

	// statusLineMu / lastStatusLines 用于 inline 状态更新的终端光标管理。
	// renderInlineStatus 在多次调用时需要精确清除上一次渲染的行。
	statusLineMu    sync.Mutex
	lastStatusLines int
}

// NewREPL 构造一个 REPL；若 stdout/stdin 不是 TTY 或 NO_COLOR 已设置，会自动禁用色彩
func NewREPL(engine *query.Engine, model, provider, workDir string) *REPL {
	term := NewTerminal()
	hist := NewHistory(DefaultHistoryPath(), 0)
	_ = hist.Load()

	useColor := term.IsTerminal()
	if os.Getenv("NO_COLOR") != "" {
		useColor = false
	}

	r := &REPL{
		Engine:         engine,
		Term:           term,
		History:        hist,
		Model:          model,
		Provider:       provider,
		WorkDir:        workDir,
		PermissionMode: "default",
		messages:       make([]query.Message, 0, 16),
		useColor:       useColor,
		useASCII:       detectUseASCII(),
	}

	// 加载自定义 prompt-类 slash 命令
	r.CustomCommands = NewCustomCommands()
	r.CustomCommands.LoadDefaults(workDir)

	// 工具权限弹窗
	r.PermissionDialog = NewPermissionDialog()

	// 多候选时把候选列表打印到 prompt 之上（PrintAboveLine）
	onList := func(candidates []string) {
		if r.Editor == nil {
			return
		}
		// 简单按 4 列对齐
		var sb strings.Builder
		const cols = 4
		colW := 18
		for i, c := range candidates {
			if i%cols == 0 && i > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(c)
			pad := colW - len(c)
			if pad < 1 {
				pad = 1
			}
			sb.WriteString(strings.Repeat(" ", pad))
		}
		r.Editor.PrintAboveLine(r.colorize(sb.String(), colorDim))
	}

	// 合并内置 + 自定义命令到补全字典
	allCmds := append([]string(nil), builtinCommandNames()...)
	allCmds = append(allCmds, r.CustomCommands.SlashNames()...)

	completer := NewSlashCompleter(allCmds, onList)
	r.Editor = NewEditor(term, os.Stdout, hist, completer)
	return r
}

// Run 启动主循环；阻塞直至用户退出
func (r *REPL) Run(ctx context.Context) error {
	if !r.Term.IsTerminal() {
		return fmt.Errorf("interactive shell requires a TTY")
	}
	if err := r.Term.EnterRaw(); err != nil {
		return fmt.Errorf("enter raw mode: %w", err)
	}
	// 启用 bracketed paste，让多行粘贴不被当作多次 Enter
	r.Editor.EnableBracketedPaste(true)
	defer func() {
		r.Editor.EnableBracketedPaste(false)
		_ = r.Term.LeaveRaw()
		if r.History != nil {
			_ = r.History.Save()
		}
		if r.OnExit != nil {
			r.OnExit()
		}
	}()

	// 把 Shift+Tab 接到权限模式切换
	r.Editor.OnShiftTab = func() {
		if r.OnPermissionModeChange == nil {
			return
		}
		newMode := r.OnPermissionModeChange()
		if newMode != "" {
			r.PermissionMode = newMode
			r.Editor.PrintAboveLine(r.colorize(
				fmt.Sprintf("⏿ permission mode → %s", newMode), colorCyan))
		}
	}

	// Ctrl+O：进入 transcript 全屏只读模式
	r.Editor.OnCtrlO = func() {
		// 暂停信号 SIGWINCH 重绘（避免在 transcript 中收到中断）
		r.pauseInputMu.Lock()
		defer r.pauseInputMu.Unlock()
		r.ShowTranscript()
	}

	// Ctrl+X Ctrl+E：调用外部编辑器（$EDITOR / $VISUAL）
	r.Editor.OnExternalEditor = func(current string) (string, bool) {
		return r.LaunchExternalEditor(current)
	}

	r.printBanner()

	// 安装信号处理
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGWINCH)
	defer signal.Stop(sigCh)

	// 信号 goroutine：根据 generating 决定语义
	signalDone := make(chan struct{})
	go r.signalLoop(ctx, sigCh, signalDone)
	defer close(signalDone)

	// 生成中 stdin 监听 goroutine：
	// 终端原始模式（MakeRaw 清除 ISIG）下，Ctrl+C 产生字节 0x03 而非 SIGINT。
	// 当主循环阻塞在 runOnce（LLM 生成）时，ReadLine 不在运行，没有人消费 stdin；
	// 此 goroutine 专门在生成中轮询 stdin，将 0x03 字节转化为对当前查询的取消。
	stdinDone := make(chan struct{})
	go r.stdinCancelLoop(ctx, stdinDone)
	defer close(stdinDone)

	for {
		// ctx 提前取消（SIGTERM 等）则优雅退出
		select {
		case <-ctx.Done():
			r.writeOut("\r\n再见！\r\n")
			return nil
		default:
		}

		prompt := r.makePrompt()
		line, err := r.Editor.ReadLine(prompt)
		if err != nil {
			switch {
			case errors.Is(err, ErrInterrupted):
				// 空闲态 Ctrl+C：
				//   - wantsExit 已标记（来自外部 SIGINT 信号）→ 直接退出
				//   - 否则调用 maybeOfferExit：
				//       第一次按 → 提示并记录时间
				//       第二次按（在时间窗口内）→ 标记 wantsExit=true 并退出
				if r.wantsExit.Load() {
					r.writeOut("\r\n再见！\r\n")
					return nil
				}
				r.maybeOfferExit()
				// maybeOfferExit 在两次快速 Ctrl+C 时会置 wantsExit=true，立即退出
				if r.wantsExit.Load() {
					r.writeOut("\r\n再见！\r\n")
					return nil
				}
				continue
			case errors.Is(err, ErrCancel):
				// Esc：丢弃当前行，继续
				continue
			case errors.Is(err, io.EOF):
				// 空行 Ctrl+D → 优雅退出
				r.writeOut("\r\n再见！\r\n")
				return nil
			}
			return err
		}

		// 整行 trim 仅用于"空白"检测；保留多行内部空白
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// 写入历史（多行也只存一条；此时仍含粘贴占位符，便于回看）
		if r.History != nil {
			r.History.Append(line)
		}

		// 把 [Pasted text #N +M lines] 占位符还原为原始粘贴内容
		// 历史里已经写入了占位符版本，提交给 LLM 的是还原版本
		expandedLine := r.Editor.ExpandPasteRefs(line)
		expandedTrimmed := strings.TrimSpace(expandedLine)

		// 退出关键字（对齐 src `handlePromptSubmit`）
		switch expandedTrimmed {
		case "exit", "quit", ":q", ":q!", ":wq", ":wq!":
			r.writeOut("再见！\r\n")
			r.Editor.ResetPasteRefs()
			return nil
		}

		// 处理本地命令（/...）
		if strings.HasPrefix(expandedTrimmed, "/") {
			exit, expandedPrompt := r.handleLocalCommand(expandedTrimmed)
			if exit {
				r.Editor.ResetPasteRefs()
				return nil
			}
			if expandedPrompt != "" {
				// 自定义 prompt-类命令：把渲染结果作为 user prompt 发给 LLM
				r.runOnce(ctx, expandedPrompt)
			}
			r.Editor.ResetPasteRefs()
			continue
		}

		// `!` bash 模式（对齐 src/components/PromptInput/inputModes.ts）
		// `!cmd`     → 本地执行 shell 并显示结果，不发给 LLM
		// `!!cmd`    → 转义：把整段（含开头一个 `!`）作为 user prompt 发给 LLM
		if strings.HasPrefix(expandedTrimmed, "!") {
			if strings.HasPrefix(expandedTrimmed, "!!") {
				// 去掉一个 ! 后发给 LLM
				r.runOnce(ctx, expandedTrimmed[1:])
			} else {
				r.runBash(ctx, expandedTrimmed[1:])
			}
			r.Editor.ResetPasteRefs()
			continue
		}

		// 检测 workflow 创建意图：自然语言描述 → 自动路由到 /workflow run
		// 这样用户无需知道 slash 命令的存在，通过自然语言即可创建并执行工作流。
		if r.Workflows != nil {
			if intent := application.ParseWorkflowIntent(expandedTrimmed); intent != nil && intent.Triggered {
				r.writeOut(r.colorize("🔧 检测到 workflow 意图 → 自动规划并执行…\r\n", colorCyan))
				r.handleWorkflowCmd([]string{"run", intent.Description})
				r.Editor.ResetPasteRefs()
				continue
			}
		}

		// 默认：发给 LLM（用展开后的版本）
		r.runOnce(ctx, expandedLine)
		r.Editor.ResetPasteRefs()
	}
}

// runOnce 执行一次完整的查询：追加 user 消息 → 流式打印 → 追加 assistant 消息
func (r *REPL) runOnce(parent context.Context, userInput string) {
	// 提交前钩子：team leader 在此同步 inbox，把 teammate 进展注入本轮上下文，
	// 让模型在回复前先"看到"协作者的最新状态。
	if r.OnBeforeTurn != nil {
		if note := strings.TrimSpace(r.OnBeforeTurn()); note != "" {
			r.writeOut(r.colorize("  📥 收到团队消息，已同步到本轮上下文", colorCyan) + "\r\n")
			userInput = note + "\n\n" + userInput
		}
	}

	r.mu.Lock()
	r.messages = append(r.messages, query.NewTextMessage(query.RoleUser, userInput))
	userMsgIdx := len(r.messages) - 1
	msgs := append([]query.Message(nil), r.messages...)
	r.mu.Unlock()

	// 派生可取消 ctx，安装到 r.activeCancel
	ctx, cancel := context.WithCancel(parent)
	r.mu.Lock()
	r.activeCancel = cancel
	r.mu.Unlock()
	r.generating.Store(true)

	defer func() {
		r.generating.Store(false)
		r.mu.Lock()
		r.activeCancel = nil
		r.mu.Unlock()
		cancel()
	}()

	events := make(chan query.StreamEvent, 64)
	type doneT struct {
		res *query.QueryResult
		err error
	}
	done := make(chan doneT, 1)
	go func() {
		res, err := r.Engine.Execute(ctx, msgs, events)
		close(events)
		done <- doneT{res, err}
	}()

	// 实时打印（带 markdown 流式格式化）
	r.printAssistantHeader()
	var assistantText strings.Builder
	startedAt := time.Now()

	// indent + 边条让助手回复区与用户输入视觉分离
	formatter := NewStreamFormatter("  ", "│ ")
	buf := &bytes.Buffer{}

	// flushBuf 立即将缓冲内容写入终端
	flushBuf := func() {
		if buf.Len() > 0 {
			r.writeOut(buf.String())
			buf.Reset()
		}
	}

	// flushFmt 把 formatter 的行缓冲与字节缓冲一起冲掉
	// （在切到非 markdown 输出前必须调用）
	flushFmt := func() {
		formatter.Flush(buf)
		flushBuf()
	}

	// 跟踪当前 block index → tool_use 信息，用于结果阶段配对
	type toolMeta struct {
		name      string
		toolUseID string
		partial   string // 累积的 partial JSON（流式工具调用）
		skillName string // 仅 Skill 工具：解析出的 skill 名称
		collapsed bool   // 内部协调工具（parse_team_intent 等）：折叠为单行精简摘要
		// inputSummary 工具参数可读摘要（从 partial JSON 提取）；用于在工具名后显示"在做什么"
		inputSummary string
		// summaryFlushed 参数摘要已经打印过启动行（避免 partial 更新时重复刷新）
		summaryFlushed bool
		// stepNum 该工具的步骤编号
		stepNum int
		// isSandboxed 是否为沙箱执行的命令
		isSandboxed bool
	}
	toolUseIDToMeta := map[string]*toolMeta{} // ToolUseID → *toolMeta 快速查找
	tools := map[int]*toolMeta{}

	// 思考块状态：默认折叠为一行；verbose 时实时打印 thinking 文本
	thinkingShown := false

	// 工具步骤计数器（用于显示 [1] [2] [3] ...）
	toolStepCount := 0

	for ev := range events {
		switch ev.Type {
		case query.EventContentBlockStart:
			if ev.ContentBlock == nil {
				continue
			}
			b := ev.ContentBlock
			switch b.Type {
			case query.ContentTypeToolUse:
				flushFmt()
				meta := &toolMeta{
					name:      b.ToolName,
					toolUseID: b.ToolUseID,
					collapsed: isNoisyCoordinationTool(b.ToolName),
				}
				tools[ev.Index] = meta
				if b.ToolUseID != "" {
					toolUseIDToMeta[b.ToolUseID] = meta
				}
				if meta.collapsed {
					break
				}
				// 递增步骤计数
				toolStepCount++
				// 渲染启动行：[N] ◌ tool_name  …（运行中状态）
				displayName := b.ToolName
				marker := ""
				if strings.HasPrefix(b.ToolName, "mcp__") {
					marker = r.colorize(" ⟨MCP⟩", colorInfo)
				}
				stepLabel := r.colorize(fmt.Sprintf("  [%d] ", toolStepCount), colorStepNum)
				r.writeOut(stepLabel +
					r.colorize("◌ ", colorInfo) +
					r.colorize(displayName, colorToolName) + marker +
					r.colorize("  …", colorMuted) + "\r\n")
				meta.summaryFlushed = true
				meta.stepNum = toolStepCount

			case query.ContentTypeToolResult:
				flushFmt()
				// 折叠工具
				if m, ok := toolUseIDToMeta[b.ToolUseID]; ok && m.collapsed {
					if b.IsError {
						r.writeOut("      " + r.colorize("✗ "+m.name, colorError) + "\r\n")
					} else {
						r.writeOut("      " + r.colorize("· "+m.name, colorMuted) + "\r\n")
					}
					break
				}

				// 判断是否为沙箱执行的命令
				isSandboxedResult := false
				if m, ok := toolUseIDToMeta[b.ToolUseID]; ok {
					isSandboxedResult = m.isSandboxed
				}

				// 结果行：成功 ✓ / 失败 ✗，颜色明确区分
				isError := b.IsError
				var statusIcon, statusColor, fallback string
				if isError {
					statusIcon = "✗"
					statusColor = colorError
					fallback = "failed"
				} else {
					statusIcon = "✓"
					statusColor = colorSuccess
					fallback = "done"
				}

				// 结果摘要
				var body string
				maxCols := r.termWidth() - 12
				if maxCols < 20 {
					maxCols = 20
				}
				if isError {
					body = summarizeError(b.Text, min(100, maxCols))
					if body == "" {
						body = fallback
					} else if !r.useColor {
						body = fallback + ": " + body
					}

					// 沙箱命令错误：使用醒目的错误框显示
					if isSandboxedResult && b.Text != "" {
						prefix := r.colorize("      "+statusIcon+" ", statusColor)
						r.writeOut(prefix + r.colorize(body, colorError))
						if tail, ok := r.consumeToolFinish(b.ToolUseID); ok {
							r.writeOut(tail)
						}
						r.writeOut("\r\n")
						r.renderSandboxError(b.Text, 6)
						break
					}

					// verbose 模式：展开错误输出（最多 8 行）
					if r.Verbose && b.Text != "" {
						prefix := r.colorize("      "+statusIcon+" ", statusColor)
						r.writeOut(prefix + r.colorize(body, colorSubtle))
						if tail, ok := r.consumeToolFinish(b.ToolUseID); ok {
							r.writeOut(tail)
						}
						r.writeOut("\r\n")
						r.renderToolOutputVerbose(b.Text, colorErrOutput, 8)
						break
					}
				} else {
					body = summarizeResult(b.Text, min(100, maxCols))
					if body == "" {
						body = fallback
					}
					// verbose 模式：成功时也展示输出（最多 5 行）
					if r.Verbose && b.Text != "" && strings.Count(b.Text, "\n") > 0 {
						prefix := r.colorize("      "+statusIcon+" ", statusColor)
						r.writeOut(prefix + r.colorize(body, colorSubtle))
						if tail, ok := r.consumeToolFinish(b.ToolUseID); ok {
							r.writeOut(tail)
						}
						r.writeOut("\r\n")
						r.renderToolOutputVerbose(b.Text, colorOutput, 5)
						break
					}
				}

				// 标准结果行
				line := r.colorize("      "+statusIcon+" ", statusColor) +
					r.colorize(body, colorSubtle)
				if tail, ok := r.consumeToolFinish(b.ToolUseID); ok {
					line += tail
				}
				if b.ToolUseID != "" {
					if m, ok := toolUseIDToMeta[b.ToolUseID]; ok && m.skillName != "" {
						line += r.colorize("  skill="+m.skillName, colorInfo)
					}
				}
				r.writeOut(line + "\r\n")
			}
		case query.EventContentBlockDelta:
			if ev.Delta == nil {
				continue
			}
			switch ev.Delta.Type {
			case query.ContentTypeText:
				if ev.Delta.Text != "" {
					formatter.Write(buf, ev.Delta.Text)
					assistantText.WriteString(ev.Delta.Text)
					formatter.FlushIncomplete(buf)
					flushBuf()
				}
			case query.ContentTypeToolUse:
				if m, ok := tools[ev.Index]; ok && ev.Delta.PartialJSON != "" {
					m.partial += ev.Delta.PartialJSON
					// 实时流式输出：对文件写入工具，实时显示正在生成的代码内容
					if isFileWriteTool(m.name) && !m.collapsed {
						r.streamFileWriteContent(m.toolUseID, m.partial, ev.Delta.PartialJSON)
					}
				}
			case query.ContentTypeThinking:
				if ev.Delta.Thinking == "" {
					continue
				}
				if r.Verbose {
					flushFmt()
					line := strings.ReplaceAll(ev.Delta.Thinking, "\n", "\r\n")
					r.writeOut(r.colorize(line, colorMuted))
				} else if !thinkingShown {
					thinkingShown = true
					flushFmt()
					r.writeOut(r.colorize("  "+r.gl().minor+"thinking…\r\n", colorMuted))
				}
			}
		case query.EventContentBlockStop:
			if m, ok := tools[ev.Index]; ok && m.partial != "" && !m.collapsed {
				// 清理流式输出状态
				if isFileWriteTool(m.name) {
					r.cleanupStreamState(m.toolUseID)
				}

				var input map[string]interface{}
				if err := json.Unmarshal([]byte(m.partial), &input); err == nil {
					if m.name == "Skill" || m.name == "skill" {
						if nm, ok2 := input["name"].(string); ok2 {
							m.skillName = nm
						}
					}
				}
				summaryMaxRunes := r.termWidth() - 30
				if summaryMaxRunes < 20 {
					summaryMaxRunes = 20
				}
				m.inputSummary = extractToolSummary(m.name, m.partial, summaryMaxRunes)

				// 对 bash 类工具：检测沙箱命令并美化显示
				formatted, sandboxed := formatSandboxBashSummary(m.name, m.inputSummary, summaryMaxRunes)
				if sandboxed {
					m.isSandboxed = true
					m.inputSummary = formatted
				}

				// 覆盖打印启动行：ANSI 上移一行 + 清行 + 重写含摘要的版本
				if m.summaryFlushed && m.inputSummary != "" {
					displayName := m.name
					marker := ""
					if strings.HasPrefix(m.name, "mcp__") {
						marker = r.colorize(" ⟨MCP⟩", colorInfo)
					}
					// 沙箱命令：显示 🔒 标记 + 高亮实际命令
					if m.isSandboxed {
						marker += r.colorize(" 🔒", colorSandboxTag)
					}
					stepLabel := r.colorize(fmt.Sprintf("  [%d] ", m.stepNum), colorStepNum)
					if r.useColor {
						summaryColor := colorSubtle
						if m.isSandboxed {
							summaryColor = colorSandboxCmd
						}
						r.writeOut("\x1b[1A\x1b[2K\r" +
							stepLabel +
							r.colorize("◌ ", colorInfo) +
							r.colorize(displayName, colorToolName) + marker +
							"  " + r.colorize(m.inputSummary, summaryColor) +
							"\r\n")
					} else {
						prefix := "    ↳ "
						if m.isSandboxed {
							prefix = "    🔒 "
						}
						r.writeOut(prefix + m.inputSummary + "\r\n")
					}
				}
			}
		case query.EventError:
			if ev.Error != nil {
				flushFmt()
				r.writeOut("\r\n" + r.colorize("  ✗ Error: "+ev.Error.Error(), colorError) + "\r\n")
			}
		}
	}
	flushFmt()
	r.writeOut("\r\n")

	d := <-done
	if d.err != nil {
		if errors.Is(d.err, context.Canceled) {
			r.mu.Lock()
			if userMsgIdx >= 0 && userMsgIdx < len(r.messages) {
				r.messages = r.messages[:userMsgIdx]
			}
			r.mu.Unlock()
			r.writeOut(r.colorize("  ── 已取消 ──\r\n", colorMuted))
		} else {
			r.writeOut(r.colorize(fmt.Sprintf("  ✗ %v\r\n", d.err), colorError))
		}
		return
	}

	// 把 assistant 响应纳入对话上下文
	r.mu.Lock()
	if d.res != nil && d.res.CompactedMessages != nil {
		r.messages = d.res.CompactedMessages
	} else if d.res != nil && d.res.Response != nil {
		r.messages = append(r.messages, *d.res.Response)
	} else if assistantText.Len() > 0 {
		r.messages = append(r.messages, query.NewTextMessage(query.RoleAssistant, assistantText.String()))
	}
	r.mu.Unlock()

	// 执行摘要尾行：统计 turns/耗时/token 用量
	if d.res != nil {
		elapsed := time.Since(startedAt).Truncate(time.Millisecond)
		if r.Verbose {
			usage := ""
			if d.res.Usage != nil {
				usage = fmt.Sprintf("  tokens: %d→%d", d.res.Usage.InputTokens, d.res.Usage.OutputTokens)
			}
			r.writeOut(r.colorize(
				fmt.Sprintf("  ── %d turns · %s · stop=%s%s ──\r\n",
					d.res.TurnCount, elapsed, d.res.StopReason, usage),
				colorMuted,
			))
		} else if toolStepCount > 0 {
			// 有工具调用时显示轻量统计行
			r.writeOut(r.colorize(
				fmt.Sprintf("  ── %d steps · %s ──\r\n", toolStepCount, formatElapsedCompact(elapsed)),
				colorMuted,
			))
		}
	}
}
