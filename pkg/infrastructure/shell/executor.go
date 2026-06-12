// Package shell 提供 Shell 命令执行功能（支持沙箱）
package shell

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/yaoice/goclaude/pkg/infrastructure/sandbox"
)

// Executor Shell命令执行器（支持沙箱）
type Executor struct {
	// workDir 工作目录
	workDir string
	// env 环境变量
	env []string
	// timeout 默认超时
	timeout time.Duration
	// sandbox 沙箱（nil = 不使用沙箱）
	sandbox *sandbox.Sandbox
}

// NewExecutor 创建Shell执行器（原始接口，向后兼容）
func NewExecutor(workDir string, timeout time.Duration) *Executor {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &Executor{
		workDir: workDir,
		timeout: timeout,
		sandbox: nil, // 默认不使用沙箱
	}
}

// NewExecutorWithSandbox 创建带沙箱的Shell执行器
func NewExecutorWithSandbox(workDir string, timeout time.Duration, cfg *sandbox.Config) (*Executor, error) {
	exec := NewExecutor(workDir, timeout)

	if cfg != nil && cfg.Enabled {
		sb, err := sandbox.New(cfg, workDir, timeout)
		if err != nil {
			return nil, fmt.Errorf("failed to create sandbox: %w", err)
		}
		exec.sandbox = sb
	}

	return exec, nil
}

// SetEnv 设置环境变量
func (e *Executor) SetEnv(env map[string]string) {
	for k, v := range env {
		e.env = append(e.env, fmt.Sprintf("%s=%s", k, v))
	}
}

// SetSandbox 设置沙箱（支持热重载）
func (e *Executor) SetSandbox(sb *sandbox.Sandbox) {
	e.sandbox = sb
}

// GetSandbox 获取当前沙箱（可能为 nil）
func (e *Executor) GetSandbox() *sandbox.Sandbox {
	return e.sandbox
}

// ExecResult 命令执行结果
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

// Execute 执行Shell命令（自动判断是否使用沙箱）
func (e *Executor) Execute(ctx context.Context, command string) (*ExecResult, error) {
	// 如果启用了沙箱，使用沙箱执行
	if e.sandbox != nil && e.sandbox.Enabled() {
		return e.executeSandboxed(ctx, command)
	}

	// 否则直接执行（原始行为）
	return e.executeDirect(ctx, command)
}

// ExecuteStreaming 流式执行Shell命令（实时输出，自动判断是否使用沙箱）
func (e *Executor) ExecuteStreaming(ctx context.Context, command string, onOutput func(string)) (*ExecResult, error) {
	// 如果启用了沙箱，使用沙箱执行
	if e.sandbox != nil && e.sandbox.Enabled() {
		return e.executeStreamingSandboxed(ctx, command, onOutput)
	}

	// 否则直接执行（原始行为）
	return e.executeStreamingDirect(ctx, command, onOutput)
}

// ── 直接执行（原始实现，未改变）─────────────────────

func (e *Executor) executeDirect(ctx context.Context, command string) (*ExecResult, error) {
	// 如果没有传入deadline的context，使用默认超时
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = e.workDir
	if len(e.env) > 0 {
		cmd.Env = append(cmd.Environ(), e.env...)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	result := &ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration,
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else if ctx.Err() != nil {
			return result, fmt.Errorf("command timed out after %v", e.timeout)
		} else {
			return result, err
		}
	}

	return result, nil
}

func (e *Executor) executeStreamingDirect(ctx context.Context, command string, onOutput func(string)) (*ExecResult, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = e.workDir
	if len(e.env) > 0 {
		cmd.Env = append(cmd.Environ(), e.env...)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	var fullOutput strings.Builder

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdoutPipe.Read(buf)
			if n > 0 {
				chunk := string(buf[:n])
				fullOutput.WriteString(chunk)
				if onOutput != nil {
					onOutput(chunk)
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}
		}
	}()

	stderrBytes, _ := io.ReadAll(stderrPipe)
	err = cmd.Wait()
	duration := time.Since(start)

	result := &ExecResult{
		Stdout:   fullOutput.String(),
		Stderr:   string(stderrBytes),
		Duration: duration,
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			return result, err
		}
	}

	return result, nil
}

// ── 沙箱执行 ──────────────────────────────────────────

func (e *Executor) executeSandboxed(ctx context.Context, command string) (*ExecResult, error) {
	// 使用沙箱包装命令
	cmd := e.sandbox.WrapCommand(ctx, command)

	// 设置 IO
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if len(e.env) > 0 {
		cmd.Env = append(cmd.Environ(), e.env...)
	}

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	result := &ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration,
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else if ctx.Err() != nil {
			return result, fmt.Errorf("sandboxed command timed out after %v", e.timeout)
		} else {
			return result, fmt.Errorf("sandboxed command failed: %w", err)
		}
	}

	return result, nil
}

func (e *Executor) executeStreamingSandboxed(ctx context.Context, command string, onOutput func(string)) (*ExecResult, error) {
	cmd := e.sandbox.WrapCommand(ctx, command)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to get stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start sandboxed command: %w", err)
	}

	var fullOutput strings.Builder

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdoutPipe.Read(buf)
			if n > 0 {
				chunk := string(buf[:n])
				fullOutput.WriteString(chunk)
				if onOutput != nil {
					onOutput(chunk)
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}
		}
	}()

	stderrBytes, _ := io.ReadAll(stderrPipe)
	err = cmd.Wait()
	duration := time.Since(start)

	result := &ExecResult{
		Stdout:   fullOutput.String(),
		Stderr:   string(stderrBytes),
		Duration: duration,
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			return result, fmt.Errorf("sandboxed command failed: %w", err)
		}
	}

	return result, nil
}

// ── 辅助函数：检查是否应使用沙箱 ─────────────────

// ShouldUseSandbox 判断特定命令是否应使用沙箱
// 由 BashTool 在调用 Execute() 前使用
func (e *Executor) ShouldUseSandbox(command string, dangerouslyDisable bool) bool {
	if e.sandbox == nil {
		return false
	}
	return e.sandbox.ShouldUseSandbox(command, dangerouslyDisable)
}
