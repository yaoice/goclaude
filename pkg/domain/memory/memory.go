// Package memory 定义记忆系统领域模型
// 参考：src/memdir/memdir.ts, src/utils/claudemd.ts
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// ---------- 常量 ----------
const (
	EntrypointName      = "MEMORY.md"
	MaxEntrypointLines  = 200
	MaxEntrypointBytes  = 25000
	FrontmatterMaxLines = 30
	MaxMemoryFiles      = 200
)

// ---------- 类型定义 ----------
type MemoryType string

const (
	MemoryTypeUser    MemoryType = "user"
	MemoryTypeProject MemoryType = "project"
	MemoryTypeAuto    MemoryType = "auto"
	MemoryTypeTeam    MemoryType = "team"
)

type Memory struct {
	Content   string     `json:"content"`
	Path      string     `json:"path"`
	Type      MemoryType `json:"type"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type MemoryHeader struct {
	Filename    string     `json:"filename"`
	FilePath    string     `json:"file_path"`
	MtimeMs     int64      `json:"mtime_ms"`
	Description string     `json:"description,omitempty"`
	Type        MemoryType `json:"type,omitempty"`
}

type TruncationResult struct {
	Content          string `json:"content"`
	LineCount        int    `json:"line_count"`
	ByteCount        int    `json:"byte_count"`
	WasLineTruncated bool   `json:"was_line_truncated"`
	WasByteTruncated bool   `json:"was_byte_truncated"`
}

type Frontmatter struct {
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"`
	Paths       string `json:"paths,omitempty"`
	Raw         string `json:"-"`
}

// ---------- Repository 接口 ----------
type Repository interface {
	Load(ctx context.Context, path string) (*Memory, error)
	Save(ctx context.Context, memory *Memory) error
	Exists(ctx context.Context, path string) bool
	ReadDir(ctx context.Context, path string, recursive bool) ([]DirEntry, error)
	MkdirAll(ctx context.Context, path string) error
	Stat(ctx context.Context, path string) (FileInfo, error)
	RealPath(ctx context.Context, path string) (string, error)
	ReadFile(ctx context.Context, path string) (string, error)
	WriteFile(ctx context.Context, path string, content string) error
}

type DirEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
}

type FileInfo struct {
	Path      string    `json:"path"`
	IsDir     bool      `json:"is_dir"`
	IsSymlink bool      `json:"is_symlink"`
	ModTime   time.Time `json:"mod_time"`
	Size      int64     `json:"size"`
}

// ---------- 核心函数 ----------
func TruncateEntrypointContent(raw string) TruncationResult {
	trimmed := strings.TrimSpace(raw)
	contentLines := strings.Split(trimmed, "\n")
	lineCount := len(contentLines)
	byteCount := len(trimmed)

	result := TruncationResult{
		Content:   trimmed,
		LineCount: lineCount,
		ByteCount: byteCount,
	}

	if lineCount > MaxEntrypointLines {
		lines := strings.Split(trimmed, "\n")
		if len(lines) > MaxEntrypointLines {
			result.Content = strings.Join(lines[:MaxEntrypointLines], "\n")
			result.WasLineTruncated = true
			result.LineCount = MaxEntrypointLines
			result.ByteCount = len(result.Content)
		}
	}

	if result.ByteCount > MaxEntrypointBytes {
		if len(result.Content) > MaxEntrypointBytes {
			cutAt := strings.LastIndex(result.Content[:MaxEntrypointBytes], "\n")
			if cutAt > 0 {
				result.Content = result.Content[:cutAt]
			} else {
				result.Content = result.Content[:MaxEntrypointBytes]
			}
		}
		result.WasByteTruncated = true
		result.ByteCount = len(result.Content)
	}

	if result.WasLineTruncated || result.WasByteTruncated {
		reason := fmt.Sprintf("%d lines and %s", result.LineCount, FormatFileSize(result.ByteCount))
		result.Content += fmt.Sprintf("\n\n> WARNING: %s is %s. Keep index entries to one line under ~200 chars.", EntrypointName, reason)
	}

	return result
}

func FormatMemoryManifest(memories []MemoryHeader) string {
	var lines []string
	for _, m := range memories {
		tag := ""
		if m.Type != "" {
			tag = fmt.Sprintf("[%s] ", m.Type)
		}
		ts := time.UnixMilli(m.MtimeMs).UTC().Format(time.RFC3339)
		if m.Description != "" {
			lines = append(lines, fmt.Sprintf("- %s%s (%s): %s", tag, m.Filename, ts, m.Description))
		} else {
			lines = append(lines, fmt.Sprintf("- %s%s (%s)", tag, m.Filename, ts))
		}
	}
	return strings.Join(lines, "\n")
}

func ParseMemoryType(typeStr string) MemoryType {
	switch strings.ToLower(typeStr) {
	case "user":
		return MemoryTypeUser
	case "project":
		return MemoryTypeProject
	case "auto":
		return MemoryTypeAuto
	case "team":
		return MemoryTypeTeam
	default:
		return ""
	}
}

func FormatFileSize(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	} else if bytes < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
}

// IsAutoMemoryEnabled 检查自动记忆功能是否启用。
//
// 优先级链（首批命中即返回）：
//  1. GOCLAUDE_DISABLE_AUTO_MEMORY 环境变量（"1"/"true" → 关闭，"0"/"false" → 强制启用）
//  2. GOCLAUDE_SIMPLE / --bare 模式 → 关闭
//  3. 默认：启用
//
// 对齐 src/memdir/paths.ts:isAutoMemoryEnabled 的核心逻辑。
func IsAutoMemoryEnabled() bool {
	if v := os.Getenv("GOCLAUDE_DISABLE_AUTO_MEMORY"); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes":
			return false
		case "0", "false", "no":
			return true
		}
	}
	// --bare 模式：关闭 auto memory
	if v := os.Getenv("GOCLAUDE_SIMPLE"); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes":
			return false
		}
	}
	return true
}

func IsTeamMemoryEnabled() bool {
	return false
}

func ScanMemoryFiles(memoryDir string, repo Repository) ([]MemoryHeader, error) {
	entries, err := repo.ReadDir(context.Background(), memoryDir, true)
	if err != nil {
		return nil, err
	}

	var headers []MemoryHeader
	for _, entry := range entries {
		if entry.IsDir || !strings.HasSuffix(entry.Path, ".md") {
			continue
		}
		filename := strings.TrimPrefix(entry.Path, memoryDir+"/")
		if filename == EntrypointName {
			continue
		}
		info, err := repo.Stat(context.Background(), entry.Path)
		if err != nil {
			continue
		}
		header := MemoryHeader{
			Filename: filename,
			FilePath: entry.Path,
			MtimeMs:  info.ModTime.UnixMilli(),
		}
		headers = append(headers, header)
	}
	return headers, nil
}

func MarshalMemory(v interface{}) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

func UnmarshalMemory(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
