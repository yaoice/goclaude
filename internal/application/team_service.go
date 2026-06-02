// Package application 中的 team_service 封装 team / mailbox 的业务流程。
//
// 与下层 (infrastructure/team) 的关系：
//   - Store / Mailbox 只懂"原子读写文件"
//   - TeamService 负责"业务规则"：team 唯一性、成员加入/退出、广播展开、
//     leader-only 的清理校验、心跳与状态机、协议消息（shutdown / task）
//     的副作用处理等
//
// 调用方：CLI (`goclaude team ...`)、tools (TeamCreate/Send/...)、REPL 命令。
//
// 实现按职责拆分到多个同包文件：
//   - team_service.go             核心：结构体、构造、哨兵错误与 helpers
//   - team_service_membership.go  成员生命周期（建队/加入/状态/退出/删除）
//   - team_service_messaging.go   消息投递、收件箱、任务消息封装
//   - team_service_tasks.go       共享任务列表 CRUD、状态摘要、自动建队/分配
package application

import (
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"

	teamfs "github.com/anthropics/goclaude/internal/infrastructure/team"
)

// TeamService 是 team 子系统的应用层入口。
//
// 内部用 sync.Mutex 串行化所有 read-modify-write 序列（JoinTeam / Leave /
// SetMemberActive / Heartbeat / DeleteTeam / Send 中的 sender 校验）。
// 同进程多 goroutine 调用安全；跨进程仍然依赖 Store/Mailbox 的文件锁与
// atomicWrite 防止半截写入。
type TeamService struct {
	Store   *teamfs.Store
	Mailbox *teamfs.Mailbox

	logger *slog.Logger
	mu     sync.Mutex // 保护跨方法 read-modify-write
}

// NewTeamService 用默认 Layout（~/.goclaude/teams/）构造。logger 可为 nil。
func NewTeamService() *TeamService {
	return NewTeamServiceWithLogger(nil)
}

// NewTeamServiceWithLogger 用指定 logger 构造（默认 Layout）。
func NewTeamServiceWithLogger(logger *slog.Logger) *TeamService {
	l := teamfs.DefaultLayout()
	return newTeamServiceWith(l, logger)
}

// NewTeamServiceWithLayout 注入自定义 Layout（测试用）。
func NewTeamServiceWithLayout(l teamfs.Layout) *TeamService {
	return newTeamServiceWith(l, nil)
}

// NewTeamServiceWithLayoutAndLogger 全字段注入（测试 + 自定义日志）。
func NewTeamServiceWithLayoutAndLogger(l teamfs.Layout, logger *slog.Logger) *TeamService {
	return newTeamServiceWith(l, logger)
}

func newTeamServiceWith(l teamfs.Layout, logger *slog.Logger) *TeamService {
	if logger == nil {
		logger = slog.Default()
	}
	return &TeamService{
		Store:   teamfs.NewStore(l),
		Mailbox: teamfs.NewMailbox(l),
		logger:  logger.With(slog.String("component", "team_service")),
	}
}

// ----- helpers + sentinel errors -----

// ErrTeamExists / ErrTeamNotFound / ErrMemberNotFound / ErrTeamHasActiveMembers
// 是上层可以用 errors.Is 判别的稳定语义错误。
var (
	ErrTeamExists           = errors.New("team already exists")
	ErrTeamNotFound         = errors.New("team not found")
	ErrMemberNotFound       = errors.New("member not found")
	ErrTeamHasActiveMembers = errors.New("team has active members")
)

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// CurrentCwd 是 os.Getwd 的薄封装；CLI 入口构造 CreateTeamInput 时方便用。
func CurrentCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}
