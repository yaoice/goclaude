package shell

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yaoice/goclaude/pkg/application"
	"github.com/yaoice/goclaude/pkg/domain/query"
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
		KeyCtrlC, KeyCtrlD, KeyCtrlR, KeyCtrlO, KeyCtrlG, KeyCtrlXE,
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

// TestEditor_SetVersions_Toggle_Clear 测试 Editor 的版本管理功能
func TestEditor_SetVersions_Toggle_Clear(t *testing.T) {
	e := NewEditor(NewTerminal(), nil, nil, nil)

	// 初始状态
	if e.HasVersions() {
		t.Error("expected no versions initially")
	}
	if e.GetCurrentVersionState() != "" {
		t.Errorf("expected empty state, got %q", e.GetCurrentVersionState())
	}

	// 设置版本
	e.SetVersions("原始提示词", "优化后的提示词")
	if !e.HasVersions() {
		t.Error("expected has versions after SetVersions")
	}
	if e.GetCurrentVersionState() != "enhanced" {
		t.Errorf("expected 'enhanced', got %q", e.GetCurrentVersionState())
	}
	if string(e.buf) != "优化后的提示词" {
		t.Errorf("expected enhanced text in buffer, got %q", string(e.buf))
	}
	if !e.preserveContent {
		t.Error("expected preserveContent to be true after SetVersions")
	}

	// 切换到原始版本
	e.ToggleVersion()
	if e.GetCurrentVersionState() != "original" {
		t.Errorf("expected 'original' after toggle, got %q", e.GetCurrentVersionState())
	}
	if string(e.buf) != "原始提示词" {
		t.Errorf("expected original text in buffer, got %q", string(e.buf))
	}

	// 再切回优化版本
	e.ToggleVersion()
	if e.GetCurrentVersionState() != "enhanced" {
		t.Errorf("expected 'enhanced' after second toggle, got %q", e.GetCurrentVersionState())
	}
	if string(e.buf) != "优化后的提示词" {
		t.Errorf("expected enhanced text in buffer, got %q", string(e.buf))
	}

	// 清除版本
	e.ClearVersions()
	if e.HasVersions() {
		t.Error("expected no versions after ClearVersions")
	}
	if e.GetCurrentVersionState() != "" {
		t.Errorf("expected empty state after clear, got %q", e.GetCurrentVersionState())
	}
}

// TestEditor_ToggleVersion_NoVersions 无版本时切换应 no-op
func TestEditor_ToggleVersion_NoVersions(t *testing.T) {
	e := NewEditor(NewTerminal(), nil, nil, nil)

	// 设置普通缓冲区内容
	e.setBuf("普通文本")
	e.ToggleVersion() // 应 no-op
	if string(e.buf) != "普通文本" {
		t.Error("expected buffer unchanged when no versions")
	}
}

// TestREPL_HandleEnhancePromptCmd_NoEnhancer 测试未注入 PromptEnhancer 时的行为
func TestREPL_HandleEnhancePromptCmd_NoEnhancer(t *testing.T) {
	r := &REPL{useColor: false, Editor: NewEditor(NewTerminal(), nil, nil, nil)}
	r.handleEnhancePromptCmd("")
	// 不应 panic，不应修改编辑器
	if r.Editor.HasVersions() {
		t.Error("expected no versions when enhancer not available")
	}
}

// TestREPL_HandleEnhancePromptCmd_EmptyArgs 测试空参数时的帮助信息
func TestREPL_HandleEnhancePromptCmd_EmptyArgs(t *testing.T) {
	prov := &stubProviderForTest{}
	enhancer := application.NewPromptEnhancer(prov, "test-model")
	r := &REPL{useColor: false, PromptEnhancer: enhancer, Editor: NewEditor(NewTerminal(), nil, nil, nil)}
	r.handleEnhancePromptCmd("")
	// 不应 panic
}

// stubProviderForTest 用于 shell 测试的简单 provider
type stubProviderForTest struct{}

func (s *stubProviderForTest) Stream(ctx context.Context, params *query.StreamParams) (<-chan query.StreamEvent, error) {
	return nil, errors.New("not implemented")
}

func (s *stubProviderForTest) Send(ctx context.Context, params *query.SendParams) (*query.Message, *query.Usage, error) {
	return nil, nil, errors.New("not implemented")
}

func TestREPL_HandleEnhancePromptCmd_Success(t *testing.T) {
	original := "帮我写个排序"
	enhanced := "请帮我实现一个冒泡排序函数，使用 Go 语言"

	prov := &stubEnhanceProvider{
		enhanced: enhanced,
	}
	enhancer := application.NewPromptEnhancer(prov, "test-model")

	r := &REPL{
		useColor:       false,
		PromptEnhancer: enhancer,
		Editor:         NewEditor(NewTerminal(), nil, nil, nil),
	}

	r.handleEnhancePromptCmd(original)

	// 验证编辑器版本已设置
	if !r.Editor.HasVersions() {
		t.Fatal("expected editor has versions after enhance")
	}
	if r.Editor.GetCurrentVersionState() != "enhanced" {
		t.Errorf("expected showing enhanced, got %q", r.Editor.GetCurrentVersionState())
	}

	// 切换回原始版本
	r.Editor.ToggleVersion()
	if r.Editor.GetCurrentVersionState() != "original" {
		t.Errorf("expected showing original after toggle, got %q", r.Editor.GetCurrentVersionState())
	}

	// 再切回优化版本
	r.Editor.ToggleVersion()
	if r.Editor.GetCurrentVersionState() != "enhanced" {
		t.Errorf("expected showing enhanced after second toggle, got %q", r.Editor.GetCurrentVersionState())
	}
}

// stubEnhanceProvider 模拟成功优化的 provider
type stubEnhanceProvider struct {
	enhanced string
}

func (s *stubEnhanceProvider) Stream(ctx context.Context, params *query.StreamParams) (<-chan query.StreamEvent, error) {
	return nil, errors.New("not implemented")
}

func (s *stubEnhanceProvider) Send(ctx context.Context, params *query.SendParams) (*query.Message, *query.Usage, error) {
	return &query.Message{
		Role: query.RoleAssistant,
		Content: []query.ContentBlock{
			{Type: query.ContentTypeText, Text: s.enhanced},
		},
	}, &query.Usage{}, nil
}

func TestREPL_HandleEnhancePromptCmd_APIError(t *testing.T) {
	prov := &stubProviderForTest{} // Send returns error
	enhancer := application.NewPromptEnhancer(prov, "test-model")

	r := &REPL{
		useColor:       false,
		PromptEnhancer: enhancer,
		Editor:         NewEditor(NewTerminal(), nil, nil, nil),
	}

	r.handleEnhancePromptCmd("test prompt")

	// 验证编辑器没有版本（失败时不应修改编辑器）
	if r.Editor.HasVersions() {
		t.Error("expected no versions after failed enhance")
	}
}

func TestREPL_HandleEnhancePromptCmd_WhitespaceOnly(t *testing.T) {
	prov := &stubEnhanceProvider{enhanced: "should not be used"}
	enhancer := application.NewPromptEnhancer(prov, "test-model")

	r := &REPL{
		useColor:       false,
		PromptEnhancer: enhancer,
		Editor:         NewEditor(NewTerminal(), nil, nil, nil),
	}

	// 空白内容不应触发 API 调用
	r.handleEnhancePromptCmd("   ")
	if r.Editor.HasVersions() {
		t.Error("expected no versions for whitespace-only input")
	}
}
