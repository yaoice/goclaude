// Package dotenv 提供轻量级的 .env 文件加载能力（零外部依赖）
//
// 支持的语法：
//
//	KEY=value
//	KEY="value with spaces"
//	KEY='single quoted'         # 单引号内不做变量插值
//	KEY="hello ${USER}"          # 双引号支持 ${VAR} / $VAR 变量插值
//	KEY=plain ${VAR} mixed       # 无引号同样支持插值
//	# 注释行
//	export KEY=value             # 兼容 shell export 前缀
//	KEY=value # 行尾注释（仅 unquoted 时识别）
//
// 加载策略：仅当环境变量"未设置"时才注入，已有变量优先（与 shell 行为一致）。
// 变量插值时优先使用进程当前环境（含先前已加载的 .env 注入值）。
package dotenv

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// loadRecord 记录一次成功加载的文件信息（供诊断）
type loadRecord struct {
	Path string
	Keys []string // 实际写入（或被 skip）的 key 列表
}

var (
	recMu     sync.Mutex
	loadedAll []loadRecord
)

// Loaded 返回所有曾经被 Load/Overload 成功打开过的 .env 文件记录
//
// 用于 `claude doctor` 等诊断场景。仅返回路径与变量名，不暴露值。
func Loaded() []loadRecord {
	recMu.Lock()
	defer recMu.Unlock()
	out := make([]loadRecord, len(loadedAll))
	copy(out, loadedAll)
	return out
}

// LoadedRecord 是 Loaded() 单条记录的导出别名
type LoadedRecord = loadRecord

// ResetLoadedForTest 仅供测试使用：清空加载记录
func ResetLoadedForTest() {
	recMu.Lock()
	loadedAll = nil
	recMu.Unlock()
}

// Load 加载一个或多个 .env 文件
//
// 文件不存在时静默跳过；解析失败时返回首个错误。
// 已存在的环境变量不会被覆盖。
func Load(paths ...string) error {
	return loadFiles(false, paths...)
}

// Overload 与 Load 类似，但**覆盖**已有环境变量
func Overload(paths ...string) error {
	return loadFiles(true, paths...)
}

// loadFiles 共享 Load/Overload 的"参数兜底 + 顺序处理"骨架
func loadFiles(overload bool, paths ...string) error {
	if len(paths) == 0 {
		paths = []string{".env"}
	}
	for _, p := range paths {
		if err := loadFile(p, overload); err != nil {
			return err
		}
	}
	return nil
}

// LoadFromWorkdir 从工作目录的**父级**开始向上查找 .env 文件并加载
//
// 适用于在子目录运行命令时仍能找到项目根目录的 .env。
// 不重新加载 cwd/.env：调用方应先用 Load(".env") 处理当前目录，避免重复 IO。
func LoadFromWorkdir() error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	dir := filepath.Dir(wd)
	if dir == wd {
		return nil
	}
	for {
		candidate := filepath.Join(dir, ".env")
		if _, err := os.Stat(candidate); err == nil {
			return loadFile(candidate, false)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

// loadFile 解析单个 .env 文件并注入到环境
func loadFile(path string, overload bool) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 文件不存在视为可选
		}
		return fmt.Errorf("dotenv: open %s: %w", path, err)
	}
	defer f.Close()

	rec := loadRecord{Path: path}

	scanner := bufio.NewScanner(f)
	// 默认 64KB 缓冲对大多数 .env 已足够；这里再加保护
	const initialBuf, maxLine = 64 * 1024, 1024 * 1024
	scanner.Buffer(make([]byte, initialBuf), maxLine)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 兼容 "export KEY=value"
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))

		eqIdx := strings.Index(line, "=")
		if eqIdx < 0 {
			return fmt.Errorf("dotenv: %s:%d invalid line (missing '='): %s", path, lineNo, line)
		}
		key := strings.TrimSpace(line[:eqIdx])
		rawVal := strings.TrimSpace(line[eqIdx+1:])
		if key == "" {
			return fmt.Errorf("dotenv: %s:%d empty key", path, lineNo)
		}

		// 解析 value：识别引号类型 → 是否做行尾注释剥离 → 是否做变量插值
		value, expand := parseValue(rawVal)
		if expand {
			value = expandVars(value)
		}
		// 占位符 \x00 还原为字面 '$'（参见 unescapeDouble 中的 \$ 处理）
		value = strings.ReplaceAll(value, "\x00", "$")

		rec.Keys = append(rec.Keys, key)

		if !overload {
			if _, exists := os.LookupEnv(key); exists {
				continue
			}
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("dotenv: setenv %s: %w", key, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("dotenv: read %s: %w", path, err)
	}

	// 记录成功加载（不论是否真的注入了某个 key）
	recMu.Lock()
	loadedAll = append(loadedAll, rec)
	recMu.Unlock()
	return nil
}

// parseValue 解析 value 字符串，返回 (净值, 是否需做变量插值)
//
// 规则（与 dotenv-cli / godotenv 行为对齐）：
//   - "..."  双引号：去除引号，**做** 变量插值，识别 \n / \t / \" 等转义
//   - '...'  单引号：去除引号，**不做** 变量插值，原样保留
//   - 无引号：去除行尾 " #" 注释，**做** 变量插值
func parseValue(s string) (string, bool) {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return unescapeDouble(s[1 : len(s)-1]), true
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1], false
	}
	// 无引号：去掉行尾注释
	return stripInlineComment(s), true
}

// unescapeDouble 处理双引号内的常见转义
func unescapeDouble(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			case '$':
				// 用占位符暂时替代，避免之后被 expandVars 当成引用展开
				b.WriteByte('\x00')
			default:
				// 未知转义保留原样
				b.WriteByte(s[i])
				b.WriteByte(s[i+1])
			}
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// expandVars 替换 ${VAR} 与 $VAR（VAR 由 [A-Za-z_][A-Za-z0-9_]* 组成）
//
// 取值优先级：当前进程环境（含本次 Load 已注入的值）。
// 不存在的变量替换为空串（与 sh 行为一致）。
// `\$` 已在 unescapeDouble 阶段被解释为字面 `$`，到此处不再视为引用。
func expandVars(s string) string {
	if !strings.ContainsRune(s, '$') {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '$' {
			b.WriteByte(s[i])
			continue
		}
		// 末尾孤立 $
		if i+1 >= len(s) {
			b.WriteByte('$')
			continue
		}
		// ${NAME}
		if s[i+1] == '{' {
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 {
				// 未闭合：原样保留
				b.WriteByte('$')
				continue
			}
			name := s[i+2 : i+2+end]
			b.WriteString(os.Getenv(name))
			i += 2 + end
			continue
		}
		// $NAME
		j := i + 1
		for j < len(s) && isVarChar(s[j], j == i+1) {
			j++
		}
		if j == i+1 {
			// $ 后不是合法变量字符，原样输出
			b.WriteByte('$')
			continue
		}
		name := s[i+1 : j]
		b.WriteString(os.Getenv(name))
		i = j - 1
	}
	return b.String()
}

func isVarChar(c byte, first bool) bool {
	switch {
	case c == '_':
		return true
	case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z':
		return true
	case !first && c >= '0' && c <= '9':
		return true
	}
	return false
}

// stripInlineComment 去除非引号区域的行尾注释（兼容旧调用）
func stripInlineComment(s string) string {
	if strings.HasPrefix(s, "\"") || strings.HasPrefix(s, "'") {
		return s
	}
	if idx := strings.Index(s, " #"); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	return s
}
