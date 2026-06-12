// Package sandbox - platform detection and support matrix.
package sandbox

import (
	"bufio"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Platform represents the OS platform for sandbox backend selection.
type Platform string

const (
	PlatformLinux       Platform = "linux"
	PlatformMacOS       Platform = "macos"
	PlatformWSL2        Platform = "wsl"
	PlatformWindows     Platform = "windows"
	PlatformUnsupported Platform = "unsupported"
)

// DetectPlatform returns the current platform.
// WSL2 is detected by checking /proc/version for "microsoft" or "WSL".
func DetectPlatform() Platform {
	switch runtime.GOOS {
	case "linux":
		// Check for WSL2
		if IsWSL2() {
			return PlatformWSL2
		}
		return PlatformLinux

	case "darwin":
		return PlatformMacOS

	case "windows":
		return PlatformWindows

	default:
		return PlatformUnsupported
	}
}

// IsWSL2 checks if running inside WSL2.
// Reads /proc/version and looks for "microsoft" or "WSL".
func IsWSL2() bool {
	file, err := os.Open("/proc/version")
	if err != nil {
		return false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.ToLower(scanner.Text())
		if strings.Contains(line, "microsoft") || strings.Contains(line, "wsl") {
			return true
		}
	}
	return false
}

// IsSupportedPlatform reports whether the current platform supports sandboxing.
func IsSupportedPlatform() bool {
	p := DetectPlatform()
	switch p {
	case PlatformLinux, PlatformMacOS, PlatformWSL2:
		return true
	default:
		return false
	}
}

// CheckDependencies checks if required sandbox tools are available.
// Returns (errors, warnings).
func CheckDependencies() (errors []string, warnings []string) {
	p := DetectPlatform()

	switch p {
	case PlatformLinux, PlatformWSL2:
		return checkBwrapDeps()
	case PlatformMacOS:
		return checkMacOSDeps()
	default:
		return []string{"unsupported platform"}, nil
	}
}

// checkBwrapDeps checks Linux/WSL2 dependencies (bwrap + optionally socat).
func checkBwrapDeps() (errors []string, warnings []string) {
	if !commandExists("bwrap") {
		errors = append(errors, "bubblewrap (bwrap) is not installed: apt install bubblewrap")
	}
	// socat is needed for network proxy in sandbox
	if !commandExists("socat") {
		warnings = append(warnings, "socat not installed (needed for network proxy): apt install socat")
	}
	return errors, warnings
}

// checkMacOSDeps checks macOS dependencies (sandbox-exec is built-in).
func checkMacOSDeps() (errors []string, warnings []string) {
	// sandbox-exec is built into macOS, no install needed
	// Check it exists anyway
	if !commandExists("sandbox-exec") {
		errors = append(errors, "sandbox-exec is not available on this macOS version")
	}
	return errors, warnings
}

// commandExists reports whether cmd exists in PATH.
func commandExists(cmd string) bool {
	path, err := exec.LookPath(cmd)
	return err == nil && path != ""
}
