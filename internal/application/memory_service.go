// Package application 实现记忆应用服务
package application

import (
	"context"
	"fmt"

	memory "github.com/anthropics/goclaude/internal/domain/memory"
)

// MemoryService 记忆应用服务
type MemoryService struct {
	repo memory.Repository
}

// NewMemoryService 创建记忆服务
func NewMemoryService(repo memory.Repository) *MemoryService {
	return &MemoryService{repo: repo}
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
