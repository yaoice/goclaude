// Package application 提供 Plan Agent — AI 驱动的 workflow 定义生成器。
//
// PlanAgentService 桥接 AgentService（LLM 调用）和 Loader（文件保存）：
//   1. 根据用户请求构建结构化 prompt
//   2. 通过 AgentService.Run 调用 LLM 生成 JSON output
//   3. 解析 → 验证 → 保存为 JSON 文件
//   4. 返回完整的 workflow.Workflow 供后续执行
package application

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/anthropics/goclaude/pkg/domain/agent"
	"github.com/anthropics/goclaude/pkg/domain/workflow"
	workflowinfra "github.com/anthropics/goclaude/pkg/infrastructure/workflow"
)

// PlanAgentService AI 驱动的 workflow 定义生成器。
//
// 对齐 oh-my-openagent Sisyphus orchestrator 的 Plan Agent：
//   - 用户自然语言描述 → Plan Agent prompt → LLM JSON 输出 → workflow.Workflow
//   - 输出 JSON（非 YAML），因 JSON 对 LLM 更友好（严格语法，无缩进歧义）
type PlanAgentService struct {
	agentSvc *AgentService
	factory  AgentEngineFactory
	loader   *workflowinfra.Loader
	logger   *slog.Logger
}

// PlanAgentDefaults Plan Agent 执行默认参数
type PlanAgentDefaults struct {
	ParentSessionID string
	WorkingDir      string
	ProjectRoot     string
	DefaultModel    string
	// WorkspaceRoot 任务产物统一输出目录
	WorkspaceRoot string
}

// PlanAgentResult Plan Agent 生成结果
type PlanAgentResult struct {
	Workflow   *workflow.Workflow
	RawJSON    string
	SavedPath  string
	AutoSaved  bool
}

// NewPlanAgentService 创建 Plan Agent 服务。
func NewPlanAgentService(
	agentSvc *AgentService,
	factory AgentEngineFactory,
	loader *workflowinfra.Loader,
	logger *slog.Logger,
) *PlanAgentService {
	if logger == nil {
		logger = slog.Default()
	}
	return &PlanAgentService{
		agentSvc: agentSvc,
		factory:  factory,
		loader:   loader,
		logger:   logger,
	}
}

// PlanFromRequest 从用户请求生成 workflow 定义。
//
// 执行流程：
//   1. 构建 Plan Agent system prompt（含可用 agent 类型列表）
//   2. 注册临时 "WorkflowPlanner" agent（system prompt = PlanAgentPrompt）
//   3. 调用 AgentService.Run("WorkflowPlanner", ...) 触发 LLM
//   4. 解析 LLM 输出的 JSON → workflow.Workflow
//   5. 验证 workflow 合法性
//   6. 可选保存到项目 workflows 目录
//
// autoSave: 为 true 时自动保存 JSON 文件到项目目录
func (s *PlanAgentService) PlanFromRequest(
	ctx context.Context,
	userRequest string,
	defaults PlanAgentDefaults,
	autoSave bool,
) (*PlanAgentResult, error) {
	// 1. 收集可用的 agent 类型
	availableTypes := s.collectAgentTypes()

	// 2. 构建 prompt
	prompt := PlanAgentPrompt(availableTypes, userRequest)

	// 3. 注册临时 Plan Agent（如果还不存在）
	const planAgentType = "WorkflowPlanner"
	if _, exists := s.agentSvc.registry.Get(planAgentType); !exists {
		s.agentSvc.registry.Register(&agent.Definition{
			AgentType:    planAgentType,
			WhenToUse:    "将用户请求分析为结构化的 workflow 执行计划，输出 JSON 格式的 workflow 定义",
			SystemPrompt: "", // prompt 通过 RunOptions.Prompt 传入
			Source:       agent.SourceBuiltIn,
			// Plan Agent 不需要工具，纯文本生成即可
			Tools: []string{},
		})
	}

	// 4. 调用 LLM
	result, err := s.agentSvc.Run(ctx, planAgentType, s.factory, RunOptions{
		Prompt:          prompt,
		ParentSessionID: defaults.ParentSessionID,
		WorkingDir:      defaults.WorkingDir,
		ProjectRoot:     defaults.ProjectRoot,
		DefaultModel:    defaults.DefaultModel,
		WorkspaceRoot:   defaults.WorkspaceRoot,
	})
	if err != nil {
		return nil, fmt.Errorf("Plan Agent execution failed: %w", err)
	}

	// 5. 解析 JSON 输出
	rawJSON := result.FinalText
	cleanJSON, err := ParsePlanAgentOutput(rawJSON)
	if err != nil {
		s.logger.Warn("Plan Agent output parse failed", "raw", truncate(rawJSON, 500), "error", err)
		return &PlanAgentResult{
			RawJSON: rawJSON,
		}, fmt.Errorf("parse Plan Agent output: %w", err)
	}

	// 6. Unmarshal → workflow.Workflow
	var wf workflow.Workflow
	if err := json.Unmarshal(cleanJSON, &wf); err != nil {
		s.logger.Warn("Plan Agent JSON unmarshal failed", "json", truncate(string(cleanJSON), 500), "error", err)
		return &PlanAgentResult{
			RawJSON: string(cleanJSON),
		}, fmt.Errorf("unmarshal workflow JSON: %w", err)
	}

	// 7. 验证
	if err := wf.Validate(); err != nil {
		s.logger.Warn("Plan Agent generated invalid workflow",
			"name", wf.Name,
			"nodes", len(wf.Nodes),
			"error", err)
		return &PlanAgentResult{
			Workflow: &wf,
			RawJSON:  string(cleanJSON),
		}, fmt.Errorf("validate generated workflow: %w", err)
	}

	pr := &PlanAgentResult{
		Workflow: &wf,
		RawJSON:  string(cleanJSON),
	}

	// 8. 可选保存
	if autoSave && wf.Name != "" {
		path, err := s.loader.Save(defaults.ProjectRoot, &wf, "json")
		if err != nil {
			s.logger.Warn("failed to save workflow", "name", wf.Name, "error", err)
		} else {
			pr.SavedPath = path
			pr.AutoSaved = true
			s.logger.Info("auto-saved workflow", "name", wf.Name, "path", path, "nodes", len(wf.Nodes))
		}
	}

	return pr, nil
}

// PlanAndSave 从请求生成并保存 workflow 定义（便捷方法）。
func (s *PlanAgentService) PlanAndSave(
	ctx context.Context,
	userRequest string,
	defaults PlanAgentDefaults,
) (*PlanAgentResult, error) {
	return s.PlanFromRequest(ctx, userRequest, defaults, true)
}

// collectAgentTypes 收集当前 AgentService 中所有已注册的 agent 类型
func (s *PlanAgentService) collectAgentTypes() []string {
	defs := s.agentSvc.registry.All()
	types := make([]string, 0, len(defs))
	for _, d := range defs {
		if d.AgentType != "WorkflowPlanner" { // 避免自引用
			types = append(types, d.AgentType)
		}
	}
	return types
}
