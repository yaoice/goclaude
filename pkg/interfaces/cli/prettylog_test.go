package cli

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// 默认（非 verbose）级别下，INFO 不应输出，避免在终端制造混乱。
func TestPrettyHandler_DefaultLevel_HidesInfo(t *testing.T) {
	var buf bytes.Buffer
	h := newPrettyHandler(&buf, slog.LevelWarn, false)
	logger := slog.New(h)
	logger.Info("subagent 启动", "agent", "Explore")
	logger.Info("MCP 连接完成", "total", 1)
	if buf.Len() != 0 {
		t.Fatalf("INFO must be hidden at Warn level, got: %q", buf.String())
	}
}

// Verbose 模式下，渲染必须是单行，且不含 "2026/" 这种 stdlib 默认时间戳前缀。
func TestPrettyHandler_VerboseLevel_RendersSingleLineWithoutTimestamp(t *testing.T) {
	var buf bytes.Buffer
	h := newPrettyHandler(&buf, slog.LevelDebug, false)
	logger := slog.New(h)
	logger.Info("subagent 启动",
		"agent_id", "agent-x",
		"agent_type", "Explore",
		"model", "haiku",
	)

	out := buf.String()
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("expected exactly 1 line, got: %q", out)
	}
	if strings.Contains(out, "2026/") || strings.Contains(out, "INFO ") {
		t.Fatalf("must not contain stdlib timestamp / level prefix: %q", out)
	}
	for _, want := range []string{"subagent 启动", "agent_id=agent-x", "agent_type=Explore", "model=haiku"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
}

// 多协程并发输出每条记录都应是完整单行（不允许跨写入交叉）。
func TestPrettyHandler_ConcurrentWrites_NoInterleaving(t *testing.T) {
	var buf bytes.Buffer
	h := newPrettyHandler(&buf, slog.LevelDebug, false)
	logger := slog.New(h)

	const N = 50
	done := make(chan struct{}, N)
	for i := 0; i < N; i++ {
		go func(i int) {
			logger.Info("执行工具", "tool", "glob", "iter", i)
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < N; i++ {
		<-done
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != N {
		t.Fatalf("expected %d lines, got %d", N, len(lines))
	}
	for _, ln := range lines {
		if !strings.HasPrefix(ln, "◆") {
			t.Fatalf("malformed line (missing icon prefix): %q", ln)
		}
		if !strings.Contains(ln, "执行工具") {
			t.Fatalf("malformed line (lost message): %q", ln)
		}
	}
}

// installLogger 必须真正生效（即使被多次调用），并且能在 verbose 关闭时
// 屏蔽任何 INFO 级日志走到 default logger。
func TestInstallLogger_ResetsDefaultLevel(t *testing.T) {
	defer resetLogger()

	// 先开 verbose，再关闭，验证可被重置
	installLoggerForTest(t, true)
	if !slog.Default().Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("verbose mode should enable INFO")
	}

	resetLogger()
	installLoggerForTest(t, false)
	if slog.Default().Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("non-verbose mode should disable INFO at default logger")
	}
}
