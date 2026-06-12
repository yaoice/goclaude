package application

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/yaoice/goclaude/pkg/domain/agent"
	"github.com/yaoice/goclaude/pkg/domain/query"
	"github.com/yaoice/goclaude/pkg/domain/tool"
)

// ---- mock 工具：可观测被调用次数 ----------------------------------------------------

type recordTool struct {
	name       string
	readonly   bool
	concurrent bool
	calls      atomic.Int32
}

func (t *recordTool) Name() string                        { return t.name }
func (t *recordTool) Aliases() []string                   { return nil }
func (t *recordTool) Description() string                 { return t.name + " mock tool" }
func (t *recordTool) IsEnabled() bool                     { return true }
func (t *recordTool) IsReadOnly(_ tool.Input) bool        { return t.readonly }
func (t *recordTool) IsConcurrencySafe(_ tool.Input) bool { return t.concurrent }
func (t *recordTool) Prompt() string                      { return "" }
func (t *recordTool) ValidateInput(_ tool.Input) error    { return nil }
func (t *recordTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (t *recordTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}
func (t *recordTool) Call(_ context.Context, _ tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	t.calls.Add(1)
	return tool.NewResult("ok from " + t.name), nil
}

// ---- mock provider：按预设脚本回复 -------------------------------------------------

type scriptedProvider struct {
	mu                 sync.Mutex
	turns              []scriptTurn
	cursor             int
	seenSys            []string
	seenTools          [][]string
	seenMessagesAtTurn [][]query.Message
}

type scriptTurn struct {
	text      string
	toolName  string
	toolID    string
	toolInput map[string]interface{}
}

func (p *scriptedProvider) Stream(_ context.Context, params *query.StreamParams) (<-chan query.StreamEvent, error) {
	p.mu.Lock()
	if p.cursor >= len(p.turns) {
		p.mu.Unlock()
		return nil, errors.New("script exhausted")
	}
	turn := p.turns[p.cursor]
	p.cursor++
	// 记录每轮的系统提示词与工具列表，便于断言
	if len(params.System) > 0 {
		p.seenSys = append(p.seenSys, params.System[0].Text)
	}
	names := make([]string, 0, len(params.Tools))
	for _, t := range params.Tools {
		names = append(names, t.Name)
	}
	p.seenTools = append(p.seenTools, names)
	// 拷贝消息列表（防止 caller 在我们读完前 mutate 切片）
	msgsCopy := make([]query.Message, len(params.Messages))
	copy(msgsCopy, params.Messages)
	p.seenMessagesAtTurn = append(p.seenMessagesAtTurn, msgsCopy)
	p.mu.Unlock()

	ch := make(chan query.StreamEvent, 8)
	go func() {
		defer close(ch)

		// 构造单条 assistant message：要么纯文本要么 tool_use
		if turn.toolName != "" {
			ch <- query.StreamEvent{
				Type:  query.EventContentBlockStart,
				Index: 0,
				ContentBlock: &query.ContentBlock{
					Type:      query.ContentTypeToolUse,
					ToolUseID: turn.toolID,
					ToolName:  turn.toolName,
					Input:     turn.toolInput,
				},
			}
			ch <- query.StreamEvent{Type: query.EventContentBlockStop, Index: 0}
			ch <- query.StreamEvent{
				Type:       query.EventMessageDelta,
				StopReason: query.StopReasonToolUse,
				Usage:      &query.Usage{InputTokens: 10, OutputTokens: 5},
			}
		} else {
			ch <- query.StreamEvent{
				Type:  query.EventContentBlockStart,
				Index: 0,
				ContentBlock: &query.ContentBlock{
					Type: query.ContentTypeText,
				},
			}
			ch <- query.StreamEvent{
				Type:  query.EventContentBlockDelta,
				Index: 0,
				Delta: &query.DeltaContent{Type: query.ContentTypeText, Text: turn.text},
			}
			ch <- query.StreamEvent{Type: query.EventContentBlockStop, Index: 0}
			ch <- query.StreamEvent{
				Type:       query.EventMessageDelta,
				StopReason: query.StopReasonEndTurn,
				Usage:      &query.Usage{InputTokens: 10, OutputTokens: 5},
			}
		}
	}()
	return ch, nil
}

func (p *scriptedProvider) Send(_ context.Context, _ *query.SendParams) (*query.Message, *query.Usage, error) {
	return nil, nil, errors.New("not implemented")
}

// ---- 端到端：AgentTool → subagent.Run → 工具过滤 ------------------------------------

func TestAgentTool_EndToEnd_ToolFiltering(t *testing.T) {
	// 父 registry：有 read/write 两个工具
	parentReg := tool.NewRegistry()
	readTool := &recordTool{name: "read_file", readonly: true, concurrent: true}
	writeTool := &recordTool{name: "write_file"}
	_ = parentReg.Register(readTool)
	_ = parentReg.Register(writeTool)

	// 注册一个只允许 read_file 的 subagent
	svc := NewAgentService(slog.Default())
	svc.Registry().Register(&agent.Definition{
		AgentType:    "Searcher",
		WhenToUse:    "test searcher",
		Tools:        []string{"read_file"}, // 仅白名单 read_file
		SystemPrompt: "you are a searcher",
		Source:       agent.SourceBuiltIn,
	})

	// 脚本：subagent 第一轮调用 read_file；第二轮直接 end_turn 返回文本
	prov := &scriptedProvider{
		turns: []scriptTurn{
			{
				toolName:  "read_file",
				toolID:    "call-1",
				toolInput: map[string]interface{}{"path": "/x"},
			},
			{text: "done"},
		},
	}
	budget := query.NewTokenBudget(100000, 0.8)
	factory := NewDefaultAgentEngineFactory(parentReg, prov, budget, slog.Default())

	result, err := svc.Run(context.Background(), "Searcher", factory, RunOptions{
		Prompt:       "find foo",
		DefaultModel: "test-model",
	})
	if err != nil {
		t.Fatalf("subagent run error: %v", err)
	}
	if result.FinalText != "done" {
		t.Errorf("final text = %q", result.FinalText)
	}
	if result.TurnCount != 2 {
		t.Errorf("turn count = %d, want 2", result.TurnCount)
	}

	// 验证只读工具被实际调用
	if readTool.calls.Load() != 1 {
		t.Errorf("read_file should be called once, got %d", readTool.calls.Load())
	}
	if writeTool.calls.Load() != 0 {
		t.Errorf("write_file should NOT be called (filtered out), got %d", writeTool.calls.Load())
	}

	// 验证 Provider 看到的工具列表里没有 write_file（说明过滤生效）
	if len(prov.seenTools) == 0 {
		t.Fatal("provider never saw tools")
	}
	for i, toolList := range prov.seenTools {
		for _, n := range toolList {
			if n == "write_file" {
				t.Errorf("turn %d: write_file leaked into subagent tool list: %v", i, toolList)
			}
		}
	}

	// 验证 system prompt = subagent 定义
	if len(prov.seenSys) == 0 || prov.seenSys[0] != "you are a searcher" {
		t.Errorf("system prompt mismatch: %v", prov.seenSys)
	}
}

func TestAgentTool_DisallowedToolsRespected(t *testing.T) {
	parentReg := tool.NewRegistry()
	_ = parentReg.Register(&recordTool{name: "read_file", readonly: true})
	bashTool := &recordTool{name: "bash"}
	_ = parentReg.Register(bashTool)

	svc := NewAgentService(slog.Default())
	svc.Registry().Register(&agent.Definition{
		AgentType:       "ReadOnly",
		WhenToUse:       "test",
		DisallowedTools: []string{"bash"},
		SystemPrompt:    "no shell",
		Source:          agent.SourceBuiltIn,
	})

	// 脚本：先尝试调用 bash（应得到 not found）；再 end_turn
	prov := &scriptedProvider{
		turns: []scriptTurn{
			{toolName: "bash", toolID: "c1", toolInput: map[string]interface{}{"cmd": "ls"}},
			{text: "blocked"},
		},
	}
	budget := query.NewTokenBudget(100000, 0.8)
	factory := NewDefaultAgentEngineFactory(parentReg, prov, budget, slog.Default())

	_, err := svc.Run(context.Background(), "ReadOnly", factory, RunOptions{
		Prompt: "run ls", DefaultModel: "test",
	})
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if bashTool.calls.Load() != 0 {
		t.Errorf("bash should NOT have been invoked (disallowed), got %d calls", bashTool.calls.Load())
	}
}

func TestFilterTools_AllRules(t *testing.T) {
	read := &recordTool{name: "read", readonly: true}
	write := &recordTool{name: "write"}
	bash := &recordTool{name: "bash"}
	all := []tool.Tool{read, write, bash}

	cases := []struct {
		name string
		def  *agent.Definition
		want []string
	}{
		{
			name: "no rules → all",
			def:  &agent.Definition{},
			want: []string{"read", "write", "bash"},
		},
		{
			name: "whitelist only",
			def:  &agent.Definition{Tools: []string{"read"}},
			want: []string{"read"},
		},
		{
			name: "denylist subtracts",
			def:  &agent.Definition{DisallowedTools: []string{"bash"}},
			want: []string{"read", "write"},
		},
		{
			name: "whitelist + denylist combine",
			def:  &agent.Definition{Tools: []string{"read", "bash"}, DisallowedTools: []string{"bash"}},
			want: []string{"read"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FilterTools(all, tc.def)
			names := make([]string, len(got))
			for i, t := range got {
				names[i] = t.Name()
			}
			if !equalNameSet(names, tc.want) {
				t.Errorf("got %v, want %v", names, tc.want)
			}
		})
	}
}

func equalNameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]bool, len(a))
	for _, x := range a {
		m[x] = true
	}
	for _, x := range b {
		if !m[x] {
			return false
		}
	}
	return true
}

// 防止 json 包被剔除（被测试中潜在的输入序列化用到）
var _ = json.Marshal
