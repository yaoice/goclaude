package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// 抑制 user.* 错误
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@t.t",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@t.t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")
	return dir
}

func TestIsGitRepo(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not available")
	}
	repo := initRepo(t)
	if !IsGitRepo(repo) {
		t.Error("repo should be git")
	}
	if IsGitRepo(t.TempDir()) {
		t.Error("empty dir should not be git")
	}
}

func TestService_CreateAndCleanup(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not available")
	}
	repo := initRepo(t)
	svc := NewService(t.TempDir())

	wt, err := svc.Create(context.Background(), repo, "agent-test1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if wt == nil {
		t.Fatal("nil worktree")
	}
	// 验证 worktree 目录存在且包含 README
	if _, err := os.Stat(filepath.Join(wt.Path, "README.md")); err != nil {
		t.Errorf("worktree missing README: %v", err)
	}
	// 验证分支名
	if wt.Branch == "" {
		t.Error("empty branch")
	}

	// 清理
	if err := wt.Cleanup(context.Background()); err != nil {
		t.Errorf("cleanup: %v", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Errorf("worktree dir should be gone: %v", err)
	}

	// 再次 cleanup 应幂等
	if err := wt.Cleanup(context.Background()); err != nil {
		t.Errorf("second cleanup: %v", err)
	}
}

func TestService_NotARepo(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not available")
	}
	svc := NewService(t.TempDir())
	_, err := svc.Create(context.Background(), t.TempDir(), "x")
	if err == nil {
		t.Error("expected error on non-repo")
	}
}
