package query

import "context"

// AIProvider 定义AI服务提供商接口
// 领域层定义接口，基础设施层(infrastructure/api/)提供具体实现
// 这是整个查询引擎依赖倒置的核心
type AIProvider interface {
	// Stream 发起流式请求，返回事件channel
	// 调用方通过遍历channel获取流式事件，直到channel关闭或context取消
	Stream(ctx context.Context, params *StreamParams) (<-chan StreamEvent, error)

	// Send 发起非流式请求（用于简单查询如压缩摘要）
	Send(ctx context.Context, params *SendParams) (*Message, *Usage, error)
}

// StreamParams 流式请求参数
type StreamParams struct {
	// Model 模型标识（如 "claude-sonnet-4-20250514"）
	Model string `json:"model"`
	// Messages 对话消息列表
	Messages []Message `json:"messages"`
	// System 系统提示词
	System []ContentBlock `json:"system,omitempty"`
	// MaxTokens 最大输出token数
	MaxTokens int `json:"max_tokens"`
	// Temperature 温度参数
	Temperature float64 `json:"temperature,omitempty"`
	// Tools 可用工具列表（JSON Schema格式）
	Tools []ToolDefinition `json:"tools,omitempty"`
	// ToolChoice 工具选择策略
	ToolChoice *ToolChoice `json:"tool_choice,omitempty"`
	// StopSequences 停止序列
	StopSequences []string `json:"stop_sequences,omitempty"`
	// Metadata 请求元数据
	Metadata map[string]string `json:"metadata,omitempty"`
}

// SendParams 非流式请求参数（与StreamParams相同，简化别名）
type SendParams = StreamParams

// ToolDefinition API工具定义（JSON Schema格式）
type ToolDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"` // JSON Schema object
}

// ToolChoice 工具选择策略
type ToolChoice struct {
	// Type 选择类型：auto, any, tool, none
	Type string `json:"type"`
	// Name 指定工具名（当Type为"tool"时）
	Name string `json:"name,omitempty"`
}

// Compactor 消息压缩器接口
// 当token预算接近上限时，压缩历史消息以释放空间
type Compactor interface {
	// Compact 压缩消息列表，返回压缩后的消息和摘要
	Compact(ctx context.Context, messages []Message, provider AIProvider) ([]Message, error)
}

// SessionRepository 会话持久化接口
type SessionRepository interface {
	// SaveMessages 保存会话消息
	SaveMessages(sessionID string, messages []Message) error
	// LoadMessages 加载会话消息
	LoadMessages(sessionID string) ([]Message, error)
	// DeleteSession 删除会话
	DeleteSession(sessionID string) error
}
