// Package rules 测试规则系统
package rules

import (
	"context"
	"testing"
)

// MockRepository 模拟仓库（用于测试）
type MockRepository struct{}

func (m *MockRepository) ReadFile(ctx context.Context, path string) (string, error) {
	return "", nil
}

func (m *MockRepository) ReadFileRange(ctx context.Context, path string, maxLines int) (string, int64, error) {
	return "", 0, nil
}

func (m *MockRepository) ReadDir(ctx context.Context, path string, recursive bool) ([]DirEntry, error) {
	return []DirEntry{}, nil
}

func (m *MockRepository) Stat(ctx context.Context, path string) (FileInfo, error) {
	return FileInfo{}, nil
}

func (m *MockRepository) RealPath(ctx context.Context, path string) (string, error) {
	return path, nil
}

func (m *MockRepository) Exists(ctx context.Context, path string) bool {
	return false
}

func (m *MockRepository) WriteFile(ctx context.Context, path string, content string) error {
	return nil
}

func (m *MockRepository) MkdirAll(ctx context.Context, path string) error {
	return nil
}

// TestParseFrontmatter 测试 frontmatter 解析
func TestParseFrontmatter(t *testing.T) {
	content := `---
description: Test memory
type: user
paths: src/**, test/**
---
# Memory Content

This is a test memory.`

	fm, remaining := ParseFrontmatter(content, "test.md")

	if fm.Description != "Test memory" {
		t.Errorf("Expected description 'Test memory', got '%s'", fm.Description)
	}

	if fm.Type != "user" {
		t.Errorf("Expected type 'user', got '%s'", fm.Type)
	}

	if fm.Paths != "src/**, test/**" {
		t.Errorf("Expected paths 'src/**, test/**', got '%s'", fm.Paths)
	}

	if remaining != "# Memory Content\n\nThis is a test memory." {
		t.Errorf("Unexpected remaining content: '%s'", remaining)
	}
}

// TestParseFrontmatterPaths 测试 frontmatter paths 解析
func TestParseFrontmatterPaths(t *testing.T) {
	pathsStr := "src/**, test/**, docs/*"
	paths := ParseFrontmatterPaths(pathsStr)

	if len(paths) != 2 {
		t.Errorf("Expected 2 paths, got %d", len(paths))
	}

	if len(paths) > 0 && paths[0] != "src" {
		t.Errorf("Expected first path 'src', got '%s'", paths[0])
	}
}

// TestStripHtmlComments 测试 HTML 注释去除
func TestStripHtmlComments(t *testing.T) {
	content := `# Test

<!-- This is a comment -->

Actual content.`

	result, stripped := StripHtmlComments(content)

	if !stripped {
		t.Error("Expected stripped to be true")
	}

	expected := "# Test\n\nActual content."
	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

// TestIsTextFile 测试文本文件检查
func TestIsTextFile(t *testing.T) {
	tests := []struct {
		ext  string
		want bool
	}{
		{".md", true},
		{".txt", true},
		{".go", true},
		{".py", true},
		{".jpg", false},
		{".png", false},
	}

	for _, tt := range tests {
		if got := IsTextFile(tt.ext); got != tt.want {
			t.Errorf("IsTextFile(%s) = %v, want %v", tt.ext, got, tt.want)
		}
	}
}

// TestFormatMemoryContent 测试记忆内容格式化
func TestFormatMemoryContent(t *testing.T) {
	files := []MemoryFileInfo{
		{
			Path:    "/home/user/.claude/CLAUDE.md",
			Type:    MemoryTypeUser,
			Content: "User instructions",
		},
		{
			Path:    "/project/CLAUDE.md",
			Type:    MemoryTypeProject,
			Content: "Project instructions",
		},
	}

	result := FormatMemoryContent(files, nil)

	if result == "" {
		t.Error("Expected non-empty result")
	}

	// 检查是否包含记忆指令提示
	if !contains(result, "Codebase and user instructions are shown below") {
		t.Error("Expected memory instruction prompt")
	}
}

// TestMemoryTypeString 测试记忆类型字符串
func TestMemoryTypeString(t *testing.T) {
	tests := []struct {
		mt   MemoryType
		want string
	}{
		{MemoryTypeManaged, "Managed"},
		{MemoryTypeUser, "User"},
		{MemoryTypeProject, "Project"},
		{MemoryTypeLocal, "Local"},
		{MemoryTypeAutoMem, "AutoMem"},
		{MemoryTypeTeamMem, "TeamMem"},
	}

	for _, tt := range tests {
		if string(tt.mt) != tt.want {
			t.Errorf("MemoryType(%s) = %s, want %s", tt.mt, string(tt.mt), tt.want)
		}
	}
}

// 辅助函数
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0)
}
