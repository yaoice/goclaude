package query

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/anthropics/goclaude/pkg/domain/tool"
)

// Config 查询引擎配置
type Config struct {
	// Model 使用的模型名称
	Model string
	// MaxTokens 最大输出token数
	MaxTokens int
	// Temperature 温度参数
	Temperature float64
	// MaxTurns 最大对话轮数（防止无限循环）
	MaxTurns int
	// AutoCompact 是否启用自动压缩
	AutoCompact bool
	// SystemPrompt 系统提示词
	SystemPrompt []ContentBlock
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Model:       "claude-sonnet-4-20250514",
		MaxTokens:   16384,
		Temperature: 1.0,
		MaxTurns:    100,
		AutoCompact: true,
	}
}

// Engine 查询引擎 - 负责AI对话主循环
// 核心流程：消息组装 → API调用 → 流式处理 → 工具执行 → 循环
type Engine struct {
	mu sync.RWMutex

	// provider AI服务提供商（通过接口依赖倒置）
	provider AIProvider
	// toolExecutor 工具执行器
	toolExecutor *tool.Executor
	// budget token预算管理
	budget *TokenBudget
	// compactor 消息压缩器
	compactor Compactor
	// config 引擎配置
	config *Config
	// logger 结构化日志
	logger *slog.Logger

	// 运行状态
	running bool
	turns   int

	// afterToolHook 每轮工具执行后回调，返回的 user 消息将追加到对话
	// 可用于：条件 skill 激活注入、动态上下文补丁等
	afterToolHook AfterToolHook
}

// AfterToolHook 工具执行完成后的回调
//
// 参数：
//   - turn:         当前轮数（从 1 开始）
//   - toolUses:     本轮 assistant 消息中的 tool_use blocks（原始输入信息）
//   - toolResults:  本轮工具执行后的 tool_result blocks
//
// 返回追加到对话的额外 user 消息（按顺序）；返回 nil 表示无追加。
type AfterToolHook func(turn int, toolUses, toolResults []ContentBlock) []Message

// SetAfterToolHook 设置工具执行后的钩子
func (e *Engine) SetAfterToolHook(h AfterToolHook) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.afterToolHook = h
}

// NewEngine 创建查询引擎实例
func NewEngine(
	provider AIProvider,
	toolExecutor *tool.Executor,
	budget *TokenBudget,
	compactor Compactor,
	config *Config,
	logger *slog.Logger,
) *Engine {
	if config == nil {
		config = DefaultConfig()
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{
		provider:     provider,
		toolExecutor: toolExecutor,
		budget:       budget,
		compactor:    compactor,
		config:       config,
		logger:       logger,
	}
}

// QueryResult 单轮查询结果
type QueryResult struct {
	// Response 最终的助手响应消息
	Response *Message
	// CompactedMessages 压缩后的完整消息列表（如有压缩；否则为 nil）
	// REPL 应用此字段替换 r.messages，避免无限增长
	CompactedMessages []Message
	// Usage 本轮使用量
	Usage *Usage
	// StopReason 停止原因
	StopReason StopReason
	// TurnCount 本次Execute中经历的轮数
	TurnCount int
}

// Execute 执行完整的查询循环
// 会持续循环直到：1)AI不再调用工具 2)达到最大轮数 3)context取消
// events channel 输出流式事件供TUI实时渲染
func (e *Engine) Execute(ctx context.Context, messages []Message, events chan<- StreamEvent) (*QueryResult, error) {
	e.mu.Lock()
	e.running = true
	e.turns = 0
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		e.running = false
		e.mu.Unlock()
	}()

	currentMessages := make([]Message, len(messages))
	copy(currentMessages, messages)

	var lastUsage *Usage
	var lastStopReason StopReason

	for {
		// 检查是否超过最大轮数
		e.mu.Lock()
		e.turns++
		currentTurn := e.turns
		e.mu.Unlock()

		if currentTurn > e.config.MaxTurns {
			return nil, fmt.Errorf("exceeded max turns (%d)", e.config.MaxTurns)
		}

		// 检查是否需要压缩
		if e.config.AutoCompact && e.budget.ShouldCompact() && e.compactor != nil {
			e.logger.Debug("触发自动压缩", "turn", currentTurn)
			compacted, err := e.compactor.Compact(ctx, currentMessages, e.provider)
			if err != nil {
				e.logger.Warn("压缩失败，继续使用原消息", "error", err)
			} else {
				currentMessages = compacted
				e.budget.Reset()
			}
		}

		// 构建流式请求参数
		params := &StreamParams{
			Model:       e.config.Model,
			Messages:    currentMessages,
			System:      e.config.SystemPrompt,
			MaxTokens:   e.config.MaxTokens,
			Temperature: e.config.Temperature,
			Tools:       e.getToolDefinitions(),
		}

		// 发起流式API调用
		streamCh, err := e.provider.Stream(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("stream request failed: %w", err)
		}

		// 收集本轮完整响应
		response, usage, stopReason, err := e.processStream(ctx, streamCh, events)
		if err != nil {
			return nil, fmt.Errorf("stream processing failed: %w", err)
		}

		lastUsage = usage
		lastStopReason = stopReason

		// 记录token使用量
		e.budget.RecordUsage(usage)

		// 将助手响应加入消息列表
		currentMessages = append(currentMessages, *response)

		// 如果没有工具调用，查询循环结束
		if !response.HasToolUse() {
			return &QueryResult{
				Response:          response,
				CompactedMessages: currentMessages,
				Usage:             lastUsage,
				StopReason:        lastStopReason,
				TurnCount:         currentTurn,
			}, nil
		}

		// 执行工具调用
		toolUseBlocks := response.GetToolUseBlocks()
		toolResults, err := e.executeTools(ctx, toolUseBlocks, events)
		if err != nil {
			return nil, fmt.Errorf("tool execution failed: %w", err)
		}

		// 将工具结果作为user消息加入
		toolResultMsg := Message{
			Role:    RoleUser,
			Content: toolResults,
		}
		currentMessages = append(currentMessages, toolResultMsg)

		// 调用 afterToolHook 让外层追加额外消息（条件 skill 激活等）
		e.mu.RLock()
		hook := e.afterToolHook
		e.mu.RUnlock()
		if hook != nil {
			if extras := hook(currentTurn, toolUseBlocks, toolResults); len(extras) > 0 {
				currentMessages = append(currentMessages, extras...)
			}
		}
	}
}

// processStream 处理流式响应，组装完整消息
func (e *Engine) processStream(ctx context.Context, streamCh <-chan StreamEvent, events chan<- StreamEvent) (*Message, *Usage, StopReason, error) {
	response := &Message{
		Role:    RoleAssistant,
		Content: []ContentBlock{},
	}
	var usage *Usage
	var stopReason StopReason

	// 当前正在构建的内容块
	currentBlocks := make(map[int]*ContentBlock)

	for {
		select {
		case <-ctx.Done():
			return nil, nil, "", ctx.Err()
		case event, ok := <-streamCh:
			if !ok {
				// 流结束
				return response, usage, stopReason, nil
			}

			// 转发事件到TUI
			if events != nil {
				select {
				case events <- event:
				case <-ctx.Done():
					return nil, nil, "", ctx.Err()
				}
			}

			// 处理事件
			switch event.Type {
			case EventContentBlockStart:
				if event.ContentBlock != nil {
					currentBlocks[event.Index] = event.ContentBlock
				}

			case EventContentBlockDelta:
				if block, ok := currentBlocks[event.Index]; ok && event.Delta != nil {
					switch event.Delta.Type {
					case ContentTypeText:
						block.Text += event.Delta.Text
					case ContentTypeToolUse:
						block.Text += event.Delta.PartialJSON
					case ContentTypeThinking:
						block.Thinking += event.Delta.Thinking
					}
				}

			case EventContentBlockStop:
				if block, ok := currentBlocks[event.Index]; ok {
					// 对 ToolUse 块：累积期使用 block.Text 作为 partial_json 缓冲；
					// 在结束时一次性反序列化为 block.Input，避免下游 executeTools
					// 拿到空入参（这是流式工具调用的必要收尾步骤）。
					if block.Type == ContentTypeToolUse && block.Text != "" {
						var input interface{}
						if err := json.Unmarshal([]byte(block.Text), &input); err == nil {
							block.Input = input
						} else {
							e.logger.Warn("解析 tool_use 流式 input 失败",
								"tool", block.ToolName,
								"id", block.ToolUseID,
								"raw", block.Text,
								"error", err,
							)
						}
						block.Text = "" // 清掉用作累积缓冲的脏字段
					}
					response.Content = append(response.Content, *block)
					delete(currentBlocks, event.Index)
				}

			case EventMessageDelta:
				stopReason = event.StopReason
				if event.Usage != nil {
					usage = event.Usage
				}

			case EventMessageStart:
				if event.Usage != nil {
					usage = event.Usage
				}

			case EventError:
				return nil, nil, "", event.Error
			}
		}
	}
}

// executeTools 执行工具调用列表
func (e *Engine) executeTools(ctx context.Context, toolUseBlocks []ContentBlock, events chan<- StreamEvent) ([]ContentBlock, error) {
	if e.toolExecutor == nil {
		// 没有工具执行器，返回错误结果
		var results []ContentBlock
		for _, block := range toolUseBlocks {
			results = append(results, NewToolResultBlock(block.ToolUseID, "tool executor not available", true))
		}
		return results, nil
	}

	// 转换为工具执行请求
	requests := make([]tool.ExecutionRequest, 0, len(toolUseBlocks))
	for _, block := range toolUseBlocks {
		requests = append(requests, tool.ExecutionRequest{
			ToolUseID: block.ToolUseID,
			ToolName:  block.ToolName,
			Input:     block.Input,
		})
	}

	// 调用工具执行器（内部处理并发调度）
	results, err := e.toolExecutor.Execute(ctx, requests)
	if err != nil {
		return nil, err
	}

	// 转换为内容块；每个结果同时通过 events 通道发一个
	// EventContentBlockStart 事件携带 ToolResult 块，便于上层 UI 实时
	// 显示工具调用的成功/失败状态。这是一个 best-effort 通知：
	// 通道满或 ctx 取消时不会阻塞引擎主循环。
	var contentBlocks []ContentBlock
	for _, r := range results {
		block := NewToolResultBlock(r.ToolUseID, r.Content, r.IsError)
		contentBlocks = append(contentBlocks, block)
		if events != nil {
			ev := StreamEvent{
				Type:         EventContentBlockStart,
				ContentBlock: &block,
			}
			select {
			case events <- ev:
			case <-ctx.Done():
				return contentBlocks, ctx.Err()
			default:
				// channel 满 → 跳过通知（不影响主流程）
			}
		}
	}
	return contentBlocks, nil
}

// getToolDefinitions 获取所有已启用工具的API定义
func (e *Engine) getToolDefinitions() []ToolDefinition {
	if e.toolExecutor == nil {
		return nil
	}
	rawDefs := e.toolExecutor.GetToolDefinitions()
	defs := make([]ToolDefinition, 0, len(rawDefs))
	for _, raw := range rawDefs {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		def := ToolDefinition{InputSchema: m["input_schema"]}
		if name, ok := m["name"].(string); ok {
			def.Name = name
		} else {
			def.Name = fmt.Sprintf("%v", m["name"])
		}
		if desc, ok := m["description"].(string); ok {
			def.Description = desc
		}
		defs = append(defs, def)
	}
	return defs
}

// IsRunning 返回引擎是否正在运行
func (e *Engine) IsRunning() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.running
}

// GetTurns 返回当前轮数
func (e *Engine) GetTurns() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.turns
}
