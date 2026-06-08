// Package shell - dialog_viewport.go
//
// 轻量级 viewport 滚动组件，为 Dialog 详情页 / 长列表提供上下滚动能力。
//
// 支持：
//   - 键盘 ↑/↓ 逐行滚动
//   - PgUp/PgDown 翻页
//   - Home/End 跳转首尾
//   - 鼠标滚轮（需配合 tea.WithMouseCellMotion）
//
// 设计：不引入额外依赖（charmbracelet/bubbles/viewport），直接基于 bubbletea 原语实现。
package shell

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// dialogViewport 一个嵌入式 viewport，管理内容的滚动偏移。
type dialogViewport struct {
	content string // 完整内容（可含 ANSI）
	lines   []string

	width  int
	height int // 可视区域高度（不含 header/footer）

	offset int // 当前滚动偏移（行数）
}

// SetContent 设置要显示的内容
func (v *dialogViewport) SetContent(content string) {
	v.content = content
	v.lines = strings.Split(content, "\n")
	// 确保 offset 仍有效
	v.clampOffset()
}

// SetSize 更新视口尺寸
func (v *dialogViewport) SetSize(width, height int) {
	v.width = width
	v.height = height
	v.clampOffset()
}

// ScrollUp 向上滚动 n 行
func (v *dialogViewport) ScrollUp(n int) {
	v.offset -= n
	v.clampOffset()
}

// ScrollDown 向下滚动 n 行
func (v *dialogViewport) ScrollDown(n int) {
	v.offset += n
	v.clampOffset()
}

// GotoTop 跳转到顶部
func (v *dialogViewport) GotoTop() {
	v.offset = 0
}

// GotoBottom 跳转到底部
func (v *dialogViewport) GotoBottom() {
	v.offset = v.maxOffset()
}

// Update 处理键盘/鼠标滚动事件，返回是否已消费该事件
func (v *dialogViewport) Update(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			v.ScrollUp(1)
			return true
		case "down", "j":
			v.ScrollDown(1)
			return true
		case "pgup", "b":
			v.ScrollUp(v.pageSize())
			return true
		case "pgdown", "f", " ":
			v.ScrollDown(v.pageSize())
			return true
		case "home", "g":
			v.GotoTop()
			return true
		case "end", "G":
			v.GotoBottom()
			return true
		}
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			v.ScrollUp(3)
			return true
		case tea.MouseButtonWheelDown:
			v.ScrollDown(3)
			return true
		}
	}
	return false
}

// View 返回可视区域内的文本
func (v *dialogViewport) View() string {
	if len(v.lines) == 0 || v.height <= 0 {
		return ""
	}

	end := v.offset + v.height
	if end > len(v.lines) {
		end = len(v.lines)
	}

	visible := v.lines[v.offset:end]
	return strings.Join(visible, "\n")
}

// ScrollPercent 返回滚动百分比（用于状态指示）
func (v *dialogViewport) ScrollPercent() int {
	max := v.maxOffset()
	if max <= 0 {
		return 100
	}
	return v.offset * 100 / max
}

// NeedsScroll 返回内容是否超出视口需要滚动
func (v *dialogViewport) NeedsScroll() bool {
	return len(v.lines) > v.height
}

// ScrollIndicator 返回滚动位置提示字符串
func (v *dialogViewport) ScrollIndicator() string {
	if !v.NeedsScroll() {
		return ""
	}
	style := lipgloss.NewStyle().Faint(true)
	return style.Render(
		strings.Repeat(" ", 2) +
			scrollBar(v.offset, v.maxOffset(), v.height))
}

func (v *dialogViewport) clampOffset() {
	max := v.maxOffset()
	if v.offset > max {
		v.offset = max
	}
	if v.offset < 0 {
		v.offset = 0
	}
}

func (v *dialogViewport) maxOffset() int {
	max := len(v.lines) - v.height
	if max < 0 {
		return 0
	}
	return max
}

func (v *dialogViewport) pageSize() int {
	ps := v.height - 2
	if ps < 1 {
		ps = 1
	}
	return ps
}

// scrollBar 生成简洁的滚动位置提示
func scrollBar(offset, maxOffset, _ int) string {
	if maxOffset <= 0 {
		return ""
	}
	pct := float64(offset) / float64(maxOffset)

	if offset == 0 {
		return "↓ more below"
	}
	if offset >= maxOffset {
		return "↑ more above"
	}
	return fmt.Sprintf("↕ %d%% (↑/↓/PgUp/PgDn)", int(pct*100))
}
