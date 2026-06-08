// Package application 实现记忆应用服务
package application

import (
	"context"
	"fmt"

	memory "github.com/anthropics/goclaude/pkg/domain/memory"
)

// MemoryService 记忆应用服务
type MemoryService struct {
	repo      memory.Repository
	entrypoint *memory.EntrypointManager
	autoMemDir string
}

// NewMemoryService 创建记忆服务
func NewMemoryService(repo memory.Repository) *MemoryService {
	return &MemoryService{repo: repo}
}

// NewMemoryServiceWithDir 创建记忆服务（含 auto-memory 目录）
func NewMemoryServiceWithDir(repo memory.Repository, autoMemDir string) *MemoryService {
	return &MemoryService{
		repo:        repo,
		autoMemDir:  autoMemDir,
		entrypoint:  memory.NewEntrypointManager(repo, autoMemDir),
	}
}

// SetAutoMemDir 设置 auto-memory 目录并初始化 EntrypointManager
func (s *MemoryService) SetAutoMemDir(autoMemDir string) {
	s.autoMemDir = autoMemDir
	s.entrypoint = memory.NewEntrypointManager(s.repo, autoMemDir)
}

// EntrypointManager 返回 MEMORY.md 入口文件管理器
func (s *MemoryService) EntrypointManager() *memory.EntrypointManager {
	return s.entrypoint
}

// AutoMemDir 返回 auto-memory 目录路径
func (s *MemoryService) AutoMemDir() string {
	return s.autoMemDir
}

// LoadMemoryPrompt 加载记忆提示词
func (s *MemoryService) LoadMemoryPrompt(ctx context.Context, autoMemDir string, skipIndex bool) (string, error) {
	autoEnabled := memory.IsAutoMemoryEnabled()

	if !autoEnabled {
		return "", nil
	}

	err := memory.EnsureMemoryDirExists(ctx, s.repo, autoMemDir)
	if err != nil {
		return "", fmt.Errorf("failed to ensure memory dir exists: %w", err)
	}

	var extraGuidelines []string
	prompt := memory.BuildMemoryLines("auto memory", autoMemDir, extraGuidelines, skipIndex)
	return prompt, nil
}

// BuildMemoryPrompt 构建完整记忆提示词
func (s *MemoryService) BuildMemoryPrompt(ctx context.Context, displayName string, memoryDir string, extraGuidelines []string) (string, error) {
	return memory.BuildMemoryPrompt(displayName, memoryDir, extraGuidelines), nil
}

// ScanMemories 扫描记忆目录
func (s *MemoryService) ScanMemories(ctx context.Context, memoryDir string) ([]memory.MemoryHeader, error) {
	return memory.ScanMemoryFiles(memoryDir, s.repo)
}

// FormatManifest 格式化记忆清单
func (s *MemoryService) FormatManifest(memories []memory.MemoryHeader) string {
	return memory.FormatMemoryManifest(memories)
}

// TruncateEntrypointContent 截断入口文件内容
func (s *MemoryService) TruncateEntrypointContent(raw string) memory.TruncationResult {
	return memory.TruncateEntrypointContent(raw)
}

// EnsureMemoryDirExists 确保记忆目录存在
func (s *MemoryService) EnsureMemoryDirExists(ctx context.Context, memoryDir string) error {
	return memory.EnsureMemoryDirExists(ctx, s.repo, memoryDir)
}

// GetEntrypointContent 读取 MEMORY.md 原始内容（用于上下文注入）
func (s *MemoryService) GetEntrypointContent(ctx context.Context) (string, error) {
	if s.entrypoint == nil {
		return "", nil
	}
	return s.entrypoint.BuildContextSection(ctx)
}

// AppendEntry 追加记忆条目到 MEMORY.md
func (s *MemoryService) AppendEntry(ctx context.Context, title, content, category string) (*memory.EntryItem, error) {
	if s.entrypoint == nil {
		return nil, fmt.Errorf("entrypoint manager not initialized: call SetAutoMemDir first")
	}
	return s.entrypoint.AppendEntry(ctx, title, content, category)
}

// DeleteEntry 按 ID 删除记忆条目
func (s *MemoryService) DeleteEntry(ctx context.Context, id string) (bool, error) {
	if s.entrypoint == nil {
		return false, fmt.Errorf("entrypoint manager not initialized: call SetAutoMemDir first")
	}
	return s.entrypoint.DeleteEntry(ctx, id)
}

// ListEntries 列出所有记忆条目
func (s *MemoryService) ListEntries(ctx context.Context) ([]memory.EntryItem, error) {
	if s.entrypoint == nil {
		return nil, fmt.Errorf("entrypoint manager not initialized: call SetAutoMemDir first")
	}
	return s.entrypoint.ListEntries(ctx)
}

// SearchEntries 按关键词搜索记忆条目
func (s *MemoryService) SearchEntries(ctx context.Context, keyword string) ([]memory.EntryItem, error) {
	if s.entrypoint == nil {
		return nil, fmt.Errorf("entrypoint manager not initialized: call SetAutoMemDir first")
	}
	return s.entrypoint.SearchEntries(ctx, keyword)
}
