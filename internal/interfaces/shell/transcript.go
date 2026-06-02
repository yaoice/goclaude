package shell

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/goclaude/internal/domain/query"
)

// ShowTranscript 进入 transcript 全屏只读模式
//
// 行为：
//   - 清屏并按时间顺序渲染所有 messages（user / assistant / tool_use / tool_result）
//   - 底部固定一行提示，等待用户按 q / Esc / Ctrl-O 退出
//   - 不接受其它输入，纯只读视图
//
// 设计取舍：
//   - 不实现自带滚动；依赖终端原生 scroll-back
//   - 渲染走 StreamFormatter（与流式回复一致）
//   - 助手块用与流式相同的 "  │ " 边条；user 块用 "» " 前缀
func (r *REPL) ShowTranscript() {
	// 清屏 + 移到 (1,1)
	r.writeOut("\x1b[2J\x1b[H")
	r.writeOut(r.colorize("── Transcript ──────────────────────────────\r\n", colorCyan))
	r.writeOut(r.colorize(fmt.Sprintf("provider=%s  model=%s  cwd=%s\r\n",
		r.Provider, r.Model, r.WorkDir), colorDim))
	r.writeOut("\r\n")

	r.mu.Lock()
	msgs := append([]query.Message(nil), r.messages...)
	r.mu.Unlock()

	if len(msgs) == 0 {
		r.writeOut(r.colorize("（暂无对话）\r\n", colorDim))
	}

	for i, m := range msgs {
		r.renderTranscriptMessage(i, m)
	}

	r.writeOut("\r\n")
	r.writeOut(r.colorize(
		"── 按 q / Esc / Ctrl-O 退出 transcript ──\r\n", colorDim))

	// 等待退出键
	for {
		b, err := r.Term.ReadByte()
		if err != nil {
			return
		}
		switch b {
		case 'q', 'Q', 0x1b /*Esc*/, 0x0f /*Ctrl-O*/, 0x03 /*Ctrl-C*/ :
			// 退出：清屏 + 重绘 banner，让 REPL 主循环继续
			r.writeOut("\x1b[2J\x1b[H")
			r.printBanner()
			return
		}
	}
}

// renderTranscriptMessage 渲染单条消息
func (r *REPL) renderTranscriptMessage(idx int, m query.Message) {
	switch m.Role {
	case query.RoleUser:
		// 用户消息：可能含 text + tool_result blocks
		text := concatTextBlocks(m.Content)
		if text != "" {
			r.writeOut(r.colorize("» ", colorAccent) + r.colorize("You", colorDim) + "\r\n")
			r.writeOut(indentMultiline(text, "  ") + "\r\n\r\n")
		}
		// 工具结果（如果是 user 消息携带的）
		for _, b := range m.Content {
			if b.Type == query.ContentTypeToolResult {
				connColor := colorGreen
				fallback := "done"
				if b.IsError {
					connColor = colorRed
					fallback = "failed"
				}
				prefix := r.colorize("  "+r.gl().result, connColor)
				summary := r.fitResult(b.Text, 100, 8)
				if summary == "" {
					summary = fallback
				} else if b.IsError && !r.useColor {
					summary = fallback + ": " + summary
				}
				prefix += r.colorize(summary, colorDim)
				r.writeOut(prefix + "\r\n")
			}
		}
	case query.RoleAssistant:
		r.writeOut(r.colorize("● ", colorGreen) + r.colorize("Claude", colorDim) + "\r\n")
		// 用 StreamFormatter 渲染助手 markdown，保持与流式一致
		formatter := NewStreamFormatter("  ", "│ ")
		var buf bytes.Buffer
		for _, b := range m.Content {
			switch b.Type {
			case query.ContentTypeText:
				if b.Text != "" {
					formatter.Write(&buf, b.Text+"\n")
				}
			case query.ContentTypeThinking:
				// 折叠 thinking
				preview := summarizeOneLine(b.Thinking, 80)
				r.writeOut(r.colorize("  "+r.gl().minor+"thinking: "+preview, colorDim) + "\r\n")
			case query.ContentTypeToolUse:
				r.writeOut(r.colorize(
					fmt.Sprintf("  %s%s", r.gl().toolCall, b.ToolName), colorAccent) + "\r\n")
			}
		}
		formatter.Flush(&buf)
		_, _ = os.Stdout.Write(buf.Bytes())
		r.writeOut("\r\n")
	}
}

// concatTextBlocks 把 content blocks 中的 text 字段串联
func concatTextBlocks(content []query.ContentBlock) string {
	var sb strings.Builder
	for _, b := range content {
		if b.Type == query.ContentTypeText && b.Text != "" {
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

// indentMultiline 给多行文本每行加前缀
func indentMultiline(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\r\n")
}
