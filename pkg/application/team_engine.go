package application

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/goclaude/pkg/domain/team"
	"github.com/anthropics/goclaude/pkg/infrastructure/appconfig"
)

// TeamEngine 管理 team member 的 spawn/stop 生命周期。
//
// 每个 team member 是一个在独立 goroutine 中运行的 memberWorker，
// 拥有自己的 query.Engine 用于执行任务。TeamEngine 为每个 team
// 创建专属 workspace 子目录，所有 member worker 共享该目录写入产物。
//
// TeamEngine 被 TeamService 和 REPL 层引用：
//   - team_create / auto_setup_team 调用 SpawnMembers 自动启动物 member
//   - team_delete 调用 ShutdownAll 关闭所有 member
//   - REPL 退出时调用 ShutdownAll 清理
type TeamEngine struct {
	agentSvc *AgentService
	teamSvc  *TeamService
	factory  AgentEngineFactory
	logger   *slog.Logger

	mu      sync.Mutex
	workers map[string]*memberWorker // key: "<teamName>/<memberName>"

	// teamWorkspaces 记录每个 team 专属的产物目录（teamName → path）
	// 避免每次 spawn 都创建新目录
	teamWorkspaces map[string]string

	// 默认配置
	defaultModel      string
	defaultProjectRoot string
	defaultMaxTurns   int
	pollInterval      time.Duration
	taskTimeout       time.Duration
	shutdownTimeout   time.Duration
}

// TeamEngineConfig 配置 TeamEngine 的可选参数。
type TeamEngineConfig struct {
	// DefaultModel team member 默认使用的模型；为空则继承
	DefaultModel string
	// ProjectRoot 项目根目录，用于计算 team workspace 路径
	ProjectRoot string
	// MaxTurns 单个任务最大轮数（0 表示用 agent 定义或引擎默认值）
	MaxTurns int
	// PollInterval inbox 轮询间隔
	PollInterval time.Duration
	// TaskTimeout 单个任务执行超时
	TaskTimeout time.Duration
	// ShutdownTimeout 关闭 member 的等待超时
	ShutdownTimeout time.Duration
}

// DefaultTeamEngineConfig 返回默认配置。
func DefaultTeamEngineConfig() TeamEngineConfig {
	return TeamEngineConfig{
		DefaultModel:    "inherit",
		ProjectRoot:     "",
		MaxTurns:        0, // 0 = 不做额外限制，用 agent 定义或引擎默认
		PollInterval:    5 * time.Second,
		TaskTimeout:     5 * time.Minute,
		ShutdownTimeout: 30 * time.Second,
	}
}

// NewTeamEngine 创建 TeamEngine。
//
// agentSvc 必须已完成 LoadAll()，确保 team-worker agent 可用。
// factory 用于为每个 member 构造子 Engine。
func NewTeamEngine(
	agentSvc *AgentService,
	teamSvc *TeamService,
	factory AgentEngineFactory,
	cfg TeamEngineConfig,
	logger *slog.Logger,
) *TeamEngine {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5 * time.Second
	}
	if cfg.TaskTimeout <= 0 {
		cfg.TaskTimeout = 5 * time.Minute
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 30 * time.Second
	}
	return &TeamEngine{
		agentSvc:          agentSvc,
		teamSvc:           teamSvc,
		factory:           factory,
		logger:            logger.With(slog.String("component", "team_engine")),
		workers:           make(map[string]*memberWorker),
		teamWorkspaces:    make(map[string]string),
		defaultModel:      cfg.DefaultModel,
		defaultProjectRoot: cfg.ProjectRoot,
		pollInterval:      cfg.PollInterval,
		taskTimeout:       cfg.TaskTimeout,
		shutdownTimeout:   cfg.ShutdownTimeout,
		defaultMaxTurns:   cfg.MaxTurns,
	}
}

// workerKey 生成内部索引键。
func workerKey(teamName, memberName string) string {
	return teamName + "/" + memberName
}

// SpawnMembers 为 team 的所有非 leader 成员启动 worker goroutine。
//
// 调用时机：team_create / auto_setup_team 成功后调用。
// 幂等：已存在的 member 不会重复启动。
func (e *TeamEngine) SpawnMembers(ctx context.Context, teamName string) error {
	f, err := e.teamSvc.GetTeam(teamName)
	if err != nil {
		return fmt.Errorf("get team: %w", err)
	}
	if f == nil {
		return fmt.Errorf("team %q not found", teamName)
	}

	var spawned int
	for _, m := range f.NonLeaderMembers() {
		if err := e.SpawnMember(ctx, teamName, m.Name, m.AgentType); err != nil {
			e.logger.Warn("spawn member failed",
				"team", teamName,
				"member", m.Name,
				"error", err,
			)
			continue
		}
		spawned++
	}

	e.logger.Info("spawned team members",
		"team", teamName,
		"count", spawned,
	)
	return nil
}

// SpawnMember 启动单个 team member 的 worker goroutine。
//
// agentType 为空或在 registry 中找不到时自动回退为 "team-worker"。
// 幂等：已存在的 member 不会重复启动。
func (e *TeamEngine) SpawnMember(ctx context.Context, teamName, memberName, agentType string) error {
	if agentType == "" {
		agentType = "team-worker"
	}

	key := workerKey(teamName, memberName)

	e.mu.Lock()
	if _, exists := e.workers[key]; exists {
		e.mu.Unlock()
		e.logger.Debug("member already running", "team", teamName, "member", memberName)
		return nil
	}
	e.mu.Unlock()

	// 检查 agent type 是否存在；不存在则回退到 team-worker
	_, ok := e.agentSvc.registry.Get(agentType)
	if !ok {
		e.logger.Info("agent type not found, falling back to team-worker",
			"team", teamName,
			"member", memberName,
			"requested", agentType,
		)
		agentType = "team-worker"
	}

	w := newMemberWorker(
		teamName, memberName, agentType,
		e.defaultModel,
		e.teamSvc,
		e.agentSvc,
		e.factory,
		e.logger,
	)
		w.pollInterval = e.pollInterval
	w.taskTimeout = e.taskTimeout
	w.maxTurns = e.defaultMaxTurns

	// 注入产物输出路径：team 共享 workspace
	ws, workingDir, projectRoot := e.getOrCreateTeamWorkspace(teamName)
	w.workspaceRoot = ws
	w.workingDir = workingDir
	w.projectRoot = projectRoot

	e.mu.Lock()
	e.workers[key] = w
	e.mu.Unlock()

	w.start()

	e.logger.Info("spawned team member",
		"team", teamName,
		"member", memberName,
		"agent_type", agentType,
		"workspace", ws,
	)
	return nil
}

// getOrCreateTeamWorkspace 获取或创建 team 的产物输出目录。
//
// 路径规则：<projectRoot>/.goclaude/workspaces/team-<sanitized_name>/
// 不包含时间戳，同一 team 多次启动复用同一目录。
//
// 返回 (workspaceRoot, workingDir, projectRoot).
//   - workspaceRoot: team 专属目录，所有 member 共享
//   - workingDir: 项目根目录
//   - projectRoot: 项目根目录
//
// projectRoot 为空时，workspaceRoot 也为空（不限制产物路径）。
func (e *TeamEngine) getOrCreateTeamWorkspace(teamName string) (workspaceRoot, workingDir, projectRoot string) {
	projectRoot = e.defaultProjectRoot
	if projectRoot == "" {
		return "", "", ""
	}

	// 检查缓存
	e.mu.Lock()
	if ws, ok := e.teamWorkspaces[teamName]; ok {
		e.mu.Unlock()
		return ws, projectRoot, projectRoot
	}
	e.mu.Unlock()

	// 稳定路径：<workspace_root>/team-<sanitized_name>/
	// 去时间戳，同级目录清晰可辨
	cfg := appconfig.DefaultConfig()
	wsRoot := cfg.WorkspaceRoot(projectRoot)
	sanitized := sanitizeTeamName(teamName)
	ws := filepath.Join(wsRoot, "team-"+sanitized)

	if err := os.MkdirAll(ws, 0755); err != nil {
		e.logger.Warn("failed to create team workspace",
			"team", teamName,
			"path", ws,
			"error", err,
		)
		ws = projectRoot
	}

	// 写入 .identity 标记该目录身份
	_ = os.WriteFile(filepath.Join(ws, ".identity"),
		[]byte(fmt.Sprintf("team:%s\ncreated:%s\n", teamName, time.Now().Format(time.RFC3339))),
		0644)

	// 缓存
	e.mu.Lock()
	e.teamWorkspaces[teamName] = ws
	e.mu.Unlock()

	e.logger.Info("team workspace ready",
		"team", teamName,
		"path", ws,
	)
	return ws, projectRoot, projectRoot
}

// sanitizeTeamName 规范化 team 名为安全目录名
func sanitizeTeamName(name string) string {
	s := strings.ToLower(name)
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, s)
	// 去除连续和首尾的 '-'
	s = strings.Trim(s, "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	if s == "" {
		s = "team"
	}
	return s
}

// ShutdownMember 向指定 member 发送 shutdown 信号并等待退出。
//
// 流程：
//  1. 通过 inbox 发送 shutdown_request（worker 的 ticker 会捡起并处理）
//  2. 等待 worker goroutine 自行退出（处理 shutdown → cleanup → close(done)）
//  3. 超时后强制取消 context
//  4. 从 workers map 中移除
func (e *TeamEngine) ShutdownMember(ctx context.Context, teamName, memberName string) error {
	key := workerKey(teamName, memberName)

	e.mu.Lock()
	w, exists := e.workers[key]
	if !exists {
		e.mu.Unlock()
		e.logger.Debug("member not found for shutdown", "key", key)
		return nil
	}
	e.mu.Unlock()

	// 1) 通过 inbox 发送 shutdown_request
	shutdownMsg := team.NewShutdownRequest(team.LeaderName, "", "team engine shutdown")
	e.teamSvc.Send(SendInput{
		TeamName:   teamName,
		From:       team.LeaderName,
		To:         memberName,
		Structured: &shutdownMsg,
	})

	// 2) 先等待 worker 自行退出（处理 inbox → shutdown_response → cleanup）
	select {
	case <-w.done:
		e.logger.Info("member shut down gracefully", "key", key)
	case <-time.After(e.shutdownTimeout):
		e.logger.Warn("member shutdown timed out, forcing", "key", key)
		// 强制取消
		if err := w.stop(2 * time.Second); err != nil {
			e.logger.Warn("force stop failed", "key", key, "error", err)
		}
	}

	e.mu.Lock()
	delete(e.workers, key)
	e.mu.Unlock()

	return nil
}

// ShutdownAll 关闭指定 team 的所有 member。
func (e *TeamEngine) ShutdownAll(ctx context.Context, teamName string) {
	e.mu.Lock()
	type workItem struct {
		key        string
		memberName string
	}
	var items []workItem
	for key, w := range e.workers {
		if w.teamName == teamName {
			items = append(items, workItem{key: key, memberName: w.memberName})
		}
	}
	e.mu.Unlock()

	if len(items) == 0 {
		return
	}

	e.logger.Info("shutting down all team members",
		"team", teamName,
		"count", len(items),
	)

	var wg sync.WaitGroup
	for _, item := range items {
		wg.Add(1)
		go func(item workItem) {
			defer wg.Done()
			if err := e.ShutdownMember(ctx, teamName, item.memberName); err != nil {
				e.logger.Warn("shutdown member failed",
					"key", item.key,
					"error", err,
				)
			}
		}(item)
	}
	wg.Wait()
}

// ShutdownAllTeams 关闭所有 team 的所有 member。
func (e *TeamEngine) ShutdownAllTeams(ctx context.Context) {
	e.mu.Lock()
	type workItem struct {
		teamName   string
		memberName string
	}
	var items []workItem
	for key, w := range e.workers {
		items = append(items, workItem{
			teamName:   w.teamName,
			memberName: w.memberName,
		})
		_ = key
	}
	e.mu.Unlock()

	if len(items) == 0 {
		return
	}

	e.logger.Info("shutting down all team members across all teams",
		"count", len(items),
	)

	var wg sync.WaitGroup
	for _, item := range items {
		wg.Add(1)
		go func(item workItem) {
			defer wg.Done()
			if err := e.ShutdownMember(ctx, item.teamName, item.memberName); err != nil {
				e.logger.Warn("shutdown member failed",
					"team", item.teamName,
					"member", item.memberName,
					"error", err,
				)
			}
		}(item)
	}
	wg.Wait()
}

// IsRunning 检查 member 是否在运行。
func (e *TeamEngine) IsRunning(teamName, memberName string) bool {
	key := workerKey(teamName, memberName)
	e.mu.Lock()
	defer e.mu.Unlock()
	_, exists := e.workers[key]
	return exists
}

// RunningCount 返回指定 team 当前运行的 member 数。
func (e *TeamEngine) RunningCount(teamName string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	count := 0
	for _, w := range e.workers {
		if w.teamName == teamName {
			count++
		}
	}
	return count
}

// MemberStatuses 返回 team 中所有 member 的运行状态。
func (e *TeamEngine) MemberStatuses(teamName string) map[string]MemberWorkerStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[string]MemberWorkerStatus)
	for _, w := range e.workers {
		if w.teamName == teamName {
			out[w.memberName] = w.getStatus()
		}
	}
	return out
}
