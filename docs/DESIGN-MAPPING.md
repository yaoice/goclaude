# 设计 → 代码 对照（Design Conformance Mapping）

> 本文档把上游设计方案 `CODEBUDDY.md`（基于 `src/` TypeScript 实现逆向梳理的软件设计）逐模块映射到 `goclaude` 的 Go 包，用于验证 **"严格遵循设计的架构与模块划分"**。
>
> 设计采用 **"内核 + 可插拔外围"** 的分层；Go 实现采用 **DDD 四层（domain / application / infrastructure / interfaces）+ 依赖倒置**。两者并非一一对名，而是按职责对齐：设计中的"与 UI/传输无关的 Agent 内核"落在 `domain`，"可替换的入口/渲染"落在 `interfaces`，"外部系统交互"落在 `infrastructure`，"领域编排"落在 `application`。

---

## 0. 分层与依赖方向

```
interfaces ──▶ application ──▶ domain ◀── infrastructure
   (入口/渲染)    (用例编排)      (核心抽象)     (实现接口)
                         tools ──▶ domain/tool（实现 Tool 接口）
```

依赖倒置原则（DIP）已用静态约束验证（见 README"架构守护"或 `go vet`）：

- `domain/*` **不导入** `infrastructure / interfaces / application / tools`
- `infrastructure/*` **不导入** `interfaces`
- 领域只定义接口（如 `query.AIProvider`、`tool.Tool`、`query.Compactor`），由外层实现

---

## 1. Agent 内核（设计 §2.1 query / QueryEngine）

| 设计要素 | src(TS) | goclaude 包 / 文件 |
|---|---|---|
| 代理主循环 `queryLoop` | `src/query.ts` | `internal/domain/query/engine.go`（`Engine.Execute` 的 `for{}` 状态机） |
| 会话/SDK 编排 `QueryEngine` | `src/QueryEngine.ts` | `internal/application/query_service.go` |
| 流式事件解析 | `src/query.ts` processStream | `internal/domain/query/stream.go`、`engine.go:processStream` |
| token 预算 | `src/query/tokenBudget.ts` | `internal/domain/query/budget.go` |
| 消息模型 | `src/types/message.ts` | `internal/domain/query/message.go` |
| 会话持久化接口 | — | `internal/domain/query/repository.go` |

## 2. 工具系统（设计 §2.2 tools/ + services/tools/）

| 设计要素 | src(TS) | goclaude 包 / 文件 |
|---|---|---|
| 工具契约 `Tool` 接口 | `src/Tool.ts` | `internal/domain/tool/tool.go`（`Tool` 接口） |
| 工具上下文 `ToolUseContext` | `src/Tool.ts` | `internal/domain/tool/context.go`（`UseContext`） |
| 工具注册/装配 | `src/tools.ts` | `internal/domain/tool/registry.go`、`internal/tools/register.go` |
| 编排：读并发/写串行 | `src/services/tools/toolOrchestration.ts` | `internal/domain/tool/executor.go`（`Executor.Execute`） |
| 流式调度 | `StreamingToolExecutor.ts` | `internal/domain/tool/executor.go` + `events.go` |
| 内置工具实现 | `src/tools/*` | `internal/tools/*`（file_read/write/edit, bash, glob, grep, agent_tool, mcp_tool, skill_tool…） |

## 3. 权限系统（设计 §2.3 utils/permissions/）

| 设计要素 | src(TS) | goclaude 包 / 文件 |
|---|---|---|
| 权限类型（Mode/Result/Behavior） | `src/types/permissions.ts` | `internal/domain/tool/tool.go`（`PermissionMode`/`PermissionResult`/`PermissionContext`） |
| 工具级 `checkPermissions` | `src/Tool.ts` | 各工具 `CheckPermissions(...)`（`internal/tools/*`） |
| 模式循环（Shift+Tab） | `getNextPermissionMode.ts` | `internal/interfaces/cli/permission_mode.go` |
| 规则来源分层（rules） | `src/utils/permissions` | `internal/domain/rules/*` + `internal/infrastructure/rules` |

## 4. 上下文与压缩（设计 §2.4 services/compact/）

| 设计要素 | src(TS) | goclaude 包 / 文件 |
|---|---|---|
| 压缩器接口 `Compactor` | — | `internal/domain/query`（领域接口） |
| 摘要式压缩 autoCompact | `services/compact/*` | `internal/infrastructure/compact/summarizing.go` |
| 截断式压缩 micro/snip | `services/compact/*` | `internal/infrastructure/compact/truncating.go` |
| 装配入口 | — | `internal/infrastructure/compact/compact.go` |

## 5. API 服务层（设计 §2.5 services/api/）

| 设计要素 | src(TS) | goclaude 包 / 文件 |
|---|---|---|
| 领域 Provider 接口 | — | `internal/domain/query`（`AIProvider`） |
| Anthropic 客户端（SSE） | `services/api/claude.ts` | `internal/infrastructure/api/anthropic/*` |
| DeepSeek（OpenAI 兼容） | — | `internal/infrastructure/api/deepseek/*` |
| AWS Bedrock | — | `internal/infrastructure/api/bedrock/*` |
| GCP Vertex | — | `internal/infrastructure/api/vertex/*` |
| 重试/回退/错误分类 | `withRetry.ts`/`errors.ts` | 各 client 内 `withRetry`/错误解析 |

## 6. 输入入口与输出渲染（设计 §2.6）

| 设计要素 | src(TS) | goclaude 包 / 文件 |
|---|---|---|
| 进程入口 + .env/配置装配 | `entrypoints/cli.tsx` | `cmd/goclaude/main.go` |
| 命令树（Commander） | `main.tsx` | `internal/interfaces/cli/root.go` 等（cobra） |
| 交互式 REPL | `screens/REPL.tsx` | `internal/interfaces/shell/repl.go` + 同包渲染件 |
| 行编辑器 | `src/ink/*` | `internal/interfaces/shell/editor.go` |
| 终端渲染（markdown/高亮/格式化） | `src/ink/*` | `internal/interfaces/shell/{markdown,highlight,formatter}.go` |
| headless `-p` 渲染 | `QueryEngine.ask` | `internal/interfaces/cli/headless_render.go` |
| TUI（bubbletea） | `src/ink/*` | `internal/interfaces/tui/app.go` |

## 7. 命令系统（设计 §2.7 commands.ts + commands/）

| 设计要素 | src(TS) | goclaude 包 / 文件 |
|---|---|---|
| Slash 命令框架（prompt/local） | `src/commands.ts` | `internal/domain/command/command.go` |
| 命令路由执行 | `src/commands/*` | `internal/application/command_service.go` |

## 8. 多 Agent 协调与任务（设计 §2.8 coordinator/ + tasks/）

| 设计要素 | src(TS) | goclaude 包 / 文件 |
|---|---|---|
| 子 agent 派生 | `src/tools/AgentTool/` | `internal/domain/agent/*` + `internal/tools/agent_tool.go` + `internal/application/agent_service.go`、`agent_factory.go` |
| Coordinator+Worker | `src/coordinator/` | `internal/application/team_service.go`、`team_nlp.go` + `internal/domain/team/*` |
| 任务生命周期 | `src/Task.ts`/`src/tasks/` | `internal/domain/task/*` + `internal/application/task_service.go` |
| 团队/消息工具 | `SendMessageTool`/`TeamCreateTool` | `internal/tools/team_*.go` |
| 团队持久化 | — | `internal/infrastructure/team/store.go` |
| worktree 隔离 | — | `internal/infrastructure/worktree/*` |

## 9. 可插拔扩展（设计 §2.9 mcp / plugins / skills）

| 设计要素 | src(TS) | goclaude 包 / 文件 |
|---|---|---|
| MCP 协议（JSON-RPC） | `services/mcp/*` | `internal/domain/mcp/*` |
| MCP 传输（stdio/HTTP/SSE/WS） | `services/mcp/*` | `internal/infrastructure/mcp/*`（`manager.go`/`client_impl.go`） |
| MCP 服务编排 | — | `internal/application/mcp_service.go` |
| Skills 加载/条件激活 | `src/skills/*` | `internal/domain/skill/*` + `internal/infrastructure/skill/*` + `internal/application/skill_service.go`、`skill_activation.go` |
| Hooks | `src/hooks/*` | `internal/domain/hook/*` |
| 记忆（MEMORY.md） | `memdir/*` | `internal/domain/memory/*` + `internal/infrastructure/memory/*` + `internal/application/memory_service.go` |

## 10. 配置 / 会话 / 状态（横切）

| 设计要素 | src(TS) | goclaude 包 / 文件 |
|---|---|---|
| 多层配置合并 | 配置多层合并 | `internal/domain/config/*` + `internal/infrastructure/{appconfig,persistence}` + `internal/application/config_service.go` |
| 会话管理/恢复 | `--resume` 链路 | `internal/domain/session/*` + `internal/application/session_service.go` |
| 内存状态 Store | `state/AppState.ts` | `internal/infrastructure/state/store.go` |
| 沙箱 | sandbox | `internal/infrastructure/sandbox/*` |
| 文件系统/glob/ripgrep | `tools/Glob,Grep` | `internal/infrastructure/filesystem/*` |
| shell 执行 | `BashTool` | `internal/infrastructure/shell/*` |
| git | — | `internal/infrastructure/git/*` |

## 11. 可复用基础库（pkg/）

| 包 | 职责 |
|---|---|
| `pkg/dotenv` | `.env` 解析与多文件优先级加载（与官方 claude 行为对齐） |
| `pkg/frontmatter` | Markdown frontmatter 解析（skills/rules 用） |
| `pkg/settingsenv` | `settings.json` 的 env 字段桥接为进程环境 |
| `pkg/wsclient` | WebSocket 客户端（MCP WS 传输支撑） |

---

## 端到端数据流（对应设计 §3.1）

```
用户输入(cli/shell)
  └─▶ interfaces 解析 → application/query_service 组装 system prompt + 消息
        └─▶ domain/query.Engine.Execute  ── Agent 主循环 ──┐
              ├─ 压缩: infrastructure/compact（接近预算阈值）
              ├─ provider.Stream(): infrastructure/api/*（流式 SSE）
              ├─ 解析 tool_use → domain/tool.Executor（读并发/写串行）
              │     每工具: ValidateInput → CheckPermissions → Call
              ├─ tool_result 回灌为 user 消息
              └─ afterToolHook（skill 条件激活等）→ 下一轮 ┘
        └─▶ StreamEvent 流 → interfaces/shell(REPL) 渲染 / cli headless 序列化
```

> 与设计一致的核心取舍：`Engine.Execute` 用 `chan StreamEvent` 实现"边生成边渲染"，UI 与内核解耦；`context.Context` 替代 TS 的 `AbortController` 实现级联取消；只读工具并发、写工具串行，在性能与一致性间平衡。
