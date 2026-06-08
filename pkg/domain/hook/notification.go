// Package hook — 通知子系统
//
// 对齐 src/hooks/notifs/ 中的通知钩子：
//   - NotificationBus: 发布/订阅模式的通知总线
//   - Notification: 标准通知消息体
//
// 设计要点：
//   - 单例总线，支持多订阅者
//   - 历史记录环缓冲（可配置大小）
//   - 线程安全
//   - Subscribe 返回取消函数，方便生命周期管理
package hook

import (
	"sync"
	"time"
)

// Notification 通知消息
type Notification struct {
	ID          string
	Type        string
	Title       string
	Body        string
	Level       string    // "info", "warning", "error"
	Priority    int       // 0=low, 1=normal, 2=high
	CreatedAt   time.Time
	ExpiresAt   *time.Time
	Dismissible bool
	Actionable  bool
	Metadata    map[string]interface{}
}

// NotificationHandler 通知处理器
type NotificationHandler func(n *Notification)

// NotificationBus 通知总线（单例模式）
type NotificationBus struct {
	mu         sync.RWMutex
	handlers   []NotificationHandler
	history    []*Notification
	maxHistory int
}

// NewNotificationBus 创建通知总线
func NewNotificationBus(maxHistory int) *NotificationBus {
	if maxHistory <= 0 {
		maxHistory = 100
	}
	return &NotificationBus{
		maxHistory: maxHistory,
	}
}

// Subscribe 订阅通知，返回取消函数
func (b *NotificationBus) Subscribe(h NotificationHandler) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers = append(b.handlers, h)
	id := len(b.handlers) - 1
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.handlers = append(b.handlers[:id], b.handlers[id+1:]...)
	}
}

// Publish 发布通知
func (b *NotificationBus) Publish(n *Notification) {
	b.mu.Lock()
	b.history = append(b.history, n)
	if len(b.history) > b.maxHistory {
		b.history = b.history[1:]
	}
	handlers := make([]NotificationHandler, len(b.handlers))
	copy(handlers, b.handlers)
	b.mu.Unlock()

	for _, h := range handlers {
		h(n)
	}
}

// History 返回历史通知
func (b *NotificationBus) History() []*Notification {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]*Notification, len(b.history))
	copy(result, b.history)
	return result
}

// Clear 清空历史
func (b *NotificationBus) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.history = nil
}
