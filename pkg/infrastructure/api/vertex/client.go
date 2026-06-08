// Package vertex 实现 GCP Vertex AI Provider
package vertex

import (
	"context"
	"fmt"

	"github.com/anthropics/goclaude/pkg/domain/query"
)

// Client GCP Vertex AI 客户端
type Client struct {
	projectID string
	region    string
	modelID   string
}

// Config Vertex客户端配置
type Config struct {
	ProjectID string
	Region    string
	ModelID   string
}

// NewClient 创建 Vertex AI 客户端
func NewClient(config *Config) *Client {
	return &Client{
		projectID: config.ProjectID,
		region:    config.Region,
		modelID:   config.ModelID,
	}
}

// Stream 实现 AIProvider.Stream - 通过Vertex AI流式调用
func (c *Client) Stream(ctx context.Context, params *query.StreamParams) (<-chan query.StreamEvent, error) {
	// TODO: 实现 Vertex AI streamGenerateContent
	return nil, fmt.Errorf("vertex streaming not yet implemented")
}

// Send 实现 AIProvider.Send - 通过Vertex AI非流式调用
func (c *Client) Send(ctx context.Context, params *query.SendParams) (*query.Message, *query.Usage, error) {
	// TODO: 实现 Vertex AI generateContent
	return nil, nil, fmt.Errorf("vertex send not yet implemented")
}
