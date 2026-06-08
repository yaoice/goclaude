package application

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/anthropics/goclaude/pkg/domain/agent"
	"github.com/anthropics/goclaude/pkg/domain/hook"
	agentinfra "github.com/anthropics/goclaude/pkg/infrastructure/agent"
	"github.com/anthropics/goclaude/pkg/infrastructure/memory"
	"github.com/anthropics/goclaude/pkg/infrastructure/worktree"
)

// AgentService Subagent 应用服务
//
// 编排 Registry / Loader / 执行：
//   - 启动时合并内置 + 文件系统 agents
//   - 提供执行单个 subagent 的 RunAgent（与 query.Engine 集成）
//   - 可选的 Memory / Worktree 子系统按 Definition 字段触发
//
// 对齐 src/tools/AgentTool/runAgent.ts 的核心语义。
type AgentService struct {
	registry  *agent.Registry
	loader    *agentinfra.Loader
	memorySvc *memory.Service
	wtSvc     *worktree.Service
	skillSvc  *SkillService
	hooks     *hook.Registry
	logger    *slog.Logger

	listenerMu sync.RWMutex
	listener   SubagentEventListener
}

// SubagentPhase subagent 生命周期阶段。
type SubagentPhase string

const (
	SubagentPhaseStart    SubagentPhase = "start"
	SubagentPhaseProgress SubagentPhase = "progress"
	SubagentPhaseFinish   SubagentPhase = "finish"
)

// SubagentStatus 终态。
type SubagentStatus string

const (
	SubagentStatusSuccess SubagentStatus = "success"
	SubagentStatusError   SubagentStatus = "error"
)

// SubagentEvent 描述一次 subagent 生命周期事件。
//
// 三个 Phase 的语义：
//   - Start：subagent 已建好独立 Engine、工具白名单、PermContext，即将发送第一轮请求。
//     携带：AgentID/AgentType/Model/Memory/Isolation/ParentSession。
//   - Progress：每完成一轮 LLM 交互（无论是否触发工具）回放一次，便于上层做"心跳"展示。
//     携带：Turns（当前已完成轮数）/LastTool（本轮触发的最后一个工具名，若有）。
//   - Finish：subagent 结束（成功/错误/被取消）。
//     携带：Status/Elapsed/Turns/ErrorMessage/ResultPreview。
//
// CLI/REPL 监听这些事件后渲染为对齐的 `◇ subagent <type> ⏵`/`◆ subagent <type> ✔ 1.2s` 行，
// 替代旧版本 stdlib log 直接打印的 `INFO subagent 启动 agent_id=...` 多协程乱序输出。
type SubagentEvent struct {
	Phase         SubagentPhase
	AgentID       string
	AgentType     string
	Model         string
	Memory        string
	Isolation     string
	ParentSession string
	Turns         int
	Status        SubagentStatus
	Elapsed       time.Duration
	ErrorMessage  string
	// LastTool 仅 Progress 阶段填充；记录本轮 subagent 实际调用的最后一个工具名，
	// 用于 UI 展示"当前在做什么"，避免对长时间运行的 subagent 失去观感。
	LastTool string
	// LastToolDetail 仅 Progress 阶段填充；记录 LastTool 调用的参数摘要
	// （如 bash 命令、文件路径、搜索 pattern），让用户看清 subagent 具体在执行什么。
	// 可为空（工具无可读摘要时）。
	LastToolDetail string
	// ResultPreview 仅 Finish.Status=Success 阶段填充；
	// 取 subagent 最终回复的首行（截断到 80 字符），用于在主对话外快速看到摘要。
	ResultPreview string
}

// SubagentEventListener subagent 事件订阅者。
type SubagentEventListener interface {
	HandleSubagentEvent(ev SubagentEvent)
}

// SubagentEventListenerFunc 函数适配器。
type SubagentEventListenerFunc func(ev SubagentEvent)

// HandleSubagentEvent 实现 SubagentEventListener。
func (f SubagentEventListenerFunc) HandleSubagentEvent(ev SubagentEvent) { f(ev) }

// SetSubagentEventListener 注入监听器；nil 表示禁用。
func (s *AgentService) SetSubagentEventListener(l SubagentEventListener) {
	s.listenerMu.Lock()
	defer s.listenerMu.Unlock()
	s.listener = l
}

func (s *AgentService) publishSubagent(ev SubagentEvent) {
	s.listenerMu.RLock()
	l := s.listener
	s.listenerMu.RUnlock()
	if l != nil {
		l.HandleSubagentEvent(ev)
	}
}

// NewAgentService 创建 AgentService 并加载内置 agents
func NewAgentService(logger *slog.Logger) *AgentService {
	if logger == nil {
		logger = slog.Default()
	}
	svc := &AgentService{
		registry: agent.NewRegistry(),
		loader:   agentinfra.NewLoader(),
		logger:   logger,
	}
	for _, a := range agentinfra.BuiltInAgents() {
		svc.registry.Register(a)
	}
	return svc
}

// EnableMemory 注入 memory 子系统；启用后定义中带 memory: <scope> 的 subagent 启动时
// 会自动加载持久化记忆并附加到系统提示。
func (s *AgentService) EnableMemory(svc *memory.Service) { s.memorySvc = svc }

// EnableWorktree 注入 worktree 子系统；启用后定义中带 isolation: worktree 的 subagent
// 会在隔离的 git worktree 中执行，结束后自动清理。
func (s *AgentService) EnableWorktree(svc *worktree.Service) { s.wtSvc = svc }

// EnableSkills 注入 SkillService；启用后 Definition.Skills 会在 subagent 启动前预加载。
func (s *AgentService) EnableSkills(svc *SkillService) { s.skillSvc = svc }

// EnableHooks 注入 hook 注册表；subagent 启动/退出会触发对应事件
func (s *AgentService) EnableHooks(reg *hook.Registry) { s.hooks = reg }

// Registry 暴露底层注册表
func (s *AgentService) Registry() *agent.Registry {
	return s.registry
}

// LoadAll 加载用户/项目自定义 agents
func (s *AgentService) LoadAll(ctx context.Context, projectCwd, managedDir string) error {
	userDir := s.loader.DefaultUserAgentsDir()
	projectDirs := s.loader.ProjectAgentsDirs(projectCwd)
	defs, err := s.loader.LoadAll(ctx, managedDir, userDir, projectDirs)
	if err != nil {
		return err
	}
	for _, d := range defs {
		s.registry.Register(d)
	}
	s.logger.Debug("agents 加载完成",
		"user_dir", userDir,
		"project_dirs", projectDirs,
		"loaded", len(defs),
		"total", len(s.registry.All()),
	)
	return nil
}

// List 返回所有 agent，按名字排序
func (s *AgentService) List() []*agent.Definition {
	defs := s.registry.All()
	sort.Slice(defs, func(i, j int) bool {
		return defs[i].AgentType < defs[j].AgentType
	})
	return defs
}

// Get 按 agentType 获取
func (s *AgentService) Get(agentType string) (*agent.Definition, bool) {
	return s.registry.Get(agentType)
}
