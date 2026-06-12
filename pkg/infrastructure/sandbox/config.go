// Package sandbox provides OS-level sandboxing for command execution.
// It uses bubblewrap (Linux/WSL2) and sandbox-exec (macOS) to isolate
// bash commands with filesystem and network restrictions.
package sandbox

import (
	"fmt"
	"strings"
	"time"
)

// Config holds sandbox configuration (maps to SandboxRuntimeConfig from TS version).
type Config struct {
	// Enabled turns sandboxing on/off
	Enabled bool `json:"enabled" yaml:"enabled"`

	// Filesystem restrictions
	FSRead  FsReadConfig  `json:"filesystem_read"  yaml:"filesystem_read"`
	FSWrite FsWriteConfig `json:"filesystem_write" yaml:"filesystem_write"`

	// Network restrictions
	Network NetworkConfig `json:"network" yaml:"network"`

	// Command execution
	AllowUnsandboxed bool     `json:"allow_unsandboxed" yaml:"allow_unsandboxed"`
	ExcludedCommands []string `json:"excluded_commands"  yaml:"excluded_commands"`

	// Advanced
	EnableWeakerNestedSandbox bool `json:"enable_weaker_nested_sandbox" yaml:"enable_weaker_nested_sandbox"`
	IgnoreViolations          bool `json:"ignore_violations"             yaml:"ignore_violations"`
}

type FsReadConfig struct {
	Allow []string `json:"allow" yaml:"allow"` // paths readable
	Deny  []string `json:"deny"  yaml:"deny"`  // paths not readable
}

type FsWriteConfig struct {
	Allow []string `json:"allow" yaml:"allow"` // paths writable
	Deny  []string `json:"deny"  yaml:"deny"`  // paths not writable
}

type NetworkConfig struct {
	AllowedDomains    []string `json:"allowed_domains"   yaml:"allowed_domains"`
	DeniedDomains     []string `json:"denied_domains"    yaml:"denied_domains"`
	AllowUnixSockets  bool     `json:"allow_unix_sockets" yaml:"allow_unix_sockets"`
	AllowLocalBinding bool     `json:"allow_local_binding" yaml:"allow_local_binding"`
	DisableNetwork    bool     `json:"disable_network"     yaml:"disable_network"`
}

// DefaultConfig returns a default sandbox config (sandbox disabled by default).
func DefaultConfig() *Config {
	return &Config{
		Enabled: false,
		FSRead: FsReadConfig{
			Allow: []string{".", "~/.goclaude", "~/.claude"},
			Deny:  []string{},
		},
		FSWrite: FsWriteConfig{
			Allow: []string{".", "~/.goclaude/tmp"},
			Deny:  []string{"~/.ssh", "~/.aws", "~/.config/gcloud"},
		},
		Network: NetworkConfig{
			AllowedDomains:    []string{},
			DeniedDomains:     []string{},
			AllowUnixSockets:  false,
			AllowLocalBinding: true,
			DisableNetwork:    false,
		},
		AllowUnsandboxed:          true,
		EnableWeakerNestedSandbox: false,
		IgnoreViolations:          false,
	}
}

// Sandbox wraps command execution with OS-level isolation.
type Sandbox struct {
	config      *Config
	platform    Platform
	workDir     string
	timeout     time.Duration
	initialized bool
}

// New creates a new Sandbox instance.
//
// When cfg.Enabled is true, this function verifies that the required
// sandbox backend binary (bwrap on Linux, sandbox-exec on macOS) is
// available in PATH.  If the binary is missing, it returns an error
// so that callers can fall back to direct execution.
func New(cfg *Config, workDir string, timeout time.Duration) (*Sandbox, error) {
	plat := DetectPlatform()

	if cfg == nil {
		cfg = DefaultConfig()
	}

	// 当沙箱启用时，检查必需的二进制文件是否可用。
	// 若不可用则返回 error，让调用方降级为直接执行。
	if cfg.Enabled {
		errs, _ := checkDepsForPlatform(plat)
		if len(errs) > 0 {
			return nil, fmt.Errorf("sandbox disabled: %s", strings.Join(errs, "; "))
		}
	}

	s := &Sandbox{
		config:   cfg,
		platform: plat,
		workDir:  workDir,
		timeout:  timeout,
	}

	return s, nil
}

// checkDepsForPlatform 检查指定平台的沙箱二进制是否可用，避免重复调用 DetectPlatform。
func checkDepsForPlatform(plat Platform) (errors []string, warnings []string) {
	switch plat {
	case PlatformLinux, PlatformWSL2:
		if !commandExists("bwrap") {
			errors = append(errors, "bwrap not found (install: apt install bubblewrap)")
		}
		if !commandExists("socat") {
			warnings = append(warnings, "socat not found (needed for network proxy)")
		}
	case PlatformMacOS:
		if !commandExists("sandbox-exec") {
			errors = append(errors, "sandbox-exec not found")
		}
	}
	return
}

// Enabled returns whether sandboxing is active.
func (s *Sandbox) Enabled() bool {
	if s == nil || s.config == nil {
		return false
	}
	return s.config.Enabled && s.platform != PlatformUnsupported
}

// Platform returns the detected platform.
func (s *Sandbox) Platform() Platform {
	return s.platform
}

// detectPlatform is a package-level function for testing.
func detectPlatform() Platform {
	return DetectPlatform()
}

// isWSL2 is a package-level function for testing.
func isWSL2() bool {
	return IsWSL2()
}

// Config returns the current sandbox config.
func (s *Sandbox) Config() *Config {
	return s.config
}

// SetConfig updates the sandbox config (hot-reload support).
func (s *Sandbox) SetConfig(cfg *Config) {
	s.config = cfg
}

// Cleanup removes temporary files created by the sandbox.
// Call this when the sandbox is no longer needed.
func (s *Sandbox) Cleanup() {
	// Cleanup logic (remove temp profiles, etc.)
	// For now, this is a no-op since we don't track temp files globally
	// In the future, we could track temp files and remove them here
}
