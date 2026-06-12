// Package filesystem 提供文件系统操作封装
package filesystem

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FS 文件系统操作接口（方便测试时mock）
type FS interface {
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, perm os.FileMode) error
	Stat(path string) (os.FileInfo, error)
	MkdirAll(path string, perm os.FileMode) error
	Remove(path string) error
	ReadDir(path string) ([]os.DirEntry, error)
}

// OsFS 真实文件系统实现
type OsFS struct{}

func (OsFS) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
func (OsFS) WriteFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}
func (OsFS) Stat(path string) (os.FileInfo, error)        { return os.Stat(path) }
func (OsFS) MkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }
func (OsFS) Remove(path string) error                     { return os.Remove(path) }
func (OsFS) ReadDir(path string) ([]os.DirEntry, error)   { return os.ReadDir(path) }

// Service 文件系统服务
type Service struct {
	fs FS
}

// NewService 创建文件系统服务
func NewService(fs FS) *Service {
	if fs == nil {
		fs = OsFS{}
	}
	return &Service{fs: fs}
}

// ReadFile 安全读取文件
// 支持 offset/limit 参数（按行截取）
func (s *Service) ReadFile(path string, offset, limit int) (string, error) {
	// 安全检查：阻止读取设备文件
	if isBlockedPath(path) {
		return "", fmt.Errorf("access denied: %s is a blocked path", path)
	}

	data, err := s.fs.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file %s: %w", path, err)
	}

	content := string(data)

	// 按行截取
	if offset > 0 || limit > 0 {
		lines := strings.Split(content, "\n")
		if offset >= len(lines) {
			return "", nil
		}
		if offset > 0 {
			lines = lines[offset:]
		}
		if limit > 0 && limit < len(lines) {
			lines = lines[:limit]
		}
		content = strings.Join(lines, "\n")
	}

	return content, nil
}

// WriteFile 安全写入文件
func (s *Service) WriteFile(path, content string) error {
	// 确保父目录存在
	dir := filepath.Dir(path)
	if err := s.fs.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	return s.fs.WriteFile(path, []byte(content), 0644)
}

// FileExists 检查文件是否存在
func (s *Service) FileExists(path string) bool {
	_, err := s.fs.Stat(path)
	return err == nil
}

// ListDir 列出目录内容
func (s *Service) ListDir(path string) ([]string, error) {
	entries, err := s.fs.ReadDir(path)
	if err != nil {
		return nil, err
	}

	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	return names, nil
}

// CopyFile 复制文件
func (s *Service) CopyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dir := filepath.Dir(dst)
	if err := s.fs.MkdirAll(dir, 0755); err != nil {
		return err
	}

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// isBlockedPath 检查是否为阻止访问的路径
func isBlockedPath(path string) bool {
	blocked := []string{"/dev/zero", "/dev/random", "/dev/urandom", "/dev/null"}
	for _, b := range blocked {
		if path == b {
			return true
		}
	}
	return false
}
