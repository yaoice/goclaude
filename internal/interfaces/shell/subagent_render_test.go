package shell

import (
	"bytes"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/goclaude/internal/application"
)

// captureStdout 收集 stdout 输出（REPL writeOut 走 stdout）。
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	rdr, wtr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = wtr

	var buf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = buf.ReadFrom(rdr)
	}()
	fn()
	_ = wtr.Close()
	wg.Wait()
	os.Stdout = old
	return buf.String()
}

func TestREPL_HandleSubagentEvent_StartLine(t *testing.T) {
	r := &REPL{useColor: false}
	out := captureStdout(t, func() {
		r.HandleSubagentEvent(application.SubagentEvent{
			Phase:     application.SubagentPhaseStart,
			AgentType: "Explore",
			Model:     "haiku",
		})
	})
	if !strings.Contains(out, "┏━ Explore") {
		t.Fatalf("missing panel header: %q", out)
	}
	if !strings.Contains(out, "haiku") {
		t.Fatalf("missing model: %q", out)
	}
}

func TestREPL_HandleSubagentEvent_FinishSuccessLine(t *testing.T) {
	r := &REPL{useColor: false}
	out := captureStdout(t, func() {
		r.HandleSubagentEvent(application.SubagentEvent{
			Phase:     application.SubagentPhaseFinish,
			AgentType: "Explore",
			Model:     "haiku",
			Status:    application.SubagentStatusSuccess,
			Elapsed:   1200 * time.Millisecond,
			Turns:     4,
		})
	})
	if !strings.Contains(out, "┗━") {
		t.Fatalf("missing panel footer: %q", out)
	}
	if !strings.Contains(out, "done") {
		t.Fatalf("missing done status: %q", out)
	}
	if !strings.Contains(out, "1.2s") {
		t.Fatalf("missing elapsed: %q", out)
	}
	if !strings.Contains(out, "4 steps") {
		t.Fatalf("missing step count: %q", out)
	}
	// 旧契约标记必须彻底消失
	for _, gone := range []string{"◆", "turns="} {
		if strings.Contains(out, gone) {
			t.Fatalf("legacy marker %q should be gone: %q", gone, out)
		}
	}
}

func TestREPL_HandleSubagentEvent_FinishErrorIncludesMessage(t *testing.T) {
	r := &REPL{useColor: false}
	out := captureStdout(t, func() {
		r.HandleSubagentEvent(application.SubagentEvent{
			Phase:        application.SubagentPhaseFinish,
			AgentType:    "Plan",
			Model:        "haiku",
			Status:       application.SubagentStatusError,
			Elapsed:      600 * time.Millisecond,
			ErrorMessage: "exceeded max turns",
		})
	})
	if !strings.Contains(out, "┗━") {
		t.Fatalf("missing footer border: %q", out)
	}
	if !strings.Contains(out, "failed") {
		t.Fatalf("missing failed status: %q", out)
	}
	if !strings.Contains(out, "exceeded max turns") {
		t.Fatalf("missing error message: %q", out)
	}
}

// nil 接收者不应 panic（防御性测试）。
func TestREPL_HandleSubagentEvent_NilSafe(t *testing.T) {
	var r *REPL
	r.HandleSubagentEvent(application.SubagentEvent{Phase: application.SubagentPhaseStart})
}

// 并发：两个 subagent 同时活跃时，步骤/收尾行带上 agentType#tag 归属标签，可区分泳道。
func TestREPL_HandleSubagentEvent_ConcurrentLanesTagged(t *testing.T) {
	r := &REPL{useColor: false}
	out := captureStdout(t, func() {
		r.HandleSubagentEvent(application.SubagentEvent{
			Phase: application.SubagentPhaseStart, AgentType: "explore", AgentID: "agent-aaaa", Model: "haiku",
		})
		r.HandleSubagentEvent(application.SubagentEvent{
			Phase: application.SubagentPhaseStart, AgentType: "coder", AgentID: "agent-bbbb", Model: "sonnet",
		})
		// 两者都活跃 → 进入泳道模式，步骤行应带标签
		r.HandleSubagentEvent(application.SubagentEvent{
			Phase: application.SubagentPhaseProgress, AgentType: "explore", AgentID: "agent-aaaa", Turns: 1, LastTool: "grep",
		})
		r.HandleSubagentEvent(application.SubagentEvent{
			Phase: application.SubagentPhaseProgress, AgentType: "coder", AgentID: "agent-bbbb", Turns: 1, LastTool: "bash",
		})
		// explore 先结束，此时 coder 仍活跃 → 收尾行带标签
		r.HandleSubagentEvent(application.SubagentEvent{
			Phase: application.SubagentPhaseFinish, AgentType: "explore", AgentID: "agent-aaaa",
			Status: application.SubagentStatusSuccess, Turns: 2, ResultPreview: "found 3 hits",
		})
		r.HandleSubagentEvent(application.SubagentEvent{
			Phase: application.SubagentPhaseFinish, AgentType: "coder", AgentID: "agent-bbbb",
			Status: application.SubagentStatusSuccess, Turns: 4,
		})
	})
	for _, want := range []string{
		"explore#aaaa", // explore 的标签出现（步骤/收尾/结果）
		"coder#bbbb",   // coder 的标签出现
		"Exploration",  // explore 的阶段
		"Code Generation",
		"found 3 hits", // explore 的结果行
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("concurrent lane output missing %q in:\n%s", want, out)
		}
	}
}

// 单 agent（无并发）时步骤行保持干净，不插入 #tag 标签。
func TestREPL_HandleSubagentEvent_SingleAgentNoLaneLabel(t *testing.T) {
	r := &REPL{useColor: false}
	out := captureStdout(t, func() {
		r.HandleSubagentEvent(application.SubagentEvent{
			Phase: application.SubagentPhaseStart, AgentType: "coder", AgentID: "agent-zzzz", Model: "sonnet",
		})
		r.HandleSubagentEvent(application.SubagentEvent{
			Phase: application.SubagentPhaseProgress, AgentType: "coder", AgentID: "agent-zzzz", Turns: 1, LastTool: "bash",
		})
	})
	// 步骤行应含 "Code Generation"，紧跟竖线、无标签前缀
	if !strings.Contains(out, "Code Generation") {
		t.Fatalf("single-agent step should contain phase, got:\n%s", out)
	}
	if strings.Contains(out, "coder#zzzz  Code Generation") {
		t.Fatalf("single-agent step must NOT carry a lane label, got:\n%s", out)
	}
}

// 进度阶段化：同阶段多轮只打印一次步骤行，切换阶段时再打印；turn 文本不再逐行刷屏。
func TestREPL_HandleSubagentEvent_ProgressGroupsByPhase(t *testing.T) {
	r := &REPL{useColor: false}
	out := captureStdout(t, func() {
		r.HandleSubagentEvent(application.SubagentEvent{
			Phase: application.SubagentPhaseStart, AgentType: "coder", Model: "sonnet",
		})
		// 两轮 bash 同属 Code Generation：只应出现一次步骤行
		r.HandleSubagentEvent(application.SubagentEvent{
			Phase: application.SubagentPhaseProgress, AgentType: "coder", Turns: 1, LastTool: "bash",
		})
		r.HandleSubagentEvent(application.SubagentEvent{
			Phase: application.SubagentPhaseProgress, AgentType: "coder", Turns: 2, LastTool: "bash",
		})
		// 切换到 File Writing：新增一行步骤
		r.HandleSubagentEvent(application.SubagentEvent{
			Phase: application.SubagentPhaseProgress, AgentType: "coder", Turns: 3, LastTool: "write",
		})
	})
	if strings.Count(out, "Code Generation") != 1 {
		t.Fatalf("Code Generation should appear exactly once in progress, got %q", out)
	}
	if !strings.Contains(out, "File Writing") {
		t.Fatalf("expected File Writing step, got %q", out)
	}
	// 不应再出现旧版逐轮 "turn N tool" 密集文本
	if strings.Contains(out, "turn 1") || strings.Contains(out, "turn 2") {
		t.Fatalf("per-turn dense text should be gone, got %q", out)
	}
}

// 结束时打印面板收尾行 + 结果行；结果与执行过程视觉分离。
func TestREPL_HandleSubagentEvent_FinishRendersPanel(t *testing.T) {
	r := &REPL{useColor: false}
	out := captureStdout(t, func() {
		r.HandleSubagentEvent(application.SubagentEvent{
			Phase: application.SubagentPhaseStart, AgentType: "coder", Model: "sonnet",
		})
		r.HandleSubagentEvent(application.SubagentEvent{
			Phase: application.SubagentPhaseProgress, AgentType: "coder", Turns: 1, LastTool: "team_create",
		})
		r.HandleSubagentEvent(application.SubagentEvent{
			Phase: application.SubagentPhaseProgress, AgentType: "coder", Turns: 2, LastTool: "write",
		})
		r.HandleSubagentEvent(application.SubagentEvent{
			Phase: application.SubagentPhaseFinish, AgentType: "coder", Model: "sonnet",
			Status: application.SubagentStatusSuccess, Turns: 2,
			ResultPreview: "Generated src/main.go",
		})
	})
	for _, want := range []string{"┏━ coder", "Team Setup", "File Writing", "done", "Generated src/main.go"} {
		if !strings.Contains(out, want) {
			t.Fatalf("finish panel missing %q in %q", want, out)
		}
	}
}

// 失败结束：收尾行显示 failed + 错误信息。
func TestREPL_HandleSubagentEvent_FinishErrorFooter(t *testing.T) {
	r := &REPL{useColor: false}
	out := captureStdout(t, func() {
		r.HandleSubagentEvent(application.SubagentEvent{
			Phase: application.SubagentPhaseStart, AgentType: "coder", Model: "sonnet",
		})
		r.HandleSubagentEvent(application.SubagentEvent{
			Phase: application.SubagentPhaseProgress, AgentType: "coder", Turns: 1, LastTool: "bash",
		})
		r.HandleSubagentEvent(application.SubagentEvent{
			Phase: application.SubagentPhaseFinish, AgentType: "coder",
			Status: application.SubagentStatusError, ErrorMessage: "boom", Turns: 1,
		})
	})
	if !strings.Contains(out, "┗━") {
		t.Fatalf("expected footer border in %q", out)
	}
	if !strings.Contains(out, "failed") {
		t.Fatalf("expected failed status in %q", out)
	}
	if !strings.Contains(out, "boom") {
		t.Fatalf("expected error message in %q", out)
	}
}

