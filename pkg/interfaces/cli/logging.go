// Package cli - logging.go 安装 slog 默认 handler，
// 让普通模式下不再把 INFO 级别日志直接打印到终端，
// 避免 `2026/05/25 ... INFO 执行工具 tool=glob id=...` 这样的乱序输出与
// REPL/CLI 自己的彩色进度行混在一起。
//
// 设计：
//   - 非 verbose：仅 WARN/ERROR 输出到 stderr（保留关键事故信号）
//   - verbose（-v）：Debug 起，且打印到 stderr，便于排障
//   - 输出走 prettyHandler，单行、无时间戳、级别图标 + 颜色，与 REPL 风格一致
package cli

import (
	"log/slog"
	"os"
	"sync"

	"golang.org/x/term"
)

var (
	loggerMu       sync.Mutex
	loggerOriginal = slog.Default()
)

// installLogger 根据 verbose 安装默认 slog logger。
//
// 与早期实现不同的是：本函数 **不使用 sync.Once**，而是每次都重置默认
// logger。原因——`goclaude run` 与 `goclaude` REPL 在同一进程中分别走
// runFullQuery / runREPL，曾经的 once 实现只让首次生效，子命令再开 verbose
// 会被忽略；同时也方便测试 reset。
func installLogger(verbose bool) {
	loggerMu.Lock()
	defer loggerMu.Unlock()
	level := slog.LevelWarn
	if verbose {
		level = slog.LevelDebug
	}
	useColor := isStderrTTY() && os.Getenv("NO_COLOR") == ""
	handler := newPrettyHandler(os.Stderr, level, useColor)
	slog.SetDefault(slog.New(handler))
}

// resetLogger 恢复进程刚启动时的 default logger（仅供测试使用）。
func resetLogger() {
	loggerMu.Lock()
	defer loggerMu.Unlock()
	slog.SetDefault(loggerOriginal)
}

// installLoggerForTest 对 installLogger 的薄包装；测试中复用避免引入并发竞争。
func installLoggerForTest(_ testingTB, verbose bool) {
	installLogger(verbose)
}

// testingTB 是 *testing.T / *testing.B 的最小子集，避免测试包暴露依赖。
type testingTB interface {
	Helper()
}

// isStderrTTY 判断 stderr 是否连到真实终端，用于决定是否上色。
func isStderrTTY() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
}
