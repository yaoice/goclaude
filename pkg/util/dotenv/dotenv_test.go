package dotenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_BasicKV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := `# 注释行
FOO=bar
BAZ="quoted value"
QUX='single quoted'
export EXPORTED=ok
EMPTY=
WITH_HASH=abc # 行尾注释
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// 清理可能的旧值
	for _, k := range []string{"FOO", "BAZ", "QUX", "EXPORTED", "EMPTY", "WITH_HASH"} {
		os.Unsetenv(k)
	}

	if err := Load(path); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	cases := map[string]string{
		"FOO":       "bar",
		"BAZ":       "quoted value",
		"QUX":       "single quoted",
		"EXPORTED":  "ok",
		"EMPTY":     "",
		"WITH_HASH": "abc",
	}
	for k, want := range cases {
		if got := os.Getenv(k); got != want {
			t.Errorf("env[%s]: got %q, want %q", k, got, want)
		}
	}
}

func TestLoad_DoesNotOverrideExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	os.WriteFile(path, []byte("MY_VAR=fromfile\n"), 0644)

	os.Setenv("MY_VAR", "fromenv")
	defer os.Unsetenv("MY_VAR")

	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("MY_VAR"); got != "fromenv" {
		t.Errorf("Load should NOT override existing env, got %q", got)
	}
}

func TestOverload_OverridesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	os.WriteFile(path, []byte("MY_VAR2=fromfile\n"), 0644)

	os.Setenv("MY_VAR2", "fromenv")
	defer os.Unsetenv("MY_VAR2")

	if err := Overload(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("MY_VAR2"); got != "fromfile" {
		t.Errorf("Overload should override existing env, got %q", got)
	}
}

func TestLoad_MissingFileIsOK(t *testing.T) {
	if err := Load("/nonexistent/path/.env"); err != nil {
		t.Errorf("missing file should not error, got %v", err)
	}
}

func TestLoad_InvalidLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	os.WriteFile(path, []byte("INVALID_LINE_NO_EQUAL\n"), 0644)

	if err := Load(path); err == nil {
		t.Error("expected error for invalid line")
	}
}

func TestLoadFromWorkdir(t *testing.T) {
	// 在临时目录建立 .env，子目录运行
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, ".env"), []byte("WORKDIR_TEST_KEY=hello\n"), 0644)

	sub := filepath.Join(root, "deep", "nested")
	os.MkdirAll(sub, 0755)

	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	os.Chdir(sub)
	os.Unsetenv("WORKDIR_TEST_KEY")

	if err := LoadFromWorkdir(); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("WORKDIR_TEST_KEY"); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
}
