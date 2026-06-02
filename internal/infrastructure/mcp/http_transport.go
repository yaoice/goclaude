package mcpinfra

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/anthropics/goclaude/internal/domain/mcp"
)

// HTTPTransport 基于 Streamable HTTP 的 MCP 传输
//
// 协议要点（对齐 MCP HTTP transport 规范）：
//   - 每个 client 请求 POST 到 URL，Content-Type: application/json
//   - 服务端 200 + application/json 返回单条响应
//   - 服务端也可能返回 text/event-stream（用于双向流），这里支持解析单事件
//   - Notifications 不期望响应；客户端发送后立即返回
//
// 由于 MCP 的 HTTP 模式下每个请求都自带响应，我们用一个无缓冲队列把
// 响应推到 Recv() 让上层的 readLoop 统一分发，保持与 stdio 一致的接口。
type HTTPTransport struct {
	url     string
	headers map[string]string
	client  *http.Client

	mu      sync.Mutex
	queue   chan *mcp.JSONRPCMessage
	closed  bool
	closeCh chan struct{}
}

// NewHTTPTransport 创建 HTTP 传输
func NewHTTPTransport(url string, headers map[string]string) *HTTPTransport {
	return &HTTPTransport{
		url:     url,
		headers: headers,
		client:  &http.Client{},
		queue:   make(chan *mcp.JSONRPCMessage, 32),
		closeCh: make(chan struct{}),
	}
}

// Start HTTP 模式无需独立连接握手
func (t *HTTPTransport) Start(ctx context.Context) error {
	return nil
}

// Send 发送 JSON-RPC 消息，若是请求则把响应推入 queue
func (t *HTTPTransport) Send(ctx context.Context, msg *mcp.JSONRPCMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}

	// 通知不需要响应（202 Accepted 或 204 No Content 都可能）
	if msg.IsNotification() {
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	go t.handleResponse(resp)
	return nil
}

// handleResponse 解析响应（JSON 或 SSE）并推入队列
func (t *HTTPTransport) handleResponse(resp *http.Response) {
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		t.tryPush(&mcp.JSONRPCMessage{
			JSONRPC: "2.0",
			Error: &mcp.JSONRPCError{
				Code:    resp.StatusCode,
				Message: fmt.Sprintf("http %d", resp.StatusCode),
			},
		})
		return
	}

	contentType := resp.Header.Get("Content-Type")
	if isEventStream(contentType) {
		t.parseSSE(resp.Body)
		return
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil || len(data) == 0 {
		return
	}
	var rpc mcp.JSONRPCMessage
	if err := json.Unmarshal(data, &rpc); err != nil {
		return
	}
	t.tryPush(&rpc)
}

// tryPush 尝试推入队列，若已关闭则丢弃
func (t *HTTPTransport) tryPush(msg *mcp.JSONRPCMessage) {
	select {
	case <-t.closeCh:
		return
	case t.queue <- msg:
	}
}

// Recv 从队列读取一条响应
func (t *HTTPTransport) Recv(ctx context.Context) (*mcp.JSONRPCMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.closeCh:
		return nil, io.EOF
	case msg := <-t.queue:
		return msg, nil
	}
}

// Close 标记关闭
func (t *HTTPTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	close(t.closeCh)
	return nil
}

// isEventStream 判断 Content-Type 是否为 SSE
func isEventStream(contentType string) bool {
	for i := 0; i < len(contentType); i++ {
		if contentType[i] == ';' {
			contentType = contentType[:i]
			break
		}
	}
	contentType = trimSpace(contentType)
	return contentType == "text/event-stream"
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
