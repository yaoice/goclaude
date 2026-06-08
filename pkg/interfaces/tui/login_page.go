package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ─── 消息定义 ────────────────────────────────────────────────────────────────

// LoginSuccessMsg 登录成功消息，携带用户名
type LoginSuccessMsg struct {
	Username string
}

// LogoutMsg 登出消息
type LogoutMsg struct{}

// ─── 焦点字段枚举 ────────────────────────────────────────────────────────────

// Field 当前聚焦的输入字段
type Field int

const (
	FieldUsername Field = iota
	FieldPassword
	FieldLoginBtn
)

// ─── 登录模式状态枚举 ────────────────────────────────────────────────────────

// LoginMode 登录页面的子模式
type LoginMode int

const (
	LoginInputUsername LoginMode = iota
	LoginInputPassword
	LoginSubmitting
	LoginLoggedIn
)

// ─── 模型定义 ────────────────────────────────────────────────────────────────

// LoginPage 登录页面模型
type LoginPage struct {
	mode         LoginMode
	username     strings.Builder // 使用 Builder 提高字符串拼接效率
	password     []byte          // 用 []byte 存储密码（安全考虑）
	focusedField Field
	errorMsg     string
	loggedInUser string
	LoggedIn     bool
}

// ─── 构造函数 ────────────────────────────────────────────────────────────────

// NewLoginPage 创建默认登录页面
func NewLoginPage() LoginPage {
	return LoginPage{
		mode:         LoginInputUsername,
		focusedField: FieldUsername,
	}
}

// ─── 样式定义 ────────────────────────────────────────────────────────────────

var (
	// 容器样式
	loginContainerStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("6")).
				Padding(1, 2).
				Width(36).
				Align(lipgloss.Center)

	// 标题样式
	loginTitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6")).
			Bold(true).
			MarginBottom(1)

	// 输入框标签样式
	loginInputLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("7")).
				MarginTop(1).
				MarginBottom(0)

	// 输入框样式（未聚焦）
	loginInputBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("8")).
				Padding(0, 1).
				Width(30)

	// 输入框样式（聚焦）
	loginInputBoxFocusedStyle = lipgloss.NewStyle().
					Border(lipgloss.RoundedBorder()).
					BorderForeground(lipgloss.Color("6")).
					Padding(0, 1).
					Width(30)

	// 登录按钮样式（聚焦）
	loginBtnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("6")).
			Padding(0, 3).
			Bold(true).
			MarginTop(1)

	// 登录按钮样式（未聚焦）
	loginBtnInactiveStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("8")).
				Background(lipgloss.Color("7")).
				Padding(0, 3).
				MarginTop(1)

	// 错误消息样式
	loginErrorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1"))

	// 提示文字样式
	loginHintStyle = lipgloss.NewStyle().
			Faint(true).
			Italic(true).
			Foreground(lipgloss.Color("8"))

	// 退出按钮样式
	loginLogoutBtnStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("0")).
				Background(lipgloss.Color("1")).
				Padding(0, 3).
				Bold(true).
				MarginTop(1)

	// 欢迎消息样式
	loginWelcomeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("2")).
				Bold(true).
				MarginBottom(1)
)

// ─── 辅助方法 ────────────────────────────────────────────────────────────────

// maskPassword 返回密码的掩码字符串
func (l *LoginPage) maskPassword() string {
	return strings.Repeat("●", len(l.password))
}

// ─── 业务方法 ────────────────────────────────────────────────────────────────

// submit 验证并提交登录
func (l LoginPage) submit() (LoginPage, tea.Cmd) {
	username := strings.TrimSpace(l.username.String())

	if username == "" {
		l.errorMsg = "❌ 用户名不能为空"
		l.focusedField = FieldUsername
		return l, nil
	}

	if len(l.password) == 0 {
		l.errorMsg = "❌ 密码不能为空"
		l.focusedField = FieldPassword
		return l, nil
	}

	l.mode = LoginSubmitting
	l.errorMsg = ""

	return l, func() tea.Msg {
		return LoginSuccessMsg{Username: username}
	}
}

// logout 执行登出，完全重置所有状态
func (l LoginPage) logout() (LoginPage, tea.Cmd) {
	l.username.Reset()
	l.password = nil
	l.mode = LoginInputUsername
	l.focusedField = FieldUsername
	l.errorMsg = ""
	l.loggedInUser = ""
	l.LoggedIn = false
	return l, func() tea.Msg {
		return LogoutMsg{}
	}
}

// updateLoggedIn 处理已登录状态的事件
func (l LoginPage) updateLoggedIn(msg tea.Msg) (LoginPage, tea.Cmd) {
	switch msg := msg.(type) {
	case LoginSuccessMsg:
		l.loggedInUser = msg.Username
		l.LoggedIn = true
		l.mode = LoginLoggedIn
		return l, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "enter", " ":
			return l.logout()
		}
	}

	return l, nil
}

// ─── Update 方法（核心逻辑）────────────────────────────────────────────────────

// Update 处理消息，返回自身及命令
func (l LoginPage) Update(msg tea.Msg) (LoginPage, tea.Cmd) {
	switch l.mode {
	case LoginSubmitting:
		// 提交中忽略所有键盘事件
		return l, nil

	case LoginLoggedIn:
		return l.updateLoggedIn(msg)

	default:
		// 输入模式：LoginInputUsername / LoginInputPassword
		return l.updateInput(msg)
	}
}

// updateInput 处理输入模式下的键盘事件
func (l LoginPage) updateInput(msg tea.Msg) (LoginPage, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEsc, tea.KeyCtrlC, tea.KeyCtrlD:
			return l, tea.Quit

		case tea.KeyEnter:
			return l.submit()

		case tea.KeyBackspace:
			switch l.focusedField {
			case FieldUsername:
				s := l.username.String()
				if len(s) > 0 {
					l.username.Reset()
					l.username.WriteString(s[:len(s)-1])
				}
			case FieldPassword:
				if len(l.password) > 0 {
					l.password = l.password[:len(l.password)-1]
				}
			}
			return l, nil

		case tea.KeyTab:
			l.errorMsg = ""
			l.focusedField = (l.focusedField + 1) % 3
			return l, nil

		case tea.KeyShiftTab:
			l.errorMsg = ""
			l.focusedField = (l.focusedField + 2) % 3 // +2 等价于 -1 mod 3
			return l, nil

		case tea.KeyUp:
			l.errorMsg = ""
			if l.focusedField > 0 {
				l.focusedField--
			} else {
				l.focusedField = FieldLoginBtn
			}
			return l, nil

		case tea.KeyDown:
			l.errorMsg = ""
			l.focusedField = (l.focusedField + 1) % 3
			return l, nil

		case tea.KeyRunes:
			for _, r := range msg.Runes {
				switch l.focusedField {
				case FieldUsername:
					l.username.WriteRune(r)
				case FieldPassword:
					l.password = append(l.password, byte(r))
				}
			}
			return l, nil
		}
	}

	return l, nil
}

// ─── View 方法 ───────────────────────────────────────────────────────────────

// View 渲染登录页面
func (l LoginPage) View(width int) string {
	if width < 40 {
		return "终端宽度过小，无法显示登录页面（需至少 40 列）"
	}

	var page string
	if l.mode == LoginLoggedIn {
		page = l.renderLoggedIn(width)
	} else {
		page = l.renderForm(width)
	}

	return lipgloss.Place(width, 0, lipgloss.Center, lipgloss.Center, page)
}

// ─── 表单渲染（非登录状态） ──────────────────────────────────────────────────

// renderForm 渲染登录表单
func (l LoginPage) renderForm(width int) string {
	var rows []string

	// 标题
	rows = append(rows, loginTitleStyle.Render("🔐 GoClaude 登录"))

	// 用户名标签 + 输入框
	rows = append(rows, loginInputLabelStyle.Render("👤 用户名"))
	rows = append(rows, l.renderInputBox(FieldUsername, l.renderUsernameText()))

	// 密码标签 + 输入框
	rows = append(rows, loginInputLabelStyle.Render("🔒 密码"))
	rows = append(rows, l.renderInputBox(FieldPassword, l.renderPasswordText()))

	// 登录按钮
	rows = append(rows, l.getBtnStyle().Render("登  录"))

	// 错误消息（条件渲染）
	if l.errorMsg != "" {
		rows = append(rows, loginErrorStyle.Render("⚠ "+l.errorMsg))
	}

	// 操作提示
	rows = append(rows, loginHintStyle.Render("Tab / ↑↓ 切换字段  Enter 提交"))

	return loginContainerStyle.Render(strings.Join(rows, "\n"))
}

// renderInputBox 渲染单个输入框
func (l LoginPage) renderInputBox(field Field, content string) string {
	return l.getInputBoxStyle(field).Render(content)
}

// renderUsernameText 渲染用户名输入框文本（含光标/占位符逻辑）
func (l LoginPage) renderUsernameText() string {
	focused := l.focusedField == FieldUsername
	if l.username.String() == "" && focused {
		return loginHintStyle.Render("请输入用户名...")
	}
	if focused {
		return l.username.String() + "█"
	}
	if l.username.String() == "" {
		return loginHintStyle.Render("[Empty]")
	}
	return l.username.String()
}

// renderPasswordText 渲染密码输入框文本（含光标/占位符/掩码逻辑）
func (l LoginPage) renderPasswordText() string {
	masked := l.maskPassword()
	focused := l.focusedField == FieldPassword
	if masked == "" && focused {
		return loginHintStyle.Render("请输入密码...")
	}
	if focused {
		return masked + "█"
	}
	if masked == "" {
		return loginHintStyle.Render("[Empty]")
	}
	return masked
}

// ─── 已登录渲染 ──────────────────────────────────────────────────────────────

// renderLoggedIn 渲染登录成功后的欢迎界面
func (l LoginPage) renderLoggedIn(width int) string {
	var rows []string

	// 标题
	rows = append(rows, loginTitleStyle.Render("🔐 GoClaude 登录 · 已登录"))

	// 欢迎消息
	rows = append(rows, loginWelcomeStyle.Render("✅ 已登录: "+l.loggedInUser))

	// 退出按钮
	rows = append(rows, loginLogoutBtnStyle.Render("退  出  登  录"))

	// 操作提示
	rows = append(rows, loginHintStyle.Render("按 Enter 或 Space 退出登录"))

	return loginContainerStyle.Render(strings.Join(rows, "\n"))
}

// ─── 辅助渲染方法 ────────────────────────────────────────────────────────────

// getInputBoxStyle 根据焦点状态返回输入框样式
func (l LoginPage) getInputBoxStyle(field Field) lipgloss.Style {
	if l.focusedField == field {
		return loginInputBoxFocusedStyle
	}
	return loginInputBoxStyle
}

// getBtnStyle 根据焦点状态返回按钮样式
func (l LoginPage) getBtnStyle() lipgloss.Style {
	if l.focusedField == FieldLoginBtn {
		return loginBtnStyle
	}
	return loginBtnInactiveStyle
}
