package hooks

import (
	"sync"
	"time"
)

// ============================================================
// useCopyOnSelect — 选中时复制到剪贴板
// 对齐 src/hooks/useCopyOnSelect.ts
//
// 核心逻辑：当用户选中文本时，自动复制到系统剪贴板。
// ============================================================

// CopyOnSelect 选中时复制管理器
type CopyOnSelect struct {
	enabled    bool
	copier     func(text string) error
	lastCopy   string
	lastCopyAt time.Time
	debounceMs time.Duration
	mu         sync.Mutex
}

// NewCopyOnSelect 创建
func NewCopyOnSelect(copier func(text string) error) *CopyOnSelect {
	if copier == nil {
		copier = func(string) error { return nil }
	}
	return &CopyOnSelect{
		copier:     copier,
		debounceMs: 200 * time.Millisecond,
	}
}

// SetEnabled 设置启用状态
func (c *CopyOnSelect) SetEnabled(enabled bool) {
	c.mu.Lock()
	c.enabled = enabled
	c.mu.Unlock()
}

// OnSelect 处理选中事件
func (c *CopyOnSelect) OnSelect(text string) {
	if !c.enabled || text == "" {
		return
	}

	c.mu.Lock()
	if text == c.lastCopy && time.Since(c.lastCopyAt) < c.debounceMs {
		c.mu.Unlock()
		return
	}
	c.lastCopy = text
	c.lastCopyAt = time.Now()
	copier := c.copier
	c.mu.Unlock()

	_ = copier(text)
}
