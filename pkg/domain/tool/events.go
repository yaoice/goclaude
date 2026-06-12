package tool

import "time"

// ToolPhase 工具事件阶段：start（即将执行）/ finish（执行结束）。
type ToolPhase string

const (
	// ToolPhaseStart 工具即将开始执行；finish 事件之前一定先有 start。
	ToolPhaseStart ToolPhase = "start"
	// ToolPhaseFinish 工具执行完成（无论成功/失败）。
	ToolPhaseFinish ToolPhase = "finish"
)

// ToolStatus 工具最终状态：success / error。
type ToolStatus string

const (
	ToolStatusSuccess ToolStatus = "success"
	ToolStatusError   ToolStatus = "error"
)

// ToolEvent 工具执行的结构化事件，供 UI 渲染或日志整理消费。
//
// 设计要点：
//   - 取代 slog.Info("执行工具", "tool", ..., "id", ...) 直接打印到 stderr 造成的乱序输出
//   - Listener 负责将事件渲染为 CLI 友好格式（如 `⏵ tool` / `✔ tool 12ms`）
//   - 多个工具并发执行时事件可能交错；每个 ToolUseID 上保证 Start→Finish 严格成对
type ToolEvent struct {
	Phase     ToolPhase
	ToolName  string
	ToolUseID string
	Input     Input
	// InputSummary 工具参数的可读单行摘要（由 Executor 在分发 start 事件前填充）；
	// 例如 bash → "ls -la src/"，file_read → "src/main.go"，grep → "\"func New\" in src/"。
	// 空串表示无摘要（UI 应回退到 "..." 占位）。
	InputSummary string
	Status       ToolStatus    // 仅 finish 阶段有效
	Elapsed      time.Duration // 仅 finish 阶段有效
	ResultLen    int           // finish 成功时携带结果字符串长度
	ErrorMessage string        // finish 失败时的错误信息
}

// ToolEventListener 工具事件订阅者。
//
// 实现需要保证非阻塞：内部如果有 IO 应自行排队/异步处理。Executor 在持有 finish
// 事件时已经返回，不会阻塞主循环。
type ToolEventListener interface {
	HandleToolEvent(ev ToolEvent)
}

// ToolEventListenerFunc 把普通函数适配为 ToolEventListener。
type ToolEventListenerFunc func(ev ToolEvent)

// HandleToolEvent 实现 ToolEventListener 接口。
func (f ToolEventListenerFunc) HandleToolEvent(ev ToolEvent) { f(ev) }
