package shell

// style.go —— 终端兼容性适配：字形降级（UTF-8 / ASCII）与按宽度截断。
//
// 目标：让新的输出样式在非理想终端下也能优雅显示：
//   - 非 UTF-8 / 老终端：把 ╭ │ ╰ ⎿ → · 等 Unicode 字形换成 ASCII 兜底，避免乱码；
//   - 窄终端：按实际列宽截断长行（CJK 安全），避免换行错位把面板"撑破"；
//   - 无色（NO_COLOR）：状态不再仅靠颜色传达，渲染层在无色时补文字标识（见 repl/transcript）。

import (
	"os"
	"strings"
)

// glyphSet 一组渲染字符；按终端能力在 Unicode / ASCII 间切换。
type glyphSet struct {
	panelTop    string // 面板上角 ╭
	panelRail   string // 面板竖线 │
	panelBottom string // 面板下角 ╰
	result      string // 结果/产出连接符 ⎿
	toolCall    string // 工具调用 →
	minor       string // 次要信息 ·
	ellipsis    string // 截断省略号 …
}

var unicodeGlyphs = glyphSet{
	panelTop:    "╭ ",
	panelRail:   "│ ",
	panelBottom: "╰ ",
	result:      "⎿ ",
	toolCall:    "→ ",
	minor:       "· ",
	ellipsis:    "…",
}

// asciiGlyphs 纯 ASCII 兜底；每个 token 仍是 2 列宽，保持与 Unicode 版对齐一致。
var asciiGlyphs = glyphSet{
	panelTop:    "+ ",
	panelRail:   "| ",
	panelBottom: "+ ",
	result:      "\\ ",
	toolCall:    "> ",
	minor:       "- ",
	ellipsis:    "...",
}

// detectUseASCII 探测终端是否需要 ASCII 兜底字形。
//
// 规则（启发式，可被显式开关覆盖）：
//   - 环境变量 GOCLAUDE_ASCII=1 / true → 强制 ASCII；
//   - 否则看 locale（LC_ALL > LC_CTYPE > LANG）：首个非空变量含 "utf-8"/"utf8" → 支持
//     Unicode；含其它（如 C / POSIX / ISO-8859-1）→ ASCII 兜底；
//   - 完全没有 locale 信息 → 保守假设现代终端支持 UTF-8。
func detectUseASCII() bool {
	if v := os.Getenv("GOCLAUDE_ASCII"); v == "1" || strings.EqualFold(v, "true") {
		return true
	}
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		v := os.Getenv(key)
		if v == "" {
			continue
		}
		lv := strings.ToLower(v)
		return !(strings.Contains(lv, "utf-8") || strings.Contains(lv, "utf8"))
	}
	return false
}

// gl 返回当前 REPL 应使用的字形集（按 useASCII 切换）。
func (r *REPL) gl() glyphSet {
	if r != nil && r.useASCII {
		return asciiGlyphs
	}
	return unicodeGlyphs
}

// termWidth 当前终端列宽；无终端 / 测试场景回退 80。
func (r *REPL) termWidth() int {
	if r == nil || r.Term == nil {
		return 80
	}
	w, _ := r.Term.Size()
	if w <= 0 {
		return 80
	}
	return w
}

// fitLine 把单行内容按**显示宽度**截断到 maxCells（CJK 安全），超出加省略号。
//
// 与 truncOneLine 的区别：truncOneLine 按 rune 数截断，CJK 混排时实际占格会翻倍；
// 这里按 runeCellWidth 累加，确保不超过终端实际列数。ASCII 模式用 "..." 作省略号。
func (r *REPL) fitLine(s string, maxCells int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if maxCells < 4 || visibleWidth(s) <= maxCells {
		return s
	}
	ell := r.gl().ellipsis
	budget := maxCells - visibleWidth(ell)
	if budget < 1 {
		budget = 1
	}
	w := 0
	var b strings.Builder
	for _, ch := range s {
		cw := runeCellWidth(ch)
		if w+cw > budget {
			break
		}
		b.WriteRune(ch)
		w += cw
	}
	return b.String() + ell
}

// fitResult 针对"结果/摘要"长行的便捷封装：在硬上限 hardMax 与终端可用宽度间取较小者。
//
// reserve 预留给行首前缀（缩进 + 字形 + 标签等）的列数，避免内容 + 前缀超出终端。
func (r *REPL) fitResult(s string, hardMax, reserve int) string {
	avail := r.termWidth() - reserve
	if avail < 20 {
		avail = 20 // 极窄终端兜底，至少留一点可读空间
	}
	return r.fitLine(s, min(hardMax, avail))
}
