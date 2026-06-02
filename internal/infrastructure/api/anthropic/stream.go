package anthropic

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/anthropics/goclaude/internal/domain/query"
)

// parseSSEStream 解析SSE流式响应
// 从HTTP响应body中读取SSE事件，转换为领域StreamEvent发送到channel
func (c *Client) parseSSEStream(ctx context.Context, body io.ReadCloser, events chan<- query.StreamEvent) {
	defer close(events)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	// 增大扫描缓冲区以处理大型工具输入
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	var eventType string
	var dataLines []string

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			events <- query.StreamEvent{Type: query.EventError, Error: ctx.Err()}
			return
		default:
		}

		line := scanner.Text()

		// 空行表示事件结束
		if line == "" {
			if eventType != "" && len(dataLines) > 0 {
				data := strings.Join(dataLines, "\n")
				event := c.parseSSEEvent(eventType, data)
				if event != nil {
					select {
					case events <- *event:
					case <-ctx.Done():
						return
					}
				}
			}
			eventType = ""
			dataLines = nil
			continue
		}

		// 解析SSE字段
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		}
	}

	if err := scanner.Err(); err != nil {
		events <- query.StreamEvent{Type: query.EventError, Error: fmt.Errorf("SSE scan error: %w", err)}
	}
}

// parseSSEEvent 将SSE事件转换为领域流式事件
func (c *Client) parseSSEEvent(eventType, data string) *query.StreamEvent {
	switch eventType {
	case "message_start":
		var evt SSEMessageStart
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			return &query.StreamEvent{Type: query.EventError, Error: err}
		}
		event := &query.StreamEvent{Type: query.EventMessageStart}
		if evt.Message.Usage != nil {
			event.Usage = &query.Usage{
				InputTokens:  evt.Message.Usage.InputTokens,
				OutputTokens: evt.Message.Usage.OutputTokens,
			}
		}
		return event

	case "content_block_start":
		var evt SSEContentBlockStart
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			return &query.StreamEvent{Type: query.EventError, Error: err}
		}
		block := convertSSEContentBlock(evt.ContentBlock)
		return &query.StreamEvent{
			Type:         query.EventContentBlockStart,
			Index:        evt.Index,
			ContentBlock: block,
		}

	case "content_block_delta":
		var evt SSEContentBlockDelta
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			return &query.StreamEvent{Type: query.EventError, Error: err}
		}
		delta := convertSSEDelta(evt.Delta)
		return &query.StreamEvent{
			Type:  query.EventContentBlockDelta,
			Index: evt.Index,
			Delta: delta,
		}

	case "content_block_stop":
		var evt SSEContentBlockStop
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			return &query.StreamEvent{Type: query.EventError, Error: err}
		}
		return &query.StreamEvent{
			Type:  query.EventContentBlockStop,
			Index: evt.Index,
		}

	case "message_delta":
		var evt SSEMessageDelta
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			return &query.StreamEvent{Type: query.EventError, Error: err}
		}
		event := &query.StreamEvent{
			Type:       query.EventMessageDelta,
			StopReason: query.StopReason(evt.Delta.StopReason),
		}
		if evt.Usage != nil {
			event.Usage = &query.Usage{
				InputTokens:  evt.Usage.InputTokens,
				OutputTokens: evt.Usage.OutputTokens,
			}
		}
		return event

	case "message_stop":
		return &query.StreamEvent{Type: query.EventMessageStop}

	case "ping":
		return &query.StreamEvent{Type: query.EventPing}

	case "error":
		var evt SSEError
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			return &query.StreamEvent{Type: query.EventError, Error: err}
		}
		return &query.StreamEvent{
			Type:  query.EventError,
			Error: fmt.Errorf("API error: %s - %s", evt.Error.Type, evt.Error.Message),
		}

	default:
		// 未知事件类型，忽略
		return nil
	}
}

// convertSSEContentBlock 转换SSE内容块
func convertSSEContentBlock(block *SSEBlock) *query.ContentBlock {
	if block == nil {
		return nil
	}
	switch block.Type {
	case "text":
		return &query.ContentBlock{Type: query.ContentTypeText, Text: block.Text}
	case "tool_use":
		return &query.ContentBlock{
			Type:      query.ContentTypeToolUse,
			ToolUseID: block.ID,
			ToolName:  block.Name,
		}
	case "thinking":
		return &query.ContentBlock{Type: query.ContentTypeThinking}
	default:
		return &query.ContentBlock{Type: query.ContentTypeText}
	}
}

// convertSSEDelta 转换SSE增量
func convertSSEDelta(delta *SSEDeltaBlock) *query.DeltaContent {
	if delta == nil {
		return nil
	}
	switch delta.Type {
	case "text_delta":
		return &query.DeltaContent{Type: query.ContentTypeText, Text: delta.Text}
	case "input_json_delta":
		return &query.DeltaContent{Type: query.ContentTypeToolUse, PartialJSON: delta.PartialJSON}
	case "thinking_delta":
		return &query.DeltaContent{Type: query.ContentTypeThinking, Thinking: delta.Thinking}
	default:
		return &query.DeltaContent{Type: query.ContentTypeText, Text: delta.Text}
	}
}

// SSE事件结构体

type SSEMessageStart struct {
	Message struct {
		ID    string    `json:"id"`
		Usage *APIUsage `json:"usage,omitempty"`
	} `json:"message"`
}

type SSEContentBlockStart struct {
	Index        int       `json:"index"`
	ContentBlock *SSEBlock `json:"content_block"`
}

type SSEContentBlockDelta struct {
	Index int            `json:"index"`
	Delta *SSEDeltaBlock `json:"delta"`
}

type SSEContentBlockStop struct {
	Index int `json:"index"`
}

type SSEMessageDelta struct {
	Delta struct {
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage *APIUsage `json:"usage,omitempty"`
}

type SSEError struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type SSEBlock struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	Text string `json:"text,omitempty"`
}

type SSEDeltaBlock struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
}
