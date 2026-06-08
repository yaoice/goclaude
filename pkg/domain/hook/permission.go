// Package hook — tool permission management (对齐 src/hooks/toolPermission/ 中的 PermissionContext.ts 和 usePermission.ts)
//
// 提供 Go 版本的工具权限管理：
//   - PermissionLevel: allow / deny / ask / default
//   - ToolPermissionRule: 基于工具名和路径模式的权限规则
//   - ToolPermission: 单个工具的授权记录（含过期时间）
//   - PermissionContext: 完整的权限上下文，支持规则匹配和显式授权
package hook

import (
	"sync"
	"time"
)

// PermissionLevel 权限级别
type PermissionLevel string

const (
	PermissionAllow   PermissionLevel = "allow"
	PermissionDeny    PermissionLevel = "deny"
	PermissionAsk     PermissionLevel = "ask"
	PermissionDefault PermissionLevel = ""
)

// ToolPermissionRule 工具权限规则
type ToolPermissionRule struct {
	ToolNames   []string
	PathPattern string
	Level       PermissionLevel
}

// ToolPermission 单个工具权限
type ToolPermission struct {
	ToolName  string
	Level     PermissionLevel
	GrantedAt time.Time
	ExpiresAt *time.Time
	Scope     string // "session" or "always"
}

// PermissionContext 权限上下文（对齐 PermissionContext.ts）
type PermissionContext struct {
	mu           sync.RWMutex
	rules        []ToolPermissionRule
	permissions  map[string]ToolPermission
	defaultLevel PermissionLevel
	onChange     func()
}

// NewPermissionContext 创建权限上下文
func NewPermissionContext(defaultLevel PermissionLevel) *PermissionContext {
	if defaultLevel == "" {
		defaultLevel = PermissionAsk
	}
	return &PermissionContext{
		permissions:  make(map[string]ToolPermission),
		defaultLevel: defaultLevel,
	}
}

// OnChange 设置变更回调
func (pc *PermissionContext) OnChange(fn func()) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.onChange = fn
}

// SetRules 设置权限规则
func (pc *PermissionContext) SetRules(rules []ToolPermissionRule) {
	pc.mu.Lock()
	pc.rules = rules
	pc.mu.Unlock()
	if pc.onChange != nil {
		pc.onChange()
	}
}

// Grant 授予工具权限
func (pc *PermissionContext) Grant(toolName string, level PermissionLevel, scope string) {
	pc.mu.Lock()
	pc.permissions[toolName] = ToolPermission{
		ToolName:  toolName,
		Level:     level,
		GrantedAt: time.Now(),
		Scope:     scope,
	}
	pc.mu.Unlock()
	if pc.onChange != nil {
		pc.onChange()
	}
}

// GetLevel 获取工具权限级别
func (pc *PermissionContext) GetLevel(toolName string, path string) PermissionLevel {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	// Check explicit grants first
	if perm, ok := pc.permissions[toolName]; ok {
		return perm.Level
	}

	// Check rules
	for _, rule := range pc.rules {
		for _, tn := range rule.ToolNames {
			if tn == toolName {
				if rule.PathPattern == "" {
					return rule.Level
				}
				// Simple substring path matching (simplified)
				if path != "" && matchPath(path, rule.PathPattern) {
					return rule.Level
				}
			}
		}
	}

	return pc.defaultLevel
}

func matchPath(path, pattern string) bool {
	// Simple path matching: supports exact match and suffix match
	if pattern == "" || path == pattern {
		return true
	}
	// Support patterns like "*.ts" or "src/*"
	if len(pattern) > 1 && pattern[0] == '*' {
		suffix := pattern[1:]
		return len(path) >= len(suffix) && path[len(path)-len(suffix):] == suffix
	}
	return false
}

// Clear 清空所有权限
func (pc *PermissionContext) Clear() {
	pc.mu.Lock()
	pc.permissions = make(map[string]ToolPermission)
	pc.mu.Unlock()
	if pc.onChange != nil {
		pc.onChange()
	}
}

// CanUseTool 检查是否可以使用工具
func (pc *PermissionContext) CanUseTool(toolName string, path string) (allowed bool, needAsk bool) {
	level := pc.GetLevel(toolName, path)
	switch level {
	case PermissionAllow:
		return true, false
	case PermissionDeny:
		return false, false
	default:
		return false, true
	}
}
