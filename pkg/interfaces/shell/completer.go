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

	// Dynamic 可选：返回运行时才确定的候选命令（带 `/` 前缀）。
	//
	// 每次补全时调用，结果与静态 Commands 合并去重。用于补全那些在
	// 构造补全器之后才注入的命令，例如 `/<skill名称>`、`/<skill别名>`
	// 以及插件事后贡献的自定义命令。
	Dynamic func() []string

	// OnListCandidates 当存在多个候选且无法进一步补全时调用
	// 调用方可借此把候选打印到屏幕（一般会写换行→列表→重绘 prompt）
	OnListCandidates func(candidates []string)
}

// commandSet 合并静态命令与动态命令源（动态结果可为空）
func (p *PrefixCompleter) commandSet() []string {
	if p.Dynamic == nil {
		return p.Commands
	}
	out := append([]string(nil), p.Commands...)
	return append(out, p.Dynamic()...)
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

	newToken, ok := resolveCompletion(token, p.commandSet(), p.OnListCandidates)
	if !ok {
		return line, pos
	}
	newLine := head[:start] + newToken + tail
	return newLine, start + len(newToken)
}

// resolveCompletion 计算 token 在 candidates 中的补全结果。
//
// 返回 (newToken, ok)：
//   - ok=false 表示无可替换的补全（无匹配，或已通过 onList 列出多候选）；
//   - ok=true 时 newToken 为替换当前 token 的文本：唯一匹配时带尾随空格，
//     多候选时为可继续的公共前缀。
//
// 被 PrefixCompleter（命令名）与 ArgCompleter（命令参数）共用。
func resolveCompletion(token string, candidates []string, onList func([]string)) (string, bool) {
	var matches []string
	seen := map[string]bool{}
	for _, c := range candidates {
		if c == "" || seen[c] {
			continue
		}
		if strings.HasPrefix(c, token) {
			seen[c] = true
			matches = append(matches, c)
		}
	}
	if len(matches) == 0 {
		return "", false
	}
	// 动态候选不保证有序，统一排序以保证公共前缀计算与候选列表显示稳定。
	sort.Strings(matches)

	// 唯一匹配：直接补完并加 trailing space
	if len(matches) == 1 {
		return matches[0] + " ", true
	}

	// 多候选：取公共前缀
	common := matches[0]
	for _, m := range matches[1:] {
		common = longestCommonPrefix(common, m)
	}
	if len(common) <= len(token) {
		// 无法进一步补全，列出候选
		if onList != nil {
			onList(matches)
		}
		return "", false
	}
	return common, true
}

// ArgCompleter 为指定 slash 命令补全其参数（子命令关键字、名称等）。
//
// 仅当输入行形如 `<cmd> <token>`（cmd 命中 Commands，且光标已离开命令本身）
// 时触发。候选由 Args 回调按"当前 token 之前已输入的参数"动态给出，例如：
//   - `/plugin ` → 子命令 list/marketplaces/enable/disable；
//   - `/plugin enable ` → 已安装插件名。
type ArgCompleter struct {
	// Commands 触发该补全器的命令名（含 `/`，含别名），如 ["/plugin","/plugins"]。
	Commands []string

	// Args 返回当前参数位置的候选；prior 为当前 token 之前已确定的参数
	// （不含命令本身）。返回空切片表示该位置无候选。
	Args func(prior []string) []string

	// OnListCandidates 多候选无法进一步补全时回调（同 PrefixCompleter）。
	OnListCandidates func(candidates []string)
}

// Complete 实现 Completer 接口
func (a *ArgCompleter) Complete(line string, pos int) (string, int) {
	if a.Args == nil {
		return line, pos
	}
	head := line[:pos]
	tail := line[pos:]

	fields := strings.Fields(head)
	if len(fields) == 0 || !a.matchCmd(fields[0]) {
		return line, pos
	}

	// 当前 token：光标前最后一个空白之后；start==0 说明仍在命令本身，不处理。
	start := strings.LastIndexAny(head, " \t") + 1
	if start == 0 {
		return line, pos
	}
	token := head[start:]

	// prior：当前 token 之前已确定的参数。若 head 不以空白结尾，则 fields
	// 的最后一项正是正在输入的 token，需从 prior 中剔除。
	prior := fields[1:]
	if !endsWithSpace(head) && len(prior) > 0 {
		prior = prior[:len(prior)-1]
	}

	cands := a.Args(prior)
	if len(cands) == 0 {
		return line, pos
	}
	newToken, ok := resolveCompletion(token, cands, a.OnListCandidates)
	if !ok {
		return line, pos
	}
	newLine := head[:start] + newToken + tail
	return newLine, start + len(newToken)
}

func (a *ArgCompleter) matchCmd(cmd string) bool {
	for _, c := range a.Commands {
		if c == cmd {
			return true
		}
	}
	return false
}

func endsWithSpace(s string) bool {
	return strings.HasSuffix(s, " ") || strings.HasSuffix(s, "\t")
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
