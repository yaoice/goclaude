package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/yaoice/goclaude/pkg/domain/tool"
	"github.com/yaoice/goclaude/pkg/infrastructure/filesystem"
)

// GlobTool 文件模式匹配搜索工具
type GlobTool struct {
	glob *filesystem.Glob
}

func NewGlobTool(workDir string) *GlobTool {
	return &GlobTool{glob: filesystem.NewGlob(workDir)}
}

func (t *GlobTool) Name() string      { return "glob" }
func (t *GlobTool) Aliases() []string { return []string{"Glob"} }
func (t *GlobTool) Description() string {
	return "使用glob模式搜索文件。返回匹配的文件路径列表。"
}
func (t *GlobTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pattern": map[string]interface{}{"type": "string", "description": "glob模式（如 *.go, **/*.ts）"},
			"path":    map[string]interface{}{"type": "string", "description": "搜索起始路径"},
		},
		"required": []string{"pattern"},
	}
}

func (t *GlobTool) IsEnabled() bool                         { return true }
func (t *GlobTool) IsReadOnly(input tool.Input) bool        { return true }
func (t *GlobTool) IsConcurrencySafe(input tool.Input) bool { return true }
func (t *GlobTool) Prompt() string                          { return "" }
func (t *GlobTool) ValidateInput(input tool.Input) error {
	if input.GetString("pattern") == "" {
		return fmt.Errorf("pattern is required")
	}
	return nil
}
func (t *GlobTool) CheckPermissions(ctx context.Context, input tool.Input, permCtx *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *GlobTool) Call(ctx context.Context, input tool.Input, toolCtx *tool.UseContext) (*tool.Result, error) {
	pattern := input.GetString("pattern")
	path := input.GetString("path")

	results, err := t.glob.Match(filesystem.GlobOptions{Pattern: pattern, Path: path})
	if err != nil {
		return tool.NewErrorResult(fmt.Sprintf("Glob error: %v", err)), nil
	}

	if len(results) == 0 {
		return tool.NewResult("No files matched the pattern."), nil
	}
	return tool.NewResult(strings.Join(results, "\n")), nil
}

// GrepTool 内容搜索工具
type GrepTool struct {
	grep *filesystem.Grep
}

func NewGrepTool(workDir string) *GrepTool {
	return &GrepTool{grep: filesystem.NewGrep(workDir)}
}

func (t *GrepTool) Name() string      { return "grep" }
func (t *GrepTool) Aliases() []string { return []string{"Grep"} }
func (t *GrepTool) Description() string {
	return "使用正则表达式搜索文件内容（基于ripgrep）。"
}
func (t *GrepTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pattern":        map[string]interface{}{"type": "string", "description": "搜索正则表达式"},
			"path":           map[string]interface{}{"type": "string", "description": "搜索路径"},
			"case_sensitive": map[string]interface{}{"type": "boolean", "description": "是否区分大小写"},
			"file_pattern":   map[string]interface{}{"type": "string", "description": "文件过滤glob（如 *.go）"},
		},
		"required": []string{"pattern"},
	}
}

func (t *GrepTool) IsEnabled() bool                         { return true }
func (t *GrepTool) IsReadOnly(input tool.Input) bool        { return true }
func (t *GrepTool) IsConcurrencySafe(input tool.Input) bool { return true }
func (t *GrepTool) Prompt() string                          { return "" }
func (t *GrepTool) ValidateInput(input tool.Input) error {
	if input.GetString("pattern") == "" {
		return fmt.Errorf("pattern is required")
	}
	return nil
}
func (t *GrepTool) CheckPermissions(ctx context.Context, input tool.Input, permCtx *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

func (t *GrepTool) Call(ctx context.Context, input tool.Input, toolCtx *tool.UseContext) (*tool.Result, error) {
	opts := filesystem.GrepOptions{
		Pattern:       input.GetString("pattern"),
		Path:          input.GetString("path"),
		CaseSensitive: input.GetBool("case_sensitive"),
		FilePattern:   input.GetString("file_pattern"),
		MaxResults:    100,
	}

	results, err := t.grep.Search(opts)
	if err != nil {
		return tool.NewErrorResult(fmt.Sprintf("Grep error: %v", err)), nil
	}

	if len(results) == 0 {
		return tool.NewResult("No matches found."), nil
	}

	var output strings.Builder
	for _, r := range results {
		if r.File != "" {
			output.WriteString(fmt.Sprintf("%s:%d: %s\n", r.File, r.Line, r.Content))
		} else {
			output.WriteString(r.Content + "\n")
		}
	}
	return tool.NewResult(output.String()), nil
}
