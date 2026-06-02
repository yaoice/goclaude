// Package shell - skills_dialog.go
//
// 与 src `components/skills/SkillsMenu.tsx` 对齐的 SkillsDialog。
//
// 行为：
//   - 全屏 alt-screen Dialog，标题 "Skills"
//   - 按 source 分组显示（user / project / plugin / mcp / 其它）
//   - 每组带标题（如 "User skills (~/.claude/skills)"）
//   - ↑/↓ 移动；Enter 进入详情视图（完整 prompt + when_to_use + arguments）
//   - 详情页：支持 ↑/↓/PgUp/PgDn/鼠标滚轮 滚动浏览
//   - 列表页：Esc / q / Ctrl-C 退出 Dialog 回到 prompt
package shell

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// skillsDialogModel SkillsMenu 的 bubbletea Model
type skillsDialogModel struct {
	mgr      SkillManager
	skills   []SkillInfo // 平铺顺序（按分组排好）
	groupsAt []int       // 每个分组首项在 skills 中的索引

	// view 0=list, 1=detail
	view   int
	cursor int // 当前选中的 skill 在 skills 中的索引

	width  int
	height int

	// 详情页内容及 viewport
	detailContent string
	viewport      dialogViewport
}

// newSkillsDialogModel 构造模型
func newSkillsDialogModel(mgr SkillManager) skillsDialogModel {
	all := mgr.List()
	// 按 source 分组（与 src 顺序一致）
	order := []string{"user", "project", "policy", "local", "flag", "plugin", "mcp"}
	groups := map[string][]SkillInfo{}
	for _, s := range all {
		key := normalizeSource(s.Source)
		groups[key] = append(groups[key], s)
	}
	for k := range groups {
		sort.Slice(groups[k], func(i, j int) bool {
			return groups[k][i].Name < groups[k][j].Name
		})
	}
	var flat []SkillInfo
	var firstIdx []int
	for _, src := range order {
		if g, ok := groups[src]; ok && len(g) > 0 {
			firstIdx = append(firstIdx, len(flat))
			flat = append(flat, g...)
		}
	}
	// 兜底：未识别 source 也展示
	for src, g := range groups {
		if !contains(order, src) && len(g) > 0 {
			firstIdx = append(firstIdx, len(flat))
			flat = append(flat, g...)
		}
	}
	return skillsDialogModel{
		mgr:      mgr,
		skills:   flat,
		groupsAt: firstIdx,
	}
}

func (m skillsDialogModel) Init() tea.Cmd { return nil }

func (m skillsDialogModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
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
				m.detailContent = ""
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
			if m.cursor < len(m.skills)-1 {
				m.cursor++
			}
		case "home", "g":
			m.cursor = 0
		case "end", "G":
			if len(m.skills) > 0 {
				m.cursor = len(m.skills) - 1
			}
		case "enter":
			if len(m.skills) > 0 {
				m.view = 1
				name := m.skills[m.cursor].Name
				if body, ok := m.mgr.Render(name); ok {
					m.detailContent = body
				} else {
					m.detailContent = "(no content)"
				}
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

func (m skillsDialogModel) View() string {
	if m.view == 1 {
		return m.viewDetail()
	}
	return m.viewList()
}

func (m skillsDialogModel) viewList() string {
	var b strings.Builder
	subtitle := fmt.Sprintf("%d skill(s)", len(m.skills))
	b.WriteString(dialogHeader(m.width, "Skills", subtitle))

	if len(m.skills) == 0 {
		b.WriteString(dlgDimStyle.Render(
			"Create skills in .claude/skills/ or ~/.claude/skills/"))
		b.WriteString(dialogFooter("Esc/q  退出"))
		return b.String()
	}

	// 分组渲染
	groupSet := map[int]struct{}{}
	for _, idx := range m.groupsAt {
		groupSet[idx] = struct{}{}
	}

	for i, s := range m.skills {
		// 分组标题
		if _, isHead := groupSet[i]; isHead {
			if i > 0 {
				b.WriteString("\n")
			}
			title := skillSourceTitle(s.Source)
			path := skillSourcePathHint(s)
			b.WriteString(dlgSectionTitleStyle.Render(title))
			if path != "" {
				b.WriteString("  " + dlgSubtitleStyle.Render("("+path+")"))
			}
			b.WriteString("\n")
		}
		// 项
		marker := "  "
		nameStyle := dlgItemNormalStyle
		if i == m.cursor {
			marker = "› "
			nameStyle = dlgItemSelStyle
		}
		desc := s.Description
		if desc == "" {
			desc = s.WhenToUse
		}
		line := marker + nameStyle.Render(s.Name)
		if desc != "" {
			line += "  " + dlgDimStyle.Render(truncOneLine(desc, 60))
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(dialogFooter("↑/↓ 选择   Enter 详情   Esc/q 退出"))
	return b.String()
}

func (m skillsDialogModel) buildDetailContent() string {
	if m.cursor >= len(m.skills) {
		return ""
	}
	var b strings.Builder
	cur := m.skills[m.cursor]

	if cur.Description != "" {
		b.WriteString(lipgloss.NewStyle().Bold(true).Render("Description"))
		b.WriteString("\n  " + cur.Description + "\n\n")
	}
	if cur.WhenToUse != "" {
		b.WriteString(lipgloss.NewStyle().Bold(true).Render("When to use"))
		b.WriteString("\n  " + cur.WhenToUse + "\n\n")
	}
	if cur.FilePath != "" {
		b.WriteString(dlgDimStyle.Render("Path: "+cur.FilePath) + "\n\n")
	}
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("Body"))
	b.WriteString("\n")
	b.WriteString(m.detailContent)
	b.WriteString("\n")
	return b.String()
}

func (m skillsDialogModel) viewDetail() string {
	var b strings.Builder
	cur := m.skills[m.cursor]
	b.WriteString(dialogHeader(m.width,
		"Skill: "+cur.Name,
		fmt.Sprintf("source=%s", cur.Source)))

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

// ----------------- 工具函数 -----------------

func skillSourceTitle(src string) string {
	switch normalizeSource(src) {
	case "user":
		return "User skills"
	case "project":
		return "Project skills"
	case "policy":
		return "Policy skills"
	case "local":
		return "Local skills"
	case "flag":
		return "Flag skills"
	case "plugin":
		return "Plugin skills"
	case "mcp":
		return "MCP skills"
	default:
		if src == "" {
			return "Skills"
		}
		return src + " skills"
	}
}

func skillSourcePathHint(s SkillInfo) string {
	if s.FilePath != "" {
		// 取目录部分
		dir := s.FilePath
		if idx := strings.LastIndex(dir, "/"); idx > 0 {
			dir = dir[:idx]
		}
		return dir
	}
	return ""
}

func normalizeSource(src string) string {
	s := strings.ToLower(strings.TrimSuffix(src, "Settings"))
	if s == "" {
		return ""
	}
	return s
}

func contains(ss []string, x string) bool {
	for _, s := range ss {
		if s == x {
			return true
		}
	}
	return false
}

func truncOneLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	rs := []rune(s)
	if max < 4 || len(rs) <= max {
		return s
	}
	return string(rs[:max-1]) + "…"
}

// ShowSkillsDialog 启动 SkillsDialog（由 REPL 调用）
func (r *REPL) ShowSkillsDialog() {
	if r.Skills == nil {
		r.writeOut(r.colorize("（skill 服务未启用）\r\n", colorYellow))
		return
	}
	model := newSkillsDialogModel(r.Skills)
	if err := r.runDialog(model); err != nil {
		r.writeOut(r.colorize(fmt.Sprintf("dialog error: %v\r\n", err), colorRed))
	}
}
