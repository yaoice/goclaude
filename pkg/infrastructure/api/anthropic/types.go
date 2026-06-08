package anthropic

// MessageRequest Anthropic Messages API 请求结构体
type MessageRequest struct {
	Model       string         `json:"model"`
	Messages    []APIMessage   `json:"messages"`
	System      []SystemBlock  `json:"system,omitempty"`
	MaxTokens   int            `json:"max_tokens"`
	Temperature *float64       `json:"temperature,omitempty"`
	Stream      bool           `json:"stream"`
	Tools       []APIToolDef   `json:"tools,omitempty"`
	ToolChoice  *APIToolChoice `json:"tool_choice,omitempty"`
	StopSeqs    []string       `json:"stop_sequences,omitempty"`
	Metadata    *APIMetadata   `json:"metadata,omitempty"`
}

// MessageResponse Anthropic Messages API 响应结构体
type MessageResponse struct {
	ID         string            `json:"id"`
	Type       string            `json:"type"`
	Role       string            `json:"role"`
	Content    []APIContentBlock `json:"content"`
	Model      string            `json:"model"`
	StopReason string            `json:"stop_reason"`
	Usage      *APIUsage         `json:"usage"`
}

// APIMessage API消息格式
type APIMessage struct {
	Role    string            `json:"role"`
	Content []APIContentBlock `json:"content"`
}

// APIContentBlock API内容块格式
type APIContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     interface{}     `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Source    *APIImageSource `json:"source,omitempty"`
}

// APIImageSource 图片来源
type APIImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// APIToolDef API工具定义
type APIToolDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"`
}

// APIToolChoice 工具选择策略
type APIToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

// APIUsage API使用量
type APIUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// SystemBlock 系统提示块
type SystemBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// CacheControl 缓存控制
type CacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

// APIMetadata 请求元数据
type APIMetadata struct {
	UserID string `json:"user_id,omitempty"`
}
