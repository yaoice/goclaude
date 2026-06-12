package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/yaoice/goclaude/pkg/application"
	"github.com/yaoice/goclaude/pkg/domain/query"
	teamdomain "github.com/yaoice/goclaude/pkg/domain/team"
	"github.com/yaoice/goclaude/pkg/domain/tool"
	"github.com/yaoice/goclaude/pkg/infrastructure/compact"
	"github.com/yaoice/goclaude/pkg/infrastructure/todo"
	workflowinfra "github.com/yaoice/goclaude/pkg/infrastructure/workflow"
	"github.com/yaoice/goclaude/pkg/interfaces/shell"
	"github.com/yaoice/goclaude/pkg/tools"
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

	// 解析 agent-teams 执行模式开关（供 system prompt 与工具注册共用）。
	teamsEnabled := resolveAgentTeamsEnabled(cmd)

	// --workspace flag 覆盖 YAML workspace.dir
	if cmd.Flags().Changed("workspace") && flagWorkspace != "" {
		app.Workspace.Dir = flagWorkspace
	}
	// 确保 workspace 目录存在（auto_create 默认开启）
	if _, err := app.EnsureWorkspace(cwd); err != nil {
		logger.Warn("ensure workspace dir", "error", err)
	}

	// 工作区产物路径：直接使用 workspace 根目录，不创建子目录
	sessionID := fmt.Sprintf("repl-%d", os.Getpid())
	workspaceDir := app.WorkspaceRoot(cwd)
	if workspaceDir == "" {
		workspaceDir = cwd
	}
	logger.Debug("workspace path resolved", "dir", workspaceDir, "session", sessionID)

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

	// 3) Executor + Engine
	executor := tool.NewExecutor(wired.Registry, 10, logger)
	permCtx := &tool.PermissionContext{
		Mode:          resolveInitialPermissionMode(flagBypass),
		WorkingDir:    cwd,
		ProjectRoot:   cwd,
		WorkspaceRoot: workspaceDir,
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
		{Type: query.ContentTypeText, Text: buildMainSystemPrompt(wired, teamsEnabled, app, workspaceDir)},
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
		Mode:          tool.PermissionModeDefault,
		WorkingDir:    cwd,
		ProjectRoot:   cwd,
		WorkspaceRoot: workspaceDir,
	}
	factory.Compactor = compactor
	agentTool := tools.NewAgentToolWithService(wired.AgentSvc, factory, tools.AgentToolDefaults{
		ParentSessionID: sessionID,
		WorkingDir:      cwd,
		ProjectRoot:     cwd,
		DefaultModel:    modelName,
		WorkspaceRoot:   workspaceDir,
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

		// TeamEngine：管理 team member worker goroutine 的 spawn/stop 生命周期。
		// 在 team_create / auto_setup_team 成功创建 team 并添加 members 后，
		// 通过 TeamSession.OnTeamCreated 回调自动 spawn worker goroutine。
		teamEngine := application.NewTeamEngine(
			wired.AgentSvc,
			wired.TeamSvc,
			factory,
			application.TeamEngineConfig{
				DefaultModel:    modelName,
				ProjectRoot:     cwd,
				WorkspaceRootFn: func() string { return app.WorkspaceRoot(cwd) },
				PollInterval:    5 * time.Second,
				TaskTimeout:     5 * time.Minute,
			},
			logger,
		)

		// 注入到 AgentService，供 team 工具（通过 agentSvc.TeamEngine()）访问
		wired.AgentSvc.EnableTeamEngine(teamEngine)

		// team 创建后自动 spawn member workers
		teamRT.Session.OnTeamCreated = func(teamName string) {
			if err := teamEngine.SpawnMembers(context.Background(), teamName); err != nil {
				logger.Warn("auto-spawn team members failed",
					"team", teamName,
					"error", err,
				)
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
		// REPL 退出时关闭所有 team member worker
		defer teamEngine.ShutdownAllTeams(context.Background())
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

	// 5.5) Workflow 编排服务 + Plan Agent
	wfLoader := workflowinfra.NewLoader(os.Getenv("HOME"))
	wfDefaults := application.WorkflowDefaults{
		ParentSessionID: sessionID,
		WorkingDir:      cwd,
		ProjectRoot:     cwd,
		DefaultModel:    modelName,
		WorkspaceRoot:   workspaceDir,
	}
	wfSvc := application.NewWorkflowService(wired.AgentSvc, factory, wfDefaults, logger)

	// Plan Agent: AI 驱动的 workflow 定义生成器（对齐 oh-my-openagent Sisyphus）
	planSvc := application.NewPlanAgentService(wired.AgentSvc, factory, wfLoader, logger)

	workflows := newWorkflowAdapter(wfSvc, planSvc, wfLoader, wired.AgentSvc, factory, wfDefaults, cwd, func() string { return app.WorkspaceRoot(cwd) })

	// 5) REPL
	repl := shell.NewREPL(engine, modelName, flagProvider, cwd)
	repl.Verbose = flagVerbose
	repl.PermissionMode = string(permCtx.Mode)
	repl.HookReg = wired.HookReg // 长期记忆生命周期 hooks
	repl.SessionID = sessionID   // 当前会话 ID
	// 注入扩展能力管理服务
	repl.Skills = &skillAdapter{svc: wired.SkillSvc}
	repl.Agents = &agentAdapter{svc: wired.AgentSvc}
	repl.MCP = &mcpAdapter{svc: wired.MCPSvc}
	repl.Tools = &toolsAdapter{reg: wired.Registry}
	repl.Teams = &teamAdapter{svc: wired.TeamSvc}
	repl.Workflows = workflows
	repl.Memory = wired.MemorySvc // /remember /memory 命令依赖

	// 共享的 todo 存储：避免 /workspace 切换时重置 todo 清单
	todoStore := todo.NewMemoryStore()

	// /workspace 动态切换：更新 config + executor + permCtx
	repl.OnWorkspaceSet = func(newPath string) (string, error) {
		if newPath == "" {
			// 查看当前路径
			return app.WorkspaceRoot(cwd), nil
		}
		// 更新 config
		app.Workspace.Dir = newPath
		// 解析并确保目录存在
		resolved, err := app.EnsureWorkspace(cwd)
		if err != nil {
			return "", err
		}
		// 更新 executor 的 UseContext 模板（file_write 等工具从此读取）
		executor.SetUseContextTemplate(tool.UseContext{
			AskUser:       repl.AskUser,
			TodoStore:     todoStore,
			WorkingDir:    cwd,
			ProjectRoot:   cwd,
			WorkspaceRoot: resolved,
		})
		// 更新权限上下文
		permCtx.WorkspaceRoot = resolved
		executor.SetPermissionContext(permCtx)
		return resolved, nil
	}

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
		AskUser:       repl.AskUser,
		TodoStore:     todoStore,
		WorkingDir:    cwd,
		ProjectRoot:   cwd,
		WorkspaceRoot: workspaceDir,
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
