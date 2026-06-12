package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/yaoice/goclaude/pkg/application"
	"github.com/yaoice/goclaude/pkg/domain/mcp"
	teamdomain "github.com/yaoice/goclaude/pkg/domain/team"
	mcpinfra "github.com/yaoice/goclaude/pkg/infrastructure/mcp"
)

// newSkillsCmd 创建 `goclaude skills` 子命令
//
//	goclaude skills list
//	goclaude skills show <name>
func newSkillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "管理 Skills（按需 prompt 包）",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "列出所有可用 skill",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			svc := application.NewSkillService(slog.Default())
			if err := svc.LoadAll(context.Background(), cwd, ""); err != nil {
				return err
			}
			skills := svc.List()
			if len(skills) == 0 {
				fmt.Println("（未发现 skill）")
				return nil
			}
			fmt.Printf("发现 %d 个 skill：\n\n", len(skills))
			for _, s := range skills {
				fmt.Printf("  %s [%s]\n", s.Name, s.Source)
				if s.Description != "" {
					fmt.Printf("    %s\n", s.Description)
				}
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "show <name>",
		Short: "查看 skill 内容",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			svc := application.NewSkillService(slog.Default())
			if err := svc.LoadAll(context.Background(), cwd, ""); err != nil {
				return err
			}
			content, ok := svc.Render(args[0], "")
			if !ok {
				return fmt.Errorf("skill %q 未找到", args[0])
			}
			fmt.Println(content)
			return nil
		},
	})

	return cmd
}

// newAgentsCmd 创建 `goclaude agents` 子命令
//
//	goclaude agents list
//	goclaude agents show <type>
func newAgentsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "管理 Subagents",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "列出所有可用 agent（内置 + 自定义）",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			svc := application.NewAgentService(slog.Default())
			_ = svc.LoadAll(context.Background(), cwd, "")
			defs := svc.List()
			fmt.Printf("共 %d 个 agent：\n\n", len(defs))
			for _, d := range defs {
				fmt.Printf("  %s [%s]\n", d.AgentType, d.Source)
				if d.WhenToUse != "" {
					fmt.Printf("    %s\n", d.WhenToUse)
				}
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "show <type>",
		Short: "查看 agent 详情",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			svc := application.NewAgentService(slog.Default())
			_ = svc.LoadAll(context.Background(), cwd, "")
			d, ok := svc.Get(args[0])
			if !ok {
				return fmt.Errorf("agent %q 未找到", args[0])
			}
			fmt.Printf("=== %s ===\n", d.AgentType)
			fmt.Printf("source: %s\n", d.Source)
			fmt.Printf("model: %s\n", d.Model)
			if len(d.Tools) > 0 {
				fmt.Printf("tools: %v\n", d.Tools)
			}
			if len(d.DisallowedTools) > 0 {
				fmt.Printf("disallowed: %v\n", d.DisallowedTools)
			}
			fmt.Printf("\nwhen-to-use:\n%s\n", d.WhenToUse)
			fmt.Printf("\n--- system prompt ---\n%s\n", d.SystemPrompt)
			return nil
		},
	})

	return cmd
}

// newMcpCmd 创建 `goclaude mcp` 子命令
//
//	goclaude mcp list           列出已配置的 MCP 服务器
//	goclaude mcp connect        连接所有启用的 MCP 服务器并打印工具列表
//	goclaude mcp tools          列出所有 MCP 工具
func newMcpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "管理 MCP 服务器",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "列出配置文件中发现的 MCP 服务器",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			configs, err := loadMcpConfigs(cwd)
			if err != nil {
				return err
			}
			if len(configs) == 0 {
				fmt.Println("（未发现 MCP 服务器配置）")
				return nil
			}
			fmt.Printf("发现 %d 个 MCP 服务器：\n\n", len(configs))
			for _, c := range configs {
				fmt.Printf("  %s [%s/%s, enabled=%v]\n", c.Name, c.TransportType, c.Scope, c.IsEnabled())
				if c.Command != "" {
					fmt.Printf("    command: %s %v\n", c.Command, c.Args)
				}
				if c.URL != "" {
					fmt.Printf("    url: %s\n", c.URL)
				}
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "tools",
		Short: "连接所有 MCP 服务器并打印工具列表",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			svc := application.NewMCPService(slog.Default())
			if err := svc.LoadAndConnect(ctx, cwd); err != nil {
				return err
			}
			defer svc.Shutdown()

			tools, err := svc.ListAllTools(ctx)
			if err != nil {
				return err
			}
			if len(tools) == 0 {
				fmt.Println("（无可用 MCP 工具）")
				return nil
			}
			for _, t := range tools {
				fmt.Printf("  %s — %s\n", application.PrefixedToolName(t.Server, t.Tool.Name), t.Tool.Description)
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "连接所有 MCP 服务器并打印连接状态（JSON）",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			svc := application.NewMCPService(slog.Default())
			ctx := context.Background()
			if err := svc.LoadAndConnect(ctx, cwd); err != nil {
				return err
			}
			defer svc.Shutdown()
			statuses := svc.Statuses()
			out, _ := json.MarshalIndent(statuses, "", "  ")
			fmt.Println(string(out))
			return nil
		},
	})

	return cmd
}

// loadMcpConfigs 直接读取配置但不连接（仅用于 list）
func loadMcpConfigs(cwd string) ([]*mcp.ServerConfig, error) {
	return mcpinfra.LoadDefault(cwd)
}

// newTeamCmd 创建 `goclaude team` 子命令
//
//	goclaude team list                       列出 ~/.goclaude/teams 下的所有 team
//	goclaude team show <name>                打印 team config + 成员
//	goclaude team delete <name> [--force]    清理 team 目录
//	goclaude team send <to> <text> --team N --from F  从 CLI 投递文本消息（调试用）
//	goclaude team inbox <agent> --team N [--peek]    拉取自己的未读消息
func newTeamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "team",
		Short: "管理 agent 协同 team / mailbox",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "列出所有 team（磁盘目录名）",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := application.NewTeamService()
			names, err := svc.ListTeams()
			if err != nil {
				return err
			}
			if len(names) == 0 {
				fmt.Println("(no teams found at ~/.goclaude/teams)")
				return nil
			}
			fmt.Printf("发现 %d 个 team：\n\n", len(names))
			for _, n := range names {
				fmt.Printf("  %s\n", n)
			}
			return nil
		},
	})

	createCmd := &cobra.Command{
		Use:   "create <name>",
		Short: "创建一个新 team（leader 自动作为第一个 member）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			desc, _ := cmd.Flags().GetString("description")
			role, _ := cmd.Flags().GetString("role")
			svc := application.NewTeamService()
			f, err := svc.CreateTeam(application.CreateTeamInput{
				Name:          args[0],
				Description:   desc,
				LeadAgentType: role,
				LeadCwd:       application.CurrentCwd(),
			})
			if err != nil {
				return err
			}
			fmt.Printf("✓ created team %q (lead=%s)\n", f.Name, f.LeadAgentID)
			return nil
		},
	}
	createCmd.Flags().String("description", "", "team purpose")
	createCmd.Flags().String("role", "", "leader role / agent_type")
	cmd.AddCommand(createCmd)

	joinCmd := &cobra.Command{
		Use:   "join <team> <agent>",
		Short: "把 agent 加入 team（用于多 goclaude 实例协同）",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			role, _ := cmd.Flags().GetString("role")
			svc := application.NewTeamService()
			_, m, err := svc.JoinTeam(application.JoinTeamInput{
				TeamName:  args[0],
				AgentName: args[1],
				AgentType: role,
				Cwd:       application.CurrentCwd(),
			})
			if err != nil {
				return err
			}
			fmt.Printf("✓ %s joined %s (agent_id=%s)\n", m.Name, args[0], m.AgentID)
			return nil
		},
	}
	joinCmd.Flags().String("role", "", "agent_type / role")
	cmd.AddCommand(joinCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   "show <name>",
		Short: "打印 team config + 成员",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := application.NewTeamService()
			f, err := svc.GetTeam(args[0])
			if err != nil {
				return err
			}
			if f == nil {
				return fmt.Errorf("team %q not found", args[0])
			}
			b, _ := json.MarshalIndent(f, "", "  ")
			fmt.Println(string(b))
			return nil
		},
	})

	deleteCmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "清理 team 目录（默认拒绝在仍有活跃成员时清理）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")
			svc := application.NewTeamService()
			deleted, err := svc.DeleteTeam(args[0], application.DeleteTeamOptions{Force: force})
			if err != nil {
				return err
			}
			if !deleted {
				fmt.Printf("team %q not found, nothing to do\n", args[0])
				return nil
			}
			fmt.Printf("✓ deleted team %q\n", args[0])
			return nil
		},
	}
	deleteCmd.Flags().Bool("force", false, "跳过活跃成员检查")
	cmd.AddCommand(deleteCmd)

	sendCmd := &cobra.Command{
		Use:   "send <to> <text>",
		Short: "从 CLI 投递文本消息到 teammate（调试用）",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamName, _ := cmd.Flags().GetString("team")
			from, _ := cmd.Flags().GetString("from")
			summary, _ := cmd.Flags().GetString("summary")
			if teamName == "" || from == "" {
				return fmt.Errorf("--team and --from are required")
			}
			svc := application.NewTeamService()
			res, err := svc.Send(application.SendInput{
				TeamName: teamName, From: from, To: args[0],
				Summary: summary, Text: args[1],
			})
			if err != nil {
				return err
			}
			fmt.Printf("✓ delivered to: %v\n", res.Recipients)
			return nil
		},
	}
	sendCmd.Flags().String("team", "", "team name (required)")
	sendCmd.Flags().String("from", "", "sender agent name (required)")
	sendCmd.Flags().String("summary", "cli send", "5-10 word preview")
	cmd.AddCommand(sendCmd)

	inboxCmd := &cobra.Command{
		Use:   "inbox <agent>",
		Short: "拉取自己的未读消息（默认 drain，--peek 仅查看）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamName, _ := cmd.Flags().GetString("team")
			peek, _ := cmd.Flags().GetBool("peek")
			if teamName == "" {
				return fmt.Errorf("--team is required")
			}
			svc := application.NewTeamService()
			msgs, err := svc.ReadInbox(teamName, args[0], !peek)
			if err != nil {
				return err
			}
			b, _ := json.MarshalIndent(msgs, "", "  ")
			fmt.Println(string(b))
			fmt.Fprintf(os.Stderr, "\n%d message(s) %s\n",
				len(msgs), map[bool]string{true: "peeked", false: "drained"}[peek])
			_ = slog.Default
			return nil
		},
	}
	inboxCmd.Flags().String("team", "", "team name (required)")
	inboxCmd.Flags().Bool("peek", false, "do not mark messages as read")
	cmd.AddCommand(inboxCmd)

	leaveCmd := &cobra.Command{
		Use:   "leave <team> <agent>",
		Short: "把 agent 从 team 中移除（不删除其 inbox 文件）",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := application.NewTeamService()
			if err := svc.LeaveTeam(args[0], args[1]); err != nil {
				return err
			}
			fmt.Printf("✓ %s left team %q\n", args[1], args[0])
			return nil
		},
	}
	cmd.AddCommand(leaveCmd)

	assignCmd := &cobra.Command{
		Use:   "assign <to> <subject>",
		Short: "派发结构化任务（task_assign 协议消息；调试用）",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamName, _ := cmd.Flags().GetString("team")
			from, _ := cmd.Flags().GetString("from")
			taskID, _ := cmd.Flags().GetString("task-id")
			desc, _ := cmd.Flags().GetString("description")
			if teamName == "" || from == "" {
				return fmt.Errorf("--team and --from are required")
			}
			if taskID == "" {
				taskID = fmt.Sprintf("task-cli-%d", time.Now().UnixMilli())
			}
			svc := application.NewTeamService()
			res, err := svc.AssignTask(application.AssignTaskInput{
				TeamName:    teamName,
				From:        from,
				To:          args[0],
				TaskID:      taskID,
				Subject:     args[1],
				Description: desc,
			})
			if err != nil {
				return err
			}
			fmt.Printf("✓ task %s assigned to %v (msg_id=%s)\n", taskID, res.Recipients, res.MessageID)
			return nil
		},
	}
	assignCmd.Flags().String("team", "", "team name (required)")
	assignCmd.Flags().String("from", "team-lead", "sender (default team-lead)")
	assignCmd.Flags().String("task-id", "", "stable task id; auto-generated if empty")
	assignCmd.Flags().String("description", "", "task description / requirements")
	cmd.AddCommand(assignCmd)

	statusCmd := &cobra.Command{
		Use:   "status <agent> <status>",
		Short: "设置成员状态：idle/working/blocked/error/done（调试用）",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			teamName, _ := cmd.Flags().GetString("team")
			if teamName == "" {
				return fmt.Errorf("--team is required")
			}
			svc := application.NewTeamService()
			if err := svc.SetMemberStatus(teamName, args[0], teamdomain.MemberStatus(args[1])); err != nil {
				return err
			}
			fmt.Printf("✓ %s status -> %s\n", args[0], args[1])
			return nil
		},
	}
	statusCmd.Flags().String("team", "", "team name (required)")
	cmd.AddCommand(statusCmd)

	return cmd
}
