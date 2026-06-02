package tools

import (
	"context"
	"fmt"

	"github.com/anthropics/goclaude/internal/domain/tool"
	"github.com/anthropics/goclaude/internal/infrastructure/filesystem"
)

// FileWriteTool 文件写入工具
type FileWriteTool struct {
	fs *filesystem.Service
}

func NewFileWriteTool(fs *filesystem.Service) *FileWriteTool {
	return &FileWriteTool{fs: fs}
}

func (t *FileWriteTool) Name() string     { return "file_write" }
func (t *FileWriteTool) Aliases() []string { return []string{"Write"} }
func (t *FileWriteTool) Description() string {
	return "创建或覆写文件。将完整内容写入指定路径，自动创建父目录。"
}
func (t *FileWriteTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"file_path": map[string]interface{}{"type": "string", "description": "要写入的文件绝对路径"},
			"content":   map[string]interface{}{"type": "string", "description": "文件完整内容"},
		},
		"required": []string{"file_path", "content"},
	}
}

func (t *FileWriteTool) IsEnabled() bool                    { return true }
func (t *FileWriteTool) IsReadOnly(input tool.Input) bool   { return false }
func (t *FileWriteTool) IsConcurrencySafe(input tool.Input) bool { return false }
func (t *FileWriteTool) Prompt() string                     { return "" }
func (t *FileWriteTool) ValidateInput(input tool.Input) error {
	if input.GetString("file_path") == "" {
		return fmt.Errorf("file_path is required")
	}
	return nil
}

func (t *FileWriteTool) CheckPermissions(ctx context.Context, input tool.Input, permCtx *tool.PermissionContext) (tool.PermissionResult, error) {
	if permCtx != nil {
		switch permCtx.Mode {
		case tool.PermissionModeBypass, tool.PermissionModeAcceptEdits:
			return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
		case tool.PermissionModePlan:
			return tool.PermissionResult{Behavior: tool.PermissionDeny, Reason: "plan mode: writes disabled"}, nil
		}
	}
	return tool.PermissionResult{Behavior: tool.PermissionAsk, Reason: "写入文件需要确认"}, nil
}

func (t *FileWriteTool) Call(ctx context.Context, input tool.Input, toolCtx *tool.UseContext) (*tool.Result, error) {
	filePath := input.GetString("file_path")
	content := input.GetString("content")

	if err := t.fs.WriteFile(filePath, content); err != nil {
		return tool.NewErrorResult(fmt.Sprintf("Error writing file: %v", err)), nil
	}
	return tool.NewResult(fmt.Sprintf("Successfully wrote to %s", filePath)), nil
}
