// Package tools 实现所有具体工具（属于基础设施层，实现领域Tool接口）
package tools

import (
	"context"
	"fmt"

	"github.com/anthropics/goclaude/pkg/domain/tool"
	"github.com/anthropics/goclaude/pkg/infrastructure/filesystem"
)

// FileReadTool 文件读取工具
type FileReadTool struct {
	fs *filesystem.Service
}

func NewFileReadTool(fs *filesystem.Service) *FileReadTool {
	return &FileReadTool{fs: fs}
}

func (t *FileReadTool) Name() string     { return "file_read" }
func (t *FileReadTool) Aliases() []string { return []string{"Read"} }
func (t *FileReadTool) Description() string {
	return "读取文件内容。支持通过offset和limit参数读取文件的指定部分。"
}
func (t *FileReadTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"file_path": map[string]interface{}{"type": "string", "description": "要读取的文件绝对路径"},
			"offset":    map[string]interface{}{"type": "integer", "description": "起始行号（从0开始）"},
			"limit":     map[string]interface{}{"type": "integer", "description": "读取的最大行数"},
		},
		"required": []string{"file_path"},
	}
}

func (t *FileReadTool) IsEnabled() bool                    { return true }
func (t *FileReadTool) IsReadOnly(input tool.Input) bool   { return true }
func (t *FileReadTool) IsConcurrencySafe(input tool.Input) bool { return true }
func (t *FileReadTool) Prompt() string                     { return "" }
func (t *FileReadTool) ValidateInput(input tool.Input) error {
	if input.GetString("file_path") == "" {
		return fmt.Errorf("file_path is required")
	}
	return nil
}

func (t *FileReadTool) CheckPermissions(ctx context.Context, input tool.Input, permCtx *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *FileReadTool) Call(ctx context.Context, input tool.Input, toolCtx *tool.UseContext) (*tool.Result, error) {
	filePath := input.GetString("file_path")
	offset := input.GetInt("offset")
	limit := input.GetInt("limit")

	content, err := t.fs.ReadFile(filePath, offset, limit)
	if err != nil {
		return tool.NewErrorResult(fmt.Sprintf("Error reading file: %v", err)), nil
	}
	return tool.NewResult(content), nil
}
