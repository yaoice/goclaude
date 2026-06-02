// Package git 提供 Git 操作封装
package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Repository Git仓库操作
type Repository struct {
	// root 仓库根目录
	root string
}

// 缓存git root查找结果
var (
	gitRootCache sync.Map
)

// FindRoot 从指定路径向上查找Git仓库根目录
func FindRoot(startPath string) (string, error) {
	// 检查缓存
	if cached, ok := gitRootCache.Load(startPath); ok {
		return cached.(string), nil
	}

	current := startPath
	for {
		gitPath := filepath.Join(current, ".git")
		info, err := os.Stat(gitPath)
		if err == nil {
			if info.IsDir() {
				// 正常的 .git 目录
				gitRootCache.Store(startPath, current)
				return current, nil
			}
			// .git 是文件 — worktree
			gitRootCache.Store(startPath, current)
			return current, nil
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("not a git repository: %s", startPath)
		}
		current = parent
	}
}

// NewRepository 创建Git仓库操作实例
func NewRepository(root string) *Repository {
	return &Repository{root: root}
}

// Root 获取仓库根目录
func (r *Repository) Root() string {
	return r.root
}

// Status 获取仓库状态
func (r *Repository) Status() (string, error) {
	return r.run("status", "--porcelain")
}

// Branch 获取当前分支名
func (r *Repository) Branch() (string, error) {
	output, err := r.run("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

// Log 获取最近的提交日志
func (r *Repository) Log(count int) (string, error) {
	return r.run("log", fmt.Sprintf("-%d", count), "--oneline")
}

// Diff 获取工作区差异
func (r *Repository) Diff(staged bool) (string, error) {
	args := []string{"diff"}
	if staged {
		args = append(args, "--staged")
	}
	return r.run(args...)
}

// Add 添加文件到暂存区
func (r *Repository) Add(paths ...string) error {
	args := append([]string{"add"}, paths...)
	_, err := r.run(args...)
	return err
}

// Commit 提交
func (r *Repository) Commit(message string) error {
	_, err := r.run("commit", "-m", message)
	return err
}

// RemoteURL 获取远程仓库URL
func (r *Repository) RemoteURL() (string, error) {
	output, err := r.run("remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

// IsClean 仓库是否干净（无未提交更改）
func (r *Repository) IsClean() (bool, error) {
	status, err := r.Status()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(status) == "", nil
}

// run 执行git命令
func (r *Repository) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.root

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), string(exitErr.Stderr))
		}
		return "", err
	}
	return string(output), nil
}
