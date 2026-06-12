# CODEBUDDY.md This file provides guidance to CodeBuddy when working with code in this repository.

## Project Overview

GoClaude is a Go reimplementation of the upstream TypeScript claude-code terminal AI assistant. It follows strict **DDD (Domain-Driven Design) four-layer architecture** with dependency inversion. The project has **87 test files** and builds a single binary at `./bin/goclaude`.

Module: `github.com/yaoice/goclaude` (Go 1.22, no external CGO dependencies; uses embedded `modernc.org/sqlite`).

## Commands

### Build
```bash
make build             # Compile to ./bin/goclaude with git version injection
make all               # Full pipeline: fmt → vet → lint → test → build
make build-all         # Cross-compile: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64
```

### Test
```bash
make test                    # Run all tests with -race and -cover
make test-verbose            # Same with verbose output
make test-coverage           # Generate coverage report in ./coverage/
make test-race               # Race detector only
go test ./pkg/domain/query/...  # Run tests for a specific package
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
make install       # go install
```

### Dependencies
```bash
make deps          # go mod download && go mod tidy
make deps-verify   # go mod verify
```

### Utilities
```bash
make clean         # Remove bin/ and coverage/
make clean-all     # make clean + go clean -cache
make help          # List all available targets
```

## Architecture

### Layer Dependency Flow

```
interfaces → application → domain ← infrastructure
```

- **Domain** defines pure interfaces and models with zero external dependencies
- **Infrastructure** implements domain interfaces (dependency inversion)
- **Application** orchestrates domain logic into use cases
- **Interfaces** adapts external input (CLI, Shell REPL, TUI) to application services

---

## Domain Layer (`pkg/domain/`)

The core business logic, entirely free of external dependencies:

| Package | Purpose |
|---------|---------|
| **`query/`** | Query engine main loop: conversation turns, token budgets, streaming events, message models. This is the heart of the AI interaction cycle. |
| **`tool/`** | Tool interface (`Tool`), `Registry` (tool discovery), and `Executor` (concurrent reads, serial writes). |
| **`command/`** | Slash command framework for REPL commands like `/help`, `/clear`, `/compact`. |
| **`task/`** | Task lifecycle (pending → running → complete/failed). |
| **`mcp/`** | MCP (Model Context Protocol) types and JSON-RPC message definitions. |
| **`config/`** | Configuration models: `GlobalConfig` (user-level, stored per-project key) and `ProjectConfig` (project-level, YAML). |
| **`permission/`** | Permission system with modes (allow/deny/ask) and path-based rule matching. |
| **`agent/`** | Sub-agent definitions and lifecycle. |
| **`team/`** | Multi-agent team coordination: coordinator, members, task assignment, message passing. |
| **`skill/`** | Skill definitions and conditional activation rules. |
| **`rules/`** | Rule files loading and precedence model. |
| **`hook/`** | Hook lifecycle events and callback definitions. |
| **`memory/`** | Long-term memory persistence model. |
| **`session/`** | Session state and lifecycle. |
| **`workflow/`** | Workflow engine: `Definition`, `Engine`, and `State` — declarative multi-step agent workflows. |

---

## Application Layer (`pkg/application/`)

Orchestrates domain logic. Top-level service files + two sub-packages:

### Core Services (top-level)
| File | Responsibility |
|------|---------------|
| `query_service.go` | Drives the query engine loop: wires API provider + tools. |
| `team_service.go` | Multi-agent team orchestration (coordinator + worker pattern). |
| `agent_service.go` / `agent_service_exec.go` / `agent_factory.go` | Agent execution factory and lifecycle. |
| `mcp_service.go` | MCP server connection management. |
| `skill_service.go` / `skill_activation.go` | Skill loading with conditional activation. |
| `workflow_service.go` / `workflow_nlp.go` | Workflow execution orchestration with NLP routing. |
| `plan_agent.go` / `plan_agent_service.go` | Planning agent: generates execution plans before acting. |
| `team_engine.go` / `team_nlp.go` / `team_planning.go` | Team planning and NLP-based task routing. |
| `team_member_worker.go` | Worker agent lifecycle within a team. |
| `team_service_membership.go` / `team_service_messaging.go` / `team_service_tasks.go` | Team member management, inter-agent messaging, task tracking. |
| `fork_context.go` | Context forking for sub-agent execution. |
| `agent_tool_summary.go` | Summarization of sub-agent results. |
| `config_service.go`, `session_service.go`, `memory_service.go`, `rules_service.go`, `command_service.go`, `task_service.go` | Corresponding domain orchestrations. |

### `hooks/` Sub-package
Hook lifecycle implementations (11 files): `session_lifecycle.go`, `message_hooks.go`, `permission_hooks.go`, `notification_hooks.go`, `diff_hooks.go`, `ide_hooks.go`, `vim_hooks.go`, `terminal_hooks.go`, `memory_lifecycle.go`, `memory_filter.go`, `api_key_verifier.go`, `history_search.go`, `session_hooks.go`.

### `memory/` Sub-package
- `longterm_service.go` — Long-term memory persistence service with SQLite backend.

---

## Infrastructure Layer (`pkg/infrastructure/`)

Implements domain interfaces with real I/O:

| Package | Purpose |
|---------|---------|
| **`api/anthropic/`** | Anthropic API client with SSE streaming. |
| **`api/deepseek/`** | DeepSeek API client (OpenAI-compatible protocol). |
| **`api/bedrock/`** | AWS Bedrock API provider. |
| **`api/vertex/`** | GCP Vertex AI API provider. |
| **`sandbox/`** | Command sandboxing: Linux uses `bwrap` (bubblewrap), macOS uses `sandbox-exec`. Restricts file/network access. |
| **`mcp/`** | MCP transport implementations: stdio, HTTP, SSE, WebSocket. |
| **`filesystem/`** | File operations, glob matching, ripgrep integration. |
| **`git/`** | Git operations wrapper. |
| **`shell/`** | Shell command execution with timeout and output capture. |
| **`compact/`** | Message compaction via summarization or truncation when context exceeds budget. |
| **`worktree/`** | Git worktree isolation for parallel agent work. |
| **`persistence/`** | Config/session file persistence (JSON-based). |
| **`state/`** | In-memory state store for runtime session data. |
| **`appconfig/`** | YAML-based application config loading from `configs/default.yaml`. |
| **`configdir/`** | Project and user config directory detection (`~/.goclaude/`, `.goclaude/`). |
| **`memory/sqlite/`** | SQLite-backed long-term memory storage (using `modernc.org/sqlite`). |
| **`rules/`** | Filesystem-based `.goclaude/rules/` file loading. |
| **`skill/`** | Skill file loading from filesystem and built-in paths. |
| **`team/`** | Team session persistence and message bus. |
| **`todo/`** | Todo list persistence and tracking. |
| **`workflow/`** | Workflow definition loader from YAML files. |
| **`agent/`** | Agent runtime implementation for sub-agent processes. |

---

## Interfaces Layer (`pkg/interfaces/`)

### CLI (`cli/`) — 15 source files + 1 sub-package
Cobra command definitions: `root.go`, `chat.go`, `run.go`, `root_doctor.go`, `root_repl.go`, `appcfg.go`, `extensions.go`, `permission_mode.go`, `logging.go`, `prettylog.go`, `headless_render.go`, `shell_adapter.go`, `team_lifecycle.go`, `workflow_adapter.go`, `cli/hooks/navigation.go`.

Key commands: `chat`, `run`, `doctor`, `repl`, `config`, `agent`, `team`, `mcp`, `skill`, `workflow`.

### Shell REPL (`shell/`) — 51 source files (34 non-test + 17 test)
Interactive terminal: line editor (`editor*.go`), markdown rendering with syntax highlighting (`markdown.go`, `highlight.go`), dialog system (`dialog*.go`, `agents_dialog.go`, `mcp_dialog.go`, `skills_dialog.go`), tab completion (`completer.go`), history (`history.go`), streaming code display, sub-agent progress rendering, permission dialogs, transcript output, tool rendering, sandbox display, and workflow REPL integration.

### TUI (`tui/`) — 2 files
Bubbletea-based terminal UI: `app.go`, `login_page.go`.

---

## Tools (`pkg/tools/`)

Concrete tool implementations registered with the domain `Registry`:

**File tools:** `file_read`, `file_write`, `file_edit`  
**Search tools:** `glob`, `grep` (via ripgrep)  
**Execution:** `bash` (sandboxed)  
**Agent/Team:** `agent_tool`, `team_tools` (with sub-variants: `team_tasks`, `team_tasks_auto`, `team_tools_advanced`, `team_tools_planning`, `team_tools_status`, `team_session`)  
**External:** `mcp_tool` (dynamic from MCP servers), `skill_tool`  
**Interaction:** `ask_user`, `todo_write`  
**Web:** `web_search`, `web_fetch`  
**Registration:** `register.go`, `team_register.go`

---

## Utility Packages (`pkg/util/`)

| Package | Purpose |
|---------|---------|
| **`dotenv/`** | `.env` file parser with override/load modes. |
| **`frontmatter/`** | Markdown YAML frontmatter extraction. |
| **`settingsenv/`** | Bridges `settings.json` values to environment variables. |
| **`wsclient/`** | WebSocket client with reconnection. |
| **`hooks/`** | Shared hook utilities used across application and interfaces layers. |

---

## Configuration

**Config priority** (highest wins): CLI flags → project `.goclaude.yaml` → user `~/.goclaude/config.yaml` → builtin `configs/default.yaml`.

`GlobalConfig` stores per-project settings (API keys, MCP servers) keyed by project path. `ProjectConfig` stores project-level settings (allowed tools, dialog acceptance).

**Environment variable priority** (highest wins): process env → `--env-file` → `.env.local` → `.env` (local) → nearest `.env` (parent dirs) → `~/.claude/.env`.

---

## Key Design Decisions

- **Query engine** (`domain/query/`) drives the main AI loop: send messages → receive streaming response → detect tool calls → execute tools → loop.
- **Tool execution** uses a read/write split: read-only tools run concurrently, write tools run serially with permission checks.
- **Sandbox** (`infrastructure/sandbox/`) wraps `bash` tool execution to restrict filesystem and network access based on configurable policies.
- **Multi-agent teams** use a coordinator pattern: the lead agent delegates tasks to worker agents, communicating via an in-memory message bus.
- **MCP integration** allows connecting to external tool servers via multiple transport protocols (stdio, HTTP, SSE, WebSocket), dynamically registering their tools.
- **Workflow engine** (`domain/workflow/`) enables declarative multi-step agent workflows defined in YAML, executed by the workflow service with NLP-based routing.
- **Memory system** uses SQLite (via `modernc.org/sqlite`) for long-term memory persistence, with a dedicated `memory/` sub-package in application layer.
- **Hook system** spans application and interfaces layers: lifecycle hooks for sessions, messages, permissions, notifications, diffs, IDE integration, Vim mode, terminal events, and memory filtering.

## Test Structure

- **87 test files** total across the project (counted as `*_test.go`)
- Unit tests co-located with source in `pkg/` (Go convention)
- Integration tests: `tests/integration/e2e_integration_test.go`
- E2E scripts: `tests/e2e/` (expect-based REPL smoke tests, MCP echo server)

## Entry Points

- `cmd/goclaude/main.go` — Main binary entry point
- `cmd/prove_memory/prove_memory.go` — Long-term memory end-to-end verification tool (build-tag gated)
