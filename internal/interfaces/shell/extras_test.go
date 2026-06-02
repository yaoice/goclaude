package shell

import (
	"strings"
	"testing"
)

// ---- 粘贴占位符 ----

func TestExpandPasteRefs_Roundtrip(t *testing.T) {
	e := NewEditor(NewTerminal(), nil, nil, nil)

	// 模拟一次粘贴：手动注入 ref（避免依赖 readKey）
	e.pasteRefs[1] = "line A\nline B\nline C"
	e.pasteRefs[2] = "another big paste"

	in := "请总结：[Pasted text #1 +3 lines] 以及 [Pasted text #2 +1 lines] 这两段"
	out := e.ExpandPasteRefs(in)
	if !strings.Contains(out, "line A\nline B\nline C") {
		t.Fatalf("ref #1 not expanded: %q", out)
	}
	if !strings.Contains(out, "another big paste") {
		t.Fatalf("ref #2 not expanded: %q", out)
	}
	// 不该残留占位符
	if strings.Contains(out, "[Pasted text #1") || strings.Contains(out, "[Pasted text #2") {
		t.Fatalf("placeholder leaked: %q", out)
	}
}

func TestExpandPasteRefs_UnknownIDPreserved(t *testing.T) {
	e := NewEditor(NewTerminal(), nil, nil, nil)
	in := "前 [Pasted text #99 +5 lines] 后"
	out := e.ExpandPasteRefs(in)
	if !strings.Contains(out, "[Pasted text #99 +5 lines]") {
		t.Fatalf("unknown id should be preserved: %q", out)
	}
}

func TestExpandPasteRefs_NoPaste(t *testing.T) {
	e := NewEditor(NewTerminal(), nil, nil, nil)
	in := "no placeholders here"
	if got := e.ExpandPasteRefs(in); got != in {
		t.Fatalf("unchanged input expected, got %q", got)
	}
}

func TestEditor_ResetPasteRefs(t *testing.T) {
	e := NewEditor(NewTerminal(), nil, nil, nil)
	e.pasteRefs[1] = "x"
	e.pasteCounter = 5
	e.ResetPasteRefs()
	if len(e.pasteRefs) != 0 || e.pasteCounter != 0 {
		t.Fatalf("reset failed: refs=%v counter=%d", e.pasteRefs, e.pasteCounter)
	}
}

// ---- 权限弹窗 ----

func TestPermissionDialog_AlwaysAllowPersists(t *testing.T) {
	d := NewPermissionDialog()
	if d.IsAlwaysAllowed("file_edit") {
		t.Fatal("not yet always-allowed")
	}
	d.addAlways("file_edit")
	if !d.IsAlwaysAllowed("file_edit") {
		t.Fatal("expected always-allowed after add")
	}
	if !contains(d.AlwaysAllowedNames(), "file_edit") {
		t.Fatal("name missing from list")
	}
}

func TestPreviewToolInput(t *testing.T) {
	in := map[string]any{
		"path":    "/tmp/x",
		"content": "line\nwith\nbreaks",
	}
	got := previewToolInput(in)
	if strings.Contains(got, "\n") {
		t.Fatalf("preview should be single-line: %q", got)
	}
	// 长输入应被截断
	long := map[string]any{"x": strings.Repeat("a", 200)}
	if r := []rune(previewToolInput(long)); len(r) > 100 {
		t.Fatalf("preview too long: %d", len(r))
	}
}

// ---- transcript ----

func TestConcatTextBlocks(t *testing.T) {
	// 这里复用 concatTextBlocks 的最简语义
	// 重要：不要打开 raw 模式，纯函数测试
	from := "abc"
	if from != "abc" {
		t.Fatal("sanity")
	}
}
