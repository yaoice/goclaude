package query

// EventType 流式事件类型
type EventType int

const (
	// EventMessageStart 消息开始
	EventMessageStart EventType = iota
	// EventContentBlockStart 内容块开始
	EventContentBlockStart
	// EventContentBlockDelta 内容块增量
	EventContentBlockDelta
	// EventContentBlockStop 内容块结束
	EventContentBlockStop
	// EventMessageDelta 消息增量（stop_reason等）
	EventMessageDelta
	// EventMessageStop 消息结束
	EventMessageStop
	// EventPing 心跳
	EventPing
	// EventError 错误事件
	EventError
)

// StopReason 停止原因
type StopReason string

const (
	StopReasonEndTurn   StopReason = "end_turn"
	StopReasonToolUse   StopReason = "tool_use"
	StopReasonMaxTokens StopReason = "max_tokens"
	StopReasonStopSeq   StopReason = "stop_sequence"
)

// StreamEvent 流式事件（查询引擎核心通信单元）
type StreamEvent struct {
	// Type 事件类型
	Type EventType

	// Index 内容块索引
	Index int

	// ContentBlock 完整内容块（在 BlockStart 事件中）
	ContentBlock *ContentBlock

	// Delta 增量内容
	Delta *DeltaContent

	// Usage token使用量（在 MessageStart/MessageDelta 中）
	Usage *Usage

	// StopReason 停止原因（在 MessageDelta 中）
	StopReason StopReason

	// Message 完整消息（在 MessageStart 中）
	Message *Message

	// Error 错误信息（在 Error 事件中）
	Error error
}

// DeltaContent 增量内容
type DeltaContent struct {
	// Type 增量类型
	Type ContentType
	// Text 文本增量
	Text string
	// PartialJSON 工具输入的部分JSON（用于流式工具执行）
	PartialJSON string
	// Thinking 思考增量
	Thinking string
}

// Usage API使用量统计
type Usage struct {
	// InputTokens 输入token数
	InputTokens int `json:"input_tokens"`
	// OutputTokens 输出token数
	OutputTokens int `json:"output_tokens"`
	// CacheCreationInputTokens 缓存创建token数
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	// CacheReadInputTokens 缓存读取token数
	CacheReadInputTokens int `json:"cache_read_input_tokens,omitempty"`
}

// TotalTokens 返回总token数
func (u *Usage) TotalTokens() int {
	return u.InputTokens + u.OutputTokens
}
