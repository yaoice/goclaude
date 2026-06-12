// Package application 实现应用层服务编排
package application

import (
	"context"
	"log/slog"

	"github.com/yaoice/goclaude/pkg/domain/query"
	"github.com/yaoice/goclaude/pkg/domain/tool"
)

// QueryService 查询引擎应用服务
// 负责组装查询引擎依赖，管理会话级查询
type QueryService struct {
	engine   *query.Engine
	provider query.AIProvider
	registry *tool.Registry
	logger   *slog.Logger
}

// NewQueryService 创建查询服务
func NewQueryService(
	provider query.AIProvider,
	registry *tool.Registry,
	config *query.Config,
	logger *slog.Logger,
) *QueryService {
	// 创建工具执行器
	executor := tool.NewExecutor(registry, 10, logger)

	// 创建token预算
	budget := query.NewTokenBudget(200000, 0.8)

	// 创建查询引擎
	engine := query.NewEngine(provider, executor, budget, nil, config, logger)

	return &QueryService{
		engine:   engine,
		provider: provider,
		registry: registry,
		logger:   logger,
	}
}

// Execute 执行一次完整查询
func (s *QueryService) Execute(ctx context.Context, messages []query.Message, events chan<- query.StreamEvent) (*query.QueryResult, error) {
	return s.engine.Execute(ctx, messages, events)
}

// IsRunning 引擎是否运行中
func (s *QueryService) IsRunning() bool {
	return s.engine.IsRunning()
}
