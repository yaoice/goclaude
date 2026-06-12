// Package hook extensions — 扩展 hook 事件系统，对齐 src/utils/hooks/hookEvents.ts
//
// 提供 Go 版本的 hook 事件广播系统：
//   - HookExecutionEvent 事件类型（started / progress / response）
//   - EventHandler 注册与分发
//   - 待处理事件缓冲（handler 未注册前的缓存）
//   - 总是发送事件白名单（SessionStart, Setup）
package hook

import (
	"sync"
	"time"
)

// HookExecutionEvent 表示一次 hook 执行事件
type HookExecutionEvent struct {
	Type      string `json:"type"` // "started" / "progress" / "response"
	HookID    string `json:"hookId"`
	HookName  string `json:"hookName"`
	HookEvent string `json:"hookEvent"`
	Stdout    string `json:"stdout,omitempty"`
	Stderr    string `json:"stderr,omitempty"`
	Output    string `json:"output,omitempty"`
	ExitCode  *int   `json:"exitCode,omitempty"`
	Outcome   string `json:"outcome,omitempty"` // "success" / "error" / "cancelled"
}

// ExecutionEventHandler hook 执行事件的处理函数
type ExecutionEventHandler func(event *HookExecutionEvent)

// alwaysEmittedEvents 始终发送的事件（与 TypeScript ALWAYS_EMITTED_HOOK_EVENTS 对齐）
var alwaysEmittedEvents = map[string]bool{
	"SessionStart": true,
	"Setup":        true,
}

// executionEventBus hook 执行事件总线（单例）
type executionEventBus struct {
	mu         sync.RWMutex
	handler    ExecutionEventHandler
	pending    []*HookExecutionEvent
	allEnabled bool
}

const maxPendingEvents = 100

var eventBus = &executionEventBus{}

// RegisterExecutionEventHandler 注册 hook 执行事件处理器
// 若已有待处理事件，立即回放
func RegisterExecutionEventHandler(handler ExecutionEventHandler) {
	eventBus.mu.Lock()
	defer eventBus.mu.Unlock()
	eventBus.handler = handler
	if handler != nil && len(eventBus.pending) > 0 {
		for _, ev := range eventBus.pending {
			handler(ev)
		}
		eventBus.pending = nil
	}
}

// SetAllHookEventsEnabled 启用全部 hook 事件发送（默认仅发送 SessionStart 和 Setup）
func SetAllHookEventsEnabled(enabled bool) {
	eventBus.mu.Lock()
	defer eventBus.mu.Unlock()
	eventBus.allEnabled = enabled
}

// ClearEventState 清空事件状态（测试用）
func ClearEventState() {
	eventBus.mu.Lock()
	defer eventBus.mu.Unlock()
	eventBus.handler = nil
	eventBus.pending = nil
	eventBus.allEnabled = false
}

func shouldEmit(hookEvent string) bool {
	if alwaysEmittedEvents[hookEvent] {
		return true
	}
	eventBus.mu.RLock()
	defer eventBus.mu.RUnlock()
	return eventBus.allEnabled
}

func emit(ev *HookExecutionEvent) {
	eventBus.mu.Lock()
	defer eventBus.mu.Unlock()
	if eventBus.handler != nil {
		eventBus.handler(ev)
	} else {
		eventBus.pending = append(eventBus.pending, ev)
		if len(eventBus.pending) > maxPendingEvents {
			eventBus.pending = eventBus.pending[1:]
		}
	}
}

// EmitHookStarted 发送 hook 启动事件
func EmitHookStarted(hookID, hookName, hookEvent string) {
	if !shouldEmit(hookEvent) {
		return
	}
	emit(&HookExecutionEvent{
		Type:      "started",
		HookID:    hookID,
		HookName:  hookName,
		HookEvent: hookEvent,
	})
}

// EmitHookProgress 发送 hook 进度事件
func EmitHookProgress(hookID, hookName, hookEvent, stdout, stderr, output string) {
	if !shouldEmit(hookEvent) {
		return
	}
	emit(&HookExecutionEvent{
		Type:      "progress",
		HookID:    hookID,
		HookName:  hookName,
		HookEvent: hookEvent,
		Stdout:    stdout,
		Stderr:    stderr,
		Output:    output,
	})
}

// EmitHookResponse 发送 hook 响应事件
func EmitHookResponse(hookID, hookName, hookEvent, stdout, stderr, output string, exitCode *int, outcome string) {
	if !shouldEmit(hookEvent) {
		return
	}
	emit(&HookExecutionEvent{
		Type:      "response",
		HookID:    hookID,
		HookName:  hookName,
		HookEvent: hookEvent,
		Stdout:    stdout,
		Stderr:    stderr,
		Output:    output,
		ExitCode:  exitCode,
		Outcome:   outcome,
	})
}

// StartHookProgressInterval 启动定时进度发送，返回停止函数
// 对齐 src/utils/hooks/hookEvents.ts:startHookProgressInterval
func StartHookProgressInterval(
	hookID, hookName, hookEvent string,
	getOutput func() (stdout, stderr, output string),
	intervalMs time.Duration,
) func() {
	if !shouldEmit(hookEvent) {
		return func() {}
	}
	if intervalMs <= 0 {
		intervalMs = 1000 * time.Millisecond
	}

	ticker := time.NewTicker(intervalMs)
	lastOutput := ""

	go func() {
		for range ticker.C {
			stdout, stderr, output := getOutput()
			if output == lastOutput {
				continue
			}
			lastOutput = output
			EmitHookProgress(hookID, hookName, hookEvent, stdout, stderr, output)
		}
	}()

	return ticker.Stop
}
