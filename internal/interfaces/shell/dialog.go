// Package shell - dialog.go
//
// 提供与 src `components/design-system/Dialog.tsx` 对齐的全屏 Dialog 框架。
//
// 设计要点：
//   - 每个 Dialog 是独立的 bubbletea Program，运行在 alt-screen
//   - shell 进入 Dialog 前暂停 raw 模式（bubbletea 自己会重新接管 stdin）
//   - Dialog 退出后 shell 自动恢复 raw + bracketed paste
//   - Dialog 之间的视图切换由各自的 Model 内部状态机驱动
//   - 所有 Dialog 复用 baseStyle / sectionStyle / footerStyle 保持视觉一致
package shell

import (
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// 通用样式：与 src Dialog 视觉对齐
var (
	dlgTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("6")) // cyan

	dlgSubtitleStyle = lipgloss.NewStyle().
				Faint(true)

	dlgSectionTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Faint(true)

	dlgItemSelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("6")) // selected: cyan bold

	dlgItemNormalStyle = lipgloss.NewStyle()

	dlgDimStyle = lipgloss.NewStyle().Faint(true)

	dlgFooterStyle = lipgloss.NewStyle().
			Faint(true).
			Italic(true)

	dlgErrorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1"))
)

// runDialog 暂停 shell readline，启动 bubbletea program，结束后恢复
//
// 调用约定：
//   - 调用方必须保证当前 REPL 已 EnterRaw（runDialog 会 LeaveRaw 再让 bubbletea 接管）
//   - bubbletea 程序退出后自动重新 EnterRaw
//   - bracketedPaste 的开关由调用方在外层管理（避免 dialog 内部回显粘贴序列）
func (r *REPL) runDialog(model tea.Model) error {
	r.pauseInputMu.Lock()
	defer r.pauseInputMu.Unlock()

	// 暂存 raw 状态；bubbletea 自己会重新 EnterRaw + 进入 alt screen
	wasRaw := r.Term.rawEnabled
	if wasRaw {
		_ = r.Term.LeaveRaw()
	}
	r.Editor.EnableBracketedPaste(false)

	defer func() {
		if wasRaw {
			_ = r.Term.EnterRaw()
		}
		r.Editor.EnableBracketedPaste(true)
		// 强制重绘 prompt 区，避免残留
		r.Editor.PrintAboveLine("")
	}()

	p := tea.NewProgram(model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithInput(os.Stdin),
		tea.WithOutput(os.Stdout))

	_, err := p.Run()
	if err != nil && err != io.EOF {
		return fmt.Errorf("dialog error: %w", err)
	}
	return nil
}

// dialogHeader 渲染 Dialog 顶部（标题 + 可选副标题 + 分隔线）
func dialogHeader(width int, title, subtitle string) string {
	var s string
	s += dlgTitleStyle.Render(title)
	if subtitle != "" {
		s += "  " + dlgSubtitleStyle.Render(subtitle)
	}
	s += "\n"
	if width > 0 {
		s += dlgDimStyle.Render(strings.Repeat("─", width)) + "\n"
	}
	return s
}

// dialogFooter 渲染 Dialog 底部快捷键提示
func dialogFooter(hints string) string {
	return "\n" + dlgFooterStyle.Render(hints)
}
