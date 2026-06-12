package shell

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

// 本文件聚合 Editor 的按键解析（含 CSI / SS3 / bracketed-paste / kitty 协议）、
// 反向增量搜索，以及字符宽度估算工具。从 editor.go 拆出，逻辑保持不变。

// reverseSearch 进入 bash 风格的反向增量搜索
//
// 行为：
//   - 显示 `(reverse-i-search)`q'`：'<match>` 的提示行
//   - 普通可打印字符 → 追加到 query 并重新搜索
//   - Backspace        → 移除 query 末尾一个字符并重搜
//   - 再按 Ctrl+R      → 跳到上一条更早的命中
//   - Enter            → 把当前 match 写回 e.buf，回到主循环（用户可继续编辑）
//   - Esc / Ctrl+G     → 取消，恢复进入搜索前的 buf
//   - Ctrl+C           → 视作 Esc（取消）
//
// 退出搜索后由 ReadLine 主循环接管；本函数只返回底层 IO 错误。
func (e *Editor) reverseSearch() error {
	if e.history == nil || e.history.Len() == 0 {
		return nil
	}

	// 备份原状态，便于取消时恢复
	origBuf := append([]rune(nil), e.buf...)
	origPos := e.pos

	var (
		query    []rune
		matched  string
		matchIdx = e.history.Len() // 下一次从这里向上搜
		hasMatch bool
	)

	doSearch := func(fromIdx int) {
		hit, idx, ok := e.history.SearchPrev(string(query), fromIdx)
		if ok {
			matched = hit
			matchIdx = idx
			hasMatch = true
		} else {
			matched = ""
			hasMatch = false
		}
	}

	render := func() {
		e.clearRendered()
		var b bytes.Buffer
		// 提示
		hint := "(reverse-i-search)`"
		if !hasMatch && len(query) > 0 {
			hint = "(failed reverse-i-search)`"
		}
		b.WriteString(hint)
		b.WriteString(string(query))
		b.WriteString("': ")
		display := matched
		if display == "" {
			display = string(origBuf)
		}
		b.WriteString(display)
		// 把光标定位到 match 中"query 出现位置"的开头
		// 简单起见：移到行尾
		e.writeRawBytes(b.Bytes())
		e.lastLines = 1
	}

	exitWith := func(accept bool) {
		// 写换行隔开
		e.writeRaw("\r\n")
		e.lastLines = 0
		e.lastCursorRow = 0
		if accept && hasMatch {
			e.setBuf(matched)
		} else {
			// 取消：恢复
			e.buf = append(e.buf[:0], origBuf...)
			e.pos = origPos
		}
		// 重绘普通 prompt
		e.redraw()
	}

	render()

	for {
		key, err := e.readKey()
		if err != nil {
			// 出错时恢复并向上传递
			e.buf = append(e.buf[:0], origBuf...)
			e.pos = origPos
			return err
		}
		switch key.Type {
		case KeyEnter:
			exitWith(true)
			return nil
		case KeyEsc, KeyCtrlC:
			exitWith(false)
			return nil
		case KeyCtrlR:
			// 跳到下一条更早的匹配
			doSearch(matchIdx)
			render()
		case KeyBackspace:
			if len(query) > 0 {
				query = query[:len(query)-1]
				matchIdx = e.history.Len()
				doSearch(matchIdx)
				render()
			}
		case KeyRune:
			query = append(query, key.Rune)
			matchIdx = e.history.Len()
			doSearch(matchIdx)
			render()
		case KeyLeft, KeyRight, KeyUp, KeyDown,
			KeyHome, KeyEnd, KeyCtrlA, KeyCtrlE,
			KeyCtrlK, KeyCtrlU, KeyCtrlW, KeyTab,
			KeyAltEnter, KeyShiftEnter, KeyShiftTab:
			// 这些按键意味着用户想退出搜索并使用其它编辑动作
			// 行为对齐 bash readline：先 accept 当前 match，然后让该按键
			// 在主循环中再生效一次。go shell 简化为 accept 后忽略一次按键。
			exitWith(hasMatch)
			return nil
		case KeyCtrlD:
			// 在搜索中按 Ctrl+D 等同于取消
			exitWith(false)
			return nil
		}
	}
}

// readKey 读一个按键事件
//
// 解析顺序：
//  1. 单字节控制字符（Ctrl-* / Enter / Backspace / Tab 等）
//  2. ESC 序列（CSI / SS3 / Alt+X / Bracketed paste 200~/201~ / Shift-Enter / Shift-Tab）
//  3. UTF-8 多字节字符
func (e *Editor) readKey() (Key, error) {
	b, err := e.term.ReadByte()
	if err != nil {
		return Key{}, err
	}

	switch b {
	case 0x01:
		return Key{Type: KeyCtrlA}, nil
	case 0x02:
		return Key{Type: KeyCtrlB}, nil
	case 0x03:
		return Key{Type: KeyCtrlC}, nil
	case 0x04:
		return Key{Type: KeyCtrlD}, nil
	case 0x05:
		return Key{Type: KeyCtrlE}, nil
	case 0x06:
		return Key{Type: KeyCtrlF}, nil
	case 0x07:
		return Key{Type: KeyCtrlG}, nil
	case 0x08, 0x7f:
		return Key{Type: KeyBackspace}, nil
	case 0x09:
		return Key{Type: KeyTab}, nil
	case 0x0a, 0x0d:
		return Key{Type: KeyEnter}, nil
	case 0x0b:
		return Key{Type: KeyCtrlK}, nil
	case 0x0c:
		return Key{Type: KeyCtrlL}, nil
	case 0x0f:
		return Key{Type: KeyCtrlO}, nil
	case 0x12:
		return Key{Type: KeyCtrlR}, nil
	case 0x15:
		return Key{Type: KeyCtrlU}, nil
	case 0x17:
		return Key{Type: KeyCtrlW}, nil
	case 0x18:
		// Ctrl+X 起始的两键序列（仅识别 Ctrl+X Ctrl+E）
		next, err := e.term.ReadByte()
		if err != nil {
			return Key{}, err
		}
		if next == 0x05 { // Ctrl+E
			return Key{Type: KeyCtrlXE}, nil
		}
		// 其它后续字节忽略（与 readline 类似：未知 prefix 序列丢弃）
		return Key{Type: KeyUnknown}, nil
	case 0x1b:
		return e.readEscapeSeq()
	}

	if b < 0x80 {
		return Key{Type: KeyRune, Rune: rune(b)}, nil
	}
	return e.readUTF8(b)
}

// readEscapeSeq 解析 ESC 后续序列
//
// 已识别：
//
//	ESC               单独 → KeyEsc
//	ESC <ascii>       Alt+X
//	ESC [ A/B/C/D     ↑/↓/→/←
//	ESC [ H/F         Home/End
//	ESC [ Z           Shift+Tab
//	ESC [ N ~         功能键扩展（含 Home/End/Delete）
//	ESC [ 200 ~       Bracketed paste 开始 → 设置 inPaste=true 并返回 KeyUnknown（外层忽略）
//	ESC [ 201 ~       Bracketed paste 结束 → inPaste=false 并返回 KeyUnknown
//	ESC [ 1 ; 2 A     Shift+↑（少见；不展开）
//	ESC [ 13 ; N u    kitty 协议 Shift/Alt+Enter
//	ESC O H/F         Home/End
//	ESC O P..S        F1..F4
//
// 兜底：未识别 → KeyEsc
func (e *Editor) readEscapeSeq() (Key, error) {
	b2, err := e.term.ReadByte()
	if err != nil {
		// 单独 ESC
		return Key{Type: KeyEsc}, nil
	}
	switch b2 {
	case '[':
		return e.readCSI()
	case 'O':
		b3, err := e.term.ReadByte()
		if err != nil {
			return Key{Type: KeyEsc}, nil
		}
		switch b3 {
		case 'H':
			return Key{Type: KeyHome}, nil
		case 'F':
			return Key{Type: KeyEnd}, nil
		}
		return Key{Type: KeyEsc}, nil
	case 0x7f, 0x08:
		// Alt+Backspace → 当作 Ctrl+W 删词
		return Key{Type: KeyCtrlW}, nil
	case 0x0d, 0x0a:
		return Key{Type: KeyAltEnter}, nil
	case 'b':
		return Key{Type: KeyAltLeft}, nil
	case 'f':
		return Key{Type: KeyAltRight}, nil
	}
	// 其它（含 Alt+<char>）暂未细分；按字符插入会破坏快捷键语义，统一回退到 Esc
	return Key{Type: KeyEsc}, nil
}

// readCSI 已读到 ESC [
func (e *Editor) readCSI() (Key, error) {
	// 读到结束字节（0x40..0x7E）；最多读 16 字节防卡死
	var params [16]byte
	n := 0
	var final byte
	for n < len(params) {
		b, err := e.term.ReadByte()
		if err != nil {
			return Key{Type: KeyEsc}, nil
		}
		if b >= 0x40 && b <= 0x7e {
			final = b
			break
		}
		params[n] = b
		n++
	}
	if final == 0 {
		return Key{Type: KeyEsc}, nil
	}
	paramStr := string(params[:n])

	switch final {
	case 'A':
		return Key{Type: KeyUp}, nil
	case 'B':
		return Key{Type: KeyDown}, nil
	case 'C':
		// 可能含修饰：1;5C (Ctrl+→) / 1;3C (Alt+→)
		if hasModifier(paramStr, 5) || hasModifier(paramStr, 3) {
			return Key{Type: KeyAltRight}, nil
		}
		return Key{Type: KeyRight}, nil
	case 'D':
		if hasModifier(paramStr, 5) || hasModifier(paramStr, 3) {
			return Key{Type: KeyAltLeft}, nil
		}
		return Key{Type: KeyLeft}, nil
	case 'H':
		return Key{Type: KeyHome}, nil
	case 'F':
		return Key{Type: KeyEnd}, nil
	case 'Z':
		return Key{Type: KeyShiftTab}, nil
	case '~':
		// 形如 ESC [ N ~ 或 ESC [ N ; M ~
		// 只取第一个数字
		num := firstInt(paramStr)
		switch num {
		case 1, 7:
			return Key{Type: KeyHome}, nil
		case 3:
			return Key{Type: KeyDelete}, nil
		case 4, 8:
			return Key{Type: KeyEnd}, nil
		case 200:
			e.inPaste = true
			e.pasteBuf = e.pasteBuf[:0]
			return Key{Type: KeyUnknown}, nil
		case 201:
			e.inPaste = false
			e.finishPaste()
			return Key{Type: KeyUnknown}, nil
		}
		return Key{Type: KeyEsc}, nil
	case 'u':
		// kitty keyboard protocol：ESC [ <code> ; <mods> u
		num := firstInt(paramStr)
		mods := secondInt(paramStr)
		if num == 13 {
			// Enter + 修饰
			if mods >= 2 { // 任何修饰都视作换行（Shift=2 / Alt=3 / Ctrl=5）
				if mods == 2 {
					return Key{Type: KeyShiftEnter}, nil
				}
				return Key{Type: KeyAltEnter}, nil
			}
			return Key{Type: KeyEnter}, nil
		}
		return Key{Type: KeyEsc}, nil
	}
	return Key{Type: KeyEsc}, nil
}

// hasModifier 判断 CSI 参数中是否带某个修饰位（如 5=Ctrl，3=Alt，2=Shift）
//
//	"1;5"  → 修饰 5
//	"1;3"  → 修饰 3
func hasModifier(paramStr string, mod int) bool {
	parts := strings.Split(paramStr, ";")
	if len(parts) < 2 {
		return false
	}
	return firstInt(parts[1]) == mod
}

// firstInt 取参数串中首个数字（"123;4" → 123）
func firstInt(s string) int {
	n := 0
	found := false
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
		found = true
	}
	if !found {
		return -1
	}
	return n
}

// secondInt 取参数串中第二个分号后的数字（"1;5" → 5）
func secondInt(s string) int {
	idx := strings.Index(s, ";")
	if idx < 0 {
		return -1
	}
	return firstInt(s[idx+1:])
}

// readUTF8 已读首字节 b，按 UTF-8 编码长度读完剩余字节
func (e *Editor) readUTF8(b byte) (Key, error) {
	var (
		size int
		buf  [4]byte
	)
	switch {
	case b&0xe0 == 0xc0:
		size = 2
	case b&0xf0 == 0xe0:
		size = 3
	case b&0xf8 == 0xf0:
		size = 4
	default:
		return Key{Type: KeyUnknown}, nil
	}
	buf[0] = b
	for i := 1; i < size; i++ {
		nb, err := e.term.ReadByte()
		if err != nil {
			return Key{}, err
		}
		buf[i] = nb
	}
	r, _ := utf8.DecodeRune(buf[:size])
	if r == utf8.RuneError {
		return Key{Type: KeyUnknown}, nil
	}
	return Key{Type: KeyRune, Rune: r}, nil
}

// isWordSep 单词分隔符（空白与少量标点）
func isWordSep(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '/', '\\', '.', ',', ';', ':', '(', ')', '[', ']', '{', '}', '"', '\'':
		return true
	}
	return false
}

// runeCellWidth 一个 rune 在终端的显示列宽（粗略）
//
//   - 控制字符 0
//   - ASCII 1
//   - CJK / 全角 / emoji 2
//   - 其它 1
func runeCellWidth(r rune) int {
	if r < 0x20 || r == 0x7f {
		return 0
	}
	if r < 0x80 {
		return 1
	}
	switch {
	case r >= 0x1100 && r <= 0x115F: // Hangul Jamo
		return 2
	case r >= 0x2E80 && r <= 0x303E: // CJK
		return 2
	case r >= 0x3041 && r <= 0x33FF:
		return 2
	case r >= 0x3400 && r <= 0x4DBF:
		return 2
	case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
		return 2
	case r >= 0xA000 && r <= 0xA4CF:
		return 2
	case r >= 0xAC00 && r <= 0xD7A3: // Hangul Syllables
		return 2
	case r >= 0xF900 && r <= 0xFAFF:
		return 2
	case r >= 0xFE30 && r <= 0xFE4F:
		return 2
	case r >= 0xFF00 && r <= 0xFF60: // 全角拉丁
		return 2
	case r >= 0xFFE0 && r <= 0xFFE6:
		return 2
	case r >= 0x1F300 && r <= 0x1FAFF: // emoji 大区
		return 2
	case r >= 0x20000 && r <= 0x2FFFD:
		return 2
	}
	return 1
}

// visibleWidth 估算可见宽度（剥离 ANSI 转义）
//
// 会跳过 ANSI 转义序列：
//   - ESC `[` ...（CSI）：吃到结束字节 (0x40..0x7E)
//   - ESC `]` ...（OSC）：吃到 BEL(0x07) 或 ESC `\`
//   - 单字节 ESC + X：吃 X
func visibleWidth(s string) int {
	w := 0
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r != 0x1b {
			w += runeCellWidth(r)
			continue
		}
		if i+1 >= len(runes) {
			return w
		}
		next := runes[i+1]
		switch next {
		case '[':
			j := i + 2
			for j < len(runes) && !(runes[j] >= 0x40 && runes[j] <= 0x7E) {
				j++
			}
			i = j
		case ']':
			j := i + 2
			for j < len(runes) {
				if runes[j] == 0x07 {
					break
				}
				if runes[j] == 0x1b && j+1 < len(runes) && runes[j+1] == '\\' {
					j++
					break
				}
				j++
			}
			i = j
		default:
			i++
		}
	}
	return w
}
