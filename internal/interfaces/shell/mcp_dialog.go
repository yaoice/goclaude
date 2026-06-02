// Package shell - mcp_dialog.go
//
// 与 src `components/mcp/MCPSettings.tsx` 对齐的 McpDialog。
//
// 视图状态机（与 src viewState.type 一致）：
//
//	list             ← 入口；显示所有 server + 状态
//	server-menu      ← 选中 server 后的二级菜单（Reconnect / Tools）
//	server-tools     ← 单个 server 的工具列表
//	server-tool-detail← 单个工具的描述/schema 预览（支持滚动）
package shell

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type mcpView int

const (
	mcpViewList mcpView = iota
	mcpViewServerMenu
	mcpViewServerTools
	mcpViewToolDetail
)

type mcpDialogModel struct {
	mgr      MCPManager
	servers  []MCPServerStatus
	allTools []MCPToolInfo // 一次性拉取
	toolsErr error

	view    mcpView
	cursor  int         // 当前视图内的光标
	server  string      // 选中的 server 名（server-menu / server-tools 用）
	menuIdx int         // server-menu 内的光标
	toolIdx int         // server-tools 内的光标
	tool    MCPToolInfo // 选中的 tool
	notice  string      // 操作反馈（如 "reconnected"）

	width, height int

	// viewport for detail view scrolling
	viewport dialogViewport
}

func newMcpDialogModel(mgr MCPManager) mcpDialogModel {
	return mcpDialogModel{mgr: mgr}
}

// loadCmd 异步拉取 server 状态 + 所有工具
type mcpDataMsg struct {
	servers []MCPServerStatus
	tools   []MCPToolInfo
	err     error
}

func (m mcpDialogModel) loadCmd() tea.Msg {
	servers := m.mgr.Statuses()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tools, err := m.mgr.Tools(ctx)
	return mcpDataMsg{servers: servers, tools: tools, err: err}
}

func (m mcpDialogModel) Init() tea.Cmd {
	return func() tea.Msg { return m.loadCmd() }
}

func (m mcpDialogModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		vpHeight := m.height - 4
		if vpHeight < 1 {
			vpHeight = 1
		}
		m.viewport.SetSize(m.width, vpHeight)
		if m.view == mcpViewToolDetail {
			m.viewport.SetContent(m.buildToolDetailContent())
		}
	case mcpDataMsg:
		m.servers = msg.servers
		m.allTools = msg.tools
		m.toolsErr = msg.err
		// 排序
		sort.Slice(m.servers, func(i, j int) bool { return m.servers[i].Name < m.servers[j].Name })
	case tea.MouseMsg:
		if m.view == mcpViewToolDetail {
			m.viewport.Update(msg)
			return m, nil
		}
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m mcpDialogModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc":
		switch m.view {
		case mcpViewList:
			return m, tea.Quit
		case mcpViewServerMenu:
			m.view = mcpViewList
			m.notice = ""
		case mcpViewServerTools:
			m.view = mcpViewServerMenu
		case mcpViewToolDetail:
			m.view = mcpViewServerTools
		}
		return m, nil
	}

	switch m.view {
	case mcpViewList:
		return m.handleKeyList(msg)
	case mcpViewServerMenu:
		return m.handleKeyServerMenu(msg)
	case mcpViewServerTools:
		return m.handleKeyServerTools(msg)
	case mcpViewToolDetail:
		// detail view: viewport handles scrolling
		m.viewport.Update(msg)
	}
	return m, nil
}

func (m mcpDialogModel) handleKeyList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.servers)-1 {
			m.cursor++
		}
	case "enter":
		if len(m.servers) > 0 {
			m.server = m.servers[m.cursor].Name
			m.view = mcpViewServerMenu
			m.menuIdx = 0
		}
	case "r":
		// 直接重连当前光标行
		if rec, ok := m.mgr.(MCPReconnector); ok && len(m.servers) > 0 {
			name := m.servers[m.cursor].Name
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := rec.Reconnect(ctx, name); err != nil {
				m.notice = fmt.Sprintf("reconnect %s failed: %v", name, err)
			} else {
				m.notice = fmt.Sprintf("✔ reconnected %s", name)
			}
			// 刷新状态
			return m, func() tea.Msg { return m.loadCmd() }
		}
	}
	return m, nil
}

// server-menu 项
var mcpServerMenuItems = []string{"Reconnect", "View tools"}

func (m mcpDialogModel) handleKeyServerMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.menuIdx > 0 {
			m.menuIdx--
		}
	case "down", "j":
		if m.menuIdx < len(mcpServerMenuItems)-1 {
			m.menuIdx++
		}
	case "enter":
		switch m.menuIdx {
		case 0: // Reconnect
			if rec, ok := m.mgr.(MCPReconnector); ok {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := rec.Reconnect(ctx, m.server); err != nil {
					m.notice = fmt.Sprintf("reconnect failed: %v", err)
				} else {
					m.notice = fmt.Sprintf("✔ reconnected %s", m.server)
				}
				return m, func() tea.Msg { return m.loadCmd() }
			}
			m.notice = "（reconnect 未启用）"
		case 1: // View tools
			m.view = mcpViewServerTools
			m.toolIdx = 0
		}
	}
	return m, nil
}

func (m mcpDialogModel) handleKeyServerTools(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	tools := m.toolsForServer(m.server)
	switch msg.String() {
	case "up", "k":
		if m.toolIdx > 0 {
			m.toolIdx--
		}
	case "down", "j":
		if m.toolIdx < len(tools)-1 {
			m.toolIdx++
		}
	case "enter":
		if len(tools) > 0 {
			m.tool = tools[m.toolIdx]
			m.view = mcpViewToolDetail
			vpHeight := m.height - 4
			if vpHeight < 1 {
				vpHeight = 1
			}
			m.viewport.SetSize(m.width, vpHeight)
			m.viewport.SetContent(m.buildToolDetailContent())
			m.viewport.GotoTop()
		}
	}
	return m, nil
}

func (m mcpDialogModel) toolsForServer(server string) []MCPToolInfo {
	var out []MCPToolInfo
	for _, t := range m.allTools {
		if t.Server == server {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// View
func (m mcpDialogModel) View() string {
	switch m.view {
	case mcpViewList:
		return m.viewList()
	case mcpViewServerMenu:
		return m.viewServerMenu()
	case mcpViewServerTools:
		return m.viewServerTools()
	case mcpViewToolDetail:
		return m.viewToolDetail()
	}
	return ""
}

func (m mcpDialogModel) viewList() string {
	var b strings.Builder
	connected := 0
	for _, s := range m.servers {
		if s.Connected {
			connected++
		}
	}
	subtitle := fmt.Sprintf("%d server(s), %d connected", len(m.servers), connected)
	b.WriteString(dialogHeader(m.width, "MCP Servers", subtitle))

	if len(m.servers) == 0 {
		b.WriteString(dlgDimStyle.Render("No MCP servers configured.\nAdd via `claude mcp add <name> <command>`."))
		b.WriteString(dialogFooter("Esc/q  退出"))
		return b.String()
	}

	for i, s := range m.servers {
		marker := "  "
		nameStyle := dlgItemNormalStyle
		if i == m.cursor {
			marker = "› "
			nameStyle = dlgItemSelStyle
		}
		var statusBadge string
		if s.Connected {
			statusBadge = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render("● connected")
		} else {
			statusBadge = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render("● disconnected")
		}
		line := marker + nameStyle.Render(s.Name) + "  " + statusBadge
		if s.Error != "" {
			line += "  " + dlgErrorStyle.Render("("+truncOneLine(s.Error, 40)+")")
		}
		b.WriteString(line + "\n")
	}

	if m.notice != "" {
		b.WriteString("\n" + dlgSubtitleStyle.Render(m.notice) + "\n")
	}
	b.WriteString(dialogFooter("↑/↓ 选择   Enter 详情   r 重连   Esc/q 退出"))
	return b.String()
}

func (m mcpDialogModel) viewServerMenu() string {
	var b strings.Builder
	status := "disconnected"
	for _, s := range m.servers {
		if s.Name == m.server {
			if s.Connected {
				status = "connected"
			}
			break
		}
	}
	b.WriteString(dialogHeader(m.width, "MCP / "+m.server, status))

	for i, item := range mcpServerMenuItems {
		marker := "  "
		style := dlgItemNormalStyle
		if i == m.menuIdx {
			marker = "› "
			style = dlgItemSelStyle
		}
		b.WriteString(marker + style.Render(item) + "\n")
	}
	if m.notice != "" {
		b.WriteString("\n" + dlgSubtitleStyle.Render(m.notice) + "\n")
	}
	b.WriteString(dialogFooter("↑/↓ 选择   Enter 进入   Esc 返回   q 退出"))
	return b.String()
}

func (m mcpDialogModel) viewServerTools() string {
	var b strings.Builder
	tools := m.toolsForServer(m.server)
	subtitle := fmt.Sprintf("%d tool(s)", len(tools))
	b.WriteString(dialogHeader(m.width, "MCP / "+m.server+" / Tools", subtitle))

	if m.toolsErr != nil {
		b.WriteString(dlgErrorStyle.Render("Error: "+m.toolsErr.Error()) + "\n\n")
	}
	if len(tools) == 0 {
		b.WriteString(dlgDimStyle.Render("（this server exposes no tools, or it is disconnected）"))
		b.WriteString(dialogFooter("Esc 返回   q 退出"))
		return b.String()
	}

	for i, t := range tools {
		marker := "  "
		style := dlgItemNormalStyle
		if i == m.toolIdx {
			marker = "› "
			style = dlgItemSelStyle
		}
		// 工具名去掉 mcp__server__ 前缀显示更清晰
		short := stripMcpPrefix(t.Name, t.Server)
		line := marker + style.Render(short)
		if t.Description != "" {
			line += "  " + dlgDimStyle.Render(truncOneLine(t.Description, 60))
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(dialogFooter("↑/↓ 选择   Enter 详情   Esc 返回   q 退出"))
	return b.String()
}

func (m mcpDialogModel) buildToolDetailContent() string {
	var b strings.Builder
	bold := lipgloss.NewStyle().Bold(true)
	b.WriteString(bold.Render("Full name") + "\n  " + m.tool.Name + "\n\n")
	if m.tool.Description != "" {
		b.WriteString(bold.Render("Description") + "\n  " + m.tool.Description + "\n\n")
	}
	return b.String()
}

func (m mcpDialogModel) viewToolDetail() string {
	var b strings.Builder
	short := stripMcpPrefix(m.tool.Name, m.tool.Server)
	b.WriteString(dialogHeader(m.width,
		"MCP Tool: "+short,
		"server="+m.tool.Server))

	b.WriteString(m.viewport.View())
	b.WriteString("\n")

	// footer with scroll hint
	footer := "Esc 返回   q/Ctrl-C 退出"
	if m.viewport.NeedsScroll() {
		footer = "↑/↓/PgUp/PgDn 滚动   " + footer
		indicator := m.viewport.ScrollIndicator()
		if indicator != "" {
			footer += "  " + indicator
		}
	}
	b.WriteString(dialogFooter(footer))
	return b.String()
}

func stripMcpPrefix(name, server string) string {
	prefix := "mcp__" + server + "__"
	return strings.TrimPrefix(name, prefix)
}

// ShowMcpDialog 启动 McpDialog
func (r *REPL) ShowMcpDialog() {
	if r.MCP == nil {
		r.writeOut(r.colorize("（MCP 服务未启用）\r\n", colorYellow))
		return
	}
	model := newMcpDialogModel(r.MCP)
	if err := r.runDialog(model); err != nil {
		r.writeOut(r.colorize(fmt.Sprintf("dialog error: %v\r\n", err), colorRed))
	}
}
