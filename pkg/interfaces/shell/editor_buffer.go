package shell

// 本文件聚合 Editor 的缓冲编辑与多行光标操作、历史导航、补全。
// 从 editor.go 拆出以提升可读性；逻辑保持不变。

// ---------- 缓冲编辑 ----------

func (e *Editor) insertRune(r rune) {
	if e.pos == len(e.buf) {
		e.buf = append(e.buf, r)
	} else {
		e.buf = append(e.buf, 0)
		copy(e.buf[e.pos+1:], e.buf[e.pos:])
		e.buf[e.pos] = r
	}
	e.pos++
	e.redraw()
}

func (e *Editor) backspace() {
	if e.pos == 0 {
		return
	}
	e.buf = append(e.buf[:e.pos-1], e.buf[e.pos:]...)
	e.pos--
	e.redraw()
}

func (e *Editor) deleteForward() {
	if e.pos >= len(e.buf) {
		return
	}
	e.buf = append(e.buf[:e.pos], e.buf[e.pos+1:]...)
	e.redraw()
}

func (e *Editor) killWordBackward() {
	if e.pos == 0 {
		return
	}
	i := e.pos
	for i > 0 && isWordSep(e.buf[i-1]) {
		i--
	}
	for i > 0 && !isWordSep(e.buf[i-1]) {
		i--
	}
	e.buf = append(e.buf[:i], e.buf[e.pos:]...)
	e.pos = i
	e.redraw()
}

func (e *Editor) moveWordLeft() {
	i := e.pos
	for i > 0 && isWordSep(e.buf[i-1]) {
		i--
	}
	for i > 0 && !isWordSep(e.buf[i-1]) {
		i--
	}
	e.pos = i
	e.redraw()
}

func (e *Editor) moveWordRight() {
	i := e.pos
	for i < len(e.buf) && isWordSep(e.buf[i]) {
		i++
	}
	for i < len(e.buf) && !isWordSep(e.buf[i]) {
		i++
	}
	e.pos = i
	e.redraw()
}

// ---------- 多行光标 ----------

// lineStart 返回 pos 所在行的起始索引（含）
func (e *Editor) lineStart(pos int) int {
	for i := pos - 1; i >= 0; i-- {
		if e.buf[i] == '\n' {
			return i + 1
		}
	}
	return 0
}

// lineEnd 返回 pos 所在行的末尾索引（不含 \n）
func (e *Editor) lineEnd(pos int) int {
	for i := pos; i < len(e.buf); i++ {
		if e.buf[i] == '\n' {
			return i
		}
	}
	return len(e.buf)
}

// lineNumber 当前光标所在行号（0 起）
func (e *Editor) lineNumber(pos int) int {
	n := 0
	for i := 0; i < pos && i < len(e.buf); i++ {
		if e.buf[i] == '\n' {
			n++
		}
	}
	return n
}

// totalLines 总行数（最少 1）
func (e *Editor) totalLines() int {
	n := 1
	for _, r := range e.buf {
		if r == '\n' {
			n++
		}
	}
	return n
}

// moveLine 上下移动一行，尽量保持列偏移
func (e *Editor) moveLine(delta int) {
	col := e.pos - e.lineStart(e.pos)
	if delta < 0 {
		// 移动到上一行同列
		ls := e.lineStart(e.pos)
		if ls == 0 {
			return
		}
		prevEnd := ls - 1 // 指向前一行的 \n
		prevStart := e.lineStart(prevEnd)
		newPos := prevStart + col
		if newPos > prevEnd {
			newPos = prevEnd
		}
		e.pos = newPos
	} else {
		le := e.lineEnd(e.pos)
		if le == len(e.buf) {
			return
		}
		nextStart := le + 1
		nextEnd := e.lineEnd(nextStart)
		newPos := nextStart + col
		if newPos > nextEnd {
			newPos = nextEnd
		}
		e.pos = newPos
	}
	e.redraw()
}

// ---------- 历史 ----------

func (e *Editor) historyPrev() {
	if e.history == nil {
		return
	}
	if e.mark == "" && len(e.buf) > 0 {
		e.mark = string(e.buf)
	}
	line, ok := e.history.Prev()
	if !ok {
		return
	}
	e.replaceLine(line)
}

func (e *Editor) historyNext() {
	if e.history == nil {
		return
	}
	line, ok := e.history.Next()
	if !ok {
		return
	}
	if line == "" {
		e.replaceLine(e.mark)
		e.mark = ""
		return
	}
	e.replaceLine(line)
}

func (e *Editor) replaceLine(line string) {
	e.setBuf(line)
	e.redraw()
}

// setBuf 把 buf 重置为 s 的内容，光标定位到末尾（不触发重绘）
func (e *Editor) setBuf(s string) {
	e.buf = append(e.buf[:0], []rune(s)...)
	e.pos = len(e.buf)
}

// ---------- 补全 ----------

func (e *Editor) complete() {
	if e.completer == nil {
		return
	}
	newLine, newPos := e.completer.Complete(string(e.buf), e.pos)
	if newLine == string(e.buf) && newPos == e.pos {
		return
	}
	e.setBuf(newLine)
	if newPos < 0 {
		newPos = 0
	}
	if newPos > len(e.buf) {
		newPos = len(e.buf)
	}
	e.pos = newPos
	e.redraw()
}
