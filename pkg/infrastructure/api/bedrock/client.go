// Package bedrock 实现 AWS Bedrock Provider
package bedrock

import (
	"context"
	"fmt"

	"github.com/anthropics/goclaude/pkg/domain/query"
)

// Client AWS Bedrock 客户端
type Client struct {
	region    string
	modelID   string
	accessKey string
	secretKey string
}

// Config Bedrock客户端配置
type Config struct {
	Region    string
	ModelID   string
	AccessKey string
	SecretKey string
}

// NewClient 创建 Bedrock 客户端
func NewClient(config *Config) *Client {
	return &Client{
		region:    config.Region,
		modelID:   config.ModelID,
		accessKey: config.AccessKey,
		secretKey: config.SecretKey,
	}
}

// Stream 实现 AIProvider.Stream - 通过Bedrock流式调用
func (c *Client) Stream(ctx context.Context, params *query.StreamParams) (<-chan query.StreamEvent, error) {
	// TODO: 实现 AWS Bedrock InvokeModelWithResponseStream
	// 使用 AWS SDK v2 的 bedrockruntime 客户端
	return nil, fmt.Errorf("bedrock streaming not yet implemented")
}

// Send 实现 AIProvider.Send - 通过Bedrock非流式调用
func (c *Client) Send(ctx context.Context, params *query.SendParams) (*query.Message, *query.Usage, error) {
	// TODO: 实现 AWS Bedrock InvokeModel
	return nil, nil, fmt.Errorf("bedrock send not yet implemented")
}
