package shell

import "context"

// 这一层定义 shell 需要的"只读"管理接口，避免直接依赖 application/infrastructure 层。
//
// REPL 实例由 cli 层装配时传入具体实现（即 application.SkillService 等）；
// 接口形态对齐现有服务的方法签名，调用方做一个薄适配即可。

// SkillInfo shell 显示用的 skill 摘要
type SkillInfo struct {
	Name        string
	Aliases     []string
	Description string
	WhenToUse   string
	Source      string
	FilePath    string
}

// AgentInfo shell 显示用的 agent 摘要
type AgentInfo struct {
	AgentType       string
	WhenToUse       string
	Source          string
	Model           string
	Tools           []string
	DisallowedTools []string
	SystemPrompt    string
}

// MCPServerStatus MCP 服务器连接状态
type MCPServerStatus struct {
	Name      string
	Connected bool
	Error     string
}

// MCPToolInfo MCP 工具
type MCPToolInfo struct {
	Server      string
	Name        string // 含前缀，如 mcp__github__list_issues
	Description string
}

// ToolInfo 通用工具（含本地 + MCP）
type ToolInfo struct {
	Name        string
	Description string
}

// SkillManager 提供 skill 列表/详情查询
type SkillManager interface {
	List() []SkillInfo
	Render(name string) (string, bool)
}

// AgentManager 提供 agent 列表/详情查询
type AgentManager interface {
	List() []AgentInfo
	Get(agentType string) (AgentInfo, bool)
}

// MCPManager 提供 MCP 状态/工具/重连
type MCPManager interface {
	Statuses() []MCPServerStatus
	Tools(ctx context.Context) ([]MCPToolInfo, error)
}

// MCPReconnector 可选接口：实现时支持单服务器重连
//
// 与 src `MCPReconnect` 组件 + `useMcpReconnect()` 对齐
type MCPReconnector interface {
	Reconnect(ctx context.Context, serverName string) error
}

// MCPToggler 可选接口：实现时支持 enable/disable 服务器
//
// 与 src `useMcpToggleEnabled()` 对齐
type MCPToggler interface {
	Toggle(serverName string, enable bool) error
}

// AgentDetailProvider 可选接口：返回更详细的 agent 信息（位置/颜色等）
type AgentDetailProvider interface {
	Detail(agentType string) (AgentInfo, string /*filePath*/, bool)
}

// TeamInfo shell 显示用的 team 摘要
type TeamInfo struct {
	Name        string
	Description string
	MemberCount int
	TaskCount   int
	CreatedAt   int64
}

// TeamManager 提供 team 列表/详情查询
type TeamManager interface {
	List() []TeamInfo
	Get(name string) (TeamInfo, bool)
}

// ToolRegistryView 提供已注册的所有工具
type ToolRegistryView interface {
	Names() []string
	Describe(name string) (ToolInfo, bool)
}
