// Package hooks 提供 session 级 hook 实现
//
// 对齐 src/hooks/useAwaySummary.ts, useDirectConnect.ts, useCancelRequest.ts
package hooks

import (
	"sync"
	"time"
)

// ============================================================
// AwaySummary — "离开时摘要" Hook
// 对齐 src/hooks/useAwaySummary.ts
//
// 核心逻辑：终端失焦 5 分钟后，生成一条 "while you were away" 摘要消息。
// 要求 (a) 失焦 >= 5min, (b) 无活跃 turn, (c) 自上次用户消息以来没有已有摘要。
// ============================================================

const blurDelayMs = 5 * time.Minute

// AwaySummary 离开摘要管理器
type AwaySummary struct {
	mu           sync.Mutex
	timer        *time.Timer
	abortCh      chan struct{}
	pending      bool
	blurred      bool
	isLoading    bool
	messages     []AwayMessage
	generateFn   func(messages []AwayMessage, signal <-chan struct{}) (string, error)
	onAddMessage func(summaryText string)
}

// AwayMessage 用于摘要的消息格式
type AwayMessage struct {
	Type    string
	Content string
	IsMeta  bool
}

// NewAwaySummary 创建离开摘要管理器
func NewAwaySummary(
	generateFn func(messages []AwayMessage, signal <-chan struct{}) (string, error),
	onAddMessage func(summaryText string),
) *AwaySummary {
	return &AwaySummary{
		abortCh:      make(chan struct{}),
		generateFn:   generateFn,
		onAddMessage: onAddMessage,
	}
}

// SetMessages 更新消息列表
func (a *AwaySummary) SetMessages(msgs []AwayMessage) {
	a.mu.Lock()
	a.messages = msgs
	a.mu.Unlock()
}

// SetLoading 更新加载状态
func (a *AwaySummary) SetLoading(loading bool) {
	a.mu.Lock()
	a.isLoading = loading
	if !loading && a.pending && a.blurred {
		a.pending = false
		a.mu.Unlock()
		go a.generate()
		return
	}
	a.mu.Unlock()
}

// Focus 终端获得焦点
func (a *AwaySummary) Focus() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.blurred = false
	a.clearTimer()
	a.abort()
	a.pending = false
}

// Blur 终端失去焦点
func (a *AwaySummary) Blur() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.blurred = true
	a.clearTimer()
	a.timer = time.AfterFunc(blurDelayMs, func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.timer = nil
		if a.isLoading {
			a.pending = true
			return
		}
		go a.generate()
	})
}

func (a *AwaySummary) generate() {
	a.mu.Lock()
	if hasSummarySinceLastUserTurn(a.messages) {
		a.mu.Unlock()
		return
	}
	msgs := make([]AwayMessage, len(a.messages))
	copy(msgs, a.messages)
	a.mu.Unlock()

	a.abort()
	abortCh := make(chan struct{})
	a.mu.Lock()
	a.abortCh = abortCh
	a.mu.Unlock()

	text, err := a.generateFn(msgs, abortCh)
	if err != nil || text == "" {
		return
	}
	if a.onAddMessage != nil {
		a.onAddMessage(text)
	}
}

func hasSummarySinceLastUserTurn(msgs []AwayMessage) bool {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Type == "user" && !m.IsMeta {
			return false
		}
		if m.Type == "system" && m.Content != "" {
			return true // simplified check
		}
	}
	return false
}

func (a *AwaySummary) clearTimer() {
	if a.timer != nil {
		a.timer.Stop()
		a.timer = nil
	}
}

func (a *AwaySummary) abort() {
	select {
	case a.abortCh <- struct{}{}:
	default:
	}
}

// ============================================================
// CancelRequestHandler — 取消请求处理器
// 对齐 src/hooks/useCancelRequest.ts
//
// 核心逻辑：优先级处理 Escape / Ctrl+C 按键。
// 1. 活跃任务 → 取消当前任务
// 2. 命令队列 → 弹出命令
// 3. 队友视图 → 退出或终止
// ============================================================

// CancelRequestState 取消请求状态
type CancelRequestState struct {
	AbortSignal       func() bool // returns true if aborted
	OnCancel          func()
	OnKillAgents      func(count int)
	HasQueuedCommands func() bool
	PopCommand        func()
}

// CancelRequestHandler 取消请求处理器
type CancelRequestHandler struct {
	mu         sync.Mutex
	state      CancelRequestState
	lastKillAt time.Time
	killWindow time.Duration
}

const defaultKillConfirmWindow = 3 * time.Second

// NewCancelRequestHandler 创建取消请求处理器
func NewCancelRequestHandler(state CancelRequestState) *CancelRequestHandler {
	return &CancelRequestHandler{
		state:      state,
		killWindow: defaultKillConfirmWindow,
	}
}

// HandleEscape 处理 Escape 键
func (c *CancelRequestHandler) HandleEscape() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Priority 1: Cancel active task
	if c.state.AbortSignal != nil && !c.state.AbortSignal() {
		c.state.OnCancel()
		return
	}

	// Priority 2: Pop command queue
	if c.state.HasQueuedCommands != nil && c.state.HasQueuedCommands() {
		if c.state.PopCommand != nil {
			c.state.PopCommand()
			return
		}
	}

	// Fallback
	c.state.OnCancel()
}

// HandleCtrlC 处理 Ctrl+C
func (c *CancelRequestHandler) HandleCtrlC() {
	c.HandleEscape()
}

// HandleKillAgents 处理 kill agents 操作（双击确认）
func (c *CancelRequestHandler) HandleKillAgents() (confirmed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	if now.Sub(c.lastKillAt) <= c.killWindow {
		c.lastKillAt = time.Time{}
		return true // confirmed
	}
	c.lastKillAt = now
	return false // first press, need confirmation
}
