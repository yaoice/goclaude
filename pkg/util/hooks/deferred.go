// Package hooks — deferred hook messages
//
// 对齐 src/hooks/useDeferredHookMessages.ts：
// 提供线程安全的延迟消息缓冲，支持累积消息并在适当时机批量取出（flush）。
package hooks

import "sync"

// DeferredMessages deferred hook 消息管理器
// Equivalent of useDeferredHookMessages
type DeferredMessages struct {
	mu       sync.RWMutex
	messages []string
}

// NewDeferredMessages 创建延迟消息管理器
func NewDeferredMessages() *DeferredMessages {
	return &DeferredMessages{}
}

// Add 添加延迟消息
func (d *DeferredMessages) Add(msg string) {
	d.mu.Lock()
	d.messages = append(d.messages, msg)
	d.mu.Unlock()
}

// Flush 取出所有待处理消息并清空
func (d *DeferredMessages) Flush() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	msgs := d.messages
	d.messages = nil
	return msgs
}

// Count 返回待处理消息数量
func (d *DeferredMessages) Count() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.messages)
}
