package cli

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/yaoice/goclaude/pkg/application"
	wf "github.com/yaoice/goclaude/pkg/domain/workflow"
	workflowinfra "github.com/yaoice/goclaude/pkg/infrastructure/workflow"
	"github.com/yaoice/goclaude/pkg/interfaces/shell"
)

// workflowAdapter 实现 shell.WorkflowManager 和 wf.WorkflowEventListener，
// 桥接 CLI 层到 application.WorkflowService 和 PlanAgentService。
type workflowAdapter struct {
	svc     *application.WorkflowService
	planSvc *application.PlanAgentService
	loader  *workflowinfra.Loader
	project string

	// workspaceRootFn 动态获取 workspace 根目录（支持 /workspace 切换）
	workspaceRootFn func() string

	// 执行所需依赖
	agentSvc *application.AgentService
	factory  application.AgentEngineFactory
	defaults application.WorkflowDefaults

	mu      sync.Mutex
	runners map[string]*workflowRunner // name → running instance
}

type workflowRunner struct {
	cancel context.CancelFunc
	state  *workflowRunnerState
}

type workflowRunnerState struct {
	Name        string
	Status      string
	CurrentWave int
	TotalWaves  int
	Progress    float64
	Nodes       map[string]*shell.WorkflowNodeView
}

// newWorkflowAdapter 创建适配器并注册事件监听。
//
// workspaceRootFn: 动态获取 workspace 根目录（支持 /workspace 切换）
func newWorkflowAdapter(
	svc *application.WorkflowService,
	planSvc *application.PlanAgentService,
	loader *workflowinfra.Loader,
	agentSvc *application.AgentService,
	factory application.AgentEngineFactory,
	defaults application.WorkflowDefaults,
	projectDir string,
	workspaceRootFn func() string,
) *workflowAdapter {
	a := &workflowAdapter{
		svc:             svc,
		planSvc:         planSvc,
		loader:          loader,
		project:         projectDir,
		workspaceRootFn: workspaceRootFn,
		agentSvc:        agentSvc,
		factory:         factory,
		defaults:        defaults,
		runners:         make(map[string]*workflowRunner),
	}
	svc.SetEventListener(a)
	return a
}

// --- wf.WorkflowEventListener ---

func (a *workflowAdapter) HandleWorkflowEvent(ev wf.WorkflowEvent) {
	a.mu.Lock()
	runner, ok := a.runners[ev.WorkflowName]
	a.mu.Unlock()

	if !ok {
		return
	}

	s := runner.state

	switch ev.Phase {
	case wf.WorkflowEventPhaseStart:
		s.Status = "running"
		s.TotalWaves = ev.TotalWaves
	case wf.WorkflowEventPhaseWaveStart:
		s.CurrentWave = ev.WaveIndex
	case wf.WorkflowEventPhaseWaveEnd:
		s.Progress = ev.Progress
	case wf.WorkflowEventPhaseNodeStart:
		if s.Nodes == nil {
			s.Nodes = make(map[string]*shell.WorkflowNodeView)
		}
		s.Nodes[ev.NodeID] = &shell.WorkflowNodeView{
			NodeID: ev.NodeID,
			Status: "running",
		}
	case wf.WorkflowEventPhaseNodeEnd:
		if n, ok := s.Nodes[ev.NodeID]; ok {
			n.Status = string(ev.NodeStatus)
			n.Output = ev.NodeOutput
			n.Error = ev.NodeError
		}
		s.Progress = ev.Progress
	case wf.WorkflowEventPhaseEnd:
		s.Status = string(ev.WorkflowStatus)
		s.Progress = ev.Progress
	}
}

// --- shell.WorkflowManager ---

func (a *workflowAdapter) List() []shell.WorkflowInfo {
	wfs, err := a.loader.Load(a.project)
	if err != nil {
		return nil
	}
	result := make([]shell.WorkflowInfo, 0, len(wfs))
	for _, w := range wfs {
		result = append(result, shell.WorkflowInfo{
			Name:        w.Name,
			Description: w.Description,
			NodeCount:   len(w.Nodes),
		})
	}
	return result
}

func (a *workflowAdapter) Get(name string) (shell.WorkflowInfo, bool) {
	w, err := a.loader.LoadByName(name, a.project)
	if err != nil {
		return shell.WorkflowInfo{}, false
	}
	return shell.WorkflowInfo{
		Name:        w.Name,
		Description: w.Description,
		NodeCount:   len(w.Nodes),
	}, true
}

// Plan 通过 Plan Agent 分析用户请求，生成 workflow 定义（不立即执行）。
// 对齐 oh-my-openagent 的 Plan Agent → 输出 plan（不自动执行）。
func (a *workflowAdapter) Plan(ctx context.Context, description string) (*shell.WorkflowGenerateResult, error) {
	planDefaults := application.PlanAgentDefaults{
		ParentSessionID: a.defaults.ParentSessionID,
		WorkingDir:      a.defaults.WorkingDir,
		ProjectRoot:     a.defaults.ProjectRoot,
		DefaultModel:    a.defaults.DefaultModel,
	}

	pr, err := a.planSvc.PlanAndSave(ctx, description, planDefaults)
	if err != nil {
		// 即使解析失败，如果有 raw JSON 也返回部分结果
		if pr != nil && pr.RawJSON != "" {
			return &shell.WorkflowGenerateResult{
				RawJSON: pr.RawJSON,
			}, err
		}
		return nil, err
	}

	return &shell.WorkflowGenerateResult{
		Name:        pr.Workflow.Name,
		Description: pr.Workflow.Description,
		NodeCount:   len(pr.Workflow.Nodes),
		SavedPath:   pr.SavedPath,
		RawJSON:     pr.RawJSON,
		Workflow: &shell.WorkflowInfo{
			Name:        pr.Workflow.Name,
			Description: pr.Workflow.Description,
			NodeCount:   len(pr.Workflow.Nodes),
		},
	}, nil
}

// RunOrGenerate 执行 workflow。若 definition 文件不存在，通过 Plan Agent
// 自动生成 JSON 定义、保存后执行。这是 goclaude 对齐 oh-my-openagent 的核心入口。
//
// 返回值:
//   - result: 执行结果
//   - generated: true 表示定义是 AI 动态生成的
//   - err: 错误
func (a *workflowAdapter) RunOrGenerate(ctx context.Context, description string) (*shell.WorkflowRunResult, bool, error) {
	// 尝试 1: 按名称加载预定义文件
	w, err := a.loader.LoadByName(description, a.project)
	if err == nil {
		// 预定义文件存在 → 直接执行
		result, execErr := a.executeWorkflow(ctx, w)
		return result, false, execErr
	}

	// 尝试 2: 无预定义文件 → Plan Agent 自动生成
	planDefaults := application.PlanAgentDefaults{
		ParentSessionID: a.defaults.ParentSessionID,
		WorkingDir:      a.defaults.WorkingDir,
		ProjectRoot:     a.defaults.ProjectRoot,
		DefaultModel:    a.defaults.DefaultModel,
	}

	pr, genErr := a.planSvc.PlanAndSave(ctx, description, planDefaults)
	if genErr != nil {
		return nil, false, fmt.Errorf("workflow %q not found and Plan Agent failed: %w", description, genErr)
	}
	if pr.Workflow == nil {
		return nil, false, fmt.Errorf("Plan Agent generated empty workflow for: %s", description)
	}

	// 执行生成的 workflow
	result, execErr := a.executeWorkflow(ctx, pr.Workflow)
	return result, true, execErr
}

// Run 执行 workflow（仅预定义文件；无文件时返回错误）。
// 如需自动生成，请使用 RunOrGenerate。
func (a *workflowAdapter) Run(ctx context.Context, name string) (*shell.WorkflowRunResult, error) {
	w, err := a.loader.LoadByName(name, a.project)
	if err != nil {
		return nil, fmt.Errorf("load workflow %q: %w", name, err)
	}
	return a.executeWorkflow(ctx, w)
}

// executeWorkflow 共享的执行逻辑：ParseAndValidate → Execute
func (a *workflowAdapter) executeWorkflow(ctx context.Context, w *wf.Workflow) (*shell.WorkflowRunResult, error) {
	plan, err := a.svc.ParseAndValidate(w)
	if err != nil {
		return nil, fmt.Errorf("parse workflow %q: %w", w.Name, err)
	}

	// 动态获取 workspace 目录（支持 /workspace 切换）
	defaults := a.defaults
	if a.workspaceRootFn != nil {
		if ws := a.workspaceRootFn(); ws != "" {
			defaults.WorkspaceRoot = ws
		}
	}

	execCtx, cancel := context.WithCancel(ctx)
	runner := &workflowRunner{
		cancel: cancel,
		state: &workflowRunnerState{
			Name:       w.Name,
			Status:     "pending",
			TotalWaves: len(plan.Waves),
			Nodes:      make(map[string]*shell.WorkflowNodeView),
		},
	}
	a.mu.Lock()
	a.runners[w.Name] = runner
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		delete(a.runners, w.Name)
		a.mu.Unlock()
		cancel()
	}()

	executor := application.NewAgentNodeExecutor(a.agentSvc, a.factory, defaults)
	result, _, execErr := a.svc.Execute(execCtx, plan, executor)
	if execErr != nil {
		return &shell.WorkflowRunResult{
			WorkflowName: w.Name,
			Status:       "error",
			TotalNodes:   plan.TotalNodes,
			Elapsed:      "0s",
			Output:       execErr.Error(),
		}, nil
	}

	output := buildSummaryOutput(result)
	return &shell.WorkflowRunResult{
		WorkflowName: result.WorkflowName,
		Status:       string(result.Status),
		TotalNodes:   result.TotalNodes,
		Completed:    result.Completed,
		Failed:       result.Failed,
		Skipped:      result.Skipped,
		Elapsed:      result.Elapsed.Round(time.Millisecond).String(),
		Output:       output,
	}, nil
}

func (a *workflowAdapter) Status(name string) (*shell.WorkflowStatusView, error) {
	a.mu.Lock()
	runner, ok := a.runners[name]
	a.mu.Unlock()

	if !ok {
		return nil, nil
	}

	s := runner.state
	nodes := make([]shell.WorkflowNodeView, 0, len(s.Nodes))
	for _, n := range s.Nodes {
		nodes = append(nodes, *n)
	}

	return &shell.WorkflowStatusView{
		Name:        s.Name,
		Status:      s.Status,
		CurrentWave: s.CurrentWave,
		TotalWaves:  s.TotalWaves,
		Nodes:       nodes,
		Progress:    s.Progress,
	}, nil
}

func (a *workflowAdapter) Cancel(name string) error {
	a.mu.Lock()
	runner, ok := a.runners[name]
	a.mu.Unlock()

	if !ok {
		return fmt.Errorf("workflow %q is not running", name)
	}

	runner.cancel()
	return nil
}

func buildSummaryOutput(result *wf.WorkflowResult) string {
	if result == nil {
		return ""
	}
	// 计算最长的 NodeID，用于对齐输出
	maxLen := 0
	for _, nr := range result.NodeResults {
		if len(nr.NodeID) > maxLen {
			maxLen = len(nr.NodeID)
		}
	}
	var out string
	for _, nr := range result.NodeResults {
		icon := "✔"
		if nr.Status == wf.NodeStatusFailed {
			icon = "✘"
		} else if nr.Status == wf.NodeStatusSkipped {
			icon = "↷"
		}
		elapsed := nr.Elapsed.Round(time.Millisecond).String()
		out += fmt.Sprintf("  %s %-*s  %s  (%s)\n", icon, maxLen, nr.NodeID, nr.Status, elapsed)
	}
	return out
}
