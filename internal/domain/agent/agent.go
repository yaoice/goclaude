// Package agent 定义 Subagent 系统领域模型
//
// 对齐 src/tools/AgentTool/loadAgentsDir.ts 中 AgentDefinition 的核心字段。
// 与 TS 实现一致的取舍：
//   - 同一 agentType 在不同来源中按优先级合并（built-in < plugin < user < project < flag < policy）
//   - 必须有 name (=agentType) 与 description (=whenToUse)
package agent

import (
	"context"
	"sync"
)

// Source 来源
type Source string

const (
	SourceBuiltIn Source = "built-in"
	SourceUser    Source = "userSettings"
	SourceProject Source = "projectSettings"
	SourceManaged Source = "policySettings"
	SourcePlugin  Source = "plugin"
	SourceFlag    Source = "flagSettings"
)

// Definition Agent 定义
//
// 对齐 TS 中的 BaseAgentDefinition + CustomAgentDefinition 字段集。
type Definition struct {
	// AgentType 唯一标识，对应 frontmatter.name
	AgentType string
	// WhenToUse 描述，对应 frontmatter.description
	WhenToUse string
	// Tools 允许使用的工具白名单（nil 表示继承父 agent 的全部工具）
	Tools []string
	// DisallowedTools 禁用的工具
	DisallowedTools []string
	// Model 指定模型；"inherit" 表示继承
	Model string
	// PermissionMode 权限模式
	PermissionMode string
	// MaxTurns 最大轮数（0 表示用引擎默认）
	MaxTurns int
	// Skills 启动时预加载的 skill 名列表
	Skills []string
	// MCPServers 该 agent 需要连接的 MCP 服务器名列表
	MCPServers []string
	// SystemPrompt 系统提示词（对于 built-in 由 GetSystemPrompt 提供）
	SystemPrompt string
	// GetSystemPrompt 动态系统提示词；优先于 SystemPrompt
	GetSystemPrompt func() string `json:"-"`
	// Source 来源
	Source Source
	// FilePath 文件路径（custom agent 时存在）
	FilePath string
	// BaseDir 该 agent 所在目录
	BaseDir string
	// Background 是否后台运行
	Background bool
	// Color 颜色提示
	Color string
	// Effort 推理努力等级
	Effort string
	// Memory 持久记忆作用域（""/user/project/local）
	Memory string
	// Isolation 隔离模式（""/worktree）
	Isolation string
	// AllowSubagentChaining 是否允许该 subagent 持有"会再次启动 subagent /
	// 跨 subagent 通信"的工具（Agent/Task/team_*/send_message）。
	//
	// 默认 false：application.FilterTools 会从工具集中硬剔除这些保留工具，
	// 保证文章中"子 ↔ 子不直接通信、主进程唯一调度"的契约。
	//
	// 仅当用户明确知道在做"调度型 subagent"分发工作（且自行接受失去观察性的
	// 代价）时，才在 Definition frontmatter 中显式置 true。
	AllowSubagentChaining bool
}

// ResolvedSystemPrompt 返回 GetSystemPrompt（若存在）或 SystemPrompt
func (d *Definition) ResolvedSystemPrompt() string {
	if d.GetSystemPrompt != nil {
		if s := d.GetSystemPrompt(); s != "" {
			return s
		}
	}
	return d.SystemPrompt
}

// Registry agent 注册表（线程安全）
type Registry struct {
	mu      sync.RWMutex
	agents  map[string]*Definition
	sources map[string]Source // agentType -> 最近写入的 source
}

// NewRegistry 创建注册表
func NewRegistry() *Registry {
	return &Registry{
		agents:  make(map[string]*Definition),
		sources: make(map[string]Source),
	}
}

// 优先级（越大越高）
var sourcePriority = map[Source]int{
	SourceBuiltIn: 1,
	SourcePlugin:  2,
	SourceUser:    3,
	SourceProject: 4,
	SourceFlag:    5,
	SourceManaged: 6,
}

// Register 注册一个 agent；高优先级 source 覆盖低优先级 source
func (r *Registry) Register(d *Definition) {
	if d == nil || d.AgentType == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.sources[d.AgentType]; ok {
		if sourcePriority[d.Source] < sourcePriority[existing] {
			return // 已有更高优先级版本
		}
	}
	r.agents[d.AgentType] = d
	r.sources[d.AgentType] = d.Source
}

// Get 获取
func (r *Registry) Get(agentType string) (*Definition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.agents[agentType]
	return d, ok
}

// All 返回所有
func (r *Registry) All() []*Definition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Definition, 0, len(r.agents))
	for _, d := range r.agents {
		out = append(out, d)
	}
	return out
}

// Clear 清空（测试）
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents = make(map[string]*Definition)
	r.sources = make(map[string]Source)
}

// Loader Agent 加载器接口
type Loader interface {
	LoadFromDir(ctx context.Context, dir string, source Source) ([]*Definition, error)
}
