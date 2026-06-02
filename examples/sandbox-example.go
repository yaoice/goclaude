// Package main - example usage of goclaude sandbox.
//
// This example demonstrates how to:
//   1. Create a sandbox with custom config
//   2. Wrap bash commands with OS-level isolation
//   3. Execute commands inside the sandbox
//   4. Handle errors and cleanup
//
// Usage:
//
//	go run sandbox-example.go
//
// Prerequisites:
//   - Linux: install bubblewrap (apt install bubblewrap)
//   - macOS: no prerequisites (sandbox-exec is built-in)
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/anthropics/goclaude/internal/infrastructure/sandbox"
	"github.com/anthropics/goclaude/internal/infrastructure/shell"
)

func main() {
	fmt.Println("=== GoClaude Sandbox Example ===")
	fmt.Println()

	// ── Step 1: Create sandbox config ──────────────────
	fmt.Println("[1] Creating sandbox config...")

	cfg := sandbox.DefaultConfig()
	cfg.Enabled = true

	// Allow reading from home directory
	cfg.FSRead.Allow = append(cfg.FSRead.Allow, "~", "/tmp")

	// Allow writing to workspace and temp
	cfg.FSWrite.Allow = append(cfg.FSWrite.Allow, ".", "/tmp")

	// Deny writing to sensitive paths
	cfg.FSWrite.Deny = append(cfg.FSWrite.Deny,
		"~/.ssh",
		"~/.aws",
		"~/.bashrc",
	)

	// Disable network (uncomment to test network isolation)
	// cfg.Network.DisableNetwork = true

	fmt.Printf("    Config: enabled=%v, platform=%s\n",
		cfg.Enabled, sandbox.DetectPlatform())
	fmt.Println()

	// ── Step 2: Create sandbox instance ─────────────────
	fmt.Println("[2] Creating sandbox instance...")

	sb, err := sandbox.New(cfg, "/tmp", 30*time.Second)
	if err != nil {
		log.Printf("    WARN: Sandbox not available: %v", err)
		log.Printf("    Continuing without sandbox...")
		sb = nil
	} else {
		defer sb.Cleanup()

		fmt.Printf("    Sandbox created: enabled=%v, platform=%s\n",
			sb.Enabled(), sb.Platform())
	}
	fmt.Println()

	// ── Step 3: Create executor (with or without sandbox) ──
	fmt.Println("[3] Creating shell executor...")

	var exec *shell.Executor

	if sb != nil && sb.Enabled() {
		// Create executor with sandbox
		exec, err = shell.NewExecutorWithSandbox("/tmp", 30*time.Second, cfg)
		if err != nil {
			log.Printf("    WARN: Failed to create sandboxed executor: %v", err)
			log.Printf("    Falling back to direct execution...")
			exec = shell.NewExecutor("/tmp", 30*time.Second)
		} else {
			fmt.Println("    Executor created WITH sandbox")
		}
	} else {
		// Create executor without sandbox
		exec = shell.NewExecutor("/tmp", 30*time.Second)
		fmt.Println("    Executor created WITHOUT sandbox")
	}
	fmt.Println()

	// ── Step 4: Execute commands ───────────────────────
	fmt.Println("[4] Executing commands...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Test 1: Simple command
	fmt.Println("  → Test 1: Simple command")
	result, err := exec.Execute(ctx, "echo 'Hello from sandbox!'")
	if err != nil {
		log.Printf("    ERROR: %v", err)
	} else {
		fmt.Printf("    Output: %s", result.Stdout)
		fmt.Printf("    Duration: %v\n", result.Duration)
	}

	// Test 2: List files
	fmt.Println("  → Test 2: List files")
	result, err = exec.Execute(ctx, "ls -la /tmp | head -10")
	if err != nil {
		log.Printf("    ERROR: %v", err)
	} else {
		fmt.Printf("    Output:\n%s\n", result.Stdout)
	}

	// Test 3: Write to allowed path
	fmt.Println("  → Test 3: Write to allowed path")
	tmpFile := "/tmp/goclaude-sandbox-test.txt"
	result, err = exec.Execute(ctx, fmt.Sprintf("echo 'test' > %s && cat %s", tmpFile, tmpFile))
	if err != nil {
		log.Printf("    ERROR: %v", err)
	} else {
		fmt.Printf("    Output: %s", result.Stdout)
	}

	// Test 4: Read sensitive file (should fail if sandbox is active)
	fmt.Println("  → Test 4: Read sensitive file (~/.ssh/id_rsa)")
	fmt.Println("    (Expected to fail if sandbox is active)")
	result, err = exec.Execute(ctx, "cat ~/.ssh/id_rsa 2>&1 || echo 'ACCESS DENIED (good!)'")
	if err != nil {
		fmt.Printf("    ERROR (expected): %v\n", err)
	} else {
		if stringsContains(result.Stdout, "ACCESS DENIED") {
			fmt.Printf("    ✓ Sandbox correctly blocked access\n")
		} else {
			fmt.Printf("    Output: %s", result.Stdout)
			fmt.Printf("    ⚠ Sandbox may not be working\n")
		}
	}

	// Test 5: Network access (if network is disabled)
	if cfg.Network.DisableNetwork {
		fmt.Println("  → Test 5: Network access (should fail)")
		fmt.Println("    (Expected to fail because disable_network=true)")
		result, err = exec.Execute(ctx, "curl -s https://www.google.com > /dev/null 2>&1 && echo 'SUCCESS' || echo 'FAILED (good!)'")
		if err != nil {
			fmt.Printf("    ERROR (expected): %v\n", err)
		} else {
			fmt.Printf("    Output: %s", result.Stdout)
		}
	}

	fmt.Println()

	// ── Step 5: Cleanup ─────────────────────────────────
	fmt.Println("[5] Cleaning up...")

	if sb != nil {
		sb.Cleanup()
		fmt.Println("    Sandbox cleaned up")
	}

	fmt.Println()
	fmt.Println("=== Example completed ===")

	// ── Additional: Platform info ─────────────────────
	fmt.Println()
	fmt.Println("--- Platform Info ---")
	plat := sandbox.DetectPlatform()
	fmt.Printf("Platform: %s\n", plat)

	switch plat {
	case sandbox.PlatformLinux, sandbox.PlatformWSL2:
		fmt.Println("Backend: bubblewrap (bwrap)")
		fmt.Println("Dependencies:")
		errors, warnings := sandbox.CheckDependencies()
		for _, e := range errors {
			fmt.Printf("  ✗ ERROR: %s\n", e)
		}
		for _, w := range warnings {
			fmt.Printf("  ⚠ WARNING: %s\n", w)
		}
		if len(errors) == 0 {
			fmt.Println("  ✓ All dependencies satisfied")
		}

	case sandbox.PlatformMacOS:
		fmt.Println("Backend: sandbox-exec (Seatbelt)")
		fmt.Println("Dependencies: Built-in (no installation needed)")

	case sandbox.PlatformUnsupported:
		fmt.Println("Backend: Unsupported")
		fmt.Println("Sandbox is not available on this platform")
	}

	fmt.Println()
	fmt.Println("--- Configuration ---")
	fmt.Printf("Enabled: %v\n", cfg.Enabled)
	fmt.Printf("Allow read: %v\n", cfg.FSRead.Allow)
	fmt.Printf("Allow write: %v\n", cfg.FSWrite.Allow)
	fmt.Printf("Deny write: %v\n", cfg.FSWrite.Deny)
	fmt.Printf("Network disabled: %v\n", cfg.Network.DisableNetwork)
}

// stringsContains checks if s contains substr.
func stringsContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
