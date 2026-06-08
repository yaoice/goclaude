package shell

import (
	"bytes"
	"fmt"
	"strings"
)

// 本文件聚合 Editor 的终端渲染：清屏、多行重绘、续行列宽计算与底层写入。
// 从 editor.go 拆出以提升可读性；逻辑保持不变。

// clearRendered 清掉之前 redraw 写下的所有可见行（光标移回起点）
func (e *Editor) clearRendered() {
	if e.lastLines <= 0 {
		// 仍可能在第一行末尾留下了光标；保险写一个 \r 清行
		e.writeRaw("\r\x1b[K")
		return
	}
	var b bytes.Buffer
	// 上次渲染时光标停在某一行；为简单起见，redraw 末尾把光标定位到最后一行末尾。
	// 这里先把光标移到首行：上移 (lastLines - 1)
	if e.lastLines > 1 {
		fmt.Fprintf(&b, "\x1b[%dA", e.lastLines-1)
	}
	b.WriteString("\r")
	// 逐行清并下移，最后再回到首行
	for i := 0; i < e.lastLines; i++ {
		b.WriteString("\x1b[2K")
		if i < e.lastLines-1 {
			b.WriteString("\x1b[1B")
		}
	}
	if e.lastLines > 1 {
		fmt.Fprintf(&b, "\x1b[%dA", e.lastLines-1)
	}
	b.WriteString("\r")
	e.writeRawBytes(b.Bytes())
}

// redraw 重绘整个编辑区
//
// 策略（多行 friendly）：
//  1. 清掉上次渲染的所有行
//  2. 第一行写 prompt + 第一段 buf；后续行写 contPrompt + 段
//  3. 计算光标应在的 (row, col)，把光标移过去
func (e *Editor) redraw() {
	e.clearRendered()

	// 把 buf 按 \n 切成段，注意最后可能没有 \n
	segments := []string{}
	cur := strings.Builder{}
	for _, r := range e.buf {
		if r == '\n' {
			segments = append(segments, cur.String())
			cur.Reset()
		} else {
			cur.WriteRune(r)
		}
	}
	segments = append(segments, cur.String())

	// 计算光标 (row, col)
	row := 0
	col := 0
	{
		pos := e.pos
		for i, r := range e.buf {
			if i == pos {
				break
			}
			if r == '\n' {
				row++
				col = 0
			} else {
				col += runeCellWidth(r)
			}
		}
	}

	// 写出
	var b bytes.Buffer
	for i, seg := range segments {
		if i == 0 {
			b.WriteString(e.prompt)
		} else {
			b.WriteString("\r\n")
			b.WriteString(e.contPrompt)
		}
		b.WriteString(seg)
	}
	// 把光标定位到 (row, col)
	totalRows := len(segments)
	// 当前光标在最后一行末尾；先回到第一行
	if totalRows > 1 {
		fmt.Fprintf(&b, "\x1b[%dA", totalRows-1)
	}
	b.WriteString("\r")
	if row > 0 {
		fmt.Fprintf(&b, "\x1b[%dB", row)
	}
	// 光标列 = prompt(0)/cont(>0) 的可见宽度 + col
	prefixCol := e.continuationCol(row)
	if c := prefixCol + col; c > 0 {
		fmt.Fprintf(&b, "\x1b[%dC", c)
	}
	e.writeRawBytes(b.Bytes())
	e.lastLines = totalRows
}

// continuationCol 返回第 row 行 prompt 可见宽度
func (e *Editor) continuationCol(row int) int {
	if row == 0 {
		return visibleWidth(e.prompt)
	}
	return visibleWidth(e.contPrompt)
}

func (e *Editor) writeRaw(s string)      { _, _ = e.out.Write([]byte(s)) }
func (e *Editor) writeRawBytes(b []byte) { _, _ = e.out.Write(b) }
