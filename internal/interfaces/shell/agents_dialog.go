// Package shell - agents_dialog.go
//
// 与 src `components/agents/AgentsMenu.tsx` 对齐的 AgentsDialog。
//
// 当前实现：list-agents → detail（只读）。
// 创建/编辑视图（src 的 create-agent / edit-agent）暂留 placeholder：
// 因为 go 端的 agent 管理后端目前以只读为主，写操作稳妥起见走 cli `claude agents`。
package shell

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type agentsDialogModel struct {
	mgr      AgentManager
	agents   []AgentInfo
	groupsAt []int

	view   int // 0=list, 1=detail
	cursor int

	width, height int

	// viewport for detail view scrolling
	viewport dialogViewport
}

func newAgentsDialogModel(mgr AgentManager) agentsDialogModel {
	all := mgr.List()
	// 按 source 分组
	order := []string{"builtin", "user", "project", "plugin"}
	groups := map[string][]AgentInfo{}
	for _, a := range all {
		key := strings.ToLower(a.Source)
		if key == "" {
			key = "builtin"
		}
		groups[key] = append(groups[key], a)
	}
	for k := range groups {
		sort.Slice(groups[k], func(i, j int) bool {
			return groups[k][i].AgentType < groups[k][j].AgentType
		})
	}
	var flat []AgentInfo
	var firstIdx []int
	for _, src := range order {
		if g, ok := groups[src]; ok && len(g) > 0 {
			firstIdx = append(firstIdx, len(flat))
			flat = append(flat, g...)
		}
	}
	for src, g := range groups {
		if !contains(order, src) && len(g) > 0 {
			firstIdx = append(firstIdx, len(flat))
			flat = append(flat, g...)
		}
	}
	return agentsDialogModel{mgr: mgr, agents: flat, groupsAt: firstIdx}
}

func (m agentsDialogModel) Init() tea.Cmd { return nil }

func (m agentsDialogModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// header(2 lines) + footer(2 lines) = 4 lines reserved
		vpHeight := m.height - 4
		if vpHeight < 1 {
			vpHeight = 1
		}
		m.viewport.SetSize(m.width, vpHeight)
		if m.view == 1 {
			m.viewport.SetContent(m.buildDetailContent())
		}
	case tea.MouseMsg:
		if m.view == 1 {
			m.viewport.Update(msg)
			return m, nil
		}
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "esc":
			if m.view == 1 {
				m.view = 0
				return m, nil
			}
			return m, tea.Quit
		}
		// detail view: viewport handles scrolling
		if m.view == 1 {
			m.viewport.Update(msg)
			return m, nil
		}
		// list view: cursor navigation
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.agents)-1 {
				m.cursor++
			}
		case "enter":
			if len(m.agents) > 0 {
				m.view = 1
				vpHeight := m.height - 4
				if vpHeight < 1 {
					vpHeight = 1
				}
				m.viewport.SetSize(m.width, vpHeight)
				m.viewport.SetContent(m.buildDetailContent())
				m.viewport.GotoTop()
			}
		}
	}
	return m, nil
}

func (m agentsDialogModel) View() string {
	if m.view == 1 {
		return m.viewDetail()
	}
	return m.viewList()
}

func (m agentsDialogModel) viewList() string {
	var b strings.Builder
	subtitle := fmt.Sprintf("%d agent(s)", len(m.agents))
	b.WriteString(dialogHeader(m.width, "Agents", subtitle))

	if len(m.agents) == 0 {
		b.WriteString(dlgDimStyle.Render(
			"Create agents in .claude/agents/ or ~/.claude/agents/"))
		b.WriteString(dialogFooter("Esc/q  退出"))
		return b.String()
	}

	groupSet := map[int]struct{}{}
	for _, idx := range m.groupsAt {
		groupSet[idx] = struct{}{}
	}

	for i, a := range m.agents {
		if _, isHead := groupSet[i]; isHead {
			if i > 0 {
				b.WriteString("\n")
			}
			title := agentSourceTitle(a.Source)
			b.WriteString(dlgSectionTitleStyle.Render(title) + "\n")
		}
		marker := "  "
		style := dlgItemNormalStyle
		if i == m.cursor {
			marker = "› "
			style = dlgItemSelStyle
		}
		line := marker + style.Render(a.AgentType)
		if a.Model != "" {
			line += "  " + dlgDimStyle.Render("("+a.Model+")")
		}
		if a.WhenToUse != "" {
			line += "  " + dlgDimStyle.Render(truncOneLine(a.WhenToUse, 50))
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(dialogFooter("↑/↓ 选择   Enter 详情   Esc/q 退出"))
	return b.String()
}

func (m agentsDialogModel) buildDetailContent() string {
	if m.cursor >= len(m.agents) {
		return ""
	}
	var b strings.Builder
	a := m.agents[m.cursor]

	bold := lipgloss.NewStyle().Bold(true)
	if a.Model != "" {
		b.WriteString(bold.Render("Model") + "\n  " + a.Model + "\n\n")
	}
	if a.WhenToUse != "" {
		b.WriteString(bold.Render("When to use") + "\n  " + a.WhenToUse + "\n\n")
	}
	if len(a.Tools) > 0 {
		b.WriteString(bold.Render("Tools") + "\n  " + strings.Join(a.Tools, ", ") + "\n\n")
	}
	if len(a.DisallowedTools) > 0 {
		b.WriteString(bold.Render("Disallowed tools") + "\n  " + strings.Join(a.DisallowedTools, ", ") + "\n\n")
	}
	if a.SystemPrompt != "" {
		b.WriteString(bold.Render("System prompt") + "\n")
		body := a.SystemPrompt
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.String()
}

func (m agentsDialogModel) viewDetail() string {
	var b strings.Builder
	a := m.agents[m.cursor]
	b.WriteString(dialogHeader(m.width,
		"Agent: "+a.AgentType,
		"source="+a.Source))

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

func agentSourceTitle(src string) string {
	switch strings.ToLower(src) {
	case "", "builtin":
		return "Built-in agents"
	case "user":
		return "User agents (~/.claude/agents)"
	case "project":
		return "Project agents (.claude/agents)"
	case "plugin":
		return "Plugin agents"
	default:
		return src + " agents"
	}
}

// ShowAgentsDialog 启动 AgentsDialog
func (r *REPL) ShowAgentsDialog() {
	if r.Agents == nil {
		r.writeOut(r.colorize("（agent 服务未启用）\r\n", colorYellow))
		return
	}
	model := newAgentsDialogModel(r.Agents)
	if err := r.runDialog(model); err != nil {
		r.writeOut(r.colorize(fmt.Sprintf("dialog error: %v\r\n", err), colorRed))
	}
}
