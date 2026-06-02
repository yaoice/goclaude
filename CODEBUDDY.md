# CODEBUDDY.md This file provides guidance to CodeBuddy when working with code in this repository.

## Commands

### Build
```bash
make build        # Compile to ./bin/goclaude with git version injection
make all          # Full pipeline: fmt → vet → lint → test → build
```

### Test
```bash
make test                    # Run all tests with -race and -cover
make test-verbose            # Same with verbose output
go test ./internal/domain/query/...  # Run tests for a specific package
go test -run TestName ./path/to/pkg  # Run a single test by name
make e2e                     # End-to-end tests (requires expect + python3)
```

### Lint & Format
```bash
make fmt           # gofmt -s -w .
make vet           # go vet ./...
make lint          # golangci-lint run ./...
```

### Run
```bash
make run           # go run ./cmd/goclaude/
./bin/goclaude     # After build
```

### Dependencies
```bash
make deps          # go mod download && go mod tidy
```

## Architecture

GoClaude is a Go reimplementation of the upstream TypeScript claude-code terminal AI assistant. It follows strict **DDD (Domain-Driven Design) four-layer architecture** with dependency inversion.

### Layer Dependency Flow

```
interfaces → application → domain ← infrastructure
```

- **Domain** defines pure interfaces and models with zero external dependencies
- **Infrastructure** implements domain interfaces (dependency inversion)
- **Application** orchestrates domain logic into use cases
- **Interfaces** adapts external input (CLI, Shell REPL, TUI) to application services

### Domain Layer (`internal/domain/`)

The core business logic, entirely free of external dependencies:

- **`query/`** — Query engine main loop: manages conversation turns, token budgets, streaming events, and message models. This is the heart of the AI interaction cycle.
- **`tool/`** — Tool interface (`Tool`), `Registry` (tool discovery), and `Executor` (concurrent reads, serial writes). All tools implement the `Tool` interface from this package.
- **`command/`** — Slash command framework for REPL commands like `/help`, `/clear`, `/compact`.
- **`task/`** — Task lifecycle (pending → running → complete/failed).
- **`mcp/`** — MCP (Model Context Protocol) types and JSON-RPC message definitions.
- **`config/`** — 5-level priority config merging (builtin → user → project → env → flags).
- **`permission/`** — Permission system with modes (allow/deny/ask) and path-based rule matching.
- **`agent/`**, **`team/`**, **`skill/`**, **`rules/`**, **`hook/`**, **`memory/`**, **`session/`** — Sub-agent definitions, multi-agent team coordination, skills activation, rule loading, hook lifecycle, memory persistence model, and session model.

### Application Layer (`internal/application/`)

Each service file orchestrates one domain concern:

- **`query_service.go`** — Drives the query engine loop, wires API provider + tools.
- **`team_service.go`** — Multi-agent team management with NLP routing, message passing, and task assignment (coordinator + worker pattern).
- **`agent_service.go`** — Agent execution factory and lifecycle.
- **`mcp_service.go`** — MCP server connection management.
- **`skill_service.go`** — Skill loading with conditional activation.
- **`config_service.go`**, **`session_service.go`**, **`memory_service.go`**, **`rules_service.go`**, **`command_service.go`**, **`task_service.go`** — Corresponding domain orchestrations.

### Infrastructure Layer (`internal/infrastructure/`)

Implements domain interfaces with real I/O:

- **`api/anthropic/`** — Anthropic API client with SSE streaming.
- **`api/deepseek/`** — DeepSeek API client (OpenAI-compatible protocol).
- **`api/bedrock/`**, **`api/vertex/`** — AWS Bedrock and GCP Vertex AI providers.
- **`sandbox/`** — Command sandboxing: Linux uses `bwrap` (bubblewrap), macOS uses `sandbox-exec`. Restricts file/network access for executed commands.
- **`mcp/`** — MCP transport implementations: stdio, HTTP, SSE, and WebSocket.
- **`filesystem/`** — File operations, glob matching, ripgrep integration.
- **`git/`** — Git operations wrapper.
- **`shell/`** — Shell command execution with timeout and output capture.
- **`compact/`** — Message compaction via summarization or truncation when context exceeds budget.
- **`worktree/`** — Git worktree isolation for parallel agent work.
- **`persistence/`**, **`state/`**, **`appconfig/`** — Config/session persistence, in-memory state store, YAML config loading.

### Interfaces Layer (`internal/interfaces/`)

- **`cli/`** — Cobra command definitions (17 files): `root`, `chat`, `doctor`, `run`, `config`, `mcp`, `skill`, `agent`, `team`, etc.
- **`shell/`** — Interactive REPL (49 files): line editor, markdown rendering with syntax highlighting, dialog system, tab completion, input handling, conversation display.
- **`tui/`** — Bubbletea-based terminal UI models and views.

### Tools (`internal/tools/`)

Concrete tool implementations registered with the domain `Registry`:
`file_read`, `file_write`, `file_edit`, `bash`, `glob`, `grep`, `agent_tool`, `mcp_tool`, `skill_tool`, `team_tools`, `ask_user`, `todo_write`, `web_search`, `web_fetch`.

### Public Packages (`pkg/`)

Reusable libraries independent of goclaude internals:
- **`dotenv/`** — `.env` file parser with override/load modes.
- **`frontmatter/`** — Markdown YAML frontmatter extraction.
- **`settingsenv/`** — Bridges `settings.json` values to environment variables.
- **`wsclient/`** — WebSocket client with reconnection.

### Configuration

**Config priority** (highest wins): CLI flags → project `.goclaude.yaml` → user `~/.goclaude/config.yaml` → `configs/default.yaml`.

**Environment variable priority** (highest wins): process env → `--env-file` → `.env.local` → `.env` (local) → nearest `.env` (parent dirs) → `~/.claude/.env`.

### Key Design Decisions

- The query engine in `domain/query/` drives the main AI loop: send messages → receive streaming response → detect tool calls → execute tools → loop.
- Tool execution uses a read/write split: read-only tools run concurrently, write tools run serially with permission checks.
- The sandbox (`infrastructure/sandbox/`) wraps `bash` tool execution to restrict filesystem and network access based on configurable policies.
- Multi-agent teams use a coordinator pattern where the lead agent delegates tasks to worker agents, communicating via an in-memory message bus.
- MCP integration allows connecting to external tool servers via multiple transport protocols, dynamically registering their tools into the tool registry.
