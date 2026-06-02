package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/goclaude/internal/domain/mcp"
)

func startMCPExtrasServer(t *testing.T) *httptest.Server {
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
			result = map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"serverInfo":      map[string]string{"name": "extras", "version": "1.0.0"},
			}
		case "tools/list":
			result = map[string]interface{}{"tools": []map[string]interface{}{{
				"name": "lookup", "description": "lookup data", "inputSchema": map[string]interface{}{"type": "object"},
			}}}
		case "tools/call":
			result = map[string]interface{}{
				"content":           []map[string]interface{}{{"type": "text", "text": "lookup ok"}},
				"structuredContent": map[string]interface{}{"id": "42", "ok": true},
				"_meta":             map[string]interface{}{"trace": "abc"},
			}
		case "resources/list":
			result = map[string]interface{}{"resources": []map[string]interface{}{{
				"uri": "file:///note.md", "name": "note", "description": "test note", "mimeType": "text/markdown",
			}}}
		case "resources/read":
			result = map[string]interface{}{"contents": []map[string]interface{}{{
				"uri": "file:///note.md", "mimeType": "text/markdown", "text": "# Note\nbody",
			}}}
		case "prompts/list":
			result = map[string]interface{}{"prompts": []map[string]interface{}{{
				"name": "greet", "description": "greet a user", "arguments": []map[string]interface{}{{"name": "name", "required": true}},
			}}}
		case "prompts/get":
			var params struct {
				Arguments map[string]string `json:"arguments"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			result = map[string]interface{}{
				"description": "greeting prompt",
				"messages": []map[string]interface{}{{
					"role":    "user",
					"content": map[string]interface{}{"type": "text", "text": "Hello " + params.Arguments["name"]},
				}},
			}
		default:
			result = map[string]interface{}{}
		}
		raw, _ := json.Marshal(result)
		_ = json.NewEncoder(w).Encode(mcp.JSONRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: raw})
	})
	return httptest.NewServer(mux)
}

func TestMCPService_ResourcesPromptsAndStructuredToolResults(t *testing.T) {
	srv := startMCPExtrasServer(t)
	defer srv.Close()

	svc := NewMCPService(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := svc.ConnectOne(ctx, &mcp.ServerConfig{Name: "extras", TransportType: mcp.TransportHTTP, URL: srv.URL}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer svc.Shutdown()

	toolResult, err := svc.CallTool(ctx, "extras", "lookup", map[string]interface{}{"q": "x"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if toolResult.StructuredContent["id"] != "42" || toolResult.Meta["trace"] != "abc" {
		t.Fatalf("structured tool result not preserved: %+v", toolResult)
	}

	resources, err := svc.ListAllResources(ctx)
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	if len(resources) != 1 || resources[0].Server != "extras" || resources[0].Resource.URI != "file:///note.md" {
		t.Fatalf("unexpected resources: %+v", resources)
	}
	content, err := svc.ReadResource(ctx, "extras", "file:///note.md")
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if !strings.Contains(content.Text, "# Note") {
		t.Fatalf("resource text not returned: %+v", content)
	}

	prompts, err := svc.ListAllPrompts(ctx)
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	if len(prompts) != 1 || prompts[0].Server != "extras" || prompts[0].Prompt.Name != "greet" {
		t.Fatalf("unexpected prompts: %+v", prompts)
	}
	promptText, err := svc.GetPromptText(ctx, "extras", "greet", map[string]string{"name": "Ada"})
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	if !strings.Contains(promptText, "user: Hello Ada") {
		t.Fatalf("prompt text not rendered: %q", promptText)
	}
}
