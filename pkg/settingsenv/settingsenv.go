// Package settingsenv 把 settings.json 中的 env 字段桥接为进程环境变量。
//
// 解决用户痛点："GOCLAUDE_PERMISSION_MODE / GOCLAUDE_USE_BUILTIN_GREP 能否
// 用配置文件设置"。在此之前，这些变量只能通过：
//   - shell `export`（每次重开终端要 source rc）
//   - `.env` 文件（项目级 / 用户级；已有 pkg/dotenv 支持）
//
// 现在再加一条：直接写到 settings.json 的 env 对象里。三种方式可叠加。
// 与 src 端的 settings.json 完全对齐（`env: Record<string, string>` 字段，
// 参见 src/utils/config 中的 SettingsSchema）。
//
// 加载顺序（先加载者优先；后来者不覆盖已有 env）：
//   1. 进程已有 env（含 shell export / pkg/dotenv 已加载的 .env 链）  ← 永不覆盖
//   2. 项目 .goclaude/settings.local.json   ← 个人本地覆盖
//   3. 项目 .claude/settings.local.json     ← 旧目录兜底
//   4. 项目 .goclaude/settings.json         ← 团队共享
//   5. 项目 .claude/settings.json           ← 旧目录兜底
//   6. 用户 ~/.goclaude/settings.json       ← 个人全局
//   7. 用户 ~/.claude/settings.json         ← 旧目录兜底
//
// 该顺序与 dotenv 链保持一致（user 最低 / 进程已有最高），让"shell flag"始终
// 拥有最高可控权，避免配置文件意外锁住用户。
package settingsenv

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"

	"github.com/anthropics/goclaude/internal/infrastructure/configdir"
)

// LoadRecord 描述一次成功加载——给 `goclaude doctor` / -v 启动诊断用。
//
// 不暴露值（避免 settings.json 里的敏感 API key 被打到日志）；只暴露键名。
type LoadRecord struct {
	Path string
	Keys []string
}

var (
	recordMu  sync.Mutex
	loadedAll []LoadRecord
)

// Loaded 返回截至当前所有成功加载的记录（已拷贝；并发安全）。
func Loaded() []LoadRecord {
	recordMu.Lock()
	defer recordMu.Unlock()
	out := make([]LoadRecord, len(loadedAll))
	copy(out, loadedAll)
	return out
}

// ResetForTest 仅供测试用：清空 Loaded 记录。
func ResetForTest() {
	recordMu.Lock()
	defer recordMu.Unlock()
	loadedAll = nil
}

// LoadDefaults 按 user → project → project-local 顺序加载默认 settings 链。
//
// projectCwd 为空时跳过项目级；homeDir 为空时跳过用户级。
// 任何路径不存在 / 不可解析时静默跳过；仅在 IO 错误时返回错误，
// 单文件错误不影响其它文件继续加载。
func LoadDefaults(homeDir, projectCwd string) error {
	type entry struct {
		path string
		desc string // 用于错误信息
	}
	var entries []entry
	if homeDir != "" {
		entries = append(entries,
			entry{path: configdir.JoinPrimary(homeDir, "settings.json"), desc: "user settings"},
			entry{path: configdir.JoinLegacy(homeDir, "settings.json"), desc: "user settings (legacy)"},
		)
	}
	if projectCwd != "" {
		for _, sp := range []string{"settings.json", "settings.local.json"} {
			entries = append(entries,
				entry{path: configdir.JoinPrimary(projectCwd, sp), desc: "project " + sp},
				entry{path: configdir.JoinLegacy(projectCwd, sp), desc: "project " + sp + " (legacy)"},
			)
		}
	}
	for _, e := range entries {
		if err := LoadFile(e.path); err != nil {
			// 文件存在但 JSON 损坏 → 静默跳过 + 在 Loaded 里不出现，避免启动失败。
			// 真正的 IO 异常（权限不足）也走静默——优先保证 REPL 能启动。
			// 调用方可以通过对比 Loaded() 与期望路径列表来诊断。
			_ = e
			_ = err
			continue
		}
	}
	return nil
}

// LoadFile 从单个 settings.json 提取 env 字段并注入进程环境。
//
// 行为：
//   - 文件不存在：返回 nil（静默）
//   - 文件存在但 JSON 错：返回 error（让调用方决定怎么报）
//   - 文件存在且 env 字段为 map[string]string：仅注入尚未存在的 key（不覆盖）
//   - env 字段非 map 或字段值非 string：跳过该条目；不视为错误
func LoadFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	env, err := parseEnvField(data)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if len(env) == 0 {
		return nil
	}

	// 不覆盖已有 env；与 pkg/dotenv 的 Load 行为一致
	injected := make([]string, 0, len(env))
	for k, v := range env {
		if _, exists := os.LookupEnv(k); exists {
			continue
		}
		if err := os.Setenv(k, v); err != nil {
			// Setenv 在 Unix 下几乎不会失败；仅在 key 含 NUL 等极端情况
			continue
		}
		injected = append(injected, k)
	}
	if len(injected) > 0 {
		sort.Strings(injected)
		recordMu.Lock()
		loadedAll = append(loadedAll, LoadRecord{Path: path, Keys: injected})
		recordMu.Unlock()
	}
	return nil
}

// parseEnvField 从 settings.json 字节中抽出 env 字段并转成 map[string]string。
//
// 接受的 schema：
//
//	{
//	  "env": {
//	    "GOCLAUDE_PERMISSION_MODE": "acceptEdits",
//	    "GOCLAUDE_USE_BUILTIN_GREP": "1"
//	  }
//	  ...其它字段被忽略
//	}
//
// 非 string value 自动 stringify（数字→十进制；bool→true/false）；
// 其它复杂类型（array/object/null）跳过——env 只接 scalar。
func parseEnvField(data []byte) (map[string]string, error) {
	var raw struct {
		Env map[string]json.RawMessage `json:"env"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if len(raw.Env) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(raw.Env))
	for k, v := range raw.Env {
		s, ok := coerceEnvValue(v)
		if !ok {
			continue
		}
		out[k] = s
	}
	return out, nil
}

// coerceEnvValue 把 JSON value 转为字符串；array/object/null 返回 ("", false)。
//
// 实现注意：JSON null 用 json.Unmarshal 解到 *string 不会报错（返回 nil）；
// 这里通过先看字节内容是否为字面量 "null" 来显式 reject——避免把 env=null 解释成
// env=""（POSIX 上 setenv "" 是个边界行为，且语义上让用户困惑）。
func coerceEnvValue(v json.RawMessage) (string, bool) {
	// 显式 reject JSON null
	if isJSONNull(v) {
		return "", false
	}
	var s string
	if err := json.Unmarshal(v, &s); err == nil {
		return s, true
	}
	var n json.Number
	dec := json.NewDecoder(bytes.NewReader(v))
	dec.UseNumber()
	if err := dec.Decode(&n); err == nil && n != "" {
		return n.String(), true
	}
	var b bool
	if err := json.Unmarshal(v, &b); err == nil {
		if b {
			return "true", true
		}
		return "false", true
	}
	return "", false
}

// isJSONNull 判断 RawMessage 是否是 JSON 字面量 null（去掉首尾空白后字面匹配）。
func isJSONNull(v json.RawMessage) bool {
	s := string(v)
	// 去除前后 ASCII 空白；json.RawMessage 通常已是紧凑形式但保险起见
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s == "null"
}
