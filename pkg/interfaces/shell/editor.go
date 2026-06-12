package shell

import (
	"errors"
	"io"
)

// Editor 一个基于 ANSI 转义序列的多行编辑器（readline-lite）。
//
// 设计目标：在不依赖外部 readline 库的前提下，支持
//   - 光标左右移动 / Home / End / Ctrl-A / Ctrl-E / Alt-←/→（按词）
//   - Backspace / Delete / Ctrl-W / Ctrl-U / Ctrl-K
//   - 历史导航 ↑/↓
//   - Tab 补全（外部注入 Completer）
//   - Ctrl-L 清屏
//   - Ctrl-C 中断当前行（清空并返回 ErrInterrupted）
//   - Ctrl-D 在空行时返回 io.EOF（用于优雅退出）
//   - Esc 取消（返回 ErrCancel；用于 chat:cancel）
//   - 多行输入：Shift+Enter / Alt+Enter / 行尾 `\` + Enter 插入换行；Enter 提交
//   - Bracketed paste：识别 ESC[200~ ... ESC[201~，粘贴期间换行不触发提交
//   - Shift+Tab → KeyShiftTab（暴露给上层用于切换 permission mode）
//
// Editor 不接管信号；信号处理在 REPL 主循环里。
//
// 实现按职责拆分到多个同包文件：
//   - editor.go         核心：结构体、构造、ReadLine 主循环
//   - editor_buffer.go  缓冲编辑、多行光标、历史、补全
//   - editor_paste.go   bracketed-paste 处理与占位符展开
//   - editor_render.go  清屏与多行重绘
//   - editor_keys.go    按键解析、反向搜索、字符宽度工具
type Editor struct {
	term      *Terminal
	out       io.Writer
	history   *History
	completer Completer

	// 当前缓冲（可能含 \n 形成多行）
	buf  []rune
	pos  int    // 光标位置（字符索引）
	mark string // ↑ 之前的"草稿"，越过末尾时恢复

	// 渲染
	prompt     string
	contPrompt string // 续行提示符
	lastLines  int    // 上次 redraw 写出的物理行总数（含折行）
	// lastCursorRow 上次 redraw 结束时光标所在的物理行（0 起，含版本行）。
	// clearRendered 依此上移回到渲染区顶部，是正确处理长行折行的关键。
	lastCursorRow int

	// 状态
	inPaste bool

	// 粘贴占位符：在 bracketed paste 期间收集字符到 pasteBuf；
	// 粘贴结束时若内容"较大"则存入 pasteRefs[id] 并在 buf 插入字面量
	// 占位符 [Pasted text #N +M lines]；提交时由 expandPasteRefs 还原。
	pasteBuf     []rune
	pasteRefs    map[int]string
	pasteCounter int

	// OnShiftTab 当用户按下 Shift+Tab 时触发；为 nil 则忽略
	// （用于 REPL 切换 permission mode 而无需中断当前编辑）
	OnShiftTab func()

	// OnCtrlO 当用户按下 Ctrl+O 时触发；为 nil 则忽略
	// （用于切换 transcript 全屏只读模式）
	OnCtrlO func()

	// OnExternalEditor 当用户按下 Ctrl+X Ctrl+E 时触发
	//
	// 回调约定：返回新的 buf 内容（已编辑后的文本）；若返回 ""（且 second=false）
	// 表示用户取消，不修改原 buf。
	//
	// 回调内部应负责暂离 raw 模式 + 重绘 prompt 等。
	OnExternalEditor func(current string) (newText string, replace bool)

	// 提示词优化版本管理（Ctrl+G 切换，/enhance-prompt 命令使用）
	origText        string // 原始提示词文本
	enhancedText    string // 优化后的文本
	showingEnhanced bool   // 当前是否显示优化版本
	preserveContent bool   // ReadLine 开始时保留当前缓冲区（用于 /enhance-prompt 回填后继续编辑）
}

// Completer 按当前输入与光标位置给出补全建议
//
//	line 为完整缓冲；pos 为光标处字符索引。
//	返回值：替换后的整行 + 新光标位置；返回值与原值相同视为"无补全"。
type Completer interface {
	Complete(line string, pos int) (newLine string, newPos int)
}

// ErrInterrupted 用户按下 Ctrl+C，当前输入行被丢弃
var ErrInterrupted = errors.New("interrupted")

// ErrCancel 用户按下 Esc 取消（chat:cancel）
//
// REPL 主循环空闲态收到此错误时丢弃当前输入并继续。
var ErrCancel = errors.New("cancelled")

// NewEditor 构造行编辑器
//
// out 用于写转义序列；history/completer 可为 nil。
func NewEditor(term *Terminal, out io.Writer, history *History, completer Completer) *Editor {
	if out == nil {
		out = io.Discard
	}
	return &Editor{
		term:       term,
		out:        out,
		history:    history,
		completer:  completer,
		contPrompt: "  ",
		pasteRefs:  map[int]string{},
	}
}

// SetCompleter 替换补全器
func (e *Editor) SetCompleter(c Completer) { e.completer = c }

// SetContinuationPrompt 设置续行提示符（默认两个空格）
func (e *Editor) SetContinuationPrompt(p string) { e.contPrompt = p }

// EnableBracketedPaste 向终端写入开启/关闭 bracketed paste 序列
//
// 进入 REPL 时调用 EnableBracketedPaste(true)；退出前 false。
func (e *Editor) EnableBracketedPaste(enable bool) {
	if enable {
		e.writeRaw("\x1b[?2004h")
	} else {
		e.writeRaw("\x1b[?2004l")
	}
}

// ReadLine 读取一行（阻塞）
//
// 返回值约定：
//   - 正常 Enter → (line, nil)
//   - Ctrl-C    → ("", ErrInterrupted)
//   - Esc       → ("", ErrCancel)
//   - Ctrl-D 空行 → ("", io.EOF)
//   - 底层 IO 错误 → ("", err)
//
// 调用前要求 term 已进入原始模式。
func (e *Editor) ReadLine(prompt string) (string, error) {
	e.prompt = prompt
	// 如果 preserveContent 为 true，保留当前缓冲区内容（由 /enhance-prompt 等回填场景设置）
	if e.preserveContent {
		e.preserveContent = false
		// 保留版本信息以便 Ctrl+G 切换
	} else {
		e.buf = e.buf[:0]
		e.ClearVersions()
	}
	e.pos = 0
	e.mark = ""
	e.lastLines = 0
	e.lastCursorRow = 0
	if e.history != nil {
		e.history.Reset()
	}

	// 初次渲染
	e.redraw()

	for {
		key, err := e.readKey()
		if err != nil {
			return "", err
		}

		// 在粘贴模式中：除粘贴结束外，所有按键都按字面插入（包括换行）
		if e.inPaste {
			// 粘贴期间：把字符收集到 pasteBuf；不直接 insertRune
			// 这样可以延迟到粘贴结束时一次性决定（直接展开 / 占位符）
			switch key.Type {
			case KeyEnter, KeyShiftEnter, KeyAltEnter:
				e.pasteBuf = append(e.pasteBuf, '\n')
			case KeyRune:
				e.pasteBuf = append(e.pasteBuf, key.Rune)
			case KeyTab:
				e.pasteBuf = append(e.pasteBuf, '\t')
				// 其它键忽略
			}
			continue
		}

		switch key.Type {
		case KeyEnter:
			// 行尾 `\` 续行：删除 `\` 并插入换行
			if e.pos > 0 && e.buf[e.pos-1] == '\\' {
				e.buf = append(e.buf[:e.pos-1], e.buf[e.pos:]...)
				e.pos--
				e.insertRune('\n')
				continue
			}
			// 提交
			e.endRender()
			return string(e.buf), nil

		case KeyAltEnter, KeyShiftEnter:
			// 显式换行
			e.insertRune('\n')

		case KeyCtrlC:
			e.writeRaw("^C\r\n")
			e.lastLines = 0
			e.lastCursorRow = 0
			return "", ErrInterrupted

		case KeyEsc:
			// 上层取消语义
			e.endRender()
			return "", ErrCancel

		case KeyCtrlD:
			if len(e.buf) == 0 {
				e.writeRaw("\r\n")
				e.lastLines = 0
				e.lastCursorRow = 0
				return "", io.EOF
			}
			e.deleteForward()

		case KeyRune:
			e.insertRune(key.Rune)

		case KeyBackspace:
			e.backspace()
		case KeyDelete:
			e.deleteForward()

		case KeyLeft, KeyCtrlB:
			if e.pos > 0 {
				e.pos--
				e.redraw()
			}
		case KeyRight, KeyCtrlF:
			if e.pos < len(e.buf) {
				e.pos++
				e.redraw()
			}
		case KeyAltLeft:
			e.moveWordLeft()
		case KeyAltRight:
			e.moveWordRight()
		case KeyHome, KeyCtrlA:
			e.pos = e.lineStart(e.pos)
			e.redraw()
		case KeyEnd, KeyCtrlE:
			e.pos = e.lineEnd(e.pos)
			e.redraw()

		case KeyCtrlK:
			end := e.lineEnd(e.pos)
			if e.pos < end {
				e.buf = append(e.buf[:e.pos], e.buf[end:]...)
				e.redraw()
			}
		case KeyCtrlU:
			start := e.lineStart(e.pos)
			if start < e.pos {
				e.buf = append(e.buf[:start], e.buf[e.pos:]...)
				e.pos = start
				e.redraw()
			}
		case KeyCtrlW:
			e.killWordBackward()

		case KeyUp:
			// 多行模式下：若不在首行，则上移光标；否则历史
			if line := e.lineNumber(e.pos); line > 0 {
				e.moveLine(-1)
			} else {
				e.historyPrev()
			}
		case KeyDown:
			lines := e.totalLines()
			if line := e.lineNumber(e.pos); line < lines-1 {
				e.moveLine(+1)
			} else {
				e.historyNext()
			}

		case KeyTab:
			e.complete()
		case KeyShiftTab:
			if e.OnShiftTab != nil {
				e.OnShiftTab()
				e.redraw()
			}

		case KeyCtrlL:
			// 清屏并重绘
			e.writeRaw("\x1b[2J\x1b[H")
			e.lastLines = 0
			e.lastCursorRow = 0
			e.redraw()

		case KeyCtrlR:
			if err := e.reverseSearch(); err != nil {
				return "", err
			}
		case KeyCtrlO:
			if e.OnCtrlO != nil {
				// 暂存当前 buf；让 REPL 全屏渲染 transcript
				e.clearRendered()
				e.lastLines = 0
				e.OnCtrlO()
				// 回调结束后重新渲染 prompt 区
				e.redraw()
			}
		case KeyCtrlG:
			if e.HasVersions() {
				e.ToggleVersion()
				e.redraw()
			}
		case KeyCtrlXE:
			if e.OnExternalEditor != nil {
				// 暂离编辑区，让回调启动外部编辑器
				e.clearRendered()
				e.lastLines = 0
				newText, replace := e.OnExternalEditor(string(e.buf))
				if replace {
					e.setBuf(newText)
				}
				e.redraw()
			}
		}
	}
}

// endRender 在提交/Esc 离开时把光标移到末尾并写换行，便于上层接管输出
func (e *Editor) endRender() {
	// 光标移到 buf 末尾对应的位置后另起一行
	e.pos = len(e.buf)
	e.redraw()
	e.writeRaw("\r\n")
	e.lastLines = 0
	e.lastCursorRow = 0
	// 提交/取消时清除版本存储
	e.ClearVersions()
}

// SetVersions 设置原始和优化版本（由 /enhance-prompt 命令调用）
//
// 设置后自动切到优化版本显示，并标记 preserveContent 使 ReadLine 保留当前缓冲。
func (e *Editor) SetVersions(orig, enhanced string) {
	e.origText = orig
	e.enhancedText = enhanced
	e.showingEnhanced = true
	e.preserveContent = true
	e.setBuf(enhanced)
}

// ToggleVersion 在原始版本和优化版本之间切换显示
func (e *Editor) ToggleVersion() {
	if !e.HasVersions() {
		return
	}
	e.showingEnhanced = !e.showingEnhanced
	if e.showingEnhanced {
		e.setBuf(e.enhancedText)
	} else {
		e.setBuf(e.origText)
	}
}

// HasVersions 检查是否有可切换的版本
func (e *Editor) HasVersions() bool {
	return e.origText != "" && e.enhancedText != ""
}

// ClearVersions 清除版本存储（每次 ReadLine 开始时自动调用）
func (e *Editor) ClearVersions() {
	e.origText = ""
	e.enhancedText = ""
	e.showingEnhanced = false
}

// GetCurrentVersionState 返回当前显示的版本标签（用于状态栏）
// 返回值: "enhanced", "original", ""（无版本）
func (e *Editor) GetCurrentVersionState() string {
	if !e.HasVersions() {
		return ""
	}
	if e.showingEnhanced {
		return "enhanced"
	}
	return "original"
}
