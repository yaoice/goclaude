package shell

import (
	"fmt"
	"sync"
	"time"

	"github.com/yaoice/goclaude/pkg/domain/tool"
)

// 让 *REPL 可以直接作为 tool.ToolEventListener 注入到 Executor，
// 把"执行工具"日志收编为美观的实时执行明细。
var _ tool.ToolEventListener = (*REPL)(nil)

// toolEventBuffer 缓冲尚未在流中出现的 tool_result 元数据。
//
// REPL 流式渲染使用 ContentBlock 中的 ToolUseID 配对 tool_use / tool_result；
// Executor 早于 ContentBlock 提供 finish 信息（耗时/状态），先存起来等流中
// tool_result 到达时一并显示。
type toolEventBuffer struct {
	mu       sync.Mutex
	pending  map[string]tool.ToolEvent // toolUseID -> finish 事件
	progress map[string]time.Time      // toolUseID -> 启动时间
	stepSeq  int                       // 全局步骤序号（递增）
}

func newToolEventBuffer() *toolEventBuffer {
	return &toolEventBuffer{
		pending:  make(map[string]tool.ToolEvent),
		progress: make(map[string]time.Time),
	}
}

func (b *toolEventBuffer) recordStart(ev tool.ToolEvent) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.progress[ev.ToolUseID]; exists {
		return false
	}
	b.progress[ev.ToolUseID] = time.Now()
	b.stepSeq++
	return true
}

func (b *toolEventBuffer) currentStep() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stepSeq
}

func (b *toolEventBuffer) takeFinish(toolUseID string) (tool.ToolEvent, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ev, ok := b.pending[toolUseID]
	if ok {
		delete(b.pending, toolUseID)
		delete(b.progress, toolUseID)
	}
	return ev, ok
}

func (b *toolEventBuffer) storeFinish(ev tool.ToolEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending[ev.ToolUseID] = ev
}

// HandleToolEvent 接收 Executor 派发的工具事件，渲染为实时执行明细。
//
// 新渲染风格：
//   - start：显示步骤序号 + 工具名 + 运行状态指示器
//   - finish：缓存元数据；流式渲染 tool_result 时统一输出完整状态行
func (r *REPL) HandleToolEvent(ev tool.ToolEvent) {
	if r == nil {
		return
	}
	if r.toolEvents == nil {
		r.toolEvents = newToolEventBuffer()
	}
	switch ev.Phase {
	case tool.ToolPhaseStart:
		r.toolEvents.recordStart(ev)
	case tool.ToolPhaseFinish:
		r.toolEvents.storeFinish(ev)
	}
}

// consumeToolFinish 在流式渲染中取出对应 ToolUseID 的 finish 元数据。
//
// 返回美化的状态标签 + 耗时字符串，调用方直接拼到结果行尾。
func (r *REPL) consumeToolFinish(toolUseID string) (string, bool) {
	if r == nil || r.toolEvents == nil || toolUseID == "" {
		return "", false
	}
	ev, ok := r.toolEvents.takeFinish(toolUseID)
	if !ok {
		return "", false
	}
	var suffix string
	if ev.Elapsed > 0 {
		suffix = "  " + r.colorize(formatElapsedCompact(ev.Elapsed), colorElapsed)
	}
	if ev.Status == tool.ToolStatusError && ev.ErrorMessage != "" {
		suffix += "  " + r.colorize(summarizeOneLine(ev.ErrorMessage, 50), colorErrOutput)
	}
	return suffix, true
}

// formatElapsed 把耗时格式化为定长 8 列字符串：≤999ms 用 ms，>=1s 用 s。
func formatElapsed(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf(" %5dµs", d.Microseconds())
	}
	if d < time.Second {
		return fmt.Sprintf(" %5dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf(" %5.1fs ", d.Seconds())
	}
	return fmt.Sprintf(" %5.1fm ", d.Minutes())
}
