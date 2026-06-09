# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [0.1.0] — 2026-06-09

> First public release. 293 files, +64,730 lines of Go.

GoClaude is a terminal AI coding assistant — a Go rewrite of the upstream TypeScript
claude-code, designed around strict DDD four-layer architecture with dependency
inversion.

### Added

#### AI Providers
- Anthropic API client with SSE streaming
- DeepSeek API client (OpenAI-compatible protocol)
- AWS Bedrock and GCP Vertex AI provider interfaces
- Custom API base URL override via config

#### Interactive REPL
- Line editor, markdown rendering, syntax highlighting, tab completion
- 17 built-in slash commands (`/help`, `/skills`, `/mcp`, `/agents`, `/teams`, `/remember`, etc.)
- Permission mode live toggle (Shift+Tab)

#### Tool System
- `file_read`, `file_write`, `file_edit`, `bash`, `glob`, `grep`
- `agent` (subagent), `skill`, `mcp`, `team`, `web_search`, `web_fetch`, `todo_write`
- Read-tool concurrent execution (max 10), write-tool serial execution with permission gating

#### MCP Protocol
- Four transport layers: `stdio`, `HTTP`, `SSE`, `WebSocket`
- JSON-RPC dynamic tool registration under `mcp__<server>__<tool>` namespace
- Multi-source config (project `.mcp.json` → user `settings.json`)

#### Multi-Agent Teams
- Coordinator + Worker pattern with NLP routing
- Team creation, message passing, task assignment, auto-summarization
- `/teams` REPL command and full `goclaude team` CLI management

#### Skills
- Multi-source loading with `paths`-based conditional activation
- YAML frontmatter metadata extraction
- User-defined custom skills

#### Rules
- Project-level + user-level rules auto-injected into system prompt
- Behavioral constraints across naming, security, testing, and style

#### Memory
- Cross-session persistence under `~/.goclaude/projects/<sanitized-path>/memory/`
- Autonomous `update_memory` tool calls by the AI
- `/remember` command for review, dedup, promotion, and cleanup

#### Hook System
- 7 event types: `SessionStart/End`, `SubagentStart/Stop`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`
- YAML declarative config with stdin/stdout JSON protocol
- Context accumulation, block short-circuit, error isolation, `sync.RWMutex` safety

#### Configuration
- 5-level priority merge: CLI flags → project `.goclaude.yaml` → user `~/.goclaude/config.yaml` → `configs/default.yaml`
- `.env` loading chain: `--env-file` → `.env.local` → `.env` → parent `.env` → `~/.claude/.env`

#### Permission System
- 4 modes: `default`, `acceptEdits`, `plan`, `bypass`
- Path-based rule matching; `auto_approve_read` for read-only tools

#### Sandbox
- Linux: `bwrap` (bubblewrap) filesystem + network isolation
- macOS: `sandbox-exec` native sandboxing
- Configurable read/write path allow/deny lists

#### Context Management
- Automatic compaction when context exceeds token budget (summarization / truncation)
- `goclaude doctor` environment diagnostic command
- `goclaude chat` and `goclaude run` non-interactive CLI modes

#### Build & Platform
- Cross-compilation: linux/macOS/windows × amd64/arm64
- E2E test suite (expect + Python) and Go integration tests
- `make all` pipeline: fmt → vet → lint → test → build

---

[0.1.0]: https://github.com/yaoice/goclaude/releases/tag/v0.1.0
