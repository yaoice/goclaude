package filesystem

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// makeTree 在 tmpDir 下按 spec 写文件；spec 是 path→content 映射。
// path 用 / 作分隔符，自动转成 OS 分隔符。
func makeTree(t *testing.T, dir string, spec map[string]string) {
	t.Helper()
	for rel, content := range spec {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
}

// 强制走 builtin（确保 CI/本机有无 rg 都跑同一份 fallback 逻辑）。
func forceBuiltin(t *testing.T) {
	t.Helper()
	t.Setenv("GOCLAUDE_USE_BUILTIN_GREP", "1")
}

// ---- Grep builtin：基础匹配 ----------------------------------------------------------

func TestGrep_Builtin_BasicMatch(t *testing.T) {
	forceBuiltin(t)
	dir := t.TempDir()
	makeTree(t, dir, map[string]string{
		"a.go":           "package a\nfunc Hello() {}\n",
		"sub/b.go":       "package b\nfunc Hello() {}\n",
		"sub/skip.txt":   "no match here\n",
		"node_modules/c": "package c\nfunc Hello() {}\n", // 应被噪音目录跳过
	})

	g := NewGrep(dir)
	results, err := g.Search(GrepOptions{Pattern: "Hello"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 hits (excluding node_modules), got %d: %+v", len(results), results)
	}
	for _, r := range results {
		if r.Line != 2 {
			t.Errorf("expected line=2 for Hello(), got %d in %s", r.Line, r.File)
		}
		if !strings.Contains(r.File, ".go") {
			t.Errorf("matched non-go file: %s", r.File)
		}
		if strings.Contains(r.File, "node_modules") {
			t.Errorf("noise dir leaked into results: %s", r.File)
		}
	}
}

// MaxResults 早停：扫到限额立即返回，不浪费 IO。
func TestGrep_Builtin_MaxResultsStopsEarly(t *testing.T) {
	forceBuiltin(t)
	dir := t.TempDir()
	makeTree(t, dir, map[string]string{
		"a.txt": "x\nx\nx\nx\nx\n",
		"b.txt": "x\nx\nx\n",
	})
	results, err := NewGrep(dir).Search(GrepOptions{Pattern: "x", MaxResults: 3})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("MaxResults=3 should cap; got %d", len(results))
	}
}

// 大小写不敏感是默认；显式 CaseSensitive=true 应区分。
func TestGrep_Builtin_CaseSensitivity(t *testing.T) {
	forceBuiltin(t)
	dir := t.TempDir()
	makeTree(t, dir, map[string]string{"a.txt": "Hello\nhello\n"})
	g := NewGrep(dir)

	all, _ := g.Search(GrepOptions{Pattern: "hello"})
	if len(all) != 2 {
		t.Errorf("default insensitive should match 2; got %d", len(all))
	}

	strict, _ := g.Search(GrepOptions{Pattern: "hello", CaseSensitive: true})
	if len(strict) != 1 {
		t.Errorf("CaseSensitive=true should match only lowercase; got %d", len(strict))
	}
}

// FilePattern：仅在 *.go 中找。
func TestGrep_Builtin_FilePatternFiltersByExtension(t *testing.T) {
	forceBuiltin(t)
	dir := t.TempDir()
	makeTree(t, dir, map[string]string{
		"a.go":  "needle\n",
		"a.txt": "needle\n",
	})
	results, err := NewGrep(dir).Search(GrepOptions{Pattern: "needle", FilePattern: "*.go"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("file pattern should keep 1 result, got %d", len(results))
	}
	if !strings.HasSuffix(results[0].File, ".go") {
		t.Errorf("matched %q, want *.go", results[0].File)
	}
}

// 无匹配应返回 (nil, nil)，不要返回 error，与 rg exit-code-1 语义对齐。
func TestGrep_Builtin_NoMatchReturnsNilNil(t *testing.T) {
	forceBuiltin(t)
	dir := t.TempDir()
	makeTree(t, dir, map[string]string{"a.txt": "hello\n"})
	results, err := NewGrep(dir).Search(GrepOptions{Pattern: "nonexistent"})
	if err != nil {
		t.Fatalf("no-match must not error; got %v", err)
	}
	if results != nil {
		t.Fatalf("no-match must return nil; got %+v", results)
	}
}

// 二进制文件应被跳过（含 NUL 字节嗅探）。
func TestGrep_Builtin_SkipsBinaryFiles(t *testing.T) {
	forceBuiltin(t)
	dir := t.TempDir()
	makeTree(t, dir, map[string]string{
		"text.txt": "needle is here\n",
	})
	if err := os.WriteFile(filepath.Join(dir, "binary.bin"), []byte("needle\x00\x00binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	results, _ := NewGrep(dir).Search(GrepOptions{Pattern: "needle"})
	if len(results) != 1 {
		t.Fatalf("binary file should be skipped; got %d results: %+v", len(results), results)
	}
	if !strings.HasSuffix(results[0].File, "text.txt") {
		t.Errorf("matched binary file: %s", results[0].File)
	}
}

// 错误的 pattern 必须以 error 返回，而不是 panic。
func TestGrep_Builtin_InvalidRegexpReturnsError(t *testing.T) {
	forceBuiltin(t)
	dir := t.TempDir()
	makeTree(t, dir, map[string]string{"a.txt": "hi\n"})
	_, err := NewGrep(dir).Search(GrepOptions{Pattern: "(unclosed"})
	if err == nil {
		t.Fatal("invalid regexp must return error")
	}
}

// 空 pattern 必须立刻失败（与旧实现一致）。
func TestGrep_Search_EmptyPatternRejected(t *testing.T) {
	_, err := NewGrep(".").Search(GrepOptions{Pattern: ""})
	if err == nil {
		t.Fatal("empty pattern must error")
	}
}

// ---- ripgrep JSON 解析：旧版 TODO 的回归测试 ------------------------------------------

func TestParseRipgrepJSON_FillsAllFields(t *testing.T) {
	// 模拟真实 rg --json 输出（type=match）；夹杂 begin/end/summary 应被忽略
	in := strings.Join([]string{
		`{"type":"begin","data":{"path":{"text":"src/a.go"}}}`,
		`{"type":"match","data":{"path":{"text":"src/a.go"},"lines":{"text":"func Hello() {}\n"},"line_number":42,"absolute_offset":100,"submatches":[]}}`,
		`{"type":"match","data":{"path":{"text":"src/b.go"},"lines":{"text":"hello again\n"},"line_number":7}}`,
		`{"type":"end","data":{"path":{"text":"src/a.go"}}}`,
		`{"type":"summary","data":{"elapsed_total":{"human":"0.001s"}}}`,
	}, "\n")
	results := parseRipgrepJSON([]byte(in), 0)
	if len(results) != 2 {
		t.Fatalf("want 2 results (only type=match), got %d: %+v", len(results), results)
	}
	if results[0].File != "src/a.go" || results[0].Line != 42 || results[0].Content != "func Hello() {}" {
		t.Errorf("result[0] = %+v", results[0])
	}
	if results[1].File != "src/b.go" || results[1].Line != 7 {
		t.Errorf("result[1] = %+v", results[1])
	}
}

func TestParseRipgrepJSON_RespectsMaxResults(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"match","data":{"path":{"text":"a"},"lines":{"text":"x\n"},"line_number":1}}`,
		`{"type":"match","data":{"path":{"text":"a"},"lines":{"text":"x\n"},"line_number":2}}`,
		`{"type":"match","data":{"path":{"text":"a"},"lines":{"text":"x\n"},"line_number":3}}`,
	}, "\n")
	if got := parseRipgrepJSON([]byte(in), 2); len(got) != 2 {
		t.Fatalf("MaxResults=2 should cap; got %d", len(got))
	}
}

// 错误格式行不应导致 panic / 整体失败；应 silently 跳过。
func TestParseRipgrepJSON_TolerantOfMalformedLines(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"match","data":{"path":{"text":"a"},"lines":{"text":"hit\n"},"line_number":1}}`,
		`not valid json`,
		``,
		`{"type":"begin"}`,
	}, "\n")
	got := parseRipgrepJSON([]byte(in), 0)
	if len(got) != 1 || got[0].File != "a" {
		t.Fatalf("malformed lines should be ignored; got %+v", got)
	}
}

// ---- Glob：去 find 依赖 ---------------------------------------------------------------

func TestGlob_Match_BaseNamePattern(t *testing.T) {
	dir := t.TempDir()
	makeTree(t, dir, map[string]string{
		"a.go":                 "",
		"sub/b.go":             "",
		"sub/c.txt":            "",
		"node_modules/skip.go": "",
	})
	results, err := NewGlob(dir).Match(GlobOptions{Pattern: "*.go"})
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	got := normalizeRelTo(dir, results)
	sort.Strings(got)
	want := []string{"a.go", "sub/b.go"}
	if !equalStrings(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// ** 递归匹配：模式带路径分隔符时，按完整相对路径匹配。
func TestGlob_Match_DoubleStarRecursive(t *testing.T) {
	dir := t.TempDir()
	makeTree(t, dir, map[string]string{
		"a.go":            "",
		"sub/b.go":        "",
		"sub/inner/c.go":  "",
		"sub/inner/d.txt": "",
	})
	results, err := NewGlob(dir).Match(GlobOptions{Pattern: "**/*.go"})
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	got := normalizeRelTo(dir, results)
	sort.Strings(got)
	// **/*.go 匹配深层 go；根目录 a.go 因没有路径分隔无法 prefix-match → 不在内
	for _, w := range []string{"sub/b.go", "sub/inner/c.go"} {
		if !contains(got, w) {
			t.Errorf("missing %q in %v", w, got)
		}
	}
	if contains(got, "sub/inner/d.txt") {
		t.Errorf("non-go file leaked: %v", got)
	}
}

// noise 目录在 Glob 里也要跳过，与 Grep 一致。
func TestGlob_Match_SkipsNoiseDirs(t *testing.T) {
	dir := t.TempDir()
	makeTree(t, dir, map[string]string{
		"src/a.go":              "",
		"node_modules/x/y.go":   "",
		"vendor/lib.go":         "",
		".git/hooks/pre-commit": "",
	})
	results, _ := NewGlob(dir).Match(GlobOptions{Pattern: "*.go"})
	for _, p := range results {
		for _, banned := range []string{"node_modules", "vendor", ".git"} {
			if strings.Contains(p, banned) {
				t.Errorf("noise dir %q leaked: %s", banned, p)
			}
		}
	}
}

// 空 pattern 必须报错。
func TestGlob_Match_EmptyPatternRejected(t *testing.T) {
	_, err := NewGlob(".").Match(GlobOptions{Pattern: ""})
	if err == nil {
		t.Fatal("empty pattern must error")
	}
}

// ---- globToRegexp：边界 ---------------------------------------------------------------

func TestGlobToRegexp(t *testing.T) {
	cases := []struct {
		pattern string
		input   string
		want    bool
	}{
		{"*.go", "main.go", true},
		{"*.go", "main.go.bak", false},
		{"foo?bar", "fooXbar", true},
		{"foo?bar", "foobar", false},
		{"src/**/*.go", "src/a/b/c.go", true},
		{"src/**/*.go", "src/c.go", false}, // ** 至少匹配一段（依靠中间 /）
		{"a.b+c", "a.b+c", true},           // . + 应被转义
		{"a.b+c", "axbxc", false},
	}
	for _, c := range cases {
		re, err := globToRegexp(c.pattern)
		if err != nil {
			t.Fatalf("compile %q: %v", c.pattern, err)
		}
		got := re.MatchString(c.input)
		if got != c.want {
			t.Errorf("globToRegexp(%q).Match(%q) = %v, want %v", c.pattern, c.input, got, c.want)
		}
	}
}

// ---- helpers --------------------------------------------------------------------------

func normalizeRelTo(root string, paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			out = append(out, p)
			continue
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(xs []string, target string) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}
