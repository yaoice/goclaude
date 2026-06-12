// Package memory 实现基于文件系统的 Repository
// 参考：src/utils/fsOperations.ts
package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	memory "github.com/yaoice/goclaude/pkg/domain/memory"
)

// FileRepository 基于文件系统的实现
type FileRepository struct {
	fs FileSystem
}

func NewFileRepository(fs FileSystem) *FileRepository {
	return &FileRepository{fs: fs}
}

// FileSystem 文件系统抽象接口
type FileSystem interface {
	ReadFile(path string) (string, error)
	WriteFile(path string, content string) error
	MkdirAll(path string) error
	ReadDir(path string, recursive bool) ([]memory.DirEntry, error)
	Stat(path string) (memory.FileInfo, error)
	RealPath(path string) (string, error)
	Exists(path string) bool
}

// OSFileSystem 操作系统文件系统实现
type OSFileSystem struct{}

func (OSFileSystem) ReadFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file %s: %w", path, err)
	}
	return string(data), nil
}

func (OSFileSystem) WriteFile(path string, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", path, err)
	}
	return nil
}

func (OSFileSystem) MkdirAll(path string) error {
	return os.MkdirAll(path, 0755)
}

func (OSFileSystem) ReadDir(path string, recursive bool) ([]memory.DirEntry, error) {
	var entries []memory.DirEntry
	if recursive {
		err := filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			entries = append(entries, memory.DirEntry{Name: info.Name(), Path: p, IsDir: info.IsDir()})
			return nil
		})
		return entries, err
	}
	dirEntries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	for _, entry := range dirEntries {
		entries = append(entries, memory.DirEntry{Name: entry.Name(), Path: filepath.Join(path, entry.Name()), IsDir: entry.IsDir()})
	}
	return entries, nil
}

func (OSFileSystem) Stat(path string) (memory.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return memory.FileInfo{}, err
	}
	isSymlink := info.Mode()&os.ModeSymlink != 0
	return memory.FileInfo{Path: path, IsDir: info.IsDir(), IsSymlink: isSymlink, ModTime: info.ModTime(), Size: info.Size()}, nil
}

func (OSFileSystem) RealPath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}

func (OSFileSystem) Exists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

// 实现 Repository 接口
func (r *FileRepository) Load(ctx context.Context, path string) (*memory.Memory, error) {
	content, err := r.fs.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return &memory.Memory{Content: content, Path: path, Type: memory.ParseMemoryType("")}, nil
}

func (r *FileRepository) Save(ctx context.Context, m *memory.Memory) error {
	return r.fs.WriteFile(m.Path, m.Content)
}

func (r *FileRepository) Exists(ctx context.Context, path string) bool {
	return r.fs.Exists(path)
}

func (r *FileRepository) ReadDir(ctx context.Context, path string, recursive bool) ([]memory.DirEntry, error) {
	return r.fs.ReadDir(path, recursive)
}

func (r *FileRepository) MkdirAll(ctx context.Context, path string) error {
	return r.fs.MkdirAll(path)
}

func (r *FileRepository) Stat(ctx context.Context, path string) (memory.FileInfo, error) {
	return r.fs.Stat(path)
}

func (r *FileRepository) RealPath(ctx context.Context, path string) (string, error) {
	return r.fs.RealPath(path)
}

func (r *FileRepository) ReadFile(ctx context.Context, path string) (string, error) {
	return r.fs.ReadFile(path)
}

func (r *FileRepository) WriteFile(ctx context.Context, path string, content string) error {
	return r.fs.WriteFile(path, content)
}

// EnsureMemoryDirExists 确保记忆目录存在
func EnsureMemoryDirExists(ctx context.Context, repo memory.Repository, memoryDir string) error {
	return repo.MkdirAll(ctx, memoryDir)
}

// LoadMemoryPrompt 加载记忆提示词
func LoadMemoryPrompt(ctx context.Context, repo memory.Repository, autoMemDir string, skipIndex bool) (string, error) {
	if !memory.IsAutoMemoryEnabled() {
		return "", nil
	}
	err := EnsureMemoryDirExists(ctx, repo, autoMemDir)
	if err != nil {
		return "", err
	}
	return memory.BuildMemoryLines("auto memory", autoMemDir, nil, skipIndex), nil
}
