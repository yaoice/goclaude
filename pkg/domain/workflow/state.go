package workflow

import (
	"fmt"
	"sync"
	"time"
)

// NodeStatus 节点运行时状态
type NodeStatus string

const (
	// NodeStatusPending 等待入队（前置依赖尚未完成）
	NodeStatusPending NodeStatus = "pending"
	// NodeStatusQueued 已入队，等待被调度执行
	NodeStatusQueued NodeStatus = "queued"
	// NodeStatusRunning 正在执行中
	NodeStatusRunning NodeStatus = "running"
	// NodeStatusCompleted 执行成功完成
	NodeStatusCompleted NodeStatus = "completed"
	// NodeStatusFailed 执行失败
	NodeStatusFailed NodeStatus = "failed"
	// NodeStatusSkipped 因前置节点失败而被跳过（仅在 FailureStrategyContinue 模式下）
	NodeStatusSkipped NodeStatus = "skipped"
	// NodeStatusCanceled 被取消
	NodeStatusCanceled NodeStatus = "canceled"
)

// IsTerminal 节点是否处于终态
func (s NodeStatus) IsTerminal() bool {
	switch s {
	case NodeStatusCompleted, NodeStatusFailed, NodeStatusSkipped, NodeStatusCanceled:
		return true
	}
	return false
}

// WorkflowStatus 整条 workflow 的状态
type WorkflowStatus string

const (
	// WorkflowStatusPending 等待执行
	WorkflowStatusPending WorkflowStatus = "pending"
	// WorkflowStatusRunning 正在执行中
	WorkflowStatusRunning WorkflowStatus = "running"
	// WorkflowStatusCompleted 所有节点成功完成
	WorkflowStatusCompleted WorkflowStatus = "completed"
	// WorkflowStatusFailed 因故障而终止
	WorkflowStatusFailed WorkflowStatus = "failed"
	// WorkflowStatusCanceled 被手动取消
	WorkflowStatusCanceled WorkflowStatus = "canceled"
)

// IsTerminal workflow 是否处于终态
func (s WorkflowStatus) IsTerminal() bool {
	return s == WorkflowStatusCompleted || s == WorkflowStatusFailed || s == WorkflowStatusCanceled
}

// NodeState 单个节点的运行时状态快照
type NodeState struct {
	NodeID    string
	Status    NodeStatus
	Output    string
	Error     string
	StartedAt *time.Time
	EndedAt   *time.Time
}

// Clone 深拷贝状态快照（用于事件推送，避免竞态）
func (ns *NodeState) Clone() *NodeState {
	c := &NodeState{
		NodeID: ns.NodeID,
		Status: ns.Status,
		Output: ns.Output,
		Error:  ns.Error,
	}
	if ns.StartedAt != nil {
		t := *ns.StartedAt
		c.StartedAt = &t
	}
	if ns.EndedAt != nil {
		t := *ns.EndedAt
		c.EndedAt = &t
	}
	return c
}

// WorkflowState 整条 workflow 的运行时状态。
//
// 线程安全（sync.RWMutex）。Engine 在节点生命周期变更时调用
// 对应方法更新，上层（REPL）通过 Snapshot 获取一致性快照渲染。
type WorkflowState struct {
	mu    sync.RWMutex
	Name  string
	Nodes map[string]*NodeState

	// Status workflow 整体状态
	Status WorkflowStatus
	// CurrentWave 当前执行的波次（从 0 开始）
	CurrentWave int
	// TotalWaves 总波次数
	TotalWaves int
}

// NewWorkflowState 创建新的 workflow 运行时状态
func NewWorkflowState(name string, nodeIDs []string, totalWaves int) *WorkflowState {
	nodes := make(map[string]*NodeState, len(nodeIDs))
	for _, id := range nodeIDs {
		nodes[id] = &NodeState{
			NodeID: id,
			Status: NodeStatusPending,
		}
	}
	return &WorkflowState{
		Name:       name,
		Nodes:      nodes,
		Status:     WorkflowStatusPending,
		TotalWaves: totalWaves,
	}
}

// SetStatus 设置 workflow 整体状态
func (ws *WorkflowState) SetStatus(s WorkflowStatus) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.Status = s
}

// GetStatus 获取 workflow 整体状态
func (ws *WorkflowState) GetStatus() WorkflowStatus {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return ws.Status
}

// SetCurrentWave 设置当前波次
func (ws *WorkflowState) SetCurrentWave(wave int) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.CurrentWave = wave
}

// GetCurrentWave 获取当前波次
func (ws *WorkflowState) GetCurrentWave() int {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return ws.CurrentWave
}

// StartNode 将节点标记为运行中
func (ws *WorkflowState) StartNode(nodeID string) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	ns, ok := ws.Nodes[nodeID]
	if !ok {
		return fmt.Errorf("unknown node: %s", nodeID)
	}
	if ns.Status != NodeStatusQueued {
		return fmt.Errorf("cannot start node %s in status %s", nodeID, ns.Status)
	}
	ns.Status = NodeStatusRunning
	now := time.Now()
	ns.StartedAt = &now
	return nil
}

// CompleteNode 将节点标记为完成
func (ws *WorkflowState) CompleteNode(nodeID string, output string) {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ns, ok := ws.Nodes[nodeID]; ok {
		ns.Status = NodeStatusCompleted
		ns.Output = output
		now := time.Now()
		ns.EndedAt = &now
	}
}

// FailNode 将节点标记为失败
func (ws *WorkflowState) FailNode(nodeID string, err string) {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ns, ok := ws.Nodes[nodeID]; ok {
		ns.Status = NodeStatusFailed
		ns.Error = err
		now := time.Now()
		ns.EndedAt = &now
	}
}

// SkipNode 将节点标记为跳过
func (ws *WorkflowState) SkipNode(nodeID string, reason string) {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ns, ok := ws.Nodes[nodeID]; ok {
		ns.Status = NodeStatusSkipped
		ns.Error = reason
	}
}

// CancelNode 将节点标记为取消
func (ws *WorkflowState) CancelNode(nodeID string) {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ns, ok := ws.Nodes[nodeID]; ok {
		ns.Status = NodeStatusCanceled
		now := time.Now()
		ns.EndedAt = &now
	}
}

// QueueNode 将节点标记为已入队
func (ws *WorkflowState) QueueNode(nodeID string) {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ns, ok := ws.Nodes[nodeID]; ok {
		ns.Status = NodeStatusQueued
	}
}

// GetNodeState 获取单个节点的状态快照
func (ws *WorkflowState) GetNodeState(nodeID string) (*NodeState, bool) {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	ns, ok := ws.Nodes[nodeID]
	if !ok {
		return nil, false
	}
	return ns.Clone(), true
}

// Snapshot 获取整条 workflow 的状态快照（一致性读取）
func (ws *WorkflowState) Snapshot() *WorkflowState {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	nodes := make(map[string]*NodeState, len(ws.Nodes))
	for id, ns := range ws.Nodes {
		nodes[id] = ns.Clone()
	}
	return &WorkflowState{
		Name:        ws.Name,
		Nodes:       nodes,
		Status:      ws.Status,
		CurrentWave: ws.CurrentWave,
		TotalWaves:  ws.TotalWaves,
	}
}

// Progress 计算执行进度（完成/失败的节点百分比）
func (ws *WorkflowState) Progress() float64 {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	if len(ws.Nodes) == 0 {
		return 100.0
	}
	done := 0
	for _, ns := range ws.Nodes {
		if ns.Status.IsTerminal() {
			done++
		}
	}
	return float64(done) / float64(len(ws.Nodes)) * 100.0
}

// CountByStatus 按状态统计节点数量
func (ws *WorkflowState) CountByStatus() map[NodeStatus]int {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	counts := make(map[NodeStatus]int)
	for _, ns := range ws.Nodes {
		counts[ns.Status]++
	}
	return counts
}

// AllNodesTerminal 是否所有节点都进入终态
func (ws *WorkflowState) AllNodesTerminal() bool {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	for _, ns := range ws.Nodes {
		if !ns.Status.IsTerminal() {
			return false
		}
	}
	return true
}
