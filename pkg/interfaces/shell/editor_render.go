package shell

import (
	"bytes"
	"fmt"
	"strings"
)

// 本文件聚合 Editor 的终端渲染：清屏、多行重绘、续行列宽计算与底层写入。
//
// 核心难点：终端对超过列宽的长行会自动折行（wrap）成多个「物理行」，
// 而内容按 \n 划分得到「逻辑行」。渲染必须按物理行计算，否则清屏/光标
// 定位都会错位。为此引入 lastCursorRow 跟踪「上次 redraw 结束时光标所在
// 的物理行」，清屏时精确上移该行数即可回到渲染区顶部。

// clearRendered 清掉之前 redraw 写下的所有可见行
//
// 策略：从「上次光标所在物理行」上移到渲染区顶部（物理行 0），
// 再用 \x1b[J 清除从光标到屏幕底的所有内容（含终端自动折行产生的行）。
func (e *Editor) clearRendered() {
	var b bytes.Buffer
	if e.lastCursorRow > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", e.lastCursorRow)
	}
	b.WriteString("\r")
	b.WriteString("\x1b[0J") // erase from cursor to end of display
	e.writeRawBytes(b.Bytes())
	e.lastCursorRow = 0
	e.lastLines = 0
}

// redraw 重绘整个编辑区
//
// 流程：
//  1. clearRendered 清除上次渲染（回到渲染区顶部并清屏到底）
//  2. 写出版本指示行（可选）+ prompt + 各逻辑段（终端自动折行）
//  3. 按物理行/列计算光标目标位置，从内容末尾移动光标过去
//  4. 记录光标所在物理行（lastCursorRow）供下次 clearRendered 使用
func (e *Editor) redraw() {
	termW := e.termWidth()
	e.clearRendered()

	segments := litSegments(e.buf)

	// 物理布局：光标物理行列、内容末尾物理行、总物理行
	cursorRow, cursorCol, endRow, totalRows := e.physLayout(termW)

	var b bytes.Buffer

	// 版本指示行（短，不折行，占 1 物理行）
	if e.HasVersions() {
		tag := "\x1b[2m[原始]\x1b[0m"
		if e.showingEnhanced {
			tag = "\x1b[1m[优化]\x1b[0m"
		}
		b.WriteString(tag)
		b.WriteString("\r\n")
	}

	// 写出 prompt + 内容（终端按 termW 自动折行）
	for i, seg := range segments {
		if i == 0 {
			b.WriteString(e.prompt)
		} else {
			b.WriteString("\r\n")
			b.WriteString(e.contPrompt)
		}
		b.WriteString(seg)
	}

	// 此刻物理光标在内容末尾（物理行 endRow）。移动到目标 (cursorRow, cursorCol)。
	if up := endRow - cursorRow; up > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", up)
	}
	b.WriteString("\r")
	if cursorCol > 0 {
		fmt.Fprintf(&b, "\x1b[%dC", cursorCol)
	}

	e.writeRawBytes(b.Bytes())
	e.lastCursorRow = cursorRow
	e.lastLines = totalRows
}

// physLayout 模拟终端折行，计算物理布局。
//
// 返回：
//   - cursorRow, cursorCol: 逻辑光标在终端中的物理行（0 起，含版本行）与列（含 prompt 宽度）
//   - endRow:               写完全部内容后光标所在的物理行
//   - totalRows:            内容占用的总物理行数
//
// 采用「延迟折行」（deferred wrap）模型：刚好填满末列的字符不立即换行，
// 下一个字符才换行——与 xterm/iTerm/gnome-terminal 等主流终端一致。
func (e *Editor) physLayout(termW int) (cursorRow, cursorCol, endRow, totalRows int) {
	if termW < 2 {
		termW = 2
	}
	segments := litSegments(e.buf)
	pos := e.pos

	// 版本行占 1 物理行
	versionRows := 0
	if e.HasVersions() {
		versionRows = 1
	}

	physRow := versionRows
	physCol := 0
	charIdx := 0
	cursorFound := false
	// 默认光标在输入区起点（prompt 之后）
	cursorRow = versionRows
	cursorCol = e.prefixWidth(0)

	for si, seg := range segments {
		prefixW := e.prefixWidth(si)
		if si == 0 {
			physCol = prefixW
		} else {
			physRow++ // 段间的 \r\n
			physCol = prefixW
		}

		runes := []rune(seg)
		for ri := 0; ri <= len(runes); ri++ {
			// 在写入第 ri 个字符之前，检查光标是否落在此处
			if !cursorFound && charIdx == pos {
				cursorRow = physRow
				cursorCol = physCol
				cursorFound = true
			}
			if ri == len(runes) {
				break
			}
			r := runes[ri]
			cw := runeCellWidth(r)
			if physCol+cw > termW {
				physRow++
				physCol = 0
			}
			physCol += cw
			charIdx++
		}
		// 段间的 \n 字符占一个 buf 索引
		if si < len(segments)-1 {
			charIdx++
		}
	}
	if !cursorFound {
		cursorRow = physRow
		cursorCol = physCol
	}
	endRow = physRow
	totalRows = physRow + 1
	return
}

// termWidth 获取终端列宽（无终端时回退 80）
func (e *Editor) termWidth() int {
	if e.term != nil {
		w, _ := e.term.Size()
		if w > 0 {
			return w
		}
	}
	return 80
}

// prefixWidth 返回第 segIdx 逻辑段起始的 prompt 可见宽度
func (e *Editor) prefixWidth(segIdx int) int {
	if segIdx == 0 {
		return visibleWidth(e.prompt)
	}
	return visibleWidth(e.contPrompt)
}

// litSegments 把 rune 切片按 \n 切成字符串段
func litSegments(buf []rune) []string {
	segments := []string{}
	cur := strings.Builder{}
	for _, r := range buf {
		if r == '\n' {
			segments = append(segments, cur.String())
			cur.Reset()
		} else {
			cur.WriteRune(r)
		}
	}
	segments = append(segments, cur.String())
	return segments
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
