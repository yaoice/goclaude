package application

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/anthropics/goclaude/pkg/domain/mcp"
	mcpinfra "github.com/anthropics/goclaude/pkg/infrastructure/mcp"
)

// MCPService MCP 应用服务
//
// 编排 Manager 与配置加载，对外提供：
//   - 启动/停止 MCP 连接
//   - 列出可用 MCP 工具（带服务器名前缀，避免命名冲突）
//   - 转发工具调用到对应服务器
//
// 对齐 src 中 fetchToolsForClient + connectToServer 的核心语义。
type MCPService struct {
	manager *mcpinfra.Manager
	logger  *slog.Logger
}

// NewMCPService 创建 MCP 服务
func NewMCPService(logger *slog.Logger) *MCPService {
	if logger == nil {
		logger = slog.Default()
	}
	return &MCPService{
		manager: mcpinfra.NewManager(),
		logger:  logger,
	}
}

// Manager 暴露底层 manager
func (s *MCPService) Manager() *mcpinfra.Manager {
	return s.manager
}

// LoadAndConnect 从默认配置位置加载并并发连接所有启用的 MCP 服务器
func (s *MCPService) LoadAndConnect(ctx context.Context, projectRoot string) error {
	configs, err := mcpinfra.LoadDefault(projectRoot)
	if err != nil {
		return err
	}
	if len(configs) == 0 {
		s.logger.Debug("未发现 MCP 服务器配置")
		return nil
	}
	errs := s.manager.ConnectAll(ctx, configs)
	connected := len(configs) - len(errs)
	s.logger.Debug("MCP 连接完成", "total", len(configs), "connected", connected, "failed", len(errs))
	for name, e := range errs {
		s.logger.Warn("MCP 连接失败", "server", name, "error", e)
	}
	return nil
}

// ConnectOne 连接单个 MCP 服务器
func (s *MCPService) ConnectOne(ctx context.Context, cfg *mcp.ServerConfig) error {
	_, err := s.manager.Connect(ctx, cfg)
	return err
}

// Reconnect 重连指定服务器（保留缓存配置，先断后连）
//
// 与 src `useMcpReconnect()` 行为对齐。dialog `/mcp` 中的 r 键和 Reconnect 菜单项
// 经由 shell.MCPReconnector 接口最终调到这里。
func (s *MCPService) Reconnect(ctx context.Context, name string) error {
	return s.manager.Reconnect(ctx, name)
}

// PrefixedToolName MCP 工具在主 agent 中的展示名（mcp__<server>__<tool>），对齐 src 命名约定
func PrefixedToolName(server, tool string) string {
	return fmt.Sprintf("mcp__%s__%s", server, tool)
}

// ParsePrefixedToolName 解析 mcp__<server>__<tool>
func ParsePrefixedToolName(name string) (server, tool string, ok bool) {
	if !strings.HasPrefix(name, "mcp__") {
		return "", "", false
	}
	rest := strings.TrimPrefix(name, "mcp__")
	idx := strings.Index(rest, "__")
	if idx < 0 {
		return "", "", false
	}
	return rest[:idx], rest[idx+2:], true
}

// AggregatedTool 聚合后的工具描述（用于注册到主 Tool Registry）
type AggregatedTool struct {
	Server string
	Tool   mcp.ToolInfo
}

// AggregatedResource 聚合后的 MCP 资源描述。
type AggregatedResource struct {
	Server   string
	Resource mcp.Resource
}

// AggregatedPrompt 聚合后的 MCP prompt 描述。
type AggregatedPrompt struct {
	Server string
	Prompt mcp.PromptInfo
}

// ListAllTools 列出所有已连接服务器上的工具
func (s *MCPService) ListAllTools(ctx context.Context) ([]AggregatedTool, error) {
	var out []AggregatedTool
	for _, c := range s.manager.All() {
		if !c.IsConnected() {
			continue
		}
		tools, err := c.ListTools(ctx)
		if err != nil {
			s.logger.Warn("ListTools 失败", "server", c.Name(), "error", err)
			continue
		}
		for _, t := range tools {
			out = append(out, AggregatedTool{Server: c.Name(), Tool: t})
		}
	}
	return out, nil
}

// CallTool 转发工具调用到指定服务器
func (s *MCPService) CallTool(ctx context.Context, server, tool string, args map[string]interface{}) (*mcp.ToolCallResult, error) {
	c, ok := s.manager.Get(server)
	if !ok {
		return nil, fmt.Errorf("mcp server %q not connected", server)
	}
	return c.CallTool(ctx, tool, args)
}

// ListAllResources 列出所有已连接服务器暴露的 MCP resources。
func (s *MCPService) ListAllResources(ctx context.Context) ([]AggregatedResource, error) {
	var out []AggregatedResource
	for _, c := range s.manager.All() {
		if !c.IsConnected() {
			continue
		}
		resources, err := c.ListResources(ctx)
		if err != nil {
			s.logger.Warn("ListResources 失败", "server", c.Name(), "error", err)
			continue
		}
		for _, r := range resources {
			out = append(out, AggregatedResource{Server: c.Name(), Resource: r})
		}
	}
	return out, nil
}

// ReadResource 从指定 MCP 服务器读取一个 resource。
func (s *MCPService) ReadResource(ctx context.Context, server, uri string) (*mcp.ResourceContent, error) {
	c, ok := s.manager.Get(server)
	if !ok {
		return nil, fmt.Errorf("mcp server %q not connected", server)
	}
	return c.ReadResource(ctx, uri)
}

// ListAllPrompts 列出所有已连接服务器暴露的 MCP prompts。
func (s *MCPService) ListAllPrompts(ctx context.Context) ([]AggregatedPrompt, error) {
	var out []AggregatedPrompt
	for _, c := range s.manager.All() {
		if !c.IsConnected() {
			continue
		}
		prompts, err := c.ListPrompts(ctx)
		if err != nil {
			s.logger.Warn("ListPrompts 失败", "server", c.Name(), "error", err)
			continue
		}
		for _, p := range prompts {
			out = append(out, AggregatedPrompt{Server: c.Name(), Prompt: p})
		}
	}
	return out, nil
}

// GetPrompt 从指定 MCP 服务器获取 prompt 模板。
func (s *MCPService) GetPrompt(ctx context.Context, server, name string, args map[string]string) (*mcp.PromptResult, error) {
	c, ok := s.manager.Get(server)
	if !ok {
		return nil, fmt.Errorf("mcp server %q not connected", server)
	}
	return c.GetPrompt(ctx, name, args)
}

// GetPromptText 获取 MCP prompt 并渲染为适合注入对话的纯文本。
func (s *MCPService) GetPromptText(ctx context.Context, server, name string, args map[string]string) (string, error) {
	res, err := s.GetPrompt(ctx, server, name, args)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if res.Description != "" {
		b.WriteString(res.Description)
		b.WriteString("\n\n")
	}
	for _, msg := range res.Messages {
		role := msg.Role
		if role == "" {
			role = "user"
		}
		b.WriteString(role)
		b.WriteString(": ")
		switch msg.Content.Type {
		case "", "text":
			b.WriteString(msg.Content.Text)
		case "image":
			b.WriteString(fmt.Sprintf("[image: %s, %d bytes (base64)]", msg.Content.MimeType, len(msg.Content.Data)))
		case "resource":
			b.WriteString(fmt.Sprintf("[resource: %s]", msg.Content.URI))
		default:
			raw, _ := json.Marshal(msg.Content)
			b.Write(raw)
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()), nil
}

// Shutdown 关闭所有 MCP 连接
func (s *MCPService) Shutdown() {
	s.manager.DisconnectAll()
}

// Statuses 返回各服务器连接状态
func (s *MCPService) Statuses() []mcpinfra.ConnectionStatus {
	return s.manager.Statuses()
}

// OnToolsChanged 订阅 MCP 服务器主动推送的 tools/list_changed 通知
//
// serverName 标识哪个服务器变更；订阅者通常用于刷新 Tool Registry。
func (s *MCPService) OnToolsChanged(h func(serverName string)) {
	if h == nil {
		return
	}
	s.manager.OnToolsChanged(h)
}

// ListToolsForServer 拉取指定服务器最新的工具列表
func (s *MCPService) ListToolsForServer(ctx context.Context, serverName string) ([]mcp.ToolInfo, error) {
	c, ok := s.manager.Get(serverName)
	if !ok {
		return nil, fmt.Errorf("mcp server %q not connected", serverName)
	}
	return c.ListTools(ctx)
}
