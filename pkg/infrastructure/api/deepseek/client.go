// Package deepseek 实现 DeepSeek API 客户端，适配 query.AIProvider 接口
//
// DeepSeek 使用 OpenAI 兼容协议，本客户端在领域模型(query.*)与
// OpenAI 风格协议之间做双向转换，对外暴露与 anthropic.Client 相同的接口。
package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/yaoice/goclaude/pkg/domain/query"
)

const (
	// DefaultBaseURL DeepSeek API 默认地址
	DefaultBaseURL = "https://api.deepseek.com"
	// ChatCompletionsPath Chat Completions API 路径
	ChatCompletionsPath = "/v1/chat/completions"
	// DefaultTimeout 默认HTTP超时
	DefaultTimeout = 300 * time.Second
)

// ClientConfig DeepSeek 客户端配置
type ClientConfig struct {
	BaseURL        string        // API基础地址
	APIKey         string        // API密钥（必填）
	Timeout        time.Duration // HTTP超时
	MaxRetries     int           // 最大重试次数
	RetryBaseDelay time.Duration // 重试基础延迟
	Logger         *slog.Logger  // 日志实例
}

// DefaultClientConfig 返回带 APIKey 的默认配置
func DefaultClientConfig(apiKey string) *ClientConfig {
	return &ClientConfig{
		BaseURL:        DefaultBaseURL,
		APIKey:         apiKey,
		Timeout:        DefaultTimeout,
		MaxRetries:     3,
		RetryBaseDelay: 1 * time.Second,
	}
}

// Client DeepSeek API 客户端
//
// 实现 query.AIProvider 接口，可直接注入到 application.QueryService。
type Client struct {
	config     *ClientConfig
	httpClient *http.Client
	logger     *slog.Logger
}

// NewClient 创建 DeepSeek 客户端
func NewClient(config *ClientConfig) *Client {
	if config.BaseURL == "" {
		config.BaseURL = DefaultBaseURL
	}
	if config.Timeout == 0 {
		config.Timeout = DefaultTimeout
	}
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

// Stream 实现 query.AIProvider.Stream — 发起流式请求
func (c *Client) Stream(ctx context.Context, params *query.StreamParams) (<-chan query.StreamEvent, error) {
	reqBody := c.buildRequest(params, true)

	resp, err := c.doRequest(ctx, reqBody)
	if err != nil {
		return nil, err
	}

	events := make(chan query.StreamEvent, 64)

	// context 取消时主动关闭 body，使阻塞在 bufio.Scanner.Scan() 中的
	// goroutine 能立即收到 IO 错误并退出，实现 Ctrl+C 即时取消。
	go func() {
		<-ctx.Done()
		resp.Body.Close()
	}()

	go c.parseSSEStream(ctx, resp.Body, events)
	return events, nil
}

// Send 实现 query.AIProvider.Send — 发起非流式请求
func (c *Client) Send(ctx context.Context, params *query.SendParams) (*query.Message, *query.Usage, error) {
	reqBody := c.buildRequest(params, false)

	resp, err := c.doRequest(ctx, reqBody)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	var apiResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, nil, fmt.Errorf("decode response: %w", err)
	}

	if len(apiResp.Choices) == 0 {
		return nil, nil, fmt.Errorf("deepseek: empty response choices")
	}

	msg := convertChoiceToMessage(apiResp.ID, apiResp.Choices[0].Message)
	usage := convertUsage(apiResp.Usage)
	return msg, usage, nil
}

// doRequest 发起带重试的 HTTP 请求
func (c *Client) doRequest(ctx context.Context, reqBody *ChatRequest) (*http.Response, error) {
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := c.config.BaseURL + ChatCompletionsPath
	var lastErr error

	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := c.retryDelay(attempt)
			c.logger.Debug("DeepSeek 重试请求", "attempt", attempt, "delay", delay)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
		if reqBody.Stream {
			req.Header.Set("Accept", "text/event-stream")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}

		// 5xx 与 429 需要重试
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("deepseek: status %d: %s", resp.StatusCode, string(b))
			continue
		}

		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, parseErrorResponse(resp.StatusCode, b)
		}
		return resp, nil
	}
	return nil, fmt.Errorf("deepseek: max retries exceeded: %w", lastErr)
}

// retryDelay 计算指数退避延迟
func (c *Client) retryDelay(attempt int) time.Duration {
	delay := c.config.RetryBaseDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
	}
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	return delay
}

// parseErrorResponse 解析错误响应体为可读 error
func parseErrorResponse(status int, body []byte) error {
	var er ErrorResponse
	if json.Unmarshal(body, &er) == nil && er.Error.Message != "" {
		return fmt.Errorf("deepseek API error (status %d, type=%s, code=%s): %s",
			status, er.Error.Type, er.Error.Code, er.Error.Message)
	}
	return fmt.Errorf("deepseek API error (status %d): %s", status, string(body))
}
