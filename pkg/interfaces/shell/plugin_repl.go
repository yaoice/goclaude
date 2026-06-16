package shell

import (
	"fmt"
	"strings"
)

// handlePluginCmd 处理 `/plugin [...]`：插件与市场的查看与启用/禁用。
//
// 用法：
//
//	/plugin                列出已安装插件与市场概览
//	/plugin list           列出已安装插件
//	/plugin search <kw>    在市场中检索可安装插件
//	/plugin marketplaces   列出已添加的市场
//	/plugin enable <name>  启用插件
//	/plugin disable <name> 禁用插件
//
// 安装/卸载/添加市场等"写"操作通过 CLI `goclaude plugin ...` 完成（涉及网络/磁盘）。
func (r *REPL) handlePluginCmd(args []string) { r.writeOut(r.renderPluginCmd(args)) }

// renderPluginCmd 是 handlePluginCmd 的纯函数实现，便于单测。
func (r *REPL) renderPluginCmd(args []string) string {
	if r.Plugins == nil {
		return r.colorize("（插件服务未启用）\r\n", colorYellow)
	}
	if len(args) == 0 {
		return r.renderPluginOverview()
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list", "ls":
		return r.renderPluginList()
	case "search", "find":
		return r.renderPluginSearch(strings.TrimSpace(strings.Join(rest, " ")))
	case "marketplaces", "marketplace", "mkt":
		return r.renderMarketplaceList()
	case "enable":
		if len(rest) == 0 {
			return r.colorize("用法: /plugin enable <name>\r\n", colorYellow)
		}
		if err := r.Plugins.EnablePlugin(rest[0]); err != nil {
			return r.colorize(fmt.Sprintf("启用失败: %v\r\n", err), colorError)
		}
		return r.colorize(fmt.Sprintf("✓ 已启用插件 %s（重启 REPL 生效）\r\n", rest[0]), colorGreen)
	case "disable":
		if len(rest) == 0 {
			return r.colorize("用法: /plugin disable <name>\r\n", colorYellow)
		}
		if err := r.Plugins.DisablePlugin(rest[0]); err != nil {
			return r.colorize(fmt.Sprintf("禁用失败: %v\r\n", err), colorError)
		}
		return r.colorize(fmt.Sprintf("✓ 已禁用插件 %s（重启 REPL 生效）\r\n", rest[0]), colorGreen)
	default:
		return r.colorize(fmt.Sprintf("未知子命令 %s\r\n", sub), colorYellow) +
			r.colorize("用法: /plugin [list|search <kw>|marketplaces|enable <name>|disable <name>]\r\n", colorDim)
	}
}

func (r *REPL) renderPluginOverview() string {
	var sb strings.Builder
	sb.WriteString(r.colorize("Plugins\r\n", colorCyan))
	sb.WriteString(r.renderPluginList())
	sb.WriteString(r.colorize("\r\nMarketplaces\r\n", colorCyan))
	sb.WriteString(r.renderMarketplaceList())
	sb.WriteString(r.colorize(
		"\r\n管理: CLI `goclaude plugin install <name@mkt>` / `uninstall` / `marketplace add <src>`\r\n",
		colorDim))
	return sb.String()
}

func (r *REPL) renderPluginList() string {
	plugins := r.Plugins.ListPlugins()
	if len(plugins) == 0 {
		return r.colorize("  （暂无已安装插件）\r\n", colorDim)
	}
	var sb strings.Builder
	for _, p := range plugins {
		state := r.colorize("enabled", colorGreen)
		if !p.Enabled {
			state = r.colorize("disabled", colorDim)
		}
		ver := p.Version
		if ver != "" {
			ver = " v" + ver
		}
		sb.WriteString("  " + r.colorize(p.Name, colorCyan) + r.colorize(ver, colorDim) +
			"  " + state)
		if p.Marketplace != "" {
			sb.WriteString(r.colorize("  @"+p.Marketplace, colorDim))
		}
		sb.WriteString("\r\n")
		if p.Description != "" {
			sb.WriteString("    " + r.colorize(truncOneLine(p.Description, 70), colorDim) + "\r\n")
		}
	}
	return sb.String()
}

func (r *REPL) renderPluginSearch(query string) string {
	hits := r.Plugins.SearchPlugins(query)
	if len(hits) == 0 {
		if query == "" {
			return r.colorize("  （已添加的市场中暂无可安装插件）\r\n", colorDim)
		}
		return r.colorize(fmt.Sprintf("  未找到匹配 %q 的插件\r\n", query), colorDim)
	}
	var sb strings.Builder
	for _, h := range hits {
		ver := h.Version
		if ver != "" {
			ver = " v" + ver
		}
		sb.WriteString("  " + r.colorize(h.Name, colorCyan) + r.colorize(ver, colorDim) +
			r.colorize("  @"+h.Marketplace, colorDim))
		if h.Installed {
			sb.WriteString("  " + r.colorize("[已安装]", colorGreen))
		}
		sb.WriteString("\r\n")
		if h.Description != "" {
			sb.WriteString("    " + r.colorize(truncOneLine(h.Description, 70), colorDim) + "\r\n")
		}
	}
	return sb.String()
}

func (r *REPL) renderMarketplaceList() string {
	mkts := r.Plugins.ListMarketplaces()
	if len(mkts) == 0 {
		return r.colorize("  （暂无市场，使用 `goclaude plugin marketplace add <src>` 添加）\r\n", colorDim)
	}
	var sb strings.Builder
	for _, m := range mkts {
		sb.WriteString("  " + r.colorize(m.Name, colorCyan) +
			r.colorize(fmt.Sprintf("  [%s]", m.Type), colorDim) +
			r.colorize(fmt.Sprintf("  %d plugins", m.PluginCount), colorDim) + "\r\n")
		if m.Source != "" {
			sb.WriteString("    " + r.colorize(truncOneLine(m.Source, 70), colorDim) + "\r\n")
		}
	}
	return sb.String()
}
