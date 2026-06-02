// Package anthropic 实现 Anthropic Messages API 客户端
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/anthropics/goclaude/internal/domain/query"
)

const (
	// DefaultBaseURL Anthropic API 默认地址
	DefaultBaseURL = "https://api.anthropic.com"
	// APIVersion API版本
	APIVersion = "2023-06-01"
	// DefaultTimeout 默认超时
	DefaultTimeout = 300 * time.Second
)

// ClientConfig 客户端配置
type ClientConfig struct {
	// BaseURL API基础地址
	BaseURL string
	// APIKey API密钥
	APIKey string
	// APIVersion API版本
	APIVersion string
	// Timeout HTTP超时
	Timeout time.Duration
	// MaxRetries 最大重试次数
	MaxRetries int
	// RetryBaseDelay 重试基础延迟
	RetryBaseDelay time.Duration
	// BetaFeatures Beta功能头列表
	BetaFeatures []string
	// Logger 日志实例
	Logger *slog.Logger
}

// DefaultClientConfig 默认客户端配置
func DefaultClientConfig(apiKey string) *ClientConfig {
	return &ClientConfig{
		BaseURL:        DefaultBaseURL,
		APIKey:         apiKey,
		APIVersion:     APIVersion,
		Timeout:        DefaultTimeout,
		MaxRetries:     3,
		RetryBaseDelay: 1 * time.Second,
		BetaFeatures:   []string{"messages-2024-12-19"},
	}
}

// Client Anthropic API 客户端
type Client struct {
	config     *ClientConfig
	httpClient *http.Client
	logger     *slog.Logger
}

// NewClient 创建 Anthropic API 客户端
func NewClient(config *ClientConfig) *Client {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &Client{
		config: config,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
		logger: config.Logger,
	}
}

// Stream 实现 AIProvider.Stream 接口 - 发起流式请求
func (c *Client) Stream(ctx context.Context, params *query.StreamParams) (<-chan query.StreamEvent, error) {
	// 构建API请求体
	reqBody := c.buildRequest(params, true)

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// 创建HTTP请求
	req, err := http.NewRequestWithContext(ctx, "POST", c.config.BaseURL+"/v1/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req)

	// 发送带重试的请求
	resp, err := c.doWithRetry(ctx, req, bodyBytes)
	if err != nil {
		return nil, err
	}

	// 启动流式解析goroutine
	events := make(chan query.StreamEvent, 64)
	go c.parseSSEStream(ctx, resp.Body, events)

	return events, nil
}

// Send 实现 AIProvider.Send 接口 - 发起非流式请求
func (c *Client) Send(ctx context.Context, params *query.SendParams) (*query.Message, *query.Usage, error) {
	reqBody := c.buildRequest(params, false)

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.config.BaseURL+"/v1/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.doWithRetry(ctx, req, bodyBytes)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	var apiResp MessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, nil, fmt.Errorf("decode response: %w", err)
	}

	msg := convertResponseToMessage(&apiResp)
	usage := convertUsage(apiResp.Usage)
	return msg, usage, nil
}

// buildRequest 构建API请求体
func (c *Client) buildRequest(params *query.StreamParams, stream bool) *MessageRequest {
	req := &MessageRequest{
		Model:     params.Model,
		MaxTokens: params.MaxTokens,
		Stream:    stream,
	}

	if params.Temperature > 0 {
		req.Temperature = &params.Temperature
	}

	// 转换系统提示词
	if len(params.System) > 0 {
		for _, block := range params.System {
			if block.Type == query.ContentTypeText {
				req.System = append(req.System, SystemBlock{
					Type: "text",
					Text: block.Text,
				})
			}
		}
	}

	// 转换消息列表
	for _, msg := range params.Messages {
		apiMsg := APIMessage{
			Role: string(msg.Role),
		}
		for _, block := range msg.Content {
			apiMsg.Content = append(apiMsg.Content, convertContentBlock(block))
		}
		req.Messages = append(req.Messages, apiMsg)
	}

	// 转换工具定义
	for _, toolDef := range params.Tools {
		req.Tools = append(req.Tools, APIToolDef{
			Name:        toolDef.Name,
			Description: toolDef.Description,
			InputSchema: toolDef.InputSchema,
		})
	}

	if params.ToolChoice != nil {
		req.ToolChoice = &APIToolChoice{
			Type: params.ToolChoice.Type,
			Name: params.ToolChoice.Name,
		}
	}

	return req
}

// setHeaders 设置HTTP请求头
func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.config.APIKey)
	req.Header.Set("Anthropic-Version", c.config.APIVersion)

	if len(c.config.BetaFeatures) > 0 {
		for _, beta := range c.config.BetaFeatures {
			req.Header.Add("Anthropic-Beta", beta)
		}
	}
}

// doWithRetry 带重试的HTTP请求
func (c *Client) doWithRetry(ctx context.Context, req *http.Request, body []byte) (*http.Response, error) {
	var lastErr error

	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := c.getRetryDelay(attempt)
			c.logger.Debug("重试API请求", "attempt", attempt, "delay", delay)

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}

			// 重建请求（body 已被消费）
			newReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL.String(), bytes.NewReader(body))
			if err != nil {
				return nil, err
			}
			newReq.Header = req.Header
			req = newReq
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}

		// 判断是否需要重试
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			resp.Body.Close()
			lastErr = fmt.Errorf("API returned status %d", resp.StatusCode)
			continue
		}

		if resp.StatusCode != 200 {
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(bodyBytes))
		}

		return resp, nil
	}

	return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}

// getRetryDelay 计算重试延迟（指数退避）
func (c *Client) getRetryDelay(attempt int) time.Duration {
	delay := c.config.RetryBaseDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
	}
	// 最大30秒
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	return delay
}

// convertContentBlock 转换内容块为API格式
func convertContentBlock(block query.ContentBlock) APIContentBlock {
	switch block.Type {
	case query.ContentTypeText:
		return APIContentBlock{
			Type: "text",
			Text: block.Text,
		}
	case query.ContentTypeToolUse:
		return APIContentBlock{
			Type:  "tool_use",
			ID:    block.ToolUseID,
			Name:  block.ToolName,
			Input: block.Input,
		}
	case query.ContentTypeToolResult:
		return APIContentBlock{
			Type:      "tool_result",
			ToolUseID: block.ToolResultID,
			Content:   block.Text,
			IsError:   block.IsError,
		}
	case query.ContentTypeImage:
		return APIContentBlock{
			Type: "image",
			Source: &APIImageSource{
				Type:      "base64",
				MediaType: block.MediaType,
				Data:      block.Data,
			},
		}
	default:
		return APIContentBlock{Type: "text", Text: block.Text}
	}
}

// convertResponseToMessage 将API响应转换为领域消息
func convertResponseToMessage(resp *MessageResponse) *query.Message {
	msg := &query.Message{
		ID:   resp.ID,
		Role: query.RoleAssistant,
	}

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			msg.Content = append(msg.Content, query.ContentBlock{
				Type: query.ContentTypeText,
				Text: block.Text,
			})
		case "tool_use":
			msg.Content = append(msg.Content, query.ContentBlock{
				Type:      query.ContentTypeToolUse,
				ToolUseID: block.ID,
				ToolName:  block.Name,
				Input:     block.Input,
			})
		}
	}
	return msg
}

// convertUsage 转换使用量
func convertUsage(u *APIUsage) *query.Usage {
	if u == nil {
		return nil
	}
	return &query.Usage{
		InputTokens:              u.InputTokens,
		OutputTokens:             u.OutputTokens,
		CacheCreationInputTokens: u.CacheCreationInputTokens,
		CacheReadInputTokens:     u.CacheReadInputTokens,
	}
}
