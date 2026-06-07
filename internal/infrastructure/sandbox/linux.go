// Package sandbox - Linux/WSL2 implementation using bubblewrap (bwrap).
//
// bubblewrap (https://github.com/containers/bubblewrap) is an unprivileged
// sandboxing tool using user namespaces. It is the same backend used by
// @anthropic-ai/sandbox-runtime on Linux.
//
// Usage: bwrap [options] -- command args...
//
// Key flags:
//   --ro-bind SRC DST   : read-only bind mount
//   --bind SRC DST       : read-write bind mount
//   --tmpfs PATH          : mount tmpfs at PATH
//   --proc PATH           : mount procfs at PATH
//   --dev PATH            : mount devtmpfs at PATH
//   --unshare-net         : isolate network
//   --unshare-pid         : unshare PID namespace
//   --unshare-uts         : unshare UTS namespace
//   --unshare-ipc         : unshare IPC namespace
//   --die-with-parent     : kill child if bwrap dies
//   --new-session         : create new session
package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// wrapWithBwrap builds a bwrap command that executes the given shell command
// inside a sandboxed environment.
//
// CRITICAL: bwrap uses "last-match-wins" semantics for bind mounts.
//   - Bind essential dirs FIRST (they get overlayed by --ro-bind / /)
//   - Then add --ro-bind / / (read-only root)
//   - Then add writable paths (they overlay the read-only root)
//
// Example working command:
//
//	bwrap \
//	  --proc /proc --dev /dev \
//	  --ro-bind /usr /usr \
//	  --ro-bind /lib64 /lib64 \
//	  --ro-bind / / \
//	  --tmpfs /tmp --tmpfs /run \
//	  --bind /tmp /tmp \
//	  -- bash -c "echo hello"
func (s *Sandbox) wrapWithBwrap(ctx context.Context, command string) *exec.Cmd {
	args := s.buildBwrapArgs()

	// The command to run inside sandbox:
	//   /usr/bin/bash -c "command"
	// IMPORTANT: must use absolute path, otherwise bwrap fails with:
	//   "exec_vp bash: No such file or directory"
	// CRITICAL: -- separates bwrap options from the command to execute
	args = append(args, "--", "/usr/bin/bash", "-c", command)

	cmd := exec.CommandContext(ctx, "bwrap", args...)
	cmd.Dir = s.workDir
	cmd.Env = s.buildEnv()

	// 仅在 GOCLAUDE_DEBUG_SANDBOX=1 时输出原始 bwrap 参数（调试用）
	if os.Getenv("GOCLAUDE_DEBUG_SANDBOX") == "1" {
		fmt.Fprintf(os.Stderr, "[bwrap] %s\n", strings.Join(append([]string{"bwrap"}, args...), " "))
	}

	return cmd
}

// buildBwrapArgs builds the bwrap argument list.
//
// ORDER MATTERS (last-match-wins):
//   1. Bind essential dirs (so they are accessible)
//   2. --ro-bind / / (read-only root, overlays #1)
//   3. Bind writable paths (overlay read-only, last-match-wins)
func (s *Sandbox) buildBwrapArgs() []string {
	cfg := s.config
	args := []string{}

	// ── Basic isolation ──────────────────────────────────────
	args = append(args,
		"--die-with-parent",
		"--unshare-pid",
		"--unshare-uts",
		"--unshare-ipc",
	)

	// ── Root filesystem: read-only ──────────────────────────────────
	// CRITICAL: bwrap uses "last-match-wins" semantics for overlapping
	// destinations. We mount / read-only FIRST, then overlay specific
	// paths (writable dirs, /proc, /dev) AFTER so they take precedence.
	args = append(args, "--ro-bind", "/", "/")

	// ── /proc and /dev ──────────────────────────────────────
	// MUST come AFTER --ro-bind / / so they override the read-only root.
	// If placed before, --ro-bind / / would overlay /dev with a read-only
	// bind of the host's /dev, making /dev/null inaccessible and causing
	// "Permission denied (os error 13)" when daemons try to daemonize.
	args = append(args,
		"--proc", "/proc",
		"--dev", "/dev",
	)

	// ── /dev/shm: required by Chrome/Chromium ─────────────────
	// Chrome uses /dev/shm for shared memory IPC between processes.
	// bwrap's --dev only creates a minimal devtmpfs without /dev/shm.
	// Without this, Chrome crashes with "Failed to map segment from
	// shared object" or runs extremely slowly.
	// Fallback: if /dev/shm doesn't exist on host, use --tmpfs.
	if dirExists("/dev/shm") {
		args = append(args, "--bind", "/dev/shm", "/dev/shm")
	} else {
		args = append(args, "--tmpfs", "/dev/shm")
	}

	// ── tmpfs for temporary directories ─────────────────────
	// /tmp: bind to host's real /tmp so that Unix-socket-based daemons
	// (e.g. agent-browser) that create sockets under /tmp remain
	// reachable from both inside and outside the sandbox.
	// /run: still isolated via tmpfs (keeps systemd/dbus sockets hidden);
	//       re-bound to host when AllowUnixSockets is true (see below).
	args = append(args,
		"--bind", "/tmp", "/tmp",
		"--tmpfs", "/run",
	)

	// ── Writable paths ──────────────────────────────────────
	// Always allow writing to workDir
	workDir, _ := filepath.Abs(s.workDir)
	args = append(args, "--bind", workDir, workDir)

	// Create ~/.goclaude and ~/.claude as tmpfs (writable inside sandbox only)
	home, _ := os.UserHomeDir()
	if home != "" {
		for _, dn := range []string{".goclaude", ".claude"} {
			cfgDir := filepath.Join(home, dn)
			args = append(args, "--tmpfs", cfgDir)
		}

		// ~/.cache is needed by browser tools (e.g. agent-browser stores
		// Playwright browser binaries under ~/.cache/ms-playwright).
		// Mount it as a bind from host so cached binaries are reused.
		cacheDir := filepath.Join(home, ".cache")
		if dirExists(cacheDir) {
			args = append(args, "--bind", cacheDir, cacheDir)
		} else {
			args = append(args, "--tmpfs", cacheDir)
		}

		// ~/.config is needed by browser tools and various CLI applications
		// that store configuration under ~/.config.
		configDir := filepath.Join(home, ".config")
		if dirExists(configDir) {
			args = append(args, "--bind", configDir, configDir)
		} else {
			args = append(args, "--tmpfs", configDir)
		}
	}

	// Add user-configured writable paths
	for _, path := range cfg.FSWrite.Allow {
		expanded := expandPath(path)
		if expanded == "" {
			continue
		}
		// If path exists on host, bind it (read/write)
		// If not, create tmpfs (writable inside sandbox only)
		if dirExists(expanded) {
			args = append(args, "--bind", expanded, expanded)
		} else {
			args = append(args, "--tmpfs", expanded)
		}
	}

	// ── Denied paths (read-only overlay) ────────────────────────
	// For denyWrite paths: re-bind as read-only (overlay on the rw bind)
	for _, path := range cfg.FSWrite.Deny {
		expanded := expandPath(path)
		if expanded != "" && dirExists(expanded) {
			// Remove writable bind, replace with read-only
			// bwrap uses last-match-wins semantics
			args = append(args, "--ro-bind", expanded, expanded)
		}
	}

	// ── Read-only allowed paths (explicit) ─────────────────────────
	for _, path := range cfg.FSRead.Allow {
		expanded := expandPath(path)
		if expanded != "" {
			args = append(args, "--ro-bind", expanded, expanded)
		}
	}

	// ── Denied read paths (hide by mounting empty tmpfs) ───────────
	for _, path := range cfg.FSRead.Deny {
		expanded := expandPath(path)
		if expanded != "" {
			// Hide by mounting an empty tmpfs on top
			args = append(args, "--tmpfs", expanded)
		}
	}

	// ── Unix sockets & XDG_RUNTIME_DIR ────────────────────────────
	// /run is mounted as tmpfs, which hides /run/user/<uid>/ used by
	// agent-browser and other tools for Unix sockets.
	// Bind-mount the actual XDG_RUNTIME_DIR back in so that daemon
	// sockets (agent-browser, dbus, etc.) remain accessible.
	xdgRuntime := os.Getenv("XDG_RUNTIME_DIR")
	if xdgRuntime != "" && dirExists(xdgRuntime) {
		args = append(args, "--bind", xdgRuntime, xdgRuntime)
	} else if cfg.Network.AllowUnixSockets {
		// Fallback: expose all of /run when no XDG_RUNTIME_DIR
		args = append(args, "--bind", "/run", "/run")
	}

	// ── D-Bus session socket ──────────────────────────────────────
	// Chrome/Chromium may attempt to connect to the D-Bus session bus
	// for features like accessibility and system notifications.
	// If DBUS_SESSION_BUS_ADDRESS points to a Unix socket, bind it.
	if dbusAddr := os.Getenv("DBUS_SESSION_BUS_ADDRESS"); dbusAddr != "" {
		// Format: unix:path=/run/user/1000/bus or unix:abstract=...
		if strings.HasPrefix(dbusAddr, "unix:path=") {
			dbusPath := strings.TrimPrefix(dbusAddr, "unix:path=")
			// Trim anything after comma (e.g. ,guid=...)
			if idx := strings.Index(dbusPath, ","); idx >= 0 {
				dbusPath = dbusPath[:idx]
			}
			if fileExists(dbusPath) {
				dir := filepath.Dir(dbusPath)
				if dirExists(dir) {
					args = append(args, "--bind", dir, dir)
				}
			}
		}
	}

	// ── /sys/devices/system/cpu: Chrome renderer info ─────────────
	// Chrome reads /sys/devices/system/cpu/... for CPU topology info.
	// Without it, Chrome logs errors or falls back to single-process.
	if dirExists("/sys") {
		args = append(args, "--ro-bind", "/sys", "/sys")
	}

	if cfg.Network.DisableNetwork {
		args = append(args, "--unshare-net")
	}

	return args
}

// buildEnv builds the environment variables for the sandboxed process.
func (s *Sandbox) buildEnv() []string {
	env := os.Environ()

	env = append(env, "SANDBOX_ACTIVE=1")
	env = append(env, "SANDBOX_BACKEND=bwrap")

	// agent-browser launches Chromium/Chrome internally. Chrome's own
	// sandbox (setuid helper / user-namespace) conflicts with bwrap's
	// mount namespace. Passing --no-sandbox disables Chrome's inner
	// layer; bwrap still provides the outer OS-level isolation.
	//
	// Additional flags:
	//   --disable-dev-shm-usage: Use /tmp instead of /dev/shm for shared
	//     memory. Even though we bind /dev/shm, its size may be limited
	//     inside the sandbox, causing Chrome tab crashes on heavy pages.
	//   --disable-gpu: GPU access is typically unavailable in sandboxed
	//     environments (no /dev/dri). Prevents Chrome from hanging on
	//     GPU initialization.
	//   --disable-setuid-sandbox: Redundant with --no-sandbox but makes
	//     intent explicit; prevents Chrome from trying setuid helper.
	//
	// De-duplicate: remove any inherited AGENT_BROWSER_ARGS first.
	var filtered []string
	for _, e := range env {
		if !strings.HasPrefix(e, "AGENT_BROWSER_ARGS=") {
			filtered = append(filtered, e)
		}
	}
	filtered = append(filtered, "AGENT_BROWSER_ARGS=--no-sandbox --disable-dev-shm-usage --disable-gpu --disable-setuid-sandbox")
	return filtered
}

// expandPath expands ~ to home directory and converts relative paths to absolute.
func expandPath(p string) string {
	// Handle bare ~ (home directory)
	if p == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
		return p
	}

	// Expand ~/ to home directory
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return p
		}
		return filepath.Join(home, p[2:])
	}

	// Convert relative paths to absolute
	if !filepath.IsAbs(p) {
		abs, err := filepath.Abs(p)
		if err == nil {
			return abs
		}
	}

	return p
}

// dirExists reports whether path exists and is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// fileExists reports whether path exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// WrapCommand builds the appropriate sandboxed command for the current platform.
func (s *Sandbox) WrapCommand(ctx context.Context, command string) *exec.Cmd {
	if !s.Enabled() {
		// No sandbox: run directly
		cmd := exec.CommandContext(ctx, "bash", "-c", command)
		cmd.Dir = s.workDir
		return cmd
	}

	switch s.platform {
	case PlatformLinux, PlatformWSL2:
		return s.wrapWithBwrap(ctx, command)
	case PlatformMacOS:
		return s.wrapWithSandboxExec(ctx, command)
	default:
		// Fallback: no sandbox
		cmd := exec.CommandContext(ctx, "bash", "-c", command)
		cmd.Dir = s.workDir
		return cmd
	}
}

// ShouldUseSandbox determines if a command should be sandboxed.
// Respects the excluded_commands config and dangerouslyDisableSandbox flag.
func (s *Sandbox) ShouldUseSandbox(command string, dangerouslyDisable bool) bool {
	if !s.Enabled() {
		return false
	}

	// Explicit override
	if dangerouslyDisable && s.config.AllowUnsandboxed {
		return false
	}

	// Check excluded commands
	for _, pattern := range s.config.ExcludedCommands {
		if matchesPattern(command, pattern) {
			return false
		}
	}

	return true
}

// matchesPattern does a simple prefix/wildcard match.
func matchesPattern(command, pattern string) bool {
	// Simple wildcard: "git*" matches "git status", "git log", etc.
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(command, prefix)
	}
	// Exact match
	return command == pattern
}
