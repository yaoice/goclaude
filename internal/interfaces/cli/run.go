package cli

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/anthropics/goclaude/internal/application"
	"github.com/anthropics/goclaude/internal/domain/hook"
	"github.com/anthropics/goclaude/internal/domain/query"
	"github.com/anthropics/goclaude/internal/domain/tool"
	"github.com/anthropics/goclaude/internal/infrastructure/appconfig"
	"github.com/anthropics/goclaude/internal/infrastructure/compact"
	"github.com/anthropics/goclaude/internal/infrastructure/memory"
	"github.com/anthropics/goclaude/internal/infrastructure/sandbox"
	"github.com/anthropics/goclaude/internal/infrastructure/todo"
	"github.com/anthropics/goclaude/internal/infrastructure/worktree"
	"github.com/anthropics/goclaude/internal/tools"
)

var (
	runProvider     string
	runModel        string
	runMaxTurns     int
	runNoMCP        bool
	runNoCompact    bool
	runMaxContextKB int
)

// newRunCmd 创建 `goclaude run` 子命令
//
// 与 chat 不同，run 走完整 QueryEngine：注册全部工具、加载 skills、连接 MCP、
// 接入 AgentTool 子代理。这是验证 Wire 装配是否串通的端到端入口。
//
//	goclaude run "帮我列出 src 目录下的 .go 文件并计数"
func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [prompt]",
		Short: "运行一次完整 QueryEngine（含工具/skills/MCP/subagent）",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runFullQuery,
	}
	cmd.Flags().StringVarP(&runProvider, "provider", "p", providerDeepSeek, "AI Provider: anthropic | deepseek")
	cmd.Flags().StringVar(&runModel, "model-name", "", "模型名（覆盖默认）")
	cmd.Flags().IntVar(&runMaxTurns, "max-turns", 20, "最大对话轮数")
	cmd.Flags().BoolVar(&runNoMCP, "no-mcp", false, "禁用 MCP 自动连接")
	cmd.Flags().BoolVar(&runNoCompact, "no-compact", false, "禁用上下文自动压缩")
	cmd.Flags().IntVar(&runMaxContextKB, "max-context-kb", 200, "上下文 token 预算（千）")
	return cmd
}

// runFullQuery 装配整个系统并执行一次查询
func runFullQuery(cmd *cobra.Command, args []string) error {
	installLogger(flagVerbose)
	logger := slog.Default()
	prompt := strings.Join(args, " ")
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()
	go handleInterrupt(cancel)

	cwd, _ := os.Getwd()
	app := AppConfig()

	// 用户未显式传 flag 时，回退到 yaml 配置
	if !cmd.Flags().Changed("max-turns") && app.Engine.MaxTurns > 0 {
		runMaxTurns = app.Engine.MaxTurns
	}
	if !cmd.Flags().Changed("max-context-kb") && app.Engine.TokenBudget > 0 {
		runMaxContextKB = app.Engine.TokenBudget / 1000
	}
	if !cmd.Flags().Changed("no-compact") {
		runNoCompact = !app.Engine.AutoCompact
	}
	if !cmd.Flags().Changed("no-mcp") {
		runNoMCP = !app.MCP.Enabled
	}

	// 1. 构造 Provider
	provider, modelName, err := buildProvider(runProvider)
	if err != nil {
		return err
	}
	if runModel != "" {
		modelName = runModel
	}

	// 2. 装配 SkillService、AgentService、MCPService
	wired, err := buildAppContext(ctx, cwd, modelName, logger, !runNoMCP)
	if err != nil {
		return err
	}
	defer wired.Close()

	// 解析 agent-teams 执行模式开关（供 system prompt 与工具注册共用）。
	teamsEnabled := resolveAgentTeamsEnabled(cmd)

	// 3. 构造 Engine（主 agent 的）
	executor := tool.NewExecutor(wired.Registry, 10, logger)
	executor.SetPermissionContext(&tool.PermissionContext{
		Mode:        tool.PermissionModeDefault,
		WorkingDir:  cwd,
		ProjectRoot: cwd,
	})
	// 装一个 headless 渲染器：把工具/subagent 事件渲染成单行 ⏵/✔/◇/◆ 进度，
	// 与 REPL（`shell.tool_render` / `shell.subagent_render`）风格对齐，
	// 彻底替代旧版本里 `2026/.. INFO 执行工具 tool=... id=...` 的乱序输出。
	render := newHeadlessRender(os.Stderr)
	executor.SetToolEventListener(render)
	wired.AgentSvc.SetSubagentEventListener(render)
	// 注入工具运行时回调：AskUser（stdin）+ TodoStore（内存）
	// WebSearch 默认 nil；用户后续注入 backend 时启用 web_search。
	executor.SetUseContextTemplate(tool.UseContext{
		AskUser:   newStdinAskUser(),
		TodoStore: todo.NewMemoryStore(),
	})
	budget := query.NewTokenBudget(runMaxContextKB*1000, 0.8)
	cfg := query.DefaultConfig()
	cfg.Model = modelName
	cfg.MaxTurns = runMaxTurns
	cfg.AutoCompact = !runNoCompact
	if app.API.MaxTokens > 0 {
		cfg.MaxTokens = app.API.MaxTokens
	}
	if app.API.Temperature > 0 {
		cfg.Temperature = app.API.Temperature
	}
	cfg.SystemPrompt = []query.ContentBlock{
		{Type: query.ContentTypeText, Text: buildMainSystemPrompt(wired, teamsEnabled)},
	}

	// 装配 Compactor：默认 LLM 摘要，失败回退本地截断
	var compactor query.Compactor
	if cfg.AutoCompact {
		sc := compact.NewSummarizingCompactor()
		sc.Logger = logger
		sc.Model = modelName
		// 透传当前可用工具，保持 Anthropic prompt cache 一致（prompt 已禁止工具调用）
		sc.Tools = collectToolDefinitions(executor)
		compactor = sc
	}

	engine := query.NewEngine(provider, executor, budget, compactor, cfg, logger)

	// 条件 skill 激活钩子：工具执行后自动激活匹配路径的条件 skill 并注入对话
	engine.SetAfterToolHook(application.NewSkillActivationHook(wired.SkillSvc, "", cwd, nil))

	// 4. 把 AgentTool 实例绑定到刚刚构造好的 Factory（subagent 复用主 registry）
	factory := application.NewDefaultAgentEngineFactory(wired.Registry, provider, budget, logger)
	factory.PermContext = &tool.PermissionContext{
		Mode:        tool.PermissionModeDefault,
		WorkingDir:  cwd,
		ProjectRoot: cwd,
	}
	factory.Compactor = compactor
	sessionID := fmt.Sprintf("run-%d", os.Getpid())
	agentTool := tools.NewAgentToolWithService(wired.AgentSvc, factory, tools.AgentToolDefaults{
		ParentSessionID: sessionID,
		WorkingDir:      cwd,
		ProjectRoot:     cwd,
		DefaultModel:    modelName,
	})
	// 用 agentTool 覆盖之前注册的占位 AgentTool
	wired.Registry.Unregister(agentTool.Name())
	_ = wired.Registry.Register(agentTool)

	// SkillTool（与 src `tools/SkillTool` 对齐）：让 LLM 通过工具调用主动加载 skill body
	skillTool := tools.NewSkillTool(wired.SkillSvc, cwd, "")
	wired.Registry.Unregister(skillTool.Name())
	_ = wired.Registry.Register(skillTool)

	// Team 工具组（与 src tools/TeamCreateTool / TeamDeleteTool / SendMessageTool 对齐）
	// 非交互式 run 也支持 --team-name / --agent-name 自动 JoinTeam，
	// 让脚本化派生 worker 进程的工作流可以一行命令完成。
	teamRT := tools.TeamRuntime{
		TeamName:  flagTeamName,
		AgentName: flagAgentName,
		Session:   tools.NewTeamSession(),
	}

	// 执行模式路由：开启 agent-teams → 注册 team 工具 + 生命周期；
	// 关闭 → 仅保留单一 subagent（Agent 工具），任务下发给单一子代理执行。
	if teamsEnabled {
		tools.RegisterTeamTools(wired.Registry, wired.TeamSvc, teamRT)

		teamLifecycleCtx, teamLifecycleCancel := context.WithCancel(cmd.Context())
		teamCleanup := startTeamLifecycle(teamLifecycleCtx, wired.TeamSvc, teamRT, logger)
		defer func() {
			teamLifecycleCancel()
			teamCleanup()
		}()
	} else {
		logger.Debug("agent-teams disabled: tasks route to a single subagent")
	}

	if flagVerbose {
		mode := "single-subagent"
		if teamsEnabled {
			mode = "agent-teams"
		}
		fmt.Fprintf(os.Stderr, "[run] provider=%s model=%s tools=%d skills=%d agents=%d mcp_tools=%d exec_mode=%s\n",
			runProvider, modelName,
			wired.Registry.Count(),
			len(wired.SkillSvc.List()),
			len(wired.AgentSvc.List()),
			wired.MCPToolCount,
			mode,
		)
	}

	// 5. 执行
	messages := []query.Message{
		query.NewTextMessage(query.RoleUser, prompt),
	}
	events := make(chan query.StreamEvent, 64)
	doneCh := make(chan *query.QueryResult, 1)
	errCh := make(chan error, 1)
	go func() {
		r, err := engine.Execute(ctx, messages, events)
		close(events)
		if err != nil {
			errCh <- err
			return
		}
		doneCh <- r
	}()

	// 6. 流式打印
	for ev := range events {
		switch ev.Type {
		case query.EventContentBlockDelta:
			if ev.Delta != nil && ev.Delta.Text != "" {
				fmt.Print(ev.Delta.Text)
			}
		case query.EventError:
			fmt.Fprintf(os.Stderr, "\n[error] %v\n", ev.Error)
		}
	}
	fmt.Println()

	select {
	case err := <-errCh:
		return err
	case r := <-doneCh:
		if flagVerbose {
			fmt.Fprintf(os.Stderr, "[run] turns=%d stop=%s\n", r.TurnCount, r.StopReason)
		}
	}
	return nil
}

// AppContext 主 agent 共享的运行时上下文（聚合 Skill/MCP/Agent/Team 四大服务）
type AppContext struct {
	Registry     *tool.Registry
	SkillSvc     *application.SkillService
	MCPSvc       *application.MCPService
	AgentSvc     *application.AgentService
	TeamSvc      *application.TeamService
	HookReg      *hook.Registry
	MCPToolCount int
}

// Close 释放 MCP 连接
func (c *AppContext) Close() {
	if c.MCPSvc != nil {
		c.MCPSvc.Shutdown()
	}
}

// buildAppContext 集中装配三大服务 + Tool Registry
//
// 调用顺序：
//  1. 创建 SkillService 并加载磁盘 skills
//  2. 创建 AgentService 并加载自定义 agents（含内置）
//  3. 创建 MCPService，可选地连接所有 MCP 服务器
//  4. 创建 Tool Registry，注册内置工具 + MCP 工具适配器
//
// 主 agent 与 subagent 共用同一个 Registry / SkillService / AgentService /
// MCPService，subagent 通过 Factory 在 Run 时再做工具过滤。
func buildAppContext(ctx context.Context, cwd, model string, logger *slog.Logger, enableMCP bool) (*AppContext, error) {
	app := &AppContext{}

	app.SkillSvc = application.NewSkillService(logger)
	if err := app.SkillSvc.LoadAll(ctx, cwd, ""); err != nil {
		logger.Warn("加载 skills 失败", "error", err)
	}

	app.AgentSvc = application.NewAgentService(logger)
	app.AgentSvc.EnableMemory(memory.NewService("", cwd))
	app.AgentSvc.EnableWorktree(worktree.NewService(""))
	app.AgentSvc.EnableSkills(app.SkillSvc)
	app.HookReg = hook.NewRegistry(logger)
	app.AgentSvc.EnableHooks(app.HookReg)
	if err := app.AgentSvc.LoadAll(ctx, cwd, ""); err != nil {
		logger.Warn("加载 agents 失败", "error", err)
	}

	app.MCPSvc = application.NewMCPService(logger)
	if enableMCP {
		if err := app.MCPSvc.LoadAndConnect(ctx, cwd); err != nil {
			logger.Warn("MCP 连接失败", "error", err)
		}
	}

	app.TeamSvc = application.NewTeamService()

	app.Registry = tool.NewRegistry()
	tools.RegisterAll(app.Registry, cwd, toSandboxConfig(AppConfig().Sandbox))
	if enableMCP {
		count, err := tools.RegisterMCPTools(ctx, app.Registry, app.MCPSvc)
		if err != nil {
			logger.Warn("注册 MCP 工具失败", "error", err)
		}
		app.MCPToolCount = count

		// 订阅 tools/list_changed：服务器主动推送时自动重建该 server 的工具
		registry := app.Registry
		mcpSvc := app.MCPSvc
		app.MCPSvc.OnToolsChanged(func(serverName string) {
			n, err := tools.SyncMCPTools(context.Background(), registry, mcpSvc, serverName)
			if err != nil {
				logger.Warn("同步 MCP 工具失败", "server", serverName, "error", err)
				return
			}
			logger.Debug("MCP 工具列表已同步", "server", serverName, "tools", n)
		})
	}
	return app, nil
}

// toSandboxConfig 把 appconfig 的沙箱参数转换为 infrastructure/sandbox.Config
//
// 这是 YAML 配置（configs/default.yaml 的 sandbox 段）到运行时沙箱的桥接：
// 之前两者之间缺失映射，导致即使 enabled: true 也不会生效。
func toSandboxConfig(c appconfig.SandboxConfig) *sandbox.Config {
	return &sandbox.Config{
		Enabled: c.Enabled,
		FSRead: sandbox.FsReadConfig{
			Allow: c.FilesystemRead.Allow,
			Deny:  c.FilesystemRead.Deny,
		},
		FSWrite: sandbox.FsWriteConfig{
			Allow: c.FilesystemWrite.Allow,
			Deny:  c.FilesystemWrite.Deny,
		},
		Network: sandbox.NetworkConfig{
			AllowedDomains:    c.Network.AllowedDomains,
			DeniedDomains:     c.Network.DeniedDomains,
			AllowUnixSockets:  c.Network.AllowUnixSockets,
			AllowLocalBinding: c.Network.AllowLocalBinding,
			DisableNetwork:    c.Network.DisableNetwork,
		},
		AllowUnsandboxed:          c.AllowUnsandboxedCommands,
		ExcludedCommands:          c.ExcludedCommands,
		EnableWeakerNestedSandbox: c.EnableWeakerNestedSandbox,
		IgnoreViolations:          c.IgnoreViolations,
	}
}

// buildMainSystemPrompt 把 skills / agents 元信息汇总到主 agent 系统提示中
//
// 对齐 src/commands.ts 中 getSkillToolCommands / Agent tool prompt：
//   - skill 列表 + 调用方式（Skill 工具）
//   - subagent 列表 + 调用方式（Agent 工具，subagent_type 参数）
//   - MCP 服务器连接概况（具体工具名以 mcp__ 前缀已通过 InputSchema 暴露给模型）
func buildMainSystemPrompt(app *AppContext, teamsEnabled bool) string {
	var sb strings.Builder
	sb.WriteString("You are goclaude, an open-source AI coding CLI. Your name is goclaude.\n")
	sb.WriteString("When the user asks who you are, what your name is, or which assistant " +
		"is talking to them, you MUST identify yourself as \"goclaude\" — an open-source " +
		"AI coding CLI. Do NOT claim to be Claude, ChatGPT, GPT, Gemini, or any other " +
		"branded product. You may discuss the configured backend model only when the user " +
		"explicitly asks about the model or provider configuration.\n\n")

	if skills := app.SkillSvc.List(); len(skills) > 0 {
		sb.WriteString("Available Skills (load full body via the `Skill` tool with `name=<skill-name>`):\n")
		for _, s := range skills {
			line := fmt.Sprintf("  - %s", s.Name)
			if s.WhenToUse != "" {
				line += ": " + s.WhenToUse
			} else if s.Description != "" {
				line += ": " + s.Description
			}
			sb.WriteString(line + "\n")
		}
		sb.WriteString("\n")
	}

	if agents := app.AgentSvc.List(); len(agents) > 0 {
		sb.WriteString("Available Subagents (call via the `Agent` tool with `subagent_type`):\n")
		for _, a := range agents {
			sb.WriteString(fmt.Sprintf("  - %s: %s\n", a.AgentType, a.WhenToUse))
		}
		sb.WriteString("\n")
	}

	// 执行模式提示：让模型清楚当前可用的任务编排方式，避免调用不存在的工具。
	if teamsEnabled {
		sb.WriteString("Task Execution Mode: AGENT-TEAMS (multi-agent collaboration is ENABLED).\n" +
			"For complex tasks that benefit from parallel specialists, you MAY coordinate a team " +
			"using the team tools (team_create / send_message / create_task / auto_assign_task ...). " +
			"You may also still delegate isolated subtasks to a single subagent via the `Agent` tool.\n\n")
	} else {
		sb.WriteString("Task Execution Mode: SINGLE-SUBAGENT (multi-agent team collaboration is DISABLED).\n" +
			"Team tools are NOT available. Delegate subtasks ONLY to a single subagent via the `Agent` tool; " +
			"do NOT attempt to create teams or send inter-agent messages.\n\n")
	}

	if statuses := app.MCPSvc.Statuses(); len(statuses) > 0 {
		connected := 0
		for _, s := range statuses {
			if s.Connected {
				connected++
			}
		}
		if connected > 0 {
			sb.WriteString(fmt.Sprintf("MCP Servers Connected: %d. MCP tools are prefixed with mcp__<server>__<tool>.\n\n", connected))
		}
	}

	return sb.String()
}

// collectToolDefinitions 把 executor 中的工具定义转成 query.ToolDefinition
//
// compactor 在调用 AIProvider.Send 时需要传入与主对话一致的 tools，
// 用于命中 Anthropic prompt cache。
func collectToolDefinitions(executor *tool.Executor) []query.ToolDefinition {
	raw := executor.GetToolDefinitions()
	defs := make([]query.ToolDefinition, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		def := query.ToolDefinition{}
		if v, ok := m["name"].(string); ok {
			def.Name = v
		}
		if v, ok := m["description"].(string); ok {
			def.Description = v
		}
		def.InputSchema = m["input_schema"]
		defs = append(defs, def)
	}
	return defs
}

// newStdinAskUser 构造一个把问题打到 stderr、从 stdin 读一行作为答复的回调
//
// 当 stdin 不是终端时（如管道/CI），返回错误而非阻塞 — 调用方（AskUserTool）
// 会把错误转成 tool error 返回给模型，避免主循环卡死。
func newStdinAskUser() func(ctx context.Context, question string) (string, error) {
	return func(ctx context.Context, question string) (string, error) {
		// 在 stderr 提问，避免污染对话 stdout
		fmt.Fprintf(os.Stderr, "\n[ask_user] %s\n> ", question)

		// 后台 goroutine 读 stdin，主路径 select 等 ctx 或读完
		type lineRes struct {
			line string
			err  error
		}
		ch := make(chan lineRes, 1)
		go func() {
			r := bufio.NewReader(os.Stdin)
			line, err := r.ReadString('\n')
			ch <- lineRes{line: strings.TrimRight(line, "\r\n"), err: err}
		}()

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case res := <-ch:
			if res.err != nil && res.line == "" {
				return "", fmt.Errorf("read stdin: %w", res.err)
			}
			return res.line, nil
		}
	}
}
