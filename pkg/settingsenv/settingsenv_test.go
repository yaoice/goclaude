package settingsenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeJSON 写入 path（自动建目录）。
func writeJSON(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// 最小用例：单文件 + 单个 env 键被注入。
func TestLoadFile_InjectsStringEnv(t *testing.T) {
	ResetForTest()
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeJSON(t, path, `{"env": {"GOCLAUDE_PERMISSION_MODE": "acceptEdits"}}`)

	// 确保起始没有这个变量
	t.Setenv("GOCLAUDE_PERMISSION_MODE", "")
	_ = os.Unsetenv("GOCLAUDE_PERMISSION_MODE")

	if err := LoadFile(path); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if v := os.Getenv("GOCLAUDE_PERMISSION_MODE"); v != "acceptEdits" {
		t.Fatalf("env not injected; got %q", v)
	}

	// Loaded 应留下记录，但只含 key 不含 value
	recs := Loaded()
	if len(recs) != 1 || recs[0].Path != path {
		t.Fatalf("unexpected Loaded(): %+v", recs)
	}
	found := false
	for _, k := range recs[0].Keys {
		if k == "GOCLAUDE_PERMISSION_MODE" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Loaded[0].Keys missing GOCLAUDE_PERMISSION_MODE: %v", recs[0].Keys)
	}
}

// 已存在 env 不被覆盖（这是与 .env 链一致的核心安全契约）。
func TestLoadFile_DoesNotOverrideExistingEnv(t *testing.T) {
	ResetForTest()
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeJSON(t, path, `{"env": {"GOCLAUDE_PERMISSION_MODE": "bypass"}}`)

	t.Setenv("GOCLAUDE_PERMISSION_MODE", "default") // 进程已有

	if err := LoadFile(path); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if got := os.Getenv("GOCLAUDE_PERMISSION_MODE"); got != "default" {
		t.Fatalf("env should NOT be overridden; got %q want \"default\"", got)
	}
	// 没有注入新键 → Loaded 不该记录该文件
	for _, r := range Loaded() {
		if r.Path == path {
			t.Fatalf("Loaded should be empty when nothing new injected, got %+v", r)
		}
	}
}

// 文件不存在：返回 nil，不当错误。
func TestLoadFile_NonExistentSilent(t *testing.T) {
	ResetForTest()
	if err := LoadFile(filepath.Join(t.TempDir(), "no-such-file.json")); err != nil {
		t.Fatalf("nonexistent should be silent; got %v", err)
	}
}

// JSON 损坏：返回 error 但不 panic。
func TestLoadFile_InvalidJSONErrors(t *testing.T) {
	ResetForTest()
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeJSON(t, path, `{not json`)
	if err := LoadFile(path); err == nil {
		t.Fatal("expected error on malformed json")
	}
}

// env 字段为空 / 非对象时不报错。
func TestLoadFile_EmptyOrMissingEnvField(t *testing.T) {
	ResetForTest()
	cases := []string{
		`{}`,
		`{"env": {}}`,
		`{"other": 1}`,
	}
	for _, c := range cases {
		dir := t.TempDir()
		path := filepath.Join(dir, "settings.json")
		writeJSON(t, path, c)
		if err := LoadFile(path); err != nil {
			t.Errorf("%q should not error: %v", c, err)
		}
	}
}

// 非 string value 被合理 stringify。
func TestParseEnvField_CoerceNonStringValues(t *testing.T) {
	in := `{"env": {
		"S": "hello",
		"N": 42,
		"F": 3.14,
		"B": true,
		"FALSE": false,
		"ARR": [1,2],
		"OBJ": {"x":1},
		"NULL": null
	}}`
	got, err := parseEnvField([]byte(in))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := map[string]string{
		"S":     "hello",
		"N":     "42",
		"F":     "3.14",
		"B":     "true",
		"FALSE": "false",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("env[%q] = %q, want %q", k, got[k], v)
		}
	}
	// 复杂类型与 null 应被丢弃
	for _, k := range []string{"ARR", "OBJ", "NULL"} {
		if _, ok := got[k]; ok {
			t.Errorf("env[%q] should be dropped (got %q)", k, got[k])
		}
	}
}

// LoadDefaults 优先级：user → project → local（先加载者优先），
// 即 user 的 GOCLAUDE_PERMISSION_MODE=user 不被后续 project / local 覆盖。
func TestLoadDefaults_LoadOrderUserFirst(t *testing.T) {
	ResetForTest()

	home := t.TempDir()
	proj := t.TempDir()

	writeJSON(t,
		filepath.Join(home, ".goclaude", "settings.json"),
		`{"env": {"GOCLAUDE_PERMISSION_MODE": "from-user"}}`,
	)
	writeJSON(t,
		filepath.Join(proj, ".goclaude", "settings.json"),
		`{"env": {"GOCLAUDE_PERMISSION_MODE": "from-project"}}`,
	)
	writeJSON(t,
		filepath.Join(proj, ".goclaude", "settings.local.json"),
		`{"env": {"GOCLAUDE_PERMISSION_MODE": "from-local"}}`,
	)

	_ = os.Unsetenv("GOCLAUDE_PERMISSION_MODE")
	if err := LoadDefaults(home, proj); err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}

	// user 先加载且未被覆盖 → 最终是 from-user
	if got := os.Getenv("GOCLAUDE_PERMISSION_MODE"); got != "from-user" {
		t.Fatalf("user settings should win (first-loaded); got %q", got)
	}
}

// LoadDefaults 优先级：项目独有的 key 仍能被加载，不被 user 屏蔽。
func TestLoadDefaults_NonOverlappingKeysCoexist(t *testing.T) {
	ResetForTest()
	home := t.TempDir()
	proj := t.TempDir()

	writeJSON(t,
		filepath.Join(home, ".goclaude", "settings.json"),
		`{"env": {"A": "user-a"}}`,
	)
	writeJSON(t,
		filepath.Join(proj, ".goclaude", "settings.json"),
		`{"env": {"B": "project-b"}}`,
	)

	_ = os.Unsetenv("A")
	_ = os.Unsetenv("B")
	if err := LoadDefaults(home, proj); err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	if os.Getenv("A") != "user-a" || os.Getenv("B") != "project-b" {
		t.Fatalf("non-overlapping keys should both be set; A=%q B=%q",
			os.Getenv("A"), os.Getenv("B"))
	}
}

// 空 homeDir / projectCwd 应被静默跳过，不 panic。
func TestLoadDefaults_EmptyPathsAreSafe(t *testing.T) {
	ResetForTest()
	if err := LoadDefaults("", ""); err != nil {
		t.Fatalf("empty paths should be silent: %v", err)
	}
}

// Loaded 不暴露值——这是审计契约（settings.json 可能含敏感 token）。
func TestLoaded_OnlyRecordsKeys(t *testing.T) {
	ResetForTest()
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeJSON(t, path, `{"env": {"GOCLAUDE_FOO": "super-secret-token-do-not-log"}}`)

	_ = os.Unsetenv("GOCLAUDE_FOO")
	_ = LoadFile(path)

	recs := Loaded()
	for _, r := range recs {
		for _, k := range r.Keys {
			if strings.Contains(k, "secret") {
				t.Fatalf("Loaded() leaked value into Keys: %v", r.Keys)
			}
		}
	}
}
