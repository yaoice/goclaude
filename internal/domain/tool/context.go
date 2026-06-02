package tool

import "context"

// UseContext 工具执行上下文
//
// 提供工具执行时需要的环境信息以及向上层 TUI/UI 注入回调的钩子。
type UseContext struct {
	// WorkingDir 当前工作目录
	WorkingDir string
	// ProjectRoot 项目根目录
	ProjectRoot string
	// SessionID 当前会话ID
	SessionID string
	// Env 环境变量
	Env map[string]string

	// AskUser 由上层（TUI/CLI）注入的"向用户提问并等待回答"回调
	//
	// 为空时 AskUserTool 会通过 IsEnabled()=false 自我禁用。
	AskUser func(ctx context.Context, question string) (string, error)

	// TodoStore 由上层注入的 todo 持久化接口
	//
	// 为空时 TodoWriteTool 会自我禁用。
	TodoStore TodoStore

	// WebSearch 由上层注入的网络搜索后端
	//
	// 为空时 WebSearchTool 会自我禁用（不出现在工具列表中）。
	WebSearch WebSearchBackend
}

// TodoStore 任务列表持久化接口（由 application 层实现）
type TodoStore interface {
	// Write 覆盖式写入完整 todo 列表
	Write(ctx context.Context, sessionID, todosJSON string, merge bool) error
	// Read 返回当前会话的 todo JSON
	Read(ctx context.Context, sessionID string) (string, error)
}

// WebSearchBackend 抽象的搜索后端
type WebSearchBackend interface {
	// Search 返回搜索结果（已格式化的纯文本）
	Search(ctx context.Context, query string, maxResults int) (string, error)
}
