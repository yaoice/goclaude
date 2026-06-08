// Package hooks — application-level permission hook implementations
//
// 对齐 src/hooks/toolPermission/ 和 src/hooks/usePermission.ts 的应用层编排。
// PermissionManager 包装 domain/hook.PermissionContext，提供会话模式切换和工具审批计数。
package hooks

import (
	"github.com/anthropics/goclaude/pkg/domain/hook"
)

// PermissionManager 权限管理器（应用层编排）
type PermissionManager struct {
	ctx           *hook.PermissionContext
	sessionMode   string
	toolApprovals map[string]int // toolName -> approval count
}

// NewPermissionManager 创建权限管理器
func NewPermissionManager(defaultLevel hook.PermissionLevel) *PermissionManager {
	return &PermissionManager{
		ctx:           hook.NewPermissionContext(defaultLevel),
		toolApprovals: make(map[string]int),
	}
}

// Context 返回权限上下文
func (pm *PermissionManager) Context() *hook.PermissionContext {
	return pm.ctx
}

// SetSessionMode 设置会话权限模式
func (pm *PermissionManager) SetSessionMode(mode string) {
	pm.sessionMode = mode
	switch mode {
	case "acceptEdits":
		// Auto-grant file write/edit permissions
	case "bypassPermissions":
		pm.ctx.SetRules([]hook.ToolPermissionRule{
			{ToolNames: []string{"*"}, Level: hook.PermissionAllow},
		})
	}
}

// RecordToolUse 记录工具使用（用于跟踪审批次数）
func (pm *PermissionManager) RecordToolUse(toolName string) {
	pm.toolApprovals[toolName]++
}

// ApprovalCount 返回工具审批次数
func (pm *PermissionManager) ApprovalCount(toolName string) int {
	return pm.toolApprovals[toolName]
}
