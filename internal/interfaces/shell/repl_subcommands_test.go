package shell

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// 本测试覆盖 /skills /agents /mcp /tools 的带参子命令渲染（render*Cmd 纯函数）。
// 复用 dialogs_test.go 已有的 fake 管理器；仅为缺失的 ToolRegistryView 与
// MCP 错误路径补两个不冲突的小桩。useColor=false 让断言可读，并与
// NO_COLOR / 非 TTY 行为一致。

// fakeToolView 是 ToolRegistryView 的测试桩（dialogs_test.go 尚无）。
type fakeToolView struct {
	names  []string
	byName map[string]ToolInfo
}

func (f *fakeToolView) Names() []string { return f.names }
func (f *fakeToolView) Describe(name string) (ToolInfo, bool) {
	t, ok := f.byName[name]
	return t, ok
}

// errMcpMgr 仅用于覆盖 Tools() 返回 error 的分支。
type errMcpMgr struct{ err error }

func (errMcpMgr) Statuses() []MCPServerStatus { return nil }
func (m errMcpMgr) Tools(_ context.Context) ([]MCPToolInfo, error) {
	return nil, m.err
}

// 所有渲染输出必须用 CRLF（原始模式 PTY 约定），不得有裸 LF。
func assertCRLF(t *testing.T, text string) {
	t.Helper()
	stripped := strings.ReplaceAll(text, "\r\n", "")
	if strings.Contains(stripped, "\n") {
		t.Fatalf("output contains bare LF:\n%q", text)
	}
}

// ---- /skills ----

func TestRenderSkillsCmd_NotEnabled(t *testing.T) {
	r := &REPL{useColor: false}
	if got := r.renderSkillsCmd(nil); !strings.Contains(got, "skill 服务未启用") {
		t.Fatalf("want not-enabled hint, got %q", got)
	}
}

func TestRenderSkillsCmd_List(t *testing.T) {
	r := &REPL{useColor: false, Skills: &fakeSkillMgr{
		skills: []SkillInfo{
			{Name: "pdf", Description: "处理 PDF"},
			{Name: "xlsx", WhenToUse: "处理表格"}, // Description 为空时回落 WhenToUse
		},
	}}
	got := r.renderSkillsCmd(nil)
	for _, want := range []string{"pdf", "处理 PDF", "xlsx", "处理表格"} {
		if !strings.Contains(got, want) {
			t.Errorf("list missing %q in:\n%s", want, got)
		}
	}
	assertCRLF(t, got)
}

func TestRenderSkillsCmd_Body(t *testing.T) {
	// 既有 fakeSkillMgr.Render 对存在的 name 返回 "BODY of <name>"。
	r := &REPL{useColor: false, Skills: &fakeSkillMgr{skills: []SkillInfo{{Name: "pdf"}}}}
	got := r.renderSkillsCmd([]string{"pdf"})
	if !strings.Contains(got, "BODY of pdf") {
		t.Fatalf("body not rendered: %q", got)
	}
	assertCRLF(t, got)
}

func TestRenderSkillsCmd_NotFound(t *testing.T) {
	r := &REPL{useColor: false, Skills: &fakeSkillMgr{}}
	if got := r.renderSkillsCmd([]string{"missing"}); !strings.Contains(got, "未找到 skill：missing") {
		t.Fatalf("want not-found, got %q", got)
	}
}

// ---- /agents ----

func TestRenderAgentsCmd_List(t *testing.T) {
	r := &REPL{useColor: false, Agents: &fakeAgentMgr{
		agents: []AgentInfo{{AgentType: "researcher", WhenToUse: "做调研"}},
	}}
	got := r.renderAgentsCmd(nil)
	if !strings.Contains(got, "researcher") || !strings.Contains(got, "做调研") {
		t.Fatalf("list missing fields:\n%s", got)
	}
	assertCRLF(t, got)
}

func TestRenderAgentsCmd_Detail(t *testing.T) {
	r := &REPL{useColor: false, Agents: &fakeAgentMgr{
		agents: []AgentInfo{{
			AgentType:    "coder",
			Model:        "claude-sonnet",
			Tools:        []string{"file_edit", "bash"},
			WhenToUse:    "写代码",
			SystemPrompt: "You are a coder.\nBe concise.\n",
		}},
	}}
	got := r.renderAgentsCmd([]string{"coder"})
	for _, want := range []string{"agent: coder", "model: claude-sonnet", "file_edit, bash", "写代码", "You are a coder.\r\nBe concise."} {
		if !strings.Contains(got, want) {
			t.Errorf("detail missing %q in:\n%s", want, got)
		}
	}
	assertCRLF(t, got)
}

func TestRenderAgentsCmd_NotFound(t *testing.T) {
	r := &REPL{useColor: false, Agents: &fakeAgentMgr{}}
	if got := r.renderAgentsCmd([]string{"ghost"}); !strings.Contains(got, "未找到 agent：ghost") {
		t.Fatalf("want not-found, got %q", got)
	}
}

// ---- /mcp ----

func TestRenderMcpCmd_StatusDefault(t *testing.T) {
	r := &REPL{useColor: false, MCP: &fakeMcpMgr{
		servers: []MCPServerStatus{
			{Name: "github", Connected: true},
			{Name: "db", Connected: false, Error: "dial timeout"},
		},
	}}
	got := r.renderMcpCmd(nil) // 无参默认走 status
	for _, want := range []string{"github", "connected", "db", "disconnected", "dial timeout"} {
		if !strings.Contains(got, want) {
			t.Errorf("status missing %q in:\n%s", want, got)
		}
	}
	assertCRLF(t, got)
}

func TestRenderMcpCmd_Tools(t *testing.T) {
	r := &REPL{useColor: false, MCP: &fakeMcpMgr{
		tools: []MCPToolInfo{{Name: "mcp__github__list_issues", Description: "列出 issues"}},
	}}
	got := r.renderMcpCmd([]string{"tools"})
	if !strings.Contains(got, "mcp__github__list_issues") || !strings.Contains(got, "列出 issues") {
		t.Fatalf("tools missing fields:\n%s", got)
	}
}

func TestRenderMcpCmd_ToolsError(t *testing.T) {
	r := &REPL{useColor: false, MCP: errMcpMgr{err: errors.New("boom")}}
	got := r.renderMcpCmd([]string{"tools"})
	if !strings.Contains(got, "获取 MCP 工具失败") || !strings.Contains(got, "boom") {
		t.Fatalf("want error text, got %q", got)
	}
}

func TestRenderMcpCmd_NotEnabled(t *testing.T) {
	r := &REPL{useColor: false}
	if got := r.renderMcpCmd(nil); !strings.Contains(got, "MCP 未启用") {
		t.Fatalf("want not-enabled, got %q", got)
	}
}

// ---- /tools ----

func TestRenderToolsCmd_List(t *testing.T) {
	r := &REPL{useColor: false, Tools: &fakeToolView{
		names:  []string{"file_read", "bash"},
		byName: map[string]ToolInfo{"file_read": {Name: "file_read", Description: "读取文件"}},
	}}
	got := r.renderToolsCmd(nil)
	for _, want := range []string{"file_read", "读取文件", "bash"} {
		if !strings.Contains(got, want) {
			t.Errorf("list missing %q in:\n%s", want, got)
		}
	}
	assertCRLF(t, got)
}

func TestRenderToolsCmd_Detail(t *testing.T) {
	r := &REPL{useColor: false, Tools: &fakeToolView{
		byName: map[string]ToolInfo{"bash": {Name: "bash", Description: "run shell\nwith care"}},
	}}
	got := r.renderToolsCmd([]string{"bash"})
	if !strings.Contains(got, "tool: bash") || !strings.Contains(got, "run shell\r\nwith care") {
		t.Fatalf("detail wrong:\n%s", got)
	}
	assertCRLF(t, got)
}

func TestRenderToolsCmd_NotFound(t *testing.T) {
	r := &REPL{useColor: false, Tools: &fakeToolView{byName: map[string]ToolInfo{}}}
	if got := r.renderToolsCmd([]string{"nope"}); !strings.Contains(got, "未找到 tool：nope") {
		t.Fatalf("want not-found, got %q", got)
	}
}
