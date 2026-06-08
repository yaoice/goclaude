// Package hook 提供 goclaude 的 hook 子系统（最小核心）
//
// 与 src `utils/hooks.ts` 行为对齐。
//
// 对齐 src/utils/hooks 的核心事件：
//   - SessionStart / SessionEnd
//   - SubagentStart / SubagentStop
//   - PreToolUse  / PostToolUse
//   - UserPromptSubmit
//
// 当前实现仅覆盖 SubagentStart/SubagentStop（M6 要求），其余事件类型已预留。
// 设计要点：
//   - 注册式：上层（CLI/SDK）通过 Register(event, handler) 订阅
//   - 同步执行：handler 顺序运行，可累积返回 AdditionalContext 注入到对话
//   - 出错隔离：单个 handler panic / 返回错误不影响其它 handler
//   - 线程安全：可在 subagent 并发执行时被读取
package hook

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// Event 钩子事件类型
type Event string

const (
	EventSessionStart     Event = "SessionStart"
	EventSessionEnd       Event = "SessionEnd"
	EventSubagentStart    Event = "SubagentStart"
	EventSubagentStop     Event = "SubagentStop"
	EventPreToolUse       Event = "PreToolUse"
	EventPostToolUse      Event = "PostToolUse"
	EventUserPromptSubmit Event = "UserPromptSubmit"
)

// Context Hook 执行时传入的上下文
type Context struct {
	// SessionID 当前会话 ID
	SessionID string
	// AgentID 触发事件的 agent；SubagentStart/Stop 时必填
	AgentID string
	// AgentType subagent 类型名
	AgentType string
	// ToolName 触发 PreToolUse/PostToolUse 时的工具名
	ToolName string
	// Extra 任意业务字段；handler 可以读取
	Extra map[string]interface{}
}

// Result Hook 执行结果
type Result struct {
	// AdditionalContexts 每条会作为独立 user 消息追加到对话历史（subagent.PreloadedSkills 的姊妹机制）
	AdditionalContexts []string
	// Block 为 true 时表示阻止后续处理（仅 PreToolUse 等可被中断的事件有效）
	Block bool
	// BlockReason 当 Block=true 时给上层显示的理由
	BlockReason string
}

// Handler hook 处理函数
//
// 返回 nil 表示无任何变更；返回 Result.Block=true 表示该 hook 拒绝继续执行。
type Handler func(ctx context.Context, hookCtx *Context) (*Result, error)

// Registry hook 注册表（线程安全）
type Registry struct {
	mu       sync.RWMutex
	handlers map[Event][]Handler
	logger   *slog.Logger
}

// NewRegistry 创建 hook 注册表
func NewRegistry(logger *slog.Logger) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	return &Registry{
		handlers: make(map[Event][]Handler),
		logger:   logger,
	}
}

// Register 注册一个 hook handler
func (r *Registry) Register(event Event, h Handler) {
	if h == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[event] = append(r.handlers[event], h)
}

// Run 顺序执行某事件的所有 handler，聚合 AdditionalContexts；
// 任意 handler 返回 Block=true 时立即中断后续 handler 并返回该 Result。
//
// handler 内部 panic 不会传播：会被 recover 后转成日志，下一个 handler 继续。
func (r *Registry) Run(ctx context.Context, event Event, hookCtx *Context) *Result {
	r.mu.RLock()
	handlers := append([]Handler(nil), r.handlers[event]...)
	logger := r.logger
	r.mu.RUnlock()
	if len(handlers) == 0 {
		return &Result{}
	}
	merged := &Result{}
	for i, h := range handlers {
		res := safeRun(ctx, h, hookCtx, event, i, logger)
		if res == nil {
			continue
		}
		if len(res.AdditionalContexts) > 0 {
			merged.AdditionalContexts = append(merged.AdditionalContexts, res.AdditionalContexts...)
		}
		if res.Block {
			merged.Block = true
			merged.BlockReason = res.BlockReason
			return merged
		}
	}
	return merged
}

// safeRun 包一层 recover + error 日志，确保单个 handler 异常不影响其它
func safeRun(ctx context.Context, h Handler, hookCtx *Context, event Event, idx int, logger *slog.Logger) (res *Result) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("hook panic recovered",
				"event", event,
				"index", idx,
				"agent", hookCtx.AgentType,
				"panic", fmt.Sprint(r),
			)
			res = nil
		}
	}()
	out, err := h(ctx, hookCtx)
	if err != nil {
		logger.Warn("hook handler error",
			"event", event,
			"index", idx,
			"agent", hookCtx.AgentType,
			"error", err,
		)
		return nil
	}
	return out
}

// Count 返回某事件的 handler 数量（测试 / debug 用）
func (r *Registry) Count(event Event) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.handlers[event])
}

// Clear 清空（测试用）
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers = make(map[Event][]Handler)
}
