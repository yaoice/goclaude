package shell

import (
	"strings"
	"testing"
)

// ---- 行内链接解析（applyInline 扩展） ----

func TestApplyInlineLink(t *testing.T) {
	got := applyInline("see [Example](https://example.com) here")
	// 链接文本应被下划线蓝着色
	if !strings.Contains(got, colorLink+"Example"+colorReset) {
		t.Fatalf("link text not styled: %q", got)
	}
	// url 应以 dim 灰显在括号中
	if !strings.Contains(got, "https://example.com") {
		t.Fatalf("link url missing: %q", got)
	}
	// 链接内的 url 不应再被裸 URL 规则二次着色为 \x1b[4;34mhttps（仅出现一次链接色）
	if strings.Count(got, colorLink) != 1 {
		t.Fatalf("unexpected duplicate link styling: %q", got)
	}
}

// ---- 块级解析 ----

func TestMarkdownHeadings(t *testing.T) {
	out := RenderMarkdown("# H1\n## H2\n### H3\n#### H4\n", 80)
	if !strings.Contains(out, "\x1b[1;36m# H1") {
		t.Fatalf("missing H1 color: %q", out)
	}
	if !strings.Contains(out, "\x1b[1;33m## H2") {
		t.Fatalf("missing H2 color: %q", out)
	}
	if !strings.Contains(out, "\x1b[1;32m### H3") {
		t.Fatalf("missing H3 color: %q", out)
	}
	// H4 无专色，仅粗体
	if !strings.Contains(out, "\x1b[1m#### H4") {
		t.Fatalf("missing H4 bold: %q", out)
	}
}

func TestMarkdownListBulletAndOrdered(t *testing.T) {
	out := RenderMarkdown("- alpha\n- beta\n1. one\n2. two\n", 80)
	if !strings.Contains(out, "•") {
		t.Fatalf("missing bullet: %q", out)
	}
	if !strings.Contains(out, colorAccent+"1."+colorReset) {
		t.Fatalf("missing ordered marker: %q", out)
	}
}

func TestMarkdownCheckbox(t *testing.T) {
	out := RenderMarkdown("- [ ] todo\n- [x] done\n", 80)
	if !strings.Contains(out, "✓") {
		t.Fatalf("missing checked mark: %q", out)
	}
	if !strings.Contains(out, "[ ]") {
		t.Fatalf("missing unchecked box: %q", out)
	}
}

func TestMarkdownQuote(t *testing.T) {
	out := RenderMarkdown("> quoted text\n", 80)
	if !strings.Contains(out, "▏") {
		t.Fatalf("missing quote marker: %q", out)
	}
	if !strings.Contains(out, "quoted text") {
		t.Fatalf("missing quote content: %q", out)
	}
}

func TestMarkdownInline(t *testing.T) {
	out := RenderMarkdown("a **bold** and `code` and *it* word\n", 80)
	if !strings.Contains(out, "\x1b[1mbold"+colorReset) {
		t.Fatalf("missing bold: %q", out)
	}
	if !strings.Contains(out, colorInlineCode+"code"+colorReset) {
		t.Fatalf("missing inline code: %q", out)
	}
	if !strings.Contains(out, "\x1b[3mit"+colorReset) {
		t.Fatalf("missing italic: %q", out)
	}
}

// ---- 代码块：原样保留 + 禁用行内解析 ----

func TestMarkdownCodeBlockPreservesRaw(t *testing.T) {
	src := "```\nthis **not bold** and `not code`\n   indented kept\n```\n"
	out := RenderMarkdown(src, 80)
	// 代码块内的 ** 与 ` 必须原样保留，不被解析
	if !strings.Contains(out, "**not bold**") {
		t.Fatalf("code block should keep raw **: %q", out)
	}
	if !strings.Contains(out, "`not code`") {
		t.Fatalf("code block should keep raw backticks: %q", out)
	}
	// 前导空白应保留
	if !strings.Contains(out, "   indented kept") {
		t.Fatalf("code block should keep leading spaces: %q", out)
	}
}

func TestMarkdownCodeBlockNoWrap(t *testing.T) {
	// 代码块内超长行不应被换行（保留原始格式）
	longLine := strings.Repeat("x", 200)
	src := "```\n" + longLine + "\n```\n"
	out := RenderMarkdown(src, 40)
	if !strings.Contains(out, longLine) {
		t.Fatalf("code block long line must not be wrapped: %q", out)
	}
}

// ---- 宽度自适应换行 ----

func TestWrapANSIBasicWidth(t *testing.T) {
	// 无样式纯文本，每行可见宽度不得超过 width
	s := "the quick brown fox jumps over the lazy dog"
	lines := wrapANSI(s, 10)
	if len(lines) < 2 {
		t.Fatalf("expected multiple wrapped lines, got %d: %v", len(lines), lines)
	}
	for _, ln := range lines {
		if w := visibleWidth(ln); w > 10 {
			t.Fatalf("line exceeds width: %q (w=%d)", ln, w)
		}
	}
}

func TestWrapANSIIgnoresEscapesForWidth(t *testing.T) {
	// 含 ANSI 转义的内容，宽度计算应只算可见字符
	styled := "\x1b[1mbold\x1b[0m \x1b[3mital\x1b[0m word more text here"
	lines := wrapANSI(styled, 12)
	for _, ln := range lines {
		if w := visibleWidth(ln); w > 12 {
			t.Fatalf("line exceeds visible width: %q (w=%d)", ln, w)
		}
	}
}

func TestWrapANSICJKWidth(t *testing.T) {
	// 中文每字宽 2：width=6 时每行最多 3 个汉字
	s := "你好世界测试换行能力"
	lines := wrapANSI(s, 6)
	for _, ln := range lines {
		if w := visibleWidth(ln); w > 6 {
			t.Fatalf("CJK line exceeds width: %q (w=%d)", ln, w)
		}
	}
	// 拼回去应等于原文（无空白丢失）
	joined := strings.Join(lines, "")
	if joined != s {
		t.Fatalf("CJK rejoin mismatch: %q != %q", joined, s)
	}
}

func TestWrapANSIStyleCarryAcrossLines(t *testing.T) {
	// 一个粗体跨多个词，换行后样式不应渗漏，且续行应重新打开样式
	styled := "\x1b[1mone two three four five six\x1b[0m"
	lines := wrapANSI(styled, 8)
	if len(lines) < 2 {
		t.Fatalf("expected wrap, got %v", lines)
	}
	// 每行都应以 reset 收尾（防止渗漏到换行/前缀）
	for i, ln := range lines {
		if !strings.HasSuffix(ln, colorReset) {
			t.Fatalf("line %d should end with reset: %q", i, ln)
		}
	}
	// 续行应重新注入粗体
	if !strings.HasPrefix(lines[1], "\x1b[1m") {
		t.Fatalf("continuation should reopen bold: %q", lines[1])
	}
}

func TestMarkdownParagraphWrapWithIndent(t *testing.T) {
	r := NewMarkdownRenderer(20, "  ", "│ ")
	out := r.Render("the quick brown fox jumps over the lazy dog\n")
	for _, ln := range strings.Split(out, "\n") {
		if ln == "" {
			continue
		}
		if w := visibleWidth(ln); w > 20 {
			t.Fatalf("paragraph line exceeds width: %q (w=%d)", ln, w)
		}
		// 每行都应带缩进与边条
		if !strings.HasPrefix(ln, "  ") {
			t.Fatalf("missing indent: %q", ln)
		}
		if !strings.Contains(ln, "│ ") {
			t.Fatalf("missing bar: %q", ln)
		}
	}
}

func TestMarkdownListHangingIndent(t *testing.T) {
	// 列表项换行后续行应有悬挂缩进（对齐到文本起点，而非 bullet）
	r := NewMarkdownRenderer(16, "", "")
	out := r.Render("- alpha beta gamma delta epsilon\n")
	lines := splitNonEmpty(out)
	if len(lines) < 2 {
		t.Fatalf("expected list item to wrap: %v", lines)
	}
	// 首行含 bullet
	if !strings.Contains(lines[0], "•") {
		t.Fatalf("first line missing bullet: %q", lines[0])
	}
	// 续行应以两个空格悬挂缩进开头，且不含 bullet
	if !strings.HasPrefix(lines[1], "  ") || strings.Contains(lines[1], "•") {
		t.Fatalf("continuation line should be hanging-indented without bullet: %q", lines[1])
	}
}

func TestMarkdownCRLFMode(t *testing.T) {
	r := NewMarkdownRenderer(80, "", "").SetCRLF(true)
	out := r.Render("hello\n")
	if !strings.Contains(out, "\r\n") {
		t.Fatalf("expected CRLF line endings: %q", out)
	}
}

func TestMarkdownNoWrapWhenWidthZero(t *testing.T) {
	long := "the quick brown fox jumps over the lazy dog again and again"
	out := RenderMarkdown(long+"\n", 0)
	// width<=0 不换行：正文应在同一行
	if strings.Count(strings.TrimRight(out, "\n"), "\n") != 0 {
		t.Fatalf("should not wrap when width<=0: %q", out)
	}
}

func splitNonEmpty(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if strings.TrimSpace(stripANSI(ln)) != "" {
			out = append(out, ln)
		}
	}
	return out
}
