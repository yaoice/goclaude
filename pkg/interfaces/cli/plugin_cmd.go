package cli

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/yaoice/goclaude/pkg/application"
	plugininfra "github.com/yaoice/goclaude/pkg/infrastructure/plugin"
)

// newPluginCmd 创建 `goclaude plugin` 子命令树。
//
//	goclaude plugin list
//	goclaude plugin search [query]
//	goclaude plugin install <name@marketplace> | <source>
//	goclaude plugin uninstall <name>
//	goclaude plugin enable <name>
//	goclaude plugin disable <name>
//	goclaude plugin marketplace add <source>
//	goclaude plugin marketplace list
//	goclaude plugin marketplace remove <name>
func newPluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "管理插件与插件市场（commands/agents/skills/hooks/MCP）",
		Long: "插件可贡献 commands / agents / skills / hooks / MCP servers。\n" +
			"市场来源支持本地目录、远程 git 仓库与 HTTP(S) 压缩包。",
	}

	var allowInternal bool
	cmd.PersistentFlags().BoolVar(&allowInternal, "allow-internal-hosts", false,
		"允许从内网地址拉取市场/插件（默认禁止，谨慎使用）")

	newSvc := func() *application.PluginService {
		plugininfra.SetAllowInternalHosts(allowInternal)
		svc := application.NewPluginService("", slog.Default())
		if err := svc.Load(context.Background()); err != nil {
			slog.Warn("加载插件状态失败", "error", err)
		}
		return svc
	}

	cmd.AddCommand(newPluginListCmd(newSvc))
	cmd.AddCommand(newPluginSearchCmd(newSvc))
	cmd.AddCommand(newPluginInstallCmd(newSvc))
	cmd.AddCommand(newPluginUninstallCmd(newSvc))
	cmd.AddCommand(newPluginToggleCmd(newSvc, true))
	cmd.AddCommand(newPluginToggleCmd(newSvc, false))
	cmd.AddCommand(newPluginMarketplaceCmd(newSvc))

	return cmd
}

func newPluginListCmd(newSvc func() *application.PluginService) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "列出已安装插件",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := newSvc()
			plugins := svc.ListPlugins()
			if len(plugins) == 0 {
				fmt.Println("（暂无已安装插件）")
				return nil
			}
			fmt.Printf("已安装 %d 个插件：\n\n", len(plugins))
			for _, p := range plugins {
				state := "enabled"
				if !p.Enabled {
					state = "disabled"
				}
				ver := ""
				if p.Version != "" {
					ver = " v" + p.Version
				}
				mkt := ""
				if p.Marketplace != "" {
					mkt = " @" + p.Marketplace
				}
				fmt.Printf("  %s%s  [%s]%s\n", p.Name, ver, state, mkt)
				if p.Description != "" {
					fmt.Printf("    %s\n", p.Description)
				}
			}
			return nil
		},
	}
}

func newPluginSearchCmd(newSvc func() *application.PluginService) *cobra.Command {
	return &cobra.Command{
		Use:   "search [query]",
		Short: "在已添加市场中检索可安装插件（按名称/描述匹配）",
		Long: "在所有已添加市场的清单中按关键词检索插件。\n" +
			"不带参数时列出全部可安装条目。",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := newSvc()
			query := ""
			if len(args) == 1 {
				query = args[0]
			}
			results := svc.SearchPlugins(query)
			if len(results) == 0 {
				if query == "" {
					fmt.Println("（已添加的市场中暂无可安装插件）")
				} else {
					fmt.Printf("未找到匹配 %q 的插件\n", query)
				}
				return nil
			}
			if query == "" {
				fmt.Printf("可安装插件 %d 个：\n\n", len(results))
			} else {
				fmt.Printf("匹配 %q 的插件 %d 个：\n\n", query, len(results))
			}
			for _, r := range results {
				ver := ""
				if r.Version != "" {
					ver = " v" + r.Version
				}
				tag := ""
				if r.Installed {
					tag = " [已安装]"
				}
				fmt.Printf("  %s%s  @%s%s\n", r.Name, ver, r.Marketplace, tag)
				if r.Description != "" {
					fmt.Printf("    %s\n", r.Description)
				}
				fmt.Printf("    安装: goclaude plugin install %s@%s\n", r.Name, r.Marketplace)
			}
			return nil
		},
	}
}

func newPluginInstallCmd(newSvc func() *application.PluginService) *cobra.Command {
	return &cobra.Command{
		Use:   "install <name@marketplace | source>",
		Short: "安装插件（来自市场或直接来源）",
		Args:  cobra.ExactArgs(1),
		// 补全市场中的可安装条目：同时给出裸名与 name@marketplace 两种候选。
		ValidArgsFunction: func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			svc := newSvc()
			seen := map[string]bool{}
			var out []string
			for _, m := range svc.ListMarketplaces() {
				for _, e := range m.Entries {
					if e.Name == "" {
						continue
					}
					if !seen[e.Name] {
						seen[e.Name] = true
						out = append(out, e.Name)
					}
					out = append(out, e.Name+"@"+m.Name)
				}
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := newSvc()
			p, err := svc.Install(context.Background(), args[0])
			if err != nil {
				return err
			}
			fmt.Printf("✓ 已安装插件 %s", p.Name)
			if p.Version != "" {
				fmt.Printf(" v%s", p.Version)
			}
			fmt.Println("（已启用；下次启动 REPL 生效）")
			return nil
		},
	}
}

func newPluginUninstallCmd(newSvc func() *application.PluginService) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "uninstall <name>",
		Aliases: []string{"remove", "rm"},
		Short:   "卸载插件",
		Args:    cobra.ExactArgs(1),
		// 补全已安装插件名（仅第一个参数）。
		ValidArgsFunction: installedPluginNameComp(newSvc),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := newSvc()
			if err := svc.Uninstall(context.Background(), args[0]); err != nil {
				return err
			}
			fmt.Printf("✓ 已卸载插件 %s\n", args[0])
			return nil
		},
	}
	return cmd
}

func newPluginToggleCmd(newSvc func() *application.PluginService, enable bool) *cobra.Command {
	use := "enable <name>"
	short := "启用插件"
	if !enable {
		use = "disable <name>"
		short = "禁用插件"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		// 补全已安装插件名（仅第一个参数）。
		ValidArgsFunction: installedPluginNameComp(newSvc),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := newSvc()
			var err error
			if enable {
				err = svc.Enable(args[0])
			} else {
				err = svc.Disable(args[0])
			}
			if err != nil {
				return err
			}
			verb := "启用"
			if !enable {
				verb = "禁用"
			}
			fmt.Printf("✓ 已%s插件 %s（下次启动 REPL 生效）\n", verb, args[0])
			return nil
		},
	}
}

func newPluginMarketplaceCmd(newSvc func() *application.PluginService) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "marketplace",
		Aliases: []string{"mkt"},
		Short:   "管理插件市场",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "add <source>",
		Short: "添加市场（本地目录 / git URL / http 压缩包）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := newSvc()
			m, err := svc.AddMarketplace(context.Background(), args[0])
			if err != nil {
				return err
			}
			fmt.Printf("✓ 已添加市场 %s [%s]，包含 %d 个插件\n", m.Name, m.Type, len(m.Entries))
			for _, e := range m.Entries {
				fmt.Printf("  - %s  %s\n", e.Name, e.Description)
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "列出已添加的市场",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := newSvc()
			mkts := svc.ListMarketplaces()
			if len(mkts) == 0 {
				fmt.Println("（暂无市场）")
				return nil
			}
			for _, m := range mkts {
				fmt.Printf("  %s [%s]  %d plugins\n", m.Name, m.Type, len(m.Entries))
				fmt.Printf("    source: %s\n", m.Source)
			}
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "移除市场",
		Args:    cobra.ExactArgs(1),
		// 补全已添加市场名（仅第一个参数）。
		ValidArgsFunction: func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			svc := newSvc()
			var out []string
			for _, m := range svc.ListMarketplaces() {
				if m.Name != "" {
					out = append(out, m.Name)
				}
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := newSvc()
			if err := svc.RemoveMarketplace(args[0]); err != nil {
				return err
			}
			fmt.Printf("✓ 已移除市场 %s\n", args[0])
			return nil
		},
	})

	return cmd
}

// installedPluginNameComp 返回一个补全函数：仅在第一个参数位补全已安装插件名。
//
// 供 uninstall / enable / disable 复用。
func installedPluginNameComp(newSvc func() *application.PluginService) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		svc := newSvc()
		var out []string
		for _, p := range svc.ListPlugins() {
			if p.Name != "" {
				out = append(out, p.Name)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}
