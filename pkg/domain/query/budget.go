package query

import "sync"

// TokenBudget Token预算管理器
// 追踪当前会话的token使用量，决定是否触发压缩
type TokenBudget struct {
	mu sync.RWMutex

	// maxContextTokens 最大上下文token数
	maxContextTokens int
	// compactThreshold 触发压缩的阈值比例（0.0-1.0）
	compactThreshold float64

	// currentInputTokens 当前输入token使用量
	currentInputTokens int
	// currentOutputTokens 当前输出token使用量
	currentOutputTokens int
	// totalInputTokens 累计输入token数
	totalInputTokens int
	// totalOutputTokens 累计输出token数
	totalOutputTokens int
}

// NewTokenBudget 创建token预算管理器
func NewTokenBudget(maxContext int, threshold float64) *TokenBudget {
	if threshold <= 0 || threshold > 1.0 {
		threshold = 0.8 // 默认80%触发压缩
	}
	return &TokenBudget{
		maxContextTokens: maxContext,
		compactThreshold: threshold,
	}
}

// RecordUsage 记录一次API调用的token使用量
func (tb *TokenBudget) RecordUsage(usage *Usage) {
	if usage == nil {
		return
	}
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.currentInputTokens += usage.InputTokens
	tb.currentOutputTokens += usage.OutputTokens
	tb.totalInputTokens += usage.InputTokens
	tb.totalOutputTokens += usage.OutputTokens
}

// ShouldCompact 判断是否应该触发自动压缩
func (tb *TokenBudget) ShouldCompact() bool {
	tb.mu.RLock()
	defer tb.mu.RUnlock()

	return float64(tb.currentInputTokens) >= float64(tb.maxContextTokens)*tb.compactThreshold
}

// RemainingTokens 剩余可用token数
func (tb *TokenBudget) RemainingTokens() int {
	tb.mu.RLock()
	defer tb.mu.RUnlock()

	remaining := tb.maxContextTokens - tb.currentInputTokens
	if remaining < 0 {
		return 0
	}
	return remaining
}

// GetStats 获取使用量统计
func (tb *TokenBudget) GetStats() BudgetStats {
	tb.mu.RLock()
	defer tb.mu.RUnlock()

	return BudgetStats{
		MaxContextTokens:    tb.maxContextTokens,
		CurrentInputTokens:  tb.currentInputTokens,
		CurrentOutputTokens: tb.currentOutputTokens,
		TotalInputTokens:    tb.totalInputTokens,
		TotalOutputTokens:   tb.totalOutputTokens,
		UsagePercent:        float64(tb.currentInputTokens) / float64(tb.maxContextTokens) * 100,
	}
}

// Reset 重置预算（用于压缩后）
func (tb *TokenBudget) Reset() {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.currentInputTokens = 0
	tb.currentOutputTokens = 0
}

// BudgetStats 预算统计信息
type BudgetStats struct {
	MaxContextTokens    int     `json:"max_context_tokens"`
	CurrentInputTokens  int     `json:"current_input_tokens"`
	CurrentOutputTokens int     `json:"current_output_tokens"`
	TotalInputTokens    int     `json:"total_input_tokens"`
	TotalOutputTokens   int     `json:"total_output_tokens"`
	UsagePercent        float64 `json:"usage_percent"`
}
