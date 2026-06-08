// Package memory 提供 subagent 持久化记忆能力
//
// 对齐 src/tools/AgentTool/agentMemory.ts 的核心语义：
//   - 按 (agentType, scope) 隔离目录
//   - scope = user | project | local
//   - subagent 启动时读取目录中的所有 .md 文件作为系统提示词扩展
//   - subagent 通过 Read/Write/Edit 工具持久化记忆
//
// 三个 scope 的物理路径：
//
//	user:    ~/.goclaude/agent-memory/<agentType>/（兼容读取 ~/.claude）
//	project: <cwd>/.goclaude/agent-memory/<agentType>/（兼容读取 <cwd>/.claude）
//	local:   <cwd>/.goclaude/agent-memory-local/<agentType>/（兼容读取 <cwd>/.claude）
package memory

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Scope 记忆作用域
type Scope string

const (
	ScopeUser    Scope = "user"
	ScopeProject Scope = "project"
	ScopeLocal   Scope = "local"
)

// Service 记忆服务
type Service struct {
	HomeDir string
	Cwd     string
}

// NewService 创建服务
func NewService(homeDir, cwd string) *Service {
	if homeDir == "" {
		homeDir, _ = os.UserHomeDir()
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return &Service{HomeDir: homeDir, Cwd: cwd}
}

// Dir 返回某 (agentType, scope) 对应的写入目录。
func (s *Service) Dir(agentType string, scope Scope) string {
	return s.WriteDir(agentType, scope)
}

// Dirs 返回某 (agentType, scope) 的读取目录列表：主路径优先，legacy 路径兜底。
func (s *Service) Dirs(agentType string, scope Scope) []string {
	dirName := sanitize(agentType)
	var root string
	var sub string
	switch scope {
	case ScopeProject:
		root = s.Cwd
		sub = "agent-memory"
	case ScopeLocal:
		root = s.Cwd
		sub = "agent-memory-local"
	case ScopeUser:
		fallthrough
	default:
		root = s.HomeDir
		sub = "agent-memory"
	}
	return []string{
		filepath.Join(root, ".goclaude", sub, dirName),
		filepath.Join(root, ".claude", sub, dirName),
	}
}

// WriteDir 返回写入目录：优先 .goclaude；若只有 legacy .claude 已存在则继续写入 legacy。
func (s *Service) WriteDir(agentType string, scope Scope) string {
	dirs := s.Dirs(agentType, scope)
	if len(dirs) == 0 {
		return ""
	}
	if _, err := os.Stat(dirs[0]); err == nil {
		return dirs[0]
	}
	if len(dirs) > 1 {
		if _, err := os.Stat(dirs[1]); err == nil {
			return dirs[1]
		}
	}
	return dirs[0]
}

// EnsureDir 创建目录（若不存在）
func (s *Service) EnsureDir(agentType string, scope Scope) (string, error) {
	dir := s.WriteDir(agentType, scope)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// LoadPrompt 读取目录中所有 .md 文件并拼接成 prompt 片段
//
// 文件按文件名升序拼接；每个文件前加 "## <basename>\n" 标题。
// 缺失目录返回空字符串、nil 错误。
func (s *Service) LoadPrompt(agentType string, scope Scope) (string, error) {
	filesByName := make(map[string]string)
	for _, dir := range s.Dirs(agentType, scope) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return "", err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
				continue
			}
			if _, exists := filesByName[e.Name()]; exists {
				continue
			}
			filesByName[e.Name()] = filepath.Join(dir, e.Name())
		}
	}
	if len(filesByName) == 0 {
		return "", nil
	}

	files := make([]string, 0, len(filesByName))
	for name := range filesByName {
		files = append(files, name)
	}
	sort.Strings(files)

	var sb strings.Builder
	for _, name := range files {
		raw, err := os.ReadFile(filesByName[name])
		if err != nil {
			continue
		}
		sb.WriteString("## ")
		sb.WriteString(strings.TrimSuffix(name, filepath.Ext(name)))
		sb.WriteString("\n\n")
		sb.Write(raw)
		sb.WriteString("\n\n")
	}
	return strings.TrimSpace(sb.String()), nil
}

// Write 写入一条记忆文件（覆盖式）
//
// name 是相对文件名（如 "decisions.md"）；不允许跨目录。
func (s *Service) Write(agentType string, scope Scope, name, content string) error {
	if strings.Contains(name, "..") || strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("invalid memory file name: %q", name)
	}
	dir, err := s.EnsureDir(agentType, scope)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}

// sanitize 把 agentType 中不能做目录名的字符替换掉（与 src 一致：":" → "-"）
func sanitize(agentType string) string {
	return strings.ReplaceAll(agentType, ":", "-")
}
