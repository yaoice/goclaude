package dotenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_VariableInterpolation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := `BASE=https://api.example.com
API_URL=${BASE}/v1
ALT_URL=$BASE/v2
QUOTED="${BASE}/q"
LITERAL='${BASE}/literal'
ESCAPED="\$BASE plain"
MISSING=${NOT_SET}/x
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	for _, k := range []string{"BASE", "API_URL", "ALT_URL", "QUOTED", "LITERAL", "ESCAPED", "MISSING", "NOT_SET"} {
		os.Unsetenv(k)
	}

	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"BASE":    "https://api.example.com",
		"API_URL": "https://api.example.com/v1",
		"ALT_URL": "https://api.example.com/v2",
		"QUOTED":  "https://api.example.com/q",
		"LITERAL": "${BASE}/literal", // 单引号不展开
		"ESCAPED": "$BASE plain",     // \$ 转义
		"MISSING": "/x",              // 未定义变量替换为空
	}
	for k, want := range cases {
		if got := os.Getenv(k); got != want {
			t.Errorf("env[%s] = %q, want %q", k, got, want)
		}
	}
}

func TestLoad_EscapeSequences(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := `MULTILINE="line1\nline2\ttab"
QUOTE="say \"hello\""
SINGLE_NO_ESC='line1\nline2'
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"MULTILINE", "QUOTE", "SINGLE_NO_ESC"} {
		os.Unsetenv(k)
	}
	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("MULTILINE"); got != "line1\nline2\ttab" {
		t.Errorf("MULTILINE = %q", got)
	}
	if got := os.Getenv("QUOTE"); got != `say "hello"` {
		t.Errorf("QUOTE = %q", got)
	}
	if got := os.Getenv("SINGLE_NO_ESC"); got != `line1\nline2` {
		t.Errorf("SINGLE_NO_ESC = %q (single quotes should not expand escapes)", got)
	}
}

func TestLoaded_RecordsPathAndKeys(t *testing.T) {
	ResetLoadedForTest()

	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("LOADED_REC_A=1\nLOADED_REC_B=2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"LOADED_REC_A", "LOADED_REC_B"} {
		os.Unsetenv(k)
	}
	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	recs := Loaded()
	if len(recs) != 1 {
		t.Fatalf("expect 1 record, got %d", len(recs))
	}
	if recs[0].Path != path {
		t.Errorf("path = %q, want %q", recs[0].Path, path)
	}
	if len(recs[0].Keys) != 2 {
		t.Errorf("keys = %v", recs[0].Keys)
	}
	if !strings.Contains(strings.Join(recs[0].Keys, ","), "LOADED_REC_A") {
		t.Errorf("keys missing LOADED_REC_A: %v", recs[0].Keys)
	}
}

func TestLoad_InterpolationUsesEnvAlreadyLoaded(t *testing.T) {
	// 先加载 file1：注入 PRIMARY；再加载 file2：通过 ${PRIMARY} 引用
	dir := t.TempDir()
	f1 := filepath.Join(dir, "first.env")
	f2 := filepath.Join(dir, "second.env")
	os.WriteFile(f1, []byte("INTERP_PRIMARY=alpha\n"), 0644)
	os.WriteFile(f2, []byte("INTERP_DERIVED=${INTERP_PRIMARY}-beta\n"), 0644)

	for _, k := range []string{"INTERP_PRIMARY", "INTERP_DERIVED"} {
		os.Unsetenv(k)
	}
	if err := Load(f1, f2); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("INTERP_DERIVED"); got != "alpha-beta" {
		t.Errorf("INTERP_DERIVED = %q, want alpha-beta", got)
	}
}
