package application

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropics/goclaude/pkg/domain/agent"
	"github.com/anthropics/goclaude/pkg/domain/query"
	"github.com/anthropics/goclaude/pkg/domain/tool"
)

// recordingSubagentListener 收集 SubagentEvent，断言 Phase/字段。
type recordingSubagentListener struct {
	mu     sync.Mutex
	events []SubagentEvent
}

func (l *recordingSubagentListener) HandleSubagentEvent(ev SubagentEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, ev)
}

func (l *recordingSubagentListener) snapshot() []SubagentEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]SubagentEvent, len(l.events))
	copy(out, l.events)
	return out
}

func (l *recordingSubagentListener) phases() []SubagentPhase {
	out := []SubagentPhase{}
	for _, ev := range l.snapshot() {
		out = append(out, ev.Phase)
	}
	return out
}

// ---- 1) FilterTools：默认硬剔除保留集 --------------------------------------------------

func TestFilterTools_ReservedToolsRemovedByDefault(t *testing.T) {
	parent := []tool.Tool{
		&recordTool{name: "read_file", readonly: true, concurrent: true},
		&recordTool{name: "Agent"},     // reserved
		&recordTool{name: "Task"},      // reserved
		&recordTool{name: "team_create"},
		&recordTool{name: "send_message"},
		&recordTool{name: "bash"},
	}
	def := &agent.Definition{AgentType: "Plain"}

	out := FilterTools(parent, def)
	names := toolNames(out)
	// 保留集应被剔除
	for _, banned := range []string{"Agent", "Task", "team_create", "send_message"} {
		if contains(names, banned) {
			t.Errorf("reserved tool %q must be removed; got %v", banned, names)
		}
	}
	// 普通工具应保留
	for _, want := range []string{"read_file", "bash"} {
		if !contains(names, want) {
			t.Errorf("ordinary tool %q must be kept; got %v", want, names)
		}
	}
}

// AllowSubagentChaining=true 时保留集不再被强制剔除。
func TestFilterTools_AllowSubagentChainingKeepsReserved(t *testing.T) {
	parent := []tool.Tool{
		&recordTool{name: "Agent"},
		&recordTool{name: "send_message"},
		&recordTool{name: "read_file", readonly: true, concurrent: true},
	}
	def := &agent.Definition{AgentType: "Dispatcher", AllowSubagentChaining: true}
	names := toolNames(FilterTools(parent, def))
	for _, want := range []string{"Agent", "send_message", "read_file"} {
		if !contains(names, want) {
			t.Errorf("AllowSubagentChaining=true must keep %q; got %v", want, names)
		}
	}
}

// 保留集剔除优先于 Tools 白名单：即便用户在白名单里列出 Agent，也必须被剔除。
func TestFilterTools_ReservedBeatsWhitelist(t *testing.T) {
	parent := []tool.Tool{
		&recordTool{name: "Agent"},
		&recordTool{name: "read_file", readonly: true, concurrent: true},
	}
	def := &agent.Definition{
		AgentType: "Curious",
		Tools:     []string{"Agent", "read_file"}, // 用户尝试 sneak in
	}
	names := toolNames(FilterTools(parent, def))
	if contains(names, "Agent") {
		t.Fatalf("Agent must NOT be smuggled via whitelist when chaining disabled; got %v", names)
	}
	if !contains(names, "read_file") {
		t.Fatalf("read_file (legitimate whitelist entry) missing; got %v", names)
	}
}

// IsReservedSubagentTool 暴露的对齐工具应与内部集合一致。
func TestIsReservedSubagentTool(t *testing.T) {
	for _, n := range []string{"Agent", "Task", "agent", "team_create", "team_delete", "send_message"} {
		if !IsReservedSubagentTool(n) {
			t.Errorf("%q should be reserved", n)
		}
	}
	for _, n := range []string{"read_file", "bash", "mcp__x__y", ""} {
		if IsReservedSubagentTool(n) {
			t.Errorf("%q must NOT be reserved", n)
		}
	}
}

// ---- 2) Subagent 事件：Start/Progress/Finish 全生命周期 -----------------------------------

func TestAgentService_SubagentEvent_StartProgressFinish(t *testing.T) {
	svc := NewAgentService(slog.Default())
	svc.Registry().Register(&agent.Definition{
		AgentType:    "Probe",
		WhenToUse:    "probe",
		SystemPrompt: "p",
		Source:       agent.SourceBuiltIn,
	})

	listener := &recordingSubagentListener{}
	svc.SetSubagentEventListener(listener)

	// 两轮：第一轮触发 read_file 工具，第二轮 end_turn。预期产出 Start + 2*Progress + Finish。
	parentReg := tool.NewRegistry()
	_ = parentReg.Register(&recordTool{name: "read_file", readonly: true, concurrent: true})
	prov := &scriptedProvider{
		turns: []scriptTurn{
			{toolName: "read_file", toolID: "c1", toolInput: map[string]interface{}{}},
			{text: "Final result line 1\nLine 2"},
		},
	}
	factory := NewDefaultAgentEngineFactory(parentReg, prov, query.NewTokenBudget(100000, 0.8), slog.Default())

	if _, err := svc.Run(context.Background(), "Probe", factory, RunOptions{
		Prompt: "go", DefaultModel: "t",
	}); err != nil {
		t.Fatalf("run: %v", err)
	}

	events := listener.snapshot()
	phases := []SubagentPhase{}
	for _, ev := range events {
		phases = append(phases, ev.Phase)
	}
	// 必须先 Start，最后 Finish；中间出现至少一次 Progress
	if len(phases) < 3 {
		t.Fatalf("expected >=3 events, got %d: %v", len(phases), phases)
	}
	if phases[0] != SubagentPhaseStart {
		t.Errorf("first event must be Start; got %v", phases)
	}
	if phases[len(phases)-1] != SubagentPhaseFinish {
		t.Errorf("last event must be Finish; got %v", phases)
	}
	gotProgress := false
	var progressEv SubagentEvent
	for _, ev := range events {
		if ev.Phase == SubagentPhaseProgress {
			gotProgress = true
			progressEv = ev
			break
		}
	}
	if !gotProgress {
		t.Fatalf("must emit at least one Progress event; phases=%v", phases)
	}
	// Progress.Turns 应单调递增且 >=1
	if progressEv.Turns < 1 {
		t.Errorf("Progress.Turns must be >=1; got %d", progressEv.Turns)
	}
	// 第一轮触发 read_file 工具，LastTool 应被探针抓到
	if progressEv.LastTool != "read_file" {
		t.Errorf("Progress.LastTool = %q, want read_file", progressEv.LastTool)
	}

	// Finish 携带 ResultPreview（首行）
	finish := events[len(events)-1]
	if finish.Status != SubagentStatusSuccess {
		t.Errorf("Finish.Status = %v, want Success", finish.Status)
	}
	if !strings.Contains(finish.ResultPreview, "Final result line 1") {
		t.Errorf("ResultPreview should contain first line; got %q", finish.ResultPreview)
	}
	if strings.Contains(finish.ResultPreview, "Line 2") {
		t.Errorf("ResultPreview must NOT include second line; got %q", finish.ResultPreview)
	}
	// Finish.Turns 应等于 provider 走的轮数 (2)
	if finish.Turns != 2 {
		t.Errorf("Finish.Turns = %d, want 2", finish.Turns)
	}
}

// 即使调用方传入了 EventSink，探针也必须并行转发，不阻塞 Engine。
func TestAgentService_ProgressProbe_ForwardsToCallerSink(t *testing.T) {
	svc := NewAgentService(slog.Default())
	svc.Registry().Register(&agent.Definition{
		AgentType: "Forward", WhenToUse: "f", SystemPrompt: "f", Source: agent.SourceBuiltIn,
	})
	parentReg := tool.NewRegistry()
	prov := &scriptedProvider{turns: []scriptTurn{{text: "ok"}}}
	factory := NewDefaultAgentEngineFactory(parentReg, prov, query.NewTokenBudget(100000, 0.8), slog.Default())

	sink := make(chan query.StreamEvent, 32)
	if _, err := svc.Run(context.Background(), "Forward", factory, RunOptions{
		Prompt: "go", DefaultModel: "t", EventSink: sink,
	}); err != nil {
		t.Fatalf("run: %v", err)
	}
	close(sink)
	// caller 应该收到 Engine 发出的事件（至少有 MessageDelta）
	gotDelta := false
	for ev := range sink {
		if ev.Type == query.EventMessageDelta {
			gotDelta = true
		}
	}
	if !gotDelta {
		t.Fatal("caller sink should have received MessageDelta from forwarded events")
	}
}

// 错误路径：Engine 失败时 Finish 必须为 Error 且 ErrorMessage 非空。
func TestAgentService_SubagentEvent_ErrorPath(t *testing.T) {
	svc := NewAgentService(slog.Default())
	svc.Registry().Register(&agent.Definition{
		AgentType: "Boom", WhenToUse: "x", SystemPrompt: "x", Source: agent.SourceBuiltIn,
		MaxTurns: 1, // 强制超 max turns 报错
	})
	listener := &recordingSubagentListener{}
	svc.SetSubagentEventListener(listener)

	// 永远要求继续调用工具，触发 max-turns 错误
	parentReg := tool.NewRegistry()
	_ = parentReg.Register(&recordTool{name: "rd", readonly: true, concurrent: true})
	prov := &scriptedProvider{
		turns: []scriptTurn{
			{toolName: "rd", toolID: "c1", toolInput: map[string]interface{}{}},
			{toolName: "rd", toolID: "c2", toolInput: map[string]interface{}{}},
		},
	}
	factory := NewDefaultAgentEngineFactory(parentReg, prov, query.NewTokenBudget(100000, 0.8), slog.Default())

	_, err := svc.Run(context.Background(), "Boom", factory, RunOptions{
		Prompt: "go", DefaultModel: "t",
	})
	if err == nil {
		t.Fatal("expected error from exceeded max turns")
	}
	events := listener.snapshot()
	last := events[len(events)-1]
	if last.Phase != SubagentPhaseFinish || last.Status != SubagentStatusError {
		t.Fatalf("last event must be Finish/Error; got phase=%v status=%v", last.Phase, last.Status)
	}
	if last.ErrorMessage == "" {
		t.Fatal("Finish.ErrorMessage must be populated on error")
	}
	if last.Elapsed <= 0 {
		t.Errorf("Finish.Elapsed must be >0 even on error; got %v", last.Elapsed)
	}
}

// ---- 3) previewFirstLine ---------------------------------------------------------------

func TestPreviewFirstLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"hello", "hello"},
		{"\n\n  trimmed  \n", "trimmed"},
		{"first\nsecond\nthird", "first"},
		{strings.Repeat("a", 100), strings.Repeat("a", 79) + "…"},
	}
	for _, c := range cases {
		got := previewFirstLine(c.in, 80)
		if got != c.want {
			t.Errorf("previewFirstLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ---- 4) 并发：多个 subagent 同时跑互不串扰 -----------------------------------------------

func TestAgentService_ParallelRuns_NoListenerInterleaving(t *testing.T) {
	svc := NewAgentService(slog.Default())
	svc.Registry().Register(&agent.Definition{
		AgentType: "Par", WhenToUse: "p", SystemPrompt: "p", Source: agent.SourceBuiltIn,
	})

	listener := &recordingSubagentListener{}
	svc.SetSubagentEventListener(listener)

	// 每个 subagent 独立 provider + factory；同时启动 N 个，验证 Listener 收到正确数量事件
	const N = 8
	var wg sync.WaitGroup
	var fails atomic.Int32
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			prov := &scriptedProvider{turns: []scriptTurn{{text: "ok"}}}
			f := NewDefaultAgentEngineFactory(tool.NewRegistry(), prov, query.NewTokenBudget(100000, 0.8), slog.Default())
			if _, err := svc.Run(context.Background(), "Par", f, RunOptions{
				Prompt: "go", DefaultModel: "t",
			}); err != nil {
				fails.Add(1)
			}
		}()
	}
	wg.Wait()
	if fails.Load() > 0 {
		t.Fatalf("parallel subagent runs had %d failures", fails.Load())
	}
	// 每个 subagent 至少：Start + Finish；可能还有 Progress（1 轮 stop → 1 progress）。
	// 因此事件总数 >= 2*N
	events := listener.snapshot()
	if len(events) < 2*N {
		t.Fatalf("expected >= %d events for %d parallel runs, got %d", 2*N, N, len(events))
	}
	// 每个 AgentID 至少出现 Start+Finish 一次
	byID := map[string][]SubagentPhase{}
	for _, ev := range events {
		byID[ev.AgentID] = append(byID[ev.AgentID], ev.Phase)
	}
	if len(byID) != N {
		t.Fatalf("expected %d distinct agent IDs, got %d", N, len(byID))
	}
	for id, phases := range byID {
		if phases[0] != SubagentPhaseStart {
			t.Errorf("agent %s: first phase = %v, want Start", id, phases[0])
		}
		if phases[len(phases)-1] != SubagentPhaseFinish {
			t.Errorf("agent %s: last phase = %v, want Finish", id, phases[len(phases)-1])
		}
	}
}

// 加个 200ms 超时保险，防止探针 deadlock 让测试挂死
func TestAgentService_SubagentEvent_FinishWithinTimeout(t *testing.T) {
	svc := NewAgentService(slog.Default())
	svc.Registry().Register(&agent.Definition{
		AgentType: "Quick", WhenToUse: "q", SystemPrompt: "q", Source: agent.SourceBuiltIn,
	})
	listener := &recordingSubagentListener{}
	svc.SetSubagentEventListener(listener)

	prov := &scriptedProvider{turns: []scriptTurn{{text: "x"}}}
	factory := NewDefaultAgentEngineFactory(tool.NewRegistry(), prov, query.NewTokenBudget(100000, 0.8), slog.Default())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = svc.Run(context.Background(), "Quick", factory, RunOptions{
			Prompt: "g", DefaultModel: "t",
		})
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not finish within 2s, probable deadlock in progress probe")
	}
	if listener.phases()[len(listener.phases())-1] != SubagentPhaseFinish {
		t.Fatal("last phase must be Finish")
	}
}

// ---- helpers ---------------------------------------------------------------------------

func toolNames(ts []tool.Tool) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Name())
	}
	return out
}

func contains(xs []string, target string) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}
