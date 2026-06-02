package shell

import (
	"strings"
	"testing"
)

func TestDetectUseASCII_ExplicitSwitch(t *testing.T) {
	t.Setenv("GOCLAUDE_ASCII", "1")
	if !detectUseASCII() {
		t.Fatal("GOCLAUDE_ASCII=1 should force ASCII")
	}
	t.Setenv("GOCLAUDE_ASCII", "true")
	if !detectUseASCII() {
		t.Fatal("GOCLAUDE_ASCII=true should force ASCII")
	}
}

func TestDetectUseASCII_Locale(t *testing.T) {
	cases := []struct {
		lang string
		want bool // true = ASCII fallback
	}{
		{"en_US.UTF-8", false},
		{"zh_CN.utf8", false},
		{"C", true},
		{"POSIX", true},
		{"en_US.ISO-8859-1", true},
	}
	for _, c := range cases {
		t.Setenv("GOCLAUDE_ASCII", "")
		t.Setenv("LC_ALL", "")
		t.Setenv("LC_CTYPE", "")
		t.Setenv("LANG", c.lang)
		if got := detectUseASCII(); got != c.want {
			t.Errorf("LANG=%q: detectUseASCII()=%v, want %v", c.lang, got, c.want)
		}
	}
}

func TestREPL_GlyphSet_Switches(t *testing.T) {
	uni := (&REPL{useASCII: false}).gl()
	if uni.panelTop != "╭ " || uni.result != "⎿ " || uni.toolCall != "→ " {
		t.Fatalf("unicode glyphs wrong: %+v", uni)
	}
	asc := (&REPL{useASCII: true}).gl()
	if strings.ContainsAny(asc.panelTop+asc.panelRail+asc.panelBottom+asc.result+asc.toolCall+asc.minor, "╭│╰⎿→·") {
		t.Fatalf("ascii glyph set must not contain unicode box chars: %+v", asc)
	}
	if asc.ellipsis != "..." {
		t.Fatalf("ascii ellipsis should be ..., got %q", asc.ellipsis)
	}
}

func TestREPL_FitLine_ByDisplayWidth(t *testing.T) {
	r := &REPL{} // useASCII=false → ellipsis "…"
	// 短串不截断
	if got := r.fitLine("hello", 10); got != "hello" {
		t.Fatalf("short string should be untouched, got %q", got)
	}
	// 超长按宽度截断，末尾加 …
	got := r.fitLine("hello world", 5)
	if !strings.HasSuffix(got, "…") || visibleWidth(got) > 5 {
		t.Fatalf("fitLine truncation wrong: %q (width %d)", got, visibleWidth(got))
	}
	// CJK 宽字符按 2 列计
	cjk := r.fitLine("你好世界abc", 5)
	if visibleWidth(cjk) > 5 {
		t.Fatalf("CJK fit should respect cell width, got %q (width %d)", cjk, visibleWidth(cjk))
	}
}

func TestREPL_FitLine_ASCIIEllipsis(t *testing.T) {
	r := &REPL{useASCII: true}
	got := r.fitLine("hello world", 6)
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("ascii mode should truncate with '...', got %q", got)
	}
	if strings.Contains(got, "…") {
		t.Fatalf("ascii mode must not emit unicode ellipsis, got %q", got)
	}
}

// 无 Terminal 时 termWidth 回退 80；fitResult 在极窄预留下仍给最小可读宽度。
func TestREPL_FitResult_Fallbacks(t *testing.T) {
	r := &REPL{}
	if w := r.termWidth(); w != 80 {
		t.Fatalf("termWidth fallback should be 80, got %d", w)
	}
	// reserve 极大 → avail < 20 → 兜底 20 列
	long := strings.Repeat("x", 200)
	got := r.fitResult(long, 100, 1000)
	if visibleWidth(got) > 20 {
		t.Fatalf("fitResult floor width should be 20, got width %d", visibleWidth(got))
	}
}
