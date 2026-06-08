package hooks

import (
	"fmt"
	"sync"
	"time"
)

// ============================================================
// useElapsedTime — 已用时间计时器
// 对齐 src/hooks/useElapsedTime.ts
//
// 核心逻辑：返回格式化的已用时间（startTime 至今），
// 通过 interval 定时更新，支持暂停和终止冻结。
// ============================================================

// ElapsedTime 已用时间计时器
type ElapsedTime struct {
	startTime time.Time
	pausedMs  time.Duration
	endTime   *time.Time
	stopCh    chan struct{}
	mu        sync.RWMutex
}

// NewElapsedTime 创建计时器
func NewElapsedTime() *ElapsedTime {
	return &ElapsedTime{
		stopCh: make(chan struct{}),
	}
}

// Start 启动计时
// updateFn: 每次更新时调用的回调，传入格式化后的时长字符串
func (e *ElapsedTime) Start(startTime time.Time, interval time.Duration, updateFn func(duration string)) {
	e.mu.Lock()
	e.startTime = startTime
	e.mu.Unlock()

	if interval <= 0 {
		interval = time.Second
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if updateFn != nil {
					updateFn(formatDuration(e.duration()))
				}
			case <-e.stopCh:
				return
			}
		}
	}()
}

// SetPaused 设置暂停时长 (ms)
func (e *ElapsedTime) SetPaused(pausedMs time.Duration) {
	e.mu.Lock()
	e.pausedMs = pausedMs
	e.mu.Unlock()
}

// SetEndTime 设置终止时间（冻结用）
func (e *ElapsedTime) SetEndTime(t time.Time) {
	e.mu.Lock()
	e.endTime = &t
	e.mu.Unlock()
}

// Duration 返回已用时间
func (e *ElapsedTime) Duration() time.Duration {
	return e.duration()
}

func (e *ElapsedTime) duration() time.Duration {
	e.mu.RLock()
	defer e.mu.RUnlock()
	now := time.Now()
	if e.endTime != nil {
		now = *e.endTime
	}
	d := now.Sub(e.startTime) - e.pausedMs
	if d < 0 {
		return 0
	}
	return d
}

// Stop 停止计时
func (e *ElapsedTime) Stop() {
	select {
	case <-e.stopCh:
	default:
		close(e.stopCh)
	}
}

// formatDuration 格式化时长为 h/m/s 字符串（对齐 formatDuration）
func formatDuration(d time.Duration) string {
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	if hours > 0 {
		if minutes > 0 {
			return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
		}
		return fmt.Sprintf("%dh %ds", hours, seconds)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

// ============================================================
// useDoublePress — 双击检测（时间窗口内两次按键）
// 对齐 src/hooks/useDoublePress.ts
//
// 核心逻辑：在超时窗口内（默认800ms）检测两次相同按键。
// 首次按键设置 pending 状态和定时器，第二次在窗口内按键触发回调。
// ============================================================

// DefaultDoublePressTimeout 默认双击超时
const DefaultDoublePressTimeout = 800 * time.Millisecond

// DoublePress 双击检测器
type DoublePress struct {
	timeout        time.Duration
	lastPress      time.Time
	timer          *time.Timer
	pending        bool
	pendingChanged func(bool)
}

// NewDoublePress 创建双击检测器
func NewDoublePress(timeout time.Duration) *DoublePress {
	if timeout <= 0 {
		timeout = DefaultDoublePressTimeout
	}
	return &DoublePress{
		timeout: timeout,
	}
}

// OnPendingChanged 设置 pending 状态变更回调（对齐 setPending）
func (d *DoublePress) OnPendingChanged(fn func(pending bool)) {
	d.pendingChanged = fn
}

// Press 处理一次按键，返回是否为双击
// onFirstPress: 首次按下时的回调（可选）
// onDoublePress: 双击时的回调
func (d *DoublePress) Press(onFirstPress, onDoublePress func()) bool {
	now := time.Now()

	if d.timer != nil && now.Sub(d.lastPress) <= d.timeout {
		// 双击确认
		d.resetTimer()
		d.pending = false
		if d.pendingChanged != nil {
			d.pendingChanged(false)
		}
		d.lastPress = now
		if onDoublePress != nil {
			onDoublePress()
		}
		return true
	}

	// 首次点击
	if onFirstPress != nil {
		onFirstPress()
	}

	d.pending = true
	if d.pendingChanged != nil {
		d.pendingChanged(true)
	}

	d.resetTimer()
	d.timer = time.AfterFunc(d.timeout, func() {
		d.pending = false
		d.timer = nil
		if d.pendingChanged != nil {
			d.pendingChanged(false)
		}
	})

	d.lastPress = now
	return false
}

func (d *DoublePress) resetTimer() {
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
}

// Reset 重置检测器状态
func (d *DoublePress) Reset() {
	d.resetTimer()
	d.pending = false
	d.lastPress = time.Time{}
}

// IsPending 是否处于 pending（等待双击确认）
func (d *DoublePress) IsPending() bool {
	return d.pending
}

// ============================================================
// useTimeout — 超时控制（可取消/重置）
// 对齐 src/hooks/useTimeout.ts
// ============================================================

// Timeout 可取消的超时控制器
type Timeout struct {
	timer   *time.Timer
	d       time.Duration
	mu      sync.Mutex
	active  bool
	elapsed time.Duration
}

// NewTimeout 创建超时控制器
func NewTimeout(duration time.Duration, callback func()) *Timeout {
	if callback == nil {
		callback = func() {}
	}
	t := &Timeout{d: duration}
	t.timer = time.AfterFunc(duration, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.active = false
		t.elapsed = duration
		callback()
	})
	t.active = true
	return t
}

// Cancel 取消超时
func (t *Timeout) Cancel() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	t.active = false
}

// Reset 重置超时（可指定新时长）
func (t *Timeout) Reset(duration time.Duration, callback func()) {
	if duration <= 0 {
		duration = t.d
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer != nil {
		t.timer.Stop()
	}
	t.timer = time.AfterFunc(duration, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.active = false
		t.elapsed = duration
		if callback != nil {
			callback()
		}
	})
	t.active = true
}

// IsActive 是否活跃
func (t *Timeout) IsActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.active
}

// Elapsed 已运行时长
func (t *Timeout) Elapsed() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.elapsed
}

// ============================================================
// useBlink — 同步闪烁动画
// 对齐 src/hooks/useBlink.ts
//
// 核心逻辑：基于统一时钟的闪烁效果。
// 所有 Blink 实例共享同一时间基准，自动同步。
// 当 enabled=false 或 focused=false 时始终可见。
// ============================================================

// DefaultBlinkInterval 默认闪烁间隔
const DefaultBlinkInterval = 600 * time.Millisecond

// Blink 同步闪烁控制器
type Blink struct {
	enabled   bool
	focused   bool
	interval  time.Duration
	startTime time.Time
	stopCh    chan struct{}
	isRunning bool
}

// NewBlink 创建闪烁控制器
func NewBlink(interval time.Duration) *Blink {
	if interval <= 0 {
		interval = DefaultBlinkInterval
	}
	return &Blink{
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start 启动闪烁
func (b *Blink) Start() {
	if b.isRunning {
		return
	}
	b.isRunning = true
	b.startTime = time.Now()
}

// Stop 停止闪烁
func (b *Blink) Stop() {
	if b.isRunning {
		b.isRunning = false
		select {
		case <-b.stopCh:
		default:
			close(b.stopCh)
		}
	}
}

// SetEnabled 设置启用状态
func (b *Blink) SetEnabled(enabled bool) {
	b.enabled = enabled
}

// SetFocused 设置焦点状态
func (b *Blink) SetFocused(focused bool) {
	b.focused = focused
}

// IsVisible 当前是否可见（在闪烁周期中）
func (b *Blink) IsVisible() bool {
	if !b.enabled || !b.focused {
		return true
	}
	elapsed := time.Since(b.startTime)
	cycles := elapsed.Nanoseconds() / b.interval.Nanoseconds()
	return cycles%2 == 0
}
