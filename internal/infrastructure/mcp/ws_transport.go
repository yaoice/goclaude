package mcpinfra

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"

	"github.com/anthropics/goclaude/internal/domain/mcp"
	"github.com/anthropics/goclaude/pkg/wsclient"
)

// WSTransport 基于 WebSocket 的 MCP 传输（对齐 src TransportSchema.ws）
//
// 协议要点：
//   - WS 连接建立后双向收发 JSON-RPC 文本帧
//   - 客户端发送的每条消息 = 一个完整的 text frame
//   - 服务端→客户端同理；不依赖 newline 分隔
//   - 控制帧（ping/pong/close）由底层 wsclient 自动处理
type WSTransport struct {
	url     string
	headers map[string]string
	conn    *wsclient.Conn

	mu      sync.Mutex
	closeCh chan struct{}
	closed  bool
}

// NewWSTransport 创建 WebSocket 传输
func NewWSTransport(url string, headers map[string]string) *WSTransport {
	return &WSTransport{
		url:     url,
		headers: headers,
		closeCh: make(chan struct{}),
	}
}

// Start 建立 WS 连接
func (t *WSTransport) Start(ctx context.Context) error {
	if t.conn != nil {
		return nil
	}
	hdrs := http.Header{}
	for k, v := range t.headers {
		hdrs.Set(k, v)
	}
	// 部分服务端要求声明子协议（如 mcp）；这里加上以兼容
	if hdrs.Get("Sec-WebSocket-Protocol") == "" {
		hdrs.Set("Sec-WebSocket-Protocol", "mcp")
	}

	// 在 goroutine 中 dial 以支持 ctx 取消
	type dialResult struct {
		c   *wsclient.Conn
		err error
	}
	resCh := make(chan dialResult, 1)
	go func() {
		c, err := wsclient.Dial(t.url, hdrs)
		resCh <- dialResult{c, err}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case r := <-resCh:
		if r.err != nil {
			return r.err
		}
		t.conn = r.c
		return nil
	}
}

// Send 发送一条 JSON-RPC 消息
//
// 通过 goroutine + select 让 ctx 取消能尽快返回；底层 WS 写若仍在网络栈
// 中阻塞，goroutine 不会立即返回但调用方已被解除阻塞。
func (t *WSTransport) Send(ctx context.Context, msg *mcp.JSONRPCMessage) error {
	if t.conn == nil {
		return errors.New("ws transport not started")
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		done <- t.conn.WriteText(data)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	case <-t.closeCh:
		return io.EOF
	}
}

// Recv 接收一条 JSON-RPC 消息
func (t *WSTransport) Recv(_ context.Context) (*mcp.JSONRPCMessage, error) {
	if t.conn == nil {
		return nil, errors.New("ws transport not started")
	}
	for {
		select {
		case <-t.closeCh:
			return nil, io.EOF
		default:
		}
		data, err := t.conn.ReadText()
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			continue
		}
		var msg mcp.JSONRPCMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			// 跳过无法解析的帧（保持与 stdio/http 一致的宽容策略）
			continue
		}
		return &msg, nil
	}
}

// Close 关闭 WS 连接
func (t *WSTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	close(t.closeCh)
	if t.conn != nil {
		return t.conn.Close()
	}
	return nil
}
