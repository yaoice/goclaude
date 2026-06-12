// Package shell - env_overview.go 实现 /env 命令的渲染逻辑。
//
// 用户痛点："有 .env 文件，能否统一下"——回答：3 种配置入口已统一为同一组
// GOCLAUDE_* 环境变量，但变量越多，"这个值到底从哪里来"越难追。这里给出
// 单一命令 `/env` 一行答出来：
//
//	GOCLAUDE_PERMISSION_MODE     set    .env
//	GOCLAUDE_USE_BUILTIN_GREP    unset  -
//	DEEPSEEK_API_KEY             set    .env  (hidden)
//
// 设计约束：
//   - 仅展示 set/unset + 来源路径，**永不打印值**（避免 API key 泄漏到截图/日志）
//   - 来源查找只看 dotenv.Loaded() 与 settingsenv.Loaded() 已注入记录；
//     已 set 但找不到来源 → 标 "shell or --env-file"
//   - 关键变量集（whitelist）显式列出，避免把系统几百个无关 env 也打出来
package shell

import (
	"os"
	"sort"
	"strings"

	"github.com/yaoice/goclaude/pkg/util/dotenv"
	"github.com/yaoice/goclaude/pkg/util/settingsenv"
)

// envStatus 描述一个变量的当前状态与来源。
type envStatus struct {
	Name string
	Set  bool   // 该变量在进程 env 中是否存在
	From string // 来源描述：文件路径 / "shell or --env-file" / ""
}

// trackedEnvVars 列出 /env 默认展示的关键变量。
//
// 选取标准：goclaude 真正读取（影响运行时行为）的变量。
// 系统级变量（PATH/HOME/SHELL 等）不列出，避免噪声。
// 当未来新增运行时开关时，**只需要在这里加一行**——/env 与 /help 文档自动对齐。
var trackedEnvVars = []string{
	// 运行时开关
	"GOCLAUDE_PERMISSION_MODE",
	"GOCLAUDE_USE_BUILTIN_GREP",
	// API key（仅显示 set/unset，不打印值）
	"DEEPSEEK_API_KEY",
	"DEEPSEEK_BASE_URL",
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_BASE_URL",
	// 团队协作（与 RUNBOOK.md 对齐）
	"GOCLAUDE_TEAM_NAME",
	"GOCLAUDE_AGENT_NAME",
}

// sensitiveKeyPattern 用于判断变量名是否敏感（决定 /env 是否标 hidden）。
//
// 与官方 claude 行为一致：包含 KEY/TOKEN/SECRET/PASSWORD 的变量永远脱敏，
// 即便它们出现在 trackedEnvVars 里也仅展示 "set / unset"，不展示值。
//
// 注：我们本来就不打印**任何**变量的值（见 collectEnvSources），所以这个函数
// 当前只用于 /env 输出的标识列。保留它是为了将来如果加 --show-values 等开关时
// 立即知道哪些键必须强制脱敏。
func isSensitiveEnv(name string) bool {
	upper := strings.ToUpper(name)
	for _, kw := range []string{"KEY", "TOKEN", "SECRET", "PASSWORD"} {
		if strings.Contains(upper, kw) {
			return true
		}
	}
	return false
}

// envSourceLookup 由 collectEnvSources 内部构造：name → 来源路径。
//
// 抽象为函数类型方便测试替换（注入假的 dotenv/settingsenv 记录）。
type envSourceLookup func(name string) string

// realEnvSourceLookup 把 dotenv.Loaded() 与 settingsenv.Loaded() 合并为 name→path 索引。
//
// 两条链按"先来者优先"的原则：dotenv 在前，settingsenv 在后；同一 name 在多条记录
// 中出现时，只取**最先加载**的那条（与实际注入行为一致——后加载的不会覆盖）。
func realEnvSourceLookup() envSourceLookup {
	idx := map[string]string{}
	for _, rec := range dotenv.Loaded() {
		for _, k := range rec.Keys {
			if _, exists := idx[k]; !exists {
				idx[k] = rec.Path
			}
		}
	}
	for _, rec := range settingsenv.Loaded() {
		for _, k := range rec.Keys {
			if _, exists := idx[k]; !exists {
				idx[k] = rec.Path
			}
		}
	}
	return func(name string) string {
		return idx[name]
	}
}

// collectEnvSources 收集 names 中每个变量的当前状态。
//
// lookup 为 nil 时使用 realEnvSourceLookup（注入式设计，便于单测）。
// 返回顺序：按 name 字母序，对 /env 输出稳定有利。
func collectEnvSources(names []string, lookup envSourceLookup) []envStatus {
	if lookup == nil {
		lookup = realEnvSourceLookup()
	}
	out := make([]envStatus, 0, len(names))
	for _, n := range names {
		val, ok := os.LookupEnv(n)
		_ = val // 显式声明：我们看 "是否存在" 而非内容
		var from string
		if ok {
			if from = lookup(n); from == "" {
				// 已 set 但不在任何 loaded 记录里 → 来自 shell export 或 --env-file
				from = "shell or --env-file"
			}
		}
		out = append(out, envStatus{Name: n, Set: ok, From: from})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// renderEnvOverview 把 collectEnvSources 的结果渲染成对齐的多行文本。
//
// 拆出来便于单测；printEnvOverview 仅做 IO。
func (r *REPL) renderEnvOverview(statuses []envStatus) string {
	var sb strings.Builder

	sb.WriteString(r.colorize("▌ Effective environment (values are never printed)", colorCyan))
	sb.WriteString("\r\n")

	// 计算 name 列宽（按可视宽度，与 helpRow 对齐）
	leftW := 0
	for _, s := range statuses {
		if w := visibleWidth(s.Name); w > leftW {
			leftW = w
		}
	}
	leftW += 4

	for _, s := range statuses {
		// 第一列：name（cyan）
		left := r.colorize(s.Name, colorAccent)
		// 第二列：set/unset 状态
		statusCol := r.colorize("unset", colorDim)
		fromCol := r.colorize("-", colorDim)
		if s.Set {
			label := "set"
			if isSensitiveEnv(s.Name) {
				label = "set (hidden)"
			}
			statusCol = r.colorize(label, colorGreen)
			fromCol = r.colorize(s.From, colorDim)
		}
		// 拼接：name + pad + status + 2 space + from
		pad := leftW - visibleWidth(s.Name)
		if pad < 2 {
			pad = 2
		}
		sb.WriteString("  ")
		sb.WriteString(left)
		sb.WriteString(strings.Repeat(" ", pad))
		// status 列也右补，让 from 列对齐
		const statusW = 14
		sb.WriteString(statusCol)
		spad := statusW - visibleWidth(stripANSI(statusCol))
		if spad < 2 {
			spad = 2
		}
		sb.WriteString(strings.Repeat(" ", spad))
		sb.WriteString(fromCol)
		sb.WriteString("\r\n")
	}

	// Footer：提示 /help 的 Configuration sources 节
	sb.WriteString("\r\n")
	sb.WriteString(r.colorize(
		"Tip: see \"/help → Configuration sources\" for where to set each variable.",
		colorDim))
	sb.WriteString("\r\n")

	return sb.String()
}

// printEnvOverview 渲染并输出 /env 命令的结果。
func (r *REPL) printEnvOverview() {
	statuses := collectEnvSources(trackedEnvVars, nil)
	r.writeOut(r.renderEnvOverview(statuses))
}

// stripANSI 去掉字符串中的 ANSI 转义序列，用于按可视宽度对齐。
//
// 与 visibleWidth 内部逻辑略有不同：visibleWidth 直接算宽度；这里需要返回**字符串**
// 再交给 visibleWidth 算（避免在颜色文本里二次嵌套着色后宽度计算错乱）。
func stripANSI(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] != 0x1b {
			b.WriteRune(runes[i])
			continue
		}
		// 跳过 ESC [ ... 终止字符（@-~）
		if i+1 < len(runes) && runes[i+1] == '[' {
			j := i + 2
			for j < len(runes) {
				c := runes[j]
				if c >= '@' && c <= '~' {
					break
				}
				j++
			}
			i = j
			continue
		}
		// 单字节 ESC X：吃 X
		if i+1 < len(runes) {
			i++
		}
	}
	return b.String()
}
