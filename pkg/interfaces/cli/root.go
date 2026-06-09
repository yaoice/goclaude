// Package cli 实现 Cobra CLI 命令定义
package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/anthropics/goclaude/pkg/domain/tool"
)

var (
	// 全局标志
	flagModel    string
	flagVerbose  bool
	flagBypass   bool
	flagProvider string

	// REPL 专用标志
	replNoMCP        bool
	replNoCompact    bool
	replMaxTurns     int
	replMaxContextKB int

	// Team 集成标志（REPL 与 run 子命令共用）：
	//   --team-name N --agent-name A     启动时自动 JoinTeam(N, A)，
	//                                    退出时 SetMemberActive(false)，
	//                                    并在后台周期性 Heartbeat。
	//   --team-role  R                   可选的 agent_type / role 标注。
	flagTeamName  string
	flagAgentName string
	flagTeamRole  string

	// flagAgentTeams 控制子任务执行模式的功能开关（agent-teams 总闸）：
	//   true  → 多智能体团队协作：注册全部 team 工具 + 生命周期 + leader inbox 钩子
	//   false → 单一 subagent：不注册 team 工具，任务只走 Agent 工具下发给单一子代理
	// 未在命令行显式传入时回退到 YAML 的 agent_teams.enabled（默认 true）。
	flagAgentTeams bool

	// flagWorkspace 覆盖任务产物输出目录（覆盖 YAML workspace.dir）。
	// 支持绝对路径、~/ 和相对路径。未设置时使用 YAML 配置的默认值 ./workspaces/。
	flagWorkspace string
)

// NewRootCmd 创建根命令
func NewRootCmd(version string) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "goclaude",
		Short: "GoClaude - AI编程助手",
		Long: `基于DDD架构的Golang版终端AI编程助手（GoClaude）。

无参数直接运行将进入交互式 shell（REPL）。`,
		// SilenceUsage 防止用户操作错误时刷一屏 usage
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runREPL(cmd, args)
		},
	}

	// 全局标志
	rootCmd.PersistentFlags().StringVarP(&flagModel, "model", "m", "deepseek-chat", "AI模型名称")
	rootCmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "详细输出模式")
	rootCmd.PersistentFlags().BoolVar(&flagBypass, "dangerously-skip-permissions", false, "跳过权限检查（危险）")

	// --env-file 已由 main 在 cobra 解析前截获并加载；这里只为让 --help 能展示
	// 该标志（cobra 不会再去读它，因为 main 已从 args 中移除）
	var dummyEnvFiles []string
	rootCmd.PersistentFlags().StringArrayVar(&dummyEnvFiles, "env-file", nil,
		"额外加载的 .env 文件（可重复；优先级最高）")

	// REPL 专用（仅在根命令直接执行时生效；不与 chat/run 子命令的 -p 冲突）
	rootCmd.Flags().StringVarP(&flagProvider, "provider", "p", providerDeepSeek, "AI Provider: anthropic | deepseek")
	rootCmd.Flags().BoolVar(&replNoMCP, "no-mcp", false, "禁用 MCP 自动连接")
	rootCmd.Flags().BoolVar(&replNoCompact, "no-compact", false, "禁用上下文自动压缩")
	rootCmd.Flags().IntVar(&replMaxTurns, "max-turns", 20, "单轮查询的最大工具循环轮数")
	rootCmd.Flags().IntVar(&replMaxContextKB, "max-context-kb", 200, "上下文 token 预算（千）")

	// Team 集成（PersistentFlag → REPL/run 子命令共享）
	rootCmd.PersistentFlags().StringVar(&flagTeamName, "team-name", os.Getenv("GOCLAUDE_TEAM_NAME"),
		"加入的 team 名（启动时自动 JoinTeam，退出时自动设为 idle）；env: GOCLAUDE_TEAM_NAME")
	rootCmd.PersistentFlags().StringVar(&flagAgentName, "agent-name", os.Getenv("GOCLAUDE_AGENT_NAME"),
		"本会话在 team 中的 agent 名；env: GOCLAUDE_AGENT_NAME")
	rootCmd.PersistentFlags().StringVar(&flagTeamRole, "team-role", os.Getenv("GOCLAUDE_AGENT_ROLE"),
		"agent_type / 角色标注（可选）；env: GOCLAUDE_AGENT_ROLE")

	// Agent-Teams 执行模式开关（PersistentFlag → REPL/run 子命令共享）
	//   --agent-teams=false   关闭团队协作，任务下发给单一 subagent
	//   不传时依次检查 env GOCLAUDE_AGENT_TEAMS → YAML agent_teams.enabled
	rootCmd.PersistentFlags().BoolVar(&flagAgentTeams, "agent-teams", true,
		"启用多智能体团队协作（agent-teams）；=false 时任务只下发给单一 subagent。env: GOCLAUDE_AGENT_TEAMS；默认随 YAML agent_teams.enabled")

	// Workspace 产物输出路径（PersistentFlag → REPL/run 子命令共享）
	//   可指定自定义目录；默认使用 YAML workspace.dir（./workspaces/）。
	rootCmd.PersistentFlags().StringVar(&flagWorkspace, "workspace", "",
		"任务产物统一输出目录。支持绝对路径、~/ 和相对路径。默认: ./workspaces/")

	// 子命令
	rootCmd.AddCommand(newDoctorCmd())
	rootCmd.AddCommand(newVersionCmd(version))
	rootCmd.AddCommand(newChatCmd())
	rootCmd.AddCommand(newSkillsCmd())
	rootCmd.AddCommand(newAgentsCmd())
	rootCmd.AddCommand(newMcpCmd())
	rootCmd.AddCommand(newTeamCmd())
	rootCmd.AddCommand(newRunCmd())

	return rootCmd
}

// newVersionCmd 创建version子命令
func newVersionCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "显示版本信息",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("goclaude version %s\n", version)
		},
	}
}

// checkCommand 检查命令是否在 PATH 中可用
//
// 使用 exec.LookPath 跨平台正确解析 PATH（Linux/macOS Homebrew/Nix/Windows
// 都能命中），避免硬编码 /usr/bin 漏报。
//
// 若提供了 args，会进一步运行 `<name> <args...>` 验证返回码 0。
// 退出码非 0 视为不可用（处理某些命令存在但不可执行的情况）。
func checkCommand(name string, args ...string) bool {
	if _, err := exec.LookPath(name); err != nil {
		return false
	}
	if len(args) == 0 {
		return true
	}
	cmd := exec.Command(name, args...)
	// 抑制输出（doctor 自己打印结果）
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// indexOfMode 在 modes 中定位 cur；找不到返回 0
func indexOfMode(modes []tool.PermissionMode, cur tool.PermissionMode) int {
	for i, m := range modes {
		if m == cur {
			return i
		}
	}
	return 0
}

// resolveAgentTeamsEnabled 计算 agent-teams 执行模式开关的最终取值。
//
// 优先级（高 → 低）：
//  1. 命令行显式传入 --agent-teams（最高优先级）
//  2. 环境变量 GOCLAUDE_AGENT_TEAMS（支持动态生效，无需改文件）
//  3. YAML agent_teams.enabled（长期配置兜底）
//
// 环境变量接受 true/false/1/0/yes/no（大小写不敏感）。
func resolveAgentTeamsEnabled(cmd *cobra.Command) bool {
	// 1) CLI flag 显式传入时优先级最高
	if cmd.Flags().Changed("agent-teams") {
		return flagAgentTeams
	}

	// 2) 环境变量 GOCLAUDE_AGENT_TEAMS 动态生效
	if envVal := os.Getenv("GOCLAUDE_AGENT_TEAMS"); envVal != "" {
		switch envVal {
		case "true", "True", "TRUE", "1", "yes", "Yes", "YES", "on", "On", "ON":
			return true
		case "false", "False", "FALSE", "0", "no", "No", "NO", "off", "Off", "OFF":
			return false
		}
	}

	// 3) YAML 配置兜底
	return AppConfig().AgentTeams.Enabled
}
