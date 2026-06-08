# GoClaude

基于 **DDD（领域驱动设计）** 四层架构的 Golang 终端 AI 编程助手，是上游 TypeScript claude-code 的 Go 重写版本。支持多 AI Provider 流式对话、工具调用、MCP 协议集成与多 Agent 团队协作。

---

## 目录

- [项目简介](#项目简介)
- [功能特性](#功能特性)
- [系统环境要求](#系统环境要求)
- [快速开始](#快速开始)
- [安装与构建](#安装与构建)
- [配置说明](#配置说明)
- [使用示例](#使用示例)
  - [REPL 基础](#使用示例repl-基础)
  - [Skill 定义与触发](#实战skill--定义专业能力)
  - [MCP 外部工具连接](#实战mcp--连接外部工具)
  - [Subagent 子任务执行](#实战subagent--派生子-agent-执行子任务)
  - [Agent Teams 团队协作](#实战agent-teams--多-agent-团队协作)
  - [Rules 行为约束](#实战rules--行为约束规则)
  - [Memory 持久化上下文](#实战memory--持久化上下文)
  - [Hook 事件拦截与上下文注入](#实战hook--事件拦截与上下文注入)
- [CLI 命令速查](#cli-命令速查)
- [支持的模型](#支持的模型)
- [项目结构](#项目结构)
- [开发指南](#开发指南)
- [许可证](#许可证)

---

## 项目简介

GoClaude 是一个运行在终端中的 AI 编程助手，能够与大模型进行流式对话、自主调用工具完成编码任务、连接外部 MCP 服务器扩展能力，并支持多 Agent 团队协作处理复杂项目。

### 适用场景

| 场景 | 说明 |
|------|------|
| 日常编码辅助 | 在终端中与 AI 讨论代码、让 AI 帮助编写和修改文件 |
| 代码审查与重构 | AI 读取项目代码后给出重构建议并直接实施 |
| 自动化任务 | 通过 `run` 命令让 AI 自主调用工具完成复杂任务 |
| 多 Agent 协作 | 将复杂项目分解为子任务，由团队并行执行 |
| 私有部署 | 支持自定义 API Base URL，对接内部代理或私有模型服务 |

---

## 功能特性

### 核心能力

| 特性 | 说明 |
|------|------|
| 多 Provider | Anthropic（SSE 流式）、DeepSeek（OpenAI 兼容）、AWS Bedrock、GCP Vertex AI |
| 工具系统 | file_read / write / edit、bash、glob、grep、agent、skill、mcp、team 等 14+ 种内置工具 |
| MCP 协议 | stdio / HTTP / SSE / WebSocket 四种传输，JSON-RPC 动态工具注册 |
| 多 Agent 协作 | Coordinator + Worker 团队模式，NLP 路由、消息传递与任务分配 |
| Skills 系统 | 多来源按需加载，条件激活，可扩展自定义 Skill |
| 沙箱 | Linux 使用 bwrap（bubblewrap）、macOS 使用 sandbox-exec，限制文件系统与网络访问 |
| 交互式 REPL | 行编辑、Markdown 渲染、语法高亮、Tab 补全、对话显示 / `/remember` 记忆审查 |
| 权限控制 | default / acceptEdits / plan / bypass 四种模式 |
| 自动压缩 | 上下文超出 Token 预算时自动摘要或截断 |
| 多 Agent 团队 | 异步团队创建与管理，消息传递、任务分配 |

### 已实现模块

| 模块 | 状态 | 说明 |
|------|------|------|
| 查询引擎 | ✅ | 消息循环、流式处理、Token 预算、自动压缩 |
| 工具系统 | ✅ | 注册表、并发执行器、权限检查 |
| 核心工具 | ✅ | file_read / write / edit、bash、glob、grep、agent 等 |
| 命令系统 | ✅ | Slash 命令框架（/help、/clear、/compact 等） |
| 任务系统 | ✅ | 生命周期管理、后台任务调度 |
| MCP 协议 | ✅ | stdio / HTTP / SSE / WebSocket 传输，JSON-RPC 客户端 |
| 权限系统 | ✅ | 四种模式、规则匹配、路径限定 |
| Hook 系统 | ✅ | 7 种事件钩子、上下文注入、异步执行、通知总线 |
| 配置系统 | ✅ | 5 层优先级合并（CLI → 项目 → 用户 → 默认 → 内置） |
| 记忆系统 | ✅ | Agent memory 管理，MEMORY.md 持久化 |
| 认证系统 | ✅ | 多源凭证（OAuth / APIKey / AWS） |
| 多 Agent 协调 | ✅ | Coordinator + Worker 模式 |
| Skills 系统 | ✅ | 多来源加载、注册表、条件激活 |
| TUI 界面 | ✅ | bubbletea REPL、消息渲染、状态栏 |

---

## 系统环境要求

### 必需依赖

| 依赖 | 最低版本 | 说明 |
|------|---------|------|
| Go | ≥ 1.22.0 | 编译与运行 |
| Git | ≥ 2.0 | 版本信息注入 |

### 可选依赖

| 依赖 | 说明 |
|------|------|
| golangci-lint | Lint 静态检查工具 |
| ripgrep (`rg`) | grep 加速，未安装时退化为纯 Go 实现 |
| bwrap | Linux 沙箱支持（macOS 使用内置 sandbox-exec） |
| expect | E2E 测试需要 |
| python3 | MCP echo server 示例 / E2E 测试 |

### 支持平台

| 操作系统 | 架构 | 状态 |
|----------|------|------|
| Linux | amd64 / arm64 | ✅ 全功能支持 |
| macOS | amd64 / arm64 | ✅ 全功能支持（沙箱使用 sandbox-exec） |
| Windows | amd64 | 🔨 编译通过，部分功能待验证 |

---

## 快速开始

```bash
# 1. 克隆仓库
git clone https://github.com/yaoice/goclaude.git
cd goclaude

# 2. 安装依赖
make deps

# 3. 构建
make build

# 4. 配置 API Key
cp .env.example .env
# 编辑 .env，填入你的 API Key

# 5. 环境检查
./bin/goclaude doctor

# 6. 启动交互式 REPL
./bin/goclaude
```

---

## 安装与构建

### 从源码构建

```bash
# 克隆项目
git clone https://github.com/yaoice/goclaude.git
cd goclaude

# 下载依赖
make deps

# 构建（输出到 ./bin/goclaude）
make build
```

### 交叉编译

```bash
# Linux amd64
make build-linux-amd64

# Linux arm64
make build-linux-arm64

# macOS amd64
make build-darwin-amd64

# macOS arm64 (Apple Silicon)
make build-darwin-arm64

# Windows amd64
make build-windows-amd64

# 所有平台
make build-all
```

交叉编译产物输出到 `./bin/<os>_<arch>/goclaude`（Windows 为 `goclaude.exe`）。

### 安装到系统路径

```bash
# 安装到 $GOPATH/bin
make install

# 或手动复制
sudo cp ./bin/goclaude /usr/local/bin/
```

### 一键开发流程

```bash
# 格式化 → 静态检查 → Lint → 测试 → 构建
make all
```

### 配置 API Key

GoClaude 通过环境变量读取 API Key。创建项目根目录下的 `.env` 文件（参考 `.env.example`）：

```bash
# .env
DEEPSEEK_API_KEY=sk-your-deepseek-api-key
# ANTHROPIC_API_KEY=sk-ant-your-anthropic-api-key
```

环境变量加载优先级（从高到低）：

| 优先级 | 来源 | 说明 |
|--------|------|------|
| 0 | 进程环境变量 | `export` / shell 注入，最高优先 |
| 1 | `--env-file <path>` | CLI 显式指定，可重复使用 |
| 2 | `./.env.local` | 本地开发覆盖（建议加入 .gitignore） |
| 3 | `./.env` | 当前目录 |
| 4 | 父目录 `.env` | 向上查找到的最近 .env 文件 |
| 5 | `~/.claude/.env` | 用户全局 |

> 诊断命令 `goclaude doctor` 可检查实际加载的 .env 文件路径与变量名。

---

## 配置说明

### 配置层级

GoClaude 支持 5 层配置合并，高优先级覆盖低优先级：

| 优先级 | 来源 | 路径 | 说明 |
|--------|------|------|------|
| 1 | CLI 参数 | `--max-turns`、`--no-mcp` 等 | 一次性临时覆盖，最高优先 |
| 2 | 项目级配置 | `<project>/.goclaude.yaml` | 团队共享或项目特定 |
| 3 | 用户级配置 | `~/.goclaude/config.yaml` | 个人偏好 |
| 4 | 内置默认 | `configs/default.yaml` | 与二进制一同发布 |

### 默认配置 (`configs/default.yaml`)

```yaml
api:
  provider: deepseek                  # AI Provider：deepseek | anthropic
  model: deepseek-chat                # 默认模型
  max_tokens: 32768                   # 单次最大输出
  temperature: 1.0                    # 采样温度 (0.0 ~ 2.0)
  stream: true                        # 流式响应

engine:
  max_turns: 100                      # 单次查询最大工具循环数
  token_budget: 200000                # 上下文 Token 预算上限
  auto_compact: true                  # 自动压缩

tools:
  max_concurrency: 10                 # 只读工具最大并发数
  timeout: 120s                       # 工具默认超时

mcp:
  enabled: true                       # 自动连接 MCP 服务器
  connect_timeout: 30s
  request_timeout: 60s

permissions:
  mode: bypass                        # default | acceptEdits | plan | bypass
  auto_approve_read: true             # 只读工具自动放行

sandbox:
  enabled: true
  filesystem_write:
    allow: ["./", "~/.goclaude/tmp"]
    deny: ["~/.ssh", "~/.aws"]
  network:
    disable_network: false
```

### 项目级配置示例 (`.goclaude.yaml`)

```yaml
api:
  provider: anthropic
  model: claude-sonnet-4-20250514
  temperature: 0.7

engine:
  max_turns: 50

permissions:
  mode: acceptEdits                  # 自动放行编辑，无需逐次确认
```

### 自定义 Provider Base URL

在 `~/.goclaude/config.yaml` 中覆盖 API 地址：

```yaml
providers:
  deepseek:
    base_url: https://your-proxy.example.com
    timeout: 300s
    max_retries: 3
```

---

## 使用示例：REPL 基础

### 1. 启动 REPL

```bash
./bin/goclaude
```

```
╭─────────────────────────────────────╮
│  GoClaude v0.1.0  (deepseek-chat)   │
│  Type /help for commands, Ctrl+D    │
│  to exit                            │
╰─────────────────────────────────────╯

>
```

进入 REPL 后即可与 AI 自由对话。REPL 支持 17 个内置 slash 命令（详见末尾[命令速查表](#repl-命令速查)）和自定义 prompt-类命令。

### 2. CLI 单轮模式

```bash
# 快速问答
./bin/goclaude chat "用一句话解释什么是依赖注入"

# 指定模型
./bin/goclaude chat -m deepseek-reasoner "证明 1+1=2"
./bin/goclaude chat -p anthropic -m claude-sonnet-4-20250514 "what is DDD?"

# 自主工具调用
./bin/goclaude run "读取 cmd/goclaude/main.go 并统计代码行数"
```

### 3. 环境诊断

```bash
./bin/goclaude doctor
```

---

## 实战：Skill — 定义专业能力

Skill 是封装好的专业提示词包，AI 可通过 `skill` 工具按需加载。Skill 支持三种来源：项目级、用户级、托管级。

### 定义 Skill

在项目 `.goclaude/skills/api-reviewer/` 下创建 `SKILL.md`：

```markdown
---
name: "API Reviewer"
description: "Review REST API designs for consistency, security, and performance"
when_to_use: "When reviewing HTTP API endpoints, routes, or middleware"
allowed-tools: ["file_read", "file_edit", "grep", "bash"]
---

You are an API design reviewer. When reviewing code, check:

1. RESTful conventions: proper HTTP methods, resource naming, status codes
2. Security: input validation, auth middleware, CORS settings
3. Performance: pagination defaults, caching headers, N+1 query patterns
4. Error handling: consistent error response format, appropriate status codes

Propose concrete edits using file_edit and explain the rationale for each change.
```

当前所在的目录结构和上下文变量：

| 占位符 | 含义 |
|--------|------|
| `${CLAUDE_PROJECT_DIR}` | 项目根目录 |
| `${CLAUDE_CWD}` | 当前工作目录 |
| `$ARGS` / `${ARGS}` | 用户传入参数 |

### 条件激活

Skill 支持 `paths` 字段实现**自动激活**——当 AI 读取的文件路径匹配 pattern 时，Skill 自动加载：

```markdown
---
name: "Go Code Style"
description: "Enforce idiomatic Go conventions"
when_to_use: "When reviewing or writing Go code"
paths: ["**/*.go", "go.mod", "go.sum"]
allowed-tools: ["file_edit", "file_write", "grep", "bash"]
---
```

### 终端中触发 Skill

#### 方式一：自然语言触发

```
> 审查我项目中 handlers/ 下的所有 API 端点，检查 RESTful 设计是否规范

⚙ 调用工具: skill
  name: api-reviewer
  args: handlers/

⚡ skill activated: api-reviewer

[Skill prompt 被注入到上下文，AI 以 API Reviewer 身份回复]
```

#### 方式二：Slash 命令查看

```
> /skills

╭─ Skills ─────────────────────────────╮
│                                       │
│  api-reviewer     审查 REST API 设计  │
│  go-code-style    强制 Go 代码规范    │
│                                       │
│  [Enter] 查看  [Esc] 退出             │
╰───────────────────────────────────────╯

> /skills api-reviewer

╭─ Skill: api-reviewer ────────────────╮
│ 来源: .goclaude/skills/api-reviewer/  │
│ 激活: 按需（when_to_use 匹配时）      │
│ 工具: file_read, file_edit, grep      │
│                                       │
│ === Prompt Body ===                   │
│ You are an API design reviewer...     │
╰───────────────────────────────────────╯
```

#### Skill 加载目录优先级

| 优先级 | 路径 | 说明 |
|--------|------|------|
| 最高 | `~/.goclaude/skills/` | 用户全局 |
| ↓ | `~/.claude/skills/` | legacy 兜底 |
| ↓ | 逐级向上 `.goclaude/skills/` | 项目级（从 CWD 向上 16 层） |
| 最低 | 逐级向上 `.claude/skills/` | 项目级 legacy 兜底 |

---

## 实战：MCP — 连接外部工具

MCP（Model Context Protocol）让 GoClaude 通过 `mcp__<server>__<tool>` 名称空间调用外部进程提供的工具。

### 配置 MCP 服务器

创建 `.goclaude/.mcp.json`：

```json
{
  "mcpServers": {
    "postgres": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-postgres", "postgresql://localhost/mydb"],
      "env": {
        "PGPASSWORD": "secret"
      }
    },
    "github": {
      "type": "sse",
      "url": "https://api.github.com/mcp",
      "headers": {
        "Authorization": "Bearer ${GITHUB_TOKEN}"
      }
    },
    "custom-api": {
      "type": "http",
      "url": "https://mcp.internal.example.com",
      "headers": {
        "X-API-Key": "${INTERNAL_API_KEY}"
      }
    }
  }
}
```

> 配置优先级：`.goclaude/.mcp.json`（主） > `.claude/.mcp.json`（legacy） > `.mcp.json`（项目根）。用户全局用 `~/.goclaude/settings.json`。

### 终端中触发 MCP 工具

#### 查看连接状态

```
> /mcp

╭─ MCP 服务器 ─────────────────────────╮
│                                       │
│  postgres     ✓ connected            │
│  github       ✓ connected            │
│  custom-api   ✗ disconnected         │
│      Error: connection refused       │
│                                       │
│  [Enter] 查看工具  [Esc] 退出         │
╰───────────────────────────────────────╯
```

#### 列出 MCP 工具

```
> /mcp tools

  mcp__postgres__query       执行 SQL 查询
  mcp__postgres__list_tables 列举所有表
  mcp__github__search_repos  搜索仓库
  mcp__github__create_issue  创建 Issue
```

#### 自然语言触发 MCP 工具

```
> 查询数据库中有多少用户，帮我找出注册时间最早的 5 个

⚙ 调用工具: mcp__postgres__query
  sql: SELECT COUNT(*) FROM users

  返回值: 10234

⚙ 调用工具: mcp__postgres__query
  sql: SELECT id, username, created_at FROM users
       ORDER BY created_at ASC LIMIT 5

  返回值:
  id | username | created_at
  1  | admin    | 2020-01-15
  2  | alice    | 2020-03-22
  3  | bob      | 2020-04-10
  4  | carol    | 2020-05-01
  5  | dave     | 2020-05-18

数据库中共有 10,234 个用户。最早注册的 5 个用户如上所示。

[tokens: 68 in / 512 out | cost: $0.0042 | tools: 2 calls]
```

---

## 实战：Subagent — 派生子 Agent 执行子任务

Subagent 是独立的 AI worker，拥有隔离上下文，专门处理特定子任务。GoClaude 内置 `Explore`（代码搜索）和 `Plan`（方案规划）Agent，也支持自定义。

### 定义自定义 Subagent

创建 `.goclaude/agents/code-reviewer.md`：

```markdown
---
name: "Code Reviewer"
description: "Review code changes for bugs, style, and best practices"
when_to_use: "When code has been written and needs a review pass"
tools: ["file_read", "grep", "bash"]
model: "deepseek-chat"
---

You are a senior code reviewer. Your review should cover:

1. Correctness: logic errors, edge cases, nil checks
2. Style: idiomatic patterns, naming conventions, package structure
3. Performance: unnecessary allocations, goroutine leaks, blocking I/O
4. Security: input sanitization, SQL injection, hardcoded secrets

Report findings grouped by severity: CRITICAL / WARNING / SUGGESTION.
```

### 终端中触发 Subagent

#### 自然语言触发

```
> 我刚在 pkg/util/auth/ 下写了新的认证模块，请派一个 subagent 去做 code review

⚙ 调用工具: agent
  subagent_type: code-reviewer
  prompt: |-
    审查 pkg/util/auth/ 下的所有文件，检查：
    1. token 生成与验证逻辑是否正确
    2. 是否处理了过期和无效 token
    3. 是否有 SQL 注入风险

⚡ subagent [code-reviewer] started...

  [subagent 输出]:
  === CRITICAL ===
  - auth.go:42: jwt.secret 使用了硬编码默认值，应通过环境变量注入
  - middleware.go:28: 未验证 token 签名算法，存在 alg=none 绕过风险

  === WARNING ===
  - token.go:65: 错误信息泄漏了内部错误详情
  - storage.go:103: 数据库查询使用了字符串拼接而非参数化

  === SUGGESTION ===
  - 建议为 token blacklist 添加 TTL 清理机制

[tokens: 124 in / 3,021 out | cost: $0.0210 | subagent: 1]
```

#### CLI 管理

```bash
# 列出所有 Agent
./bin/goclaude agents list

# 查看某个 Agent 详情
./bin/goclaude agents show code-reviewer
```

#### Agent 加载目录

| 路径 | 说明 |
|------|------|
| `<project>/.goclaude/agents/*.md` | 项目自定义 Agent |
| `~/.goclaude/agents/*.md` | 用户全局 Agent |
| 内置 | Explore, Plan |

---

## 实战：Agent Teams — 多 Agent 团队协作

启用 `--agent-teams` 后，GoClaude 切换到 Coordinator + Worker 模式：Leader Agent 可将复杂任务拆解分配给多个 Worker，并行执行、自动汇总。

### 启动团队模式

```bash
./bin/goclaude --agent-teams
```

```
╭─────────────────────────────────────╮
│  GoClaude v0.1.0  [Team Mode]       │
│  Agent-Teams: enabled               │
╰─────────────────────────────────────╯

>
```

### 自然语言触发团队协作

```
> 我需要为一个新项目搭建完整的后端骨架：
> 1. 初始化 Go 模块和项目结构
> 2. 实现用户注册和登录 API
> 3. 添加 JWT 认证中间件
> 请用 team 模式并行处理这些任务

⚙ 调用工具: team_create
  name: backend-init

⚙ 调用工具: create_task
  team: backend-init
  assignee: worker-1
  task: 初始化 Go 模块，创建 cmd/server/main.go、internal/ 目录结构

⚙ 调用工具: create_task
  team: backend-init
  assignee: worker-2
  task: 实现 /api/register 和 /api/login 端点

⚙ 调用工具: create_task
  team: backend-init
  assignee: worker-3
  task: 实现 JWT 中间件

[worker-1] ✓ 完成: 已创建 go.mod、cmd/server/main.go、internal/ 骨架
  → main.go 启动监听 :8080
  → go.sum 已生成

[worker-2] ✓ 完成: 已实现 auth handler
  → POST /api/register: bcrypt hash + 写入 users 表
  → POST /api/login: 密码校验 + 签发 JWT

[worker-3] ✓ 完成: JWT 中间件就绪
  → 从 Authorization: Bearer <token> 提取
  → 验证签名和过期时间
  → 注入 user_id 到 context

⚙ 调用工具: send_message (broadcast)
  content: 所有任务完成。请确认代码合并无误。

[Leader 自动读取各 Worker 的输出，生成整合摘要]

所有三个模块已并行完成。文件清单：
  cmd/server/main.go        — HTTP 入口
  internal/handler/auth.go  — 注册/登录 API
  internal/middleware/jwt.go — JWT 认证中间件
  go.mod / go.sum            — 依赖管理

[tokens: 312 in / 8,421 out | cost: $0.0580 | team: backend-init | workers: 3]
```

### CLI 团队管理

```bash
# 创建团队
./bin/goclaude team create my-team

# 列出所有团队
./bin/goclaude team list
# ┌──────────┬─────────┬─────────────────────┐
# │ 团队名    │ 成员数  │ 创建时间             │
# ├──────────┼─────────┼─────────────────────┤
# │ my-team  │ 0       │ 2026-06-03 01:15:00 │
# └──────────┴─────────┴─────────────────────┘

# Agent 加入团队
./bin/goclaude team join my-team worker-1

# 收发消息
./bin/goclaude team send worker-1 "请处理 auth 模块的单元测试"
./bin/goclaude team inbox worker-1

# 强制删除（含活跃成员）
./bin/goclaude team delete my-team --force
```

### 团队消息流

```
Leader (Coordinator)
   │
   ├─ send_message(worker-1, "task A")
   ├─ send_message(worker-2, "task B")
   │
   ├─ 每轮自动 ProcessLeaderInbox()
   │   读取所有 Worker 的消息 → 注入到上下文
   │
   └─ broadcast("所有任务完成，请确认")
```

---

## 实战：Rules — 行为约束规则

Rules 是一组持久化的行为指令，GoClaude 启动时自动加载并注入到 System Prompt，全程影响 AI 的决策。

### 创建 Rules

#### 项目级 Rules（自动加载）

创建 `.goclaude/rules/coding-standards.md`：

```markdown
# Code Standards

## 命名规范
- 使用驼峰命名（camelCase），导出标识符首字母大写
- 避免缩写，除非广泛公认（URL、JSON、API）
- 测试函数命名：Test<Func>_<场景>_<期望>

## 错误处理
- 永远不要忽略 error：不要使用 `_`
- 使用 `fmt.Errorf("...: %w", err)` 包装错误，保留调用链
- 公开 API 的错误信息不要包含内部实现细节

## 测试
- 每个导出函数必须有单元测试
- 使用 testify/assert 进行断言
- 表格驱动测试优先

## 安全
- 禁止在代码中硬编码密钥、密码、Token
- 所有外部输入必须校验
- SQL 查询必须使用参数化
```

#### 用户级 Rules（全局生效）

创建 `~/.goclaude/rules/preferences.md`：

```markdown
## 语言偏好
- 始终用中文回复
- 代码注释用英文

## 格式偏好
- 优先使用 table-driven tests
- 错误消息用小写开头，不加句号
```

### Rules 加载路径

GoClaude 按以下顺序加载并合并 rules（后加载的**追加**，不覆盖）：

| 顺序 | 路径 | 说明 |
|------|------|------|
| 1 | `~/.goclaude/rules/` | 用户全局（先加载，基础层） |
| 2 | `<project>/.goclaude/rules/` | 项目级（后加载，叠加层） |

### Rules 效果验证

```
> 写一个用户登录的函数

// 注意：以下输出已受到 rules 约束

package auth

import (
    "errors"
    "fmt"
)

var ErrInvalidCredentials = errors.New("invalid credentials")
var ErrUserNotFound = errors.New("user not found")

// Login authenticates a user with the given credentials.
// Returns a session token on success.
//
// Security: passwords are compared using constant-time comparison.
func Login(username, password string) (string, error) {
    // 函数名使用驼峰 ✓
    // 错误消息小写不加句号 ✓
    // 注释用英文 ✓
    ...
}

// 没有硬编码密钥 ✓
// 没有忽略 error ✓
```

---

## 实战：Memory — 持久化上下文

GoClaude 自动管理一个跨会话的记忆系统。AI 在对话中会将项目关键信息（数据库配置、代码约定、架构决策等）持久化到 `~/.goclaude/projects/<sanitized-path>/memory/` 目录，下次新会话中自动加载。

> Memory 不在项目根目录下——它存储在 GoClaude 用户数据目录中，按项目路径隔离，不会进入你的 Git 仓库。

### Memory 目录结构

```
~/.goclaude/projects/
└── <sanitized-cwd>/           # 项目路径的 sanitized 版本（如 _home_user_projects_myapp）
    └── memory/
        ├── MEMORY.md           # 入口索引（Memory.lines ≤200，≤25KB）
        └── *.md                # AI 创建的分片记忆文件
```

> 如需自定义路径，可通过 settings.json 中的 `autoMemoryDirectory` 字段覆盖完整目录。

### Memory 自动写入

AI 在对话过程中会自主调用 `update_memory` 工具，将重要信息写入记忆系统：

```
> 我们这个项目的数据库用的是 PostgreSQL 15，部署在 Docker 容器里，
> 连接字符串在环境变量 DATABASE_URL 里。监听端口固定 5432。

好的，我已了解项目配置。让我把这些信息记录下来。

⚙ 调用工具: update_memory
  action: create
  title: "Project Database Configuration"
  content: |-
    数据库: PostgreSQL 15
    部署方式: Docker 容器
    连接字符串: 环境变量 DATABASE_URL
    端口: 5432

[tokens: 28 in / 145 out | cost: $0.0011]
```

### Memory 自动查询

下次新会话中，GoClaude 启动时自动将 MEMORY.md 内容注入 System Prompt：

```
> 帮我写一个数据库迁移脚本

[启动时已加载 MEMORY.md]
根据项目配置，你在使用 PostgreSQL 15。这是我为你生成的迁移脚本...

⚙ 调用工具: file_write
  path: migrations/001_init.sql
```

### MEMORY.md 内容示例

```markdown
## Project Database Configuration
- 数据库: PostgreSQL 15
- 部署方式: Docker 容器
- 连接字符串: 环境变量 DATABASE_URL
- 端口: 5432

## Code Conventions
- 分支命名: feature/*, bugfix/*, release/*
- PR 需要至少 1 个 review approval
- 使用 squash merge
```

### 手动触发 Memory

Memory 除自动模式外，支持三种手动触发方式：

#### 方式一：自然语言（最常用）

直接在对话中告诉 AI 记住信息：

```
> 记住：我们这个项目的 API 统一用 /api/v1/ 前缀，所有响应都用 JSON
> 记住：部署用的是 Docker Compose，数据库在容器里
> 我们的 CI 用的是 GitHub Actions，配置文件在 .github/workflows/

好的，这些信息已保存到项目记忆中。下次新会话中我会自动加载这些上下文。
```

AI 收到指令后会调用 `file_write` / `file_edit` 工具更新记忆目录。

#### 方式二：`/remember` 命令（审查与整理）

在 REPL 中输入 `/remember`，AI 会扫描全部记忆层（MEMORY.md、CLAUDE.md），生成结构化清理报告：

```
> /remember

⚡ 已发送记忆审查指令...

# Memory Review

## 1. Promotions（建议提升到 CLAUDE.md）
- "API 使用 /api/v1/ 前缀" → CLAUDE.md（团队共享的规范）
- "CI 用 GitHub Actions" → CLAUDE.md（项目基础设施信息）

## 2. Cleanup（建议清理）
- "数据库密码 xxxxxx" → 移除（敏感信息不应存在记忆中）
- 重复条目 #42 和 #87 → 合并

## 3. Ambiguous（需确认）
- "分支命名 feature/*" — 个人偏好还是团队约定？

以上方案未修改任何文件，确认后我再执行。
```

> `/remember` 只读取不修改，用户确认后才执行写操作。

#### 方式三：禁用自动 Memory

```bash
# 环境变量禁用自动记忆
GOCLAUDE_DISABLE_AUTO_MEMORY=1 ./bin/goclaude

# 或 --bare 模式完全禁用
GOCLAUDE_SIMPLE=1 ./bin/goclaude
```

禁用后系统不再注入记忆提示，但 `/remember` 命令仍可手动使用。

### Memory 配置

在 `.goclaude.yaml` 中调整 Memory 参数：

```yaml
session:
  memory_file: MEMORY.md           # 入口文件名
  max_memory_lines: 200            # MEMORY.md 最大行数
  max_memory_bytes: 25000          # MEMORY.md 最大字节数
```

---

## 实战：Hook — 事件拦截与上下文注入

Hook 系统让用户和插件在 AI 对话的 7 个关键时刻注入逻辑：拦截工具调用、注入上下文字段、监听子代理生命周期、接收通知等。全部 Hook 从上游 TypeScript claude-code 完整复刻为 Go 惯用模式。

| 包 | 层级 | 对应源 | 说明 |
|---|---|---|---|
| `pkg/domain/hook/` | Domain | `src/utils/hooks/`、`src/hooks/` | 核心接口：Registry、Handler、事件类型、权限上下文、认证模型 |
| `pkg/util/hooks/` | Util | `src/hooks/useTimer`、`useDoublePress` 等 | 工具 Hook：计时器、双击检测、超时控制、闪烁、命令队列 |
| `pkg/application/hooks/` | Application | `src/hooks/useApiKey*`、`useAwaySummary` 等 | 编排 Hook：API 验证、会话管理、IDE 集成、通知系统、终端 |
| `pkg/interfaces/cli/hooks/` | Interface | `src/hooks/useBackgroundTaskNavigation` | CLI 特定 Hook：后台任务键盘导航 |

### Hook 事件类型

Hook 系统在 AI 对话生命周期的 7 个关键节点触发，分为**生命周期事件**和**拦截事件**两类：

#### 生命周期事件

| 事件 | 触发时机 | 可用操作 |
|---|---|---|
| `SessionStart` | 会话启动时 | 注入上下文引导、初始化外部服务 |
| `SessionEnd` | 会话结束时 | 清理资源、持久化日志 |
| `SubagentStart` | 子代理启动时 | 注入子代理专属上下文、注册自定义工具白名单 |
| `SubagentStop` | 子代理退出时（成功/失败/取消） | 记录执行结果、发送通知 |
| `UserPromptSubmit` | 用户提交输入后，AI 处理前 | 前置过滤、注入自动上下文、速率限制检查 |

#### 拦截事件

| 事件 | 触发时机 | 可用操作 |
|---|---|---|
| `PreToolUse` | 工具调用前 | **Block 阻止**、修改输入参数、安全审计 |
| `PostToolUse` | 工具执行完成后 | 注入额外消息到对话、自动格式化输出 |

### 方式一：YAML 配置声明式（推荐）

在 `~/.goclaude/config.yaml` 或 `<project>/.goclaude.yaml` 中添加 `hooks` 配置块：

```yaml
hooks:
  enabled: true
  async_timeout_ms: 30000              # 异步 hook 默认超时
  progress_interval_ms: 1000           # 进度回调间隔

  # 事件匹配规则
  hooks_config:
    matchers:
      # ── 会话生命周期 ──
      - event: SessionStart
        hook_name: session-init
        command: "echo '=== Session started at $(date) ==='"

      - event: SessionEnd
        hook_name: session-cleanup
        command: "python3 ~/.goclaude/hooks/cleanup.py --session=${SESSION_ID}"

      # ── PreToolUse 拦截 bash ──
      - event: PreToolUse
        tool_name: bash
        hook_name: pre-bash-audit
        command: "python3 ~/.goclaude/hooks/bash_audit.py"
        timeout: 10000

      # ── PostToolUse 格式化 ──
      - event: PostToolUse
        tool_name: file_write
        hook_name: auto-gofmt
        command: "gofmt -w {{.file_path}}"
        timeout: 5000

      # ── SubagentStart 注入上下文 ──
      - event: SubagentStart
        hook_name: subagent-context
        command: "cat ~/.goclaude/subagent_rules.md"

      # ── UserPromptSubmit 前缀处理 ──
      - event: UserPromptSubmit
        hook_name: auto-prefix
        command: "echo '请用 Go 惯用模式实现，使用中文回复'"
```

> Hook 命令通过 stdin 接收 JSON 格式的上下文信息（sessionId, agentType, toolName 等），stdout 输出 JSON 响应。详细协议参见 `hook.Context` 和 `hook.Result` 类型。

### 配置优先级

```
CLI flags (--hook-enabled / --hook-timeout)
        ↓
项目 .goclaude.yaml (hooks: ...)
        ↓
用户 ~/.goclaude/config.yaml (hooks: ...)
        ↓
configs/default.yaml (hooks: ...)
```

配置合并策略：标量**覆盖**、数组 **concat+dedup**、对象**深度合并**。

### Handler 执行保证

- **顺序执行**：同一事件的 handler 按注册顺序依次运行
- **Context 累积**：多个 handler 的 `AdditionalContexts` 合并注入
- **Block 短路**：任一 handler 返回 `Block: true`，立即停止后续 handler
- **错误隔离**：单个 handler panic 或返回 error 不影响其他 handler（记录日志并继续）
- **线程安全**：`sync.RWMutex` 保护 Registry，支持并发 subagent 读取

---

## CLI 命令速查

### REPL 命令速查

| 命令 | 说明 |
|------|------|
| `/help` `/` `/?` | 显示完整帮助 |
| `/exit` `/quit` `/q` | 退出 REPL |
| `/clear` `/reset` | 清空对话历史 |
| `/history` | 浏览历史输入 |
| `/messages` | 查看当前消息列表 |
| `/model [name]` | 查看或设置模型标识 |
| `/cost` `/usage` | Token 用量与费用 |
| `/permissions` | 权限模式（Shift+Tab 切换） |
| `/env` | 环境变量来源 |
| `/pwd` | 当前工作目录 |
| `/redraw` | 清屏重印 Banner |
| `/compact` | 上下文压缩状态 |
| `/skills [name]` | Skills 面板 / 详情 |
| `/remember` | 审查整理记忆（去重、提升、清理） |
| `/agents [type]` | Agents 面板 / 详情 |
| `/mcp [tools\|status]` | MCP 面板 / 工具 / 状态 |
| `/tools [name]` | 工具列表 / Schema |
| `/teams [name]` | 团队列表 / 详情 |

### 顶级 CLI 命令

| 命令 | 说明 |
|------|------|
| `goclaude` | 交互式 REPL |
| `goclaude chat [prompt]` | 单轮流式对话 |
| `goclaude run [prompt]` | 自主工具调用执行 |
| `goclaude doctor` | 环境诊断 |
| `goclaude version` | 版本信息 |
| `goclaude skills [list\|show]` | Skills 管理 |
| `goclaude agents [list\|show]` | Subagent 管理 |
| `goclaude mcp [list\|tools\|status]` | MCP 管理 |
| `goclaude team [create\|list\|show\|join\|send\|inbox\|delete]` | 团队管理 |

### 常用标志

| 标志 | 默认值 | 说明 |
|------|--------|------|
| `-p, --provider` | `deepseek` | Provider |
| `-m, --model` | `deepseek-chat` | 模型 |
| `-v, --verbose` | `false` | 详细日志 |
| `--env-file` | — | 额外 .env（可重复） |
| `--agent-teams` | `false` | 团队模式 |
| `--no-mcp` | `false` | 禁用 MCP |
| `--max-turns` | `20` | 最大工具循环数 |
| `--dangerously-skip-permissions` | `false` | 跳过权限 |

---

## 支持的模型

| Provider | 模型 | 说明 |
|----------|------|------|
| DeepSeek | `deepseek-chat` | V3 通用对话（默认） |
| DeepSeek | `deepseek-reasoner` | R1 推理模型（支持 thinking） |
| DeepSeek | `deepseek-coder` | 代码专项 |
| Anthropic | `claude-sonnet-4-20250514` | Claude Sonnet 4 |
| Anthropic | `claude-opus-4-20250514` | Claude Opus 4（待验证） |
| AWS Bedrock | — | 🔨 接口已定义，待完整实现 |
| GCP Vertex AI | — | 🔨 接口已定义，待完整实现 |

---

## 项目结构

```
goclaude/
├── cmd/goclaude/              # 应用入口 (main.go)
├── pkg/
│   ├── domain/                # 领域层 — 纯 Go，零外部依赖
│   │   ├── query/             #   查询引擎（消息循环、Token 预算、流式事件）
│   │   ├── tool/              #   工具系统（接口、注册表、并发执行器）
│   │   ├── command/           #   Slash 命令框架
│   │   ├── task/              #   任务生命周期管理
│   │   ├── mcp/               #   MCP 协议（JSON-RPC、传输层抽象）
│   │   ├── config/            #   5 层配置优先级合并
│   │   ├── permission/        #   权限系统（模式、规则匹配）
│   │   ├── agent/             #   Subagent 定义
│   │   ├── team/              #   多 Agent 团队协调
│   │   ├── skill/             #   Skills 系统
│   │   ├── rules/             #   规则加载
│   │   ├── hook/              #   Hook 生命周期（事件广播/异步注册/权限/通知/认证）
│   │   ├── memory/            #   记忆持久化
│   │   └── session/           #   会话管理
│   ├── application/           # 应用层 — 编排领域服务
│   │   ├── hooks/             #   Hook 实现（认证/会话/通知/权限/IDE/终端/导航）
│   │   ├── query_service.go   #   查询引擎驱动
│   │   ├── team_service.go    #   多 Agent 团队管理（NLP 路由）
│   │   ├── agent_service.go   #   Agent 执行工厂
│   │   ├── mcp_service.go     #   MCP 连接管理
│   │   ├── skill_service.go   #   Skills 加载与条件激活
│   │   └── ...
│   ├── infrastructure/        # 基础设施层 — 实现领域接口
│   │   ├── api/               #   API Provider 实现
│   │   │   ├── anthropic/     #      Anthropic SSE 流式
│   │   │   ├── deepseek/      #      DeepSeek OpenAI 兼容
│   │   │   ├── bedrock/       #      AWS Bedrock
│   │   │   └── vertex/        #      GCP Vertex AI
│   │   ├── filesystem/        #   文件系统（读写/glob/ripgrep）
│   │   ├── shell/             #   Shell 命令执行
│   │   ├── sandbox/           #   命令沙箱（bwrap / sandbox-exec）
│   │   ├── mcp/               #   MCP 传输实现（stdio/HTTP/SSE/WS）
│   │   └── ...
│   ├── tools/                  # 工具具体实现（实现 domain/tool 接口）
│   └── util/                    # 内部公共工具库
│       ├── hooks/              #   工具 Hook（计时/双击/闪烁/队列/剪贴板）
│       ├── dotenv/             #   .env 文件解析器
│       ├── frontmatter/        #   Markdown YAML frontmatter 提取
│       ├── settingsenv/        #   settings.json env 桥接
│       └── wsclient/           #   WebSocket 客户端
├── tests/                        # 集成与 E2E 测试
│   ├── integration/            #   Go 集成测试
│   │   └── e2e_integration_test.go
│   └── e2e/                    #   E2E Shell 测试脚本
│       └── run_e2e.sh
├── configs/                   # 默认配置文件
│   └── default.yaml           #   单一信息源
├── docs/                      # 设计文档
├── examples/                  # 使用示例
├── Makefile                   # 构建自动化
└── go.mod                     # Go 模块定义
```

### 架构概览

```
interfaces → application → domain ← infrastructure
```

GoClaude 遵循严格的 **DDD 四层架构**，核心设计原则：

- **依赖倒置（DIP）**：领域层只定义接口，基础设施层提供实现。例如 `query.AIProvider` 接口由 `anthropic.Client` 和 `deepseek.Client` 实现。
- **并发模型**：`context.Context` 级联取消、`goroutine + channel` 流式响应、`errgroup` worker pool 并发工具调度、`sync.RWMutex` 保护共享状态。
- **工具调度策略**：只读工具（file_read、glob、grep）并发执行（最大 10 路），写入工具（bash、file_write、file_edit）串行执行。


---

## 许可证

本项目采用 **Apache License 2.0** 开源许可证。详见 [LICENSE](LICENSE) 文件。
