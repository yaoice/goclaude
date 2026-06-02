package application

import (
	"log/slog"

	"github.com/anthropics/goclaude/internal/domain/agent"
	"github.com/anthropics/goclaude/internal/domain/query"
	"github.com/anthropics/goclaude/internal/domain/tool"
)

// DefaultAgentEngineFactory 是 AgentEngineFactory 的默认实现
//
// 为每个 subagent 启动构造一个独立 query.Engine：
//   - 工具集：从父 registry 过滤后构建子 registry + 子 executor
//   - 系统提示词：subagent.Definition.ResolvedSystemPrompt
//   - 模型：按 ResolveModel 解析（inherit/override/literal）
//   - token budget：复用父 budget（保守策略，让 subagent 也参与总额控制）
//
// 对齐 src/tools/AgentTool/runAgent.ts 中 createSubagentContext + query() 的核心逻辑。
type DefaultAgentEngineFactory struct {
	// ParentRegistry 父 agent 的工具注册表
	ParentRegistry *tool.Registry
	// Provider AI 服务提供商
	Provider query.AIProvider
	// Budget 共享 token 预算
	Budget *query.TokenBudget
	// Compactor 共享压缩器
	Compactor query.Compactor
	// Logger 日志
	Logger *slog.Logger
	// PermContext subagent 共享的权限上下文（含工作目录等）
	PermContext *tool.PermissionContext
	// MaxConcurrency Executor 并发上限
	MaxConcurrency int
}

// NewDefaultAgentEngineFactory 构造一个默认 Factory
func NewDefaultAgentEngineFactory(
	parentRegistry *tool.Registry,
	provider query.AIProvider,
	budget *query.TokenBudget,
	logger *slog.Logger,
) *DefaultAgentEngineFactory {
	if logger == nil {
		logger = slog.Default()
	}
	return &DefaultAgentEngineFactory{
		ParentRegistry: parentRegistry,
		Provider:       provider,
		Budget:         budget,
		Logger:         logger,
		MaxConcurrency: 10,
	}
}

// NewSubagentEngine 构造一个适配 subagent 的 Engine
func (f *DefaultAgentEngineFactory) NewSubagentEngine(def *agent.Definition, opts RunOptions) (*query.Engine, error) {
	if f.ParentRegistry == nil {
		// 没有父 registry → subagent 也没工具可用
		f.ParentRegistry = tool.NewRegistry()
	}

	// 1. 按 Definition 过滤工具，构造子 registry
	parentTools := f.ParentRegistry.GetAll()
	allowed := FilterTools(parentTools, def)

	subRegistry := tool.NewRegistry()
	for _, t := range allowed {
		_ = subRegistry.Register(t)
	}

	// 2. 构造 executor；复用父 PermContext（subagent 默认共享权限策略）
	executor := tool.NewExecutor(subRegistry, f.MaxConcurrency, f.Logger)
	if f.PermContext != nil {
		permCopy := *f.PermContext
		// PermissionMode 可被 Definition 覆盖（除非父在 bypass）
		if def.PermissionMode != "" && permCopy.Mode != tool.PermissionModeBypass {
			permCopy.Mode = tool.PermissionMode(def.PermissionMode)
		}
		executor.SetPermissionContext(&permCopy)
	}

	// 3. 引擎配置：system prompt + 模型 + maxTurns
	cfg := query.DefaultConfig()
	cfg.Model = ResolveModel(def, opts)
	cfg.SystemPrompt = []query.ContentBlock{
		{Type: query.ContentTypeText, Text: def.ResolvedSystemPrompt()},
	}
	switch {
	case opts.MaxTurns > 0:
		cfg.MaxTurns = opts.MaxTurns
	case def.MaxTurns > 0:
		cfg.MaxTurns = def.MaxTurns
	}
	// subagent 默认禁用 thinking（对齐 src：节省成本），通过更低 temperature 间接体现
	// 这里不强行改 temperature，等用户显式配置。
	// AutoCompact：subagent 默认开启（继承 Compactor），但只有当 Factory.Compactor 非 nil 时才生效。
	// 调用方未注入 Compactor 时，AutoCompact 标志即使为 true 也不会触发（Engine 内部判 nil）。
	cfg.AutoCompact = f.Compactor != nil

	engine := query.NewEngine(f.Provider, executor, f.Budget, f.Compactor, cfg, f.Logger)
	return engine, nil
}
