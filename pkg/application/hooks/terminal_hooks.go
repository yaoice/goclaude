// Package hooks — 终端与 Ink 钩子
//
// 对齐 src/ink/hooks/ 中的终端钩子实现：
//   - TerminalFocus:  等价于 use-terminal-focus.ts
//   - TerminalSize:   等价于 use-terminal-viewport.ts / useTerminalSize
//   - TerminalTitle:  等价于 use-terminal-title.ts
//   - Interval:       等价于 use-interval.ts
//   - AnimationFrame: 等价于 use-animation-frame.ts
//   - TabStatus:      等价于 use-tab-status.ts
//   - SearchHighlight:等价于 use-search-highlight.ts

package hooks

import (
	"sync"
	"time"
)

// TerminalFocus 终端焦点状态
// Equivalent of ink/hooks/use-terminal-focus.ts
type TerminalFocus struct {
	mu       sync.RWMutex
	focused  bool
	onChange func(bool)
}

// NewTerminalFocus 创建终端焦点管理器
func NewTerminalFocus() *TerminalFocus {
	return &TerminalFocus{focused: true}
}

// IsFocused 返回当前焦点状态
func (tf *TerminalFocus) IsFocused() bool {
	tf.mu.RLock()
	defer tf.mu.RUnlock()
	return tf.focused
}

// SetFocused 设置焦点状态
func (tf *TerminalFocus) SetFocused(focused bool) {
	tf.mu.Lock()
	if tf.focused != focused {
		tf.focused = focused
		cb := tf.onChange
		tf.mu.Unlock()
		if cb != nil {
			cb(focused)
		}
		return
	}
	tf.mu.Unlock()
}

// OnChange 设置焦点变更回调
func (tf *TerminalFocus) OnChange(fn func(bool)) { tf.onChange = fn }

// TerminalSize 终端尺寸
// Equivalent of ink/hooks/use-terminal-viewport.ts and useTerminalSize
type TerminalSize struct {
	mu       sync.RWMutex
	width    int
	height   int
	onChange func(width, height int)
}

// NewTerminalSize 创建终端尺寸管理器
func NewTerminalSize(w, h int) *TerminalSize {
	return &TerminalSize{width: w, height: h}
}

// Resize 更新终端尺寸
func (ts *TerminalSize) Resize(w, h int) {
	ts.mu.Lock()
	if ts.width != w || ts.height != h {
		ts.width, ts.height = w, h
		cb := ts.onChange
		ts.mu.Unlock()
		if cb != nil {
			cb(w, h)
		}
		return
	}
	ts.mu.Unlock()
}

// Size 返回当前尺寸
func (ts *TerminalSize) Size() (int, int) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.width, ts.height
}

// OnChange 设置尺寸变更回调
func (ts *TerminalSize) OnChange(fn func(w, h int)) { ts.onChange = fn }

// TerminalTitle 终端标题
// Equivalent of ink/hooks/use-terminal-title.ts
type TerminalTitle struct {
	mu    sync.RWMutex
	title string
}

// NewTerminalTitle 创建终端标题管理器
func NewTerminalTitle() *TerminalTitle { return &TerminalTitle{} }

// SetTitle 设置标题
func (tt *TerminalTitle) SetTitle(t string) {
	tt.mu.Lock()
	tt.title = t
	tt.mu.Unlock()
}

// Title 返回当前标题
func (tt *TerminalTitle) Title() string {
	tt.mu.RLock()
	defer tt.mu.RUnlock()
	return tt.title
}

// Interval 定时器（替代 React setInterval）
// Equivalent of ink/hooks/use-interval.ts
type Interval struct {
	interval time.Duration
	ticker   *time.Ticker
	stopCh   chan struct{}
	running  bool
}

// NewInterval 创建并启动一个定时器
func NewInterval(d time.Duration, fn func()) *Interval {
	i := &Interval{interval: d, stopCh: make(chan struct{})}
	i.ticker = time.NewTicker(d)
	i.running = true
	go func() {
		for {
			select {
			case <-i.ticker.C:
				fn()
			case <-i.stopCh:
				return
			}
		}
	}()
	return i
}

// Stop 停止定时器
func (i *Interval) Stop() {
	if i.running {
		i.running = false
		i.ticker.Stop()
		close(i.stopCh)
	}
}

// AnimationFrame 动画帧 Hook（基于定时器模拟）
// Equivalent of ink/hooks/use-animation-frame.ts
type AnimationFrame struct {
	interval time.Duration
	start    time.Time
	running  bool
	stopCh   chan struct{}
	updateCh chan time.Duration
}

// NewAnimationFrame 创建动画帧管理器
func NewAnimationFrame(interval time.Duration) *AnimationFrame {
	if interval <= 0 {
		interval = 16 * time.Millisecond // ~60fps
	}
	return &AnimationFrame{
		interval: interval,
		start:    time.Now(),
		stopCh:   make(chan struct{}),
		updateCh: make(chan time.Duration, 1),
	}
}

// Start 启动动画帧
func (af *AnimationFrame) Start() {
	if af.running {
		return
	}
	af.running = true
	af.start = time.Now()
	go func() {
		ticker := time.NewTicker(af.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				select {
				case af.updateCh <- time.Since(af.start):
				default:
				}
			case <-af.stopCh:
				return
			}
		}
	}()
}

// Stop 停止动画帧
func (af *AnimationFrame) Stop() {
	if af.running {
		af.running = false
		close(af.stopCh)
	}
}

// Updates 返回动画帧更新通道
func (af *AnimationFrame) Updates() <-chan time.Duration { return af.updateCh }

// TabStatus Tab 键状态
// Equivalent of ink/hooks/use-tab-status.ts
type TabStatus struct {
	mu     sync.RWMutex
	active bool
	label  string
}

// NewTabStatus 创建 Tab 状态管理器
func NewTabStatus() *TabStatus { return &TabStatus{} }

// SetActive 设置激活状态与标签
func (ts *TabStatus) SetActive(active bool, label string) {
	ts.mu.Lock()
	ts.active = active
	ts.label = label
	ts.mu.Unlock()
}

// IsActive 返回是否激活
func (ts *TabStatus) IsActive() bool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.active
}

// Label 返回当前标签
func (ts *TabStatus) Label() string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.label
}

// SearchHighlight 搜索高亮状态
// Equivalent of ink/hooks/use-search-highlight.ts
type SearchHighlight struct {
	mu      sync.RWMutex
	active  bool
	query   string
	results []int
}

// NewSearchHighlight 创建搜索高亮管理器
func NewSearchHighlight() *SearchHighlight { return &SearchHighlight{} }

// Search 执行搜索并更新高亮状态
func (sh *SearchHighlight) Search(query string, results []int) {
	sh.mu.Lock()
	sh.active = query != ""
	sh.query = query
	sh.results = results
	sh.mu.Unlock()
}

// IsActive 返回是否有活跃搜索
func (sh *SearchHighlight) IsActive() bool {
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	return sh.active
}

// Query 返回当前搜索词
func (sh *SearchHighlight) Query() string {
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	return sh.query
}

// Results 返回搜索结果行号列表
func (sh *SearchHighlight) Results() []int {
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	r := make([]int, len(sh.results))
	copy(r, sh.results)
	return r
}
