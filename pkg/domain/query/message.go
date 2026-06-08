// Package query 定义查询引擎领域模型
package query

import "time"

// Role 消息角色
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// ContentType 内容块类型
type ContentType string

const (
	ContentTypeText       ContentType = "text"
	ContentTypeImage      ContentType = "image"
	ContentTypeToolUse    ContentType = "tool_use"
	ContentTypeToolResult ContentType = "tool_result"
	ContentTypeThinking   ContentType = "thinking"
)

// Message 对话消息领域模型
type Message struct {
	ID        string         `json:"id"`
	Role      Role           `json:"role"`
	Content   []ContentBlock `json:"content"`
	CreatedAt time.Time      `json:"created_at"`
}

// ContentBlock 消息内容块（文本、图片、工具调用等）
type ContentBlock struct {
	Type ContentType `json:"type"`

	// 文本内容
	Text string `json:"text,omitempty"`

	// 图片内容
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"` // base64编码
	URL       string `json:"url,omitempty"`

	// 工具调用
	ToolUseID string      `json:"tool_use_id,omitempty"`
	ToolName  string      `json:"tool_name,omitempty"`
	Input     interface{} `json:"input,omitempty"`

	// 工具结果
	ToolResultID string `json:"tool_result_id,omitempty"`
	IsError      bool   `json:"is_error,omitempty"`

	// 思考内容
	Thinking string `json:"thinking,omitempty"`
}

// NewTextMessage 创建文本消息
func NewTextMessage(role Role, text string) Message {
	return Message{
		Role: role,
		Content: []ContentBlock{
			{Type: ContentTypeText, Text: text},
		},
		CreatedAt: time.Now(),
	}
}

// NewToolUseBlock 创建工具调用内容块
func NewToolUseBlock(id, name string, input interface{}) ContentBlock {
	return ContentBlock{
		Type:      ContentTypeToolUse,
		ToolUseID: id,
		ToolName:  name,
		Input:     input,
	}
}

// NewToolResultBlock 创建工具结果内容块
func NewToolResultBlock(toolUseID string, content string, isError bool) ContentBlock {
	return ContentBlock{
		Type:         ContentTypeToolResult,
		ToolResultID: toolUseID,
		Text:         content,
		IsError:      isError,
	}
}

// GetTextContent 提取消息中的所有文本内容
func (m *Message) GetTextContent() string {
	var text string
	for _, block := range m.Content {
		if block.Type == ContentTypeText {
			text += block.Text
		}
	}
	return text
}

// GetToolUseBlocks 提取消息中的所有工具调用块
func (m *Message) GetToolUseBlocks() []ContentBlock {
	var blocks []ContentBlock
	for _, block := range m.Content {
		if block.Type == ContentTypeToolUse {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

// HasToolUse 判断消息是否包含工具调用
func (m *Message) HasToolUse() bool {
	for _, block := range m.Content {
		if block.Type == ContentTypeToolUse {
			return true
		}
	}
	return false
}

// TokenCount 估算消息的token数量（简易估算）
func (m *Message) TokenCount() int {
	count := 0
	for _, block := range m.Content {
		switch block.Type {
		case ContentTypeText:
			// 粗略估算：每4字符约1个token
			count += len(block.Text) / 4
		case ContentTypeToolUse:
			count += 50 // 工具调用基础开销
		case ContentTypeToolResult:
			count += len(block.Text) / 4
		case ContentTypeImage:
			count += 1000 // 图片固定token开销
		}
	}
	return count
}
