package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yaoice/goclaude/pkg/domain/query"
)

// PromptEnhancer 提示词优化服务
//
// 使用独立的轻量 API 调用将用户输入的简短/模糊提示词改写为更清晰、完整、可执行的版本。
// 不经过主对话 Engine，不消耗主对话的 token 预算。
type PromptEnhancer struct {
	provider    query.AIProvider
	model       string
	timeout     time.Duration
	maxTokens   int
	temperature float64
}

// NewPromptEnhancer 创建提示词优化服务
func NewPromptEnhancer(provider query.AIProvider, model string) *PromptEnhancer {
	return &PromptEnhancer{
		provider:    provider,
		model:       model,
		timeout:     30 * time.Second,
		maxTokens:   4096,
		temperature: 0.3,
	}
}

// SetTimeout 设置单次优化调用的超时
func (pe *PromptEnhancer) SetTimeout(d time.Duration) {
	pe.timeout = d
}

// SetMaxTokens 设置优化返回最大 token 数
func (pe *PromptEnhancer) SetMaxTokens(n int) {
	if n > 0 {
		pe.maxTokens = n
	}
}

// SetTemperature 设置优化调用温度
func (pe *PromptEnhancer) SetTemperature(t float64) {
	if t >= 0 && t <= 2.0 {
		pe.temperature = t
	}
}

const enhanceSystemPrompt = `You are a prompt engineering expert. Your task is to optimize user prompts to be clearer, more complete, and more actionable.

Follow these rules:
1. Preserve the user's original intent and language (Chinese → Chinese, English → English).
2. Add necessary context, constraints, and expected output format if missing.
3. Make vague requests specific and executable.
4. Keep the optimized prompt concise — don't add unnecessary verbosity.
5. Output ONLY the optimized prompt text, no explanations, no prefixes like "优化后的提示词:".`

// Enhance 优化提示词
//
// 参数：
//   - ctx: 上下文（应带有超时控制）
//   - original: 用户原始提示词文本
//
// 返回优化后的提示词；失败时返回原始文本和 error。
func (pe *PromptEnhancer) Enhance(ctx context.Context, original string) (string, error) {
	original = strings.TrimSpace(original)
	if original == "" {
		return "", fmt.Errorf("empty prompt")
	}

	ctx, cancel := context.WithTimeout(ctx, pe.timeout)
	defer cancel()

	// Anthropic API 使用特殊的 system 参数而非消息列表中的 system 角色，
	// 这里通过 SendParams.System 字段传递 system prompt
	userMsg := query.NewTextMessage(query.RoleUser, original)

	params := &query.SendParams{
		Model:       pe.model,
		Messages:    []query.Message{userMsg},
		System:      []query.ContentBlock{{Type: query.ContentTypeText, Text: enhanceSystemPrompt}},
		MaxTokens:   pe.maxTokens,
		Temperature: pe.temperature,
	}

	msg, _, err := pe.provider.Send(ctx, params)
	if err != nil {
		return original, fmt.Errorf("enhance API call failed: %w", err)
	}

	if msg == nil || len(msg.Content) == 0 {
		return original, fmt.Errorf("enhance returned empty response")
	}

	var result strings.Builder
	for _, block := range msg.Content {
		if block.Type == query.ContentTypeText {
			result.WriteString(block.Text)
		}
	}

	enhanced := strings.TrimSpace(result.String())
	if enhanced == "" || enhanced == original {
		return original, fmt.Errorf("enhance produced no meaningful change")
	}

	return enhanced, nil
}
