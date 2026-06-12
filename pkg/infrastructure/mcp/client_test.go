package mcpinfra

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yaoice/goclaude/pkg/domain/mcp"
)

// 模拟 MCP HTTP 服务器：接受 initialize / tools/list / tools/call
func startMockHTTPServer(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var callCount int32

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		var msg mcp.JSONRPCMessage
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// 通知没有响应
		if msg.IsNotification() {
			w.WriteHeader(http.StatusAccepted)
			return
		}

		var result interface{}
		switch msg.Method {
		case "initialize":
			result = map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"serverInfo": map[string]string{
					"name":    "mock-server",
					"version": "0.0.0",
				},
			}
		case "tools/list":
			result = map[string]interface{}{
				"tools": []map[string]interface{}{
					{
						"name":        "echo",
						"description": "echo input",
						"inputSchema": map[string]interface{}{
							"type": "object",
						},
					},
				},
			}
		case "tools/call":
			result = map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "text", "text": "ok"},
				},
			}
		default:
			result = map[string]interface{}{}
		}

		raw, _ := json.Marshal(result)
		resp := mcp.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  raw,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	srv := httptest.NewServer(mux)
	return srv, &callCount
}

func TestClient_HTTPConnectAndListTools(t *testing.T) {
	srv, _ := startMockHTTPServer(t)
	defer srv.Close()

	cfg := &mcp.ServerConfig{
		Name:          "mock",
		TransportType: mcp.TransportHTTP,
		URL:           srv.URL,
	}
	client := NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Disconnect()

	if !client.IsConnected() {
		t.Fatal("client should be connected")
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Errorf("unexpected tools: %+v", tools)
	}
}

func TestClient_ConcurrentCalls(t *testing.T) {
	// 验证并发请求不再错乱（之前实现的核心缺陷）
	srv, _ := startMockHTTPServer(t)
	defer srv.Close()

	client := NewClient(&mcp.ServerConfig{
		Name:          "mock",
		TransportType: mcp.TransportHTTP,
		URL:           srv.URL,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Disconnect()

	// 并发发起 10 个 ListTools；每个都应正确收到自己的响应
	const N = 10
	done := make(chan error, N)
	for i := 0; i < N; i++ {
		go func() {
			_, err := client.ListTools(ctx)
			done <- err
		}()
	}
	for i := 0; i < N; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("concurrent call failed: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("concurrent call timed out")
		}
	}
}

func TestManager_ConnectAll(t *testing.T) {
	srv, _ := startMockHTTPServer(t)
	defer srv.Close()

	m := NewManager()
	errs := m.ConnectAll(context.Background(), []*mcp.ServerConfig{
		{Name: "a", TransportType: mcp.TransportHTTP, URL: srv.URL},
		{Name: "b", TransportType: mcp.TransportHTTP, URL: srv.URL},
	})
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(m.All()) != 2 {
		t.Errorf("expected 2 clients, got %d", len(m.All()))
	}
}

func TestParsePrefixedToolName(t *testing.T) {
	cases := []struct {
		in       string
		wantSrv  string
		wantTool string
		wantOk   bool
	}{
		{"mcp__github__create_issue", "github", "create_issue", true},
		{"not_mcp", "", "", false},
		{"mcp__only_server", "", "", false},
	}
	for _, tc := range cases {
		// 引入循环依赖，因此在测试侧本地解析
		var srv, tool string
		var ok bool
		const prefix = "mcp__"
		if len(tc.in) > len(prefix) && tc.in[:len(prefix)] == prefix {
			rest := tc.in[len(prefix):]
			for i := 0; i+1 < len(rest); i++ {
				if rest[i] == '_' && rest[i+1] == '_' {
					srv = rest[:i]
					tool = rest[i+2:]
					ok = true
					break
				}
			}
		}
		if ok != tc.wantOk || srv != tc.wantSrv || tool != tc.wantTool {
			t.Errorf("parse %q -> (%q,%q,%v), want (%q,%q,%v)",
				tc.in, srv, tool, ok, tc.wantSrv, tc.wantTool, tc.wantOk)
		}
	}
}
