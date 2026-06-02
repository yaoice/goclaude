package mcpinfra

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/anthropics/goclaude/internal/domain/mcp"
)

// SSETransport 基于 Server-Sent Events 的 MCP 传输（早期 MCP 双 endpoint 模式）
//
// 协议要点：
//   - GET URL with Accept: text/event-stream 建立事件流（接收响应/通知）
//   - 首条事件 "endpoint" 返回客户端 POST 端点 URL
//   - 客户端通过 POST 到该端点发送请求
type SSETransport struct {
	url        string
	headers    map[string]string
	postURL    string
	postURLCh  chan string
	httpClient *http.Client

	streamResp *http.Response

	mu      sync.Mutex
	queue   chan *mcp.JSONRPCMessage
	closed  bool
	closeCh chan struct{}
}

// NewSSETransport 创建 SSE 传输
func NewSSETransport(url string, headers map[string]string) *SSETransport {
	return &SSETransport{
		url:        url,
		headers:    headers,
		httpClient: &http.Client{},
		postURLCh:  make(chan string, 1),
		queue:      make(chan *mcp.JSONRPCMessage, 32),
		closeCh:    make(chan struct{}),
	}
}

// Start 建立 SSE 流
func (t *SSETransport) Start(ctx context.Context) error {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, t.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return fmt.Errorf("sse handshake: http %d", resp.StatusCode)
	}
	t.streamResp = resp
	go t.parseSSE(resp.Body)
	return nil
}

// Send 通过 POST 发送 JSON-RPC 消息
func (t *SSETransport) Send(ctx context.Context, msg *mcp.JSONRPCMessage) error {
	postURL, err := t.getPostURL(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("sse post: http %d", resp.StatusCode)
	}
	return nil
}

// Recv 从队列读响应
func (t *SSETransport) Recv(ctx context.Context) (*mcp.JSONRPCMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.closeCh:
		return nil, io.EOF
	case msg := <-t.queue:
		return msg, nil
	}
}

// Close 关闭 SSE 流
func (t *SSETransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	close(t.closeCh)
	if t.streamResp != nil {
		_ = t.streamResp.Body.Close()
	}
	return nil
}

// getPostURL 阻塞等待 SSE 事件流告知 endpoint
func (t *SSETransport) getPostURL(ctx context.Context) (string, error) {
	if t.postURL != "" {
		return t.postURL, nil
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-t.closeCh:
		return "", io.EOF
	case u := <-t.postURLCh:
		t.postURL = u
		return u, nil
	}
}

// parseSSE 解析 SSE 流，处理 endpoint 事件与 message 事件
//
// 流结束（连接断开 / EOF）时会主动 Close transport，让 Recv() 立即返回 io.EOF，
// 避免上层 readLoop 永远阻塞读取 queue。
func (t *SSETransport) parseSSE(body io.ReadCloser) {
	defer body.Close()
	defer func() {
		// 进入此函数说明流已结束；通知 Recv 释放阻塞
		t.mu.Lock()
		if !t.closed {
			t.closed = true
			close(t.closeCh)
		}
		t.mu.Unlock()
	}()
	reader := bufio.NewReaderSize(body, 64*1024)

	var (
		eventName string
		dataBuf   bytes.Buffer
	)
	flush := func() {
		defer func() {
			eventName = ""
			dataBuf.Reset()
		}()
		data := strings.TrimRight(dataBuf.String(), "\n")
		if data == "" {
			return
		}
		switch eventName {
		case "endpoint":
			// SSE 协议下首条事件提供 POST endpoint
			endpoint := strings.TrimSpace(data)
			// 支持相对路径
			if strings.HasPrefix(endpoint, "/") {
				endpoint = resolveURL(t.url, endpoint)
			}
			select {
			case t.postURLCh <- endpoint:
			default:
			}
		default:
			// 默认 "message" 事件：负载是 JSON-RPC
			var rpc mcp.JSONRPCMessage
			if err := json.Unmarshal([]byte(data), &rpc); err == nil {
				t.tryPush(&rpc)
			}
		}
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return
		}
		line = strings.TrimRight(line, "\r\n")

		// 空行表示事件结束
		if line == "" {
			flush()
			continue
		}
		// 注释
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			d := strings.TrimPrefix(line, "data:")
			d = strings.TrimPrefix(d, " ")
			dataBuf.WriteString(d)
			dataBuf.WriteByte('\n')
			continue
		}
		// 其它字段（id:/retry: 等）忽略
	}
}

func (t *SSETransport) tryPush(msg *mcp.JSONRPCMessage) {
	select {
	case <-t.closeCh:
		return
	case t.queue <- msg:
	}
}

// resolveURL 把相对路径拼接到 baseURL 的 host
func resolveURL(baseURL, rel string) string {
	idx := strings.Index(baseURL, "://")
	if idx < 0 {
		return rel
	}
	// 找到 host 后的第一个 /
	hostStart := idx + 3
	pathStart := strings.Index(baseURL[hostStart:], "/")
	if pathStart < 0 {
		return baseURL + rel
	}
	return baseURL[:hostStart+pathStart] + rel
}

// parseSSE 也供 HTTPTransport 使用（包级辅助）
func (t *HTTPTransport) parseSSE(body io.ReadCloser) {
	defer body.Close()
	reader := bufio.NewReaderSize(body, 64*1024)
	var (
		eventName string
		dataBuf   bytes.Buffer
	)
	flush := func() {
		defer func() {
			eventName = ""
			dataBuf.Reset()
		}()
		data := strings.TrimRight(dataBuf.String(), "\n")
		if data == "" {
			return
		}
		if eventName == "endpoint" {
			return // HTTPTransport 不使用 endpoint 事件
		}
		var rpc mcp.JSONRPCMessage
		if err := json.Unmarshal([]byte(data), &rpc); err == nil {
			t.tryPush(&rpc)
		}
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			d := strings.TrimPrefix(line, "data:")
			d = strings.TrimPrefix(d, " ")
			dataBuf.WriteString(d)
			dataBuf.WriteByte('\n')
			continue
		}
	}
}
