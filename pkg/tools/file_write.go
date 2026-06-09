package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/anthropics/goclaude/pkg/domain/tool"
	"github.com/anthropics/goclaude/pkg/infrastructure/filesystem"
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

	// 若配置了 workspace，确保文件写入 workspace 目录下
	filePath = resolveOutputPath(filePath, toolCtx)

	if err := t.fs.WriteFile(filePath, content); err != nil {
		return tool.NewErrorResult(fmt.Sprintf("Error writing file: %v", err)), nil
	}
	return tool.NewResult(fmt.Sprintf("Successfully wrote to %s", filePath)), nil
}

// resolveOutputPath 将文件路径解析为 workspace 内的路径（若已配置 workspace）。
//
// 规则：
//   - 若未配置 WorkspaceRoot → 原样返回
//   - 若 path 已在 WorkspaceRoot 内 → 原样返回
//   - 若 path 是绝对路径且在 ProjectRoot 内 → 对 ProjectRoot 取相对路径后拼到 WorkspaceRoot 下
//   - 若 path 是绝对路径但不在 ProjectRoot 内 → 将完整路径镜像到 WorkspaceRoot 下
//   - 若 path 是相对路径 → 相对 WorkspaceRoot 解析
func resolveOutputPath(path string, toolCtx *tool.UseContext) string {
	if toolCtx == nil || toolCtx.WorkspaceRoot == "" {
		return path
	}
	wsClean := filepath.Clean(toolCtx.WorkspaceRoot)
	if strings.HasPrefix(filepath.Clean(path), wsClean+string(filepath.Separator)) {
		return path
	}
	if filepath.IsAbs(path) {
		// 若路径在 ProjectRoot 内，去掉 ProjectRoot 前缀再拼到 WorkspaceRoot 下，
		// 避免把完整绝对路径镜像到 workspace 内产生深层嵌套
		if toolCtx.ProjectRoot != "" {
			projClean := filepath.Clean(toolCtx.ProjectRoot)
			pathClean := filepath.Clean(path)
			if strings.HasPrefix(pathClean, projClean+string(filepath.Separator)) || pathClean == projClean {
				rel, err := filepath.Rel(projClean, pathClean)
				if err == nil {
					return filepath.Join(wsClean, rel)
				}
			}
		}
		// 不在 ProjectRoot 内的绝对路径：保留全路径结构拼到 workspace 下
		rel, err := filepath.Rel("/", filepath.Clean(path))
		if err != nil {
			rel = filepath.Base(path)
		}
		return filepath.Join(wsClean, rel)
	}
	return filepath.Join(wsClean, path)
}
