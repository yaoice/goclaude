package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/goclaude/internal/application"
	"github.com/anthropics/goclaude/internal/domain/mcp"
	"github.com/anthropics/goclaude/internal/domain/tool"
)

// MCPTool 把单个 MCP 工具适配为本地 Tool 接口
//
// 对齐 src/tools/MCPTool/MCPTool.ts 的角色：MCP 工具在主 agent 视角下是普通工具，
// 名称形如 mcp__<server>__<tool>，input schema 直接复用 MCP 服务器返回的 JSON Schema。
type MCPTool struct {
	server  string
	info    mcp.ToolInfo
	service *application.MCPService
}

// NewMCPTool 创建 MCPTool 适配器
func NewMCPTool(svc *application.MCPService, server string, info mcp.ToolInfo) *MCPTool {
	return &MCPTool{server: server, info: info, service: svc}
}

// Name 工具名（带 mcp__server__ 前缀）
func (t *MCPTool) Name() string {
	return application.PrefixedToolName(t.server, t.info.Name)
}

func (t *MCPTool) Aliases() []string { return nil }

func (t *MCPTool) Description() string {
	desc := t.info.Description
	if desc == "" {
		desc = fmt.Sprintf("MCP tool from server %q", t.server)
	}
	return desc
}

func (t *MCPTool) IsEnabled() bool { return true }

// IsReadOnly 优先使用 MCP 工具 annotation；缺省按 src 一致的保守策略 false
func (t *MCPTool) IsReadOnly(_ tool.Input) bool {
	return t.info.Annotations.IsReadOnly()
}

// IsConcurrencySafe 只读工具或幂等工具视为并发安全
//
// 对齐 src 实现的同时把 idempotentHint 也纳入：幂等工具即使有副作用，
// 多次并发调用也不会破坏一致性。
func (t *MCPTool) IsConcurrencySafe(_ tool.Input) bool {
	return t.info.Annotations.IsReadOnly() || t.info.Annotations.IsIdempotent()
}

func (t *MCPTool) Prompt() string { return t.Description() }

func (t *MCPTool) ValidateInput(input tool.Input) error { return nil }

func (t *MCPTool) InputSchema() map[string]interface{} {
	if t.info.InputSchema != nil {
		return t.info.InputSchema
	}
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

// CheckPermissions MCP 工具默认放行；用户级别的权限策略在更高层处理
func (t *MCPTool) CheckPermissions(ctx context.Context, input tool.Input, permCtx *tool.PermissionContext) (tool.PermissionResult, error) {
	if permCtx != nil {
		for _, denied := range permCtx.DeniedTools {
			if denied == t.Name() {
				return tool.PermissionResult{Behavior: tool.PermissionDeny, Reason: "denied by config"}, nil
			}
		}
	}
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

// Call 把工具调用转发给 MCP 服务器
func (t *MCPTool) Call(ctx context.Context, input tool.Input, toolCtx *tool.UseContext) (*tool.Result, error) {
	args := map[string]interface{}(input)
	res, err := t.service.CallTool(ctx, t.server, t.info.Name, args)
	if err != nil {
		return nil, err
	}
	return mcpResultToToolResult(res), nil
}

// mcpResultToToolResult 把 MCP 的 content blocks 序列化为字符串结果
func mcpResultToToolResult(res *mcp.ToolCallResult) *tool.Result {
	var sb strings.Builder
	for _, c := range res.Content {
		switch c.Type {
		case "text":
			sb.WriteString(c.Text)
		case "image":
			fmt.Fprintf(&sb, "[image: %s, %d bytes (base64)]", c.MimeType, len(c.Data))
		case "resource":
			fmt.Fprintf(&sb, "[resource: %s]", c.URI)
		default:
			// 未知类型：以 JSON 表示
			if b, err := json.Marshal(c); err == nil {
				sb.Write(b)
			}
		}
		sb.WriteString("\n")
	}
	if sb.Len() == 0 && len(res.StructuredContent) > 0 {
		if b, err := json.MarshalIndent(res.StructuredContent, "", "  "); err == nil {
			sb.Write(b)
		}
	}
	out := tool.NewResult(strings.TrimSpace(sb.String()))
	out.IsError = res.IsError
	if len(res.StructuredContent) > 0 {
		out.WithMetadata("structuredContent", res.StructuredContent)
	}
	if len(res.Meta) > 0 {
		out.WithMetadata("_meta", res.Meta)
	}
	return out
}

// ListMCPResourcesTool 列出 MCP resources。
type ListMCPResourcesTool struct{ service *application.MCPService }

func NewListMCPResourcesTool(svc *application.MCPService) *ListMCPResourcesTool {
	return &ListMCPResourcesTool{service: svc}
}
func (*ListMCPResourcesTool) Name() string      { return "list_mcp_resources" }
func (*ListMCPResourcesTool) Aliases() []string { return []string{"ListMcpResources"} }
func (*ListMCPResourcesTool) Description() string {
	return "List resources exposed by connected MCP servers. Optionally pass server to filter."
}
func (*ListMCPResourcesTool) IsEnabled() bool                     { return true }
func (*ListMCPResourcesTool) IsReadOnly(_ tool.Input) bool        { return true }
func (*ListMCPResourcesTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *ListMCPResourcesTool) Prompt() string                    { return t.Description() }
func (*ListMCPResourcesTool) ValidateInput(_ tool.Input) error    { return nil }
func (*ListMCPResourcesTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"server": map[string]interface{}{"type": "string", "description": "Optional MCP server name to filter."},
		},
	}
}
func (*ListMCPResourcesTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}
func (t *ListMCPResourcesTool) Call(ctx context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("MCP service not configured"), nil
	}
	resources, err := t.service.ListAllResources(ctx)
	if err != nil {
		return tool.NewErrorResult(err.Error()), nil
	}
	serverFilter := input.GetString("server")
	rows := make([]map[string]interface{}, 0, len(resources))
	for _, r := range resources {
		if serverFilter != "" && r.Server != serverFilter {
			continue
		}
		rows = append(rows, map[string]interface{}{
			"server":      r.Server,
			"uri":         r.Resource.URI,
			"name":        r.Resource.Name,
			"description": r.Resource.Description,
			"mimeType":    r.Resource.MimeType,
		})
	}
	return tool.NewResult(jsonOut(map[string]interface{}{"resources": rows, "count": len(rows)})), nil
}

// ReadMCPResourceTool 读取 MCP resource 内容。
type ReadMCPResourceTool struct{ service *application.MCPService }

func NewReadMCPResourceTool(svc *application.MCPService) *ReadMCPResourceTool {
	return &ReadMCPResourceTool{service: svc}
}
func (*ReadMCPResourceTool) Name() string      { return "read_mcp_resource" }
func (*ReadMCPResourceTool) Aliases() []string { return []string{"ReadMcpResource"} }
func (*ReadMCPResourceTool) Description() string {
	return "Read a resource from a connected MCP server by server and uri."
}
func (*ReadMCPResourceTool) IsEnabled() bool                     { return true }
func (*ReadMCPResourceTool) IsReadOnly(_ tool.Input) bool        { return true }
func (*ReadMCPResourceTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *ReadMCPResourceTool) Prompt() string                    { return t.Description() }
func (*ReadMCPResourceTool) ValidateInput(input tool.Input) error {
	if input.GetString("server") == "" {
		return fmt.Errorf("server is required")
	}
	if input.GetString("uri") == "" {
		return fmt.Errorf("uri is required")
	}
	return nil
}
func (*ReadMCPResourceTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"server": map[string]interface{}{"type": "string"},
			"uri":    map[string]interface{}{"type": "string"},
		},
		"required": []string{"server", "uri"},
	}
}
func (*ReadMCPResourceTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}
func (t *ReadMCPResourceTool) Call(ctx context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("MCP service not configured"), nil
	}
	content, err := t.service.ReadResource(ctx, input.GetString("server"), input.GetString("uri"))
	if err != nil {
		return tool.NewErrorResult(err.Error()), nil
	}
	if content.Text != "" {
		out := tool.NewResult(content.Text)
		out.WithMetadata("uri", content.URI).WithMetadata("mimeType", content.MimeType)
		return out, nil
	}
	return tool.NewResult(jsonOut(content)), nil
}

// GetMCPPromptTool 获取 MCP prompt 并渲染为文本。
type GetMCPPromptTool struct{ service *application.MCPService }

func NewGetMCPPromptTool(svc *application.MCPService) *GetMCPPromptTool {
	return &GetMCPPromptTool{service: svc}
}
func (*GetMCPPromptTool) Name() string      { return "get_mcp_prompt" }
func (*GetMCPPromptTool) Aliases() []string { return []string{"GetMcpPrompt"} }
func (*GetMCPPromptTool) Description() string {
	return "Get and render a prompt from a connected MCP server. Pass args as an object of string values."
}
func (*GetMCPPromptTool) IsEnabled() bool                     { return true }
func (*GetMCPPromptTool) IsReadOnly(_ tool.Input) bool        { return true }
func (*GetMCPPromptTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *GetMCPPromptTool) Prompt() string                    { return t.Description() }
func (*GetMCPPromptTool) ValidateInput(input tool.Input) error {
	if input.GetString("server") == "" {
		return fmt.Errorf("server is required")
	}
	if input.GetString("name") == "" {
		return fmt.Errorf("name is required")
	}
	return nil
}
func (*GetMCPPromptTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"server": map[string]interface{}{"type": "string"},
			"name":   map[string]interface{}{"type": "string"},
			"args":   map[string]interface{}{"type": "object", "additionalProperties": map[string]interface{}{"type": "string"}},
		},
		"required": []string{"server", "name"},
	}
}
func (*GetMCPPromptTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}
func (t *GetMCPPromptTool) Call(ctx context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	if t.service == nil {
		return tool.NewErrorResult("MCP service not configured"), nil
	}
	text, err := t.service.GetPromptText(ctx, input.GetString("server"), input.GetString("name"), inputStringMap(input["args"]))
	if err != nil {
		return tool.NewErrorResult(err.Error()), nil
	}
	return tool.NewResult(text), nil
}

func inputStringMap(v interface{}) map[string]string {
	out := map[string]string{}
	switch m := v.(type) {
	case map[string]string:
		for k, v := range m {
			out[k] = v
		}
	case map[string]interface{}:
		for k, v := range m {
			if s, ok := v.(string); ok {
				out[k] = s
			}
		}
	}
	return out
}

// RegisterMCPTools 把所有 MCP 服务器上发现的工具批量注册到 registry。
//
// 返回值只统计远端 MCP tools；resources/prompts helper tools 始终额外注册。
func RegisterMCPTools(ctx context.Context, registry *tool.Registry, svc *application.MCPService) (int, error) {
	if registry == nil || svc == nil {
		return 0, fmt.Errorf("registry and mcp service are required")
	}
	for _, helper := range []tool.Tool{
		NewListMCPResourcesTool(svc),
		NewReadMCPResourceTool(svc),
		NewGetMCPPromptTool(svc),
	} {
		registry.Unregister(helper.Name())
		_ = registry.Register(helper)
	}

	all, err := svc.ListAllTools(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, t := range all {
		mt := NewMCPTool(svc, t.Server, t.Tool)
		if err := registry.Register(mt); err == nil {
			count++
		}
	}
	return count, nil
}

// SyncMCPTools 重新拉取某个 MCP 服务器的工具列表并刷新 registry
//
// 实现策略：先移除该 server 旧的工具，再注册新的。
// 旧工具识别：name 以 "mcp__<server>__" 开头。
//
// 用于响应 notifications/tools/list_changed 通知。
func SyncMCPTools(ctx context.Context, registry *tool.Registry, svc *application.MCPService, serverName string) (added int, err error) {
	prefix := application.PrefixedToolName(serverName, "")
	// 1. 移除旧的
	for _, name := range registry.Names() {
		if strings.HasPrefix(name, prefix) {
			registry.Unregister(name)
		}
	}
	// 2. 重新拉取并注册
	tools, err := svc.ListToolsForServer(ctx, serverName)
	if err != nil {
		return 0, err
	}
	for _, ti := range tools {
		mt := NewMCPTool(svc, serverName, ti)
		if err := registry.Register(mt); err == nil {
			added++
		}
	}
	return added, nil
}
