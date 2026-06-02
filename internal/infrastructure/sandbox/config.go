// Package sandbox provides OS-level sandboxing for command execution.
// It uses bubblewrap (Linux/WSL2) and sandbox-exec (macOS) to isolate
// bash commands with filesystem and network restrictions.
package sandbox

import "time"

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
	ExcludedCommands  []string `json:"excluded_commands"  yaml:"excluded_commands"`

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
	AllowedDomains   []string `json:"allowed_domains"   yaml:"allowed_domains"`
	DeniedDomains    []string `json:"denied_domains"    yaml:"denied_domains"`
	AllowUnixSockets bool     `json:"allow_unix_sockets" yaml:"allow_unix_sockets"`
	AllowLocalBinding bool    `json:"allow_local_binding" yaml:"allow_local_binding"`
	DisableNetwork   bool     `json:"disable_network"     yaml:"disable_network"`
}

// DefaultConfig returns a default sandbox config (sandbox disabled by default).
func DefaultConfig() *Config {
	return &Config{
		Enabled: false,
		FSRead: FsReadConfig{
			Allow: []string{".", "~/.claude"},
			Deny:  []string{},
		},
		FSWrite: FsWriteConfig{
			Allow: []string{".", "~/.claude/tmp"},
			Deny:  []string{"~/.ssh", "~/.aws", "~/.config/gcloud"},
		},
		Network: NetworkConfig{
			AllowedDomains:   []string{},
			DeniedDomains:    []string{},
			AllowUnixSockets: false,
			AllowLocalBinding: true,
			DisableNetwork:   false,
		},
		AllowUnsandboxed:        true,
		EnableWeakerNestedSandbox: false,
		IgnoreViolations:         false,
	}
}

// Sandbox wraps command execution with OS-level isolation.
type Sandbox struct {
	config     *Config
	platform   Platform
	workDir    string
	timeout    time.Duration
	initialized bool
}

// New creates a new Sandbox instance.
func New(cfg *Config, workDir string, timeout time.Duration) (*Sandbox, error) {
	plat := DetectPlatform()

	if cfg == nil {
		cfg = DefaultConfig()
	}

	s := &Sandbox{
		config:   cfg,
		platform: plat,
		workDir:  workDir,
		timeout:  timeout,
	}

	return s, nil
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
