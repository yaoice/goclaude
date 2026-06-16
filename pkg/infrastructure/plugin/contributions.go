package plugininfra

import (
	"os"
	"path/filepath"

	"github.com/yaoice/goclaude/pkg/domain/plugin"
)

// ResolveContributions 把插件 manifest 解析为可投喂给各注册表的绝对路径集合。
//
// 路径解析规则（对齐 Claude 约定）：
//   - commands：manifest.Commands 指定的目录，缺省为 <root>/commands
//   - agents：  manifest.Agents 指定的目录，缺省为 <root>/agents
//   - skills：  manifest.Skills 指定的目录，缺省为 <root>/skills
//   - hooks：   manifest.Hooks 指定的文件，缺省为 <root>/hooks/hooks.json
//   - mcp：     manifest.MCPServers 指定的文件，缺省为 <root>/.mcp.json
//
// 仅当目标实际存在时才纳入，避免无谓加载。
func ResolveContributions(rootPath string, m *plugin.Manifest) plugin.Contributions {
	c := plugin.Contributions{Root: rootPath}
	if m != nil {
		c.PluginName = m.Name
	}

	addDir := func(dst *[]string, rels []string, def string) {
		paths := rels
		if len(paths) == 0 {
			paths = []string{def}
		}
		for _, rel := range paths {
			abs := resolveRel(rootPath, rel)
			if dirExists(abs) {
				*dst = append(*dst, abs)
			}
		}
	}
	addFile := func(dst *[]string, rel, def string) {
		p := rel
		if p == "" {
			p = def
		}
		abs := resolveRel(rootPath, p)
		if fileExists(abs) {
			*dst = append(*dst, abs)
		}
	}

	var commands, agents, skills []string
	if m != nil {
		commands = m.Commands
		agents = m.Agents
		skills = m.Skills
	}
	addDir(&c.CommandDirs, commands, "commands")
	addDir(&c.AgentDirs, agents, "agents")
	addDir(&c.SkillDirs, skills, "skills")

	hooks := ""
	mcp := ""
	if m != nil {
		hooks = m.Hooks
		mcp = m.MCPServers
	}
	addFile(&c.HookFiles, hooks, filepath.Join("hooks", "hooks.json"))
	addFile(&c.MCPConfigFiles, mcp, ".mcp.json")

	return c
}

func resolveRel(root, rel string) string {
	if filepath.IsAbs(rel) {
		return filepath.Clean(rel)
	}
	return filepath.Join(root, filepath.Clean(rel))
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
