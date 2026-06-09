// Package cli 中的 prettylog 提供一个 slog.Handler，
// 把日志渲染为 CLI 友好的单行格式，并按 verbose 决定级别。
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
)

// 色彩常量（256 色，与 shell 包视觉一致）
const (
	prettyReset    = "\x1b[0m"
	prettyCyan     = "\x1b[38;5;75m"  // 品蓝
	prettyGreen    = "\x1b[38;5;78m"  // 翠绿
	prettyRed      = "\x1b[38;5;203m" // 珊瑚红
	prettyYellow   = "\x1b[38;5;214m" // 琥珀
	prettyDim      = "\x1b[38;5;245m" // 暗灰
	prettyBright   = "\x1b[1;37m"     // 亮白粗体（沙箱命令高亮）
	prettyToolName = "\x1b[1;38;5;75m"
	prettyStepNum  = "\x1b[38;5;75m"
	prettyElapsed  = "\x1b[38;5;245m"
	prettyBorder   = "\x1b[38;5;240m"
)

// prettyHandler 实现 slog.Handler；
// 输出形如：`  ◆ subagent started agent=Explore model=haiku`，
// 不带时间戳，与终端 CLI 风格统一。
//
// 设计要点：
//   - 默认 minLevel=Warn，让 INFO 级别"运行时遥测"完全静默
//   - Verbose 时 minLevel=Debug，但仍走 pretty 渲染——而非默认 stdlib 的
//     `2026/.. INFO key=value` 多行串联、无对齐
//   - WithAttrs / WithGroup 能正确串联（slog 接口要求）
type prettyHandler struct {
	mu       *sync.Mutex
	out      io.Writer
	level    slog.Level
	useColor bool
	attrs    []slog.Attr
	group    string
}

func newPrettyHandler(out io.Writer, level slog.Level, useColor bool) *prettyHandler {
	return &prettyHandler{
		mu:       &sync.Mutex{},
		out:      out,
		level:    level,
		useColor: useColor,
	}
}

func (h *prettyHandler) Enabled(_ context.Context, lvl slog.Level) bool {
	return lvl >= h.level
}

func (h *prettyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := *h
	out.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &out
}

func (h *prettyHandler) WithGroup(name string) slog.Handler {
	out := *h
	if h.group != "" {
		out.group = h.group + "." + name
	} else {
		out.group = name
	}
	return &out
}

func (h *prettyHandler) Handle(_ context.Context, r slog.Record) error {
	icon, color := levelStyle(r.Level)

	// 收集 attrs（Record 自带 + WithAttrs 串联）
	pairs := make([][2]string, 0, len(h.attrs)+r.NumAttrs())
	for _, a := range h.attrs {
		pairs = appendAttr(pairs, h.group, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		pairs = appendAttr(pairs, h.group, a)
		return true
	})
	// 按 key 字母序输出，避免不同 goroutine 写入顺序导致排版不稳
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i][0] < pairs[j][0] })

	var b strings.Builder
	if h.useColor {
		b.WriteString(color)
		b.WriteString(icon)
		b.WriteString(prettyReset)
	} else {
		b.WriteString(icon)
	}
	b.WriteString(" ")
	b.WriteString(r.Message)
	for _, kv := range pairs {
		b.WriteString(" ")
		if h.useColor {
			b.WriteString(prettyDim)
		}
		b.WriteString(kv[0])
		b.WriteString("=")
		b.WriteString(kv[1])
		if h.useColor {
			b.WriteString(prettyReset)
		}
	}
	b.WriteString("\r\n")

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.out, b.String())
	return err
}

func levelStyle(lvl slog.Level) (icon, color string) {
	switch {
	case lvl >= slog.LevelError:
		return "✗", prettyRed
	case lvl >= slog.LevelWarn:
		return "!", prettyYellow
	case lvl >= slog.LevelInfo:
		return "◆", prettyCyan
	default:
		return "·", prettyDim
	}
}

func appendAttr(pairs [][2]string, group string, a slog.Attr) [][2]string {
	if a.Equal(slog.Attr{}) {
		return pairs
	}
	key := a.Key
	if group != "" {
		key = group + "." + key
	}
	val := a.Value.Resolve()
	if val.Kind() == slog.KindGroup {
		for _, sub := range val.Group() {
			pairs = appendAttr(pairs, key, sub)
		}
		return pairs
	}
	return append(pairs, [2]string{key, formatValue(val)})
}

func formatValue(v slog.Value) string {
	switch v.Kind() {
	case slog.KindString:
		s := v.String()
		if s == "" {
			return `""`
		}
		// 含空格则加引号，便于人眼一行扫读
		if strings.ContainsAny(s, " \t") {
			return fmt.Sprintf("%q", s)
		}
		return s
	case slog.KindInt64:
		return fmt.Sprintf("%d", v.Int64())
	case slog.KindUint64:
		return fmt.Sprintf("%d", v.Uint64())
	case slog.KindFloat64:
		return fmt.Sprintf("%g", v.Float64())
	case slog.KindBool:
		if v.Bool() {
			return "true"
		}
		return "false"
	case slog.KindDuration:
		return v.Duration().String()
	case slog.KindTime:
		return v.Time().Format("15:04:05")
	default:
		return fmt.Sprintf("%v", v.Any())
	}
}
