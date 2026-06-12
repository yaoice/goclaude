// Package rules implements rules system file IO.
// Reference TS implementation: src/utils/fsOperations.ts
package rules

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yaoice/goclaude/pkg/infrastructure/configdir"
)

// EntrypointName is the memory entrypoint filename
const EntrypointName = "MEMORY.md"

// FileRepository is a filesystem-based rules persistence implementation.
type FileRepository struct {
	fs FileSystem
}

// NewFileRepository creates a new filesystem-based repository.
func NewFileRepository(fs FileSystem) *FileRepository {
	return &FileRepository{fs: fs}
}

// FileSystem is the filesystem abstraction interface.
type FileSystem interface {
	ReadFile(path string) (string, error)
	ReadFileRange(path string, maxLines int) (content string, mtimeMs int64, err error)
	ReadDir(path string, recursive bool) ([]DirEntry, error)
	Stat(path string) (FileInfo, error)
	RealPath(path string) (string, error)
	Exists(path string) bool
	WriteFile(path string, content string) error
	MkdirAll(path string) error
}

// OSFileSystem is the OS filesystem implementation.
type OSFileSystem struct{}

// ReadFile reads file content.
func (OSFileSystem) ReadFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file %s: %w", path, err)
	}
	return string(data), nil
}

// ReadFileRange reads the first N lines of a file.
func (OSFileSystem) ReadFileRange(path string, maxLines int) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("failed to open file %s: %w", path, err)
	}
	defer file.Close()

	var content strings.Builder
	lineCount := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() && lineCount < maxLines {
		content.WriteString(scanner.Text())
		content.WriteString("\n")
		lineCount++
	}

	info, err := file.Stat()
	if err != nil {
		return content.String(), 0, nil
	}

	return content.String(), info.ModTime().UnixMilli(), nil
}

// ReadDir reads directory entries.
func (OSFileSystem) ReadDir(path string, recursive bool) ([]DirEntry, error) {
	var entries []DirEntry

	if recursive {
		err := filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // skip errors
			}
			entries = append(entries, DirEntry{
				Name:  info.Name(),
				Path:  p,
				IsDir: info.IsDir(),
			})
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("failed to walk directory %s: %w", path, err)
		}
	} else {
		dirEntries, err := os.ReadDir(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read directory %s: %w", path, err)
		}
		for _, entry := range dirEntries {
			entries = append(entries, DirEntry{
				Name:  entry.Name(),
				Path:  filepath.Join(path, entry.Name()),
				IsDir: entry.IsDir(),
			})
		}
	}

	return entries, nil
}

// Stat gets file info.
func (OSFileSystem) Stat(path string) (FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return FileInfo{}, fmt.Errorf("failed to stat %s: %w", path, err)
	}

	isSymlink := false
	if info.Mode()&os.ModeSymlink != 0 {
		isSymlink = true
	}

	return FileInfo{
		Path:      path,
		IsDir:     info.IsDir(),
		IsSymlink: isSymlink,
		ModTime:   info.ModTime(),
		Size:      info.Size(),
	}, nil
}

// RealPath resolves symlinks.
func (OSFileSystem) RealPath(path string) (string, error) {
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("failed to eval symlinks for %s: %w", path, err)
	}
	return realPath, nil
}

// Exists checks if a path exists.
func (OSFileSystem) Exists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

// WriteFile writes content to a file.
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

// MkdirAll creates a directory recursively.
func (OSFileSystem) MkdirAll(path string) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", path, err)
	}
	return nil
}

// GetUserMemoryPath gets the user-level memory file path.
func GetUserMemoryPath(homeDir string) string {
	return configdir.JoinPrimary(homeDir, EntrypointName)
}

// GetProjectMemoryPath gets the project-level memory file path.
func GetProjectMemoryPath(projectRoot string) string {
	return configdir.JoinPrimary(projectRoot, EntrypointName)
}

// GetManagedClaudeRulesDir gets the managed rules directory.
func GetManagedClaudeRulesDir() string {
	return "/etc/claude-code/rules"
}

// GetUserClaudeRulesDir gets the user rules directory.
func GetUserClaudeRulesDir(homeDir string) string {
	return configdir.JoinPrimary(homeDir, "rules")
}

// DirEntry is a directory entry.
type DirEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
}

// FileInfo is file information.
type FileInfo struct {
	Path      string    `json:"path"`
	IsDir     bool      `json:"is_dir"`
	IsSymlink bool      `json:"is_symlink"`
	ModTime   time.Time `json:"mod_time"`
	Size      int64     `json:"size"`
}
