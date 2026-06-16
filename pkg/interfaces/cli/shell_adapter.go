package cli

import (
	"context"

	"github.com/yaoice/goclaude/pkg/application"
	"github.com/yaoice/goclaude/pkg/domain/tool"
	"github.com/yaoice/goclaude/pkg/interfaces/shell"
)

// 本文件提供把 application/domain 层服务适配到 shell 包定义的 *Manager 接口的薄包装。
//
// 之所以放在 cli 层：
//   - shell 包不应直接依赖 application 层（保持 UI 层纯净）
//   - 该装配只在 cli 层启动 REPL 时使用一次

// ----- Skill 适配 -----

type skillAdapter struct {
	svc       *application.SkillService
	cwd       string
	sessionID string
}

func (a *skillAdapter) List() []shell.SkillInfo {
	out := make([]shell.SkillInfo, 0, 16)
	for _, s := range a.svc.List() {
		out = append(out, shell.SkillInfo{
			Name:        s.Name,
			Aliases:     s.Aliases,
			Description: s.Description,
			WhenToUse:   s.WhenToUse,
			Source:      string(s.Source),
			FilePath:    s.FilePath,
		})
	}
	return out
}

func (a *skillAdapter) Render(name string) (string, bool) {
	return a.svc.Render(name, "")
}

// Invoke 实现 shell.SkillInvoker：渲染 skill 正文并返回执行语义。
func (a *skillAdapter) Invoke(name, args string) (shell.SkillInvocation, bool) {
	sk, ok := a.svc.Get(name)
	if !ok {
		return shell.SkillInvocation{}, false
	}
	body, _ := a.svc.RenderWith(name, application.RenderContext{
		SessionID: a.sessionID,
		Cwd:       a.cwd,
		Args:      args,
	})
	return shell.SkillInvocation{
		Name:          sk.Name,
		Body:          body,
		Fork:          sk.ExecutionContext == "fork",
		Agent:         sk.Agent,
		UserInvocable: sk.UserInvocable,
	}, true
}

var _ shell.SkillInvoker = (*skillAdapter)(nil)

// ----- Plugin 适配 -----

type pluginAdapter struct{ svc *application.PluginService }

func (a *pluginAdapter) ListPlugins() []shell.PluginInfo {
	if a.svc == nil {
		return nil
	}
	out := make([]shell.PluginInfo, 0, 8)
	for _, p := range a.svc.ListPlugins() {
		out = append(out, shell.PluginInfo{
			Name:        p.Name,
			Version:     p.Version,
			Description: p.Description,
			Marketplace: p.Marketplace,
			Enabled:     p.Enabled,
		})
	}
	return out
}

func (a *pluginAdapter) ListMarketplaces() []shell.PluginMarketplaceInfo {
	if a.svc == nil {
		return nil
	}
	out := make([]shell.PluginMarketplaceInfo, 0, 8)
	for _, m := range a.svc.ListMarketplaces() {
		out = append(out, shell.PluginMarketplaceInfo{
			Name:        m.Name,
			Type:        string(m.Type),
			Source:      m.Source,
			PluginCount: len(m.Entries),
		})
	}
	return out
}

func (a *pluginAdapter) SearchPlugins(query string) []shell.PluginSearchHit {
	if a.svc == nil {
		return nil
	}
	results := a.svc.SearchPlugins(query)
	out := make([]shell.PluginSearchHit, 0, len(results))
	for _, r := range results {
		out = append(out, shell.PluginSearchHit{
			Name:        r.Name,
			Version:     r.Version,
			Description: r.Description,
			Marketplace: r.Marketplace,
			Installed:   r.Installed,
		})
	}
	return out
}

func (a *pluginAdapter) EnablePlugin(name string) error {
	if a.svc == nil {
		return nil
	}
	return a.svc.Enable(name)
}

func (a *pluginAdapter) DisablePlugin(name string) error {
	if a.svc == nil {
		return nil
	}
	return a.svc.Disable(name)
}

var _ shell.PluginManager = (*pluginAdapter)(nil)

// ----- Agent 适配 -----

type agentAdapter struct{ svc *application.AgentService }

func (a *agentAdapter) List() []shell.AgentInfo {
	defs := a.svc.List()
	out := make([]shell.AgentInfo, 0, len(defs))
	for _, d := range defs {
		out = append(out, shell.AgentInfo{
			AgentType:       d.AgentType,
			WhenToUse:       d.WhenToUse,
			Source:          string(d.Source),
			Model:           d.Model,
			Tools:           append([]string(nil), d.Tools...),
			DisallowedTools: append([]string(nil), d.DisallowedTools...),
			SystemPrompt:    d.SystemPrompt,
		})
	}
	return out
}

func (a *agentAdapter) Get(agentType string) (shell.AgentInfo, bool) {
	d, ok := a.svc.Get(agentType)
	if !ok {
		return shell.AgentInfo{}, false
	}
	return shell.AgentInfo{
		AgentType:       d.AgentType,
		WhenToUse:       d.WhenToUse,
		Source:          string(d.Source),
		Model:           d.Model,
		Tools:           append([]string(nil), d.Tools...),
		DisallowedTools: append([]string(nil), d.DisallowedTools...),
		SystemPrompt:    d.SystemPrompt,
	}, true
}

// ----- MCP 适配 -----

type mcpAdapter struct{ svc *application.MCPService }

func (a *mcpAdapter) Statuses() []shell.MCPServerStatus {
	rows := a.svc.Statuses()
	out := make([]shell.MCPServerStatus, 0, len(rows))
	for _, s := range rows {
		out = append(out, shell.MCPServerStatus{
			Name:      s.Name,
			Connected: s.Connected,
			Error:     s.Error,
		})
	}
	return out
}

func (a *mcpAdapter) Tools(ctx context.Context) ([]shell.MCPToolInfo, error) {
	tools, err := a.svc.ListAllTools(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]shell.MCPToolInfo, 0, len(tools))
	for _, t := range tools {
		out = append(out, shell.MCPToolInfo{
			Server:      t.Server,
			Name:        application.PrefixedToolName(t.Server, t.Tool.Name),
			Description: t.Tool.Description,
		})
	}
	return out, nil
}

// Reconnect 实现 shell.MCPReconnector 接口；让 dialog `/mcp` 的 r 键 / Reconnect
// 菜单项落地到 application.MCPService.Reconnect。
func (a *mcpAdapter) Reconnect(ctx context.Context, name string) error {
	return a.svc.Reconnect(ctx, name)
}

// 编译期保证：mcpAdapter 实现 shell.MCPReconnector
// 这样如果未来接口签名变化，会立刻在此处编译失败，避免 dialog `r` 键被悄悄退化为 no-op
var _ shell.MCPReconnector = (*mcpAdapter)(nil)

// ----- Team 适配 -----

type teamAdapter struct{ svc *application.TeamService }

func (a *teamAdapter) List() []shell.TeamInfo {
	names, err := a.svc.ListTeams()
	if err != nil {
		return nil
	}
	out := make([]shell.TeamInfo, 0, len(names))
	for _, n := range names {
		f, err := a.svc.GetTeam(n)
		if err != nil || f == nil {
			continue
		}
		out = append(out, shell.TeamInfo{
			Name:        f.Name,
			Description: f.Description,
			MemberCount: len(f.Members),
			TaskCount:   len(f.Tasks),
			CreatedAt:   f.CreatedAt,
		})
	}
	return out
}

func (a *teamAdapter) Get(name string) (shell.TeamInfo, bool) {
	f, err := a.svc.GetTeam(name)
	if err != nil || f == nil {
		return shell.TeamInfo{}, false
	}
	return shell.TeamInfo{
		Name:        f.Name,
		Description: f.Description,
		MemberCount: len(f.Members),
		TaskCount:   len(f.Tasks),
		CreatedAt:   f.CreatedAt,
	}, true
}

// ----- Tool Registry 适配 -----

type toolsAdapter struct{ reg *tool.Registry }

func (a *toolsAdapter) Names() []string {
	return a.reg.Names()
}

func (a *toolsAdapter) Describe(name string) (shell.ToolInfo, bool) {
	t, ok := a.reg.Get(name)
	if !ok {
		return shell.ToolInfo{}, false
	}
	return shell.ToolInfo{
		Name:        t.Name(),
		Description: t.Description(),
	}, true
}
