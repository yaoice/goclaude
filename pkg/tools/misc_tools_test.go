package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/goclaude/pkg/domain/tool"
)

// ---- WebFetchTool -----------------------------------------------------------

func TestWebFetch_HTMLToText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html>
  <head><title>T</title>
    <style>body { color: red }</style>
    <script>alert('xss')</script>
  </head>
  <body>
    <h1>Hello</h1>
    <p>世界 &amp; world</p>
    <p>line2</p>
  </body>
</html>`)
	}))
	defer srv.Close()

	wf := NewWebFetchTool()
	res, err := wf.Call(context.Background(), tool.Input{"url": srv.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Content)
	}
	got := res.Content
	if strings.Contains(got, "alert") || strings.Contains(got, "color: red") {
		t.Errorf("script/style 未被剥离: %q", got)
	}
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "世界 & world") {
		t.Errorf("正文丢失或 entity 未解码: %q", got)
	}
}

func TestWebFetch_NonHTML_ReturnedAsIs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok": true}`)
	}))
	defer srv.Close()

	wf := NewWebFetchTool()
	res, err := wf.Call(context.Background(), tool.Input{"url": srv.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, `"ok"`) {
		t.Errorf("got %q", res.Content)
	}
}

func TestWebFetch_ValidateInput(t *testing.T) {
	wf := NewWebFetchTool()
	cases := []struct {
		input   tool.Input
		wantErr bool
	}{
		{tool.Input{"url": "https://example.com"}, false},
		{tool.Input{"url": ""}, true},
		{tool.Input{"url": "ftp://x"}, true},
		{tool.Input{}, true},
	}
	for _, tc := range cases {
		err := wf.ValidateInput(tc.input)
		if (err != nil) != tc.wantErr {
			t.Errorf("input=%v err=%v wantErr=%v", tc.input, err, tc.wantErr)
		}
	}
}

func TestWebFetch_4xxErrorReturned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()

	wf := NewWebFetchTool()
	res, _ := wf.Call(context.Background(), tool.Input{"url": srv.URL}, nil)
	if !res.IsError {
		t.Errorf("expected error result, got %+v", res)
	}
	if !strings.Contains(res.Content, "403") {
		t.Errorf("expected 403 in error: %q", res.Content)
	}
}

// ---- AskUserTool ------------------------------------------------------------

func TestAskUser_NoCallbackReturnsError(t *testing.T) {
	tt := NewAskUserTool()
	// useCtx 为 nil 时应返回 tool error 而非阻塞或 panic
	res, err := tt.Call(context.Background(), tool.Input{"question": "?"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("expected error result when no UI callback configured")
	}
}

func TestAskUser_WithCallback(t *testing.T) {
	tt := NewAskUserTool()
	useCtx := &tool.UseContext{
		AskUser: func(_ context.Context, q string) (string, error) {
			return "got: " + q, nil
		},
	}
	res, err := tt.Call(context.Background(), tool.Input{"question": "what?"}, useCtx)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || res.Content != "got: what?" {
		t.Errorf("unexpected: %+v", res)
	}
}

// ---- TodoWriteTool ----------------------------------------------------------

type fakeTodoStore struct {
	last  string
	merge bool
}

func (s *fakeTodoStore) Write(_ context.Context, _, todos string, merge bool) error {
	s.last = todos
	s.merge = merge
	return nil
}
func (s *fakeTodoStore) Read(_ context.Context, _ string) (string, error) { return s.last, nil }

func TestTodoWrite_RoutesToStore(t *testing.T) {
	store := &fakeTodoStore{}
	tt := NewTodoWriteTool()
	res, _ := tt.Call(context.Background(), tool.Input{
		"todos": `[{"id":"1","status":"in_progress","content":"x"}]`,
		"merge": true,
	}, &tool.UseContext{TodoStore: store})

	if res.IsError {
		t.Fatalf("got error: %s", res.Content)
	}
	if !strings.Contains(store.last, "in_progress") {
		t.Errorf("store.last = %q", store.last)
	}
	if !store.merge {
		t.Error("merge flag lost")
	}
}

func TestTodoWrite_NoStoreReturnsError(t *testing.T) {
	tt := NewTodoWriteTool()
	res, _ := tt.Call(context.Background(), tool.Input{"todos": "[]"}, nil)
	if !res.IsError {
		t.Error("expected error when no TodoStore")
	}
}

// ---- WebSearchTool ----------------------------------------------------------

type fakeSearch struct{ called int }

func (s *fakeSearch) Search(_ context.Context, q string, n int) (string, error) {
	s.called++
	return fmt.Sprintf("results for %q (max %d)", q, n), nil
}

func TestWebSearch_RoutesToBackend(t *testing.T) {
	be := &fakeSearch{}
	tt := NewWebSearchTool()
	res, _ := tt.Call(context.Background(), tool.Input{
		"query":       "go testing",
		"max_results": 3,
	}, &tool.UseContext{WebSearch: be})

	if res.IsError {
		t.Fatalf("got error: %s", res.Content)
	}
	if !strings.Contains(res.Content, `results for "go testing"`) {
		t.Errorf("got %q", res.Content)
	}
	if be.called != 1 {
		t.Errorf("backend called %d times", be.called)
	}
}

func TestWebSearch_NoBackendReturnsError(t *testing.T) {
	tt := NewWebSearchTool()
	res, _ := tt.Call(context.Background(), tool.Input{"query": "x"}, nil)
	if !res.IsError {
		t.Error("expected error when no backend")
	}
}
