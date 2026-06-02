// Package teamfs 是 team / mailbox 的文件系统持久化层。
//
// 路径布局（与 src 对齐，前缀按 goclaude 规范改为 ~/.goclaude/）：
//
//	~/.goclaude/teams/<sanitized-team>/
//	  config.json                    # team 元数据 + 成员列表（domain.team.File）
//	  inboxes/
//	    <sanitized-agent>.json       # 每个 agent 的收件箱（[]domain.team.Message）
//	    <sanitized-agent>.json.lock  # 写入锁（O_EXCL 哨兵文件）
//
// 并发安全：
//   - config.json：写入用 "tmp + rename" 保证原子性（POSIX rename 是原子的）
//   - inbox：在 <inbox>.lock 上做 O_CREATE|O_EXCL 抢占，配合指数退避重试
package teamfs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/anthropics/goclaude/internal/domain/team"
)

// ----- 路径 -----

// Layout 描述 team 数据的根目录布局。
//
// HomeDir 通常为用户家目录；测试中可以注入临时目录。
type Layout struct {
	// HomeDir 通常等于 os.UserHomeDir()
	HomeDir string
}

// DefaultLayout 用 os.UserHomeDir() 作为根。失败时落到当前目录。
func DefaultLayout() Layout {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	return Layout{HomeDir: home}
}

// TeamsRoot 返回 ~/.goclaude/teams/。
func (l Layout) TeamsRoot() string {
	return filepath.Join(l.HomeDir, ".goclaude", "teams")
}

// TeamDir 返回某个 team 的目录（已 sanitize）。
func (l Layout) TeamDir(teamName string) string {
	return filepath.Join(l.TeamsRoot(), team.SanitizeName(teamName))
}

// ConfigPath 返回 team 的 config.json 路径。
func (l Layout) ConfigPath(teamName string) string {
	return filepath.Join(l.TeamDir(teamName), "config.json")
}

// InboxesDir 返回 team 的 inboxes/ 目录。
func (l Layout) InboxesDir(teamName string) string {
	return filepath.Join(l.TeamDir(teamName), "inboxes")
}

// InboxPath 返回某个 agent 的 inbox 文件路径。
func (l Layout) InboxPath(teamName, agentName string) string {
	return filepath.Join(l.InboxesDir(teamName), team.SanitizeAgent(agentName)+".json")
}

// inboxLockPath 同名 + ".lock" 后缀，作为写入哨兵。
func (l Layout) inboxLockPath(teamName, agentName string) string {
	return l.InboxPath(teamName, agentName) + ".lock"
}

// ----- Store: team 元数据持久化 -----

// Store 负责 config.json 的读写。
//
// 所有方法都是并发安全的（读直接 ReadFile，写走 atomicWrite）。
// 注意：跨多个 goclaude 进程并发 Update 时仍可能丢更新——上层调用方
// （application.TeamService）需要按 leader-only 原则避免冲突写入。
type Store struct {
	Layout Layout
}

// NewStore 用给定 layout 构造 Store。
func NewStore(l Layout) *Store {
	return &Store{Layout: l}
}

// Read 读取 team 的 config.json。
//
// 如果文件不存在，返回 (nil, nil)。
func (s *Store) Read(teamName string) (*team.File, error) {
	path := s.Layout.ConfigPath(teamName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read team file %s: %w", path, err)
	}
	var f team.File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("decode team file %s: %w", path, err)
	}
	return &f, nil
}

// Write 原子写入 config.json。
//
// 调用方必须先通过 file.Validate() 校验。
func (s *Store) Write(f *team.File) error {
	if err := f.Validate(); err != nil {
		return err
	}
	dir := s.Layout.TeamDir(f.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encode team file: %w", err)
	}
	return atomicWrite(s.Layout.ConfigPath(f.Name), data)
}

// Delete 删除 team 目录（含 config.json + inboxes/）。
//
// 如果 team 不存在，返回 (false, nil)。
func (s *Store) Delete(teamName string) (bool, error) {
	dir := s.Layout.TeamDir(teamName)
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("stat %s: %w", dir, err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return false, fmt.Errorf("remove team dir %s: %w", dir, err)
	}
	return true, nil
}

// List 列出所有 team 目录名（已 sanitize）。
//
// 不存在时返回空切片，无错误。
func (s *Store) List() ([]string, error) {
	root := s.Layout.TeamsRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("readdir %s: %w", root, err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}
