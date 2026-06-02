package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/anthropics/goclaude/internal/application"
	"github.com/anthropics/goclaude/internal/domain/query"
	teamdomain "github.com/anthropics/goclaude/internal/domain/team"
	"github.com/anthropics/goclaude/internal/domain/tool"
	"github.com/anthropics/goclaude/internal/infrastructure/compact"
	"github.com/anthropics/goclaude/internal/infrastructure/todo"
	"github.com/anthropics/goclaude/internal/interfaces/shell"
	"github.com/anthropics/goclaude/internal/tools"
)

// 本文件聚合 `goclaude run` 的核心：装配 QueryEngine + 工具 + skills + MCP + subagent，
// 并把单次执行扩展成 REPL 多轮循环。从 root.go 拆出以提升可读性；逻辑保持不变。

func runREPL(cmd *cobra.Command, args []string) error {
	installLogger(flagVerbose)
	logger := slog.Default()
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("获取工作目录失败: %w", err)
	}
	app := AppConfig()

	// 未显式传 flag 时回退到 yaml
	if !cmd.Flags().Changed("provider") && app.API.Provider != "" {
		flagProvider = app.API.Provider
	}
	if !cmd.Flags().Changed("max-turns") && app.Engine.MaxTurns > 0 {
		// REPL 默认 max-turns=20（更短，避免长时间无响应）；yaml 提供时仍可放宽
		replMaxTurns = app.Engine.MaxTurns
	}
	if !cmd.Flags().Changed("max-context-kb") && app.Engine.TokenBudget > 0 {
		replMaxContextKB = app.Engine.TokenBudget / 1000
	}
	if !cmd.Flags().Changed("no-compact") {
		replNoCompact = !app.Engine.AutoCompact
	}
	if !cmd.Flags().Changed("no-mcp") {
		replNoMCP = !app.MCP.Enabled
	}

	// 非 TTY → 退回到说明信息（也可以选择从 stdin 一次性读取，但语义不同）
	if !shell.NewTerminal().IsTerminal() {
		fmt.Fprintln(os.Stderr, "提示：交互式 shell 需要 TTY。请通过 `goclaude run \"<prompt>\"` 执行单次查询。")
		return nil
	}

	// 1) Provider
	provider, modelName, err := buildProvider(flagProvider)
	if err != nil {
		return err
	}
	// 仅当用户在命令行显式传了 -m 时才覆盖 provider 默认模型；
	// 这样切换 provider 不需要再手动指定 -m。
	if cmd.Flags().Changed("model") && flagModel != "" {
		modelName = flagModel
	}

	// 2) AppContext（skills/agents/mcp/tools）
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	wired, err := buildAppContext(ctx, cwd, modelName, logger, !replNoMCP)
	if err != nil {
		return err
	}
	defer wired.Close()

	// 解析 agent-teams 执行模式开关（供 system prompt 与工具注册共用）。
	teamsEnabled := resolveAgentTeamsEnabled(cmd)

	// 3) Executor + Engine
	executor := tool.NewExecutor(wired.Registry, 10, logger)
	permCtx := &tool.PermissionContext{
		Mode:        resolveInitialPermissionMode(flagBypass),
		WorkingDir:  cwd,
		ProjectRoot: cwd,
	}
	executor.SetPermissionContext(permCtx)
	// 注：tool.UseContext 在 REPL 构建后注入，使 AskUser 可走 cooked 模式

	budget := query.NewTokenBudget(replMaxContextKB*1000, 0.8)
	cfg := query.DefaultConfig()
	cfg.Model = modelName
	cfg.MaxTurns = replMaxTurns
	cfg.AutoCompact = !replNoCompact
	if app.API.MaxTokens > 0 {
		cfg.MaxTokens = app.API.MaxTokens
	}
	if app.API.Temperature > 0 {
		cfg.Temperature = app.API.Temperature
	}
	cfg.SystemPrompt = []query.ContentBlock{
		{Type: query.ContentTypeText, Text: buildMainSystemPrompt(wired, teamsEnabled)},
	}

	var compactor query.Compactor
	if cfg.AutoCompact {
		sc := compact.NewSummarizingCompactor()
		sc.Logger = logger
		sc.Model = modelName
		sc.Tools = collectToolDefinitions(executor)
		compactor = sc
	}
	engine := query.NewEngine(provider, executor, budget, compactor, cfg, logger)
	// 先注册无回调的 hook；REPL 创建后再替换带回调的版本
	engine.SetAfterToolHook(application.NewSkillActivationHook(wired.SkillSvc, "", cwd, nil))

	// 4) AgentTool 注入（subagent）
	factory := application.NewDefaultAgentEngineFactory(wired.Registry, provider, budget, logger)
	factory.PermContext = &tool.PermissionContext{
		Mode:        tool.PermissionModeDefault,
		WorkingDir:  cwd,
		ProjectRoot: cwd,
	}
	factory.Compactor = compactor
	// ParentSessionID 之前恒为空，导致 subagent 事件无法溯源到发起会话；
	// 这里赋一个稳定的会话 ID，让并发 subagent 在日志/事件中可被归属。
	sessionID := fmt.Sprintf("repl-%d", os.Getpid())
	agentTool := tools.NewAgentToolWithService(wired.AgentSvc, factory, tools.AgentToolDefaults{
		ParentSessionID: sessionID,
		WorkingDir:      cwd,
		ProjectRoot:     cwd,
		DefaultModel:    modelName,
	})
	wired.Registry.Unregister(agentTool.Name())
	_ = wired.Registry.Register(agentTool)

	// SkillTool（与 src `tools/SkillTool` 对齐）：让 LLM 通过工具调用主动加载 skill body
	skillTool := tools.NewSkillTool(wired.SkillSvc, cwd, "")
	wired.Registry.Unregister(skillTool.Name())
	_ = wired.Registry.Register(skillTool)

	// Team 工具组（与 src tools/TeamCreateTool / TeamDeleteTool / SendMessageTool 对齐）
	// REPL 默认 (team_name, agent_name) 来自 flag/env：
	//   --team-name / GOCLAUDE_TEAM_NAME
	//   --agent-name / GOCLAUDE_AGENT_NAME
	// 可空 —— 主 agent 通常作为 leader 自己 team_create 启动新 team。
	teamRT := tools.TeamRuntime{
		TeamName:  flagTeamName,
		AgentName: flagAgentName,
		Session:   tools.NewTeamSession(),
	}

	// 执行模式路由：agent-teams 开关决定子任务走"多智能体团队协作"还是"单一 subagent"。
	if teamsEnabled {
		// 多智能体团队协作模式：注册全部 team 工具 + 生命周期 + leader inbox 钩子。
		// 若启动时已通过 flag/env 指定了 team，预登记会话身份：leader 名（或缺省）→
		// leader；其它 → 普通成员。这样即便不经 team_create，leader 也能自动处理 inbox。
		if flagTeamName != "" {
			if flagAgentName == "" || flagAgentName == teamdomain.LeaderName {
				teamRT.Session.SetLeader(flagTeamName)
			} else {
				teamRT.Session.SetMember(flagTeamName)
			}
		}
		tools.RegisterTeamTools(wired.Registry, wired.TeamSvc, teamRT)

		// 若指定了 (team, agent)：启动时自动 JoinTeam，并起一个心跳 goroutine；
		// REPL 退出时把成员状态设为 idle。team 不存在时仅打印警告，不退出 REPL，
		// 避免影响其它工具的可用性（与 src 的容错策略一致）。
		teamLifecycleCtx, teamLifecycleCancel := context.WithCancel(cmd.Context())
		teamCleanup := startTeamLifecycle(teamLifecycleCtx, wired.TeamSvc, teamRT, logger)
		defer func() {
			teamLifecycleCancel()
			teamCleanup()
		}()
	} else {
		// 单一 subagent 模式：不注册任何 team 工具，主 agent 只能通过 Agent 工具
		// 把任务下发给单一 subagent 独立执行。team 工具仅由 RegisterTeamTools 注入，
		// 这里不调用即等于完全不向模型暴露团队协作能力。
		logger.Debug("agent-teams disabled: tasks route to a single subagent")
	}

	if flagVerbose {
		mode := "single-subagent"
		if teamsEnabled {
			mode = "agent-teams"
		}
		fmt.Fprintf(os.Stderr,
			"[repl] provider=%s model=%s tools=%d skills=%d agents=%d mcp_tools=%d exec_mode=%s\n",
			flagProvider, modelName,
			wired.Registry.Count(),
			len(wired.SkillSvc.List()),
			len(wired.AgentSvc.List()),
			wired.MCPToolCount,
			mode,
		)
	}

	// 5) REPL
	repl := shell.NewREPL(engine, modelName, flagProvider, cwd)
	repl.Verbose = flagVerbose
	repl.PermissionMode = string(permCtx.Mode)
	// 注入扩展能力管理服务
	repl.Skills = &skillAdapter{svc: wired.SkillSvc}
	repl.Agents = &agentAdapter{svc: wired.AgentSvc}
	repl.MCP = &mcpAdapter{svc: wired.MCPSvc}
	repl.Tools = &toolsAdapter{reg: wired.Registry}

	// Shift+Tab 循环切换 permission mode（对齐 src 的 cycleMode 顺序）
	// default → acceptEdits → plan → bypass → default
	modes := []tool.PermissionMode{
		tool.PermissionModeDefault,
		tool.PermissionModeAcceptEdits,
		tool.PermissionModePlan,
		tool.PermissionModeBypass,
	}
	repl.OnPermissionModeChange = func() string {
		idx := indexOfMode(modes, permCtx.Mode)
		permCtx.Mode = modes[(idx+1)%len(modes)]
		executor.SetPermissionContext(permCtx)
		return string(permCtx.Mode)
	}

	// 修复 AskUser 在 raw 模式下读不到 \n 的问题：用 REPL 暂停 raw 模式后再读
	executor.SetUseContextTemplate(tool.UseContext{
		AskUser:   repl.AskUser,
		TodoStore: todo.NewMemoryStore(),
	})

	// 工具权限弹窗：当工具 CheckPermissions 返回 PermissionAsk 时，弹窗向用户确认
	// 与 src `Permission/PermissionRequest.tsx` 对齐
	executor.SetAskPermission(func(ctx context.Context, toolName string, input tool.Input, reason string) (bool, error) {
		return repl.AskPermission(ctx, toolName, input, reason)
	})

	// 把 REPL 注入为工具事件监听器：start/finish 事件改由 REPL 渲染
	// （✔/✗ 行尾追加耗时与状态摘要），从根上消除多协程并发 INFO 日志的乱序输出。
	executor.SetToolEventListener(repl)
	// 同样地，subagent 启动/结束事件由 REPL 渲染为 ◇/◆ 单行，
	// 替代过去 `INFO subagent 启动 agent_id=...` 的多行乱序日志。
	wired.AgentSvc.SetSubagentEventListener(repl)

	// skill 激活回调：条件 skill 被自动激活时打印通知
	repl.OnSkillActivate = repl.NotifySkillActivated
	engine.SetAfterToolHook(application.NewSkillActivationHook(wired.SkillSvc, "", cwd, repl.OnSkillActivate))

	// team leader 每轮自动处理 inbox：把协作者的任务进展同步进共享任务列表，
	// 并把可读摘要注入对话上下文，形成"分配 → 执行 → 回流"的闭环反馈。
	// 仅在多智能体团队协作模式下生效；单一 subagent 模式不挂此钩子。
	if teamsEnabled {
		repl.OnBeforeTurn = makeLeaderInboxHook(wired.TeamSvc, teamRT.Session, logger)
	}

	return repl.Run(ctx)
}
