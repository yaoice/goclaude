package tools

import (
	"context"
	"fmt"

	"github.com/anthropics/goclaude/pkg/domain/tool"
	"github.com/anthropics/goclaude/pkg/infrastructure/shell"
)

// BashTool Shell命令执行工具
type BashTool struct {
	executor *shell.Executor
}

func NewBashTool(executor *shell.Executor) *BashTool {
	return &BashTool{executor: executor}
}

func (t *BashTool) Name() string     { return "bash" }
func (t *BashTool) Aliases() []string { return []string{"Bash"} }
func (t *BashTool) Description() string {
	return "在bash shell中执行命令。命令在工作目录中执行，支持超时控制。"
}
func (t *BashTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command":     map[string]interface{}{"type": "string", "description": "要执行的bash命令"},
			"timeout":     map[string]interface{}{"type": "integer", "description": "超时秒数（默认120）"},
			"description": map[string]interface{}{"type": "string", "description": "命令用途简述（用于权限展示）"},
		},
		"required": []string{"command"},
	}
}

func (t *BashTool) IsEnabled() bool                    { return true }
func (t *BashTool) IsReadOnly(input tool.Input) bool   { return false }
func (t *BashTool) IsConcurrencySafe(input tool.Input) bool { return false }
func (t *BashTool) Prompt() string                     { return "" }
func (t *BashTool) ValidateInput(input tool.Input) error {
	if input.GetString("command") == "" {
		return fmt.Errorf("command is required")
	}
	return nil
}

func (t *BashTool) CheckPermissions(ctx context.Context, input tool.Input, permCtx *tool.PermissionContext) (tool.PermissionResult, error) {
	if permCtx != nil {
		switch permCtx.Mode {
		case tool.PermissionModeBypass:
			return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
		case tool.PermissionModePlan:
			return tool.PermissionResult{Behavior: tool.PermissionDeny, Reason: "plan mode: shell disabled"}, nil
		}
	}
	return tool.PermissionResult{
		Behavior: tool.PermissionAsk,
		Reason:   fmt.Sprintf("执行命令: %s", input.GetString("command")),
	}, nil
}

func (t *BashTool) Call(ctx context.Context, input tool.Input, toolCtx *tool.UseContext) (*tool.Result, error) {
	command := input.GetString("command")

	result, err := t.executor.Execute(ctx, command)
	if err != nil {
		return tool.NewErrorResult(fmt.Sprintf("Command error: %v", err)), nil
	}

	output := result.Stdout
	if result.Stderr != "" {
		output += "\nSTDERR:\n" + result.Stderr
	}
	if result.ExitCode != 0 {
		output += fmt.Sprintf("\n(exit code: %d)", result.ExitCode)
		return tool.NewErrorResult(output), nil
	}

	return tool.NewResult(output), nil
}
