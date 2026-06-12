// Package application 实现规则应用服务
package application

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yaoice/goclaude/pkg/domain/memory"
	rules "github.com/yaoice/goclaude/pkg/domain/rules"
	"github.com/yaoice/goclaude/pkg/infrastructure/configdir"
)

var sanitizePathRe = regexp.MustCompile(`[^a-zA-Z0-9]`)

// sanitizeProjectKey 将项目路径转换为文件系统安全名称。
// 非字母数字字符替换为 `-`，去除首尾 `-`，压缩连续 `-`，
// 对齐上游 src/utils/sessionStoragePortable.ts:sanitizePath。
func sanitizeProjectKey(cwd string) string {
	s := sanitizePathRe.ReplaceAllString(cwd, "-")
	s = strings.Trim(s, "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	if s == "" {
		return "default"
	}
	return s
}

// RulesService 规则应用服务
type RulesService struct {
	scanner *rules.Scanner
	repo    rules.Repository
}

// NewRulesService 创建规则服务
func NewRulesService(repo rules.Repository) *RulesService {
	return &RulesService{
		scanner: rules.NewScanner(repo),
		repo:    repo,
	}
}

// LoadRules 加载所有规则文件
func (s *RulesService) LoadRules(ctx context.Context, opts rules.LoadOptions) ([]rules.MemoryFileInfo, error) {
	return s.scanner.LoadMemoryFiles(ctx, opts)
}

// FormatRulesForPrompt 格式化规则文件用于提示词
func (s *RulesService) FormatRulesForPrompt(files []rules.MemoryFileInfo, filter func(rules.MemoryType) bool) string {
	return rules.FormatMemoryContent(files, filter)
}

// GetMemoryFiles 获取记忆文件列表
func (s *RulesService) GetMemoryFiles(ctx context.Context, forceIncludeExternal bool) ([]rules.MemoryFileInfo, error) {
	homeDir, _ := os.UserHomeDir()
	if homeDir == "" {
		homeDir = "/home/user"
	}

	cwd, _ := os.Getwd()
	if cwd == "" {
		cwd = "."
	}

	opts := rules.LoadOptions{
		ManagedClaudeMdPath:    filepath.Join("/etc", "claude-code", "CLAUDE.md"),
		ManagedClaudeRulesDir:  filepath.Join("/etc", "claude-code", "rules"),
		UserClaudeMdPath:       configdir.JoinPrimary(homeDir, "CLAUDE.md"),
		UserClaudeRulesDir:     configdir.JoinPrimary(homeDir, "rules"),
		OriginalCwd:            cwd,
		IncludeExternal:        forceIncludeExternal,
		UserSettingsEnabled:    true,
		ProjectSettingsEnabled: true,
		LocalSettingsEnabled:   true,
		AutoMemoryEnabled:      memory.IsAutoMemoryEnabled(),
		TeamMemoryEnabled:      false,
		AutoMemDir:             configdir.JoinPrimary(homeDir, "projects", sanitizeProjectKey(cwd), "memory"),
		TeamMemDir:             configdir.JoinPrimary(homeDir, "projects", sanitizeProjectKey(cwd), "memory", "team"),
	}

	return s.LoadRules(ctx, opts)
}

// GetClaudeMds 格式化记忆文件内容用于注入系统提示词
func (s *RulesService) GetClaudeMds(files []rules.MemoryFileInfo, filter func(rules.MemoryType) bool) string {
	return rules.FormatMemoryContent(files, filter)
}

// GetAllMemoryFilePaths 获取所有记忆文件路径
func (s *RulesService) GetAllMemoryFilePaths(files []rules.MemoryFileInfo) []string {
	paths := []string{}
	for _, f := range files {
		if len(f.Content) > 0 {
			paths = append(paths, f.Path)
		}
	}
	return paths
}

// ClearCache 清除缓存
func (s *RulesService) ClearCache() {
	// 简化实现：无缓存
}

// ResetCache 重置缓存
func (s *RulesService) ResetCache() {
	// 简化实现：无缓存
}
