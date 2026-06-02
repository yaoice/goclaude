package shell

import (
	"strings"
	"testing"
)

func TestHighlightLine_Go(t *testing.T) {
	got := HighlightLine(`func main() { x := 1 // comment`, "go")
	if !strings.Contains(got, hlKeyword+"func"+hlReset) {
		t.Errorf("missing keyword color: %q", got)
	}
	if !strings.Contains(got, hlNumber+"1"+hlReset) {
		t.Errorf("missing number color: %q", got)
	}
	if !strings.Contains(got, hlComment+"// comment"+hlReset) {
		t.Errorf("missing comment color: %q", got)
	}
}

func TestHighlightLine_String(t *testing.T) {
	got := HighlightLine(`fmt.Println("hello, world")`, "go")
	if !strings.Contains(got, hlString+`"hello, world"`+hlReset) {
		t.Errorf("missing string color: %q", got)
	}
}

func TestHighlightLine_StringInsideHashIgnored(t *testing.T) {
	// Go 里 // 在 string 内不应被识别为注释
	got := HighlightLine(`s := "// not a comment"`, "go")
	if strings.Contains(got, hlComment) {
		t.Errorf("// inside string mistakenly treated as comment: %q", got)
	}
}

func TestHighlightLine_Python(t *testing.T) {
	got := HighlightLine(`def foo(x): return x + 1  # bar`, "python")
	if !strings.Contains(got, hlKeyword+"def"+hlReset) {
		t.Errorf("missing keyword: %q", got)
	}
	if !strings.Contains(got, hlComment+"# bar"+hlReset) {
		t.Errorf("missing python comment: %q", got)
	}
}

func TestHighlightJSON(t *testing.T) {
	got := highlightJSON(`{"name": "alice", "age": 30, "ok": true}`)
	if !strings.Contains(got, hlType+`"name"`+hlReset) {
		t.Errorf("missing key color for name: %q", got)
	}
	if !strings.Contains(got, hlString+`"alice"`+hlReset) {
		t.Errorf("missing value color for alice: %q", got)
	}
	if !strings.Contains(got, hlNumber+"30"+hlReset) {
		t.Errorf("missing number color: %q", got)
	}
	if !strings.Contains(got, hlKeyword+"true"+hlReset) {
		t.Errorf("missing bool keyword color: %q", got)
	}
}

func TestHighlightYAML(t *testing.T) {
	got := highlightYAML(`name: claude    # codename`)
	if !strings.Contains(got, hlType+"name"+hlReset) {
		t.Errorf("missing key color: %q", got)
	}
	if !strings.Contains(got, hlComment+"# codename"+hlReset) {
		t.Errorf("missing comment color: %q", got)
	}
}

func TestHighlightLine_UnknownLangPassThrough(t *testing.T) {
	in := "some weird code with no syntax we know"
	got := HighlightLine(in, "brainfuck")
	if got != in {
		t.Errorf("unknown lang should return input unchanged: %q -> %q", in, got)
	}
}

func TestFormatter_FenceWithLangApplies(t *testing.T) {
	src := "```go\nfunc x() {}\n```\n"
	out := formatAll(src)
	// 围栏内的 func 应该被染色
	if !strings.Contains(out, hlKeyword+"func"+hlReset) {
		t.Errorf("formatter did not invoke highlighter inside fence: %q", out)
	}
}

// ---- checkbox / table ----

func TestRenderCheckbox(t *testing.T) {
	got := renderInlineLine("- [x] done item")
	if !strings.Contains(got, "✓") {
		t.Errorf("checkmark missing: %q", got)
	}
	got = renderInlineLine("- [ ] todo item")
	if !strings.Contains(got, "[ ]") {
		t.Errorf("empty box missing: %q", got)
	}
}

func TestRenderTable(t *testing.T) {
	if !isTableRow("| a | b | c |") {
		t.Fatal("expected isTableRow=true")
	}
	if isTableRow("just `code | with` pipe in it") {
		t.Fatal("inline code with | should not be a table row")
	}
	got := renderInlineLine("| a | b | c |")
	if !strings.Contains(got, "│") {
		t.Errorf("table separator | should be replaced with │: %q", got)
	}
}

func TestRenderTableSep(t *testing.T) {
	got := renderInlineLine("| --- | --- |")
	if !strings.Contains(got, colorDim) {
		t.Errorf("table sep should be dim: %q", got)
	}
}
