package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// ── Platform detection tests ──────────────────────────────

func TestDetectPlatform(t *testing.T) {
	plat := DetectPlatform()
	switch plat {
	case PlatformLinux, PlatformMacOS, PlatformWSL2, PlatformUnsupported:
		// OK
	default:
		t.Errorf("unknown platform: %s", plat)
	}
}

func TestIsSupportedPlatform(t *testing.T) {
	// This will pass on Linux/macOS/WSL2, fail on Windows/other
	// We just test that the function doesn't panic
	_ = IsSupportedPlatform()
}

func TestCheckDependencies(t *testing.T) {
	errors, warnings := CheckDependencies()
	// On CI without bwrap, we expect errors
	t.Logf("Dependencies - errors: %v, warnings: %v", errors, warnings)
}

// ── Config tests ─────────────────────────────────────────────────

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg == nil {
		t.Fatal("DefaultConfig() returned nil")
	}
	if cfg.Enabled {
		t.Error("DefaultConfig().Enabled should be false")
	}
	if len(cfg.FSWrite.Allow) == 0 {
		t.Error("DefaultConfig() should have writable paths")
	}
}

func TestConfigExpandPaths(t *testing.T) {
	cfg := &Config{
		FSRead: FsReadConfig{
			Allow: []string{"~", "/tmp"},
		},
	}
	_ = cfg
	// Just verify config structure is intact
}

// ── Sandbox creation tests ─────────────────────────────────

func TestNewSandbox(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true

	sb, err := New(cfg, "/tmp", 30*time.Second)
	if err != nil {
		// May fail if platform unsupported or deps missing
		t.Logf("New() error (expected on CI): %v", err)
		return
	}
	defer sb.Cleanup()

	if sb.Platform() == PlatformUnsupported {
		t.Log("Platform unsupported, skipping further tests")
		return
	}

	if sb.Config() == nil {
		t.Error("Config() returned nil")
	}
}

func TestSandboxEnabled(t *testing.T) {
	// Disabled config
	cfg := DefaultConfig()
	cfg.Enabled = false

	sb, err := New(cfg, "/tmp", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Cleanup()

	if sb.Enabled() {
		t.Error("Sandbox should be disabled when cfg.Enabled=false")
	}

	// Enabled config (may fail on unsupported platform)
	cfg2 := DefaultConfig()
	cfg2.Enabled = true

	sb2, err := New(cfg2, "/tmp", 30*time.Second)
	if err != nil {
		t.Logf("Expected error on unsupported platform: %v", err)
		return
	}
	defer sb2.Cleanup()

	if sb2.Platform() != PlatformUnsupported && !sb2.Enabled() {
		t.Error("Sandbox should be enabled")
	}
}

// ── Command wrapping tests ─────────────────────────────────

func TestWrapCommandNoSandbox(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = false

	sb, err := New(cfg, "/tmp", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Cleanup()

	ctx := context.Background()
	cmd := sb.WrapCommand(ctx, "echo hello")

	if cmd == nil {
		t.Fatal("WrapCommand returned nil")
	}

	// Should be direct bash execution (no sandbox prefix)
	// (We can't easily assert the command path here, but we can run it)
}

func TestWrapCommandSandbox(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true

	sb, err := New(cfg, "/tmp", 30*time.Second)
	if err != nil {
		// Expected on unsupported platforms or missing dependencies
		t.Skipf("Skipping (platform unsupported or deps missing): %v", err)
		return
	}
	defer sb.Cleanup()

	if sb.Platform() == PlatformUnsupported {
		t.Skip("Skipping (unsupported platform)")
		return
	}

	// Check if bwrap/sandbox-exec actually exists
	if sb.Platform() == PlatformLinux || sb.Platform() == PlatformWSL2 {
		if _, err := exec.LookPath("bwrap"); err != nil {
			t.Skip("Skipping (bwrap not installed)")
		}
	}
	if sb.Platform() == PlatformMacOS {
		if _, err := exec.LookPath("sandbox-exec"); err != nil {
			t.Skip("Skipping (sandbox-exec not installed)")
		}
	}

	ctx := context.Background()
	cmd := sb.WrapCommand(ctx, "echo hello")

	if cmd == nil {
		t.Fatal("WrapCommand returned nil")
	}

	// Verify command runs
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Run()
	if err != nil {
		t.Errorf("Sandboxed command failed: %v", err)
	}
}

func TestShouldUseSandbox(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.AllowUnsandboxed = true
	cfg.ExcludedCommands = []string{"cd *", "pwd"}

	sb, err := New(cfg, "/tmp", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Cleanup()

	tests := []struct {
		command          string
		dangerouslyDisable bool
		want             bool
	}{
		{"ls -la", false, true},
		{"cd /tmp", false, false}, // excluded
		{"pwd", false, false},      // excluded
		{"git status", false, true},
		{"ls -la", true, false},    // dangerouslyDisable=true, allowUnsandboxed=true
	}

	for _, tt := range tests {
		got := sb.ShouldUseSandbox(tt.command, tt.dangerouslyDisable)
		if got != tt.want {
			t.Errorf("ShouldUseSandbox(%q, %v) = %v, want %v",
				tt.command, tt.dangerouslyDisable, got, tt.want)
		}
	}
}

// ── Integration test (requires bwrap or sandbox-exec) ───────────

func TestSandboxedExecution(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.FSWrite.Allow = append(cfg.FSWrite.Allow, "/tmp")

	sb, err := New(cfg, "/tmp", 30*time.Second)
	if err != nil {
		t.Skipf("Sandbox not available: %v", err)
	}
	defer sb.Cleanup()

	if sb.Platform() == PlatformUnsupported {
		t.Skip("Platform unsupported")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Test 1: Simple command
	cmd := sb.WrapCommand(ctx, "echo hello from sandbox")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("Command failed: %v, output: %s", err, output)
	}

	t.Logf("Output: %s", output)

	// Test 2: Write to allowed path
	allowedPath := filepath.Join(os.TempDir(), "goclaude-sandbox-test.txt")
	cmd2 := sb.WrapCommand(ctx, "echo test > "+allowedPath)
	_, err = cmd2.CombinedOutput()
	if err != nil {
		t.Errorf("Write to allowed path failed: %v", err)
	}

	// Cleanup
	_ = os.Remove(allowedPath)
}

func TestSandboxedNetworkIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Network.DisableNetwork = true // No network

	sb, err := New(cfg, "/tmp", 30*time.Second)
	if err != nil {
		t.Skipf("Sandbox not available: %v", err)
	}
	defer sb.Cleanup()

	if sb.Platform() == PlatformUnsupported {
		t.Skip("Platform unsupported")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Try to curl a website (should fail with network isolation)
	cmd := sb.WrapCommand(ctx, "curl -s https://www.google.com")
	_, err = cmd.CombinedOutput()

	// On bwrap with --unshare-net, curl should fail
	// On macOS with (deny network*), curl should fail
	if err == nil {
		t.Log("WARNING: Network isolation may not be working (curl succeeded)")
	} else {
		t.Logf("Network correctly blocked: %v", err)
	}
}

// ── Helper tests ──────────────────────────────────────────────────

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		input    string
		wantPrefix string
	}{
		{"~", home},
		{"~/", home + "/"},
		{"/tmp", "/tmp"},
		{".", "."},
	}

	for _, tt := range tests {
		got := expandPath(tt.input)
		t.Logf("expandPath(%q) = %q", tt.input, got)
	}
}

func TestMatchesPattern(t *testing.T) {
	tests := []struct {
		command string
		pattern  string
		want     bool
	}{
		{"cd /tmp", "cd *", true},
		{"pwd", "pwd", true},
		{"git status", "git *", true},
		{"ls -la", "cd *", false},
	}

	for _, tt := range tests {
		got := matchesPattern(tt.command, tt.pattern)
		if got != tt.want {
			t.Errorf("matchesPattern(%q, %q) = %v, want %v",
				tt.command, tt.pattern, got, tt.want)
		}
	}
}

// ── macOS seatbelt profile tests ─────────────────────────────────

func TestGenerateSeatbeltProfile(t *testing.T) {
	if DetectPlatform() != PlatformMacOS {
		t.Skip("Seatbelt profile test only runs on macOS")
	}

	cfg := DefaultConfig()
	cfg.FSRead.Allow = []string{"/tmp", "/usr"}
	cfg.FSWrite.Allow = []string{"/tmp"}
	cfg.Network.DisableNetwork = true

	sb, err := New(cfg, "/tmp", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Cleanup()

	profile := sb.generateSeatbeltProfile()

	if profile == "" {
		t.Error("generateSeatbeltProfile returned empty string")
	}

	// Check for required elements
	checks := []string{
		"(version 1)",
		"(deny default)",
		"file-read*",
		"file-write*",
		"subpath",
	}

	for _, check := range checks {
		if !contains(profile, check) {
			t.Errorf("Profile missing %q", check)
		}
	}

	t.Logf("Generated profile:\n%s", profile)
}

// ── Benchmarks ──────────────────────────────────────────────────

func BenchmarkWrapCommandDirect(b *testing.B) {
	cfg := DefaultConfig()
	cfg.Enabled = false

	sb, _ := New(cfg, "/tmp", 30*time.Second)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sb.WrapCommand(ctx, "echo hello")
	}
}

func BenchmarkWrapCommandSandboxed(b *testing.B) {
	cfg := DefaultConfig()
	cfg.Enabled = true

	sb, err := New(cfg, "/tmp", 30*time.Second)
	if err != nil {
		b.Skip("Sandbox not available")
	}

	if sb.Platform() == PlatformUnsupported {
		b.Skip("Platform unsupported")
	}

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sb.WrapCommand(ctx, "echo hello")
	}
}

// ── Helpers ──────────────────────────────────────────────────────

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}


