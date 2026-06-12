// Package rules 实现 CLAUDE.md / .claude/rules/*.md 的规则加载系统
// 参考 TypeScript 实现：src/utils/claudemd.ts
package rules

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/yaoice/goclaude/pkg/infrastructure/configdir"
)

// Scanner 规则文件扫描器
type Scanner struct {
	repo Repository
}

// NewScanner 创建规则文件扫描器
func NewScanner(repo Repository) *Scanner {
	return &Scanner{repo: repo}
}

// LoadMemoryFiles 加载所有记忆文件
func (s *Scanner) LoadMemoryFiles(ctx context.Context, opts LoadOptions) ([]MemoryFileInfo, error) {
	var result []MemoryFileInfo
	processedPaths := make(map[string]bool)

	// 1. 加载 Managed 文件
	managedPath := opts.ManagedClaudeMdPath
	managedFiles, err := s.ProcessMemoryFile(ctx, managedPath, MemoryTypeManaged, processedPaths, opts.IncludeExternal, 0, "", opts)
	if err == nil {
		result = append(result, managedFiles...)
	}

	// 2. 加载 Managed rules
	managedRulesDir := opts.ManagedClaudeRulesDir
	managedRuleFiles, err := s.ProcessMdRules(ctx, managedRulesDir, MemoryTypeManaged, processedPaths, opts.IncludeExternal, false, opts)
	if err == nil {
		result = append(result, managedRuleFiles...)
	}

	// 3. 加载 User 文件
	if opts.UserSettingsEnabled {
		userPath := opts.UserClaudeMdPath
		userFiles, err := s.ProcessMemoryFile(ctx, userPath, MemoryTypeUser, processedPaths, true, 0, "", opts)
		if err == nil {
			result = append(result, userFiles...)
		}

		userRulesDir := opts.UserClaudeRulesDir
		userRuleFiles, err := s.ProcessMdRules(ctx, userRulesDir, MemoryTypeUser, processedPaths, true, false, opts)
		if err == nil {
			result = append(result, userRuleFiles...)
		}
	}

	// 4. 从 CWD 向上遍历，加载 Project 和 Local 文件
	projectDirs := s.getProjectDirs(opts.OriginalCwd)

	for _, dir := range projectDirs {
		if opts.SkipProject && s.isInCanonicalRoot(dir, opts.CanonicalRoot) && !s.isInGitRoot(dir, opts.GitRoot) {
			continue
		}

		if opts.ProjectSettingsEnabled {
			projectPath := filepath.Join(dir, "CLAUDE.md")
			projectFiles, err := s.ProcessMemoryFile(ctx, projectPath, MemoryTypeProject, processedPaths, opts.IncludeExternal, 0, "", opts)
			if err == nil {
				result = append(result, projectFiles...)
			}

			// 优先 .goclaude/CLAUDE.md，兜底 .claude/CLAUDE.md
			for _, cd := range []string{"CLAUDE.md"} {
				for _, cfgDir := range configdir.DirNames() {
					dotClaudePath := filepath.Join(dir, cfgDir, cd)
					dotClaudeFiles, err := s.ProcessMemoryFile(ctx, dotClaudePath, MemoryTypeProject, processedPaths, opts.IncludeExternal, 0, "", opts)
					if err == nil && len(dotClaudeFiles) > 0 {
						result = append(result, dotClaudeFiles...)
						break
					}
				}
			}

			// 优先 .goclaude/rules，兜底 .claude/rules
			for _, cfgDir := range configdir.DirNames() {
				rulesDir := filepath.Join(dir, cfgDir, "rules")
				ruleFiles, err := s.ProcessMdRules(ctx, rulesDir, MemoryTypeProject, processedPaths, opts.IncludeExternal, false, opts)
				if err == nil && len(ruleFiles) > 0 {
					result = append(result, ruleFiles...)
					break
				}
			}
		}

		if opts.LocalSettingsEnabled {
			localPath := filepath.Join(dir, "CLAUDE.local.md")
			localFiles, err := s.ProcessMemoryFile(ctx, localPath, MemoryTypeLocal, processedPaths, opts.IncludeExternal, 0, "", opts)
			if err == nil {
				result = append(result, localFiles...)
			}
		}
	}

	// 5. 加载 AutoMem 入口文件
	if opts.AutoMemoryEnabled {
		autoMemPath := filepath.Join(opts.AutoMemDir, EntrypointName)
		autoMemFiles, err := s.ProcessMemoryFile(ctx, autoMemPath, MemoryTypeAutoMem, processedPaths, false, 0, "", opts)
		if err == nil {
			result = append(result, autoMemFiles...)
		}
	}

	// 6. 加载 TeamMem 入口文件
	if opts.TeamMemoryEnabled {
		teamMemPath := filepath.Join(opts.TeamMemDir, EntrypointName)
		teamMemFiles, err := s.ProcessMemoryFile(ctx, teamMemPath, MemoryTypeTeamMem, processedPaths, false, 0, "", opts)
		if err == nil {
			result = append(result, teamMemFiles...)
		}
	}

	return result, nil
}

// ProcessMemoryFile 处理单个记忆文件及其 @include 引用
func (s *Scanner) ProcessMemoryFile(ctx context.Context, filePath string, memoryType MemoryType, processedPaths map[string]bool, includeExternal bool, depth int, parent string, opts LoadOptions) ([]MemoryFileInfo, error) {
	if depth >= MaxIncludeDepth {
		return nil, nil
	}

	normalizedPath := NormalizePath(filePath)
	if processedPaths[normalizedPath] {
		return nil, nil
	}

	if s.isExcluded(filePath, memoryType, opts) {
		return nil, nil
	}

	resolvedPath, err := s.repo.RealPath(ctx, filePath)
	if err == nil && resolvedPath != "" && resolvedPath != filePath {
		resolvedNormalized := NormalizePath(resolvedPath)
		processedPaths[resolvedNormalized] = true
	}

	processedPaths[normalizedPath] = true

	content, err := s.repo.ReadFile(ctx, filePath)
	if err != nil {
		return nil, nil
	}

	info, includePaths, err := s.parseMemoryFileContent(content, filePath, memoryType, depth)
	if err != nil {
		return nil, err
	}

	if info == nil || strings.TrimSpace(info.Content) == "" {
		return nil, nil
	}

	if parent != "" {
		info.Parent = parent
	}

	result := []MemoryFileInfo{*info}

	for _, includePath := range includePaths {
		isExternal := !s.isInOriginalCwd(includePath, opts.OriginalCwd)
		if isExternal && !includeExternal {
			continue
		}

		includedFiles, err := s.ProcessMemoryFile(ctx, includePath, memoryType, processedPaths, includeExternal, depth+1, filePath, opts)
		if err == nil {
			result = append(result, includedFiles...)
		}
	}

	return result, nil
}

// ProcessMdRules 处理 .claude/rules/ 目录下的所有 .md 文件
func (s *Scanner) ProcessMdRules(ctx context.Context, rulesDir string, memoryType MemoryType, processedPaths map[string]bool, includeExternal bool, conditionalRule bool, opts LoadOptions) ([]MemoryFileInfo, error) {
	visitedDirs := make(map[string]bool)
	return s.processMdRulesRecursive(ctx, rulesDir, memoryType, processedPaths, includeExternal, conditionalRule, visitedDirs, opts)
}

func (s *Scanner) processMdRulesRecursive(ctx context.Context, rulesDir string, memoryType MemoryType, processedPaths map[string]bool, includeExternal bool, conditionalRule bool, visitedDirs map[string]bool, opts LoadOptions) ([]MemoryFileInfo, error) {
	if visitedDirs[rulesDir] {
		return nil, nil
	}

	// Check for symlink to avoid cycles
	resolvedPath, err := s.repo.RealPath(ctx, rulesDir)
	if err == nil && resolvedPath != "" && resolvedPath != rulesDir {
		visitedDirs[resolvedPath] = true
	}
	visitedDirs[rulesDir] = true

	entries, err := s.repo.ReadDir(ctx, rulesDir, false)
	if err != nil {
		return nil, nil
	}

	var result []MemoryFileInfo

	for _, entry := range entries {
		entryPath := filepath.Join(rulesDir, entry.Name)

		if entry.IsDir {
			subFiles, err := s.processMdRulesRecursive(ctx, entryPath, memoryType, processedPaths, includeExternal, conditionalRule, visitedDirs, opts)
			if err == nil {
				result = append(result, subFiles...)
			}
		} else if strings.HasSuffix(entry.Name, ".md") {
			files, err := s.ProcessMemoryFile(ctx, entryPath, memoryType, processedPaths, includeExternal, 0, "", opts)
			if err == nil {
				for _, f := range files {
					hasGlobs := len(f.Globs) > 0
					if conditionalRule && hasGlobs {
						result = append(result, f)
					} else if !conditionalRule && !hasGlobs {
						result = append(result, f)
					}
				}
			}
		}
	}

	return result, nil
}

func (s *Scanner) parseMemoryFileContent(content string, filePath string, memoryType MemoryType, depth int) (*MemoryFileInfo, []string, error) {
	ext := filepath.Ext(filePath)
	if ext != "" && !IsTextFile(ext) {
		return nil, nil, nil
	}

	fm, remainingContent := ParseFrontmatter(content, filePath)
	strippedContent, stripped := StripHtmlComments(remainingContent)
	includePaths := []string{}

	finalContent := strippedContent
	if memoryType == MemoryTypeAutoMem || memoryType == MemoryTypeTeamMem {
		if len(finalContent) > MaxEntrypointBytes {
			finalContent = finalContent[:MaxEntrypointBytes]
		}
	}

	contentDiffersFromDisk := stripped || (fm.Raw != "")
	var globs []string
	if fm.Paths != "" {
		globs = ParseFrontmatterPaths(fm.Paths)
	}

	info := &MemoryFileInfo{
		Path:                   filePath,
		Type:                   memoryType,
		Content:                finalContent,
		Globs:                  globs,
		ContentDiffersFromDisk: contentDiffersFromDisk,
	}

	if contentDiffersFromDisk {
		info.RawContent = content
	}

	return info, includePaths, nil
}

func (s *Scanner) getProjectDirs(cwd string) []string {
	dirs := []string{}
	currentDir := cwd

	for {
		dirs = append(dirs, currentDir)
		parent := filepath.Dir(currentDir)
		if parent == currentDir {
			break
		}
		currentDir = parent
	}

	for i, j := 0, len(dirs)-1; i < j; i, j = i+1, j-1 {
		dirs[i], dirs[j] = dirs[j], dirs[i]
	}

	return dirs
}

func (s *Scanner) isExcluded(filePath string, memoryType MemoryType, opts LoadOptions) bool {
	if memoryType != MemoryTypeUser && memoryType != MemoryTypeProject && memoryType != MemoryTypeLocal {
		return false
	}

	if len(opts.ClaudeMdExcludes) == 0 {
		return false
	}

	for _, pattern := range opts.ClaudeMdExcludes {
		if strings.Contains(filePath, pattern) {
			return true
		}
	}

	return false
}

func (s *Scanner) isInOriginalCwd(path string, originalCwd string) bool {
	return strings.HasPrefix(path, originalCwd)
}

func (s *Scanner) isInCanonicalRoot(dir string, canonicalRoot string) bool {
	if canonicalRoot == "" {
		return false
	}
	return strings.HasPrefix(dir, canonicalRoot)
}

func (s *Scanner) isInGitRoot(dir string, gitRoot string) bool {
	if gitRoot == "" {
		return false
	}
	return strings.HasPrefix(dir, gitRoot)
}
