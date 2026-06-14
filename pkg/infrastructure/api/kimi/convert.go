package kimi

import (
	"encoding/json"
	"strings"

	"github.com/yaoice/goclaude/pkg/domain/query"
)

// buildRequest 将领域层 query.StreamParams 转换为 Kimi (OpenAI 兼容) 请求体
func (c *Client) buildRequest(params *query.StreamParams, stream bool) *ChatRequest {
	req := &ChatRequest{
		Model:     resolveModel(params.Model),
		Stream:    stream,
		MaxTokens: params.MaxTokens,
	}
	if params.Temperature > 0 {
		t := params.Temperature
		req.Temperature = &t
	}
	if len(params.StopSequences) > 0 {
		req.Stop = params.StopSequences
	}
	if stream {
		req.StreamOptions = &StreamOpts{IncludeUsage: true}
	}

	// 系统提示 -> system 消息（合并所有文本块）
	if len(params.System) > 0 {
		var sb strings.Builder
		for _, b := range params.System {
			if b.Type == query.ContentTypeText && b.Text != "" {
				if sb.Len() > 0 {
					sb.WriteString("\n\n")
				}
				sb.WriteString(b.Text)
			}
		}
		if sb.Len() > 0 {
			req.Messages = append(req.Messages, ChatMessage{
				Role:    "system",
				Content: strPtr(sb.String()),
			})
		}
	}

	// 转换对话消息
	for _, msg := range params.Messages {
		req.Messages = append(req.Messages, convertDomainMessage(msg)...)
	}

	// 转换工具定义
	for _, tool := range params.Tools {
		req.Tools = append(req.Tools, ToolDef{
			Type: "function",
			Function: FunctionDef{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			},
		})
	}

	// 转换工具选择策略
	if params.ToolChoice != nil {
		req.ToolChoice = convertToolChoice(params.ToolChoice)
	}

	return req
}

// resolveModel 将上层模型名映射到 Kimi 实际模型名
//
// 兼容用户使用 claude-* / deepseek-* 名称（默认回退到 kimi-k2.6）
func resolveModel(name string) string {
	if name == "" {
		return ModelK2
	}
	switch name {
	case ModelK2, ModelCode:
		return name
	}
	if strings.HasPrefix(name, "kimi-") || strings.HasPrefix(name, "Kimi ") {
		return name
	}
	// 非 Kimi 模型名 -> 回退到默认模型
	return ModelK2
}

// convertToolChoice 转换工具选择策略到 OpenAI 格式
func convertToolChoice(tc *query.ToolChoice) interface{} {
	switch tc.Type {
	case "auto", "none":
		return tc.Type
	case "any":
		return "required"
	case "tool":
		if tc.Name == "" {
			return "auto"
		}
		return map[string]interface{}{
			"type":     "function",
			"function": map[string]string{"name": tc.Name},
		}
	}
	return "auto"
}

// convertDomainMessage 将领域消息转换为 OpenAI 风格消息（一条领域消息可能拆为多条 OpenAI 消息）
//
// 转换规则：
//   - user 消息中的 tool_result 块 -> role=tool 的独立消息
//   - assistant 消息中的 tool_use 块 -> 合并到同一条 assistant 的 tool_calls 中
//   - text 块按顺序合并为 content
func convertDomainMessage(msg query.Message) []ChatMessage {
	switch msg.Role {
	case query.RoleUser:
		return convertUserMessage(msg)
	case query.RoleAssistant:
		return []ChatMessage{convertAssistantMessage(msg)}
	}
	return nil
}

func convertUserMessage(msg query.Message) []ChatMessage {
	var out []ChatMessage
	var textParts []string

	for _, block := range msg.Content {
		switch block.Type {
		case query.ContentTypeText:
			if block.Text != "" {
				textParts = append(textParts, block.Text)
			}
		case query.ContentTypeToolResult:
			// 先 flush 累积的文本
			if len(textParts) > 0 {
				out = append(out, ChatMessage{
					Role:    "user",
					Content: strPtr(strings.Join(textParts, "\n")),
				})
				textParts = nil
			}
			content := block.Text
			if block.IsError {
				content = "[ERROR] " + content
			}
			out = append(out, ChatMessage{
				Role:       "tool",
				ToolCallID: block.ToolResultID,
				Content:    strPtr(content),
			})
		}
	}
	if len(textParts) > 0 {
		out = append(out, ChatMessage{
			Role:    "user",
			Content: strPtr(strings.Join(textParts, "\n")),
		})
	}
	return out
}

func convertAssistantMessage(msg query.Message) ChatMessage {
	out := ChatMessage{Role: "assistant"}
	var textParts []string

	for _, block := range msg.Content {
		switch block.Type {
		case query.ContentTypeThinking:
			// 领域 thinking 块 → Kimi reasoning_content（多块时拼接）
			if block.Thinking != "" {
				if out.ReasoningContent != "" {
					out.ReasoningContent += "\n"
				}
				out.ReasoningContent += block.Thinking
			}
		case query.ContentTypeText:
			if block.Text != "" {
				textParts = append(textParts, block.Text)
			}
		case query.ContentTypeToolUse:
			argBytes, _ := json.Marshal(block.Input)
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID:   block.ToolUseID,
				Type: "function",
				Function: FunctionCall{
					Name:      block.ToolName,
					Arguments: string(argBytes),
				},
			})
		}
	}
	// OpenAI 兼容协议要求 assistant.content 字段必须存在：
	// - 有文本时：填文本
	// - 仅有 tool_calls 时：必须显式填 ""（不能省略，否则 API 报
	//   "messages[i]: missing field content"）
	switch {
	case len(textParts) > 0:
		out.Content = strPtr(strings.Join(textParts, "\n"))
	case len(out.ToolCalls) > 0:
		out.Content = strPtr("")
	}
	return out
}

// convertChoiceToMessage 将 OpenAI 风格响应消息转换为领域消息
func convertChoiceToMessage(id string, m ChatMessage) *query.Message {
	msg := &query.Message{
		ID:   id,
		Role: query.RoleAssistant,
	}
	// reasoning_content（Kimi K2 推理链）→ thinking 块，排在 text 之前
	if m.ReasoningContent != "" {
		msg.Content = append(msg.Content, query.ContentBlock{
			Type:     query.ContentTypeThinking,
			Thinking: m.ReasoningContent,
		})
	}
	if m.Content != nil && *m.Content != "" {
		msg.Content = append(msg.Content, query.ContentBlock{
			Type: query.ContentTypeText,
			Text: *m.Content,
		})
	}
	for _, tc := range m.ToolCalls {
		var input interface{}
		if tc.Function.Arguments != "" {
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
				input = tc.Function.Arguments
			}
		}
		msg.Content = append(msg.Content, query.ContentBlock{
			Type:      query.ContentTypeToolUse,
			ToolUseID: tc.ID,
			ToolName:  tc.Function.Name,
			Input:     input,
		})
	}
	return msg
}

// convertUsage 转换 Usage
func convertUsage(u *Usage) *query.Usage {
	if u == nil {
		return nil
	}
	return &query.Usage{
		InputTokens:              u.PromptTokens,
		OutputTokens:             u.CompletionTokens,
		CacheReadInputTokens:     u.PromptCacheHitTokens,
		CacheCreationInputTokens: u.PromptCacheMissTokens,
	}
}

// mapFinishReason Kimi finish_reason -> 领域 StopReason
func mapFinishReason(r string) query.StopReason {
	switch r {
	case "stop":
		return query.StopReasonEndTurn
	case "length":
		return query.StopReasonMaxTokens
	case "tool_calls":
		return query.StopReasonToolUse
	case "content_filter":
		return query.StopReasonStopSeq
	}
	if r == "" {
		return ""
	}
	return query.StopReason(r)
}
