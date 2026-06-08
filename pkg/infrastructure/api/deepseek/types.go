// Package deepseek 实现 DeepSeek Chat Completions API 客户端
// DeepSeek 使用与 OpenAI 兼容的协议
package deepseek

// ChatRequest DeepSeek Chat Completions 请求体（OpenAI 兼容）
type ChatRequest struct {
	Model            string        `json:"model"`
	Messages         []ChatMessage `json:"messages"`
	MaxTokens        int           `json:"max_tokens,omitempty"`
	Temperature      *float64      `json:"temperature,omitempty"`
	TopP             *float64      `json:"top_p,omitempty"`
	Stream           bool          `json:"stream"`
	StreamOptions    *StreamOpts   `json:"stream_options,omitempty"`
	Tools            []ToolDef     `json:"tools,omitempty"`
	ToolChoice       interface{}   `json:"tool_choice,omitempty"` // "auto"/"none" 或 {"type":"function",...}
	Stop             []string      `json:"stop,omitempty"`
	PresencePenalty  *float64      `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64      `json:"frequency_penalty,omitempty"`
}

// StreamOpts 流式选项
type StreamOpts struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// ChatMessage Chat 消息（兼容 OpenAI 格式）
// content 可能是 string 或 []ContentPart（视觉模型）
//
// 注意：Content 用 *string 是为了能显式序列化为空串 ""。
// DeepSeek 在 assistant 消息含 tool_calls 时仍要求 content 字段必须存在
// （即使为 ""/null），否则返回 `messages[i]: missing field content`。
// 用 omitempty + string 时空串会被整体省略，会触发该错误。
type ChatMessage struct {
	Role             string     `json:"role"`                        // "system" | "user" | "assistant" | "tool"
	Content          *string    `json:"content,omitempty"`           // 简单文本内容；为 nil 时不发送，为 &"" 时发送空串
	ReasoningContent string     `json:"reasoning_content,omitempty"` // deepseek-reasoner 推理链
	Name             string     `json:"name,omitempty"`              //
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"` // role=tool 时填
}

// strPtr 返回指向 s 的指针（用于 ChatMessage.Content）
func strPtr(s string) *string { return &s }

// ToolCall 助手发起的工具调用
type ToolCall struct {
	Index    int          `json:"index,omitempty"`
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall 函数调用细节
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON字符串（流式时为增量片段）
}

// ToolDef 工具定义（OpenAI function calling 格式）
type ToolDef struct {
	Type     string      `json:"type"` // "function"
	Function FunctionDef `json:"function"`
}

// FunctionDef 函数定义
type FunctionDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters"` // JSON Schema
}

// ChatResponse 非流式响应
type ChatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage"`
}

// Choice 单个选择
type Choice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	Delta        *Delta      `json:"delta,omitempty"`
	FinishReason string      `json:"finish_reason"`
}

// Delta 流式增量
type Delta struct {
	Role             string     `json:"role,omitempty"`
	Content          string     `json:"content,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"` // deepseek-reasoner 推理链增量
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
}

// StreamChunk 流式事件块
type StreamChunk struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"` // 仅最后一个 chunk 含
}

// Usage Token 使用量
type Usage struct {
	PromptTokens          int `json:"prompt_tokens"`
	CompletionTokens      int `json:"completion_tokens"`
	TotalTokens           int `json:"total_tokens"`
	PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens,omitempty"`
	PromptCacheMissTokens int `json:"prompt_cache_miss_tokens,omitempty"`
}

// ErrorResponse 错误响应
type ErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// 内置模型常量
const (
	ModelChat     = "deepseek-chat"     // V3 通用对话模型
	ModelReasoner = "deepseek-reasoner" // R1 推理模型（具备 thinking）
)
