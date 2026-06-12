package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yaoice/goclaude/pkg/util/dotenv"
	"github.com/yaoice/goclaude/pkg/util/settingsenv"
)

// 本文件聚合 doctor 子命令（环境检查）。从 root.go 拆出以提升可读性；逻辑保持不变。

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "检查环境配置是否正确",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("🔍 环境检查...")
			fmt.Println()

			printCheck := func(label string, ok bool, missingHint string) {
				fmt.Printf("  %s: ", label)
				if ok {
					fmt.Println("✓")
				} else {
					fmt.Println("✗ " + missingHint)
				}
			}

			printCheck("Git", checkCommand("git", "--version"), "未安装")
			printCheck("ripgrep", checkCommand("rg", "--version"), "未安装（搜索功能将不可用）")

			// API Key 检查（按最长 key 名右侧 padding 对齐）
			apiKeys := []string{"ANTHROPIC_API_KEY", "DEEPSEEK_API_KEY"}
			maxKey := 0
			for _, k := range apiKeys {
				if len(k) > maxKey {
					maxKey = len(k)
				}
			}
			for _, env := range apiKeys {
				pad := strings.Repeat(" ", maxKey-len(env))
				fmt.Printf("  %s:%s ", env, pad)
				if os.Getenv(env) != "" {
					fmt.Println("✓ 已配置")
				} else {
					fmt.Println("✗ 未配置")
				}
			}

			// .env 加载诊断
			fmt.Println()
			fmt.Println("📄 .env 加载来源（按加载顺序）:")
			records := dotenv.Loaded()
			if len(records) == 0 {
				fmt.Println("  （无）")
			} else {
				for _, rec := range records {
					fmt.Printf("  • %s  (%d 个变量)\n", rec.Path, len(rec.Keys))
					if len(rec.Keys) > 0 {
						// 只列变量名，绝不打印值
						names := strings.Join(rec.Keys, ", ")
						if len(names) > 80 {
							names = names[:77] + "..."
						}
						fmt.Printf("    %s\n", names)
					}
				}
			}

			// settings.json env 字段加载诊断（与 .env 链同样的语义）
			fmt.Println()
			fmt.Println("📄 settings.json env 字段加载来源（按加载顺序）:")
			sRecords := settingsenv.Loaded()
			if len(sRecords) == 0 {
				fmt.Println("  （无）")
			} else {
				for _, rec := range sRecords {
					fmt.Printf("  • %s  (%d 个变量)\n", rec.Path, len(rec.Keys))
					if len(rec.Keys) > 0 {
						names := strings.Join(rec.Keys, ", ")
						if len(names) > 80 {
							names = names[:77] + "..."
						}
						fmt.Printf("    %s\n", names)
					}
				}
			}

			// YAML 应用配置加载诊断 —— 这是参数类配置的"唯一信息源"
			fmt.Println()
			fmt.Println("📄 YAML 配置加载来源（按加载顺序，后者覆盖前者）:")
			cfg := AppConfig()
			if len(cfg.LoadedFrom) == 0 {
				fmt.Println("  （未加载任何 YAML 文件，使用内置默认值）")
			} else {
				for _, p := range cfg.LoadedFrom {
					fmt.Printf("  • %s\n", p)
				}
			}
			fmt.Println()
			fmt.Println("⚙️  当前生效的关键参数:")
			fmt.Printf("  api.provider          = %s\n", cfg.API.Provider)
			fmt.Printf("  api.model             = %s\n", cfg.API.Model)
			fmt.Printf("  api.max_tokens        = %d\n", cfg.API.MaxTokens)
			fmt.Printf("  api.temperature       = %v\n", cfg.API.Temperature)
			fmt.Printf("  permissions.mode      = %s\n", cfg.Permissions.Mode)
			fmt.Printf("  engine.max_turns      = %d\n", cfg.Engine.MaxTurns)
			fmt.Printf("  engine.token_budget   = %d\n", cfg.Engine.TokenBudget)
			fmt.Printf("  engine.auto_compact   = %v\n", cfg.Engine.AutoCompact)
			fmt.Printf("  mcp.enabled           = %v\n", cfg.MCP.Enabled)
			fmt.Printf("  agent_teams.enabled   = %v\n", cfg.AgentTeams.Enabled)
			if pc, ok := cfg.Providers["deepseek"]; ok {
				fmt.Printf("  providers.deepseek.base_url = %s\n", pc.BaseURL)
			}

			fmt.Println()
			fmt.Println("检查完成。")
			return nil
		},
	}
}
