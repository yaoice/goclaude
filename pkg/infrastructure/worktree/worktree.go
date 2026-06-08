// Package worktree 提供 git worktree 隔离能力
//
// 对齐 src/utils/worktree.ts 的核心 enter/exit 语义：
//   - Create: 在临时目录创建新的 git worktree（基于当前 HEAD）
//   - Cleanup: 通过 `git worktree remove --force` 清理
//
// 用于 subagent 的 isolation: "worktree" 选项：在隔离目录跑命令避免污染主仓库。
package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Worktree 一个已创建的 worktree
type Worktree struct {
	// Path 隔离目录绝对路径
	Path string
	// Branch 创建时的分支名（agent-<id>）
	Branch string
	// Repo 源仓库根目录
	Repo string

	cleanupMu   sync.Mutex
	cleanupDone bool
}

// Service 管理 worktree 生命周期
type Service struct {
	// BaseDir 创建 worktree 的父目录；空则用 os.TempDir()
	BaseDir string
}

// NewService 创建服务
func NewService(baseDir string) *Service {
	return &Service{BaseDir: baseDir}
}

// IsGitRepo 判断给定目录是否在 git 工作区中
func IsGitRepo(dir string) bool {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// gitRoot 返回 dir 所在 git 仓库的根目录
func gitRoot(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Create 在隔离目录创建一个新的 git worktree
//
// 实现细节：
//   - 在 BaseDir 下创建独立目录（带时间戳防冲突）
//   - 用 "git worktree add -b <branch> <path>" 基于当前 HEAD 创建
//   - 失败时清理已创建的目录
func (s *Service) Create(ctx context.Context, srcDir, agentID string) (*Worktree, error) {
	if !IsGitRepo(srcDir) {
		return nil, errors.New("worktree: source dir is not a git repository")
	}
	root, err := gitRoot(srcDir)
	if err != nil {
		return nil, fmt.Errorf("worktree: get git root: %w", err)
	}

	base := s.BaseDir
	if base == "" {
		base = os.TempDir()
	}
	name := fmt.Sprintf("claude-wt-%s-%d", sanitize(agentID), time.Now().UnixNano())
	wtPath := filepath.Join(base, name)
	branch := "claude/" + sanitize(agentID)

	cmd := exec.CommandContext(ctx, "git", "-C", root, "worktree", "add", "-b", branch, wtPath, "HEAD")
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(wtPath)
		return nil, fmt.Errorf("worktree add failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}

	return &Worktree{
		Path:   wtPath,
		Branch: branch,
		Repo:   root,
	}, nil
}

// Cleanup 移除 worktree 与对应分支
//
// 调用语义：成功后变为 no-op；失败时不标记完成，调用方可重试。
// 这与典型 sync.Once 的"无脑只跑一次"不同——`git worktree remove`
// 偶尔会因为锁冲突或并发提交失败，重试几次就能成功。
func (w *Worktree) Cleanup(ctx context.Context) error {
	w.cleanupMu.Lock()
	defer w.cleanupMu.Unlock()
	if w.cleanupDone {
		return nil
	}

	// git worktree remove --force <path>
	cmd := exec.CommandContext(ctx, "git", "-C", w.Repo, "worktree", "remove", "--force", w.Path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// 兜底：直接删目录
		_ = os.RemoveAll(w.Path)
		// 即便 git worktree remove 报错，分支删除仍尝试
		_ = exec.CommandContext(ctx, "git", "-C", w.Repo, "branch", "-D", w.Branch).Run()
		return fmt.Errorf("worktree remove failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	// 删分支（忽略错误：分支可能未提交内容）
	_ = exec.CommandContext(ctx, "git", "-C", w.Repo, "branch", "-D", w.Branch).Run()
	w.cleanupDone = true
	return nil
}

// sanitize 将 id 转为安全的文件名片段
func sanitize(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '-' || c == '_':
			out = append(out, c)
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}
