package application

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/anthropics/goclaude/internal/domain/agent"
	"github.com/anthropics/goclaude/internal/domain/hook"
	"github.com/anthropics/goclaude/internal/domain/query"
	"github.com/anthropics/goclaude/internal/domain/tool"
	"github.com/anthropics/goclaude/internal/infrastructure/memory"
)

// 测试：当 Definition.Memory 不为空且 Memory 服务已注入时，
// subagent 看到的 system prompt 应包含 "## Persistent Memory" 段落。
func TestAgentService_MemoryInjection(t *testing.T) {
	tmp := t.TempDir()
	memSvc := memory.NewService(tmp, tmp)
	if err := memSvc.Write("MyAgent", memory.ScopeUser, "ctx.md", "remember: foo=42"); err != nil {
		t.Fatal(err)
	}

	svc := NewAgentService(slog.Default())
	svc.EnableMemory(memSvc)
	svc.Registry().Register(&agent.Definition{
		AgentType:    "MyAgent",
		WhenToUse:    "test",
		Memory:       string(memory.ScopeUser),
		SystemPrompt: "you are myagent",
		Source:       agent.SourceBuiltIn,
	})

	prov := &scriptedProvider{
		turns: []scriptTurn{{text: "ok"}},
	}
	parentReg := tool.NewRegistry()
	factory := NewDefaultAgentEngineFactory(parentReg, prov, query.NewTokenBudget(100000, 0.8), slog.Default())

	if _, err := svc.Run(context.Background(), "MyAgent", factory, RunOptions{
		Prompt:       "go",
		DefaultModel: "test",
	}); err != nil {
		t.Fatal(err)
	}

	if len(prov.seenSys) == 0 {
		t.Fatal("provider never saw system prompt")
	}
	got := prov.seenSys[0]
	if !strings.Contains(got, "you are myagent") {
		t.Errorf("system prompt missing base: %q", got)
	}
	if !strings.Contains(got, "## Persistent Memory") {
		t.Errorf("system prompt missing memory section: %q", got)
	}
	if !strings.Contains(got, "remember: foo=42") {
		t.Errorf("system prompt missing memory content: %q", got)
	}
}

// 测试：未注入 Memory 服务时，即使 Definition.Memory 设置了也不应报错（静默忽略）
func TestAgentService_MemoryDisabledNoError(t *testing.T) {
	svc := NewAgentService(slog.Default())
	svc.Registry().Register(&agent.Definition{
		AgentType:    "X",
		WhenToUse:    "x",
		Memory:       "user",
		SystemPrompt: "p",
		Source:       agent.SourceBuiltIn,
	})
	prov := &scriptedProvider{turns: []scriptTurn{{text: "ok"}}}
	factory := NewDefaultAgentEngineFactory(tool.NewRegistry(), prov, query.NewTokenBudget(100000, 0.8), slog.Default())
	if _, err := svc.Run(context.Background(), "X", factory, RunOptions{Prompt: "go", DefaultModel: "t"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// 测试：fork 上下文 + 过滤未配对 tool_use
func TestAgentService_ForkContext(t *testing.T) {
	parentReg := tool.NewRegistry()
	svc := NewAgentService(slog.Default())
	svc.Registry().Register(&agent.Definition{
		AgentType:    "F",
		WhenToUse:    "f",
		SystemPrompt: "f",
		Source:       agent.SourceBuiltIn,
	})

	prov := &scriptedProvider{turns: []scriptTurn{{text: "done"}}}
	factory := NewDefaultAgentEngineFactory(parentReg, prov, query.NewTokenBudget(100000, 0.8), slog.Default())

	// 父消息中：完整对（assistant tool_use + user tool_result）+ 一个孤立 tool_use
	parentMsgs := []query.Message{
		query.NewTextMessage(query.RoleUser, "first"),
		{
			Role: query.RoleAssistant,
			Content: []query.ContentBlock{
				{Type: query.ContentTypeToolUse, ToolUseID: "complete-1", ToolName: "x"},
			},
		},
		{
			Role: query.RoleUser,
			Content: []query.ContentBlock{
				{Type: query.ContentTypeToolResult, ToolResultID: "complete-1", Text: "ok"},
			},
		},
		{
			Role: query.RoleAssistant,
			Content: []query.ContentBlock{
				{Type: query.ContentTypeToolUse, ToolUseID: "orphan-2", ToolName: "y"},
			},
		},
	}
	if _, err := svc.Run(context.Background(), "F", factory, RunOptions{
		Prompt:              "do",
		DefaultModel:        "t",
		ForkContextMessages: parentMsgs,
	}); err != nil {
		t.Fatal(err)
	}

	// 验证 FilterIncompleteToolCalls 单测
	filtered := FilterIncompleteToolCalls(parentMsgs)
	// 期望：4 条变 3 条（去掉 orphan-2 那条 assistant 消息）
	if len(filtered) != 3 {
		t.Errorf("expected 3 messages after filter, got %d", len(filtered))
	}
	for _, m := range filtered {
		for _, b := range m.Content {
			if b.Type == query.ContentTypeToolUse && b.ToolUseID == "orphan-2" {
				t.Error("orphan-2 should be filtered out")
			}
		}
	}
}

// 测试：SubagentStart hook 注入的 AdditionalContexts 进入对话消息
func TestAgentService_SubagentStartHook(t *testing.T) {
	svc := NewAgentService(slog.Default())
	reg := hook.NewRegistry(slog.Default())
	reg.Register(hook.EventSubagentStart, func(_ context.Context, hc *hook.Context) (*hook.Result, error) {
		return &hook.Result{
			AdditionalContexts: []string{
				"<system-reminder>Be brief.</system-reminder>",
			},
		}, nil
	})
	var stopCalled int
	reg.Register(hook.EventSubagentStop, func(_ context.Context, hc *hook.Context) (*hook.Result, error) {
		stopCalled++
		if hc.AgentType != "Hooked" {
			t.Errorf("Stop hook agent type = %q", hc.AgentType)
		}
		return nil, nil
	})
	svc.EnableHooks(reg)

	svc.Registry().Register(&agent.Definition{
		AgentType:    "Hooked",
		WhenToUse:    "h",
		SystemPrompt: "you are hooked",
		Source:       agent.SourceBuiltIn,
	})

	prov := &scriptedProvider{turns: []scriptTurn{{text: "ok"}}}
	factory := NewDefaultAgentEngineFactory(tool.NewRegistry(), prov, query.NewTokenBudget(100000, 0.8), slog.Default())

	if _, err := svc.Run(context.Background(), "Hooked", factory, RunOptions{
		Prompt:       "go",
		DefaultModel: "t",
	}); err != nil {
		t.Fatal(err)
	}

	// 验证 SubagentStart 注入的 additional context 出现在 provider 看到的消息中
	if len(prov.seenMessagesAtTurn) == 0 {
		t.Fatal("provider never saw any messages")
	}
	first := prov.seenMessagesAtTurn[0]
	foundReminder := false
	for _, m := range first {
		if strings.Contains(m.GetTextContent(), "Be brief") {
			foundReminder = true
			break
		}
	}
	if !foundReminder {
		t.Errorf("SubagentStart hook 注入的 context 未出现在第一轮 messages: %+v", first)
	}

	// SubagentStop 应被触发一次
	if stopCalled != 1 {
		t.Errorf("SubagentStop hook called %d times, want 1", stopCalled)
	}
}
