// Package application 实现 workflow 编排引擎。
//
// WorkflowService 是 workflow.Engine 接口的实现，集成 AgentService
// 完成 subagent 编排：依赖图解析 → 拓扑排序 → 波次并行执行。
//
// 对齐 oh-my-openagent 的核心编排逻辑：
//   - Plan Agent 的依赖图 + 并行执行波次 (constants.ts)
//   - task() 工具的 subagent_type/category 路由 (tools.ts)
//   - Sisyphus 的逐波执行策略 (sisyphus.ts)
package application

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/yaoice/goclaude/pkg/domain/workflow"
	"golang.org/x/sync/errgroup"
)

// WorkflowService workflow 编排应用服务。
//
// 实现 workflow.Engine 接口。持有 AgentService 和 AgentEngineFactory
// 的引用，将每个 workflow 节点映射到一次 subagent 调用。
type WorkflowService struct {
	agentSvc *AgentService
	factory  AgentEngineFactory
	logger   *slog.Logger

	// defaults 默认运行时上下文
	defaults WorkflowDefaults

	listenerMu sync.RWMutex
	listener   workflow.WorkflowEventListener
}

// WorkflowDefaults workflow 执行时的默认参数
type WorkflowDefaults struct {
	ParentSessionID string
	WorkingDir      string
	ProjectRoot     string
	DefaultModel    string
	// WorkspaceRoot 任务产物统一输出目录
	WorkspaceRoot string
}

// NewWorkflowService 创建 workflow 服务。
//
// agentSvc 必须已经 LoadAll() 完成（即内置 + 用户自定义 agents 已就绪）。
// factory 用于为每个 workflow 节点构造 subagent Engine。
func NewWorkflowService(agentSvc *AgentService, factory AgentEngineFactory, defaults WorkflowDefaults, logger *slog.Logger) *WorkflowService {
	if logger == nil {
		logger = slog.Default()
	}
	return &WorkflowService{
		agentSvc: agentSvc,
		factory:  factory,
		defaults: defaults,
		logger:   logger,
	}
}

// SetEventListener 注入工作流事件监听器。
func (s *WorkflowService) SetEventListener(l workflow.WorkflowEventListener) {
	s.listenerMu.Lock()
	defer s.listenerMu.Unlock()
	s.listener = l
}

func (s *WorkflowService) publish(ev workflow.WorkflowEvent) {
	s.listenerMu.RLock()
	l := s.listener
	s.listenerMu.RUnlock()
	if l != nil {
		l.HandleWorkflowEvent(ev)
	}
}

// ---------------------------------------------------------------------------
// ParseAndValidate: DAG 构建 + 拓扑排序 + 波次分解
// ---------------------------------------------------------------------------

// ParseAndValidate 解析 workflow 定义，构建拓扑排序执行计划。
//
// 算法流程：
//  1. 验证 workflow 定义（节点 ID 唯一、依赖存在、无自依赖）
//  2. 构建邻接表和入度表
//  3. BFS 拓扑排序 + 循环依赖检测
//  4. 按拓扑层分组为执行波次（waves）
//  5. 计算临界路径
func (s *WorkflowService) ParseAndValidate(wf *workflow.Workflow) (*workflow.ExecutionPlan, error) {
	if err := wf.Validate(); err != nil {
		return nil, err
	}

	// 构建节点 ID 索引
	nodeMap := wf.NodeMap()
	nodeIDs := make([]string, 0, len(wf.Nodes))
	for _, n := range wf.Nodes {
		nodeIDs = append(nodeIDs, n.ID)
	}

	// 构建邻接表 (节点 → 依赖它的节点列表) 和入度表
	adj := make(map[string][]string, len(wf.Nodes))
	inDegree := make(map[string]int, len(wf.Nodes))

	for _, n := range wf.Nodes {
		inDegree[n.ID] = len(n.DependsOn) // 初始入度 = 依赖数
		for _, dep := range n.DependsOn {
			adj[dep] = append(adj[dep], n.ID)
		}
	}

	// BFS 拓扑排序：从所有入度为 0 的节点开始
	var waves [][]string
	visited := 0
	queue := make([]string, 0)

	for _, n := range wf.Nodes {
		if inDegree[n.ID] == 0 {
			queue = append(queue, n.ID)
		}
	}

	for len(queue) > 0 {
		// 当前层的所有节点形成一个波次
		wave := make([]string, len(queue))
		copy(wave, queue)
		waves = append(waves, wave)

		nextQueue := make([]string, 0)
		for _, nodeID := range queue {
			visited++
			// 该节点的所有后继节点入度减 1
			for _, successor := range adj[nodeID] {
				inDegree[successor]--
				if inDegree[successor] == 0 {
					nextQueue = append(nextQueue, successor)
				}
			}
		}
		queue = nextQueue
	}

	// 循环依赖检测：如果 visited != len(nodes)，说明存在环
	if visited != len(wf.Nodes) {
		// 收集剩余节点以提供诊断信息
		var remaining []string
		for _, n := range wf.Nodes {
			if inDegree[n.ID] > 0 {
				remaining = append(remaining, n.ID)
			}
		}
		return nil, fmt.Errorf("cycle detected in workflow %q: nodes still have unresolved dependencies: %v", wf.Name, remaining)
	}

	// 计算临界路径（最长依赖链）
	criticalPath := computeCriticalPath(wf.Nodes, adj)

	plan := &workflow.ExecutionPlan{
		WorkflowName: wf.Name,
		Waves:        waves,
		NodeMap:      nodeMap,
		CriticalPath: criticalPath,
		TotalNodes:   len(wf.Nodes),
	}

	s.logger.Debug("workflow 执行计划已生成",
		"name", wf.Name,
		"nodes", len(wf.Nodes),
		"waves", len(waves),
		"critical_path_len", len(criticalPath),
	)

	return plan, nil
}

// computeCriticalPath 计算从任意无依赖节点到任意无后继节点的最长路径。
//
// 使用 DP（拓扑序 DP）：dp[node] = 1 + max(dp[predecessor])，其中 predecessor
// 是 node 的前驱（node 依赖 predecessor）。
func computeCriticalPath(nodes []*workflow.Node, adj map[string][]string) []string {
	if len(nodes) == 0 {
		return nil
	}

	// dp[node] = 以该节点为终点的最长链长度
	dp := make(map[string]int)
	prev := make(map[string]string) // 前驱节点（用于回溯路径）

	// 对节点做简单拓扑排序（已保证无环）
	// inDegree 在原始 nodes 的基础上重建
	inDegree := make(map[string]int, len(nodes))
	for _, n := range nodes {
		inDegree[n.ID] = len(n.DependsOn)
	}

	queue := make([]string, 0)
	for _, n := range nodes {
		if inDegree[n.ID] == 0 {
			queue = append(queue, n.ID)
			dp[n.ID] = 1
		}
	}

	order := make([]string, 0, len(nodes))
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		order = append(order, cur)

		for _, succ := range adj[cur] {
			depth := dp[cur] + 1
			if depth > dp[succ] {
				dp[succ] = depth
				prev[succ] = cur
			}
			inDegree[succ]--
			if inDegree[succ] == 0 {
				queue = append(queue, succ)
			}
		}
	}

	// 找最长路径的终点
	var endID string
	maxLen := 0
	for id, length := range dp {
		if length > maxLen {
			maxLen = length
			endID = id
		}
	}

	if endID == "" {
		return nil
	}

	// 从终点回溯到起点
	path := make([]string, 0, maxLen)
	for cur := endID; cur != ""; {
		path = append(path, cur)
		cur = prev[cur]
	}
	// 反转得到从起点到终点的顺序
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// ---------------------------------------------------------------------------
// Execute: 逐波并行执行
// ---------------------------------------------------------------------------

// Execute 按拓扑排序波次执行 workflow。
//
// 执行流程：
//  1. 创建运行时状态
//  2. 逐波迭代：波内并发（errgroup），波间串行
//  3. 监听每个节点的生命周期事件
//  4. 失败策略处理（fail_fast / continue）
//  5. 汇总结果为 WorkflowResult + WorkflowState
func (s *WorkflowService) Execute(
	ctx context.Context,
	plan *workflow.ExecutionPlan,
	executor workflow.NodeExecutor,
) (*workflow.WorkflowResult, *workflow.WorkflowState, error) {
	if plan == nil || len(plan.Waves) == 0 {
		return nil, nil, fmt.Errorf("empty execution plan")
	}

	// 收集所有节点 ID
	var allIDs []string
	for _, wave := range plan.Waves {
		allIDs = append(allIDs, wave...)
	}

	state := workflow.NewWorkflowState(plan.WorkflowName, allIDs, len(plan.Waves))
	state.SetStatus(workflow.WorkflowStatusRunning)
	startTime := time.Now()

	s.publish(workflow.WorkflowEvent{
		Phase:        workflow.WorkflowEventPhaseStart,
		WorkflowName: plan.WorkflowName,
		TotalWaves:   len(plan.Waves),
		Progress:     0,
	})

	// 创建可取消的 context，用于 fail_fast 场景
	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		nodeResults      []*workflow.NodeResult
		completedOutputs = make(map[string]string) // nodeID → output text，用于为依赖节点注入上下文
		mu               sync.Mutex
		globalFail       bool
	)

	// 逐波执行
	for waveIdx, wave := range plan.Waves {
		// 检查 context 是否已取消
		select {
		case <-execCtx.Done():
			s.logger.Warn("workflow 被取消", "name", plan.WorkflowName, "wave", waveIdx)
			// 将未执行节点标记为 canceled
			for _, nid := range wave {
				state.CancelNode(nid)
			}
			for w := waveIdx + 1; w < len(plan.Waves); w++ {
				for _, nid := range plan.Waves[w] {
					state.CancelNode(nid)
				}
			}
			state.SetStatus(workflow.WorkflowStatusCanceled)
			return s.buildResult(plan, state, nodeResults, startTime), state, nil
		default:
		}

		state.SetCurrentWave(waveIdx)

		s.publish(workflow.WorkflowEvent{
			Phase:        workflow.WorkflowEventPhaseWaveStart,
			WorkflowName: plan.WorkflowName,
			WaveIndex:    waveIdx,
			TotalWaves:   len(plan.Waves),
			Progress:     state.Progress(),
		})

		// 波内并发执行
		g, gCtx := errgroup.WithContext(execCtx)
		g.SetLimit(10) // 波内最大并发 10

		// 收集波内失败
		waveFailed := false
		var waveMu sync.Mutex

		for _, nodeID := range wave {
			nodeID := nodeID
			node := plan.NodeMap[nodeID]
			if node == nil {
				state.SkipNode(nodeID, "node definition not found in plan")
				continue
			}

			// 检查前置节点是否全部成功（仅在失效策略为 fail_fast 时需要跳过后继）
			if globalFail {
				// 快速失败模式下，跳过所有剩余节点
				state.SkipNode(nodeID, "workflow terminated due to previous node failure (fail_fast)")
				continue
			}

			// 为当前节点注入依赖节点的输出上下文
			// 这样下游节点可以引用上游节点的产出，形成真正的 DAG 数据流
			depContext := buildDependencyContext(node, completedOutputs)

			// 入队
			state.QueueNode(nodeID)

			g.Go(func() error {
				nodeCtx, nodeCancel := context.WithCancel(gCtx)
				defer nodeCancel()

				// 可选：超时控制
				if node.TimeoutSec > 0 {
					var timeoutCancel context.CancelFunc
					nodeCtx, timeoutCancel = context.WithTimeout(nodeCtx, time.Duration(node.TimeoutSec)*time.Second)
					defer timeoutCancel()
				}

				// 标记启动
				if err := state.StartNode(nodeID); err != nil {
					s.logger.Warn("节点启动失败", "node", nodeID, "error", err)
					return err
				}

				s.publish(workflow.WorkflowEvent{
					Phase:        workflow.WorkflowEventPhaseNodeStart,
					WorkflowName: plan.WorkflowName,
					WaveIndex:    waveIdx,
					TotalWaves:   len(plan.Waves),
					NodeID:       nodeID,
					Progress:     state.Progress(),
				})

				// === 执行节点（通过 NodeExecutor，即 AgentService） ===
				// 若该节点有依赖且依赖节点已有输出，将依赖输出注入 prompt 上下文
				execNode := node
				if depContext != "" {
					nodeCopy := *node
					nodeCopy.Prompt = depContext + "\n\n---\n\n" + node.Prompt
					execNode = &nodeCopy
				}
				result, execErr := executor.ExecuteNode(nodeCtx, execNode)
				if execErr != nil {
					state.FailNode(nodeID, execErr.Error())
					s.publish(workflow.WorkflowEvent{
						Phase:        workflow.WorkflowEventPhaseNodeEnd,
						WorkflowName: plan.WorkflowName,
						WaveIndex:    waveIdx,
						TotalWaves:   len(plan.Waves),
						NodeID:       nodeID,
						NodeStatus:   workflow.NodeStatusFailed,
						NodeError:    execErr.Error(),
						Progress:     state.Progress(),
					})

					mu.Lock()
					nodeResults = append(nodeResults, &workflow.NodeResult{
						NodeID:  nodeID,
						Status:  workflow.NodeStatusFailed,
						Error:   execErr.Error(),
						EndedAt: time.Now(),
					})
					mu.Unlock()

					// 失败策略
					strategy := node.FailureStrategy
					if strategy == "" {
						strategy = workflow.FailureStrategyFailFast
					}

					waveMu.Lock()
					waveFailed = true
					waveMu.Unlock()

					if strategy == workflow.FailureStrategyFailFast {
						// 取消后续所有节点
						cancel()
					}
					return execErr
				}

				// 检查执行结果状态
				if result.Status == workflow.NodeStatusFailed {
					state.FailNode(nodeID, result.Error)
					s.publish(workflow.WorkflowEvent{
						Phase:        workflow.WorkflowEventPhaseNodeEnd,
						WorkflowName: plan.WorkflowName,
						WaveIndex:    waveIdx,
						TotalWaves:   len(plan.Waves),
						NodeID:       nodeID,
						NodeStatus:   workflow.NodeStatusFailed,
						NodeError:    result.Error,
						Progress:     state.Progress(),
					})

					mu.Lock()
					nodeResults = append(nodeResults, result)
					mu.Unlock()

					strategy := node.FailureStrategy
					if strategy == "" {
						strategy = workflow.FailureStrategyFailFast
					}

					waveMu.Lock()
					waveFailed = true
					waveMu.Unlock()

					if strategy == workflow.FailureStrategyFailFast {
						cancel()
					}
					return fmt.Errorf("node %s failed: %s", nodeID, result.Error)
				}

				// 成功完成
				state.CompleteNode(nodeID, result.Output)
				s.publish(workflow.WorkflowEvent{
					Phase:        workflow.WorkflowEventPhaseNodeEnd,
					WorkflowName: plan.WorkflowName,
					WaveIndex:    waveIdx,
					TotalWaves:   len(plan.Waves),
					NodeID:       nodeID,
					NodeStatus:   workflow.NodeStatusCompleted,
					NodeOutput:   result.Output,
					Progress:     state.Progress(),
				})

				mu.Lock()
				nodeResults = append(nodeResults, result)
				mu.Unlock()

				return nil
			})
		}

		// 等待当前波次所有节点完成
		_ = g.Wait() // 错误已通过 cancel 和 waveFailed 处理

		// 收集本波次完成节点的输出，供后续波次的依赖节点使用
		mu.Lock()
		for _, nr := range nodeResults {
			if nr.Status == workflow.NodeStatusCompleted && nr.Output != "" {
				completedOutputs[nr.NodeID] = nr.Output
			}
		}
		mu.Unlock()

		// 波次结束
		s.publish(workflow.WorkflowEvent{
			Phase:        workflow.WorkflowEventPhaseWaveEnd,
			WorkflowName: plan.WorkflowName,
			WaveIndex:    waveIdx,
			TotalWaves:   len(plan.Waves),
			Progress:     state.Progress(),
		})

		if waveFailed {
			globalFail = true
		}

		// 如果 context 已取消（fail_fast 或手动取消），不再执行后续波次
		if globalFail {
			// 将后续波次节点标记为 skipped
			for w := waveIdx + 1; w < len(plan.Waves); w++ {
				for _, nid := range plan.Waves[w] {
					state.SkipNode(nid, "skipped due to previous node failure")
				}
			}
			break
		}
	}

	// 确定最终状态
	finalStatus := workflow.WorkflowStatusCompleted
	if globalFail || state.CountByStatus()[workflow.NodeStatusFailed] > 0 {
		finalStatus = workflow.WorkflowStatusFailed
	}
	if ctx.Err() != nil && state.CountByStatus()[workflow.NodeStatusCanceled] > 0 {
		finalStatus = workflow.WorkflowStatusCanceled
	}
	state.SetStatus(finalStatus)

	s.publish(workflow.WorkflowEvent{
		Phase:          workflow.WorkflowEventPhaseEnd,
		WorkflowName:   plan.WorkflowName,
		TotalWaves:     len(plan.Waves),
		WorkflowStatus: finalStatus,
		Progress:       100,
	})

	result := s.buildResult(plan, state, nodeResults, startTime)
	return result, state, nil
}

// buildResult 汇总执行结果
func (s *WorkflowService) buildResult(
	plan *workflow.ExecutionPlan,
	state *workflow.WorkflowState,
	results []*workflow.NodeResult,
	startTime time.Time,
) *workflow.WorkflowResult {
	counts := state.CountByStatus()
	return &workflow.WorkflowResult{
		WorkflowName: plan.WorkflowName,
		Status:       state.GetStatus(),
		TotalNodes:   plan.TotalNodes,
		Completed:    counts[workflow.NodeStatusCompleted],
		Failed:       counts[workflow.NodeStatusFailed],
		Skipped:      counts[workflow.NodeStatusSkipped] + counts[workflow.NodeStatusCanceled],
		Elapsed:      time.Since(startTime),
		NodeResults:  results,
	}
}

// ---------------------------------------------------------------------------
// NodeExecutor 适配器：将 Node 转换为 AgentService.Run 调用
// ---------------------------------------------------------------------------

// AgentNodeExecutor 实现 workflow.NodeExecutor，桥接 workflow.Node 到 AgentService.Run。
type AgentNodeExecutor struct {
	agentSvc *AgentService
	factory  AgentEngineFactory
	defaults WorkflowDefaults
}

// NewAgentNodeExecutor 创建节点执行器。
func NewAgentNodeExecutor(agentSvc *AgentService, factory AgentEngineFactory, defaults WorkflowDefaults) *AgentNodeExecutor {
	return &AgentNodeExecutor{
		agentSvc: agentSvc,
		factory:  factory,
		defaults: defaults,
	}
}

// ExecuteNode 将 Node 转换为 AgentService.Run 调用并执行。
func (e *AgentNodeExecutor) ExecuteNode(ctx context.Context, node *workflow.Node) (*workflow.NodeResult, error) {
	startedAt := time.Now()

	// 构建 RunOptions
	modelOverride := node.Model
	if modelOverride == "" {
		modelOverride = e.defaults.DefaultModel
	}

	opts := RunOptions{
		Prompt:          node.Prompt,
		ParentSessionID: e.defaults.ParentSessionID,
		WorkingDir:      e.defaults.WorkingDir,
		ProjectRoot:     e.defaults.ProjectRoot,
		ModelOverride:   modelOverride,
		DefaultModel:    e.defaults.DefaultModel, // 兜底：ModelOverride 为空时回退到 DefaultModel
		WorkspaceRoot:   e.defaults.WorkspaceRoot,
	}

	// 如果定义了 Skills，需要将其预加载
	if len(node.Skills) > 0 && e.agentSvc.skillSvc != nil {
		for _, skillName := range node.Skills {
			content, ok := e.agentSvc.skillSvc.RenderWith(skillName, RenderContext{
				SessionID:  e.defaults.ParentSessionID,
				ProjectDir: e.defaults.ProjectRoot,
				Cwd:        e.defaults.WorkingDir,
			})
			if ok {
				opts.PreloadedSkills = append(opts.PreloadedSkills, PreloadedSkill{
					Name:    skillName,
					Content: content,
				})
			}
		}
	}

	// 规范化 subagent_type（去空白，避免 LLM 生成时携带多余空格导致查找失败）
	subagentType := strings.TrimSpace(node.SubagentType)

	// 先检查 agent 是否存在
	_, exists := e.agentSvc.registry.Get(subagentType)
	if !exists {
		return &workflow.NodeResult{
			NodeID:    node.ID,
			Status:    workflow.NodeStatusFailed,
			Error:     fmt.Sprintf("subagent type %q not found", subagentType),
			StartedAt: startedAt,
			EndedAt:   time.Now(),
		}, nil
	}

	// 执行 subagent
	result, err := e.agentSvc.Run(ctx, subagentType, e.factory, opts)

	elapsed := time.Since(startedAt)
	endedAt := time.Now()

	if err != nil {
		return &workflow.NodeResult{
			NodeID:    node.ID,
			Status:    workflow.NodeStatusFailed,
			Error:     fmt.Sprintf("subagent %q failed: %v", node.SubagentType, err),
			AgentID:   "",
			Elapsed:   elapsed,
			StartedAt: startedAt,
			EndedAt:   endedAt,
		}, nil
	}

	return &workflow.NodeResult{
		NodeID:    node.ID,
		Status:    workflow.NodeStatusCompleted,
		Output:    result.FinalText,
		AgentID:   result.AgentID,
		Elapsed:   elapsed,
		Turns:     result.TurnCount,
		StartedAt: startedAt,
		EndedAt:   endedAt,
	}, nil
}

// RegisterNodeExecutor 将 workflow.NodeExecutor 注册回 WorkflowService，供外部直接调用。
// 当外部需要直接执行（跳过 workflow 的波次编排）时使用。
func (s *WorkflowService) RegisterNodeExecutor(executor workflow.NodeExecutor) {
	// 存储引用（如需未来扩展）
}

// buildDependencyContext 为当前节点构建其所有依赖节点的输出上下文。
//
// 当节点 A 依赖节点 B 和 C 时，若 B 和 C 已完成（outputs 中有记录），
// 则将 B 和 C 的输出注入 A 的 prompt 前，让 A 可以引用上游产出。
//
// 参数:
//   - node: 当前要执行的节点
//   - outputs: 已完成节点的 ID → 输出文本映射
//
// 返回: 格式化后的依赖输出上下文；若无依赖或有未完成的依赖则返回空字符串。
func buildDependencyContext(node *workflow.Node, outputs map[string]string) string {
	if len(node.DependsOn) == 0 {
		return ""
	}

	var parts []string
	for _, depID := range node.DependsOn {
		if out, ok := outputs[depID]; ok && out != "" {
			// 截断过长的输出，避免上下文溢出
			summary := truncateOutput(out, 4000)
			parts = append(parts, fmt.Sprintf("## Output from dependency: %s\n\n%s", depID, summary))
		}
	}

	if len(parts) == 0 {
		return ""
	}

	return strings.Join(parts, "\n\n") +
		"\n\n---\n\nUse the above dependency outputs to complete your task. " +
		"If you need more detail from a dependency, read the files they produced."
}

// truncateOutput 截断输出文本，确保不会因为过长输出导致上下文溢出。
func truncateOutput(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n\n[... output truncated, see files for full results ...]"
}
