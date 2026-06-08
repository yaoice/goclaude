package shell

import (
	"strings"
	"testing"
)

// ---- extractToolSummary ----

func TestExtractToolSummary_Bash(t *testing.T) {
	s := extractToolSummary("bash", `{"command":"ls -la src/"}`, 80)
	if s != "ls -la src/" {
		t.Fatalf("bash summary = %q, want %q", s, "ls -la src/")
	}
}

func TestExtractToolSummary_FileRead(t *testing.T) {
	s := extractToolSummary("read_file", `{"path":"src/main.go"}`, 80)
	if s != "src/main.go" {
		t.Fatalf("file_read summary = %q, want %q", s, "src/main.go")
	}
}

func TestExtractToolSummary_Grep(t *testing.T) {
	s := extractToolSummary("grep", `{"pattern":"func New","path":"src/"}`, 80)
	if !strings.Contains(s, "func New") {
		t.Fatalf("grep summary = %q, want to contain pattern", s)
	}
	if !strings.Contains(s, "src/") {
		t.Fatalf("grep summary = %q, want to contain path", s)
	}
}

func TestExtractToolSummary_GrepPatternOnly(t *testing.T) {
	s := extractToolSummary("grep", `{"pattern":"TODO"}`, 80)
	if !strings.Contains(s, "TODO") {
		t.Fatalf("grep pattern-only summary = %q", s)
	}
}

func TestExtractToolSummary_Agent(t *testing.T) {
	s := extractToolSummary("agent", `{"subagent_type":"coder","prompt":"implement login"}`, 80)
	if !strings.Contains(s, "coder") {
		t.Fatalf("agent summary = %q, want to contain type", s)
	}
	if !strings.Contains(s, "implement login") {
		t.Fatalf("agent summary = %q, want to contain prompt", s)
	}
}

func TestExtractToolSummary_SendMessage(t *testing.T) {
	s := extractToolSummary("send_message", `{"to":"worker","summary":"please review"}`, 80)
	if !strings.Contains(s, "worker") {
		t.Fatalf("send_message summary = %q, want recipient", s)
	}
}

func TestExtractToolSummary_UnknownToolFallback(t *testing.T) {
	// 未知工具取第一个非空字符串值
	s := extractToolSummary("unknown_tool", `{"widget":"hello world"}`, 80)
	if s != "hello world" {
		t.Fatalf("unknown tool fallback = %q, want %q", s, "hello world")
	}
}

func TestExtractToolSummary_EmptyJSON(t *testing.T) {
	s := extractToolSummary("bash", "", 80)
	if s != "" {
		t.Fatalf("empty JSON should return empty summary, got %q", s)
	}
}

func TestExtractToolSummary_PartialJSON(t *testing.T) {
	// partial JSON（未闭合）应能正常解析
	s := extractToolSummary("bash", `{"command":"grep -r 'TODO'`, 80)
	if s == "" {
		t.Fatalf("partial JSON should still yield summary, got empty")
	}
}

func TestExtractToolSummary_Truncation(t *testing.T) {
	long := strings.Repeat("a", 200)
	s := extractToolSummary("bash", `{"command":"`+long+`"}`, 20)
	rs := []rune(s)
	if len(rs) > 20 {
		t.Fatalf("summary len %d exceeds maxRunes=20", len(rs))
	}
	if !strings.HasSuffix(s, "…") {
		t.Fatalf("truncated summary should end with …, got %q", s)
	}
}

func TestExtractToolSummary_FileEdit(t *testing.T) {
	s := extractToolSummary("str_replace", `{"path":"main.go","old_str":"func foo() {"}`, 80)
	if !strings.Contains(s, "main.go") {
		t.Fatalf("edit summary = %q, want path", s)
	}
	if !strings.Contains(s, "func foo") {
		t.Fatalf("edit summary = %q, want old_str fragment", s)
	}
}

func TestExtractToolSummary_WebSearch(t *testing.T) {
	s := extractToolSummary("web_search", `{"query":"golang concurrency"}`, 80)
	if s != "golang concurrency" {
		t.Fatalf("web_search summary = %q", s)
	}
}

// ---- summarizeResult ----

func TestSummarizeResult_Empty(t *testing.T) {
	if s := summarizeResult("", 80); s != "" {
		t.Fatalf("empty text should return empty, got %q", s)
	}
}

func TestSummarizeResult_SingleLine(t *testing.T) {
	s := summarizeResult("hello world", 80)
	if s != "hello world" {
		t.Fatalf("single line = %q", s)
	}
}

func TestSummarizeResult_MultiLine(t *testing.T) {
	text := "line1\nline2\nline3"
	s := summarizeResult(text, 80)
	// 多行结果返回行数统计
	if s != "3 lines" {
		t.Fatalf("multi-line result should be '3 lines', got %q", s)
	}
}

func TestSummarizeResult_TwoLines(t *testing.T) {
	text := "first\nsecond"
	s := summarizeResult(text, 80)
	if s != "2 lines" {
		t.Fatalf("two-line result should be '2 lines', got %q", s)
	}
}

func TestSummarizeResult_JSONContent(t *testing.T) {
	// JSON 内容返回行数，不展示碎片
	text := "{\n  \"key\": \"value\"\n}"
	s := summarizeResult(text, 80)
	if strings.Contains(s, "{") || strings.Contains(s, "\"") {
		t.Fatalf("JSON content should not show fragments, got %q", s)
	}
	if s != "3 lines" {
		t.Fatalf("JSON multi-line should be '3 lines', got %q", s)
	}
}

func TestSummarizeResult_SingleLineJSON(t *testing.T) {
	// 单行 JSON 碎片
	s := summarizeResult(`{"key": "value"}`, 80)
	if s != "1 line" {
		t.Fatalf("single-line JSON should be '1 line', got %q", s)
	}
}

func TestSummarizeResult_NormalSingleLine(t *testing.T) {
	// 正常单行文本直接显示
	s := summarizeResult("File created successfully", 80)
	if s != "File created successfully" {
		t.Fatalf("normal single line should be shown directly, got %q", s)
	}
}

func TestSummarizeResult_Truncation(t *testing.T) {
	s := summarizeResult(strings.Repeat("x", 200), 20)
	rs := []rune(s)
	if len(rs) > 20 {
		t.Fatalf("result len %d exceeds maxRunes=20", len(rs))
	}
}

// ---- summarizeError ----

func TestSummarizeError_PrefersErrorLine(t *testing.T) {
	text := "output line\nerror: file not found\nmore output"
	s := summarizeError(text, 100)
	if s != "error: file not found" {
		t.Fatalf("should prefer error line, got %q", s)
	}
}

func TestSummarizeError_FallbackFirstLine(t *testing.T) {
	// 含 "failed" 关键字的行被优先选取
	text := "build failed\nsome detail"
	s := summarizeError(text, 100)
	if s != "build failed" {
		t.Fatalf("should find 'failed' keyword line, got %q", s)
	}
}

func TestSummarizeError_PanicLine(t *testing.T) {
	text := "goroutine 1 [running]:\npanic: runtime error: index out of range"
	s := summarizeError(text, 100)
	if s != "panic: runtime error: index out of range" {
		t.Fatalf("should find panic line exactly, got %q", s)
	}
}

func TestSummarizeError_NoSuffix(t *testing.T) {
	// 错误摘要不再附加 lines hidden 后缀
	text := "line1\nerror: something wrong\nline3\nline4\nline5"
	s := summarizeError(text, 100)
	if strings.Contains(s, "lines") || strings.Contains(s, "hidden") {
		t.Fatalf("error summary should not have suffix, got %q", s)
	}
}

// ---- itoa ----

func TestItoa(t *testing.T) {
	cases := map[int]string{0: "0", 1: "1", 42: "42", 999: "999", 1000: "1000"}
	for n, want := range cases {
		if got := itoa(n); got != want {
			t.Errorf("itoa(%d) = %q, want %q", n, got, want)
		}
	}
}

// ---- parsePartialJSON ----

func TestParsePartialJSON_Complete(t *testing.T) {
	m := parsePartialJSON(`{"key":"value"}`)
	if m["key"] != "value" {
		t.Fatalf("complete JSON parse failed: %v", m)
	}
}

func TestParsePartialJSON_MissingBrace(t *testing.T) {
	m := parsePartialJSON(`{"command":"ls -la"`)
	if m["command"] != "ls -la" {
		t.Fatalf("partial JSON (missing }) parse failed: %v", m)
	}
}

func TestParsePartialJSON_UnclosedString(t *testing.T) {
	// partial JSON 末尾字符串未闭合
	m := parsePartialJSON(`{"command":"ls`)
	if len(m) == 0 {
		// 接受解析失败（取决于补全策略），但不能 panic
		return
	}
	if cmd, ok := m["command"].(string); ok && !strings.HasPrefix(cmd, "ls") {
		t.Fatalf("partial string parse unexpected: %v", m)
	}
}

func TestParsePartialJSON_Empty(t *testing.T) {
	if m := parsePartialJSON(""); m != nil {
		t.Fatalf("empty should return nil, got %v", m)
	}
}
