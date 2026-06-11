<div align="center">

# GoClaude

**基于 DDD 四层架构的 Golang 终端 AI 编程助手**

[![Go](https://img.shields.io/badge/Go-%E2%89%A5%201.22-00ADD8?style=flat-square&logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue?style=flat-square)](LICENSE)
[![Architecture](https://img.shields.io/badge/Architecture-DDD%204--Layer-blueviolet?style=flat-square)](#架构与运行机制)


</div>

---

> **GoClaude** 把 **多 Provider 流式对话、工具调用、MCP 扩展、Skills、Subagent、Agent Teams、Memory、Hook 与沙箱权限控制** 收束到同一个终端运行时，适合在本地项目里完成编码、审查、重构与自动化任务。

<br>

<p align="center">
  <img src="docs/assets/readme-capability-map.svg" alt="GoClaude 能力地图" width="100%">
</p>

<br>

## 📖 阅读路线

| 你想了解什么？ | 推荐阅读 |
|:---|:---|
| 5 分钟跑起来 | [快速开始](#-快速开始) → [安装与构建](#-安装与构建) |
| 看整体能力 | [功能特性](#-功能特性) → [架构与运行机制](#-架构与运行机制) |
| 接入外部能力 | [配置说明](#-配置说明) → [MCP 连接外部工具](#-实战mcp--连接外部工具) |
| 组织复杂任务 | [Subagent 子任务执行](#-实战subagent--派生子-agent-执行子任务) → [Agent Teams 协作](#-实战agent-teams--多-agent-团队协作) |
| 查命令与模型 | [CLI 命令速查](#-cli-命令速查) → [支持的模型](#-支持的模型) |

---

## 📑 目录

<details>
<summary><strong>点击展开完整目录</strong></summary>

- [项目简介](#-项目简介)
- [功能特性](#-功能特性)
- [系统环境要求](#-系统环境要求)
- [快速开始](#-快速开始)
- [安装与构建](#-安装与构建)
- [配置说明](#-配置说明)
- [使用示例：REPL 基础](#-使用示例repl-基础)
- [实战：Skill — 定义专业能力](#-实战skill--定义专业能力)
- [实战：MCP — 连接外部工具](#-实战mcp--连接外部工具)
- [实战：Subagent — 派生子 Agent 执行子任务](#-实战subagent--派生子-agent-执行子任务)
- [实战：Agent Teams — 多 Agent 团队协作](#-实战agent-teams--多-agent-团队协作)
- [实战：Rules — 行为约束规则](#-实战rules--行为约束规则)
- [实战：Memory — 持久化上下文](#-实战memory--持久化上下文)
- [实战：Hook — 事件拦截与上下文注入](#-实战hook--事件拦截与上下文注入)
- [CLI 命令速查](#-cli-命令速查)
- [支持的模型](#-支持的模型)
- [项目结构](#-项目结构)
- [架构与运行机制](#-架构与运行机制)
- [许可证](#-许可证)

</details>

---

## 💡 项目简介

GoClaude 是一个运行在终端中的 AI 编程助手。它通过 **Query Engine** 管理多轮消息循环，让模型在需要时自主调用工具、加载 Skill、连接 MCP 服务器或派生 Subagent，并把执行结果回填到上下文继续推理。

### 适用场景

| 场景 | 典型工作流 |
|:---|:---|
| **日常编码辅助** | 在 REPL 中讨论代码、生成实现、解释报错 |
| **代码审查与重构** | 读取项目代码 → 给出方案 → 执行编辑 → 验证结果 |
| **自动化任务** | 通过 `run` 命令让 AI 自主调用工具完成一次性任务 |
| **多 Agent 协作** | Leader 拆分任务，多个 Worker 并行执行并回流结果 |
| **私有模型接入** | 通过 Provider Base URL 对接内部代理或私有模型服务 |

---

## ⚡ 功能特性

### 核心能力

| 能力域 | 说明 |
|:---|:---|
| **模型接入** | Anthropic（SSE 流式）、DeepSeek（OpenAI 兼容）、AWS Bedrock、GCP Vertex AI |
| **工具系统** | `file_read` / `file_write` / `file_edit`、`bash`、`glob`、`grep`、`agent`、`skill`、`mcp`、`team` 等内置工具 |
| **MCP 协议** | stdio / HTTP / SSE / WebSocket 四种传输，支持 JSON-RPC 动态工具注册 |
| **Skills 系统** | 多来源按需加载，条件激活，可扩展自定义专业能力 |
| **多 Agent 协作** | Coordinator + Worker 团队模式，支持消息传递、任务分配与结果汇总 |
| **权限与沙箱** | `default` / `acceptEdits` / `plan` / `bypass` 四种模式；Linux `bwrap`，macOS `sandbox-exec` |
| **REPL / TUI** | 行编辑、Markdown 渲染、语法高亮、Tab 补全、对话显示、`/remember` 记忆审查 |
| **上下文管理** | Token 预算、自动压缩、Memory 注入、Hook 生命周期扩展 |

### 模块状态

| 模块 | 状态 | 说明 |
|:---|:---:|:---|
| 查询引擎 | ✅ | 消息循环、流式处理、Token 预算、自动压缩 |
| 工具系统 | ✅ | 注册表、并发执行器、权限检查 |
| MCP 协议 | ✅ | stdio / HTTP / SSE / WebSocket 传输，JSON-RPC 客户端 |
| Skills / Subagent | ✅ | 多来源加载、注册表、条件激活、隔离执行 |
| Agent Teams | ✅ | Coordinator + Worker 模式、消息传递、任务分配 |
| Hook / Memory | ✅ | 事件钩子、上下文注入、跨会话记忆 |
| TUI / Slash 命令 | ✅ | bubbletea REPL、消息渲染、命令面板 |
| 配置 / 权限 / 沙箱 | ✅ | 分层配置、权限模式、路径与网络限制 |

---

## 🖥️ 系统环境要求

**必需依赖：**

| 类型 | 依赖 | 说明 |
|:---|:---|:---|
| 必需 | Go ≥ 1.22.0 | 编译与运行 |
| 必需 | Git ≥ 2.0 | 版本信息注入 |

**可选依赖：**

| 工具 | 用途 |
|:---|:---|
| `golangci-lint` | Lint 静态检查 |
| `ripgrep` (`rg`) | grep 加速，未安装时退化为纯 Go 实现 |
| `bwrap` | Linux 沙箱支持（macOS 使用内置 `sandbox-exec`） |
| `expect` / `python3` | E2E 测试与 MCP echo server 示例 |

**平台支持：**

| 操作系统 | 架构 | 状态 |
|:---|:---|:---:|
| Linux | amd64 / arm64 | ✅ 全功能支持 |
| macOS | amd64 / arm64 | ✅ 全功能支持 |
| Windows | amd64 | 🔨 编译通过，部分功能待验证 |

---

## 🚀 快速开始

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

> **提示** — 如遇问题，运行 `goclaude doctor` 可快速诊断环境配置。

---

## 🔧 安装与构建

### 从源码构建

```bash
git clone https://github.com/yaoice/goclaude.git
cd goclaude

# 下载依赖
make deps

# 构建（输出到 ./bin/goclaude）
make build
```

### 交叉编译

```bash
make build-linux-amd64       # Linux amd64
make build-linux-arm64       # Linux arm64
make build-darwin-amd64      # macOS amd64
make build-darwin-arm64      # macOS arm64 (Apple Silicon)
make build-windows-amd64     # Windows amd64
make build-all               # 所有平台
```

> 产物输出到 `./bin/<os>_<arch>/goclaude`（Windows 为 `goclaude.exe`）。

### 安装到系统路径

```bash
make install                 # 安装到 $GOPATH/bin
# 或
sudo cp ./bin/goclaude /usr/local/bin/
```

### 一键开发流程

```bash
# 格式化 → 静态检查 → Lint → 测试 → 构建
make all
```

### 配置 API Key

GoClaude 通过环境变量读取 API Key。创建 `.env` 文件（参考 `.env.example`）：

```bash
# .env
DEEPSEEK_API_KEY=sk-your-deepseek-api-key
# ANTHROPIC_API_KEY=sk-ant-your-anthropic-api-key
```

**环境变量加载优先级**（从高到低）：

| 优先级 | 来源 | 说明 |
|:---:|:---|:---|
| 0 | 进程环境变量 | `export` / shell 注入，最高优先 |
| 1 | `--env-file <path>` | CLI 显式指定，可重复使用 |
| 2 | `./.env.local` | 本地开发覆盖（建议加入 `.gitignore`） |
| 3 | `./.env` | 当前目录 |
| 4 | 父目录 `.env` | 向上查找到的最近 `.env` 文件 |
| 5 | `~/.claude/.env` | 用户全局 |

> **诊断命令** — `goclaude doctor` 可检查实际加载的 `.env` 文件路径与变量名。

---

## ⚙️ 配置说明

### 配置层级

GoClaude 支持多层配置合并。运行时先完成 `.env`、`settings.json` 与 YAML 配置加载，再注入 `AppConfig`，由 CLI / REPL 装配到 Query Engine、Tool Executor、MCP、Hook 与沙箱模块。

<p align="center">
  <img src="docs/assets/readme-config-hooks-flow.svg" alt="配置、权限与 Hook 流程" width="100%">
</p>

| 优先级 | 来源 | 路径 | 说明 |
|:---:|:---|:---|:---|
| 1 | CLI 参数 | `--max-turns`、`--no-mcp` 等 | 一次性临时覆盖，最高优先 |
| 2 | 项目级配置 | `<project>/.goclaude.yaml` | 团队共享或项目特定 |
| 3 | 用户级配置 | `~/.goclaude/config.yaml` | 个人偏好 |
| 4 | 内置默认 | `configs/default.yaml` | 与二进制一同发布 |

### 默认配置

<details>
<summary><code>configs/default.yaml</code></summary>

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

</details>

### 项目级配置示例

```yaml
# .goclaude.yaml
api:
  provider: anthropic
  model: claude-sonnet-4-20250514
  temperature: 0.7

engine:
  max_turns: 50

permissions:
  mode: acceptEdits                    # 自动放行编辑，无需逐次确认
```

### 自定义 Provider Base URL

```yaml
# ~/.goclaude/config.yaml
providers:
  deepseek:
    base_url: https://your-proxy.example.com
    timeout: 300s
    max_retries: 3
```

---

## 🎯 使用示例：REPL 基础

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

### 2. CLI 单轮模式

```bash
# 快速问答
./bin/goclaude chat "用一句话解释 DDD"

# 指定模型
./bin/goclaude chat -p deepseek -m deepseek-reasoner "写一个快速排序"

# 自主工具调用
./bin/goclaude run "帮我列出 src 目录下的 .go 文件并计数"
```

### 3. 环境诊断

```bash
./bin/goclaude doctor
# ✓ Go 1.22
# ✓ Git 2.39
# ✗ bwrap 未安装（沙箱降级为直接执行）
# ✓ DEEPSEEK_API_KEY 已配置
# ✓ ripgrep 13.0
```

---

## 🧩 实战：Skill — 定义专业能力

> **Skill** 是一组由 Markdown 描述的专业提示，可被 LLM 在合适场景下按需加载。

### 定义 Skill

创建 `.claude/skills/api-reviewer/SKILL.md`：

```markdown
---
name: api-reviewer
description: Review REST API design and suggest improvements
whenToUse: When the user asks for API review or design feedback
---

You are an API design reviewer. When reviewing API code:
- Follow REST best practices (resource naming, HTTP verbs, status codes)
- Check error handling and idempotency
- Suggest pagination, filtering, and sorting strategies
- Point out security concerns (auth, rate limit, input validation)
```

### 条件激活

通过 frontmatter 声明 `whenToUse`，模型在匹配该描述时会主动调用 `Skill` 工具加载正文。配合 `SkillService` 的条件路径（`globs` / `paths`），可在文件被读取时自动注入到上下文。

### 终端中触发 Skill

**方式一：自然语言触发**

```
> 请审查我刚写的 API 设计

⚙ 调用工具: Skill
  name: api-reviewer

Skill: api-reviewer
You are an API design reviewer. When reviewing API code...
```

**方式二：Slash 命令查看**

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

### Skill 加载目录优先级

| 优先级 | 路径 | 说明 |
|:---:|:---|:---|
| 最高 | `~/.goclaude/skills/` | 用户全局 |
| ↓ | `~/.claude/skills/` | legacy 兜底 |
| ↓ | 逐级向上 `.goclaude/skills/` | 项目级（从 CWD 向上 16 层） |
| 最低 | 逐级向上 `.claude/skills/` | 项目级 legacy 兜底 |

---

## 🔌 实战：MCP — 连接外部工具

> **MCP**（Model Context Protocol）让 GoClaude 接入 stdio / HTTP / SSE / WebSocket 四种外部工具服务器，工具可像内置工具一样被模型调用。

### 配置 MCP 服务器

创建 `.goclaude/.mcp.json`：

```json
{
  "mcpServers": {
    "postgres": {
      "type": "stdio",
      "command": "mcp-postgres",
      "args": ["--config", "/etc/mcp/pg.yaml"]
    },
    "github": {
      "type": "http",
      "url": "https://mcp.github.com/v1",
      "headers": { "Authorization": "Bearer ${GITHUB_TOKEN}" }
    },
    "realtime": {
      "type": "sse",
      "url": "https://stream.example.com/sse"
    }
  }
}
```

### 终端中触发 MCP 工具

**查看连接状态：**

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

**列出 MCP 工具：**

```
> 列出所有可用的 MCP 工具

可用 MCP 工具：
  - mcp__postgres__query         执行 SQL 查询
  - mcp__postgres__list_tables
  - mcp__github__search_repos
  - mcp__github__create_issue
```

**自然语言触发 MCP 工具：**

```
> 查询 users 表最近注册的 10 个用户

⚙ 调用工具: mcp__postgres__query
  query: SELECT id, email, created_at FROM users ORDER BY created_at DESC LIMIT 10

result: [
  {"id": 142, "email": "alice@example.com", "created_at": "2026-06-09"},
  ...
]
```

---

## 🤖 实战：Subagent — 派生子 Agent 执行子任务

> **Subagent** 在独立上下文里执行单个子任务，返回结果后由主 Agent 继续。

### 定义自定义 Subagent

创建 `.claude/agents/echo-bot.md`：

```markdown
---
name: echo-bot
description: When you want a tiny isolated agent that simply echoes back a payload prefixed with [ECHO]
tools: []
model: deepseek-chat
---

You are a minimal echo agent. Whatever input you receive, reply with one line:
  [ECHO] <the user's input verbatim>
Do not call any tools. Do not add commentary. Just the line above.
```

### 终端中触发 Subagent

**自然语言触发：**

```
> 用 echo-bot 复述这段话：hello world

⚙ 调用工具: Agent
  subagent_type: echo-bot
  prompt: hello world

[ECHO] hello world
```

**CLI 管理：**

```bash
./bin/goclaude agents list          # 列出所有 Agent
./bin/goclaude agents show echo-bot # 查看某个 Agent 详情
```

### Agent 加载目录

| 优先级 | 路径 | 说明 |
|:---:|:---|:---|
| 最高 | `~/.goclaude/agents/` | 用户全局 |
| ↓ | `~/.claude/agents/` | legacy 兜底 |
| ↓ | 逐级向上 `.goclaude/agents/` | 项目级（从 CWD 向上 16 层） |
| 最低 | 逐级向上 `.claude/agents/` | 项目级 legacy 兜底 |

---

## 👥 实战：Agent Teams — 多 Agent 团队协作

> 启用 `--agent-teams` 后，GoClaude 切换到 **Coordinator + Worker** 模式：Leader Agent 可将复杂任务拆解分配给多个 Worker，并行执行、自动汇总。

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

所有三个模块已并行完成。文件清单：
  cmd/server/main.go        — HTTP 入口
  internal/handler/auth.go  — 注册/登录 API
  internal/middleware/jwt.go — JWT 认证中间件
  go.mod / go.sum            — 依赖管理

[tokens: 312 in / 8,421 out | cost: $0.0580 | team: backend-init | workers: 3]
```

### CLI 团队管理

```bash
./bin/goclaude team create my-team                           # 创建团队
./bin/goclaude team list                                     # 列出所有团队
./bin/goclaude team join my-team worker-1                    # Agent 加入团队
./bin/goclaude team send worker-1 "请处理 auth 模块的单元测试"  # 发送消息
./bin/goclaude team inbox worker-1                           # 查看收件箱
./bin/goclaude team delete my-team --force                   # 强制删除
```

### 团队消息流

<p align="center">
  <img src="docs/assets/readme-team-workflow.svg" alt="Agent Teams 协作流程" width="100%">
</p>

> **核心闭环**：Leader 创建团队并拆分任务 → Worker 并行执行后通过 Mailbox 回传进展 → Leader 每轮自动读取 inbox → 将关键输出注入上下文 → 生成汇总、复核或二次分配。

---

## 📏 实战：Rules — 行为约束规则

> **Rules** 是一组持久化的行为指令，GoClaude 启动时自动加载并注入到 System Prompt，全程影响 AI 的决策。

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
|:---:|:---|:---|
| 1 | `~/.goclaude/rules/` | 用户全局（先加载，基础层） |
| 2 | `<project>/.goclaude/rules/` | 项目级（后加载，叠加层） |

### Rules 效果验证

```go
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

## 🧠 实战：Memory — 持久化上下文

> GoClaude 自动管理一个跨会话的记忆系统。AI 在对话中会将项目关键信息持久化到用户数据目录，下次新会话中自动加载。

> [!NOTE]
> Memory 不在项目根目录下——它存储在 `~/.goclaude/projects/<sanitized-path>/memory/` 中，按项目路径隔离，**不会进入你的 Git 仓库**。

### Memory 目录结构

```
~/.goclaude/projects/
└── <sanitized-cwd>/               # 项目路径的 sanitized 版本
    └── memory/
        ├── MEMORY.md               # 入口索引（≤200 行，≤25KB）
        └── *.md                    # AI 创建的分片记忆文件
```

> 如需自定义路径，可通过 `settings.json` 中的 `autoMemoryDirectory` 字段覆盖。

### Memory 自动写入

AI 在对话过程中会自主调用 `update_memory` 工具：

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

下次新会话中，GoClaude 启动时自动将 `MEMORY.md` 内容注入 System Prompt：

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

| 方式 | 说明 |
|:---|:---|
| **自然语言** | 直接在对话中告诉 AI「记住：...」，AI 调用工具更新记忆目录 |
| **`/remember` 命令** | AI 扫描全部记忆层，生成结构化清理报告 |
| **直接编辑文件** | 编辑 `~/.goclaude/projects/<sanitized-cwd>/memory/MEMORY.md`，下次启动时重新加载 |

### 长期记忆（可选）

默认使用 `MEMORY.md`；如需更强的全文检索和项目级知识管理，可启用长期记忆：

```yaml
# ~/.goclaude/config.yaml
long_term_memory:
  enabled: true
  db_path: ~/.goclaude/memory.db
  capacity:
    max_entries: 10000
    max_age_days: 180
```

启动后 GoClaude 会自动：

1. 在每次工具调用、Agent 切换时把关键观察写入 SQLite FTS5 索引
2. 在下次会话开始时把 `<long-term-memory>` 块注入首条 user message
3. 通过 Hook 生命周期自动清理过期条目

### Memory 设计原则

| 原则 | 说明 |
|:---|:---|
| **私密性** | 存在用户级目录，不会进入 Git 仓库 |
| **可移植** | 跨机器迁移只需拷贝 `~/.goclaude/` 目录 |
| **可审查** | 所有写入都可追溯到具体的对话轮次 |
| **可清理** | `/remember` 命令提供可视化的审查与整理 |

---

## 🪝 实战：Hook — 事件拦截与上下文注入

> **Hook** 是一组在特定事件触发时执行的命令或脚本，支持拦截、注入、通知和清理四大能力。

### 事件类型

GoClaude 提供 **7 种**内置事件，覆盖完整生命周期：

| 事件 | 触发时机 | 典型用途 |
|:---|:---|:---|
| `SessionStart` | REPL / CLI 启动 | 加载环境、初始化状态 |
| `SessionEnd` | REPL / CLI 退出 | 清理临时资源、上报统计 |
| `UserPromptSubmit` | 用户提交 prompt | 前缀处理、敏感词过滤 |
| `PreToolUse` | 工具调用前 | 参数校验、权限审计 |
| `PostToolUse` | 工具调用后 | 自动格式化、日志归档 |
| `SubagentStart` | Subagent 启动 | 注入上下文、规则 |
| `SubagentStop` | Subagent 结束 | 结果记录、通知 |

### Hook 配置

```yaml
hooks:
  enabled: true
  async_timeout_ms: 30000
  progress_interval_ms: 1000

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

> [!TIP]
> Hook 命令通过 **stdin** 接收 JSON 格式的上下文信息（`sessionId`, `agentType`, `toolName` 等），**stdout** 输出 JSON 响应。详细协议参见 `hook.Context` 和 `hook.Result` 类型。

### 配置合并与执行流程

<p align="center">
  <img src="docs/assets/readme-config-hooks-flow.svg" alt="配置、权限与 Hook 流程" width="100%">
</p>

> **合并策略**：标量 → 覆盖 | 数组 → concat + dedup | 对象 → 深度合并

### Handler 执行保证

| 保证 | 说明 |
|:---|:---|
| **顺序执行** | 同一事件的 handler 按注册顺序依次运行 |
| **Context 累积** | 多个 handler 的 `AdditionalContexts` 合并注入 |
| **Block 短路** | 任一 handler 返回 `Block: true`，立即停止后续 handler |
| **错误隔离** | 单个 handler panic 或返回 error 不影响其他 handler |
| **线程安全** | `sync.RWMutex` 保护 Registry，支持并发 subagent 读取 |

---

## 📋 CLI 命令速查

### REPL Slash 命令

| 命令 | 说明 |
|:---|:---|
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
| `/remember` | 审查整理记忆 |
| `/agents [type]` | Agents 面板 / 详情 |
| `/mcp [tools\|status]` | MCP 面板 / 工具 / 状态 |
| `/tools [name]` | 工具列表 / Schema |
| `/teams [name]` | 团队列表 / 详情 |

### 顶级 CLI 命令

| 命令 | 说明 |
|:---|:---|
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
|:---|:---:|:---|
| `-p, --provider` | `deepseek` | Provider |
| `-m, --model` | `deepseek-chat` | 模型 |
| `-v, --verbose` | `false` | 详细日志 |
| `--env-file` | — | 额外 `.env`（可重复） |
| `--agent-teams` | `false` | 团队模式 |
| `--no-mcp` | `false` | 禁用 MCP |
| `--max-turns` | `20` | 最大工具循环数 |
| `--dangerously-skip-permissions` | `false` | 跳过权限 |

---

## 🧬 支持的模型

| Provider | 模型 | 说明 |
|:---|:---|:---|
| DeepSeek | `deepseek-chat` | V3 通用对话（默认） |
| DeepSeek | `deepseek-reasoner` | R1 推理模型（支持 thinking） |
| DeepSeek | `deepseek-coder` | 代码专项 |
| Anthropic | `claude-sonnet-4-20250514` | Claude Sonnet 4 |
| Anthropic | `claude-opus-4-20250514` | Claude Opus 4（待验证） |
| AWS Bedrock | — | 🔨 接口已定义，待完整实现 |
| GCP Vertex AI | — | 🔨 接口已定义，待完整实现 |

---

## 📁 项目结构

> 项目目录是 DDD 分层的物理映射。核心目录按「入口 → 用例编排 → 领域核心 → 基础设施实现 → 工具适配」组织。

| 路径 | 角色 | 重点内容 |
|:---|:---|:---|
| `cmd/goclaude/` | 进程入口 | 环境加载、配置初始化、信号处理、Cobra 根命令启动 |
| `pkg/interfaces/` | 交互入口层 | CLI、REPL、TUI、Slash 命令、运行时适配器 |
| `pkg/application/` | 应用编排层 | Skill、Agent、MCP、Team、Workflow、Memory、Hook 服务 |
| `pkg/domain/` | 领域核心层 | Query Engine、Tool Executor、权限模型、消息与事件模型 |
| `pkg/infrastructure/` | 基础设施层 | Provider、MCP 传输、文件系统、Shell、沙箱、持久化 |
| `pkg/tools/` | 工具适配层 | file、bash、grep、glob、skill、mcp、agent、team 等工具实现 |
| `pkg/util/` | 通用支持 | `.env`、frontmatter、settings env、WebSocket 等内部工具 |
| `configs/` | 默认配置 | `default.yaml` 是参数类配置的默认来源 |
| `tests/` | 验证体系 | Go 集成测试与 Shell E2E 场景 |

**模块之间的依赖关系：**

<p align="center">
  <img src="docs/assets/readme-module-map.svg" alt="GoClaude 模块关系图" width="100%">
</p>

---

## 🏛️ 架构与运行机制

GoClaude 遵循严格的 **DDD 四层架构**：

- **`interfaces`** — CLI / REPL / TUI 入口与运行时装配
- **`application`** — 编排 Skill、Agent、MCP、Team、Memory 等用例服务
- **`domain`** — 查询引擎、工具模型、权限模型等核心抽象
- **`infrastructure`** — Provider、文件系统、MCP 传输、沙箱、持久化等具体实现

额外的 `pkg/tools` 作为**工具适配层**，将本机能力、MCP、Agent、Team 等统一适配为 `domain/tool.Tool`。

### 完整应用分层架构

<p align="center">
  <img src="docs/assets/readme-layered-application-architecture.svg" alt="GoClaude 完整应用分层架构" width="100%">
</p>

### 系统总览

<p align="center">
  <img src="docs/assets/readme-system-overview.svg" alt="GoClaude 系统架构总览" width="100%">
</p>

### 核心数据流

<p align="center">
  <img src="docs/assets/readme-query-dataflow.svg" alt="Query Engine 数据流" width="100%">
</p>

### 运行机制说明

1. **启动阶段** — `cmd/goclaude/main.go` 加载环境变量、`.env` 链、`settings.json` 与 YAML 配置，创建根 `context` 并进入 Cobra 命令树
2. **路径分流** — `chat` 走轻量 Provider 验证路径；REPL / `run` 走完整 `Query Engine`，装配工具、Skills、MCP、Subagent、Team、Memory 与 Hook
3. **服务装配** — `buildAppContext` 集中加载扩展源并构建 `Tool Registry`；本地与 MCP 工具统一暴露为工具定义，随 `StreamParams` 发送给模型
4. **核心循环** — Query Engine 以「消息组装 → Provider 流式调用 → 事件转发 → 工具执行 → tool_result 回填」的循环运行，直到无 `tool_use` / 达到 `max_turns` / `context` 取消
5. **工具调度** — Tool Executor 先做输入校验与权限检查，只读工具并行执行，写入类操作串行执行
6. **扩展机制** — 自动压缩、条件 Skill 激活、Team leader inbox 注入、长期记忆 Hook 等均通过引擎配置或回调接入

### 设计原则

| 原则 | 说明 |
|:---|:---|
| **依赖倒置（DIP）** | 领域层只定义接口，基础设施层提供实现。`query.AIProvider` 由 `anthropic.Client` / `deepseek.Client` 实现 |
| **并发模型** | `context.Context` 级联取消、`goroutine + channel` 流式响应、`errgroup` worker pool、`sync.RWMutex` 保护共享状态 |
| **安全默认值** | API Key 只走环境变量；工具调用先校验与授权；写入类操作串行；Shell 受沙箱约束 |
| **扩展优先** | Skills、MCP、Subagent、Team、Hook、Memory 都通过服务或回调接入，避免污染领域层核心循环 |

---

## 📄 许可证

本项目采用 **Apache License 2.0** 开源许可证。

详见 [LICENSE](LICENSE) 文件。
