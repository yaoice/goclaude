package shell

// streaming_code.go —— 代码生成实时流式输出支持。
//
// 当模型通过 file_write/write_to_file 工具生成代码时，JSON 参数以 partial delta
// 形式逐步流入。本文件实现对"content"字段中代码文本的实时终端渲染，让用户
// 能即时看到正在被写入的代码，而非等工具调用完成后才看到一个静态的"done"。
//
// 实现策略：
//   - 使用简单的 JSON 状态机追踪当前是否处于 "content": "..." 值段
//   - 一旦进入 content 值字符串，后续 delta 文本直接流式输出到终端
//   - 输出使用缩进 + 竖线边条，与普通助手文本区分
//   - 当 ContentBlockStop 到达时自动结束流式输出模式

import (
	"strings"
)

// streamingState 跟踪文件写入工具的流式输出状态
type streamingState struct {
	// inContentValue 当前 partial JSON 解析已经进入 "content" 字段值
	inContentValue bool
	// headerPrinted 已经打印过流式输出头部（避免重复）
	headerPrinted bool
	// lineBuffer 行缓冲：收集 partial 直到遇到换行才输出
	lineBuffer strings.Builder
	// lineCount 已输出的行数
	lineCount int
	// maxLines 最大输出行数（超出后折叠）；0 = 不限制
	maxLines int
	// truncated 超出行数后标记
	truncated bool
}

// isFileWriteTool 判断工具名是否为文件写入类工具
func isFileWriteTool(name string) bool {
	switch strings.ToLower(name) {
	case "write", "file_write", "write_file", "write_to_file", "create":
		return true
	}
	return false
}

// streamFileWriteContent 处理文件写入工具的 partial JSON delta，实时输出代码内容。
//
// JSON 结构通常为：{"path": "...", "content": "...code here..."}
// 我们需要检测何时进入 "content" 字段的值，然后实时输出文本。
//
// 参数：
//   - toolUseID: 工具调用 ID
//   - partial: 到目前为止累积的完整 partial JSON
//   - delta: 最新的增量 JSON 片段
func (r *REPL) streamFileWriteContent(toolUseID, partial, delta string) {
	if toolUseID == "" || delta == "" {
		return
	}

	// 懒初始化 streaming state
	if r.streamStates == nil {
		r.streamStates = make(map[string]*streamingState)
	}
	state, exists := r.streamStates[toolUseID]
	if !exists {
		state = &streamingState{
			maxLines: 50, // 默认最多流式显示 50 行
		}
		r.streamStates[toolUseID] = state
	}

	if state.truncated {
		return
	}

	// 简化的 content 字段检测策略：
	// 扫描累积的 partial JSON，检测是否已经到达 "content" 键后的值区域
	if !state.inContentValue {
		// 检查累积的 partial 中是否包含 "content": " 或 "content":"
		contentKeyIdx := findContentValueStart(partial)
		if contentKeyIdx >= 0 {
			state.inContentValue = true
			// 简化处理：从现在开始的 delta 直接输出
			r.flushStreamLine(state, extractContentDelta(partial[contentKeyIdx:], delta))
			return
		}
		return
	}

	// 已经在 content 值中，直接输出 delta
	// 需要处理转义字符
	unescaped := unescapeJSONString(delta)
	r.flushStreamLine(state, unescaped)
}

// flushStreamLine 将文本输出到终端，按行缓冲
func (r *REPL) flushStreamLine(state *streamingState, text string) {
	if text == "" || state.truncated {
		return
	}

	for _, ch := range text {
		if ch == '\n' {
			// 输出完整一行
			state.lineCount++
			if state.maxLines > 0 && state.lineCount > state.maxLines {
				state.truncated = true
				r.writeOut(r.colorize("      "+r.gl().panelRail+" ", colorRail) +
					r.colorize("… (streaming truncated)", colorMuted) + "\r\n")
				return
			}
			if !state.headerPrinted {
				state.headerPrinted = true
				r.writeOut(r.colorize("      "+r.gl().panelRail, colorRail) + "\r\n")
			}
			line := state.lineBuffer.String()
			r.writeOut(r.colorize("      "+r.gl().panelRail+" ", colorRail) +
				r.colorize(line, colorStreaming) + "\r\n")
			state.lineBuffer.Reset()
		} else {
			state.lineBuffer.WriteRune(ch)
		}
	}

	// 不等换行也刷出部分内容（实时感）
	if state.lineBuffer.Len() > 0 && !state.headerPrinted {
		state.headerPrinted = true
		r.writeOut(r.colorize("      "+r.gl().panelRail, colorRail) + "\r\n")
	}
}

// findContentValueStart 在 partial JSON 中找到 "content" 键对应值的起始位置。
// 返回值起始位置的索引（引号后第一个字符），未找到返回 -1。
func findContentValueStart(partial string) int {
	// 寻找 "content" 后面跟的 : 和 "
	// 支持多种格式："content": "..." 或 "content":"..."
	patterns := []string{
		`"content": "`,
		`"content":"`,
		`"content" : "`,
	}
	for _, pat := range patterns {
		idx := strings.LastIndex(partial, pat)
		if idx >= 0 {
			return idx + len(pat)
		}
	}
	return -1
}

// extractContentDelta 从刚进入 content 值区域时提取属于内容的 delta 部分
func extractContentDelta(valueStart, delta string) string {
	// valueStart 是从 "content": " 之后开始的全部文本
	// delta 是最新一个增量
	// 这里的 delta 可能还在 content 值内部
	return unescapeJSONString(delta)
}

// unescapeJSONString 简化的 JSON 字符串反转义
func unescapeJSONString(s string) string {
	// 处理常见的 JSON 转义序列
	var sb strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				sb.WriteByte('\n')
				i += 2
			case 'r':
				sb.WriteByte('\r')
				i += 2
			case 't':
				sb.WriteByte('\t')
				i += 2
			case '"':
				sb.WriteByte('"')
				i += 2
			case '\\':
				sb.WriteByte('\\')
				i += 2
			case '/':
				sb.WriteByte('/')
				i += 2
			default:
				sb.WriteByte(s[i])
				i++
			}
		} else {
			sb.WriteByte(s[i])
			i++
		}
	}
	return sb.String()
}

// cleanupStreamState 清理工具的流式输出状态（在 ContentBlockStop 时调用）
func (r *REPL) cleanupStreamState(toolUseID string) {
	if r.streamStates == nil {
		return
	}
	if state, ok := r.streamStates[toolUseID]; ok {
		// 如果行缓冲中还有未输出的内容，刷出来
		if state.lineBuffer.Len() > 0 && !state.truncated {
			if !state.headerPrinted {
				state.headerPrinted = true
				r.writeOut(r.colorize("      "+r.gl().panelRail, colorRail) + "\r\n")
			}
			line := state.lineBuffer.String()
			r.writeOut(r.colorize("      "+r.gl().panelRail+" ", colorRail) +
				r.colorize(line, colorStreaming) + "\r\n")
		}
		// 如果有流式输出，打印结束边条
		if state.headerPrinted {
			r.writeOut(r.colorize("      "+r.gl().panelRail, colorRail) + "\r\n")
		}
		delete(r.streamStates, toolUseID)
	}
}
