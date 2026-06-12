// Package skill 定义 Skills 系统领域模型
//
// Skills 是带 frontmatter 的 markdown 文件（skill-name/SKILL.md），
// 提供可被模型按需调用的 prompt 包。对齐 src/skills/loadSkillsDir.ts 的语义。
package skill

import (
	"context"
	"sync"
)

// Source 技能来源（对齐 src 中的 SettingSource + 'bundled' | 'mcp' | 'plugin'）
type Source string

const (
	SourceBundled        Source = "bundled"
	SourceUser           Source = "userSettings"
	SourceProject        Source = "projectSettings"
	SourceManaged        Source = "policySettings"
	SourceMCP            Source = "mcp"
	SourcePlugin         Source = "plugin"
	SourceFlag           Source = "flagSettings"
	SourceCommandsLegacy Source = "commands_DEPRECATED"
)

// LoadedFrom 加载来源（对齐 src 中的 LoadedFrom）
type LoadedFrom string

const (
	LoadedFromSkills         LoadedFrom = "skills"
	LoadedFromCommandsLegacy LoadedFrom = "commands_DEPRECATED"
	LoadedFromPlugin         LoadedFrom = "plugin"
	LoadedFromManaged        LoadedFrom = "managed"
	LoadedFromBundled        LoadedFrom = "bundled"
	LoadedFromMCP            LoadedFrom = "mcp"
)

// Frontmatter Skill 文件 frontmatter 字段集合（对齐 parseSkillFrontmatterFields）
type Frontmatter struct {
	Name                   string   `yaml:"name"`
	Description            string   `yaml:"description"`
	Aliases                []string `yaml:"aliases,omitempty"`
	WhenToUse              string   `yaml:"when_to_use,omitempty"`
	AllowedTools           []string `yaml:"allowed-tools,omitempty"`
	ArgumentHint           string   `yaml:"argument-hint,omitempty"`
	Arguments              []string `yaml:"arguments,omitempty"`
	Model                  string   `yaml:"model,omitempty"`
	Version                string   `yaml:"version,omitempty"`
	DisableModelInvocation bool     `yaml:"disable-model-invocation,omitempty"`
	UserInvocable          *bool    `yaml:"user-invocable,omitempty"` // 默认 true
	Context                string   `yaml:"context,omitempty"`        // "fork" | ""
	Agent                  string   `yaml:"agent,omitempty"`
	Effort                 string   `yaml:"effort,omitempty"`
	Paths                  []string `yaml:"paths,omitempty"` // 条件激活
}

// Skill 技能实体
type Skill struct {
	// Name 技能名称（唯一标识符）
	Name string
	// DisplayName 用户可见名称（默认 = Name）
	DisplayName string
	// Description 描述
	Description string
	// Aliases 别名
	Aliases []string
	// WhenToUse 何时使用（用于 AI 选择）
	WhenToUse string
	// ArgumentHint 参数提示
	ArgumentHint string
	// ArgumentNames 参数名列表
	ArgumentNames []string
	// AllowedTools 允许使用的工具
	AllowedTools []string
	// Model 指定模型
	Model string
	// Version 版本
	Version string
	// DisableModelInvocation 模型不可直接调用
	DisableModelInvocation bool
	// UserInvocable 用户可通过 /name 调用
	UserInvocable bool
	// IsHidden 不在用户列表中显示
	IsHidden bool
	// ExecutionContext "fork" 表示在子 agent 中执行
	ExecutionContext string
	// Agent 关联的 agent 类型
	Agent string
	// Paths 条件激活的路径模式（gitignore 语法）
	Paths []string
	// Content 主体 markdown 内容
	Content string
	// ContentLength 内容长度
	ContentLength int
	// SkillRoot 该 skill 所在目录（用于 ${CLAUDE_SKILL_DIR} 替换）
	SkillRoot string
	// FilePath 加载来源文件路径（用于去重）
	FilePath string
	// Source 配置来源
	Source Source
	// LoadedFrom 加载机制
	LoadedFrom LoadedFrom
	// IsEnabled 是否启用
	IsEnabled bool
}

// UserFacingName 用户可见名（DisplayName 优先）
func (s *Skill) UserFacingName() string {
	if s.DisplayName != "" {
		return s.DisplayName
	}
	return s.Name
}

// Loader 技能加载器接口
type Loader interface {
	// LoadFromDir 从指定 skills 目录加载（目录格式：<skill-name>/SKILL.md）
	LoadFromDir(ctx context.Context, dir string, source Source) ([]*Skill, error)
	// LoadBundled 加载内置 skills
	LoadBundled(ctx context.Context) ([]*Skill, error)
}

// Registry 技能注册表（线程安全）
type Registry struct {
	mu sync.RWMutex

	// skills 主索引：name -> Skill
	skills map[string]*Skill
	// aliases 别名索引：alias -> name
	aliases map[string]string
	// conditional 待激活的条件 skill（具备 paths frontmatter）
	conditional map[string]*Skill
	// activated 已激活的条件 skill 名（防止重复激活）
	activated map[string]bool
}

// NewRegistry 创建注册表
func NewRegistry() *Registry {
	return &Registry{
		skills:      make(map[string]*Skill),
		aliases:     make(map[string]string),
		conditional: make(map[string]*Skill),
		activated:   make(map[string]bool),
	}
}

// Register 注册 skill。若已存在同名 skill，新值覆盖（与 src 的优先级合并一致）
func (r *Registry) Register(s *Skill) {
	if s == nil || s.Name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.register(s)
}

// register 内部注册（调用方持锁）
func (r *Registry) register(s *Skill) {
	r.skills[s.Name] = s
	for _, alias := range s.Aliases {
		if alias != "" {
			r.aliases[alias] = s.Name
		}
	}
}

// RegisterConditional 注册条件 skill（带 paths frontmatter，待文件匹配时激活）
func (r *Registry) RegisterConditional(s *Skill) {
	if s == nil || s.Name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.activated[s.Name] {
		// 已激活过的直接进主索引
		r.register(s)
		return
	}
	r.conditional[s.Name] = s
}

// Get 获取 skill（支持别名）
func (r *Registry) Get(name string) (*Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if s, ok := r.skills[name]; ok {
		return s, true
	}
	if realName, ok := r.aliases[name]; ok {
		if s, ok := r.skills[realName]; ok {
			return s, true
		}
	}
	return nil, false
}

// Has 是否存在
func (r *Registry) Has(name string) bool {
	_, ok := r.Get(name)
	return ok
}

// All 返回所有非条件 skill（去重，按 Name 升序由调用方处理）
func (r *Registry) All() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Skill, 0, len(r.skills))
	for _, s := range r.skills {
		out = append(out, s)
	}
	return out
}

// Enabled 返回所有启用的 skill
func (r *Registry) Enabled() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Skill, 0, len(r.skills))
	for _, s := range r.skills {
		if s.IsEnabled {
			out = append(out, s)
		}
	}
	return out
}

// Conditional 返回所有等待激活的条件 skill
func (r *Registry) Conditional() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Skill, 0, len(r.conditional))
	for _, s := range r.conditional {
		out = append(out, s)
	}
	return out
}

// Activate 激活一个条件 skill（由路径匹配触发）。返回是否真的发生了激活
func (r *Registry) Activate(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.conditional[name]
	if !ok {
		return false
	}
	delete(r.conditional, name)
	r.activated[name] = true
	r.register(s)
	return true
}

// Clear 重置（测试使用）
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skills = make(map[string]*Skill)
	r.aliases = make(map[string]string)
	r.conditional = make(map[string]*Skill)
	r.activated = make(map[string]bool)
}
