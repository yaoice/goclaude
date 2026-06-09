package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/anthropics/goclaude/pkg/domain/agent"
	"github.com/anthropics/goclaude/pkg/domain/query"
	"github.com/anthropics/goclaude/pkg/domain/tool"
	teamfs "github.com/anthropics/goclaude/pkg/infrastructure/team"
)

// newTestTeamService 使用 tempDir 创建隔离的 TeamService。
func newTestTeamService() *TeamService {
	return NewTeamServiceWithLayout(teamfs.Layout{HomeDir: os.TempDir() + "/goclaude-test-" + shortID()})
}

// newTestAgentService 创建加载了内置 agents 的 AgentService。
func newTestAgentService() *AgentService {
	svc := NewAgentService(slog.New(slog.DiscardHandler))
	return svc
}

// setupTestTeamEngine 构造一个用于测试的 TeamEngine。
func setupTestTeamEngine(agentSvc *AgentService, teamSvc *TeamService) *TeamEngine {
	// 创建轻量的 Logger
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// 创建一个 no-op factory（不为每个 member 真正构建 Engine，
	// 因为我们主要测试生命周期和通信，而非实际 AI 调用）。
	factory := &testAgentEngineFactory{}

	cfg := TeamEngineConfig{
		DefaultModel:    "claude-haiku",
		PollInterval:    200 * time.Millisecond, // 测试用短轮询间隔
		TaskTimeout:     30 * time.Second,
		ShutdownTimeout: 5 * time.Second,
	}
	return NewTeamEngine(agentSvc, teamSvc, factory, cfg, logger)
}

// testAgentEngineFactory 是一个轻量级 AgentEngineFactory 实现，
// 仅用于测试生命周期和通信路径，不真正调用 AI。
type testAgentEngineFactory struct{}

func (f *testAgentEngineFactory) NewSubagentEngine(def *agent.Definition, opts RunOptions) (*query.Engine, error) {
	// 返回一个最小 Engine，仅用于测试路径。
	// 使用 mockProvider 避免真实 API 调用。
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry, 1, slog.New(slog.DiscardHandler))
	cfg := query.DefaultConfig()
	cfg.Model = "claude-haiku"
	cfg.AutoCompact = false
	cfg.SystemPrompt = []query.ContentBlock{
		{Type: query.ContentTypeText, Text: "Test engine"},
	}
	budget := query.NewTokenBudget(200000, 0.8)
	return query.NewEngine(newMockProvider(), executor, budget, nil, cfg, slog.New(slog.DiscardHandler)), nil
}

// mockProvider 是一个最小 query.AIProvider 实现，用于测试。
// Stream() 立即返回错误，让 Engine.Execute() 安全退出。
type mockProvider struct{}

func newMockProvider() *mockProvider { return &mockProvider{} }

func (m *mockProvider) Stream(ctx context.Context, params *query.StreamParams) (<-chan query.StreamEvent, error) {
	return nil, fmt.Errorf("mock provider: no AI calls in test")
}

func (m *mockProvider) Send(ctx context.Context, params *query.SendParams) (*query.Message, *query.Usage, error) {
	return nil, nil, fmt.Errorf("mock provider: no AI calls in test")
}

// shortID 生成简短 ID 用于创建唯一的 test team 名。
func shortID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}
