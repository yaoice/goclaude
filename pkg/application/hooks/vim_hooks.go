// Package hooks — Vim 模式钩子
//
// 对齐 src/hooks/ 中的 Vim 输入钩子实现：
//   - VimInput: 等价于 useVimInput.ts
//
// 提供 Vim 编辑模式的状态管理与模式切换回调。

package hooks

import "sync"

// VimMode vim 编辑模式
type VimMode string

const (
	VimNormalMode  VimMode = "normal"
	VimInsertMode  VimMode = "insert"
	VimVisualMode  VimMode = "visual"
	VimCommandMode VimMode = "command"
)

// VimInput vim 风格输入管理器
// Equivalent of useVimInput
type VimInput struct {
	mu           sync.RWMutex
	mode         VimMode
	enabled      bool
	onModeChange func(VimMode)
}

// NewVimInput 创建 Vim 输入管理器
func NewVimInput() *VimInput {
	return &VimInput{mode: VimInsertMode, enabled: false}
}

// SetEnabled 启用或禁用 Vim 模式
func (v *VimInput) SetEnabled(enabled bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.enabled = enabled
	if !enabled {
		v.mode = VimInsertMode
	}
}

// IsEnabled 返回 Vim 模式是否启用
func (v *VimInput) IsEnabled() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.enabled
}

// Mode 返回当前 Vim 模式
func (v *VimInput) Mode() VimMode {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.mode
}

// SetMode 设置 Vim 模式
func (v *VimInput) SetMode(mode VimMode) {
	v.mu.Lock()
	if v.mode != mode {
		v.mode = mode
		cb := v.onModeChange
		v.mu.Unlock()
		if cb != nil {
			cb(mode)
		}
		return
	}
	v.mu.Unlock()
}

// OnModeChange 设置模式变更回调
func (v *VimInput) OnModeChange(fn func(VimMode)) { v.onModeChange = fn }
