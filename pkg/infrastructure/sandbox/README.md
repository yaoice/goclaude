# GoClaude Sandbox Implementation

OS-level sandboxing for `goclaude` using native platform sandbox technologies.

## Architecture

```
goclaude (Go)
├── Linux/WSL2:  bwrap (bubblewrap)
├── macOS:        sandbox-exec (Seatbelt)
└── Windows:      Not supported (unsupported platform)
```

## Backend Comparison

| Feature              | Linux (bwrap)        | macOS (sandbox-exec) |
|---------------------|-----------------------|----------------------|
| Isolation Type      | User namespaces       | Mandatory Access Control (MAC) |
| Config Format       | CLI arguments         | Scheme-like `.sb` profile |
| Network Isolation   | `--unshare-net`      | `(deny network*)` |
| Filesystem          | bind mounts           | path-based allow/deny |
| Dependencies        | `bubblewrap`, `socat`| Built-in (no install) |
| Performance         | Fast                  | Fast |

## Usage

### 1. Enable in config (`configs/default.yaml`)

```yaml
sandbox:
  enabled: true
  
  filesystem:
    allow_read:
      - "."
      - "~/.claude"
    deny_read: []
    allow_write:
      - "."
      - "~/.claude/tmp"
    deny_write:
      - "~/.ssh"
      - "~/.aws"
  
  network:
    disable_network: false  # set true for full network isolation
    allowed_domains: []
    denied_domains: []
    allow_unix_sockets: false
    allow_local_binding: true
  
  allow_unsandboxed_commands: true
  excluded_commands:
    - "cd *"
    - "pwd"
```

### 2. CLI Flags

```bash
# Enable sandbox
goclaude --sandbox

# Disable sandbox (override config)
goclaude --no-sandbox

# Skip permissions (implies --no-sandbox)
goclaude --dangerously-skip-permissions
```

### 3. Programmatic Usage

```go
package main

import (
    "github.com/anthropics/goclaude/pkg/infrastructure/sandbox"
    "github.com/anthropics/goclaude/pkg/infrastructure/shell"
)

func main() {
    // 1. Load config
    cfg := sandbox.DefaultConfig()
    cfg.Enabled = true
    
    // 2. Create sandbox
    sb, err := sandbox.New(cfg, "/workspace", 120*time.Second)
    if err != nil {
        // Sandbox not available (unsupported platform or missing deps)
        log.Printf("Sandbox disabled: %v", err)
    }
    
    // 3. Create executor with sandbox
    exec, err := shell.NewExecutorWithSandbox("/workspace", 120*time.Second, cfg)
    if err != nil {
        // Fallback: create without sandbox
        exec = shell.NewExecutor("/workspace", 120*time.Second)
    }
    
    // 4. Execute commands (automatically sandboxed if enabled)
    ctx := context.Background()
    result, err := exec.Execute(ctx, "ls -la")
    if err != nil {
        log.Fatal(err)
    }
    
    fmt.Println(result.Stdout)
}
```

## How It Works

### Linux/WSL2: bubblewrap (bwrap)

bwrap creates an unprivileged sandbox using Linux user namespaces:

```bash
bwrap \
  --die-with-parent \
  --unshare-pid --unshare-uts --unshare-ipc \
  --proc /proc --dev /dev \
  --ro-bind / / \
  --tmpfs /tmp --tmpfs /run \
  --bind /workspace /workspace \
  --unshare-net \  # network isolation
  bash -c "command"
```

**Key features:**
- Filesystem: `--ro-bind` (read-only), `--bind` (read-write)
- Network: `--unshare-net` (full isolation) or socat proxy (filtered)
- Processes: isolated PID namespace

### macOS: sandbox-exec (Seatbelt)

macOS has a built-in sandbox mechanism:

```bash
sandbox-exec -f profile.sb bash -c "command"
```

Profile (`profile.sb`):
```scheme
(version 1)
(deny default)
(allow file-read* (subpath "/workspace"))
(allow file-write* (subpath "/workspace"))
(deny network*)
```

**Key features:**
- Scheme-like profile language
- Built-in to macOS (no installation needed)
- Path-based and operation-based rules

## Security Model

### Filesystem Isolation

| Config               | Linux (bwrap)                      | macOS (sandbox-exec) |
|----------------------|-------------------------------------|------------------------|
| `allow_read`         | `--ro-bind src dst`                | `(allow file-read* (subpath "..."))` |
| `allow_write`        | `--bind src dst`                    | `(allow file-write* (subpath "..."))` |
| `deny_write`         | `--ro-bind` (override rw bind)     | `(deny file-write* (subpath "..."))` |
| Sensitive paths      | Automatically denied                | Automatically denied |

Automatically denied paths (same as `@anthropic-ai/sandbox-runtime`):
- `~/.ssh/*`
- `~/.aws/*`
- `~/.config/gcloud/*`
- `~/.bashrc`, `~/.zshrc`
- Settings files (`.claude/settings.json`)

### Network Isolation

| Config                  | Behavior                                      |
|------------------------|-----------------------------------------------|
| `disable_network: true` | Full isolation (no network at all)           |
| `allowed_domains: [...]` | Only allow outbound to these domains      |
| `denied_domains: [...]`  | Block these domains                    |
| `allow_unix_sockets: true` | Allow Unix domain sockets            |

On Linux, network filtering uses:
1. `--unshare-net` (full isolation)
2. socat proxy (for allowed domains)

On macOS, network filtering uses:
```scheme
(deny network*)
(allow network-outbound (remote "example.com"))
```

## Dependencies

### Linux/WSL2

```bash
# Ubuntu/Debian
sudo apt install bubblewrap socat ripgrep

# Fedora
sudo dnf install bubblewrap socat ripgrep

# Arch
sudo pacman -S bubblewrap socat ripgrep
```

Check dependencies:
```bash
goclaude /doctor
# or
goclaude /sandbox
```

### macOS

No dependencies (sandbox-exec is built-in).

Check:
```bash
which sandbox-exec  # should exist
```

## Comparison with `@anthropic-ai/sandbox-runtime`

| Feature                    | `sandbox-runtime` (TS)  | `goclaude` (Go) |
|---------------------------|---------------------------|-------------------|
| Language                  | TypeScript/Node.js        | Go                |
| Linux backend             | bwrap                    | bwrap             |
| macOS backend             | sandbox-exec              | sandbox-exec       |
| Network proxy             | socat                     | (TODO)            |
| Seccomplex filter         | Yes                       | No (bwrap handles it) |
| Config format             | JSON                      | YAML              |
| Hot reload               | Yes                       | Yes              |
| Performance              | Slower (Node.js overhead) | Faster (native Go) |

## Troubleshooting

### "bwrap: command not found"

Install bubblewrap:
```bash
sudo apt install bubblewrap
```

### "sandbox-exec: command not found" (macOS)

Your macOS version may not support sandbox-exec (deprecated by Apple).

Workaround: Use Linux via Docker or WSL2.

### "Operation not permitted" inside sandbox

The command is trying to access a denied path.

Fix: Add the path to `sandbox.filesystem.allow_write` in config.

### Network still works with `disable_network: true`

Check:
1. Sandbox is actually enabled (`/sandbox` command)
2. You're on a supported platform
3. bwrap/sandbox-exec is working

Debug:
```bash
# Linux: manually test bwrap
bwrap --unshare-net -- bash -c "curl google.com"
# Should fail with network error

# macOS: manually test sandbox-exec
echo '(deny network*)' > /tmp/test.sb
sandbox-exec -f /tmp/test.sb bash -c "curl google.com"
# Should fail with network error
```

## TODO

- [ ] Add seccomp filter support (Linux)
- [ ] Add socat network proxy for filtered network (Linux)
- [ ] Add Windows support (via AppContainer or WDAG)
- [ ] Add integration tests for macOS
- [ ] Add performance benchmarks
- [ ] Add `/sandbox` REPL command (like Claude Code)
- [ ] Add auto-allow bash if sandbox enabled (like Claude Code)

## References

- [bubblewrap (bwrap)](https://github.com/containers/bubblewrap)
- [macOS Seatbelt Reference](https://reverse.put.as/wp-content/uploads/2011/09/Apple-Sandbox-Guide.pdf)
- [@anthropic-ai/sandbox-runtime](https://github.com/anthropic-experimental/sandbox-runtime)
- [Claude Code Sandboxing Docs](https://code.claude.com/docs/en/sandboxing)
