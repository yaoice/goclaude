package shell

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PrefixCompleter 基于"已知前缀集合"的简单补全器
//
// 用于补全 `/help`、`/exit` 等内置命令。
// 规则：
//   - 仅当光标处于行首 token（前面没有空格）时触发；
//   - token 必须以 `/` 开头；
//   - 找到唯一前缀匹配 → 直接补完；
//   - 多匹配 → 取公共前缀；若已是公共前缀 → 列出全部候选到 stdout（由调用方控制）。
type PrefixCompleter struct {
	Commands []string

	// OnListCandidates 当存在多个候选且无法进一步补全时调用
	// 调用方可借此把候选打印到屏幕（一般会写换行→列表→重绘 prompt）
	OnListCandidates func(candidates []string)
}

// NewSlashCompleter 构造默认的 slash 命令补全器
func NewSlashCompleter(commands []string, onList func([]string)) *PrefixCompleter {
	cp := make([]string, len(commands))
	copy(cp, commands)
	sort.Strings(cp)
	return &PrefixCompleter{Commands: cp, OnListCandidates: onList}
}

// Complete 实现 Completer 接口
func (p *PrefixCompleter) Complete(line string, pos int) (string, int) {
	// 截至光标
	head := line[:pos]
	tail := line[pos:]

	// 找到当前 token 的起点（最近一个空白）
	start := strings.LastIndexAny(head, " \t") + 1
	token := head[start:]
	if token == "" || !strings.HasPrefix(token, "/") {
		return line, pos
	}

	var matches []string
	for _, c := range p.Commands {
		if strings.HasPrefix(c, token) {
			matches = append(matches, c)
		}
	}
	if len(matches) == 0 {
		return line, pos
	}

	// 唯一匹配：直接补完并加 trailing space
	if len(matches) == 1 {
		newToken := matches[0] + " "
		newLine := head[:start] + newToken + tail
		return newLine, start + len(newToken)
	}

	// 多候选：取公共前缀
	common := matches[0]
	for _, m := range matches[1:] {
		common = longestCommonPrefix(common, m)
	}

	if len(common) <= len(token) {
		// 无法进一步补全，列出候选
		if p.OnListCandidates != nil {
			p.OnListCandidates(matches)
		}
		return line, pos
	}

	// 用公共前缀替换 token
	newLine := head[:start] + common + tail
	return newLine, start + len(common)
}

// PathCompleter 文件路径补全（用于 `@<path>` 等场景；当前未默认启用）
type PathCompleter struct {
	// Root 限定补全范围；空表示当前目录
	Root string
	// OnListCandidates 多候选回调（同 PrefixCompleter）
	OnListCandidates func(candidates []string)
}

// Complete 实现 Completer 接口（仅在 token 以 @ 开头时触发）
func (p *PathCompleter) Complete(line string, pos int) (string, int) {
	head := line[:pos]
	tail := line[pos:]
	start := strings.LastIndexAny(head, " \t") + 1
	token := head[start:]
	if !strings.HasPrefix(token, "@") {
		return line, pos
	}
	pathPart := token[1:]

	dir := p.Root
	if dir == "" {
		dir = "."
	}
	base := dir
	prefix := pathPart
	if idx := strings.LastIndex(pathPart, "/"); idx >= 0 {
		base = filepath.Join(dir, pathPart[:idx])
		prefix = pathPart[idx+1:]
	}

	entries, err := os.ReadDir(base)
	if err != nil {
		return line, pos
	}
	var matches []string
	for _, ent := range entries {
		name := ent.Name()
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}
		if strings.HasPrefix(name, prefix) {
			if ent.IsDir() {
				name += "/"
			}
			matches = append(matches, name)
		}
	}
	if len(matches) == 0 {
		return line, pos
	}
	sort.Strings(matches)

	// 唯一匹配：直接拼回
	if len(matches) == 1 {
		completed := "@" + pathPart[:len(pathPart)-len(prefix)] + matches[0]
		newLine := head[:start] + completed + tail
		return newLine, start + len(completed)
	}

	common := matches[0]
	for _, m := range matches[1:] {
		common = longestCommonPrefix(common, m)
	}
	if len(common) <= len(prefix) {
		if p.OnListCandidates != nil {
			p.OnListCandidates(matches)
		}
		return line, pos
	}

	newToken := "@" + pathPart[:len(pathPart)-len(prefix)] + common
	newLine := head[:start] + newToken + tail
	return newLine, start + len(newToken)
}

// CompositeCompleter 串联多个 completer：依次尝试，第一个有变化的胜出
type CompositeCompleter struct {
	Inner []Completer
}

// Complete 实现 Completer 接口
func (c *CompositeCompleter) Complete(line string, pos int) (string, int) {
	for _, inner := range c.Inner {
		newLine, newPos := inner.Complete(line, pos)
		if newLine != line || newPos != pos {
			return newLine, newPos
		}
	}
	return line, pos
}

func longestCommonPrefix(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return a[:i]
}
