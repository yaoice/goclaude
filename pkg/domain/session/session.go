// Package session 定义会话领域模型
package session

import (
	"sync"
	"time"
)

// Session 会话实体
type Session struct {
	// ID 会话唯一标识
	ID string `json:"id"`
	// CreatedAt 创建时间
	CreatedAt time.Time `json:"created_at"`
	// WorkingDir 工作目录
	WorkingDir string `json:"working_dir"`
	// ProjectRoot 项目根目录
	ProjectRoot string `json:"project_root"`
	// Model 当前使用的模型
	Model string `json:"model"`
	// State 会话状态
	State *State `json:"state"`
}

// State 会话状态（实时数据）
type State struct {
	mu sync.RWMutex

	// TotalInputTokens 累计输入token数
	TotalInputTokens int `json:"total_input_tokens"`
	// TotalOutputTokens 累计输出token数
	TotalOutputTokens int `json:"total_output_tokens"`
	// TotalCostUSD 累计成本（美元）
	TotalCostUSD float64 `json:"total_cost_usd"`
	// MessageCount 消息数
	MessageCount int `json:"message_count"`
	// TurnCount 轮数
	TurnCount int `json:"turn_count"`
	// LastActivity 最后活动时间
	LastActivity time.Time `json:"last_activity"`
}

// NewSession 创建新会话
func NewSession(id, workingDir, projectRoot, model string) *Session {
	return &Session{
		ID:          id,
		CreatedAt:   time.Now(),
		WorkingDir:  workingDir,
		ProjectRoot: projectRoot,
		Model:       model,
		State:       &State{LastActivity: time.Now()},
	}
}

// RecordUsage 记录使用量
func (s *State) RecordUsage(inputTokens, outputTokens int, costUSD float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.TotalInputTokens += inputTokens
	s.TotalOutputTokens += outputTokens
	s.TotalCostUSD += costUSD
	s.TurnCount++
	s.LastActivity = time.Now()
}

// IncrementMessages 增加消息计数
func (s *State) IncrementMessages() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.MessageCount++
	s.LastActivity = time.Now()
}

// GetStats 获取会话统计（线程安全）
func (s *State) GetStats() StateSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return StateSnapshot{
		TotalInputTokens:  s.TotalInputTokens,
		TotalOutputTokens: s.TotalOutputTokens,
		TotalCostUSD:      s.TotalCostUSD,
		MessageCount:      s.MessageCount,
		TurnCount:         s.TurnCount,
		LastActivity:      s.LastActivity,
	}
}

// StateSnapshot 状态快照（不可变值对象）
type StateSnapshot struct {
	TotalInputTokens  int       `json:"total_input_tokens"`
	TotalOutputTokens int       `json:"total_output_tokens"`
	TotalCostUSD      float64   `json:"total_cost_usd"`
	MessageCount      int       `json:"message_count"`
	TurnCount         int       `json:"turn_count"`
	LastActivity      time.Time `json:"last_activity"`
}
