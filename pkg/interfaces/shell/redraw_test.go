package shell

import (
	"bytes"
	"strings"
	"testing"
)

func TestPhysLayout_CursorColumn(t *testing.T) {
	e := NewEditor(NewTerminal(), nil, nil, nil)
	e.prompt = "❯ "
	e.contPrompt = "  "

	tests := []struct {
		name    string
		content string
	}{
		{"short", "做一个坦克大战游戏"},
		{"long", "使用subagent（独立子代理）方式实现一个经典的坦克大战游戏"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e.setBuf(tt.content)
			e.pos = len(e.buf)
			cursorRow, cursorCol, _, _ := e.physLayout(80)
			t.Logf("physLayout row=%d col=%d pos=%d", cursorRow, cursorCol, e.pos)
			if cursorCol <= 0 {
				t.Errorf("cursorCol=%d should be > 0", cursorCol)
			}
		})
	}
}

func TestClearRendered_MovesUpByCursorRow(t *testing.T) {
	e := NewEditor(NewTerminal(), nil, nil, nil)
	e.lastCursorRow = 3

	var out bytes.Buffer
	e.out = &out

	e.clearRendered()

	result := out.String()
	// 应上移 3 行并清屏到底
	if !strings.Contains(result, "\x1b[3A") {
		t.Errorf("clearRendered should move up 3 rows (\\x1b[3A), got: %q", result)
	}
	if !strings.Contains(result, "\x1b[0J") {
		t.Errorf("clearRendered should erase to end (\\x1b[0J), got: %q", result)
	}
	// 状态应被重置
	if e.lastCursorRow != 0 || e.lastLines != 0 {
		t.Errorf("clearRendered should reset state, got lastCursorRow=%d lastLines=%d", e.lastCursorRow, e.lastLines)
	}
}

// TestPhysLayout_Wrapping 验证长行折行的物理布局计算
func TestPhysLayout_Wrapping(t *testing.T) {
	e := NewEditor(NewTerminal(), nil, nil, nil)
	e.prompt = "❯ " // visibleWidth = 2

	tests := []struct {
		name        string
		content     string
		termW       int
		wantMinRows int
	}{
		{"短文本不折行", "abc", 80, 1},
		{"恰好填满一行", strings.Repeat("a", 78), 80, 1}, // 2(prompt)+78=80, 恰好
		{"超出一行", strings.Repeat("a", 79), 80, 2},     // 2+79=81 > 80 → 折行
		{"CJK长文本", strings.Repeat("中", 50), 80, 2},  // 2 + 50*2 = 102 > 80
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e.setBuf(tt.content)
			e.pos = len(e.buf)
			_, _, _, totalRows := e.physLayout(tt.termW)
			t.Logf("content=%d runes, termW=%d → totalRows=%d", len([]rune(tt.content)), tt.termW, totalRows)
			if totalRows < tt.wantMinRows {
				t.Errorf("totalRows=%d, want >= %d", totalRows, tt.wantMinRows)
			}
		})
	}
}

// TestPhysLayout_CursorAtEnd_Wrapped 验证折行时光标在末尾的物理位置
func TestPhysLayout_CursorAtEnd_Wrapped(t *testing.T) {
	e := NewEditor(NewTerminal(), nil, nil, nil)
	e.prompt = "❯ "
	termW := 80

	// 长文本会折行
	content := strings.Repeat("中", 50) // 100 cells + 2 prompt
	e.setBuf(content)
	e.pos = len(e.buf)

	cursorRow, cursorCol, endRow, totalRows := e.physLayout(termW)
	t.Logf("cursorRow=%d cursorCol=%d endRow=%d totalRows=%d", cursorRow, cursorCol, endRow, totalRows)

	// 光标在末尾，应等于 endRow
	if cursorRow != endRow {
		t.Errorf("cursor at end should be on endRow: cursorRow=%d endRow=%d", cursorRow, endRow)
	}
	if totalRows < 2 {
		t.Errorf("100-cell CJK text at width 80 should wrap to >= 2 rows, got %d", totalRows)
	}
}

// TestPhysLayout_VersionLine 验证版本行占 1 物理行
func TestPhysLayout_VersionLine(t *testing.T) {
	e := NewEditor(NewTerminal(), nil, nil, nil)
	e.prompt = "❯ "

	// 无版本
	e.setBuf("abc")
	e.pos = len(e.buf)
	_, _, _, rowsNoVer := e.physLayout(80)

	// 有版本
	e.SetVersions("abc", "abc def")
	e.pos = len(e.buf)
	cursorRow, _, _, rowsWithVer := e.physLayout(80)

	t.Logf("rowsNoVer=%d rowsWithVer=%d cursorRow=%d", rowsNoVer, rowsWithVer, cursorRow)
	if rowsWithVer != rowsNoVer+1 {
		t.Errorf("version line should add 1 row: %d vs %d", rowsWithVer, rowsNoVer)
	}
	// 光标应在版本行之下（row >= 1）
	if cursorRow < 1 {
		t.Errorf("cursor should be below version line (row >= 1), got %d", cursorRow)
	}
}

func TestPhysLayout_MultiLine(t *testing.T) {
	e := NewEditor(NewTerminal(), nil, nil, nil)
	e.prompt = "❯ "
	e.contPrompt = "  "

	content := "line1\nline2"
	e.setBuf(content)
	e.pos = len(e.buf)

	cursorRow, _, _, totalRows := e.physLayout(80)
	if cursorRow != 1 {
		t.Errorf("expected cursorRow 1 for second logical line, got %d", cursorRow)
	}
	if totalRows != 2 {
		t.Errorf("expected 2 total rows, got %d", totalRows)
	}
}
