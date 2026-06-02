// Package sandbox - macOS implementation using sandbox-exec (Seatbelt).
//
// macOS has a built-in sandbox mechanism called "Seatbelt" (sandbox-exec).
// It uses a Scheme-like profile language to define security policies.
//
// Unlike Linux bwrap (which uses user namespaces), macOS sandbox-exec:
//   - Is a macOS-native feature (no install needed)
//   - Uses mandatory access control (MAC)
//   - Profile is a .sb file (Scheme-like syntax)
//   - Supports file-read*, file-write*, network*, process*, etc.
//
// Usage: sandbox-exec -f profile.sb bash -c "command"
//
// Limitations:
//   - Deprecated by Apple (but still works on modern macOS)
//   - Limited to single profile (no stacking)
//   - Some operations may be silently denied
package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// wrapWithSandboxExec builds a command wrapped with macOS sandbox-exec.
//
// It generates a .sb (Seatbelt) profile based on the sandbox config,
// writes it to a temp file, then runs:
//
//	sandbox-exec -f /tmp/sandbox-XXX.sb bash -c "command"
//
// The temp file is cleaned up after command execution.
func (s *Sandbox) wrapWithSandboxExec(ctx context.Context, command string) *exec.Cmd {
	// Generate seatbelt profile content
	profile := s.generateSeatbeltProfile()

	// Write to temp file
	tmpFile, err := os.CreateTemp("", "goclaude-sandbox-*.sb")
	if err != nil {
		// Fallback: run without sandbox
		cmd := exec.CommandContext(ctx, "bash", "-c", command)
		cmd.Dir = s.workDir
		return cmd
	}

	_, _ = tmpFile.WriteString(profile)
	_ = tmpFile.Close()

	// Build command: sandbox-exec -f <profile> bash -c "command"
	cmd := exec.CommandContext(ctx, "sandbox-exec",
		"-f", tmpFile.Name(),
		"bash", "-c", command,
	)
	cmd.Dir = s.workDir
	cmd.Env = s.buildMacOSEnv(tmpFile.Name())

	// Cleanup temp file after execution
	// (Go's os.Remove doesn't work while process is running on some systems)
	// We'll let the OS clean up /tmp on reboot, or add cleanup in caller

	return cmd
}

// generateSeatbeltProfile generates a macOS Seatbelt (.sb) profile.
//
// Seatbelt profile syntax (Scheme-like):
//
//	(version 1)
//	(deny default)              ; deny everything by default
//	(allow file-read* (subpath "/path"))
//	(allow file-write* (subpath "/path"))
//	(deny network*)
//	(allow network-outbound (remote ...))
//
// Reference: https://reverse.put.as/wp-content/uploads/2011/09/Apple-Sandbox-Guide.pdf
func (s *Sandbox) generateSeatbeltProfile() string {
	cfg := s.config

	var sb strings.Builder

	// Header
	sb.WriteString("(version 1)\n")
	sb.WriteString("\n")

	// Default deny
	sb.WriteString(";; Default: deny all\n")
	sb.WriteString("(deny default)\n")
	sb.WriteString("\n")

	// ── Allowed reads ──────────────────────────────────────────
	if len(cfg.FSRead.Allow) > 0 {
		sb.WriteString(";; Allowed read paths\n")
		for _, path := range cfg.FSRead.Allow {
			expanded := expandPath(path)
			sb.WriteString(fmt.Sprintf("(allow file-read*\n  (subpath \"%s\"))\n", escapedString(expanded)))
		}
		sb.WriteString("\n")
	}

	// ── Allowed writes ───────────────────────────────────────────
	if len(cfg.FSWrite.Allow) > 0 {
		sb.WriteString(";; Allowed write paths\n")
		for _, path := range cfg.FSWrite.Allow {
			expanded := expandPath(path)
			sb.WriteString(fmt.Sprintf("(allow file-write*\n  (subpath \"%s\"))\n", escapedString(expanded)))
		}
		sb.WriteString("\n")
	}

	// ── Denied reads ─────────────────────────────────────────────
	if len(cfg.FSRead.Deny) > 0 {
		sb.WriteString(";; Denied read paths (re-deny after allow)\n")
		for _, path := range cfg.FSRead.Deny {
			expanded := expandPath(path)
			sb.WriteString(fmt.Sprintf("(deny file-read*\n  (subpath \"%s\"))\n", escapedString(expanded)))
		}
		sb.WriteString("\n")
	}

	// ── Denied writes ────────────────────────────────────────────
	if len(cfg.FSWrite.Deny) > 0 {
		sb.WriteString(";; Denied write paths\n")
		for _, path := range cfg.FSWrite.Deny {
			expanded := expandPath(path)
			sb.WriteString(fmt.Sprintf("(deny file-write*\n  (subpath \"%s\"))\n", escapedString(expanded)))
		}
		sb.WriteString("\n")
	}

	// ── Network ──────────────────────────────────────────────────
	if cfg.Network.DisableNetwork {
		sb.WriteString(";; Network isolation: deny all network\n")
		sb.WriteString("(deny network*)\n")
		sb.WriteString("\n")
	} else if len(cfg.Network.AllowedDomains) > 0 {
		// Partial network: allow outbound to specific domains
		sb.WriteString(";; Network: allow outbound to specific domains\n")
		sb.WriteString("(allow network-outbound\n")
		for _, domain := range cfg.Network.AllowedDomains {
			sb.WriteString(fmt.Sprintf("  (remote \"%s\")\n", escapedString(domain)))
		}
		sb.WriteString(")\n")
		sb.WriteString(";; Unix sockets for local IPC (agent-browser daemon)\n")
		sb.WriteString("(allow network-outbound (local))\n")
		sb.WriteString("(allow network-inbound (local))\n")
		sb.WriteString("\n")
	} else {
		// No network restriction: allow all network access
		// Required for agent-browser to connect to web pages and for
		// Chrome's internal IPC via Unix sockets / localhost.
		sb.WriteString(";; Network: unrestricted (no domain filter active)\n")
		sb.WriteString("(allow network*)\n")
		sb.WriteString("\n")
	}

	// ── Process execution ────────────────────────────────────────
	// Allow executing bash and common tools
	sb.WriteString(";; Allow process execution\n")
	sb.WriteString("(allow process-exec\n")
	sb.WriteString("  (subpath \"/bin\")\n")
	sb.WriteString("  (subpath \"/usr/bin\")\n")
	sb.WriteString("  (subpath \"/usr/local/bin\")\n")
	sb.WriteString("  (subpath \"/opt/homebrew/bin\"))\n")
	sb.WriteString("\n")

	// ── Process fork/exec: required by Chrome multi-process ──────
	// Chrome spawns renderer, GPU, utility, and crashpad processes.
	sb.WriteString(";; Chrome multi-process architecture\n")
	sb.WriteString("(allow process-fork)\n")
	sb.WriteString("(allow signal (target self))\n")
	sb.WriteString("\n")

	// ── Sysctl: Chrome reads system info ────────────────────────
	sb.WriteString(";; Chrome reads sysctl for CPU/memory info\n")
	sb.WriteString("(allow sysctl-read)\n")
	sb.WriteString("\n")

	// ── Mach services: Chrome IPC ───────────────────────────────
	// Chrome uses Mach ports extensively for IPC between processes.
	sb.WriteString(";; Mach IPC required by Chrome\n")
	sb.WriteString("(allow mach-lookup)\n")
	sb.WriteString("(allow mach-register)\n")
	sb.WriteString("\n")

	// ── IOKit: GPU & device access ──────────────────────────────
	sb.WriteString(";; IOKit for GPU/display (Chrome compositor)\n")
	sb.WriteString("(allow iokit-open)\n")
	sb.WriteString("\n")

	// ── /tmp: always allow read/write ────────────────────────────
	// agent-browser daemon socket is redirected to /tmp via
	// AGENT_BROWSER_SOCKET_DIR. Chrome also writes temp files here.
	sb.WriteString(";; /tmp: always writable (daemon sockets, Chrome temp files)\n")
	sb.WriteString("(allow file-read* (subpath \"/tmp\"))\n")
	sb.WriteString("(allow file-write* (subpath \"/tmp\"))\n")
	sb.WriteString("\n")

	// ── System frameworks required by Chrome ─────────────────────
	// Chrome needs to read system libraries and frameworks even when
	// --no-sandbox is passed; without these the process fails to start.
	sb.WriteString(";; System paths required by Chrome/Chromium\n")
	sb.WriteString("(allow file-read* (subpath \"/System\"))\n")
	sb.WriteString("(allow file-read* (subpath \"/Library\"))\n")
	sb.WriteString("(allow file-read* (subpath \"/usr/lib\"))\n")
	sb.WriteString("(allow file-read* (subpath \"/dev\"))\n")
	sb.WriteString("\n")

	// ── ~/.cache: Playwright browser binaries ────────────────────
	// agent-browser uses Playwright which stores browser binaries in
	// ~/.cache/ms-playwright. Must be readable and executable.
	home, _ := os.UserHomeDir()
	if home != "" {
		cacheDir := fmt.Sprintf("%s/.cache", home)
		sb.WriteString(";; ~/.cache: Playwright browser binaries\n")
		sb.WriteString(fmt.Sprintf("(allow file-read* (subpath \"%s\"))\n", escapedString(cacheDir)))
		sb.WriteString(fmt.Sprintf("(allow process-exec (subpath \"%s\"))\n", escapedString(cacheDir)))
		sb.WriteString("\n")

		// ~/.config for browser data directories
		configDir := fmt.Sprintf("%s/.config", home)
		sb.WriteString(";; ~/.config: browser user data\n")
		sb.WriteString(fmt.Sprintf("(allow file-read* (subpath \"%s\"))\n", escapedString(configDir)))
		sb.WriteString(fmt.Sprintf("(allow file-write* (subpath \"%s\"))\n", escapedString(configDir)))
		sb.WriteString("\n")
	}

	return sb.String()
}

// buildMacOSEnv builds environment variables for macOS sandbox.
func (s *Sandbox) buildMacOSEnv(profilePath string) []string {
	env := os.Environ()
	env = append(env, "SANDBOX_ACTIVE=1")
	env = append(env, "SANDBOX_BACKEND=sandbox-exec")
	env = append(env, fmt.Sprintf("SANDBOX_PROFILE=%s", profilePath))

	// agent-browser launches Chrome internally. On macOS, Chrome's inner
	// sandbox (Seatbelt) can conflict with the outer sandbox-exec profile.
	// Pass --no-sandbox to disable Chrome's inner layer.
	// Additional flags:
	//   --disable-dev-shm-usage: Avoids /dev/shm issues in sandbox
	//   --disable-gpu: GPU may be restricted by the Seatbelt profile
	//   --disable-setuid-sandbox: Explicit no-setuid for nested sandbox
	env = append(env, "AGENT_BROWSER_ARGS=--no-sandbox --disable-dev-shm-usage --disable-gpu --disable-setuid-sandbox")

	// agent-browser uses $TMPDIR (macOS default) or XDG_RUNTIME_DIR for
	// its daemon socket. Redirect to /tmp which is explicitly allowed in
	// the generated Seatbelt profile.
	env = append(env, "AGENT_BROWSER_SOCKET_DIR=/tmp")

	return env
}

// escapedString escapes double quotes in Scheme strings.
func escapedString(s string) string {
	return strings.ReplaceAll(s, "\"", "\\\"")
}

// CleanupProfile removes the temporary .sb profile file.
// Call this after command execution completes.
func CleanupProfile(profilePath string) error {
	return os.Remove(profilePath)
}


