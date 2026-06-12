package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yaoice/goclaude/pkg/domain/query"
)

// stubProvider 用于测试的简单 provider 实现
type stubProvider struct {
	sendFn func(ctx context.Context, params *query.SendParams) (*query.Message, *query.Usage, error)
}

func (s *stubProvider) Stream(ctx context.Context, params *query.StreamParams) (<-chan query.StreamEvent, error) {
	return nil, errors.New("not implemented")
}

func (s *stubProvider) Send(ctx context.Context, params *query.SendParams) (*query.Message, *query.Usage, error) {
	if s.sendFn != nil {
		return s.sendFn(ctx, params)
	}
	return &query.Message{
		Role: query.RoleAssistant,
		Content: []query.ContentBlock{
			{Type: query.ContentTypeText, Text: "optimized prompt"},
		},
	}, &query.Usage{}, nil
}

func TestPromptEnhancer_Enhance_Success(t *testing.T) {
	prov := &stubProvider{
		sendFn: func(ctx context.Context, params *query.SendParams) (*query.Message, *query.Usage, error) {
			// 验证 system prompt 已设置
			if len(params.System) == 0 {
				t.Error("expected system prompt to be set")
			}
			if len(params.Messages) != 1 {
				t.Errorf("expected 1 message, got %d", len(params.Messages))
			}
			return &query.Message{
				Role: query.RoleAssistant,
				Content: []query.ContentBlock{
					{Type: query.ContentTypeText, Text: "请帮我实现一个冒泡排序函数，要求时间复杂度 O(n²)，使用 Go 语言"},
				},
			}, &query.Usage{InputTokens: 10, OutputTokens: 5}, nil
		},
	}

	enhancer := NewPromptEnhancer(prov, "test-model")
	enhancer.SetTimeout(5 * time.Second)

	ctx := context.Background()
	result, err := enhancer.Enhance(ctx, "帮我写个排序")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "帮我写个排序" {
		t.Error("expected result to differ from original")
	}
	if !strings.Contains(result, "冒泡排序") {
		t.Errorf("expected enhanced result to contain '冒泡排序', got %q", result)
	}
}

func TestPromptEnhancer_Enhance_EmptyInput(t *testing.T) {
	prov := &stubProvider{}
	enhancer := NewPromptEnhancer(prov, "test-model")

	ctx := context.Background()
	_, err := enhancer.Enhance(ctx, "")
	if err == nil {
		t.Error("expected error for empty input")
	}
}

func TestPromptEnhancer_Enhance_WhitespaceOnly(t *testing.T) {
	prov := &stubProvider{}
	enhancer := NewPromptEnhancer(prov, "test-model")

	ctx := context.Background()
	_, err := enhancer.Enhance(ctx, "   ")
	if err == nil {
		t.Error("expected error for whitespace-only input")
	}
}

func TestPromptEnhancer_Enhance_APIError(t *testing.T) {
	prov := &stubProvider{
		sendFn: func(ctx context.Context, params *query.SendParams) (*query.Message, *query.Usage, error) {
			return nil, nil, errors.New("API unavailable")
		},
	}
	enhancer := NewPromptEnhancer(prov, "test-model")

	ctx := context.Background()
	result, err := enhancer.Enhance(ctx, "test prompt")
	if err == nil {
		t.Error("expected error for API failure")
	}
	if result != "test prompt" {
		t.Errorf("expected original prompt returned on error, got %q", result)
	}
}

func TestPromptEnhancer_Enhance_EmptyResponse(t *testing.T) {
	prov := &stubProvider{
		sendFn: func(ctx context.Context, params *query.SendParams) (*query.Message, *query.Usage, error) {
			return &query.Message{
				Role:    query.RoleAssistant,
				Content: nil,
			}, &query.Usage{}, nil
		},
	}
	enhancer := NewPromptEnhancer(prov, "test-model")

	ctx := context.Background()
	result, err := enhancer.Enhance(ctx, "test prompt")
	if err == nil {
		t.Error("expected error for empty response")
	}
	if result != "test prompt" {
		t.Errorf("expected original prompt on empty response, got %q", result)
	}
}

func TestPromptEnhancer_Enhance_NoMeaningfulChange(t *testing.T) {
	prov := &stubProvider{
		sendFn: func(ctx context.Context, params *query.SendParams) (*query.Message, *query.Usage, error) {
			return &query.Message{
				Role: query.RoleAssistant,
				Content: []query.ContentBlock{
					{Type: query.ContentTypeText, Text: "test prompt"},
				},
			}, &query.Usage{}, nil
		},
	}
	enhancer := NewPromptEnhancer(prov, "test-model")

	ctx := context.Background()
	result, err := enhancer.Enhance(ctx, "test prompt")
	if err == nil {
		t.Error("expected error when enhanced text equals original")
	}
	if result != "test prompt" {
		t.Errorf("expected original prompt, got %q", result)
	}
}

func TestPromptEnhancer_Enhance_Timeout(t *testing.T) {
	prov := &stubProvider{
		sendFn: func(ctx context.Context, params *query.SendParams) (*query.Message, *query.Usage, error) {
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(2 * time.Second):
				return &query.Message{
					Role:    query.RoleAssistant,
					Content: []query.ContentBlock{{Type: query.ContentTypeText, Text: "ok"}},
				}, &query.Usage{}, nil
			}
		},
	}
	enhancer := NewPromptEnhancer(prov, "test-model")
	enhancer.SetTimeout(50 * time.Millisecond)

	ctx := context.Background()
	result, err := enhancer.Enhance(ctx, "test prompt")
	if err == nil {
		t.Error("expected timeout error")
	}
	if result != "test prompt" {
		t.Errorf("expected original prompt on timeout, got %q", result)
	}
}

func TestNewPromptEnhancer(t *testing.T) {
	prov := &stubProvider{}
	enhancer := NewPromptEnhancer(prov, "gpt-4")
	if enhancer == nil {
		t.Fatal("NewPromptEnhancer returned nil")
	}
	if enhancer.model != "gpt-4" {
		t.Errorf("expected model 'gpt-4', got %q", enhancer.model)
	}
	if enhancer.timeout != 30*time.Second {
		t.Errorf("expected default timeout 30s, got %v", enhancer.timeout)
	}
}

func TestPromptEnhancer_SetTimeout(t *testing.T) {
	prov := &stubProvider{}
	enhancer := NewPromptEnhancer(prov, "test")
	enhancer.SetTimeout(10 * time.Second)
	if enhancer.timeout != 10*time.Second {
		t.Errorf("expected 10s timeout, got %v", enhancer.timeout)
	}
}
