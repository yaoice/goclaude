package shell

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
)

// StreamFormatter 流式 Markdown 格式化器（行级状态机）
//
// 设计目标：
//   - 输入是不定长度的 token 文本片段；输出已经渲染好的、可直接 Write 到终端的字节
//   - 维护 "代码围栏" 状态：进入 ``` 后不做行内格式化，仅做整体着色
//   - 行首段（# / ## / - / 1.）自动加色
//   - 行内对 `code`、**bold**、*italic*、URL 做着色
//   - 把所有 \n 重写为 \r\n（终端原始模式下需要）
//   - 在每行前自动添加固定 prefix（如 "  │ "），形成视觉边条，便于和用户输入区分
//
// 用法：
//
//	f := NewStreamFormatter("  ", "│ ")
//	f.Write(buf, chunk1)
//	f.Write(buf, chunk2)
//	...
//	f.Flush(buf)   // 在流结束时调用，把最后一行（可能没 \n）刷出去
type StreamFormatter struct {
	// indent  视觉缩进（每行最前面）
	indent string
	// bar     视觉边条（紧跟 indent 之后）
	bar string

	// 行缓冲：一行未结束的文本累积在此，遇 \n 时整行处理
	lineBuf strings.Builder

	// inFence 是否正处于代码围栏内
	inFence bool

	// fenceLang 代码围栏语言（``` 后面的内容）
	fenceLang string

	// atLineStart 当前是否处于行首（决定是否注入 prefix）
	atLineStart bool

	// lastIncompleteLen 上次 FlushIncomplete 输出时的行长度（字节数）
	// 用于避免在同一行内容未变化时重复输出
	lastIncompleteLen int

	// hasIncompleteOut 是否已有不完整行被输出（FlushIncomplete 调用过）
	// 若为 true，flushLine 只需补 \r\n，不需再输出行内容
	hasIncompleteOut bool
}

// NewStreamFormatter 构造一个流式格式化器
//
// indent/bar 都可以为空字符串（不需要边条）。
func NewStreamFormatter(indent, bar string) *StreamFormatter {
	return &StreamFormatter{
		indent:      indent,
		bar:         bar,
		atLineStart: true,
	}
}

// Write 把一段文本喂给格式化器；渲染后的字节写入 out
//
// out 通常是 bytes.Buffer 或直接 os.Stdout。
func (f *StreamFormatter) Write(out *bytes.Buffer, chunk string) {
	for i := 0; i < len(chunk); i++ {
		c := chunk[i]
		if c == '\r' {
			// 单独 \r 忽略；后续 \n 正常处理
			continue
		}
		if c == '\n' {
			f.flushLine(out)
			continue
		}
		f.lineBuf.WriteByte(c)
	}
}

// Flush 把最后未结束的一行刷出
//
// 与 flushLine 不同：如果 FlushIncomplete 已输出过该行（hasIncompleteOut == true），
// 则 flushLine 只会补 \r\n，不会重复输出行内容。
//
// 末尾 \r\n 的处理：
//   - 如果 FlushIncomplete 已输出过（hasIncompleteOut == true），保留 \r\n（行已显示在终端上，需要换行）
//   - 否则移除 \r\n（用户没输入 \n 时我们不应擅自换行）
func (f *StreamFormatter) Flush(out *bytes.Buffer) {
	if f.lineBuf.Len() == 0 {
		return
	}

	// 先检查是否有不完整输出，再调用 flushLine（flushLine 会重置 hasIncompleteOut）
	hasIncomplete := f.hasIncompleteOut

	f.flushLine(out)

	// 如果 FlushIncomplete 已输出过，保留 \r\n（行需要结束）
	if hasIncomplete {
		return
	}

	// 否则移除末尾 \r\n
	b := out.Bytes()
	if len(b) >= 2 && b[len(b)-2] == '\r' && b[len(b)-1] == '\n' {
		out.Truncate(len(b) - 2)
	}
}

// FlushIncomplete 将 lineBuf 中尚未以 \n 结尾的内容增量写入 out，
// 用于实现流式输出效果。与 Flush 不同，它：
//   - 不添加尾随 \r\n
//   - 不重置 lineBuf（下次 Write 继续追加）
//   - 只输出新增的字符（增量输出），避免重复
//   - 当行内容未变化时跳过输出
//
// 增量输出策略（避免重复的关键）：
//   - 追踪上次输出的字节偏移量（lastIncompleteLen）
//   - 只输出从 lastIncompleteLen 到当前行尾的新增部分
//   - 不依赖 \r 或 \x1b[K（这些在用户终端可能不工作）
//   - 这样即使用户终端不兼容，也绝对不会重复输出
func (f *StreamFormatter) FlushIncomplete(out *bytes.Buffer) {
	if f.lineBuf.Len() == 0 {
		return
	}

	currentLine := f.lineBuf.String()

	// 行内容未变化时跳过，避免重复输出
	if len(currentLine) == f.lastIncompleteLen {
		return
	}

	// 只输出新增的部分（增量输出）
	if f.lastIncompleteLen < len(currentLine) {
		newContent := currentLine[f.lastIncompleteLen:]

		// prefix（只在第一次输出时写入）
		if f.lastIncompleteLen == 0 {
			out.WriteString(f.indent)
			if f.bar != "" {
				out.WriteString(colorDim)
				out.WriteString(f.bar)
				out.WriteString(colorReset)
			}
		}

		// 渲染并输出新增部分
		if f.inFence {
			if hl := HighlightLine(newContent, f.fenceLang); hl != newContent {
				out.WriteString(hl)
			} else {
				out.WriteString(colorCodeBlock)
				out.WriteString(newContent)
				out.WriteString(colorReset)
			}
		} else {
			out.WriteString(renderInlineLine(newContent))
		}

		// 更新上次输出的长度
		f.lastIncompleteLen = len(currentLine)
	}

	// 标记已有不完整行被输出，flushLine 需要补全 \r\n
	f.hasIncompleteOut = true
}

// ResetIncomplete 重置不完整输出状态，用于切换块类型时避免状态混乱
func (f *StreamFormatter) ResetIncomplete() {
	f.lastIncompleteLen = 0
	f.hasIncompleteOut = false
}

// flushLine 处理一整行（不含 \n），写出 prefix + 渲染结果 + \r\n
func (f *StreamFormatter) flushLine(out *bytes.Buffer) {
	line := f.lineBuf.String()
	f.lineBuf.Reset()

	// 如果已有不完整行被输出（FlushIncomplete 调用过），
	// 说明该行内容已显示在终端上，只需补全 \r\n 即可，避免重复输出行内容
	if f.hasIncompleteOut {
		out.WriteString("\r\n")
		f.hasIncompleteOut = false
		f.lastIncompleteLen = 0
		return
	}

	// prefix
	out.WriteString(f.indent)
	if f.bar != "" {
		out.WriteString(colorDim)
		out.WriteString(f.bar)
		out.WriteString(colorReset)
	}

	// 代码围栏切换
	if isFenceLine(line) {
		f.inFence = !f.inFence
		if f.inFence {
			f.fenceLang = strings.TrimPrefix(strings.TrimSpace(line), "```")
		} else {
			f.fenceLang = ""
		}
		out.WriteString(colorDim)
		out.WriteString(line)
		out.WriteString(colorReset)
		out.WriteString("\r\n")

		// 新行开始，重置增量输出状态
		f.lastIncompleteLen = 0
		return
	}

	if f.inFence {
		// 代码块内：若已知语言走轻量语法高亮；否则整行灰色兜底
		if hl := HighlightLine(line, f.fenceLang); hl != line {
			out.WriteString(hl)
		} else {
			out.WriteString(colorCodeBlock)
			out.WriteString(line)
			out.WriteString(colorReset)
		}
		out.WriteString("\r\n")

		// 新行开始，重置增量输出状态
		f.lastIncompleteLen = 0
		return
	}

	out.WriteString(renderInlineLine(line))
	out.WriteString("\r\n")

	// 新行开始，重置增量输出状态
	f.lastIncompleteLen = 0
}

// ---------------- 行内/行首渲染 ----------------

var (
	// 行首 ATX 标题
	reHeader = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	// 行首列表项（无序）
	reListBullet = regexp.MustCompile(`^(\s*)([-*+])\s+(.*)$`)
	// 行首列表项（有序）
	reListNumber = regexp.MustCompile(`^(\s*)(\d+)\.\s+(.*)$`)
	// 行首引用
	reQuote = regexp.MustCompile(`^(>+)\s*(.*)$`)
	// checkbox 列表："- [ ] xxx" / "- [x] xxx"
	reCheckbox = regexp.MustCompile(`^(\s*)([-*+])\s+\[([ xX])\]\s+(.*)$`)
	// 表格分隔行：" | --- | --- | "（至少含 ---）
	reTableSep = regexp.MustCompile(`^\s*\|?\s*:?-{3,}:?(\s*\|\s*:?-{3,}:?)+\s*\|?\s*$`)

	// 行内：` 行内代码 `
	reInlineCode = regexp.MustCompile("`([^`]+)`")
	// 行内：[文本](url) 链接（在裸 URL 之前处理，避免 url 被重复着色）
	reLink = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	// 行内：**粗体**
	reBold = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	// 行内：*斜体* / _斜体_（避免与列表 - 冲突，用 *）
	reItalic = regexp.MustCompile(`(?:^|[^*])\*([^*\s][^*]*?)\*(?:[^*]|$)`)
	// 行内：URL
	reURL = regexp.MustCompile(`https?://[^\s)]+`)
)

// headerColors 各级 ATX 标题的 ANSI 前缀（index = level-1）
var headerColors = [...]string{
	"\x1b[1;36m", // # 粗体青
	"\x1b[1;33m", // ## 粗体黄
	"\x1b[1;32m", // ### 粗体绿
}

// renderInlineLine 处理一行的着色
//
// 顺序：先识别行首结构（标题/列表/引用），再处理行内片段。
func renderInlineLine(line string) string {
	if line == "" {
		return ""
	}

	// 行首：ATX 标题
	if m := reHeader.FindStringSubmatch(line); m != nil {
		level := len(m[1])
		color := "\x1b[1m" // 默认仅粗体（H4+）
		if idx := level - 1; idx >= 0 && idx < len(headerColors) {
			color = headerColors[idx]
		}
		return color + m[1] + " " + applyInline(m[2]) + colorReset
	}

	// 行首：checkbox 列表（先于普通列表匹配）
	if m := reCheckbox.FindStringSubmatch(line); m != nil {
		mark := m[3]
		var box string
		if mark == "x" || mark == "X" {
			box = colorAccent + "[" + colorReset + colorString + "✓" + colorReset + colorAccent + "]" + colorReset
		} else {
			box = colorDim + "[ ]" + colorReset
		}
		return m[1] + box + " " + applyInline(m[4])
	}

	// 行首：无序列表
	if m := reListBullet.FindStringSubmatch(line); m != nil {
		return m[1] + colorAccent + "•" + colorReset + " " + applyInline(m[3])
	}

	// 行首：有序列表
	if m := reListNumber.FindStringSubmatch(line); m != nil {
		return m[1] + colorAccent + m[2] + "." + colorReset + " " + applyInline(m[3])
	}

	// 表格分隔行：用 dim 灰显示
	if reTableSep.MatchString(line) {
		return colorDim + line + colorReset
	}

	// 表格行（含 |）：把 | 染色
	if isTableRow(line) {
		return renderTableRow(line)
	}

	// 行首：引用
	if m := reQuote.FindStringSubmatch(line); m != nil {
		return colorDim + m[1] + " " + applyInline(m[2]) + colorReset
	}

	return applyInline(line)
}

// applyInline 行内格式化（顺序敏感：先 inline code，再 bold，再 url，最后 italic）
func applyInline(s string) string {
	if s == "" {
		return ""
	}
	// 1) 行内 code（用占位避免被后续规则误伤）
	type seg struct {
		placeholder string
		styled      string
	}
	var saved []seg
	idx := 0
	s = reInlineCode.ReplaceAllStringFunc(s, func(m string) string {
		body := m[1 : len(m)-1]
		styled := colorInlineCode + body + colorReset
		ph := codePlaceholder(idx)
		idx++
		saved = append(saved, seg{ph, styled})
		return ph
	})

	// 1.5) markdown 链接 [text](url)：渲染为"下划线着色文本 + dim 灰显 url"
	// 用占位保护，避免内部 url 被裸 URL 规则二次着色、文本被 bold/italic 误伤。
	s = reLink.ReplaceAllStringFunc(s, func(m string) string {
		sub := reLink.FindStringSubmatch(m)
		if sub == nil {
			return m
		}
		text, url := sub[1], sub[2]
		styled := colorLink + text + colorReset + colorDim + " (" + url + ")" + colorReset
		ph := codePlaceholder(idx)
		idx++
		saved = append(saved, seg{ph, styled})
		return ph
	})

	// 2) bold
	s = reBold.ReplaceAllStringFunc(s, func(m string) string {
		body := m[2 : len(m)-2]
		return "\x1b[1m" + body + colorReset
	})

	// 3) URL
	s = reURL.ReplaceAllStringFunc(s, func(m string) string {
		return "\x1b[4;34m" + m + colorReset
	})

	// 4) italic（保守：仅匹配明确边界）
	s = reItalic.ReplaceAllStringFunc(s, func(m string) string {
		// 因为正则带了上下文字符，需要保留首尾
		// 找到中间 *...*
		first := strings.Index(m, "*")
		last := strings.LastIndex(m, "*")
		if first < 0 || last <= first {
			return m
		}
		head := m[:first]
		body := m[first+1 : last]
		tail := m[last+1:]
		return head + "\x1b[3m" + body + colorReset + tail
	})

	// 还原 inline code 占位
	for _, sv := range saved {
		s = strings.Replace(s, sv.placeholder, sv.styled, 1)
	}
	return s
}

func codePlaceholder(i int) string {
	// 用 ASCII 单元分隔符 + 序号；几乎不可能在自然语言里出现
	return "\x1f__CODE_" + strconv.Itoa(i) + "__\x1f"
}

func isFenceLine(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "```")
}

// isTableRow 启发式：行至少有 2 个 |，且不在代码块内（caller 确保）
func isTableRow(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	// 避免误把 inline code 含的 | 当表格
	if strings.Contains(t, "`") {
		return false
	}
	return strings.Count(t, "|") >= 2
}

// renderTableRow 把 | 染色，单元格内做 inline 渲染
func renderTableRow(line string) string {
	// 拆分单元格
	// 简单按 | 切；保留前后是否含 |
	cells := strings.Split(line, "|")
	var sb strings.Builder
	for i, c := range cells {
		if i > 0 {
			sb.WriteString(colorDim + "│" + colorReset)
		}
		// 对每个 cell 做 inline 渲染
		sb.WriteString(applyInline(c))
	}
	return sb.String()
}

// ---------------- Markdown 渲染专用颜色常量 ----------------
//
// 与 repl_render.go 的语义色系统互补；这些色用于 Markdown 行内/行首结构着色。

const (
	colorAccent     = "\x1b[38;5;75m"  // 列表点/序号（品蓝）
	colorInlineCode = "\x1b[38;5;214m" // 行内代码（琥珀）
	colorCodeBlock  = "\x1b[38;5;252m" // 代码块内容（亮灰）
	colorString     = "\x1b[38;5;78m"  // 字符串/勾选 ✓（翠绿）
	colorLink       = "\x1b[4;38;5;75m" // 链接文本（下划线品蓝）
	colorAgentName  = "\x1b[1;38;5;75m" // subagent 名（粗品蓝）
)
