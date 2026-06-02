// Package mcpinfra 实现 MCP 客户端
//
// 设计要点（修正之前的实现）：
//   - Connect 启动后台读循环；call 通过 ID -> chan 映射做并发响应分发
//   - 所有等待中的请求都能在 Disconnect / EOF 时被释放
//   - initialize 与 notifications/initialized 严格遵循 MCP 协议
package mcpinfra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropics/goclaude/internal/domain/mcp"
)

const (
	protocolVersion = "2024-11-05"
	clientName      = "goclaude"
	clientVersion   = "1.0.0"
)

// ErrNotConnected 在未连接时调用 RPC 触发
var ErrNotConnected = errors.New("mcp client not connected")

// ClientImpl MCP 客户端实现
type ClientImpl struct {
	mu        sync.Mutex
	transport mcp.Transport
	config    *mcp.ServerConfig
	connected bool
	closed    chan struct{}
	// done 在 readLoop 退出时关闭；上层（如 Manager.watchAndReconnect）用此判断 client 何时失联
	done   chan struct{}
	doneMu sync.Mutex
	nextID atomic.Int64

	pendingMu sync.Mutex
	pending   map[int64]chan *mcp.JSONRPCMessage

	// recvErr 保存读循环退出时的错误，让等待中的请求看到原因
	recvErr atomic.Value // error

	// notifHandler 通知回调（method -> handler）。客户端代码通过 OnNotification 注册。
	notifMu      sync.RWMutex
	notifHandler map[string]NotificationHandler
}

// NotificationHandler MCP 通知回调
//
// 当服务端推送通知（如 notifications/tools/list_changed）时被同步触发。
// 回调内不应做长时间阻塞操作；如需 I/O 应自行 go func。
type NotificationHandler func(method string, params json.RawMessage)

// NewClient 创建 MCP 客户端
func NewClient(config *mcp.ServerConfig) *ClientImpl {
	return &ClientImpl{
		config:       config,
		pending:      make(map[int64]chan *mcp.JSONRPCMessage),
		closed:       make(chan struct{}),
		done:         make(chan struct{}),
		notifHandler: make(map[string]NotificationHandler),
	}
}

// Done 返回 channel；在 readLoop 退出时被关闭
//
// 用于上层（Manager）等待 client 失联以触发重连。
func (c *ClientImpl) Done() <-chan struct{} {
	c.doneMu.Lock()
	defer c.doneMu.Unlock()
	return c.done
}

// RecvErr 返回 readLoop 退出时记录的错误（nil 表示正常关闭）
func (c *ClientImpl) RecvErr() error {
	if v := c.recvErr.Load(); v != nil {
		if err, ok := v.(error); ok {
			return err
		}
	}
	return nil
}

// OnNotification 注册某个 method 的通知处理器（覆盖式注册）
//
// 传入 method == "" 表示注册"任意通知"的兜底处理器（method 参数仍会传入）。
func (c *ClientImpl) OnNotification(method string, handler NotificationHandler) {
	c.notifMu.Lock()
	defer c.notifMu.Unlock()
	if handler == nil {
		delete(c.notifHandler, method)
		return
	}
	c.notifHandler[method] = handler
}

// Name 返回服务器名
func (c *ClientImpl) Name() string {
	if c.config == nil {
		return ""
	}
	return c.config.Name
}

// Connect 建立连接并完成 MCP initialize 握手
func (c *ClientImpl) Connect(ctx context.Context) error {
	c.mu.Lock()
	if c.connected {
		c.mu.Unlock()
		return nil
	}

	switch c.config.TransportType {
	case mcp.TransportStdio:
		c.transport = NewStdioTransport(c.config.Command, c.config.Args, c.config.Env)
	case mcp.TransportHTTP:
		c.transport = NewHTTPTransport(c.config.URL, c.config.Headers)
	case mcp.TransportSSE:
		c.transport = NewSSETransport(c.config.URL, c.config.Headers)
	case mcp.TransportWS:
		c.transport = NewWSTransport(c.config.URL, c.config.Headers)
	default:
		c.mu.Unlock()
		return fmt.Errorf("unsupported transport type: %s", c.config.TransportType)
	}

	if err := c.transport.Start(ctx); err != nil {
		c.mu.Unlock()
		return fmt.Errorf("start transport: %w", err)
	}

	c.connected = true
	c.closed = make(chan struct{})
	// 每次 Connect 都重置 done channel，让上层可在断线重连后重新等待新一次 Done
	c.doneMu.Lock()
	c.done = make(chan struct{})
	c.doneMu.Unlock()
	c.mu.Unlock()

	// 启动读循环
	go c.readLoop()

	// 握手
	if err := c.handshake(ctx); err != nil {
		_ = c.Disconnect()
		return err
	}
	return nil
}

// handshake 发送 initialize 与 notifications/initialized
func (c *ClientImpl) handshake(ctx context.Context) error {
	initParams, _ := json.Marshal(map[string]interface{}{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    clientName,
			"version": clientVersion,
		},
	})
	if _, err := c.call(ctx, "initialize", initParams); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	// 通知（无 id，不等响应）
	notifyParams, _ := json.Marshal(map[string]interface{}{})
	msg := &mcp.JSONRPCMessage{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
		Params:  notifyParams,
	}
	if err := c.transport.Send(ctx, msg); err != nil {
		return fmt.Errorf("send initialized notification: %w", err)
	}
	return nil
}

// Disconnect 断开连接，释放所有等待中的请求
//
// 在物理关闭传输前先 best-effort 发送 `notifications/cancelled`，
// 让 MCP 服务端有机会优雅清理状态（对齐 src 的 cleanup 流程）。
// 通知失败、超时均忽略，不阻塞断开流程。
func (c *ClientImpl) Disconnect() error {
	c.mu.Lock()
	if !c.connected {
		c.mu.Unlock()
		return nil
	}
	c.connected = false
	close(c.closed)
	transport := c.transport
	c.mu.Unlock()

	// best-effort 发 cancelled 通知（对齐 src）
	if transport != nil {
		notifyParams, _ := json.Marshal(map[string]interface{}{})
		notifyMsg := &mcp.JSONRPCMessage{
			JSONRPC: "2.0",
			Method:  "notifications/cancelled",
			Params:  notifyParams,
		}
		notifyCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		_ = transport.Send(notifyCtx, notifyMsg)
		cancel()
	}

	// 释放所有 pending（让 call 返回错误）
	c.failPending(errors.New("mcp client disconnected"))

	if transport != nil {
		return transport.Close()
	}
	return nil
}

// IsConnected 当前是否连接
func (c *ClientImpl) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// readLoop 从 transport 持续读取消息并按 ID 分发到 pending
func (c *ClientImpl) readLoop() {
	defer func() {
		// 让 Done() 的等待方解除阻塞（无论是正常 Disconnect 还是失联导致退出）
		c.doneMu.Lock()
		select {
		case <-c.done:
			// 已经被关闭（重复关闭是 panic）
		default:
			close(c.done)
		}
		c.doneMu.Unlock()
	}()
	for {
		// 用一个独立的 context（不可取消）让 transport 阻塞读
		// transport 在 Close() 时应返回错误，从而退出循环
		msg, err := c.transport.Recv(context.Background())
		if err != nil {
			c.recvErr.Store(err)
			c.failPending(err)
			return
		}
		if msg == nil {
			continue
		}
		// 通知（无 id）：派发到注册的处理器
		if !msg.IsResponse() {
			if msg.Method != "" {
				c.dispatchNotification(msg.Method, msg.Params)
			}
			continue
		}
		id, ok := parseIntID(msg.ID)
		if !ok {
			continue
		}
		c.pendingMu.Lock()
		ch, ok := c.pending[id]
		if ok {
			delete(c.pending, id)
		}
		c.pendingMu.Unlock()
		if ok {
			ch <- msg
			close(ch)
		}
	}
}

// dispatchNotification 把通知派发到注册的 handler
//
// 派发顺序：精确 method 匹配的 handler → wildcard("") handler。
// handler 在调用 goroutine 中同步执行；耗时操作请在 handler 内自行 go func。
func (c *ClientImpl) dispatchNotification(method string, params json.RawMessage) {
	c.notifMu.RLock()
	exact := c.notifHandler[method]
	wild := c.notifHandler[""]
	c.notifMu.RUnlock()
	if exact != nil {
		exact(method, params)
	}
	if wild != nil {
		wild(method, params)
	}
}

// failPending 在断开/读错误时让所有等待者收到失败响应
func (c *ClientImpl) failPending(err error) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	for id, ch := range c.pending {
		msg := &mcp.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      mustEncodeID(id),
			Error:   &mcp.JSONRPCError{Code: -32000, Message: err.Error()},
		}
		ch <- msg
		close(ch)
		delete(c.pending, id)
	}
}

// call 发起 JSON-RPC 请求并阻塞等待响应
func (c *ClientImpl) call(ctx context.Context, method string, params json.RawMessage) (*mcp.JSONRPCMessage, error) {
	if !c.IsConnected() && method != "initialize" {
		// initialize 时 connected 已是 true（在 Connect 中先设置）
		return nil, ErrNotConnected
	}
	id := c.nextID.Add(1)
	respCh := make(chan *mcp.JSONRPCMessage, 1)

	c.pendingMu.Lock()
	c.pending[id] = respCh
	c.pendingMu.Unlock()

	msg := &mcp.JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      mustEncodeID(id),
		Method:  method,
		Params:  params,
	}

	if err := c.transport.Send(ctx, msg); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("send: %w", err)
	}

	select {
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, ctx.Err()
	case resp := <-respCh:
		if resp.Error != nil {
			return nil, fmt.Errorf("rpc error (%d): %s", resp.Error.Code, resp.Error.Message)
		}
		return resp, nil
	}
}

// ListTools 获取工具列表
func (c *ClientImpl) ListTools(ctx context.Context) ([]mcp.ToolInfo, error) {
	resp, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Tools []mcp.ToolInfo `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

// CallTool 调用工具
func (c *ClientImpl) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.ToolCallResult, error) {
	params, _ := json.Marshal(map[string]interface{}{
		"name":      name,
		"arguments": args,
	})
	resp, err := c.call(ctx, "tools/call", params)
	if err != nil {
		return nil, err
	}
	var result mcp.ToolCallResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListResources 获取资源列表
func (c *ClientImpl) ListResources(ctx context.Context) ([]mcp.Resource, error) {
	resp, err := c.call(ctx, "resources/list", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Resources []mcp.Resource `json:"resources"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, err
	}
	return result.Resources, nil
}

// ReadResource 读取资源
func (c *ClientImpl) ReadResource(ctx context.Context, uri string) (*mcp.ResourceContent, error) {
	params, _ := json.Marshal(map[string]string{"uri": uri})
	resp, err := c.call(ctx, "resources/read", params)
	if err != nil {
		return nil, err
	}
	var result struct {
		Contents []mcp.ResourceContent `json:"contents"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, err
	}
	if len(result.Contents) == 0 {
		return nil, fmt.Errorf("no content returned for resource %s", uri)
	}
	return &result.Contents[0], nil
}

// ListPrompts 列出可用 prompt
func (c *ClientImpl) ListPrompts(ctx context.Context) ([]mcp.PromptInfo, error) {
	resp, err := c.call(ctx, "prompts/list", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Prompts []mcp.PromptInfo `json:"prompts"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, err
	}
	return result.Prompts, nil
}

// GetPrompt 获取 prompt 模板
func (c *ClientImpl) GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.PromptResult, error) {
	params, _ := json.Marshal(map[string]interface{}{
		"name":      name,
		"arguments": args,
	})
	resp, err := c.call(ctx, "prompts/get", params)
	if err != nil {
		return nil, err
	}
	var result mcp.PromptResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// 工具函数 ---------------------------------------------------------

func mustEncodeID(id int64) json.RawMessage {
	b, _ := json.Marshal(id)
	return b
}

func parseIntID(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, true
	}
	// 字符串 id 也接受，转 hash 不可行；这里只接受数字 id
	return 0, false
}
