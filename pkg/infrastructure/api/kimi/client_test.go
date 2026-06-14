package kimi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yaoice/goclaude/pkg/domain/query"
)

// TestBuildRequest_BasicConversion 验证 query.StreamParams -> ChatRequest 转换
func TestBuildRequest_BasicConversion(t *testing.T) {
	c := NewClient(DefaultClientConfig("test-key"))

	params := &query.StreamParams{
		Model:     "kimi-k2.6",
		MaxTokens: 1024,
		System: []query.ContentBlock{
			{Type: query.ContentTypeText, Text: "你是一个有用的助手"},
		},
		Messages: []query.Message{
			query.NewTextMessage(query.RoleUser, "你好"),
		},
	}

	req := c.buildRequest(params, true)

	if req.Model != "kimi-k2.6" {
		t.Errorf("expected model=kimi-k2.6, got %s", req.Model)
	}
	if !req.Stream {
		t.Errorf("expected stream=true")
	}
	if req.MaxTokens != 1024 {
		t.Errorf("expected max_tokens=1024, got %d", req.MaxTokens)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 messages (system+user), got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "system" || req.Messages[0].Content == nil || *req.Messages[0].Content != "你是一个有用的助手" {
		t.Errorf("system message wrong: %+v", req.Messages[0])
	}
	if req.Messages[1].Role != "user" || req.Messages[1].Content == nil || *req.Messages[1].Content != "你好" {
		t.Errorf("user message wrong: %+v", req.Messages[1])
	}
	if req.StreamOptions == nil || !req.StreamOptions.IncludeUsage {
		t.Errorf("stream_options.include_usage should be true")
	}
}

// TestBuildRequest_ToolCallRoundtrip 验证 tool_use 与 tool_result 的双向转换
func TestBuildRequest_ToolCallRoundtrip(t *testing.T) {
	c := NewClient(DefaultClientConfig("k"))

	params := &query.StreamParams{
		Model: "kimi-k2.6",
		Messages: []query.Message{
			query.NewTextMessage(query.RoleUser, "查询天气"),
			{
				Role: query.RoleAssistant,
				Content: []query.ContentBlock{
					{Type: query.ContentTypeText, Text: "好的，让我查询。"},
					query.NewToolUseBlock("call_1", "get_weather", map[string]string{"city": "Beijing"}),
				},
			},
			{
				Role: query.RoleUser,
				Content: []query.ContentBlock{
					query.NewToolResultBlock("call_1", "晴天 25°C", false),
				},
			},
		},
		Tools: []query.ToolDefinition{
			{
				Name:        "get_weather",
				Description: "获取天气",
				InputSchema: map[string]interface{}{"type": "object"},
			},
		},
		ToolChoice: &query.ToolChoice{Type: "auto"},
	}

	req := c.buildRequest(params, false)

	// 应有 3 条消息：user / assistant(含tool_calls) / tool
	if len(req.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "user" {
		t.Errorf("msg[0] should be user, got %s", req.Messages[0].Role)
	}
	asst := req.Messages[1]
	if asst.Role != "assistant" {
		t.Errorf("msg[1] should be assistant, got %s", asst.Role)
	}
	if len(asst.ToolCalls) != 1 || asst.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("tool_calls wrong: %+v", asst.ToolCalls)
	}
	// 工具调用参数应为合法 JSON
	var args map[string]string
	if err := json.Unmarshal([]byte(asst.ToolCalls[0].Function.Arguments), &args); err != nil {
		t.Errorf("invalid tool arguments JSON: %v", err)
	}
	if args["city"] != "Beijing" {
		t.Errorf("expected city=Beijing, got %s", args["city"])
	}

	tool := req.Messages[2]
	if tool.Role != "tool" || tool.ToolCallID != "call_1" || tool.Content == nil || *tool.Content != "晴天 25°C" {
		t.Errorf("tool result message wrong: %+v", tool)
	}

	// 工具定义
	if len(req.Tools) != 1 || req.Tools[0].Function.Name != "get_weather" {
		t.Errorf("tools wrong: %+v", req.Tools)
	}
	if req.ToolChoice != "auto" {
		t.Errorf("expected tool_choice=auto, got %v", req.ToolChoice)
	}
}

// TestBuildRequest_AssistantToolCallsOnly_HasContentField 回归测试
//
// 当 assistant 消息只发起 tool_calls、没有任何 text 时，
// 序列化后的 JSON 必须包含 content 字段（值为空串），
// 否则 OpenAI 兼容 API 服务端会报 `messages[i]: missing field content`。
func TestBuildRequest_AssistantToolCallsOnly_HasContentField(t *testing.T) {
	c := NewClient(DefaultClientConfig("k"))
	params := &query.StreamParams{
		Model: "kimi-k2.6",
		Messages: []query.Message{
			query.NewTextMessage(query.RoleUser, "查天气"),
			{
				Role: query.RoleAssistant,
				Content: []query.ContentBlock{
					// 仅一个 tool_use，没有任何 text
					query.NewToolUseBlock("call_1", "get_weather", map[string]string{"city": "Beijing"}),
				},
			},
			{
				Role: query.RoleUser,
				Content: []query.ContentBlock{
					query.NewToolResultBlock("call_1", "晴", false),
				},
			},
		},
	}
	req := c.buildRequest(params, false)

	asst := req.Messages[1]
	if asst.Role != "assistant" {
		t.Fatalf("msg[1] should be assistant, got %s", asst.Role)
	}
	if asst.Content == nil {
		t.Fatalf("assistant.Content is nil; API will reject with 'missing field content'")
	}
	if *asst.Content != "" {
		t.Errorf("expected empty content, got %q", *asst.Content)
	}

	// 真正序列化一遍，确认 JSON 里出现 "content" key
	raw, err := json.Marshal(asst)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"content"`) {
		t.Errorf("serialized assistant msg lacks content field: %s", raw)
	}
}

// TestResolveModel 验证模型名映射逻辑
func TestResolveModel(t *testing.T) {
	cases := map[string]string{
		"":                         ModelK2,
		"kimi-k2.6":                ModelK2,
		"claude-sonnet-4-20250514": ModelK2, // 非 kimi 名称回退
		"deepseek-chat":            ModelK2, // 非 kimi 名称回退
	}
	for in, want := range cases {
		if got := resolveModel(in); got != want {
			t.Errorf("resolveModel(%q): got %q, want %q", in, got, want)
		}
	}
}

// TestMapFinishReason 验证 finish_reason 映射
func TestMapFinishReason(t *testing.T) {
	cases := map[string]query.StopReason{
		"stop":           query.StopReasonEndTurn,
		"length":         query.StopReasonMaxTokens,
		"tool_calls":     query.StopReasonToolUse,
		"content_filter": query.StopReasonStopSeq,
	}
	for in, want := range cases {
		if got := mapFinishReason(in); got != want {
			t.Errorf("mapFinishReason(%q): got %q, want %q", in, got, want)
		}
	}
}

// TestSend_NonStreaming 使用 httptest 模拟非流式响应
func TestSend_NonStreaming(t *testing.T) {
	resp := ChatResponse{
		ID:    "chatcmpl-1",
		Model: "kimi-k2.6",
		Choices: []Choice{
			{
				Index: 0,
				Message: ChatMessage{
					Role:    "assistant",
					Content: strPtr("你好！"),
				},
				FinishReason: "stop",
			},
		},
		Usage: &Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing/invalid Authorization header: %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path != ChatCompletionsPath {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var body ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Stream {
			t.Errorf("Send() should not request streaming")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := DefaultClientConfig("test-key")
	cfg.BaseURL = srv.URL
	cfg.MaxRetries = 0
	c := NewClient(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	msg, usage, err := c.Send(ctx, &query.SendParams{
		Model:    "kimi-k2.6",
		Messages: []query.Message{query.NewTextMessage(query.RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("Send error: %v", err)
	}
	if msg.GetTextContent() != "你好！" {
		t.Errorf("expected text='你好！', got %q", msg.GetTextContent())
	}
	if usage == nil || usage.InputTokens != 5 || usage.OutputTokens != 3 {
		t.Errorf("unexpected usage: %+v", usage)
	}
}

// TestStream_TextEvents 使用 httptest 模拟 SSE 流，验证文本事件转换
func TestStream_TextEvents(t *testing.T) {
	chunks := []string{
		`{"id":"1","model":"kimi-k2.6","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"}}]}`,
		`{"id":"1","model":"kimi-k2.6","choices":[{"index":0,"delta":{"content":" world"}}]}`,
		`{"id":"1","model":"kimi-k2.6","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":2,"total_tokens":4}}`,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher, _ := w.(http.Flusher)
		for _, c := range chunks {
			io.WriteString(w, "data: "+c+"\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	cfg := DefaultClientConfig("k")
	cfg.BaseURL = srv.URL
	cfg.MaxRetries = 0
	c := NewClient(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	events, err := c.Stream(ctx, &query.StreamParams{
		Model:    "kimi-k2.6",
		Messages: []query.Message{query.NewTextMessage(query.RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}

	var (
		text          strings.Builder
		gotStart      bool
		gotStop       bool
		gotMsgStop    bool
		gotStopReason query.StopReason
		gotUsage      *query.Usage
	)
	for ev := range events {
		switch ev.Type {
		case query.EventMessageStart:
			gotStart = true
		case query.EventContentBlockDelta:
			if ev.Delta != nil {
				text.WriteString(ev.Delta.Text)
			}
		case query.EventMessageDelta:
			gotStopReason = ev.StopReason
			gotUsage = ev.Usage
		case query.EventMessageStop:
			gotMsgStop = true
		case query.EventContentBlockStop:
			gotStop = true
		}
	}

	if !gotStart {
		t.Errorf("missing MessageStart event")
	}
	if !gotStop {
		t.Errorf("missing ContentBlockStop event")
	}
	if !gotMsgStop {
		t.Errorf("missing MessageStop event")
	}
	if text.String() != "Hello world" {
		t.Errorf("expected text='Hello world', got %q", text.String())
	}
	if gotStopReason != query.StopReasonEndTurn {
		t.Errorf("expected stop_reason=end_turn, got %q", gotStopReason)
	}
	if gotUsage == nil || gotUsage.InputTokens != 2 || gotUsage.OutputTokens != 2 {
		t.Errorf("usage missing or wrong: %+v", gotUsage)
	}
}

// TestStream_ToolCalls 验证工具调用流式转换
func TestStream_ToolCalls(t *testing.T) {
	chunks := []string{
		`{"id":"1","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
		`{"id":"1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}}]}`,
		`{"id":"1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"BJ\"}"}}]}}]}`,
		`{"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher, _ := w.(http.Flusher)
		for _, c := range chunks {
			io.WriteString(w, "data: "+c+"\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	cfg := DefaultClientConfig("k")
	cfg.BaseURL = srv.URL
	cfg.MaxRetries = 0
	c := NewClient(cfg)

	events, err := c.Stream(context.Background(), &query.StreamParams{
		Model:    "kimi-k2.6",
		Messages: []query.Message{query.NewTextMessage(query.RoleUser, "weather")},
	})
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}

	var (
		toolStart  *query.ContentBlock
		argsBuffer strings.Builder
		stopReason query.StopReason
	)
	for ev := range events {
		switch ev.Type {
		case query.EventContentBlockStart:
			if ev.ContentBlock != nil && ev.ContentBlock.Type == query.ContentTypeToolUse {
				toolStart = ev.ContentBlock
			}
		case query.EventContentBlockDelta:
			if ev.Delta != nil && ev.Delta.PartialJSON != "" {
				argsBuffer.WriteString(ev.Delta.PartialJSON)
			}
		case query.EventMessageDelta:
			stopReason = ev.StopReason
		}
	}

	if toolStart == nil {
		t.Fatal("missing tool_use BlockStart event")
	}
	if toolStart.ToolUseID != "call_1" || toolStart.ToolName != "get_weather" {
		t.Errorf("tool block wrong: %+v", toolStart)
	}
	if got := argsBuffer.String(); got != `{"city":"BJ"}` {
		t.Errorf("aggregated args wrong: %q", got)
	}
	if stopReason != query.StopReasonToolUse {
		t.Errorf("expected stop=tool_use, got %q", stopReason)
	}
}

// TestParseErrorResponse 验证错误体解析
func TestParseErrorResponse(t *testing.T) {
	body := []byte(`{"error":{"message":"Invalid API key","type":"authentication_error","code":"invalid_request_error"}}`)
	err := parseErrorResponse(401, body)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Invalid API key") {
		t.Errorf("error message lost: %v", err)
	}
}
