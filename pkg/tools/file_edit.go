package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/yaoice/goclaude/pkg/domain/tool"
	"github.com/yaoice/goclaude/pkg/infrastructure/filesystem"
)

// FileEditTool 文件编辑工具（搜索替换）
type FileEditTool struct {
	fs *filesystem.Service
}

func NewFileEditTool(fs *filesystem.Service) *FileEditTool {
	return &FileEditTool{fs: fs}
}

func (t *FileEditTool) Name() string      { return "file_edit" }
func (t *FileEditTool) Aliases() []string { return []string{"Edit"} }
func (t *FileEditTool) Description() string {
	return "对文件执行精确的字符串替换编辑。old_str必须在文件中唯一匹配。"
}
func (t *FileEditTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"file_path": map[string]interface{}{"type": "string", "description": "要编辑的文件绝对路径"},
			"old_str":   map[string]interface{}{"type": "string", "description": "要替换的原始字符串（必须唯一匹配）"},
			"new_str":   map[string]interface{}{"type": "string", "description": "替换后的新字符串"},
		},
		"required": []string{"file_path", "old_str", "new_str"},
	}
}

func (t *FileEditTool) IsEnabled() bool                         { return true }
func (t *FileEditTool) IsReadOnly(input tool.Input) bool        { return false }
func (t *FileEditTool) IsConcurrencySafe(input tool.Input) bool { return false }
func (t *FileEditTool) Prompt() string                          { return "" }
func (t *FileEditTool) ValidateInput(input tool.Input) error {
	if input.GetString("file_path") == "" {
		return fmt.Errorf("file_path is required")
	}
	if input.GetString("old_str") == "" {
		return fmt.Errorf("old_str is required")
	}
	return nil
}

func (t *FileEditTool) CheckPermissions(ctx context.Context, input tool.Input, permCtx *tool.PermissionContext) (tool.PermissionResult, error) {
	if permCtx != nil {
		switch permCtx.Mode {
		case tool.PermissionModeBypass, tool.PermissionModeAcceptEdits:
			// acceptEdits / bypass → 自动放行编辑类工具（与 src 行为对齐）
			return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
		case tool.PermissionModePlan:
			return tool.PermissionResult{Behavior: tool.PermissionDeny, Reason: "plan mode: writes disabled"}, nil
		}
	}
	return tool.PermissionResult{Behavior: tool.PermissionAsk, Reason: "编辑文件需要确认"}, nil
}

func (t *FileEditTool) Call(ctx context.Context, input tool.Input, toolCtx *tool.UseContext) (*tool.Result, error) {
	filePath := input.GetString("file_path")
	oldStr := input.GetString("old_str")
	newStr := input.GetString("new_str")

	// 若配置了 workspace，优先在 workspace 内查找/编辑文件
	resolvedPath := resolveOutputPath(filePath, toolCtx)
	if _, err := os.Stat(resolvedPath); err == nil {
		filePath = resolvedPath
	}

	// 读取文件
	content, err := t.fs.ReadFile(filePath, 0, 0)
	if err != nil {
		return tool.NewErrorResult(fmt.Sprintf("Error reading file: %v", err)), nil
	}

	// 检查唯一匹配
	count := strings.Count(content, oldStr)
	if count == 0 {
		return tool.NewErrorResult("old_str not found in file"), nil
	}
	if count > 1 {
		return tool.NewErrorResult(fmt.Sprintf("old_str matches %d times (must be unique)", count)), nil
	}

	// 执行替换
	newContent := strings.Replace(content, oldStr, newStr, 1)

	// 若 workspace 已配置，确保写回 workspace 内的路径
	writePath := resolveOutputPath(filePath, toolCtx)
	if err := t.fs.WriteFile(writePath, newContent); err != nil {
		return tool.NewErrorResult(fmt.Sprintf("Error writing file: %v", err)), nil
	}

	return tool.NewResult(fmt.Sprintf("Successfully edited %s", writePath)), nil
}
