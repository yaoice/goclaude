package shell

import (
	"context"
	"testing"
	"time"

	"github.com/anthropics/goclaude/internal/infrastructure/sandbox"
)

// TestExecutorWithSandboxIntegration 验证 Executor 与 Sandbox 的集成
func TestExecutorWithSandboxIntegration(t *testing.T) {
	// 创建沙箱配置
	cfg := sandbox.DefaultConfig()
	cfg.Enabled = true

	// 创建带沙箱的 Executor
	executor, err := NewExecutorWithSandbox("/tmp", 30*time.Second, cfg)
	if err != nil {
		t.Skipf("Sandbox not available: %v", err)
		return
	}

	if executor.GetSandbox() == nil {
		t.Error("Executor should have sandbox when cfg.Enabled=true")
	}

	ctx := context.Background()

	// 测试 1: 简单命令执行
	t.Run("SimpleCommand", func(t *testing.T) {
		result, err := executor.Execute(ctx, "echo hello from sandbox")
		if err != nil {
			t.Errorf("Execute failed: %v", err)
		}
		if result.Stdout != "hello from sandbox\n" {
			t.Errorf("Expected 'hello from sandbox\\n', got %q", result.Stdout)
		}
	})

	// 测试 2: 网络隔离
	t.Run("NetworkIsolation", func(t *testing.T) {
		cfg := sandbox.DefaultConfig()
		cfg.Enabled = true
		cfg.Network.DisableNetwork = true

		executor2, err := NewExecutorWithSandbox("/tmp", 30*time.Second, cfg)
		if err != nil {
			t.Skipf("Sandbox not available: %v", err)
		}

		// 尝试 curl (应该失败或被隔离)
		result, err := executor2.Execute(ctx, "curl -s --max-time 2 https://www.google.com || echo 'network blocked'")
		if err != nil {
			t.Logf("Network correctly blocked: %v", err)
		} else {
			t.Logf("Output: %s", result.Stdout)
		}
	})

	// 测试 3: 流式执行
	t.Run("StreamingExecution", func(t *testing.T) {
		var outputCount int
		result, err := executor.ExecuteStreaming(ctx, "for i in 1 2 3; do echo $i; sleep 0.1; done", func(chunk string) {
			outputCount++
			t.Logf("Chunk: %q", chunk)
		})

		if err != nil {
			t.Errorf("ExecuteStreaming failed: %v", err)
		}

		if result.ExitCode != 0 {
			t.Errorf("Expected exit code 0, got %d", result.ExitCode)
		}

		t.Logf("Total output: %s", result.Stdout)
	})

	// 测试 4: 禁用沙箱
	t.Run("DisableSandbox", func(t *testing.T) {
		executor3 := NewExecutor("/tmp", 30*time.Second)
		if executor3.GetSandbox() != nil {
			t.Error("Executor should not have sandbox when created with NewExecutor")
		}

		result, err := executor3.Execute(ctx, "echo hello without sandbox")
		if err != nil {
			t.Errorf("Execute without sandbox failed: %v", err)
		}

		if result.Stdout != "hello without sandbox\n" {
			t.Errorf("Expected 'hello without sandbox\\n', got %q", result.Stdout)
		}
	})
}
