package anthropic

// 模型定价表（每百万token美元）
var modelPricing = map[string]ModelPrice{
	"claude-sonnet-4-20250514": {InputPerMillion: 3.0, OutputPerMillion: 15.0, CacheWritePerMillion: 3.75, CacheReadPerMillion: 0.30},
	"claude-opus-4-20250514":   {InputPerMillion: 15.0, OutputPerMillion: 75.0, CacheWritePerMillion: 18.75, CacheReadPerMillion: 1.50},
	"claude-haiku-3-5":         {InputPerMillion: 0.80, OutputPerMillion: 4.0, CacheWritePerMillion: 1.0, CacheReadPerMillion: 0.08},
}

// ModelPrice 模型定价
type ModelPrice struct {
	InputPerMillion      float64
	OutputPerMillion     float64
	CacheWritePerMillion float64
	CacheReadPerMillion  float64
}

// CalculateCost 计算API调用成本（美元）
func CalculateCost(model string, usage *APIUsage) float64 {
	if usage == nil {
		return 0
	}

	price, ok := modelPricing[model]
	if !ok {
		// 默认使用sonnet定价
		price = modelPricing["claude-sonnet-4-20250514"]
	}

	cost := 0.0
	cost += float64(usage.InputTokens) / 1_000_000 * price.InputPerMillion
	cost += float64(usage.OutputTokens) / 1_000_000 * price.OutputPerMillion
	cost += float64(usage.CacheCreationInputTokens) / 1_000_000 * price.CacheWritePerMillion
	cost += float64(usage.CacheReadInputTokens) / 1_000_000 * price.CacheReadPerMillion

	return cost
}
