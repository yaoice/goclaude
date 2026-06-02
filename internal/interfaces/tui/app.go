// Package tui 实现基于 bubbletea 的终端用户界面
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Model TUI主模型（bubbletea Elm Architecture）
type Model struct {
	// 界面状态
	width    int
	height   int
	ready    bool
	quitting bool

	// 消息列表
	messages []ChatMessage
	// 用户输入
	input    string
	// 状态栏信息
	status   StatusInfo
	// 当前模式
	mode     Mode

	// 登录页面
	loginPage LoginPage

	// 回调函数
	onSubmit func(string)
}

// Mode 界面模式
type Mode int

const (
	ModeLogin Mode = iota
	ModeChat
	ModePermission
	ModeCommand
)

// ChatMessage 聊天消息（UI展示用）
type ChatMessage struct {
	Role    string
	Content string
	IsError bool
}

// StatusInfo 状态栏信息
type StatusInfo struct {
	Model       string
	TokenCount  int
	CostUSD     float64
	TurnCount   int
	Mode        string
}

// NewModel 创建TUI主模型
func NewModel() Model {
	return Model{
		messages:  make([]ChatMessage, 0),
		mode:      ModeLogin, // 启动时先进入登录页面
		loginPage: NewLoginPage(),
		status: StatusInfo{
			Model: "claude-sonnet-4-20250514",
			Mode:  "default",
		},
	}
}

// Init 初始化（bubbletea接口）
func (m Model) Init() tea.Cmd {
	return nil
}

// Update 处理事件（bubbletea接口）
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		return m, nil
	case AddMessageMsg:
		m.messages = append(m.messages, ChatMessage(msg))
		return m, nil
	case UpdateStatusMsg:
		m.status = msg.Status
		return m, nil
	}

	// ── 登录模式：全部事件交由 loginPage 处理 ──
	if m.mode == ModeLogin {
		return m.handleLoginUpdate(msg)
	}

	// ── 聊天模式 ──
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

// handleKey 处理键盘输入（非登录模式）
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "ctrl+d":
		m.quitting = true
		return m, tea.Quit
	case "enter":
		if strings.TrimSpace(m.input) != "" {
			input := m.input
			m.messages = append(m.messages, ChatMessage{Role: "user", Content: input})
			m.input = ""
			if m.onSubmit != nil {
				m.onSubmit(input)
			}
		}
		return m, nil
	case "backspace":
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
		return m, nil
	default:
		if len(msg.String()) == 1 {
			m.input += msg.String()
		}
		return m, nil
	}
}

// handleLoginUpdate 处理登录模式下的所有事件
func (m Model) handleLoginUpdate(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	// ── 登录成功：从 loginPage 提升到主 Model，切换模式 ──
	case LoginSuccessMsg:
		// 先让 loginPage 处理（更新内部状态：LoggedIn、loggedInUser）
		m.loginPage, _ = m.loginPage.Update(msg)
		m.mode = ModeChat
		// 添加欢迎消息
		m.messages = append(m.messages, ChatMessage{
			Role:    "system",
			Content: "欢迎使用 GoClaude！您已登录为：" + msg.Username,
		})
		return m, nil

	// ── 登出：重置一切，回到登录页 ──
	case LogoutMsg:
		m.loginPage, _ = m.loginPage.logout()
		m.messages = make([]ChatMessage, 0)
		m.input = ""
		m.mode = ModeLogin
		return m, nil

	// ── 键盘事件 ──
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "ctrl+d":
			m.quitting = true
			return m, tea.Quit
		}

		var cmd tea.Cmd
		m.loginPage, cmd = m.loginPage.Update(msg)
		return m, cmd
	}

	// ── 其他消息透传 ──
	var cmd tea.Cmd
	m.loginPage, cmd = m.loginPage.Update(msg)
	return m, cmd
}

// View 渲染界面（bubbletea接口）
func (m Model) View() string {
	if !m.ready {
		return "正在初始化..."
	}
	if m.quitting {
		return "再见！\n"
	}

	// 登录模式：渲染登录页面
	if m.mode == ModeLogin {
		return m.renderLoginView()
	}

	var b strings.Builder

	// 标题栏
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	b.WriteString(titleStyle.Render("🤖 GoClaude"))
	b.WriteString("\n")

	// 显示当前登录用户
	if m.loginPage.LoggedIn {
		userStyle := lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("2"))
		b.WriteString(userStyle.Render("  已登录: " + m.loginPage.loggedInUser))
	}
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", m.width))
	b.WriteString("\n\n")

	// 消息列表
	for _, msg := range m.messages {
		b.WriteString(renderMessage(msg, m.width))
		b.WriteString("\n")
	}

	// 输入区域
	b.WriteString("\n")
	inputStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	b.WriteString(inputStyle.Render("> "))
	b.WriteString(m.input)
	b.WriteString("█")
	b.WriteString("\n\n")

	// 状态栏
	b.WriteString(renderStatusBar(m.status, m.width))

	return b.String()
}

// renderLoginView 渲染登录页面视图
func (m Model) renderLoginView() string {
	var b strings.Builder

	// 登录页面主体
	b.WriteString(m.loginPage.View(m.width))

	// 底部快捷键提示
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", m.width))
	b.WriteString("\n")
	hintStyle := lipgloss.NewStyle().Faint(true).Italic(true).Foreground(lipgloss.Color("8"))
	b.WriteString(hintStyle.Render("  Ctrl+C / Ctrl+D 退出"))

	return b.String()
}

// renderMessage 渲染单条消息
func renderMessage(msg ChatMessage, width int) string {
	var style lipgloss.Style
	var prefix string

	switch msg.Role {
	case "user":
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
		prefix = "You: "
	case "assistant":
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
		prefix = "AI: "
	case "tool":
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
		prefix = "⚡ "
	default:
		style = lipgloss.NewStyle()
	}

	if msg.IsError {
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
		prefix = "✗ "
	}

	return style.Render(prefix + msg.Content)
}

// renderStatusBar 渲染状态栏
func renderStatusBar(status StatusInfo, width int) string {
	style := lipgloss.NewStyle().
		Background(lipgloss.Color("8")).
		Foreground(lipgloss.Color("7")).
		Width(width)

	info := fmt.Sprintf(" %s | Tokens: %d | Cost: $%.4f | Turns: %d | Mode: %s",
		status.Model, status.TokenCount, status.CostUSD, status.TurnCount, status.Mode)

	return style.Render(info)
}

// 消息类型（用于 bubbletea Cmd 通信）

// AddMessageMsg 添加消息
type AddMessageMsg struct {
	Role    string
	Content string
	IsError bool
}

// UpdateStatusMsg 更新状态
type UpdateStatusMsg struct {
	Status StatusInfo
}
