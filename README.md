# GoClaude

基于 DDD（领域驱动设计）架构的 Golang 终端 AI 编程助手，是上游 TypeScript claude-code 的 Go 重写版本。支持多 AI Provider 流式对话、工具调用、MCP 协议集成与多 Agent 团队协作。

## 项目简介

### 主要功能

GoClaude 是一个运行在终端中的 AI 编程助手，具备以下核心能力：

- **AI 对话**：与大模型进行流式对话，支持单轮快速问答和多轮交互式会话
- **代码操作**：通过内置工具直接读写文件、执行命令、搜索代码，AI 可自主完成编码任务
- **MCP 协议**：连接外部 MCP 服务器，动态扩展工具能力
- **多 Agent 协作**：将复杂任务分解给多个 Agent 并行处理

### 核心特性

| 特性 | 说明 |
|------|------|
| 多 Provider | Anthropic（SSE 流式）、DeepSeek（OpenAI 兼容）、AWS Bedrock、GCP Vertex AI |
| 工具系统 | file_read/write/edit、bash、glob、grep、agent、skill、mcp、team 等 11+ 种 |
| MCP 协议 | stdio / HTTP / SSE / WebSocket 四种传输，JSON-RPC 动态工具注册 |
| 多 Agent | Coordinator + Worker 团队模式，NLP 路由，消息传递与任务分配 |
| Skills | 多来源按需加载，条件激活 |
| 沙箱 | Linux（bwrap）/ macOS（sandbox-exec），限制文件系统与网络访问 |
| 交互式 REPL | 行编辑、Markdown 渲染、语法高亮、Tab 补全 |
| 权限控制 | default / acceptEdits / plan / bypass 四种模式 |
| 自动压缩 | 上下文超出 Token 预算时自动摘要/截断 |

### 适用场景

- **日常编码辅助**：在终端中直接与 AI 讨论代码、让 AI 帮助编写和修改文件
- **代码审查与重构**：AI 读取项目代码后给出重构建议并直接实施
- **自动化脚本编写**：通过 `run` 命令让 AI 自主调用工具完成复杂任务
- **多人/多 Agent 协作**：复杂项目分解为子任务，由团队并行执行
- **私有部署**：支持自定义 API Base URL，可对接内部代理或私有模型服务

## 使用示例

### 安装与配置

#### 环境要求

| 依赖 | 版本 | 必需 | 说明 |
|------|------|------|------|
| Go | ≥ 1.22.0 | 是 | 编译运行 |
| golangci-lint | 最新 | 否 | Lint 检查 |
| ripgrep (`rg`) | 任意 | 否 | grep 加速，无则退化为纯 Go |
| bwrap | 任意 | 否 | Linux 沙箱（macOS 自带 sandbox-exec） |

#### 第一步：克隆与编译

```console
$ git clone https://github.com/yaoice/goclaude.git
Cloning into 'goclaude'...
remote: Enumerating objects: 1024, done.
remote: Total 1024 (delta 0), reused 0 (delta 0)
Receiving objects: 100% (1024/1024), 2.1 MiB | 5.32 MiB/s, done.

$ cd goclaude

$ make deps
>>> 下载依赖...
go: downloading github.com/charmbracelet/bubbletea v1.2.4
go: downloading github.com/spf13/cobra v1.8.1
go: downloading gopkg.in/yaml.v3 v3.0.1

$ make build
>>> 构建 goclaude...
go build -ldflags "-X main.version=v0.1.0-a3f2c1d" -o ./bin/goclaude ./cmd/goclaude/
```

#### 第二步：配置 API Key

```console
$ cp .env.example .env
$ cat .env
# DeepSeek API（OpenAI 兼容协议）
DEEPSEEK_API_KEY=sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx

# Anthropic API
# ANTHROPIC_API_KEY=sk-ant-xxxxxxxxxxxxxxxx
```

编辑 `.env`，将 `sk-xxxx...` 替换为实际的 API Key。

#### 第三步：验证环境

```console
$ ./bin/goclaude doctor
╭─────────────────────────────────────╮
│        GoClaude Doctor              │
╰─────────────────────────────────────╯

检查环境配置...

  ✓ Go version       1.22.4
  ✓ Config loaded    configs/default.yaml
  ✓ Provider         deepseek (deepseek-chat)
  ✓ API Key          DEEPSEEK_API_KEY ✓ (set)
  ✓ .env files       ./.env
  ✓ Sandbox          bwrap available
  ✓ ripgrep          rg 14.1.0

所有检查通过。
```

---

### 基础用法

#### 1. 交互式 REPL（最常用）

```console
$ ./bin/goclaude
╭─────────────────────────────────────╮
│  GoClaude v0.1.0  (deepseek-chat)   │
│  Type /help for commands, Ctrl+D    │
│  to exit                            │
╰─────────────────────────────────────╯

> 用 Go 写一个最简单的 HTTP 服务器

以下是一个监听 8080 端口的最简 HTTP 服务器：

  package main

  import (
      "fmt"
      "net/http"
  )

  func main() {
      http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
          fmt.Fprintf(w, "Hello, World!")
      })
      http.ListenAndServe(":8080", nil)
  }

运行 `go run main.go` 后访问 http://localhost:8080 即可看到输出。

[tokens: 156 in / 203 out | cost: $0.0012]

> /help
可用命令:
  /help       显示此帮助信息
  /clear      清空对话历史
  /compact    手动压缩上下文
  /model      切换模型
  /status     显示当前会话状态

> /clear
对话历史已清空。

> (Ctrl+D)
再见！
```

#### 2. 单轮快速对话

```console
$ ./bin/goclaude chat "用一句话解释什么是依赖注入"
依赖注入是一种设计模式，将组件所需的依赖从外部传入而非内部创建，从而实现
解耦和可测试性。

[tokens: 24 in / 38 out | cost: $0.0003]
```

#### 3. 指定模型与 Provider

```console
$ ./bin/goclaude chat -m deepseek-reasoner "1+1为什么等于2"
<thinking>
这是一个关于数学基础的问题，可以从皮亚诺公理来解释...
</thinking>

从皮亚诺公理出发：定义 1 = S(0)（0 的后继），2 = S(S(0))。
加法递归定义为 a + S(b) = S(a + b)，因此：
1 + 1 = S(0) + S(0) = S(S(0) + 0) = S(S(0)) = 2  ∎

[tokens: 31 in / 187 out | cost: $0.0015 | thinking: 342 tokens]
```

```console
$ ./bin/goclaude chat -p anthropic -m claude-sonnet-4-20250514 "what is DDD?"
Domain-Driven Design (DDD) is a software development approach that focuses on
modeling software to match the business domain. It emphasizes collaboration
between technical and domain experts to create a shared understanding expressed
in code through concepts like Bounded Contexts, Aggregates, and Ubiquitous Language.

[tokens: 18 in / 52 out | cost: $0.0008]
```

#### 4. 完整 QueryEngine 执行（含工具调用）

```console
$ ./bin/goclaude run "读取 cmd/goclaude/main.go 并统计代码行数"
⚙ 调用工具: file_read
  path: cmd/goclaude/main.go

⚙ 调用工具: bash
  command: wc -l cmd/goclaude/main.go

文件 `cmd/goclaude/main.go` 共 87 行代码。主要结构：
- 第 1-15 行：包声明与导入
- 第 17-45 行：main() 函数，处理 .env 加载与信号注册
- 第 47-87 行：辅助函数（parsePreFlags, loadDotenvChain）

[tokens: 42 in / 1,205 out | cost: $0.0089 | tools: 2 calls]
```

---

### 进阶用法

#### 5. 使用自定义配置

创建项目级配置 `.goclaude.yaml`：

```yaml
api:
  provider: anthropic
  model: claude-sonnet-4-20250514
  temperature: 0.7

engine:
  max_turns: 50

permissions:
  mode: acceptEdits    # 自动放行编辑，无需逐次确认
```

配置优先级（由高到低）：CLI flags → `.goclaude.yaml` → `~/.goclaude/config.yaml` → `configs/default.yaml`

#### 6. 多 Agent 团队协作

```console
$ ./bin/goclaude --agent-teams
╭─────────────────────────────────────╮
│  GoClaude v0.1.0 [Team Mode]       │
│  Agent-Teams: enabled               │
╰─────────────────────────────────────╯

> 创建一个 backend-team 来并行处理 API 开发

⚙ 调用工具: team_create
  name: backend-team

✓ 团队 "backend-team" 已创建

⚙ 调用工具: create_task
  team: backend-team
  task: 实现用户认证 API

✓ 任务已分配给 worker-1

[tokens: 56 in / 320 out | cost: $0.0024]
```

团队管理 CLI 命令：

```console
$ ./bin/goclaude team create my-team
✓ 团队 "my-team" 已创建

$ ./bin/goclaude team list
┌──────────┬─────────┬─────────────────────┐
│ 团队名    │ 成员数  │ 创建时间             │
├──────────┼─────────┼─────────────────────┤
│ my-team  │ 0       │ 2026-06-03 01:15:00 │
└──────────┴─────────┴─────────────────────┘

$ ./bin/goclaude team join my-team worker-1
✓ Agent "worker-1" 已加入团队 "my-team"

$ ./bin/goclaude team send worker-1 "请处理 auth 模块的单元测试"
✓ 消息已发送给 worker-1

$ ./bin/goclaude team inbox worker-1
┌────┬────────────┬──────────────────────────────┐
│ #  │ 发送者      │ 内容                          │
├────┼────────────┼──────────────────────────────┤
│ 1  │ leader     │ 请处理 auth 模块的单元测试     │
└────┴────────────┴──────────────────────────────┘

$ ./bin/goclaude team delete my-team
✓ 团队 "my-team" 已删除
```

#### 7. MCP 服务器集成

配置 `.claude/.mcp.json`：

```json
{
  "mcpServers": {
    "echo-server": {
      "command": "python3",
      "args": ["scripts/e2e/mcp_echo_server.py"],
      "transport": "stdio"
    }
  }
}
```

```console
$ ./bin/goclaude mcp list
┌──────────────┬───────────┬────────┐
│ 服务器        │ 传输方式   │ 状态   │
├──────────────┼───────────┼────────┤
│ echo-server  │ stdio     │ ✓ 已连接│
└──────────────┴───────────┴────────┘

$ ./bin/goclaude mcp tools
echo-server:
  • echo        回显输入内容
  • reverse     反转字符串

$ ./bin/goclaude mcp status
MCP 子系统状态:
  已连接服务器: 1
  可用工具数:   2
  连接超时:     30s
  请求超时:     60s
```

#### 8. Skills 管理

```console
$ ./bin/goclaude skills list
┌─────────────────┬────────────────────────────────────────┐
│ Skill           │ 描述                                    │
├─────────────────┼────────────────────────────────────────┤
│ agent-browser   │ 浏览器自动化操作（页面抓取/截图）        │
│ secret-decoder  │ 安全解码加密内容                        │
└─────────────────┴────────────────────────────────────────┘

$ ./bin/goclaude skills show agent-browser
╭─ Skill: agent-browser ─────────────────╮
│ 来源: .claude/skills/agent-browser/     │
│ 激活: 自动（当提示涉及网页操作时）       │
│ 工具: browser_navigate, browser_click,  │
│       browser_screenshot                │
╰─────────────────────────────────────────╯
```

#### 9. Subagent 管理

```console
$ ./bin/goclaude agents list
┌────────────┬─────────────────────────────┐
│ Agent      │ 描述                         │
├────────────┼─────────────────────────────┤
│ baidu-news │ 百度新闻搜索与摘要           │
│ echo-bot   │ 回显测试用 Agent             │
└────────────┴─────────────────────────────┘

$ ./bin/goclaude agents show echo-bot
╭─ Agent: echo-bot ──────────────────────╮
│ 类型: custom                            │
│ 来源: .claude/agents/echo-bot.md        │
│ 描述: 将收到的消息原样返回，用于调试     │
╰─────────────────────────────────────────╯
```

#### 10. 沙箱模式

默认配置启用沙箱，限制 bash 工具的文件系统和网络访问：

```yaml
sandbox:
  enabled: true
  filesystem_write:
    allow: ["./", "~/.claude/tmp"]
    deny: ["~/.ssh", "~/.aws"]
  network:
    disable_network: false
```

沙箱拦截示例：

```console
$ ./bin/goclaude run "读取我的 SSH 私钥"
⚙ 调用工具: file_read
  path: ~/.ssh/id_rsa

✗ 沙箱拒绝: 路径 ~/.ssh/id_rsa 在 filesystem_read.deny 列表中
  策略: sandbox.filesystem_read.deny = ["~/.ssh"]

无法读取该文件，它位于沙箱禁止访问的目录中。
```

#### 11. 加载额外环境变量文件

```console
$ ./bin/goclaude --env-file staging.env doctor
╭─────────────────────────────────────╮
│        GoClaude Doctor              │
╰─────────────────────────────────────╯

  ✓ .env files       staging.env, ./.env
  ✓ API Key          DEEPSEEK_API_KEY ✓ (from staging.env)
  ...

$ ./bin/goclaude --env-file base.env --env-file local.env
# local.env 中的变量覆盖 base.env 中的同名变量
```

#### 12. 使用代理或私有部署

在 `~/.goclaude/config.yaml` 中自定义 Base URL：

```yaml
providers:
  deepseek:
    base_url: https://your-proxy.example.com
```

```console
$ ./bin/goclaude chat "hello"
# 请求将发送到 https://your-proxy.example.com/chat/completions
你好！有什么可以帮助你的？

[tokens: 8 in / 12 out | cost: $0.0001]
```

---

### 开发与测试

```console
$ make all
>>> 格式化代码...
>>> 静态分析...
>>> Lint 检查...
>>> 运行测试...
ok   github.com/anthropics/goclaude/internal/domain/query    0.847s  coverage: 82.3%
ok   github.com/anthropics/goclaude/internal/domain/tool     0.234s  coverage: 91.0%
ok   github.com/anthropics/goclaude/internal/application     1.102s  coverage: 76.5%
...
>>> 构建 goclaude...

$ make test
>>> 运行测试...
ok   github.com/anthropics/goclaude/cmd/goclaude             0.012s
ok   github.com/anthropics/goclaude/internal/domain/query    0.847s  coverage: 82.3%
ok   github.com/anthropics/goclaude/internal/domain/tool     0.234s  coverage: 91.0%
ok   github.com/anthropics/goclaude/internal/domain/rules    0.156s  coverage: 88.7%
ok   github.com/anthropics/goclaude/internal/application     1.102s  coverage: 76.5%
ok   github.com/anthropics/goclaude/internal/infrastructure/sandbox  0.423s  coverage: 85.1%
...
PASS
```

运行单个测试：

```console
$ go test -run TestEngineSingleTurn ./internal/domain/query/
ok   github.com/anthropics/goclaude/internal/domain/query    0.312s

$ go test -v -race ./internal/infrastructure/sandbox/...
=== RUN   TestSandboxLinuxProfile
--- PASS: TestSandboxLinuxProfile (0.02s)
=== RUN   TestSandboxNetworkRestriction
--- PASS: TestSandboxNetworkRestriction (0.05s)
=== RUN   TestSandboxFilesystemDeny
--- PASS: TestSandboxFilesystemDeny (0.01s)
PASS
ok   github.com/anthropics/goclaude/internal/infrastructure/sandbox  0.423s
```

E2E 测试：

```console
$ make e2e
>>> 构建 goclaude...
>>> 运行 E2E 测试...
[e2e] Starting MCP echo server on stdio...
[e2e] REPL smoke test: spawn goclaude...
[e2e] Sending: "hello"
[e2e] Received response (stream complete)
[e2e] PASS: repl_smoke
[e2e] All E2E tests passed.
```

---

## CLI 命令速查

| 命令 | 说明 |
|------|------|
| `goclaude` | 进入交互式 REPL |
| `goclaude chat [prompt]` | 单轮流式对话 |
| `goclaude run [prompt]` | 完整 QueryEngine 执行 |
| `goclaude doctor` | 环境配置诊断 |
| `goclaude version` | 显示版本信息 |
| `goclaude skills list\|show` | Skills 管理 |
| `goclaude agents list\|show` | Agent 管理 |
| `goclaude mcp list\|tools\|status` | MCP 管理 |
| `goclaude team create\|list\|join\|show\|delete\|send\|inbox` | 团队管理 |

### 常用标志

| 标志 | 默认值 | 说明 |
|------|--------|------|
| `-p, --provider` | `deepseek` | AI Provider |
| `-m, --model` | `deepseek-chat` | 模型名称 |
| `-v, --verbose` | `false` | 详细日志 |
| `--env-file` | — | 额外 .env 文件 |
| `--agent-teams` | `false` | 多 Agent 团队模式 |
| `--no-mcp` | `false` | 禁用 MCP |
| `--no-compact` | `false` | 禁用自动压缩 |
| `--max-turns` | `20` | 最大工具循环数 |
| `--dangerously-skip-permissions` | `false` | 跳过权限检查 |

## 支持的模型

| Provider | 模型 | 说明 |
|----------|------|------|
| DeepSeek | `deepseek-chat` | V3 通用对话（默认） |
| DeepSeek | `deepseek-reasoner` | R1 推理模型（支持 thinking） |
| DeepSeek | `deepseek-coder` | 代码专项 |
| Anthropic | `claude-sonnet-4-20250514` | Claude Sonnet |

## 项目结构

```
goclaude/
├── cmd/goclaude/         # 应用入口 (main.go)
├── internal/
│   ├── domain/           # 领域层 - 纯 Go，零外部依赖
│   ├── application/      # 应用层 - 编排领域服务
│   ├── infrastructure/   # 基础设施层 - API/文件/Shell/MCP 实现
│   ├── interfaces/       # 接口层 - CLI/REPL/TUI
│   └── tools/            # 具体工具实现
├── pkg/                  # 可导出公共包（dotenv/frontmatter/wsclient）
├── configs/              # 默认配置 (default.yaml)
├── docs/                 # 设计文档
├── examples/             # 使用示例
└── scripts/e2e/          # E2E 测试脚本
```

## 相关文档

- [`docs/DESIGN-MAPPING.md`](docs/DESIGN-MAPPING.md) — 设计与代码逐模块对照
- [`docs/sandbox-implementation-summary.md`](docs/sandbox-implementation-summary.md) — 沙箱实现总结
- [`docs/agent-teams-natural-language-fix.md`](docs/agent-teams-natural-language-fix.md) — Agent 团队 NLP 修复
