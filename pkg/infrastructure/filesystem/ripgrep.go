// Package filesystem - ripgrep.go 提供文件内容搜索（Grep）与文件名匹配（Glob）。
//
// 用户报告："Grep error: exec: \"rg\" executable file not found in $PATH"。
// 本文件历史上**硬依赖** `rg` 与 `find` 两个外部二进制：未装 ripgrep 的机器
// （含很多发行版默认环境、Docker slim 镜像、Windows）会直接整工具失效；
// 即便 rg 装了，旧版 parseRipgrepJSON 也是个 TODO，每行原样塞进 Content，
// 字段都没填——也就是说"装了 rg 也搜不出有用结果"。
//
// 现在的设计：
//   - 优先尝试 ripgrep（更快），并完整解析 rg --json 行（match/begin/end）
//   - rg 不在 PATH（或被 GOCLAUDE_USE_BUILTIN_GREP=1 强制关闭）→ 回落到纯 Go 实现：
//     filepath.Walk + regexp + 跳过常见噪音目录（.git、node_modules、vendor、dist）
//   - Glob 同样去掉 find 依赖，纯 Go 实现，支持 `**` 递归与多分隔符
//
// 设计原则：fallback 必须**在能力上完整**——不是降级成残废。同样的入参能跑出
// 同样的结果，只是吞吐略低；相反，缺二进制时整个工具不可用是绝不能接受的。
package filesystem

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// Grep 内容搜索器。无状态，仅持工作目录。
type Grep struct {
	// workDir 默认起搜目录；GrepOptions.Path 非空时被覆盖
	workDir string
}

// NewGrep 创建 Grep 搜索器。
func NewGrep(workDir string) *Grep { return &Grep{workDir: workDir} }

// GrepOptions 搜索选项。
//
// MaxResults 为 0 表示不限制；ContextLines 为 0 表示无上下文。
type GrepOptions struct {
	Pattern       string
	Path          string
	CaseSensitive bool
	MaxResults    int
	ContextLines  int
	FilePattern   string // file name glob 过滤（如 "*.go"）
}

// GrepResult 单条搜索结果。
type GrepResult struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

// useBuiltinGrep 用户显式要求走纯 Go 实现（绕过 rg）。
//
// 设置场景：
//   - 测试中（避免外部 rg 行为漂移）
//   - 用户反馈 rg 输出有歧义时（如 binary 文件被误识别）
//
// 与 src 的 USE_BUILTIN_RIPGREP 形态一致，但默认值反过来：goclaude 没有嵌入
// vendor/ripgrep，所以不强制要求 rg 存在；只有 rg 实际在 PATH 才用它。
func useBuiltinGrep() bool {
	v := os.Getenv("GOCLAUDE_USE_BUILTIN_GREP")
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}

// hasRipgrep 检查 rg 是否在 PATH。memo 化由调用方按需做（每次启动调用 1 次）。
func hasRipgrep() bool {
	_, err := exec.LookPath("rg")
	return err == nil
}

// Search 执行内容搜索。优先 rg，失败/缺失时落到纯 Go 实现。
//
// 返回 ([]GrepResult, error)：error 仅在内部错误（如 regexp 编译失败）时非空；
// "无匹配"返回 (nil, nil)，与 rg 的 exit code 1 语义对齐。
func (g *Grep) Search(opts GrepOptions) ([]GrepResult, error) {
	if opts.Pattern == "" {
		return nil, errors.New("pattern is required")
	}
	if useBuiltinGrep() || !hasRipgrep() {
		return g.searchBuiltin(opts)
	}
	results, err := g.searchRipgrep(opts)
	if err != nil {
		// rg 启动失败（罕见——已经 LookPath 通过）→ 不要整工具失败，回落
		return g.searchBuiltin(opts)
	}
	return results, nil
}

// searchRipgrep 调用外部 ripgrep；解析 --json 输出。
//
// 与 src/utils/ripgrep.ts 中字段对齐（type=match 时 data.path.text 与
// data.line_number、data.lines.text）。
func (g *Grep) searchRipgrep(opts GrepOptions) ([]GrepResult, error) {
	args := []string{"--json"}
	if !opts.CaseSensitive {
		args = append(args, "-i")
	}
	if opts.MaxResults > 0 {
		args = append(args, "-m", strconv.Itoa(opts.MaxResults))
	}
	if opts.ContextLines > 0 {
		args = append(args, "-C", strconv.Itoa(opts.ContextLines))
	}
	if opts.FilePattern != "" {
		args = append(args, "--glob", opts.FilePattern)
	}
	args = append(args, opts.Pattern)
	if opts.Path != "" {
		args = append(args, opts.Path)
	}

	cmd := exec.Command("rg", args...)
	cmd.Dir = g.workDir
	out, err := cmd.Output()
	if err != nil {
		// rg exit code 1 = no matches；其它视为错误返回让上层 fallback
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, err
	}
	return parseRipgrepJSON(out, opts.MaxResults), nil
}

// rgJSONLine 是 ripgrep --json 输出的一行结构。
//
// 我们只关心 type=match。其它（begin/end/summary/context）忽略。
type rgJSONLine struct {
	Type string `json:"type"`
	Data struct {
		Path struct {
			Text string `json:"text"`
		} `json:"path"`
		Lines struct {
			Text string `json:"text"`
		} `json:"lines"`
		LineNumber int `json:"line_number"`
	} `json:"data"`
}

// parseRipgrepJSON 解析 rg --json 输出（每行一个 JSON 对象）为 GrepResult 列表。
//
// 与旧 TODO 实现的差异：
//   - 旧实现把每行原样塞进 Content，File/Line 全为零值
//   - 新实现严格只取 type=match，并填齐 File/Line/Content
//
// MaxResults 0 表示不限制。trim 行尾换行避免双换行。
func parseRipgrepJSON(b []byte, maxResults int) []GrepResult {
	var results []GrepResult
	scanner := bufio.NewScanner(strings.NewReader(string(b)))
	// 大文件单行可能很长；放大缓冲到 1 MiB
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev rgJSONLine
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Type != "match" {
			continue
		}
		results = append(results, GrepResult{
			File:    ev.Data.Path.Text,
			Line:    ev.Data.LineNumber,
			Content: strings.TrimRight(ev.Data.Lines.Text, "\r\n"),
		})
		if maxResults > 0 && len(results) >= maxResults {
			break
		}
	}
	return results
}

// searchBuiltin 纯 Go 内容搜索：filepath.Walk + regexp + 噪音目录跳过。
//
// 行为与 ripgrep 的常见默认对齐：
//   - 默认大小写不敏感（CaseSensitive=false）
//   - 跳过 .git、node_modules、vendor、dist、build、.next、.idea、.vscode、.cache、target
//   - 跳过 binary 文件（前 512 字节含 NUL）
//   - 文件名 glob 过滤复用 matchFileGlob（支持 * 与 **）
//
// 不做的事（保持简单）：
//   - 不读 .gitignore（rg 的高阶行为；fallback 模式不强求）
//   - 不做并行（fallback 强调正确性 > 性能；用 rg 才追求吞吐）
func (g *Grep) searchBuiltin(opts GrepOptions) ([]GrepResult, error) {
	pattern := opts.Pattern
	if !opts.CaseSensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("compile regex: %w", err)
	}

	root := g.workDir
	if opts.Path != "" {
		if filepath.IsAbs(opts.Path) {
			root = opts.Path
		} else {
			root = filepath.Join(g.workDir, opts.Path)
		}
	}
	if root == "" {
		root = "."
	}

	var results []GrepResult
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// 单个目录读不到（权限）→ 跳过子树继续，整体不失败
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		// 文件名 glob 过滤
		if opts.FilePattern != "" {
			matched, _ := matchFileGlob(opts.FilePattern, d.Name())
			if !matched {
				return nil
			}
		}
		if results = appendFileMatches(results, path, re, opts.MaxResults); opts.MaxResults > 0 && len(results) >= opts.MaxResults {
			return errStopWalk
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errStopWalk) {
		return results, walkErr
	}
	return results, nil
}

// errStopWalk 提前结束 walk 的哨兵错误。
var errStopWalk = errors.New("stop walk")

// appendFileMatches 在单个文件内行扫描，匹配则追加；MaxResults 早停。
//
// 参数 results 既作输入也作输出，便于在 caller 累加；返回新 slice。
func appendFileMatches(results []GrepResult, path string, re *regexp.Regexp, maxResults int) []GrepResult {
	f, err := os.Open(path)
	if err != nil {
		return results
	}
	defer f.Close()
	// 二进制嗅探：前 512 字节含 NUL 视为 binary，跳过
	head := make([]byte, 512)
	n, _ := f.Read(head)
	if isBinary(head[:n]) {
		return results
	}
	// reset 到文件头
	if _, err := f.Seek(0, 0); err != nil {
		return results
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if !re.MatchString(line) {
			continue
		}
		results = append(results, GrepResult{
			File:    path,
			Line:    lineNo,
			Content: line,
		})
		if maxResults > 0 && len(results) >= maxResults {
			return results
		}
	}
	return results
}

// isBinary 简单二进制嗅探：含 NUL 即视为 binary。与 git/rg 默认行为对齐。
func isBinary(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
}

// noiseDirs 是默认跳过的"噪音"目录名集合。
//
// 选择标准：搜索这些目录通常返回的是依赖/构建产物而非源码，浪费时间且产生噪声。
// 与 rg 的 .gitignore 行为不完全一致——rg 会读 .gitignore；这里只做兜底名单。
var noiseDirs = map[string]struct{}{
	".git":         {},
	".hg":          {},
	".svn":         {},
	"node_modules": {},
	"vendor":       {},
	"dist":         {},
	"build":        {},
	"target":       {},
	".next":        {},
	".nuxt":        {},
	".idea":        {},
	".vscode":      {},
	".cache":       {},
	"__pycache__":  {},
}

func shouldSkipDir(name string) bool {
	_, ok := noiseDirs[name]
	return ok
}

// matchFileGlob 文件名 glob 匹配。支持单层 * 和 **。
//
// 实现：先尝试 filepath.Match（标准库 *、? 通配）；若 pattern 含 ** 则做特殊处理：
//   - 含 ** 的模式按 / 切段，把 ** 翻译为 ".*"、单层 * 翻译为 "[^/]*"
//   - 用 regexp 匹配
//
// 注：参数 name 一般是 d.Name()（不含路径），所以 ** 在文件名内层面无意义；
// 但保留扩展能力，方便未来给 GrepOptions.FilePattern 传完整相对路径模式。
func matchFileGlob(pattern, name string) (bool, error) {
	if !strings.Contains(pattern, "**") {
		return filepath.Match(pattern, name)
	}
	re, err := globToRegexp(pattern)
	if err != nil {
		return false, err
	}
	return re.MatchString(name), nil
}

// globToRegexp 把 ** / * / ? glob 转为 regexp。
//
// 规则（与 doublestar 简化版对齐）：
//   - **           → .*
//   - *            → [^/]*
//   - ?            → [^/]
//   - 其它字符     → 正则转义
//   - 锚定为完整匹配（^...$）
func globToRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			b.WriteByte('\\')
			b.WriteByte(pattern[i])
		default:
			b.WriteByte(pattern[i])
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// ============================================================================
// Glob：文件名模式匹配（取代旧实现对外部 `find` 的依赖）
// ============================================================================

// Glob 文件模式匹配器。
type Glob struct {
	workDir string
}

// NewGlob 创建 Glob 匹配器。
func NewGlob(workDir string) *Glob { return &Glob{workDir: workDir} }

// GlobOptions Glob 选项。
type GlobOptions struct {
	// Pattern 支持 *、?、** 通配；其它字符按字面匹配
	Pattern string
	// Path 起搜目录；空时走 workDir
	Path string
}

// Match 执行文件名匹配，返回相对/绝对路径列表（取决于 Path 是否绝对）。
//
// 实现细节：
//   - 不再 fork find（很多发行版/容器/Windows 上不可靠）
//   - filepath.WalkDir + 跳过 noiseDirs，比 find 安全且跨平台一致
//   - pattern 含路径分隔符时，用 globToRegexp 在完整相对路径上匹配（支持 **）
//   - 否则只在 base name 上匹配（与旧 find -name 行为兼容）
func (g *Glob) Match(opts GlobOptions) ([]string, error) {
	pattern := opts.Pattern
	if pattern == "" {
		return nil, errors.New("pattern is required")
	}
	root := g.workDir
	if opts.Path != "" {
		root = opts.Path
	}
	if root == "" {
		root = "."
	}

	hasSep := strings.ContainsAny(pattern, "/"+string(filepath.Separator))
	var fullPathRe *regexp.Regexp
	if hasSep {
		re, err := globToRegexp(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern: %w", err)
		}
		fullPathRe = re
	}

	var out []string
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		var matched bool
		if fullPathRe != nil {
			rel, _ := filepath.Rel(root, path)
			// Windows 反斜杠归一化为 / 让 ** 行为跨平台一致
			if runtime.GOOS == "windows" {
				rel = strings.ReplaceAll(rel, "\\", "/")
			}
			matched = fullPathRe.MatchString(rel)
		} else {
			m, _ := filepath.Match(pattern, d.Name())
			matched = m
		}
		if matched {
			out = append(out, path)
		}
		return nil
	})
	if walkErr != nil {
		return out, walkErr
	}
	return out, nil
}
