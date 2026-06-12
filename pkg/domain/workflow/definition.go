// Package workflow 定义 workflow 编排系统的领域模型。
//
// workflow 是 subagent 编排的核心抽象：将多个 subagent 任务
// 组织为带依赖关系的有向无环图（DAG），支持拓扑排序→波次分解→
// 并行执行的编排流程。与简单的并发 subagent 执行不同，workflow
// 严格保证依赖任务完成后才启动后继任务。
//
// 对齐 oh-my-openagent/src/tools/delegate-task/constants.ts
// 中 Plan Agent 的依赖图 + 并行执行波次设计。
package workflow

import "time"

// FailureStrategy 节点失败时的处理策略
type FailureStrategy string

const (
	// FailureStrategyFailFast 任一节点失败立即取消整条 workflow（默认）
	FailureStrategyFailFast FailureStrategy = "fail_fast"
	// FailureStrategyContinue 独立节点失败不影响其他并行节点，
	// 只将依赖该失败节点的后续节点标记为 skipped
	FailureStrategyContinue FailureStrategy = "continue"
)

// Node 定义 workflow 中的一个任务节点。
//
// 对齐 oh-my-openagent Plan Agent 输出的 Task 结构：
//
//	### Task N: [Title]
//	**Description**: ...
//	**Depends On**: [Task IDs]
//	**Category**: `category`
//	**Skills**: [`skill-1`]
type Node struct {
	// ID 节点唯一标识（在 workflow 内唯一）
	ID string `yaml:"id" json:"id"`
	// Name 可读名称（用于 UI 渲染）
	Name string `yaml:"name" json:"name"`
	// Description 节点描述（3-5 词，对齐 AgentTool 的 description 字段）
	Description string `yaml:"description" json:"description"`
	// SubagentType 执行的 subagent 类型（对应 AgentService 中的 agentType）
	SubagentType string `yaml:"subagent_type" json:"subagent_type"`
	// Prompt 下发给 subagent 的具体任务提示词
	Prompt string `yaml:"prompt" json:"prompt"`
	// DependsOn 依赖的节点 ID 列表。
	// 为空列表表示无依赖（进入 Wave 0，可立即开始）
	DependsOn []string `yaml:"depends_on" json:"depends_on"`
	// TimeoutSec 超时秒数；0 表示无限制
	TimeoutSec int `yaml:"timeout_sec" json:"timeout_sec"`
	// FailureStrategy 节点失败策略（默认 fail_fast）
	FailureStrategy FailureStrategy `yaml:"failure_strategy" json:"failure_strategy"`
	// Skills 可选，预加载的 skill 名称列表
	Skills []string `yaml:"skills" json:"skills,omitempty"`
	// Model 可选，覆盖的模型名
	Model string `yaml:"model" json:"model,omitempty"`
}

// Workflow 完整的 workflow 定义。
//
// 对应 YAML 文件中的顶层结构：
//
//	name: feature-build
//	description: 端到端构建新功能
//	version: "1.0"
//	nodes:
//	  - id: explore-codebase
//	    name: 探索代码库
//	    description: 查找相关模式
//	    subagent_type: Explore
//	    prompt: ...
//	    depends_on: []
type Workflow struct {
	// Name 名称（用于 /workflow run <name>），文件名去扩展名
	Name string `yaml:"name" json:"name"`
	// Description 描述
	Description string `yaml:"description" json:"description"`
	// Version 版本号
	Version string `yaml:"version" json:"version"`
	// Nodes 节点列表
	Nodes []*Node `yaml:"nodes" json:"nodes"`
}

// Validate 验证 workflow 定义的合法性：
//   - Name 不为空
//   - 至少一个节点
//   - 所有节点 ID 唯一
//   - 所有 depends_on 引用的节点存在
//   - 无自依赖
func (w *Workflow) Validate() error {
	if w.Name == "" {
		return &ValidationError{Field: "name", Message: "workflow name is required"}
	}
	if len(w.Nodes) == 0 {
		return &ValidationError{Field: "nodes", Message: "at least one node is required"}
	}

	nodeIDs := make(map[string]bool)
	for _, n := range w.Nodes {
		if n.ID == "" {
			return &ValidationError{Field: "nodes", Message: "node id is required"}
		}
		if nodeIDs[n.ID] {
			return &ValidationError{Field: "nodes", Message: "duplicate node id: " + n.ID}
		}
		nodeIDs[n.ID] = true
	}

	for _, n := range w.Nodes {
		for _, dep := range n.DependsOn {
			if dep == n.ID {
				return &ValidationError{
					Field:   "nodes",
					Node:    n.ID,
					Message: "self-dependency detected",
				}
			}
			if !nodeIDs[dep] {
				return &ValidationError{
					Field:   "nodes",
					Node:    n.ID,
					Message: "depends_on references unknown node: " + dep,
				}
			}
		}

		// 验证 failure_strategy 只能是合法值或空（空默认为 fail_fast）
		if n.FailureStrategy != "" &&
			n.FailureStrategy != FailureStrategyFailFast &&
			n.FailureStrategy != FailureStrategyContinue {
			return &ValidationError{
				Field:   "nodes",
				Node:    n.ID,
				Message: "invalid failure_strategy: " + string(n.FailureStrategy) + " (expected: fail_fast, continue, or empty)",
			}
		}
	}

	return nil
}

// NodeMap 构建节点 ID → 节点定义的 map
func (w *Workflow) NodeMap() map[string]*Node {
	m := make(map[string]*Node, len(w.Nodes))
	for _, n := range w.Nodes {
		m[n.ID] = n
	}
	return m
}

// ValidationError workflow 验证错误
type ValidationError struct {
	Field   string
	Node    string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Node != "" {
		return "workflow validation: [" + e.Field + "/" + e.Node + "] " + e.Message
	}
	return "workflow validation: [" + e.Field + "] " + e.Message
}

// ---------------------------------------------------------------------------
// 运行时结果类型
// ---------------------------------------------------------------------------

// NodeResult 单个节点执行结果
type NodeResult struct {
	NodeID    string        `json:"node_id"`
	Status    NodeStatus    `json:"status"`
	Output    string        `json:"output,omitempty"`
	Error     string        `json:"error,omitempty"`
	AgentID   string        `json:"agent_id,omitempty"`
	Elapsed   time.Duration `json:"elapsed"`
	Turns     int           `json:"turns"`
	StartedAt time.Time     `json:"started_at"`
	EndedAt   time.Time     `json:"ended_at"`
}

// WorkflowResult 整条 workflow 的执行汇总
type WorkflowResult struct {
	WorkflowName string         `json:"workflow_name"`
	Status       WorkflowStatus `json:"status"`
	TotalNodes   int            `json:"total_nodes"`
	Completed    int            `json:"completed"`
	Failed       int            `json:"failed"`
	Skipped      int            `json:"skipped"`
	Elapsed      time.Duration  `json:"elapsed"`
	NodeResults  []*NodeResult  `json:"node_results,omitempty"`
}
