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

// SkillInvocation 描述一次 /<skill> 直接触发所需的信息
type SkillInvocation struct {
	// Name skill 名（规范名）
	Name string
	// Body 已渲染的 skill 正文（含参数替换与 ${CLAUDE_*} 替换）
	Body string
	// Fork 为 true 表示 frontmatter context: fork，应在独立子 agent 上下文执行
	Fork bool
	// Agent fork 执行时关联的 agent 类型；为空时由调用方回退到通用 agent
	Agent string
	// UserInvocable 是否允许用户通过 /<name> 手动触发
	UserInvocable bool
}

// SkillInvoker 可选接口：支持 /<skill名称> 直接触发。
//
// REPL 在 default 分支匹配到 skill 名时调用 Invoke 获取渲染正文与执行语义。
type SkillInvoker interface {
	// Invoke 解析并渲染指定 skill；ok=false 表示该名不是已注册 skill。
	Invoke(name, args string) (inv SkillInvocation, ok bool)
}

// PluginInfo shell 显示用的插件摘要
type PluginInfo struct {
	Name        string
	Version     string
	Description string
	Marketplace string
	Enabled     bool
}

// PluginMarketplaceInfo shell 显示用的市场摘要
type PluginMarketplaceInfo struct {
	Name        string
	Type        string
	Source      string
	PluginCount int
}

// PluginSearchHit shell 显示用的市场检索命中条目
type PluginSearchHit struct {
	Name        string
	Version     string
	Description string
	Marketplace string
	Installed   bool
}

// PluginManager 提供插件/市场的列表查询与启用/禁用（/plugin 面板使用）
type PluginManager interface {
	ListPlugins() []PluginInfo
	ListMarketplaces() []PluginMarketplaceInfo
	SearchPlugins(query string) []PluginSearchHit
	EnablePlugin(name string) error
	DisablePlugin(name string) error
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

// WorkflowInfo shell 显示用的 workflow 摘要
type WorkflowInfo struct {
	Name        string
	Description string
	NodeCount   int
}

// WorkflowGenerateResult Plan Agent 生成结果
type WorkflowGenerateResult struct {
	Name        string
	Description string
	NodeCount   int
	SavedPath   string
	RawJSON     string
	Workflow    *WorkflowInfo
}

// WorkflowManager 提供 workflow 列表/详情/执行/生成
type WorkflowManager interface {
	List() []WorkflowInfo
	Get(name string) (WorkflowInfo, bool)
	Run(ctx context.Context, name string) (*WorkflowRunResult, error)
	// RunOrGenerate 执行 workflow。如果 definition 文件不存在，通过 Plan Agent
	// 自动生成定义，保存后执行。对齐 oh-my-openagent 的 Plan Agent → Execute 链。
	RunOrGenerate(ctx context.Context, description string) (*WorkflowRunResult, bool, error)
	// Plan 通过 Plan Agent 分析用户请求，生成 workflow 定义（不立即执行）。
	Plan(ctx context.Context, description string) (*WorkflowGenerateResult, error)
	Status(name string) (*WorkflowStatusView, error)
	Cancel(name string) error
}

// WorkflowRunResult workflow 执行结果
type WorkflowRunResult struct {
	WorkflowName string
	Status       string
	TotalNodes   int
	Completed    int
	Failed       int
	Skipped      int
	Elapsed      string
	Output       string
}

// WorkflowStatusView 运行中 workflow 的状态视图
type WorkflowStatusView struct {
	Name        string
	Status      string
	CurrentWave int
	TotalWaves  int
	Nodes       []WorkflowNodeView
	Progress    float64
}

// WorkflowNodeView 单个节点视图
type WorkflowNodeView struct {
	NodeID string
	Name   string
	Status string
	Output string
	Error  string
}
