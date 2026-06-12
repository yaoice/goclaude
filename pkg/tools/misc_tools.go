package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/yaoice/goclaude/pkg/domain/tool"
)

// AskUserTool 向用户提问工具
//
// 通过 ToolUseContext.AskUser 回调把问题转发给上层 TUI/CLI；
// 上层未注入回调时该工具自我禁用，避免给模型一个永远拿不到回答的工具。
type AskUserTool struct{}

func NewAskUserTool() *AskUserTool { return &AskUserTool{} }

func (t *AskUserTool) Name() string                        { return "ask_user" }
func (t *AskUserTool) Aliases() []string                   { return nil }
func (t *AskUserTool) Description() string                 { return "向用户提出问题并等待回答。" }
func (t *AskUserTool) IsEnabled() bool                     { return true }
func (t *AskUserTool) IsReadOnly(_ tool.Input) bool        { return true }
func (t *AskUserTool) IsConcurrencySafe(_ tool.Input) bool { return false }
func (t *AskUserTool) Prompt() string                      { return "" }
func (t *AskUserTool) ValidateInput(input tool.Input) error {
	if strings.TrimSpace(input.GetString("question")) == "" {
		return fmt.Errorf("question is required")
	}
	return nil
}
func (t *AskUserTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"question": map[string]interface{}{"type": "string", "description": "要向用户提出的问题"},
		},
		"required": []string{"question"},
	}
}
func (t *AskUserTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}
func (t *AskUserTool) Call(ctx context.Context, input tool.Input, useCtx *tool.UseContext) (*tool.Result, error) {
	if useCtx == nil || useCtx.AskUser == nil {
		return tool.NewErrorResult("ask_user tool unavailable: no UI to forward question to"), nil
	}
	question := input.GetString("question")
	answer, err := useCtx.AskUser(ctx, question)
	if err != nil {
		return tool.NewErrorResult(fmt.Sprintf("ask_user failed: %v", err)), nil
	}
	return tool.NewResult(answer), nil
}

// TodoWriteTool 通过注入的 TodoStore 实际持久化 todo 列表
type TodoWriteTool struct{}

func NewTodoWriteTool() *TodoWriteTool { return &TodoWriteTool{} }

func (t *TodoWriteTool) Name() string                        { return "todo_write" }
func (t *TodoWriteTool) Aliases() []string                   { return nil }
func (t *TodoWriteTool) Description() string                 { return "创建或更新任务列表。" }
func (t *TodoWriteTool) IsEnabled() bool                     { return true }
func (t *TodoWriteTool) IsReadOnly(_ tool.Input) bool        { return false }
func (t *TodoWriteTool) IsConcurrencySafe(_ tool.Input) bool { return false }
func (t *TodoWriteTool) Prompt() string                      { return "" }
func (t *TodoWriteTool) ValidateInput(input tool.Input) error {
	if strings.TrimSpace(input.GetString("todos")) == "" {
		return fmt.Errorf("todos is required (JSON array string)")
	}
	return nil
}
func (t *TodoWriteTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"todos": map[string]interface{}{"type": "string", "description": "JSON 格式的 todo 列表"},
			"merge": map[string]interface{}{"type": "boolean", "description": "是否合并到现有列表"},
		},
		"required": []string{"todos"},
	}
}
func (t *TodoWriteTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}
func (t *TodoWriteTool) Call(ctx context.Context, input tool.Input, useCtx *tool.UseContext) (*tool.Result, error) {
	if useCtx == nil || useCtx.TodoStore == nil {
		return tool.NewErrorResult("todo_write unavailable: no TodoStore configured"), nil
	}
	if err := useCtx.TodoStore.Write(ctx, useCtx.SessionID, input.GetString("todos"), input.GetBool("merge")); err != nil {
		return tool.NewErrorResult(fmt.Sprintf("todo_write failed: %v", err)), nil
	}
	return tool.NewResult("Todos updated"), nil
}

// WebSearchTool 通过注入的 WebSearchBackend 调用搜索；后端不存在时自我禁用
type WebSearchTool struct{}

func NewWebSearchTool() *WebSearchTool { return &WebSearchTool{} }

func (t *WebSearchTool) Name() string                        { return "web_search" }
func (t *WebSearchTool) Aliases() []string                   { return nil }
func (t *WebSearchTool) Description() string                 { return "搜索网络获取实时信息。" }
func (t *WebSearchTool) IsEnabled() bool                     { return true }
func (t *WebSearchTool) IsReadOnly(_ tool.Input) bool        { return true }
func (t *WebSearchTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *WebSearchTool) Prompt() string                      { return "" }
func (t *WebSearchTool) ValidateInput(input tool.Input) error {
	if strings.TrimSpace(input.GetString("query")) == "" {
		return fmt.Errorf("query is required")
	}
	return nil
}
func (t *WebSearchTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query":       map[string]interface{}{"type": "string", "description": "搜索关键词"},
			"max_results": map[string]interface{}{"type": "integer", "description": "最大结果数（默认 5）"},
		},
		"required": []string{"query"},
	}
}
func (t *WebSearchTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}
func (t *WebSearchTool) Call(ctx context.Context, input tool.Input, useCtx *tool.UseContext) (*tool.Result, error) {
	if useCtx == nil || useCtx.WebSearch == nil {
		return tool.NewErrorResult("web_search unavailable: no search backend configured (set WEB_SEARCH_API_KEY or inject WebSearchBackend)"), nil
	}
	max := input.GetInt("max_results")
	if max <= 0 {
		max = 5
	}
	out, err := useCtx.WebSearch.Search(ctx, input.GetString("query"), max)
	if err != nil {
		return tool.NewErrorResult(fmt.Sprintf("web_search failed: %v", err)), nil
	}
	return tool.NewResult(out), nil
}

// WebFetchTool 实际抓取 URL 并把 HTML 转为纯文本（零依赖实现）
type WebFetchTool struct {
	client *http.Client
}

// NewWebFetchTool 构造默认 30s 超时的 fetcher
func NewWebFetchTool() *WebFetchTool {
	return &WebFetchTool{
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (t *WebFetchTool) Name() string                        { return "web_fetch" }
func (t *WebFetchTool) Aliases() []string                   { return nil }
func (t *WebFetchTool) Description() string                 { return "获取 URL 内容并转换为纯文本。" }
func (t *WebFetchTool) IsEnabled() bool                     { return true }
func (t *WebFetchTool) IsReadOnly(_ tool.Input) bool        { return true }
func (t *WebFetchTool) IsConcurrencySafe(_ tool.Input) bool { return true }
func (t *WebFetchTool) Prompt() string                      { return "" }
func (t *WebFetchTool) ValidateInput(input tool.Input) error {
	url := strings.TrimSpace(input.GetString("url"))
	if url == "" {
		return fmt.Errorf("url is required")
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("url must start with http:// or https://")
	}
	return nil
}
func (t *WebFetchTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url":       map[string]interface{}{"type": "string", "description": "要获取的 URL（http/https）"},
			"max_bytes": map[string]interface{}{"type": "integer", "description": "最大字节数（默认 1MB）"},
		},
		"required": []string{"url"},
	}
}
func (t *WebFetchTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}
func (t *WebFetchTool) Call(ctx context.Context, input tool.Input, _ *tool.UseContext) (*tool.Result, error) {
	url := input.GetString("url")
	maxBytes := input.GetInt("max_bytes")
	if maxBytes <= 0 {
		maxBytes = 1024 * 1024
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return tool.NewErrorResult(fmt.Sprintf("invalid url: %v", err)), nil
	}
	req.Header.Set("User-Agent", "goclaude/1.0")
	req.Header.Set("Accept", "text/html,text/plain,application/xhtml+xml,*/*")

	resp, err := t.client.Do(req)
	if err != nil {
		return tool.NewErrorResult(fmt.Sprintf("fetch failed: %v", err)), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return tool.NewErrorResult(fmt.Sprintf("http %d", resp.StatusCode)), nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)))
	if err != nil {
		return tool.NewErrorResult(fmt.Sprintf("read body: %v", err)), nil
	}

	contentType := resp.Header.Get("Content-Type")
	text := body
	if isHTML(contentType) {
		text = []byte(htmlToText(string(body)))
	}

	out := tool.NewResult(string(text))
	out.WithMetadata("status", resp.StatusCode)
	out.WithMetadata("content_type", contentType)
	out.WithMetadata("bytes", len(body))
	return out, nil
}

// isHTML 粗判断 Content-Type 是否为 HTML
func isHTML(contentType string) bool {
	c := strings.ToLower(contentType)
	return strings.Contains(c, "text/html") || strings.Contains(c, "application/xhtml")
}

// htmlToText 极简 HTML→text：剥离 <script>/<style>/标签，折叠空白
//
// 不依赖外部库；够用于 LLM 抓取上下文（不追求像 readability 那样的语义提取）。
func htmlToText(html string) string {
	// 1. 干掉 script / style 完整块
	html = stripBlock(html, "<script", "</script>")
	html = stripBlock(html, "<style", "</style>")
	html = stripBlock(html, "<!--", "-->")

	// 2. 把 <br>/<p>/<div>/<li> 等块级标签换成换行
	for _, tag := range []string{"<br", "<p", "</p", "<div", "</div", "<li", "</li", "<h1", "</h1", "<h2", "</h2", "<h3", "</h3", "<tr", "</tr"} {
		html = strings.ReplaceAll(html, tag, "\n"+tag)
	}

	// 3. 剥离所有标签
	var sb strings.Builder
	inTag := false
	for _, r := range html {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			sb.WriteRune(r)
		}
	}

	// 4. 解码常见 entity
	text := sb.String()
	for k, v := range map[string]string{
		"&amp;":  "&",
		"&lt;":   "<",
		"&gt;":   ">",
		"&quot;": `"`,
		"&#39;":  "'",
		"&nbsp;": " ",
	} {
		text = strings.ReplaceAll(text, k, v)
	}

	// 5. 折叠连续空白行
	lines := strings.Split(text, "\n")
	var out []string
	prevBlank := false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			if prevBlank {
				continue
			}
			prevBlank = true
			out = append(out, "")
		} else {
			prevBlank = false
			out = append(out, t)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// stripBlock 删除从 start 到 end（含）的所有匹配区段（大小写不敏感）
func stripBlock(s, start, end string) string {
	lower := strings.ToLower(s)
	startLow := strings.ToLower(start)
	endLow := strings.ToLower(end)
	for {
		i := strings.Index(lower, startLow)
		if i < 0 {
			break
		}
		j := strings.Index(lower[i:], endLow)
		if j < 0 {
			// 没找到对应 end：删到字符串末尾以防解析错乱
			s = s[:i]
			break
		}
		j += len(endLow)
		s = s[:i] + s[i+j:]
		lower = strings.ToLower(s)
	}
	return s
}
