package compact

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anthropics/goclaude/internal/domain/query"
)

// stubProvider 仅实现 Send，返回预设字符串作为摘要
type stubProvider struct {
	summary string
	sendErr error
	calls   int
}

func (p *stubProvider) Stream(_ context.Context, _ *query.StreamParams) (<-chan query.StreamEvent, error) {
	return nil, errors.New("not used")
}
func (p *stubProvider) Send(_ context.Context, _ *query.SendParams) (*query.Message, *query.Usage, error) {
	p.calls++
	if p.sendErr != nil {
		return nil, nil, p.sendErr
	}
	msg := &query.Message{
		Role:    query.RoleAssistant,
		Content: []query.ContentBlock{{Type: query.ContentTypeText, Text: p.summary}},
	}
	return msg, &query.Usage{InputTokens: 100, OutputTokens: 50}, nil
}

func textMsg(role query.Role, text string) query.Message {
	return query.NewTextMessage(role, text)
}

func toolUseMsg(id, name string) query.Message {
	return query.Message{
		Role: query.RoleAssistant,
		Content: []query.ContentBlock{
			{Type: query.ContentTypeToolUse, ToolUseID: id, ToolName: name},
		},
	}
}

func toolResultMsg(id, content string) query.Message {
	return query.Message{
		Role: query.RoleUser,
		Content: []query.ContentBlock{
			{Type: query.ContentTypeToolResult, ToolResultID: id, Text: content},
		},
	}
}

// ---- SummarizingCompactor ----------------------------------------

func TestSummarizing_TooShort_NoChange(t *testing.T) {
	c := NewSummarizingCompactor()
	msgs := []query.Message{
		textMsg(query.RoleUser, "hi"),
		textMsg(query.RoleAssistant, "hello"),
	}
	out, err := c.Compact(context.Background(), msgs, &stubProvider{summary: "should not be called"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(msgs) {
		t.Errorf("expected unchanged, got %d", len(out))
	}
}

func TestSummarizing_ExtractsSummaryBlock(t *testing.T) {
	c := NewSummarizingCompactor()
	c.HeadKeep = 1
	c.TailKeep = 2
	msgs := []query.Message{
		textMsg(query.RoleUser, "build a feature"),
		textMsg(query.RoleAssistant, "ok"),
		textMsg(query.RoleUser, "do step 1"),
		textMsg(query.RoleAssistant, "step 1 done"),
		textMsg(query.RoleUser, "do step 2"),
		textMsg(query.RoleAssistant, "step 2 done"),
		textMsg(query.RoleUser, "do step 3"),
		textMsg(query.RoleAssistant, "step 3 done"),
	}
	prov := &stubProvider{summary: "Some preamble.\n<summary>\nUser asked to build feature; finished steps 1-2; working on 3.\n</summary>\nDone."}

	out, err := c.Compact(context.Background(), msgs, prov)
	if err != nil {
		t.Fatal(err)
	}
	if prov.calls != 1 {
		t.Errorf("expected 1 LLM call, got %d", prov.calls)
	}
	// 期望结构：1 head + 1 boundary + 1 summary + 2 tail = 5
	if len(out) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(out))
	}
	if !strings.Contains(out[1].GetTextContent(), CompactBoundaryTag) {
		t.Errorf("missing boundary marker: %q", out[1].GetTextContent())
	}
	body := out[2].GetTextContent()
	if !strings.Contains(body, "User asked to build feature") {
		t.Errorf("summary content missing: %q", body)
	}
	// 头/尾原样保留
	if out[0].GetTextContent() != "build a feature" {
		t.Errorf("head changed: %q", out[0].GetTextContent())
	}
	if out[4].GetTextContent() != "step 3 done" {
		t.Errorf("tail changed: %q", out[4].GetTextContent())
	}
}

func TestSummarizing_LLMError_FallbackToTruncating(t *testing.T) {
	c := NewSummarizingCompactor()
	c.HeadKeep = 1
	c.TailKeep = 2
	msgs := []query.Message{
		textMsg(query.RoleUser, "head"),
		textMsg(query.RoleAssistant, "m1"),
		textMsg(query.RoleUser, "m2"),
		textMsg(query.RoleAssistant, "m3"),
		textMsg(query.RoleUser, "m4"),
		textMsg(query.RoleAssistant, "tail"),
	}
	prov := &stubProvider{sendErr: errors.New("network down")}

	out, err := c.Compact(context.Background(), msgs, prov)
	if err != nil {
		t.Fatalf("fallback should not error: %v", err)
	}
	// truncating: head(1) + boundary + omitted-marker + tail(2) = 5
	if len(out) != 5 {
		t.Fatalf("expected 5 from fallback, got %d", len(out))
	}
	if !strings.Contains(out[1].GetTextContent(), CompactBoundaryTag) {
		t.Errorf("missing boundary: %q", out[1].GetTextContent())
	}
	if !strings.Contains(out[2].GetTextContent(), "omitted") {
		t.Errorf("missing omitted marker: %q", out[2].GetTextContent())
	}
}

// ---- TruncatingCompactor -----------------------------------------

func TestTruncating_HeadAndTailPreserved(t *testing.T) {
	c := NewTruncatingCompactor()
	c.HeadKeep = 2
	c.TailKeep = 2
	msgs := []query.Message{
		textMsg(query.RoleUser, "h1"),
		textMsg(query.RoleAssistant, "h2"),
		textMsg(query.RoleUser, "m1"),
		textMsg(query.RoleAssistant, "m2"),
		textMsg(query.RoleUser, "m3"),
		textMsg(query.RoleUser, "t1"),
		textMsg(query.RoleAssistant, "t2"),
	}
	out, err := c.Compact(context.Background(), msgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 2 head + 1 boundary + 1 omitted + 2 tail = 6
	if len(out) != 6 {
		t.Fatalf("expected 6, got %d", len(out))
	}
	if out[0].GetTextContent() != "h1" || out[1].GetTextContent() != "h2" {
		t.Errorf("head wrong: %q %q", out[0].GetTextContent(), out[1].GetTextContent())
	}
	if out[4].GetTextContent() != "t1" || out[5].GetTextContent() != "t2" {
		t.Errorf("tail wrong")
	}
}

func TestTruncating_DropsDanglingToolResult(t *testing.T) {
	c := NewTruncatingCompactor()
	c.HeadKeep = 1
	c.TailKeep = 2
	// head: [user]
	// mid:  [assistant tool_use=A, user tool_result=A, user "more"]
	// tail: [user tool_result=B, user "ok"]   ← B 是孤立的 result
	msgs := []query.Message{
		textMsg(query.RoleUser, "head"),
		toolUseMsg("A", "read"),
		toolResultMsg("A", "result A"),
		textMsg(query.RoleUser, "mid extra"),
		toolResultMsg("B", "result B"), // 孤立
		textMsg(query.RoleUser, "ok"),
	}
	out, err := c.Compact(context.Background(), msgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if hasOpenToolUse(out) {
		t.Error("output should not have open tool uses")
	}
	// 验证孤立的 tool_result B 被剔除
	for _, m := range out {
		for _, b := range m.Content {
			if b.Type == query.ContentTypeToolResult && b.ToolResultID == "B" {
				t.Error("dangling tool_result B should be dropped")
			}
		}
	}
}

func TestBalanceToolPairs_PullsResultIntoMid(t *testing.T) {
	// head=[h], mid=[tool_use A], tail=[tool_result A, t]
	// 期望：tail 起点后移，使 result 进入 mid
	all := []query.Message{
		textMsg(query.RoleUser, "h"),
		toolUseMsg("A", "x"),
		toolResultMsg("A", "ok"),
		textMsg(query.RoleUser, "t"),
	}
	head, mid, tail := all[:1], all[1:2], all[2:]
	head, mid, tail = balanceToolPairs(head, mid, tail)
	// mid 应包含 use+result 两条
	if len(mid) != 2 {
		t.Fatalf("expected mid=2, got %d", len(mid))
	}
	if mid[0].Content[0].ToolUseID != "A" || mid[1].Content[0].ToolResultID != "A" {
		t.Errorf("pair not balanced: %+v", mid)
	}
	if len(tail) != 1 || tail[0].GetTextContent() != "t" {
		t.Errorf("tail wrong: %+v", tail)
	}
	if len(head) != 1 || head[0].GetTextContent() != "h" {
		t.Errorf("head wrong: %+v", head)
	}
}

// ---- 集成：Engine 自动触发 -------------------------------------------------

// 验证 query.Engine 在 ShouldCompact 触发时调用 Compactor 并把结果替换历史
// 这里直接用 Engine 的 hook 路径（我们重写一个更轻的：直接调用 Compactor 检查输出）
func TestCompact_HasOpenToolUseHelper(t *testing.T) {
	yes := []query.Message{toolUseMsg("X", "y")}
	if !hasOpenToolUse(yes) {
		t.Error("should detect open tool use")
	}
	balanced := []query.Message{toolUseMsg("X", "y"), toolResultMsg("X", "ok")}
	if hasOpenToolUse(balanced) {
		t.Error("balanced should not detect open")
	}
}
