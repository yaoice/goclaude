// Package tool 定义工具系统领域模型
package tool

import (
	"context"
)

// Tool 工具领域接口 - 所有工具必须实现此接口
// 这是整个工具系统的核心抽象，遵循依赖倒置原则
type Tool interface {
	// Name 工具名称（唯一标识）
	Name() string
	// Aliases 工具别名
	Aliases() []string
	// Description 工具描述（用于AI理解工具用途）
	Description() string
	// InputSchema 输入参数的JSON Schema定义
	InputSchema() map[string]interface{}

	// Call 执行工具
	// ctx 用于超时和取消控制
	// input 已验证的输入参数
	// toolCtx 工具执行上下文（包含权限、状态等）
	Call(ctx context.Context, input Input, toolCtx *UseContext) (*Result, error)

	// IsEnabled 工具是否启用（受Feature Flag控制）
	IsEnabled() bool
	// IsReadOnly 工具是否为只读操作（影响并发调度）
	IsReadOnly(input Input) bool
	// IsConcurrencySafe 工具是否可安全并发执行
	IsConcurrencySafe(input Input) bool

	// CheckPermissions 检查工具执行权限
	// 返回 PermissionResult 指示是否允许、拒绝或需要询问用户
	CheckPermissions(ctx context.Context, input Input, permCtx *PermissionContext) (PermissionResult, error)

	// ValidateInput 验证输入参数
	ValidateInput(input Input) error

	// Prompt 返回工具在系统提示词中的描述文本
	Prompt() string
}

// Input 工具输入（通用map，具体工具内部解析为强类型）
type Input map[string]interface{}

// GetString 从输入中获取字符串值
func (i Input) GetString(key string) string {
	if v, ok := i[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// GetInt 从输入中获取整数值
func (i Input) GetInt(key string) int {
	if v, ok := i[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return 0
}

// GetBool 从输入中获取布尔值
func (i Input) GetBool(key string) bool {
	if v, ok := i[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// GetStringSlice 从输入中获取字符串切片
func (i Input) GetStringSlice(key string) []string {
	if v, ok := i[key]; ok {
		if arr, ok := v.([]interface{}); ok {
			result := make([]string, 0, len(arr))
			for _, item := range arr {
				if s, ok := item.(string); ok {
					result = append(result, s)
				}
			}
			return result
		}
	}
	return nil
}

// PermissionResult 权限检查结果
type PermissionResult struct {
	// Behavior 权限行为：allow, deny, ask
	Behavior PermissionBehavior
	// Reason 原因说明
	Reason string
	// ToolName 工具名
	ToolName string
}

// PermissionBehavior 权限行为枚举
type PermissionBehavior string

const (
	PermissionAllow PermissionBehavior = "allow"
	PermissionDeny  PermissionBehavior = "deny"
	PermissionAsk   PermissionBehavior = "ask"
)

// PermissionContext 权限检查上下文
type PermissionContext struct {
	// Mode 权限模式
	Mode PermissionMode
	// AllowedTools 白名单工具
	AllowedTools []string
	// DeniedTools 黑名单工具
	DeniedTools []string
	// WorkingDir 当前工作目录
	WorkingDir string
	// ProjectRoot 项目根目录
	ProjectRoot string
}

// PermissionMode 权限模式
type PermissionMode string

const (
	// PermissionModeDefault 默认：写入类工具触发 ask；只读工具直通
	PermissionModeDefault PermissionMode = "default"
	// PermissionModePlan 计划模式：所有写入被拒绝（仅供"先规划再执行"用）
	PermissionModePlan PermissionMode = "plan"
	// PermissionModeAcceptEdits 自动放行编辑类工具（file_edit/file_write/bash 写）
	//
	// 与 src `permissionMode === 'acceptEdits'` 对齐。当前 go 工具尚未单独
	// 处理此模式，但常量先暴露，让 shell 与子代理可一致识别；具体工具按需
	// 在 CheckPermissions 中实现 "若 mode==acceptEdits 则 PermissionAllow"。
	PermissionModeAcceptEdits PermissionMode = "acceptEdits"
	// PermissionModeBypass 跳过所有权限检查
	PermissionModeBypass PermissionMode = "bypass"
)
