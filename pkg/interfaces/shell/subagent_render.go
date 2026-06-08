package shell

import (
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/goclaude/pkg/application"
)

// 让 *REPL 同时也是 subagent 事件监听器。
var _ application.SubagentEventListener = (*REPL)(nil)

// ---- Subagent 面板渲染 ----
//
// 现代化面板样式，使用双线边框 + 渐变色调区分各层级：
//
// 单个 subagent：
//
//	  ┏━ coder · sonnet ━━━━━━━━━━━━━━━━━
//	  ┃  Team Setup
//	  ┃  Code Generation
//	  ┗━ ✓ done · 3.2s · 8 steps ━━━━━━━━
//	     ⎿ Generated src/main.go
//
// 多个 subagent 并发（泳道模式）：
//
//	  ┏━ explore#a1b2 · haiku ━━━━━━━━━━━━
//	  ┏━ coder#c3d4 · sonnet ━━━━━━━━━━━━
//	  ┃  explore#a1b2  Exploration
//	  ┃  coder#c3d4  Code Generation
//	  ┗━ explore#a1b2  ✓ done · 1.2s
//	  ┗━ coder#c3d4  ✓ done · 3.0s
//

const subagentIndent = "    "

// HandleSubagentEvent 把 subagent 生命周期事件渲染为进度面板。
func (r *REPL) HandleSubagentEvent(ev application.SubagentEvent) {
	if r == nil {
		return
	}
	r.subagentMu.Lock()
	defer r.subagentMu.Unlock()
	if r.subagentTrackers == nil {
		r.subagentTrackers = make(map[string]*subagentTracker)
	}
	key := ev.AgentID

	switch ev.Phase {
	case application.SubagentPhaseStart:
		r.subagentTrackers[key] = newSubagentTracker(ev.AgentType, ev.Model)
		r.writeOut(r.renderPanelHeader(ev) + "\r\n")

	case application.SubagentPhaseProgress:
		t := r.subagentTrackers[key]
		if t == nil {
			t = newSubagentTracker(ev.AgentType, ev.Model)
			r.subagentTrackers[key] = t
		}
		if phase, isFirst := t.observe(ev.LastTool); isFirst && phase != "" {
			concurrent := len(r.subagentTrackers) > 1
			toolDetail := t.lastToolDetail
			step := r.colorize(subagentIndent+"┃  ", colorBorder) +
				r.laneLabel(ev, concurrent) +
				r.colorize(phase, colorSubtle)
			if toolDetail != "" {
				step += r.colorize("  · "+toolDetail, colorMuted)
			}
			r.writeOut(step + "\r\n")
		}
		if ev.LastTool != "" && ev.LastToolDetail != "" {
			t.lastToolDetail = ev.LastToolDetail
		} else if ev.LastTool != "" {
			t.lastToolDetail = ""
		}

	case application.SubagentPhaseFinish:
		concurrent := len(r.subagentTrackers) > 1
		delete(r.subagentTrackers, key)
		r.writeOut(r.renderPanelFooter(ev, concurrent) + "\r\n")

		// 结果行
		if ev.Status != application.SubagentStatusError && ev.ResultPreview != "" {
			result := r.colorize(subagentIndent+"   "+r.gl().result, colorSuccess) +
				r.laneLabel(ev, concurrent) +
				r.colorize(r.fitResult(ev.ResultPreview, 100, 12), colorSubtle)
			r.writeOut(result + "\r\n")
		}
	}
}

// renderPanelHeader 标题行：`┏━ <agent>[#tag] · <model> ━━━`
func (r *REPL) renderPanelHeader(ev application.SubagentEvent) string {
	name := r.colorize(ev.AgentType, colorAgentName)
	if tag := agentTag(ev.AgentID); tag != "" {
		name += r.colorize(tag, colorMuted)
	}

	header := r.colorize(subagentIndent+"┏━ ", colorBorder) +
		name +
		r.colorize(" · "+formatModel(ev.Model), colorSubtle)

	// 补充尾部装饰线
	lineLen := 40
	header += " " + r.colorize(strings.Repeat("━", lineLen), colorBorder)
	return header
}

// renderPanelFooter 收尾行：成功 `┗━ ✓ done · 3.2s · 8 steps`；失败 `┗━ ✗ failed · <err>`
func (r *REPL) renderPanelFooter(ev application.SubagentEvent, concurrent bool) string {
	success := ev.Status != application.SubagentStatusError

	var statusIcon, statusColor, statusWord string
	if success {
		statusIcon = "✓"
		statusColor = colorSuccess
		statusWord = "done"
	} else {
		statusIcon = "✗"
		statusColor = colorError
		statusWord = "failed"
	}

	line := r.colorize(subagentIndent+"┗━ ", statusColor) +
		r.laneLabel(ev, concurrent) +
		r.colorize(statusIcon+" "+statusWord, statusColor)

	// 元信息
	parts := []string{}
	if ev.Elapsed > 0 {
		parts = append(parts, cleanElapsed(ev.Elapsed))
	}
	if ev.Turns > 0 {
		parts = append(parts, fmt.Sprintf("%d steps", ev.Turns))
	}
	if !success && ev.ErrorMessage != "" {
		parts = append(parts, r.fitResult(ev.ErrorMessage, 60, 20))
	}
	if len(parts) > 0 {
		line += r.colorize(" · "+strings.Join(parts, " · "), colorElapsed)
	}

	// 尾部装饰线
	line += " " + r.colorize(strings.Repeat("━", 20), statusColor)
	return line
}

// laneLabel 并发模式下返回归属标签段，单 agent 时返回空串。
func (r *REPL) laneLabel(ev application.SubagentEvent, concurrent bool) string {
	if !concurrent {
		return ""
	}
	label := ev.AgentType + agentTag(ev.AgentID)
	return r.colorize(label, colorInfo) + "  "
}

func formatModel(m string) string {
	if m == "" {
		return "inherit"
	}
	return m
}

// cleanElapsed 紧凑耗时格式。
func cleanElapsed(d time.Duration) string {
	if d <= 0 {
		return "0ms"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%.1fm", d.Minutes())
}

// agentTag 由 agentID 末尾取一小段，渲染成 #xxxx 短标签。
func agentTag(agentID string) string {
	if agentID == "" {
		return ""
	}
	tail := agentID
	if i := strings.LastIndexByte(agentID, '-'); i >= 0 && i+1 < len(agentID) {
		tail = agentID[i+1:]
	}
	if len(tail) > 4 {
		tail = tail[len(tail)-4:]
	}
	return "#" + tail
}
