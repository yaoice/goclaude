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

	// currentInputTokens 当前轮 API 调用的输入 token 数（反映实际上下文窗口大小）
	// 注意：不做跨轮累加，因为每轮 InputTokens 已包含完整对话历史；
	// 累加会导致同一上下文被重复计数 N 轮，提前误触发压缩。
	currentInputTokens int
	// currentOutputTokens 当前轮 API 调用的输出 token 数
	currentOutputTokens int
	// totalInputTokens 累计输入token数（跨轮累加，用于统计）
	totalInputTokens int
	// totalOutputTokens 累计输出token数（跨轮累加，用于统计）
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
//
// currentInputTokens 仅保留最新一轮的输入 token 数（反映实际上下文窗口大小），
// 不做跨轮累加。因为每轮 API 的 InputTokens 已包含完整对话历史，累加会导致
// ShouldCompact 将同一上下文重复计数 N 轮后提前误触发。
// totalInputTokens / totalOutputTokens 保持跨轮累加，用于 GetStats 统计。
func (tb *TokenBudget) RecordUsage(usage *Usage) {
	if usage == nil {
		return
	}
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.currentInputTokens = usage.InputTokens
	tb.currentOutputTokens = usage.OutputTokens
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
