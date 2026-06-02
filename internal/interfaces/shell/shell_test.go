package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryAppendDedupe(t *testing.T) {
	h := NewHistory("", 0)
	h.Append("a")
	h.Append("a") // 连续重复 → 去重
	h.Append("b")
	h.Append("") // 空串忽略
	got := h.Snapshot()
	want := []string{"a", "b"}
	if len(got) != len(want) {
		t.Fatalf("len=%d want %d (got=%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("entry[%d]=%q want %q", i, got[i], want[i])
		}
	}
}

func TestHistoryPrevNext(t *testing.T) {
	h := NewHistory("", 0)
	h.Append("first")
	h.Append("second")
	h.Append("third")
	h.Reset()

	// Prev 三次：third, second, first，再 Prev 仍 first
	v, ok := h.Prev()
	if !ok || v != "third" {
		t.Fatalf("Prev#1 got (%q,%v)", v, ok)
	}
	v, _ = h.Prev()
	if v != "second" {
		t.Fatalf("Prev#2 got %q", v)
	}
	v, _ = h.Prev()
	if v != "first" {
		t.Fatalf("Prev#3 got %q", v)
	}
	v, _ = h.Prev()
	if v != "first" {
		t.Fatalf("Prev#4 (top) got %q", v)
	}

	// Next 两次回到 third
	if v, _ := h.Next(); v != "second" {
		t.Fatalf("Next#1 got %q", v)
	}
	if v, _ := h.Next(); v != "third" {
		t.Fatalf("Next#2 got %q", v)
	}
	// 再 Next 越界 → 空串（恢复草稿语义）
	if v, _ := h.Next(); v != "" {
		t.Fatalf("Next#3 got %q (want empty)", v)
	}
}

func TestHistoryLoadSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "h")

	h := NewHistory(path, 0)
	h.Append("alpha")
	h.Append("beta")
	if err := h.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	h2 := NewHistory(path, 0)
	if err := h2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := h2.Snapshot()
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("loaded=%v", got)
	}
	_ = os.Remove(path)
}

func TestSlashCompleterUniqueMatch(t *testing.T) {
	c := NewSlashCompleter([]string{"/help", "/history", "/exit"}, nil)
	// 输入 "/he" 只匹配 /help（/history 第三字符是 i）→ 唯一 → 补成 "/help "
	line, pos := c.Complete("/he", 3)
	if line != "/help " || pos != len("/help ") {
		t.Fatalf("/he unique case got (%q,%d)", line, pos)
	}

	// 输入 "/h" 匹配 /help、/history → 公共前缀 "/h"，等于 token → 不补全
	line, pos = c.Complete("/h", 2)
	if line != "/h" || pos != 2 {
		t.Fatalf("/h ambiguous case got (%q,%d)", line, pos)
	}

	// 输入 "/hi" → 唯一匹配 /history → 补全为 "/history "
	line, pos = c.Complete("/hi", 3)
	if line != "/history " || pos != len("/history ") {
		t.Fatalf("/hi unique case got (%q,%d)", line, pos)
	}

	// 输入 "/help" → 唯一匹配，添加空格
	line, pos = c.Complete("/help", 5)
	if line != "/help " || pos != len("/help ") {
		t.Fatalf("exact match got (%q,%d)", line, pos)
	}
}

func TestSlashCompleterCommonPrefix(t *testing.T) {
	c := NewSlashCompleter([]string{"/clear", "/clone", "/close"}, nil)
	// "/c" → 公共前缀 "/cl" → 补到 /cl
	line, pos := c.Complete("/c", 2)
	if line != "/cl" || pos != 3 {
		t.Fatalf("common prefix got (%q,%d)", line, pos)
	}
}

func TestVisibleWidthStripsAnsi(t *testing.T) {
	got := visibleWidth("\x1b[1;36m›\x1b[0m ")
	// "›"(U+203A) 在 BMP 但非 CJK/全角 → 按 1 算；空格 1 → 共 2
	if got != 2 {
		t.Fatalf("visibleWidth=%d want 2", got)
	}

	// 中文 + 英文混合：「你好」=4，"abc"=3 → 7
	got = visibleWidth("\x1b[31m你好\x1b[0mabc")
	if got != 7 {
		t.Fatalf("mixed visibleWidth=%d want 7", got)
	}
}

func TestKeyStringSmoke(t *testing.T) {
	cases := []KeyType{
		KeyEnter, KeyAltEnter, KeyShiftEnter,
		KeyBackspace, KeyTab, KeyShiftTab, KeyEsc,
		KeyLeft, KeyRight, KeyUp, KeyDown, KeyHome, KeyEnd,
		KeyAltLeft, KeyAltRight,
		KeyCtrlA, KeyCtrlE, KeyCtrlK, KeyCtrlU, KeyCtrlW, KeyCtrlL,
		KeyCtrlC, KeyCtrlD, KeyCtrlR,
	}
	for _, kt := range cases {
		if s := (Key{Type: kt}).String(); s == "" {
			t.Fatalf("empty string for KeyType=%d", kt)
		}
	}
}

// helpRenderForTest 构造一个最小化 REPL（关闭颜色），调用 renderHelp 抓取文本，
// 与生产路径用同一份渲染逻辑（避免 helper 漂移）。
func helpRenderForTest(t *testing.T, withCustom bool) string {
	t.Helper()
	r := &REPL{useColor: false}
	if withCustom {
		// 直接用 unexported 字段构造一个内含两条命令的 CustomCommands；不走磁盘加载，
		// 测试纯渲染。同 package 下可以访问 byName / names。
		cc := NewCustomCommands()
		cc.byName["git:commit"] = &CustomCommand{
			Name:         "git:commit",
			Description:  "Generate a conventional commit message",
			ArgumentHint: "[scope]",
			Source:       "user",
		}
		cc.byName["plan"] = &CustomCommand{
			Name:        "plan",
			Description: "Produce a plan before edits",
			Source:      "project",
		}
		cc.rebuildNames()
		r.CustomCommands = cc
	}
	return r.renderHelp()
}

// /help 必须包含每个核心 section + 内置命令清单（规范名）+ 至少一个快捷键。
func TestRenderHelp_StructuralSectionsPresent(t *testing.T) {
	text := helpRenderForTest(t, false)

	mustContain := []string{
		// Header
		"GoClaude",
		"understands your codebase",
		// Section titles（与 renderHelp 中 writeSection 一致）
		"▌ Input syntax",
		"▌ Commands",
		"▌ Shortcuts",
		// 部分内置命令（按规范名）
		"/help",
		"/exit",
		"/skills",
		"/agents",
		"/mcp",
		"/tools",
		"/permissions",
		// 快捷键样本
		"Shift-Tab",
		"Ctrl-X Ctrl-E",
		// Footer
		"For more help",
	}
	for _, want := range mustContain {
		if !strings.Contains(text, want) {
			t.Fatalf("renderHelp missing %q\n--- help ---\n%s", want, text)
		}
	}

	// 不允许出现旧版残留（中文 section 标题、box 框、过时提示）
	mustNotContain := []string{
		"USAGE", "GENERAL", "CAPABILITIES", "TEAM COLLABORATION",
		"╭─", "╰─",
		"团队管理工具尚未实现",
		"尚未实现",
	}
	for _, bad := range mustNotContain {
		if strings.Contains(text, bad) {
			t.Fatalf("renderHelp must not contain stale token %q\n--- help ---\n%s", bad, text)
		}
	}
}

// 不带 CustomCommands 时不应出现 "Custom commands" 节，避免空标题污染。
func TestRenderHelp_NoCustomSectionWhenEmpty(t *testing.T) {
	text := helpRenderForTest(t, false)
	if strings.Contains(text, "Custom commands") {
		t.Fatalf("renderHelp should hide custom section when registry is empty\n%s", text)
	}
}

// 注入 CustomCommands 后 Custom 节应出现，命令按字母序，且带 (source) 后缀。
func TestRenderHelp_CustomCommandsRenderedSorted(t *testing.T) {
	text := helpRenderForTest(t, true)
	if !strings.Contains(text, "▌ Custom commands (2)") {
		t.Fatalf("custom section header missing\n%s", text)
	}
	// 命令名与 source 后缀
	for _, want := range []string{
		"/git:commit", "[scope]", "(user)",
		"/plan", "(project)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("custom render missing %q\n%s", want, text)
		}
	}
	// 字母序：g 在 p 之前
	gIdx := strings.Index(text, "/git:commit")
	pIdx := strings.Index(text, "/plan")
	if gIdx <= 0 || pIdx <= 0 || gIdx >= pIdx {
		t.Fatalf("custom commands not sorted: gIdx=%d pIdx=%d\n%s", gIdx, pIdx, text)
	}
}

// /help 必须用 CRLF；命令行必须有至少 2 个空格作为列分隔。
func TestRenderHelp_UsesCRLFAndAlignedRows(t *testing.T) {
	text := helpRenderForTest(t, true)
	if strings.Contains(text, "\n") && !strings.Contains(text, "\r\n") {
		t.Fatalf("renderHelp must use CRLF for raw terminal mode")
	}
	if strings.Contains(strings.ReplaceAll(text, "\r\n", ""), "\n") {
		t.Fatalf("renderHelp contains bare LF")
	}

	lines := strings.Split(strings.TrimSuffix(text, "\r\n"), "\r\n")
	checked := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// 仅检查命令/快捷键样行（以 / 开头，或常见 shortcut keyword 开头）
		isCmdRow := strings.HasPrefix(trimmed, "/")
		isShortcutRow := false
		for _, kw := range []string{"Tab", "Shift-", "Ctrl-", "Alt-", "↑", "←", "Esc"} {
			if strings.HasPrefix(trimmed, kw) {
				isShortcutRow = true
				break
			}
		}
		if !isCmdRow && !isShortcutRow {
			continue
		}
		// 列分隔至少 2 个空格（helpRow 保底 pad=2）
		if !strings.Contains(line, "  ") {
			t.Fatalf("expected aligned row with two-space separator, got %q", line)
		}
		checked++
	}
	if checked < 15 {
		t.Fatalf("checked only %d aligned rows; help likely missing command/shortcut rows\n%s", checked, text)
	}
}

// builtinCommandNames（用于 Tab 补全）必须保留所有 case 别名，否则 "/q" 等无法 hint。
func TestBuiltinCommandNames_IncludesAliases(t *testing.T) {
	names := builtinCommandNames()
	for _, want := range []string{"/quit", "/q", "/reset", "/usage", "/?", "/help", "/exit"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("builtinCommandNames missing alias %q; got %v", want, names)
		}
	}
}

// /help 必须包含 Permission modes 节，并在节内列出 4 种模式（与 helpPermissionModes 同步）。
func TestRenderHelp_PermissionModesSection(t *testing.T) {
	text := helpRenderForTest(t, false)
	if !strings.Contains(text, "▌ Permission modes") {
		t.Fatalf("/help must show Permission modes section\n%s", text)
	}
	for _, want := range []string{"default", "acceptEdits", "plan", "bypass"} {
		if !strings.Contains(text, want) {
			t.Fatalf("/help permission modes must contain %q\n%s", want, text)
		}
	}
	// bypass 行必须显式带"unsafe"标识，让用户在 /help 里就能看到风险提示
	if !strings.Contains(text, "unsafe") {
		t.Fatalf("/help should mark bypass as unsafe\n%s", text)
	}
}

// helpPermissionModes 与 src 设计的 cycle 顺序一致：default → acceptEdits → plan → bypass。
// 这是 Shift-Tab 切换的唯一真相源；与 root.go 的 modes 列表必须保持顺序一致。
func TestHelpPermissionModes_CycleOrder(t *testing.T) {
	want := []string{"default", "acceptEdits", "plan", "bypass"}
	if len(helpPermissionModes) != len(want) {
		t.Fatalf("helpPermissionModes length mismatch: got %d, want %d", len(helpPermissionModes), len(want))
	}
	for i, w := range want {
		if helpPermissionModes[i].name != w {
			t.Errorf("helpPermissionModes[%d].name = %q, want %q", i, helpPermissionModes[i].name, w)
		}
	}
}

// /help 必须包含 Configuration sources 节，并列出三种配置入口。
// 这是用户痛点直接回答："GOCLAUDE_PERMISSION_MODE 能否配置文件设置" → 是的，看这里。
func TestRenderHelp_ConfigurationSourcesSection(t *testing.T) {
	text := helpRenderForTest(t, false)
	if !strings.Contains(text, "▌ Configuration sources") {
		t.Fatalf("/help must show Configuration sources section\n%s", text)
	}
	for _, want := range []string{
		"shell export",
		".env",
		"~/.claude/settings.json",
		".claude/settings.json",
		".claude/settings.local.json",
		"--env-file",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("/help configuration sources must contain %q\n%s", want, text)
		}
	}
}
