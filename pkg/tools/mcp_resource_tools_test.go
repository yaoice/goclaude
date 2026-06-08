package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/goclaude/pkg/application"
	"github.com/anthropics/goclaude/pkg/domain/mcp"
	"github.com/anthropics/goclaude/pkg/domain/tool"
)

func startMCPToolExtrasServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var msg mcp.JSONRPCMessage
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if msg.IsNotification() {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result interface{}
		switch msg.Method {
		case "initialize":
			result = map[string]interface{}{"protocolVersion": "2024-11-05"}
		case "tools/list":
			result = map[string]interface{}{"tools": []map[string]interface{}{}}
		case "resources/list":
			result = map[string]interface{}{"resources": []map[string]interface{}{{"uri": "file:///a.txt", "name": "a", "mimeType": "text/plain"}}}
		case "resources/read":
			result = map[string]interface{}{"contents": []map[string]interface{}{{"uri": "file:///a.txt", "mimeType": "text/plain", "text": "alpha"}}}
		case "prompts/list":
			result = map[string]interface{}{"prompts": []map[string]interface{}{{"name": "explain", "description": "explain topic"}}}
		case "prompts/get":
			result = map[string]interface{}{"messages": []map[string]interface{}{{"role": "user", "content": map[string]interface{}{"type": "text", "text": "Explain Go"}}}}
		default:
			result = map[string]interface{}{}
		}
		raw, _ := json.Marshal(result)
		_ = json.NewEncoder(w).Encode(mcp.JSONRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: raw})
	})
	return httptest.NewServer(mux)
}

func TestRegisterMCPTools_AddsResourceAndPromptHelpers(t *testing.T) {
	srv := startMCPToolExtrasServer(t)
	defer srv.Close()

	svc := application.NewMCPService(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := svc.ConnectOne(ctx, &mcp.ServerConfig{Name: "extras", TransportType: mcp.TransportHTTP, URL: srv.URL}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer svc.Shutdown()

	reg := tool.NewRegistry()
	if _, err := RegisterMCPTools(ctx, reg, svc); err != nil {
		t.Fatalf("register: %v", err)
	}
	for _, name := range []string{"list_mcp_resources", "read_mcp_resource", "get_mcp_prompt"} {
		if _, ok := reg.Get(name); !ok {
			t.Fatalf("helper tool %q not registered", name)
		}
	}

	readTool, _ := reg.Get("read_mcp_resource")
	res, err := readTool.Call(ctx, tool.Input{"server": "extras", "uri": "file:///a.txt"}, nil)
	if err != nil || res == nil || res.IsError || !strings.Contains(res.Content, "alpha") {
		t.Fatalf("read_mcp_resource failed: res=%+v err=%v", res, err)
	}

	promptTool, _ := reg.Get("get_mcp_prompt")
	res, err = promptTool.Call(ctx, tool.Input{"server": "extras", "name": "explain"}, nil)
	if err != nil || res == nil || res.IsError || !strings.Contains(res.Content, "Explain Go") {
		t.Fatalf("get_mcp_prompt failed: res=%+v err=%v", res, err)
	}
}
