package shell

import (
	"fmt"
	"strconv"
	"strings"
)

// 本文件聚合 Editor 的 bracketed-paste 处理与占位符展开，以及 PrintAboveLine。
// 从 editor.go 拆出以提升可读性；逻辑保持不变。

// finishPaste 在 bracketed paste 结束时决定如何把 pasteBuf 注入 buf
//
// 阈值规则：
//   - 字符数 ≥ 200 或 行数 ≥ 3 → 占位符 + 存到 pasteRefs
//   - 否则 → 直接逐字符 insertRune
//
// 占位符格式：[Pasted text #N +M lines]，与 src `history.ts:formatPastedTextRef` 对齐。
func (e *Editor) finishPaste() {
	if len(e.pasteBuf) == 0 {
		return
	}
	chars := len(e.pasteBuf)
	lines := 1
	for _, r := range e.pasteBuf {
		if r == '\n' {
			lines++
		}
	}
	const charThreshold = 200
	if chars < charThreshold && lines < 3 {
		for _, r := range e.pasteBuf {
			e.insertRune(r)
		}
		e.pasteBuf = e.pasteBuf[:0]
		return
	}
	e.pasteCounter++
	id := e.pasteCounter
	e.pasteRefs[id] = string(e.pasteBuf)
	placeholder := fmt.Sprintf("[Pasted text #%d +%d lines]", id, lines)
	for _, r := range placeholder {
		e.insertRune(r)
	}
	e.pasteBuf = e.pasteBuf[:0]
}

// ExpandPasteRefs 把 line 中的占位符替换为原始粘贴内容
//
// 调用时机：ReadLine 返回后由 REPL 在提交给 LLM 之前调用。
func (e *Editor) ExpandPasteRefs(line string) string {
	if len(e.pasteRefs) == 0 || !strings.Contains(line, "[Pasted text #") {
		return line
	}
	out := strings.Builder{}
	i := 0
	for i < len(line) {
		idx := strings.Index(line[i:], "[Pasted text #")
		if idx < 0 {
			out.WriteString(line[i:])
			break
		}
		out.WriteString(line[i : i+idx])
		j := i + idx + len("[Pasted text #")
		nStart := j
		for j < len(line) && line[j] >= '0' && line[j] <= '9' {
			j++
		}
		if nStart == j {
			out.WriteString(line[i+idx:])
			break
		}
		closeIdx := strings.Index(line[j:], "]")
		if closeIdx < 0 {
			out.WriteString(line[i+idx:])
			break
		}
		num, _ := strconv.Atoi(line[nStart:j])
		if ref, ok := e.pasteRefs[num]; ok {
			out.WriteString(ref)
		} else {
			out.WriteString(line[i+idx : j+closeIdx+1])
		}
		i = j + closeIdx + 1
	}
	return out.String()
}

// ResetPasteRefs 在 ReadLine 提交后清理粘贴引用（避免跨轮泄漏）
func (e *Editor) ResetPasteRefs() {
	e.pasteRefs = map[int]string{}
	e.pasteCounter = 0
}

// PrintAboveLine 暂存当前编辑，把内容打印到 prompt 之上，再恢复编辑
//
// 用于补全多候选列表/外部消息（如 SIGWINCH 重排提示）。
// content 末尾自动补 \r\n。
func (e *Editor) PrintAboveLine(content string) {
	// 清当前 prompt 区
	e.clearRendered()
	e.lastLines = 0
	// 写消息
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	// 把 \n 转 \r\n 适配原始模式
	out := strings.ReplaceAll(content, "\r\n", "\n")
	out = strings.ReplaceAll(out, "\n", "\r\n")
	e.writeRaw(out)
	// 重绘 prompt 区
	e.redraw()
}
