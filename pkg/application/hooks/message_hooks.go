package hooks

import (
	"sync"
	"time"
)

// ============================================================
// Messages & Turn 相关 Hooks
// 对齐 src/hooks/ 中消息流和 turn 管理相关 hooks
// ============================================================

// MessageGroup 消息分组
type MessageGroup struct {
	ID        string
	Role      string
	Messages  []string
	Timestamp int64
	ToolCalls []ToolCall
}

// ToolCall 工具调用记录
type ToolCall struct {
	Name   string
	Input  map[string]interface{}
	Output string
	Error  string
	Status string // "running", "success", "error"
}

// MessageReducer 消息聚合器
// Equivalent of useMessageReducer, useMessageGroups
type MessageReducer struct {
	mu       sync.RWMutex
	groups   []MessageGroup
	onChange func([]MessageGroup)
}

// NewMessageReducer 创建消息聚合器
func NewMessageReducer() *MessageReducer {
	return &MessageReducer{}
}

// OnChange 注册变更回调
func (mr *MessageReducer) OnChange(fn func([]MessageGroup)) { mr.onChange = fn }

// AddMessage 添加消息
func (mr *MessageReducer) AddMessage(groupID, role, msg string) {
	mr.mu.Lock()
	for i, g := range mr.groups {
		if g.ID == groupID {
			mr.groups[i].Messages = append(mr.groups[i].Messages, msg)
			mr.mu.Unlock()
			if mr.onChange != nil {
				mr.onChange(mr.groups)
			}
			return
		}
	}
	mr.groups = append(mr.groups, MessageGroup{
		ID: groupID, Role: role,
		Messages: []string{msg}, Timestamp: time.Now().UnixMilli(),
	})
	mr.mu.Unlock()
}

// AddToolCall 添加工具调用记录
func (mr *MessageReducer) AddToolCall(groupID string, call ToolCall) {
	mr.mu.Lock()
	for i, g := range mr.groups {
		if g.ID == groupID {
			mr.groups[i].ToolCalls = append(mr.groups[i].ToolCalls, call)
			mr.mu.Unlock()
			if mr.onChange != nil {
				mr.onChange(mr.groups)
			}
			return
		}
	}
	mr.mu.Unlock()
}

// Groups 返回所有分组
func (mr *MessageReducer) Groups() []MessageGroup {
	mr.mu.RLock()
	defer mr.mu.RUnlock()
	result := make([]MessageGroup, len(mr.groups))
	copy(result, mr.groups)
	return result
}

// Clear 清空
func (mr *MessageReducer) Clear() {
	mr.mu.Lock()
	mr.groups = nil
	mr.mu.Unlock()
}

// TurnCounter Turn 计数器
// Equivalent of useTurnDiffs / turn-related hooks
type TurnCounter struct {
	mu       sync.RWMutex
	count    int
	max      int
	onChange func(count, max int)
}

// NewTurnCounter 创建 turn 计数器
func NewTurnCounter(max int) *TurnCounter {
	return &TurnCounter{max: max}
}

// Inc 递增
func (tc *TurnCounter) Inc() {
	tc.mu.Lock()
	tc.count++
	cb := tc.onChange
	c, m := tc.count, tc.max
	tc.mu.Unlock()
	if cb != nil {
		cb(c, m)
	}
}

// Count 当前计数
func (tc *TurnCounter) Count() int {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.count
}

// SetMax 设置最大 turn 数
func (tc *TurnCounter) SetMax(max int) {
	tc.mu.Lock()
	tc.max = max
	tc.mu.Unlock()
}

// Max 最大 turn 数
func (tc *TurnCounter) Max() int {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.max
}

// OnChange 注册变更回调
func (tc *TurnCounter) OnChange(fn func(count, max int)) { tc.onChange = fn }

// StreamingStatus 流式状态追踪
// Equivalent of useStreamingStatus
type StreamingStatus struct {
	mu        sync.RWMutex
	streaming bool
	toolName  string
	onChange  func(streaming bool, toolName string)
}

// NewStreamingStatus 创建流式状态追踪器
func NewStreamingStatus() *StreamingStatus { return &StreamingStatus{} }

// SetStreaming 设置流式状态
func (ss *StreamingStatus) SetStreaming(streaming bool, toolName string) {
	ss.mu.Lock()
	changed := ss.streaming != streaming || ss.toolName != toolName
	ss.streaming = streaming
	ss.toolName = toolName
	cb := ss.onChange
	ss.mu.Unlock()
	if changed && cb != nil {
		cb(streaming, toolName)
	}
}

// IsStreaming 是否在流式传输中
func (ss *StreamingStatus) IsStreaming() bool {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	return ss.streaming
}

// CurrentTool 当前流式工具名
func (ss *StreamingStatus) CurrentTool() string {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	return ss.toolName
}

// OnChange 注册变更回调
func (ss *StreamingStatus) OnChange(fn func(streaming bool, toolName string)) { ss.onChange = fn }
