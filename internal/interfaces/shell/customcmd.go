package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/anthropics/goclaude/pkg/frontmatter"
)

// CustomCommand 用户自定义的 prompt-类 slash 命令
//
// 与 src `services/customCommands.ts` + `utils/argumentSubstitution.ts` 对齐：
//
//	加载来源（优先级递增，后加载覆盖前者）：
//	  1. ~/.claude/commands/                  user
//	  2. <cwd>/.claude/commands/              project
//	   - 文件名 = 命令名（不含 .md）
//	   - 子目录支持："~/.claude/commands/git/commit.md" → /git:commit
//
//	frontmatter 字段：
//	  description: string         # /help 中显示
//	  argument-hint: string       # /help 中显示参数提示
//	  arguments:    string|[]     # 命名参数（空格分隔或 yaml 数组）
//
//	参数注入（按顺序应用）：
//	  $name        命名参数（来自 frontmatter.arguments）
//	  $ARGUMENTS[N] 索引位置参数
//	  $N           索引位置参数（短形式，N 不能跟字母数字）
//	  $ARGUMENTS   完整原始 args 字符串
//	若无任何占位符且 args 非空 → 末尾追加 "ARGUMENTS: <args>"
type CustomCommand struct {
	Name          string // 命令名（不含前导 /，含子目录前缀如 "git:commit"）
	Description   string
	ArgumentHint  string
	ArgumentNames []string
	Source        string // user / project
	FilePath      string
	Body          string // 去掉 frontmatter 后的正文
}

// CustomCommands 自定义命令注册表
type CustomCommands struct {
	byName map[string]*CustomCommand
	names  []string
}

// NewCustomCommands 构造空注册表
func NewCustomCommands() *CustomCommands {
	return &CustomCommands{byName: map[string]*CustomCommand{}}
}

// LoadDefaults 从用户和项目目录加载所有 *.md 命令
//
// 遇到错误（个别文件解析失败）会跳过该文件并继续，最后返回 nil。
// 上层若需调试可注入 logger，目前以"宽容失败"为主。
func (c *CustomCommands) LoadDefaults(projectCwd string) {
	dirs := []struct {
		dir    string
		source string
	}{}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, struct {
			dir    string
			source string
		}{dir: filepath.Join(home, ".claude", "commands"), source: "user"})
	}
	if projectCwd != "" {
		dirs = append(dirs, struct {
			dir    string
			source string
		}{dir: filepath.Join(projectCwd, ".claude", "commands"), source: "project"})
	}
	for _, d := range dirs {
		c.loadFromDir(d.dir, d.source)
	}
	c.rebuildNames()
}

// loadFromDir 递归加载某个目录下的 *.md 命令
func (c *CustomCommands) loadFromDir(dir, source string) {
	if dir == "" {
		return
	}
	root, err := os.Stat(dir)
	if err != nil || !root.IsDir() {
		return
	}
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(info.Name()), ".md") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		// 命令名：相对 dir 的路径去 .md，目录分隔符替换为 ":"
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}
		rel = strings.TrimSuffix(rel, filepath.Ext(rel))
		name := strings.ReplaceAll(rel, string(filepath.Separator), ":")

		fm, body, err := frontmatter.Parse(string(raw))
		if err != nil {
			// 解析失败仍把整体当 body 注册，避免用户调试困难
			body = string(raw)
			fm = frontmatter.Data{}
		}
		cmd := &CustomCommand{
			Name:         name,
			Description:  frontmatter.GetString(fm, "description"),
			ArgumentHint: frontmatter.GetString(fm, "argument-hint"),
			Source:       source,
			FilePath:     path,
			Body:         strings.TrimSpace(body),
		}
		// arguments 字段：可为字符串（空格分隔）或字符串数组
		if names := parseArgumentNames(fm["arguments"]); len(names) > 0 {
			cmd.ArgumentNames = names
		}
		c.byName[name] = cmd
		return nil
	})
}

func (c *CustomCommands) rebuildNames() {
	c.names = c.names[:0]
	for n := range c.byName {
		c.names = append(c.names, n)
	}
	sort.Strings(c.names)
}

// Get 按名字查找
func (c *CustomCommands) Get(name string) (*CustomCommand, bool) {
	cmd, ok := c.byName[strings.TrimPrefix(name, "/")]
	return cmd, ok
}

// Names 返回所有命令名（不含前导 /，已排序）
func (c *CustomCommands) Names() []string {
	out := make([]string, len(c.names))
	copy(out, c.names)
	return out
}

// SlashNames 返回带 / 前缀的命令名（用于补全）
func (c *CustomCommands) SlashNames() []string {
	out := make([]string, 0, len(c.names))
	for _, n := range c.names {
		out = append(out, "/"+n)
	}
	return out
}

// Render 用给定 args 渲染命令正文
func (c *CustomCommand) Render(args string) string {
	return substituteArguments(c.Body, args, true, c.ArgumentNames)
}

// ----- 参数解析 / 替换（对齐 src/utils/argumentSubstitution.ts） -----

// parseArguments 把 args 字符串切成 token，支持双引号/单引号包裹
func parseArguments(args string) []string {
	args = strings.TrimSpace(args)
	if args == "" {
		return nil
	}
	var (
		out   []string
		buf   strings.Builder
		quote byte
	)
	flush := func() {
		if buf.Len() > 0 {
			out = append(out, buf.String())
			buf.Reset()
		}
	}
	for i := 0; i < len(args); i++ {
		c := args[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				buf.WriteByte(c)
			}
		case c == '"' || c == '\'':
			quote = c
		case c == ' ' || c == '\t':
			flush()
		default:
			buf.WriteByte(c)
		}
	}
	flush()
	return out
}

// parseArgumentNames 从 frontmatter.arguments 字段提取命名参数
func parseArgumentNames(v any) []string {
	isValid := func(s string) bool {
		s = strings.TrimSpace(s)
		if s == "" {
			return false
		}
		// 纯数字与 $0/$1 短形式冲突，过滤掉
		_, err := strconv.Atoi(s)
		return err != nil
	}
	switch x := v.(type) {
	case string:
		var out []string
		for _, p := range strings.Fields(x) {
			if isValid(p) {
				out = append(out, p)
			}
		}
		return out
	case []string:
		var out []string
		for _, p := range x {
			if isValid(p) {
				out = append(out, p)
			}
		}
		return out
	case []any:
		var out []string
		for _, e := range x {
			if s, ok := e.(string); ok && isValid(s) {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

var (
	reArgIndexed = regexp.MustCompile(`\$ARGUMENTS\[(\d+)\]`)
	reArgShort   = regexp.MustCompile(`\$(\d+)(?:\W|$)`)
)

// substituteArguments 在 content 中替换占位符
//
// 顺序与 src 一致：命名 → ARGUMENTS[N] → $N → ARGUMENTS（整段）。
// 若没有任何替换发生且 args 非空 + appendIfNoPlaceholder=true → 末尾追加。
func substituteArguments(content, args string, appendIfNoPlaceholder bool, argNames []string) string {
	if args == "" && len(argNames) == 0 {
		// 与 src 不同：当 args 为空时仍尝试替换为空字符串
	}
	parsed := parseArguments(args)
	original := content

	// 1) 命名参数：$name（后面不能紧跟 [ 或字母数字）
	for i, name := range argNames {
		if name == "" {
			continue
		}
		val := ""
		if i < len(parsed) {
			val = parsed[i]
		}
		// (?:^|[^\w]) 避免误伤；但 Go 不支持回顾断言，这里用 ReplaceAllStringFunc 自行边界判断
		re := regexp.MustCompile(`\$` + regexp.QuoteMeta(name) + `(?:[^\w\[]|$)`)
		content = re.ReplaceAllStringFunc(content, func(m string) string {
			tail := ""
			if len(m) > len("$"+name) {
				tail = m[len("$"+name):]
			}
			return val + tail
		})
	}

	// 2) $ARGUMENTS[N]
	content = reArgIndexed.ReplaceAllStringFunc(content, func(m string) string {
		// 提取数字
		s := m[len("$ARGUMENTS[") : len(m)-1]
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n >= len(parsed) {
			return ""
		}
		return parsed[n]
	})

	// 3) $N（不与字母数字相邻）
	content = reArgShort.ReplaceAllStringFunc(content, func(m string) string {
		// 末尾可能有非字母数字字符，需要保留
		// m 形如 "$3 " 或 "$3" 或 "$3."
		// 拿数字
		i := 1
		for i < len(m) && m[i] >= '0' && m[i] <= '9' {
			i++
		}
		nstr := m[1:i]
		tail := m[i:]
		n, err := strconv.Atoi(nstr)
		if err != nil || n < 0 || n >= len(parsed) {
			return tail
		}
		return parsed[n] + tail
	})

	// 4) $ARGUMENTS（整段）
	content = strings.ReplaceAll(content, "$ARGUMENTS", args)

	// 5) 没替换 + 有 args → 末尾追加
	if content == original && appendIfNoPlaceholder && args != "" {
		content = content + fmt.Sprintf("\n\nARGUMENTS: %s", args)
	}
	return content
}
