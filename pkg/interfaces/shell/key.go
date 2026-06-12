package shell

// Key 表示一个解析后的按键事件
type Key struct {
	Type KeyType
	Rune rune // 仅当 Type == KeyRune 有效
}

// KeyType 按键种类
type KeyType int

const (
	KeyUnknown KeyType = iota

	// 可打印字符（Rune 字段有效）
	KeyRune

	// 控制键
	KeyEnter      // Enter / Return（\r 或 \n）
	KeyAltEnter   // Alt/Meta+Enter → 插入换行
	KeyShiftEnter // Shift+Enter → 插入换行（部分终端报告为 ESC[13;2u 或 CSI ~）
	KeyBackspace  // Backspace
	KeyDelete     // Delete
	KeyTab        // Tab
	KeyShiftTab   // Shift+Tab → 切换 permission mode
	KeyEsc        // 单独的 ESC

	// 光标
	KeyLeft
	KeyRight
	KeyUp
	KeyDown
	KeyHome
	KeyEnd
	KeyAltLeft  // Alt+← / Ctrl+← 单词跳跃
	KeyAltRight // Alt+→ / Ctrl+→

	// Ctrl-* 组合
	KeyCtrlA  // 行首
	KeyCtrlE  // 行尾
	KeyCtrlB  // 左
	KeyCtrlF  // 右
	KeyCtrlK  // 删至行尾
	KeyCtrlU  // 删至行首
	KeyCtrlW  // 删除前一个单词
	KeyCtrlL  // 清屏
	KeyCtrlC  // 中断
	KeyCtrlD  // EOF / 退出
	KeyCtrlR  // 历史反向搜索（暂未实现完整功能，先占位）
	KeyCtrlO  // 切换 transcript 全屏只读模式
	KeyCtrlG  // 提示词优化版本切换（原始 ↔ 优化）
	KeyCtrlXE // Ctrl+X Ctrl+E：调用外部编辑器
)

// String 仅用于调试日志
func (k Key) String() string {
	switch k.Type {
	case KeyRune:
		return string(k.Rune)
	case KeyEnter:
		return "<Enter>"
	case KeyAltEnter:
		return "<Alt-Enter>"
	case KeyShiftEnter:
		return "<Shift-Enter>"
	case KeyBackspace:
		return "<Backspace>"
	case KeyDelete:
		return "<Delete>"
	case KeyTab:
		return "<Tab>"
	case KeyShiftTab:
		return "<Shift-Tab>"
	case KeyEsc:
		return "<Esc>"
	case KeyLeft:
		return "<Left>"
	case KeyRight:
		return "<Right>"
	case KeyUp:
		return "<Up>"
	case KeyDown:
		return "<Down>"
	case KeyHome:
		return "<Home>"
	case KeyEnd:
		return "<End>"
	case KeyAltLeft:
		return "<Alt-Left>"
	case KeyAltRight:
		return "<Alt-Right>"
	case KeyCtrlA:
		return "<C-a>"
	case KeyCtrlE:
		return "<C-e>"
	case KeyCtrlB:
		return "<C-b>"
	case KeyCtrlF:
		return "<C-f>"
	case KeyCtrlK:
		return "<C-k>"
	case KeyCtrlU:
		return "<C-u>"
	case KeyCtrlW:
		return "<C-w>"
	case KeyCtrlL:
		return "<C-l>"
	case KeyCtrlC:
		return "<C-c>"
	case KeyCtrlD:
		return "<C-d>"
	case KeyCtrlR:
		return "<C-r>"
	case KeyCtrlO:
		return "<C-o>"
	case KeyCtrlG:
		return "<C-g>"
	case KeyCtrlXE:
		return "<C-x C-e>"
	}
	return "<Unknown>"
}
