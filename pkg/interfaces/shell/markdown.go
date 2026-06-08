package shell

import (
	"regexp"
	"strings"
)

// MarkdownRenderer 将一段完整的 Markdown 文本渲染为带 ANSI 转义的终端富文本。
//
// 与 StreamFormatter 的区别：
//   - StreamFormatter 面向「流式增量」输出，拿到 token 即逐行即时渲染，不做整体排版；
//   - MarkdownRenderer 面向「整篇一次性」渲染，掌握完整文本后可做终端宽度自适应换行，
//     并支持更完整的块级结构（段落合并、列表悬挂缩进、引用、代码块等）。
//
// 设计目标（对应需求）：
//   - 解析基础语法：标题 h1-h6、粗体、斜体、行内代码、代码块、有序/无序列表、链接、引用
//   - 行内转 ANSI：粗体、下划线（链接）、文本着色
//   - 终端宽度自适应换行：按「显示宽度」计算（正确处理 CJK 全角、跳过 ANSI 转义不计宽），
//     列表/引用换行后保持悬挂缩进对齐
//   - 代码块内保留原始格式且禁用行内转义解析（仅做整体着色/语法高亮）
//
// 用法：
//
//	r := NewMarkdownRenderer(width, "  ", "│ ")
//	out := r.Render(markdownText)
//	os.Stdout.WriteString(out)
type MarkdownRenderer struct {
	// width 终端可用列宽；<=0 表示不进行自动换行（让终端自行硬换行）
	width int
	// indent 每行最前面的视觉缩进
	indent string
	// bar 缩进之后的视觉边条（如 "│ "），用于和用户输入区分；为空则不渲染
	bar string
	// nl 行尾换行符；默认 "\n"，原始模式终端可通过 SetCRLF(true) 改为 "\r\n"
	nl string
}

// NewMarkdownRenderer 构造一个整篇 Markdown 渲染器。
//
// width<=0 表示不做宽度自适应换行；indent/bar 均可为空。
func NewMarkdownRenderer(width int, indent, bar string) *MarkdownRenderer {
	return &MarkdownRenderer{
		width:  width,
		indent: indent,
		bar:    bar,
		nl:     "\n",
	}
}

// SetCRLF 设置是否使用 \r\n 作为行尾（原始模式终端需要）。
func (r *MarkdownRenderer) SetCRLF(crlf bool) *MarkdownRenderer {
	if crlf {
		r.nl = "\r\n"
	} else {
		r.nl = "\n"
	}
	return r
}

// RenderMarkdown 便捷函数：用默认样式（无缩进/边条、按 width 换行）渲染一段 Markdown。
func RenderMarkdown(src string, width int) string {
	return NewMarkdownRenderer(width, "", "").Render(src)
}

// Render 渲染整篇 Markdown 文本，返回带 ANSI 转义、按终端宽度排版好的字符串。
func (r *MarkdownRenderer) Render(src string) string {
	// 归一化换行：去掉 \r，统一按 \n 切行
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")
	lines := strings.Split(src, "\n")

	var out strings.Builder
	i := 0
	prevBlank := true // 抑制开头多余空行
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// 1) 代码围栏：原样保留，禁用行内解析
		if isFenceLine(line) {
			i = r.renderFence(&out, lines, i)
			prevBlank = false
			continue
		}

		// 2) 空行：折叠连续空行为一个块间隔
		if trimmed == "" {
			if !prevBlank {
				out.WriteString(r.linePrefix())
				out.WriteString(r.nl)
			}
			prevBlank = true
			i++
			continue
		}
		prevBlank = false

		// 3) 标题
		if m := reHeader.FindStringSubmatch(line); m != nil {
			r.renderHeading(&out, m)
			i++
			continue
		}

		// 4) 引用：聚合连续的 > 行
		if reQuote.MatchString(line) {
			i = r.renderQuote(&out, lines, i)
			continue
		}

		// 5) 列表：聚合连续的列表项
		if isListLine(line) {
			i = r.renderList(&out, lines, i)
			continue
		}

		// 6) 普通段落：聚合连续的非空、非特殊行后整体换行
		i = r.renderParagraph(&out, lines, i)
	}

	return out.String()
}

// ---------------- 各块渲染 ----------------

// renderFence 渲染代码围栏块；返回处理到的下一行索引。
//
// 代码块内：逐行原样保留，禁用 markdown 行内解析；已知语言走轻量语法高亮，
// 否则整行灰显。不做宽度换行（保留原始格式）。
func (r *MarkdownRenderer) renderFence(out *strings.Builder, lines []string, start int) int {
	fence := strings.TrimSpace(lines[start])
	marker := "```"
	if strings.HasPrefix(fence, "~~~") {
		marker = "~~~"
	}
	lang := strings.TrimSpace(strings.TrimPrefix(fence, marker))

	pre := r.linePrefix()

	// 顶部围栏（dim 灰）
	out.WriteString(pre)
	out.WriteString(colorDim)
	out.WriteString(lines[start])
	out.WriteString(colorReset)
	out.WriteString(r.nl)

	i := start + 1
	for i < len(lines) {
		// 闭合围栏
		if strings.HasPrefix(strings.TrimSpace(lines[i]), marker) {
			out.WriteString(pre)
			out.WriteString(colorDim)
			out.WriteString(lines[i])
			out.WriteString(colorReset)
			out.WriteString(r.nl)
			return i + 1
		}
		// 代码内容行：原样保留 + 着色/高亮（绝不解析 markdown 行内语法）
		raw := lines[i]
		out.WriteString(pre)
		if hl := HighlightLine(raw, lang); hl != raw {
			out.WriteString(hl)
		} else {
			out.WriteString(colorCodeBlock)
			out.WriteString(raw)
			out.WriteString(colorReset)
		}
		out.WriteString(r.nl)
		i++
	}
	// 未闭合：到文件结束
	return i
}

// renderHeading 渲染 ATX 标题（h1-h6）。
func (r *MarkdownRenderer) renderHeading(out *strings.Builder, m []string) {
	level := len(m[1])
	color := "\x1b[1m" // H4+ 仅粗体
	if idx := level - 1; idx >= 0 && idx < len(headerColors) {
		color = headerColors[idx]
	}
	styled := color + m[1] + " " + applyInline(m[2]) + colorReset
	r.writeBlock(out, "", "", styled)
}

// renderQuote 渲染引用块（聚合连续 > 行）；返回下一行索引。
func (r *MarkdownRenderer) renderQuote(out *strings.Builder, lines []string, start int) int {
	i := start
	marker := colorDim + "▏ " + colorReset
	for i < len(lines) {
		m := reQuote.FindStringSubmatch(lines[i])
		if m == nil {
			break
		}
		styled := colorDim + applyInline(m[2]) + colorReset
		r.writeBlock(out, marker, marker, styled)
		i++
	}
	return i
}

// renderList 渲染列表块（聚合连续列表项）；返回下一行索引。
func (r *MarkdownRenderer) renderList(out *strings.Builder, lines []string, start int) int {
	i := start
	for i < len(lines) {
		line := lines[i]
		if !isListLine(line) {
			break
		}

		// checkbox（先于普通无序列表）
		if m := reCheckbox.FindStringSubmatch(line); m != nil {
			var box string
			if m[3] == "x" || m[3] == "X" {
				box = colorAccent + "[" + colorReset + colorString + "✓" + colorReset + colorAccent + "]" + colorReset
			} else {
				box = colorDim + "[ ]" + colorReset
			}
			first := m[1] + box + " "
			cont := m[1] + "    " // 与 "[ ] " 对齐
			r.writeBlock(out, first, cont, applyInline(m[4]))
			i++
			continue
		}

		// 无序列表
		if m := reListBullet.FindStringSubmatch(line); m != nil {
			first := m[1] + colorAccent + "•" + colorReset + " "
			cont := m[1] + "  " // 悬挂缩进，对齐到文本起点
			r.writeBlock(out, first, cont, applyInline(m[3]))
			i++
			continue
		}

		// 有序列表
		if m := reListNumber.FindStringSubmatch(line); m != nil {
			first := m[1] + colorAccent + m[2] + "." + colorReset + " "
			cont := m[1] + strings.Repeat(" ", len(m[2])+2) // 对齐到 "N. " 之后
			r.writeBlock(out, first, cont, applyInline(m[3]))
			i++
			continue
		}

		break
	}
	return i
}

// renderParagraph 渲染普通段落（聚合连续非空、非特殊行，软换行合并后整体排版）；返回下一行索引。
func (r *MarkdownRenderer) renderParagraph(out *strings.Builder, lines []string, start int) int {
	var parts []string
	i := start
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || isFenceLine(line) || reHeader.MatchString(line) ||
			reQuote.MatchString(line) || isListLine(line) {
			break
		}
		parts = append(parts, trimmed)
		i++
	}
	joined := strings.Join(parts, " ")
	r.writeBlock(out, "", "", applyInline(joined))
	return i
}

// ---------------- 通用块写出（含宽度自适应换行） ----------------

// writeBlock 把一段已渲染好的行内文本（styled）按终端宽度换行后写出。
//
//   - firstMarker：首行的行内标记（如 "• "、"1. "，已含 ANSI），紧跟 linePrefix 之后
//   - contMarker：续行（换行后的行）标记，用于悬挂缩进对齐；其可视宽度需与 firstMarker 一致
//   - styled：块正文（已含 ANSI），将按可用宽度自动换行
//
// 宽度预算 = width - linePrefix 可视宽 - contMarker 可视宽。width<=0 时不换行。
func (r *MarkdownRenderer) writeBlock(out *strings.Builder, firstMarker, contMarker, styled string) {
	pre := r.linePrefix()

	avail := 0 // 0 表示不换行
	if r.width > 0 {
		avail = r.width - visibleWidth(pre) - visibleWidth(contMarker)
		if avail < minWrapWidth {
			avail = minWrapWidth // 防止过窄导致排版异常
		}
	}

	wrapped := wrapANSI(styled, avail)
	for idx, ln := range wrapped {
		out.WriteString(pre)
		if idx == 0 {
			out.WriteString(firstMarker)
		} else {
			out.WriteString(contMarker)
		}
		out.WriteString(ln)
		out.WriteString(r.nl)
	}
}

// linePrefix 计算每行左侧的固定前缀（缩进 + dim 边条）。
func (r *MarkdownRenderer) linePrefix() string {
	if r.bar == "" {
		return r.indent
	}
	return r.indent + colorDim + r.bar + colorReset
}

// minWrapWidth 自适应换行时的最小可用宽度，避免极端窄宽下死循环/不可读。
const minWrapWidth = 8

// isListLine 判断是否为列表项行（无序 / 有序 / checkbox）。
func isListLine(line string) bool {
	return reCheckbox.MatchString(line) || reListBullet.MatchString(line) || reListNumber.MatchString(line)
}

// ---------------- ANSI 感知的宽度换行 ----------------

// reSGR 匹配 SGR（着色）转义序列，用于跨行追踪当前激活样式。
var reSGR = regexp.MustCompile("\x1b\\[[0-9;]*m")

// wrapUnit 换行算法中的最小单元：一个可见字符（rune）及其前导的 ANSI 转义。
type wrapUnit struct {
	pre string // 该 rune 之前累积的 ANSI 转义（零宽）
	r   rune   // 可见字符；r==0 表示仅有尾随转义、无字符
	w   int    // 该字符的显示列宽
	sp  bool   // 是否为可断行空白
}

// wrapANSI 对「已含 ANSI 转义」的字符串按可见宽度换行。
//
// 关键点：
//   - 计算宽度时跳过 ANSI 转义（复用 runeCellWidth 处理 CJK 全角=2）
//   - 优先在空白处断行（西文按词换行）；无空白可断时按字符硬断（适配 CJK / 超长词）
//   - 跨行时保持样式：断行处先 colorReset，下一行行首重新注入当前激活的 SGR，
//     避免样式「渗漏」到行尾换行符与左侧前缀
//
// width<=0 时不换行，原样返回单行。
func wrapANSI(s string, width int) []string {
	if width <= 0 || s == "" {
		return []string{s}
	}
	units := tokenizeWrapUnits(s)
	n := len(units)

	var lines []string
	carry := "" // 上一行结束时仍激活的 SGR，需在新行行首重新打开
	start := 0
	for start < n {
		col := 0
		lastBreak := -1 // 可断空白的下标：本行取 [start, lastBreak)，丢弃该空白
		end, nextStart := n, n
		j := start
		for ; j < n; j++ {
			u := units[j]
			// 已有内容且再放该字符会超宽 → 在此之前断行
			if col > 0 && col+u.w > width {
				if lastBreak >= 0 {
					end, nextStart = lastBreak, lastBreak+1
				} else {
					end, nextStart = j, j // 无空白可断：硬断
				}
				break
			}
			col += u.w
			if u.sp {
				lastBreak = j
			}
		}
		if j >= n {
			end, nextStart = n, n
		}

		seg, active := buildWrapSegment(units, start, end, carry)
		lines = append(lines, seg)
		carry = active

		// 跳过续行行首的空白
		for nextStart < n && units[nextStart].sp {
			nextStart++
		}
		start = nextStart
	}
	return lines
}

// tokenizeWrapUnits 把含 ANSI 的字符串拆为换行单元序列。
func tokenizeWrapUnits(s string) []wrapUnit {
	runes := []rune(s)
	var units []wrapUnit
	pre := ""
	for i := 0; i < len(runes); i++ {
		if runes[i] == 0x1b {
			esc, ni := consumeANSIEscape(runes, i)
			pre += esc
			i = ni
			continue
		}
		r := runes[i]
		units = append(units, wrapUnit{
			pre: pre,
			r:   r,
			w:   runeCellWidth(r),
			sp:  r == ' ' || r == '\t',
		})
		pre = ""
	}
	if pre != "" {
		// 尾随转义：作为零宽哨兵单元，保证不丢样式闭合
		units = append(units, wrapUnit{pre: pre, r: 0, w: 0})
	}
	return units
}

// buildWrapSegment 构造 units[start:end] 的一行字符串。
//
//   - carry：本行开头需先注入的激活 SGR（来自上一行的跨行样式）
//   - 返回该行字符串，以及本行结束时仍激活的 SGR（供下一行 carry）
//   - 若行尾仍有激活样式，追加 colorReset 防止渗漏到换行符与前缀
func buildWrapSegment(units []wrapUnit, start, end int, carry string) (string, string) {
	var b strings.Builder
	active := carry
	if carry != "" {
		b.WriteString(carry)
	}
	for k := start; k < end; k++ {
		u := units[k]
		if u.pre != "" {
			b.WriteString(u.pre)
			active = activeAfterSGR(active, u.pre)
		}
		if u.r != 0 {
			b.WriteRune(u.r)
		}
	}
	if active != "" {
		b.WriteString(colorReset)
	}
	return b.String(), active
}

// activeAfterSGR 给定当前激活的 SGR 和一段（可能含多个）SGR 转义，返回处理后的激活 SGR。
//
// 约定（与 applyInline 的产物一致）：行内样式不嵌套，遇 reset(\x1b[0m / \x1b[m) 清空，
// 否则以最后一个 SGR 为当前激活样式。
func activeAfterSGR(active, esc string) string {
	for _, code := range reSGR.FindAllString(esc, -1) {
		if code == "\x1b[0m" || code == "\x1b[m" {
			active = ""
		} else {
			active = code
		}
	}
	return active
}

// consumeANSIEscape 从 runes[i]（应为 ESC）开始吞掉一个完整的转义序列，
// 返回该序列字符串与最后消费到的下标。
//
//   - ESC [ ...（CSI）：吞到结束字节 0x40..0x7E
//   - ESC ] ...（OSC）：吞到 BEL(0x07) 或 ESC \
//   - 其它 ESC X：吞 X
func consumeANSIEscape(runes []rune, i int) (string, int) {
	if i+1 >= len(runes) {
		return string(runes[i]), i
	}
	switch runes[i+1] {
	case '[':
		j := i + 2
		for j < len(runes) && !(runes[j] >= 0x40 && runes[j] <= 0x7E) {
			j++
		}
		if j < len(runes) {
			return string(runes[i : j+1]), j
		}
		return string(runes[i:]), len(runes) - 1
	case ']':
		j := i + 2
		for j < len(runes) {
			if runes[j] == 0x07 {
				return string(runes[i : j+1]), j
			}
			if runes[j] == 0x1b && j+1 < len(runes) && runes[j+1] == '\\' {
				return string(runes[i : j+2]), j + 1
			}
			j++
		}
		return string(runes[i:]), len(runes) - 1
	default:
		return string(runes[i : i+2]), i + 1
	}
}
