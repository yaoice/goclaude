package workflow

import "context"

// ExecutionPlan 拓扑排序后的执行计划。
//
// 对应 oh-my-openagent Plan Agent 输出的 Parallel Execution Graph:
//
//	Wave 1 (Start immediately):
//	├── Task 1 (no dependencies)
//	└── Task 5 (no dependencies)
//	Wave 2 (After Wave 1 completes):
//	├── Task 2 (depends: Task 1)
//	└── Task 3 (depends: Task 1)
//	Wave 3 (After Wave 2 completes):
//	└── Task 4 (depends: Task 2, Task 3)
type ExecutionPlan struct {
	// WorkflowName workflow 名称
	WorkflowName string
	// Waves 按拓扑排序分组的波次列表。
	// Waves[0] 包含所有入度为 0 的节点（无依赖，可立即并行执行）
	// Waves[1] 包含所有仅依赖 Waves[0] 节点的节点
	// Waves[n] 包含所有仅依赖前三层节点的节点
	//
	// 每波内节点可安全并��执行（互不依赖）。
	Waves [][]string `json:"waves"`
	// NodeMap 节点 ID → 节点定义
	NodeMap map[string]*Node `json:"node_map"`
	// CriticalPath 临界路径（最长依赖链），按顺序包含节点 ID
	CriticalPath []string `json:"critical_path,omitempty"`
	// TotalNodes 总节点数
	TotalNodes int `json:"total_nodes"`
}

// NodeExecutor 节点执行器接口。
//
// domain 层定义接口；application 层注入 AgentService 实现。
// 每个节点执行对应一次 subagent 调用。
type NodeExecutor interface {
	// ExecuteNode 执行单个节点。
	//
	// 参数：
	//   - ctx: 带超时/取消的上下文（Engine 在波次执行时传入）
	//   - node: 要执行的节点定义
	//
	// 返回值：
	//   - NodeResult: 执行结果（包含输出、耗时、turns 等）
	//   - error: 仅基础设施级错误（如 agent 不存在）；业务级失败
	//     通过 NodeResult.Status == NodeStatusFailed 表示
	ExecuteNode(ctx context.Context, node *Node) (*NodeResult, error)
}

// Engine 工作流引擎接口。
//
// Engine 负责解析 workflow 定义、构建 DAG、拓扑排序并逐波执行。
// 这是 workflow 系统的核心抽象。
type Engine interface {
	// ParseAndValidate 解析 workflow 定义，验证依赖图合法性，
	// 构建拓扑排序后的执行计划。
	//
	// 返回错误场景：
	//   - 循环依赖
	//   - 空节点列表
	//   - 依赖不存在的节点
	ParseAndValidate(wf *Workflow) (*ExecutionPlan, error)

	// Execute 按执行计划逐波执行节点。
	//
	// 执行流程：
	//   1. 创建 WorkflowState 并标记 WorkflowStatusRunning
	//   2. 逐波迭代 Waves：
	//      a. 波内节点并发执行（通过 errgroup）
	//      b. 监听每个节点的生命周期事件→更新状态→推送给 listener
	//      c. 若启用 fail_fast 且某节点失败→取消本波其余+跳过后续波次
	//      d. 波内全部完成/失败后进入下一波
	//   3. 汇总结果为 WorkflowResult
	//
	// ctx 取消时会级联取消所有进行中的 subagent。
	Execute(ctx context.Context, plan *ExecutionPlan, executor NodeExecutor) (*WorkflowResult, *WorkflowState, error)
}

// WorkflowEventPhase 工作流事件阶段
type WorkflowEventPhase string

const (
	// WorkflowEventPhaseStart workflow 开始执行
	WorkflowEventPhaseStart WorkflowEventPhase = "workflow_start"
	// WorkflowEventPhaseWaveStart 一波开始
	WorkflowEventPhaseWaveStart WorkflowEventPhase = "wave_start"
	// WorkflowEventPhaseNodeStart 单节点开始
	WorkflowEventPhaseNodeStart WorkflowEventPhase = "node_start"
	// WorkflowEventPhaseNodeProgress 单节点进度更新
	WorkflowEventPhaseNodeProgress WorkflowEventPhase = "node_progress"
	// WorkflowEventPhaseNodeEnd 单节点结束
	WorkflowEventPhaseNodeEnd WorkflowEventPhase = "node_end"
	// WorkflowEventPhaseWaveEnd 一波结束
	WorkflowEventPhaseWaveEnd WorkflowEventPhase = "wave_end"
	// WorkflowEventPhaseEnd workflow 结束
	WorkflowEventPhaseEnd WorkflowEventPhase = "workflow_end"
)

// WorkflowEvent 工作流事件，推送至 WorkflowEventListener。
//
// 与 SubagentEvent 设计对齐，提供 workflow 粒度的进度通知。
type WorkflowEvent struct {
	Phase WorkflowEventPhase

	// WorkflowName workflow 名称
	WorkflowName string
	// WaveIndex 当前波次（仅 wave_start / wave_end / node_start / node_end）
	WaveIndex int
	// TotalWaves 总波次数
	TotalWaves int
	// NodeID 节点 ID（仅 node_start / node_progress / node_end）
	NodeID string
	// NodeStatus 节点状态（仅 node_end）
	NodeStatus NodeStatus
	// NodeOutput 节点输出（仅 node_end, status=completed）
	NodeOutput string
	// NodeError 节点错误（仅 node_end, status=failed）
	NodeError string
	// WorkflowStatus 最终状态（仅 workflow_end）
	WorkflowStatus WorkflowStatus
	// Progress 整体进度百分比（0-100）
	Progress float64
}

// WorkflowEventListener 工作流事件监听器接口。
type WorkflowEventListener interface {
	HandleWorkflowEvent(ev WorkflowEvent)
}

// WorkflowEventListenerFunc 函数适配器。
type WorkflowEventListenerFunc func(ev WorkflowEvent)

// HandleWorkflowEvent 实现 WorkflowEventListener。
func (f WorkflowEventListenerFunc) HandleWorkflowEvent(ev WorkflowEvent) { f(ev) }
