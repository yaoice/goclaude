// Package mcp 定义 Model Context Protocol 领域模型
//
// 对齐 src/services/mcp/types.ts 与 @modelcontextprotocol/sdk 的 JSON-RPC 2.0 语义。
package mcp

import (
	"context"
	"encoding/json"
)

// TransportType 传输类型
type TransportType string

const (
	TransportStdio TransportType = "stdio"
	TransportSSE   TransportType = "sse"
	TransportHTTP  TransportType = "http"
	TransportWS    TransportType = "ws"
)

// Transport MCP 传输层接口
type Transport interface {
	// Start 启动传输连接
	Start(ctx context.Context) error
	// Send 发送 JSON-RPC 消息
	Send(ctx context.Context, msg *JSONRPCMessage) error
	// Recv 返回单条接收的消息（用于客户端读循环）
	Recv(ctx context.Context) (*JSONRPCMessage, error)
	// Close 关闭连接
	Close() error
}

// Client MCP 客户端接口
type Client interface {
	Name() string
	Connect(ctx context.Context) error
	Disconnect() error
	IsConnected() bool

	ListTools(ctx context.Context) ([]ToolInfo, error)
	CallTool(ctx context.Context, name string, args map[string]interface{}) (*ToolCallResult, error)

	ListResources(ctx context.Context) ([]Resource, error)
	ReadResource(ctx context.Context, uri string) (*ResourceContent, error)

	ListPrompts(ctx context.Context) ([]PromptInfo, error)
	GetPrompt(ctx context.Context, name string, args map[string]string) (*PromptResult, error)
}

// JSONRPCMessage JSON-RPC 2.0 消息（请求/响应/通知统一表示）
type JSONRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// IsNotification 通知没有 id
func (m *JSONRPCMessage) IsNotification() bool {
	return len(m.ID) == 0 && m.Method != ""
}

// IsResponse 响应（含 result 或 error），id 必须有
func (m *JSONRPCMessage) IsResponse() bool {
	return len(m.ID) > 0 && (m.Method == "" || m.Result != nil || m.Error != nil)
}

// JSONRPCError JSON-RPC 错误
type JSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error 实现 error 接口
func (e *JSONRPCError) Error() string {
	return e.Message
}

// ToolInfo MCP 工具元数据
type ToolInfo struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"inputSchema"`
	// Annotations MCP 2024-11-05 起的工具行为提示（读写性/幂等性/破坏性等）
	Annotations *ToolAnnotations `json:"annotations,omitempty"`
}

// ToolAnnotations 工具行为提示
//
// 对齐 MCP spec 中的 tool annotations：
//   - readOnlyHint:    工具是否只读
//   - destructiveHint: 工具是否可能造成破坏性后果
//   - idempotentHint:  多次调用是否幂等
//   - openWorldHint:   是否与外部世界交互（例如网络）
//
// 所有字段为指针，缺省表示服务端未声明，由调用方按保守策略处理。
type ToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// IsReadOnly 读取 annotation，缺省 false
func (a *ToolAnnotations) IsReadOnly() bool {
	if a == nil || a.ReadOnlyHint == nil {
		return false
	}
	return *a.ReadOnlyHint
}

// IsDestructive 读取 annotation，缺省 false
func (a *ToolAnnotations) IsDestructive() bool {
	if a == nil || a.DestructiveHint == nil {
		return false
	}
	return *a.DestructiveHint
}

// IsIdempotent 读取 annotation，缺省 false
func (a *ToolAnnotations) IsIdempotent() bool {
	if a == nil || a.IdempotentHint == nil {
		return false
	}
	return *a.IdempotentHint
}

// ToolCallResult MCP 工具调用结果
type ToolCallResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
	// Meta 保留 MCP 结果中的 _meta 字段，供 UI/调试/下游适配器使用。
	Meta map[string]interface{} `json:"_meta,omitempty"`
	// StructuredContent 保留结构化输出，对齐 MCP tools/call 的 structuredContent。
	StructuredContent map[string]interface{} `json:"structuredContent,omitempty"`
}

// ToolContent 工具返回内容
type ToolContent struct {
	Type     string `json:"type"` // "text", "image", "resource"
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"` // base64
	URI      string `json:"uri,omitempty"`
}

// Resource MCP 资源
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// ResourceContent 资源内容
type ResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"` // base64
}

// PromptInfo MCP prompt 元数据
type PromptInfo struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Arguments   []PromptArgumentSchema `json:"arguments,omitempty"`
}

// PromptArgumentSchema prompt 参数定义
type PromptArgumentSchema struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// PromptResult get_prompt 调用结果
type PromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

// PromptMessage prompt 模板消息
type PromptMessage struct {
	Role    string      `json:"role"`
	Content ToolContent `json:"content"`
}

// ServerConfig MCP 服务器配置
type ServerConfig struct {
	// Name 服务器名称（注册键）
	Name string `json:"name"`
	// TransportType 传输类型
	TransportType TransportType `json:"type"`

	// Stdio
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`

	// HTTP / SSE
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`

	// Scope 配置作用域（local/user/project/dynamic 等）
	Scope string `json:"scope,omitempty"`

	// Enabled 是否启用
	Enabled *bool `json:"enabled,omitempty"`
}

// IsEnabled 默认启用，除非显式设为 false
func (c *ServerConfig) IsEnabled() bool {
	if c == nil {
		return false
	}
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}
