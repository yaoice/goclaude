package shell

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// fakeSkillMgr 测试桩
type fakeSkillMgr struct {
	skills []SkillInfo
}

func (f *fakeSkillMgr) List() []SkillInfo { return f.skills }
func (f *fakeSkillMgr) Render(name string) (string, bool) {
	for _, s := range f.skills {
		if s.Name == name {
			return "BODY of " + name, true
		}
	}
	return "", false
}

func TestSkillsDialog_GroupingAndFlat(t *testing.T) {
	mgr := &fakeSkillMgr{
		skills: []SkillInfo{
			{Name: "alpha", Source: "user", Description: "first"},
			{Name: "beta", Source: "project", Description: "second"},
			{Name: "zeta", Source: "user", Description: "third"},
		},
	}
	m := newSkillsDialogModel(mgr)
	if len(m.skills) != 3 {
		t.Fatalf("expect 3 flat, got %d", len(m.skills))
	}
	// user 组应该排在 project 之前（与 src 顺序：user → project）
	if m.skills[0].Source != "user" || m.skills[1].Source != "user" {
		t.Fatalf("user group should be first; got order: %+v", m.skills)
	}
	if m.skills[2].Source != "project" {
		t.Fatalf("project group should be last; got: %+v", m.skills)
	}
	// 组内排序：alpha 在 zeta 前
	if m.skills[0].Name != "alpha" || m.skills[1].Name != "zeta" {
		t.Fatalf("intra-group sort failed: %+v", m.skills)
	}
	// 分组首项索引：[0, 2]
	if len(m.groupsAt) != 2 || m.groupsAt[0] != 0 || m.groupsAt[1] != 2 {
		t.Fatalf("groupsAt: %v", m.groupsAt)
	}
}

func TestSkillsDialog_NavigationAndDetail(t *testing.T) {
	mgr := &fakeSkillMgr{
		skills: []SkillInfo{
			{Name: "alpha", Source: "user"},
			{Name: "beta", Source: "user"},
		},
	}
	m := newSkillsDialogModel(mgr)

	// 初始 cursor=0, view=0
	if m.cursor != 0 || m.view != 0 {
		t.Fatal("initial state wrong")
	}

	// ↓ 移到第二项
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = mm.(skillsDialogModel)
	if m.cursor != 1 {
		t.Fatalf("cursor=%d", m.cursor)
	}

	// Enter 进入详情
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(skillsDialogModel)
	if m.view != 1 {
		t.Fatalf("expect detail view, got %d", m.view)
	}
	if m.detailContent != "BODY of beta" {
		t.Fatalf("detail body: %q", m.detailContent)
	}

	// View 字符串里应包含 "Skill: beta"
	if got := m.View(); !contains2(got, "Skill: beta") {
		t.Fatalf("detail view missing title: %s", got)
	}

	// Esc 返回列表
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(skillsDialogModel)
	if m.view != 0 {
		t.Fatal("Esc did not return to list")
	}
}

func TestSkillsDialog_EmptyState(t *testing.T) {
	mgr := &fakeSkillMgr{}
	m := newSkillsDialogModel(mgr)
	v := m.View()
	if !contains2(v, "Create skills in") {
		t.Fatalf("empty state text missing: %s", v)
	}
}

// fakeAgentMgr
type fakeAgentMgr struct {
	agents []AgentInfo
}

func (f *fakeAgentMgr) List() []AgentInfo { return f.agents }
func (f *fakeAgentMgr) Get(t string) (AgentInfo, bool) {
	for _, a := range f.agents {
		if a.AgentType == t {
			return a, true
		}
	}
	return AgentInfo{}, false
}

func TestAgentsDialog_NavigationAndDetail(t *testing.T) {
	mgr := &fakeAgentMgr{
		agents: []AgentInfo{
			{AgentType: "code-reviewer", Source: "builtin", Model: "sonnet", WhenToUse: "after coding"},
			{AgentType: "test-writer", Source: "user", Model: "haiku"},
		},
	}
	m := newAgentsDialogModel(mgr)
	// builtin 在 user 之前
	if m.agents[0].Source != "builtin" {
		t.Fatalf("builtin should come first")
	}
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(agentsDialogModel)
	if m.view != 1 {
		t.Fatal("expect detail view")
	}
	if !contains2(m.View(), "code-reviewer") {
		t.Fatal("detail missing agent name")
	}
}

// fakeMcpMgr
type fakeMcpMgr struct {
	servers []MCPServerStatus
	tools   []MCPToolInfo
}

func (f *fakeMcpMgr) Statuses() []MCPServerStatus                    { return f.servers }
func (f *fakeMcpMgr) Tools(_ context.Context) ([]MCPToolInfo, error) { return f.tools, nil }

func TestMcpDialog_StateMachine(t *testing.T) {
	mgr := &fakeMcpMgr{
		servers: []MCPServerStatus{
			{Name: "github", Connected: true},
			{Name: "local", Connected: false, Error: "spawn failed"},
		},
		tools: []MCPToolInfo{
			{Server: "github", Name: "mcp__github__list_issues", Description: "list issues"},
			{Server: "github", Name: "mcp__github__create_pr"},
		},
	}
	m := newMcpDialogModel(mgr)
	// 设置窗口尺寸以确保 viewport 有足够高度渲染内容
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = mm.(mcpDialogModel)
	// 注入数据（模拟 init 完成）
	mm, _ = m.Update(mcpDataMsg{servers: mgr.servers, tools: mgr.tools})
	m = mm.(mcpDialogModel)

	// list 视图 → enter 进 server-menu
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(mcpDialogModel)
	if m.view != mcpViewServerMenu {
		t.Fatalf("expect server-menu, got %d", m.view)
	}
	if m.server != "github" {
		t.Fatalf("server=%s", m.server)
	}

	// menuIdx=1 (View tools) → enter
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = mm.(mcpDialogModel)
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(mcpDialogModel)
	if m.view != mcpViewServerTools {
		t.Fatalf("expect server-tools, got %d", m.view)
	}

	// 工具列表非空
	if got := m.View(); !contains2(got, "list_issues") {
		t.Fatalf("tools view: %s", got)
	}

	// enter 进 tool-detail
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(mcpDialogModel)
	if m.view != mcpViewToolDetail {
		t.Fatalf("expect tool-detail, got %d", m.view)
	}
	if !contains2(m.View(), "mcp__github__create_pr") {
		t.Fatalf("tool-detail view: %s", m.View())
	}

	// Esc 逐级回退：tool-detail → server-tools → server-menu → list
	for _, expect := range []mcpView{mcpViewServerTools, mcpViewServerMenu, mcpViewList} {
		mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		m = mm.(mcpDialogModel)
		if m.view != expect {
			t.Fatalf("after Esc expect %d, got %d", expect, m.view)
		}
	}
}

func TestMcpDialog_StripPrefix(t *testing.T) {
	if got := stripMcpPrefix("mcp__github__foo", "github"); got != "foo" {
		t.Fatalf("got %q", got)
	}
	if got := stripMcpPrefix("plain_name", "github"); got != "plain_name" {
		t.Fatalf("got %q", got)
	}
}

// contains2: 避免与已有 contains 冲突
func contains2(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOfSub(s, sub) >= 0)
}

func indexOfSub(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
