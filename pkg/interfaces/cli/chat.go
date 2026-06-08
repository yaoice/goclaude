package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/anthropics/goclaude/pkg/domain/query"
	"github.com/anthropics/goclaude/pkg/infrastructure/api/anthropic"
	"github.com/anthropics/goclaude/pkg/infrastructure/api/deepseek"
	"github.com/spf13/cobra"
)

// 模型 provider 选择
const (
	providerAnthropic = "anthropic"
	providerDeepSeek  = "deepseek"
)

var (
	chatProvider string
	chatModel    string
	chatSystem   string
)

// newChatCmd 单轮对话子命令，用于快速验证 Provider 接入
//
// 用法:
//
//	export DEEPSEEK_API_KEY=sk-xxx
//	./goclaude chat -p deepseek -m deepseek-chat "你好"
func newChatCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chat [prompt]",
		Short: "向AI模型发起单轮流式对话（用于验证Provider接入）",
		Long: `向所选AI Provider发起一次流式对话请求，并将响应增量打印到stdout。

支持的Provider:
  - anthropic  (默认, 需要 ANTHROPIC_API_KEY)
  - deepseek   (需要 DEEPSEEK_API_KEY)

示例:
  ` + "`" + `goclaude chat "解释一下DDD" ` + "`" + `
  ` + "`" + `goclaude chat -p deepseek -m deepseek-chat "写一个快速排序"` + "`",
		Args: cobra.MinimumNArgs(1),
		RunE: runChat,
	}

	cmd.Flags().StringVarP(&chatProvider, "provider", "p", providerDeepSeek, "AI Provider: anthropic | deepseek")
	cmd.Flags().StringVarP(&chatModel, "chat-model", "M", "", "模型名（覆盖 --model 全局标志）")
	cmd.Flags().StringVarP(&chatSystem, "system", "s", "", "系统提示词")
	return cmd
}

// runChat 执行单轮 chat
func runChat(cmd *cobra.Command, args []string) error {
	prompt := strings.Join(args, " ")

	// 1. 构造 Provider
	provider, modelName, err := buildProvider(chatProvider)
	if err != nil {
		return err
	}
	// 优先 -M（chat 局部）；其次仅当全局 -m 被显式指定时才覆盖
	if chatModel != "" {
		modelName = chatModel
	} else if cmd.Root().PersistentFlags().Changed("model") && flagModel != "" {
		modelName = flagModel
	}

	// 2. 处理 Ctrl+C 取消
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()
	go handleInterrupt(cancel)

	// 3. 构造请求参数
	cfg := AppConfig()
	params := &query.StreamParams{
		Model:     modelName,
		MaxTokens: cfg.API.MaxTokens,
		Messages: []query.Message{
			query.NewTextMessage(query.RoleUser, prompt),
		},
	}
	if cfg.API.Temperature > 0 {
		params.Temperature = cfg.API.Temperature
	}
	if chatSystem != "" {
		params.System = []query.ContentBlock{
			{Type: query.ContentTypeText, Text: chatSystem},
		}
	}

	if flagVerbose {
		fmt.Fprintf(os.Stderr, "[provider=%s model=%s prompt_len=%d]\n",
			chatProvider, modelName, len(prompt))
	}

	// 4. 发起流式请求并实时打印
	startedAt := time.Now()
	events, err := provider.Stream(ctx, params)
	if err != nil {
		return fmt.Errorf("stream request failed: %w", err)
	}

	var (
		usage      *query.Usage
		stopReason query.StopReason
		hadError   error
	)
	for ev := range events {
		switch ev.Type {
		case query.EventContentBlockDelta:
			if ev.Delta != nil && ev.Delta.Text != "" {
				fmt.Print(ev.Delta.Text)
			}
		case query.EventMessageDelta:
			if ev.Usage != nil {
				usage = ev.Usage
			}
			if ev.StopReason != "" {
				stopReason = ev.StopReason
			}
		case query.EventError:
			hadError = ev.Error
		}
	}
	fmt.Println()

	if hadError != nil {
		return hadError
	}

	if flagVerbose {
		elapsed := time.Since(startedAt)
		fmt.Fprintf(os.Stderr, "\n[stop=%s elapsed=%s", stopReason, elapsed.Truncate(time.Millisecond))
		if usage != nil {
			fmt.Fprintf(os.Stderr, " input=%d output=%d", usage.InputTokens, usage.OutputTokens)
		}
		fmt.Fprintln(os.Stderr, "]")
	}

	return nil
}

// buildProvider 根据名称构造 Provider 实例
//
// 凭证（API Key）走 env，其余参数（base_url / timeout / max_retries 等）全部走 YAML。
// 这保证了"参数 → 配置文件、凭证 → 环境变量"的清晰边界。
func buildProvider(name string) (query.AIProvider, string, error) {
	cfg := AppConfig()
	switch strings.ToLower(name) {
	case providerAnthropic, "":
		key := firstNonEmpty(
			os.Getenv("ANTHROPIC_API_KEY"),
			os.Getenv("CLAUDE_API_KEY"),
		)
		if key == "" {
			return nil, "", fmt.Errorf("missing ANTHROPIC_API_KEY")
		}
		ccfg := anthropic.DefaultClientConfig(key)
		if pc, ok := cfg.Providers[providerAnthropic]; ok {
			if pc.BaseURL != "" {
				ccfg.BaseURL = pc.BaseURL
			}
			if pc.APIVersion != "" {
				ccfg.APIVersion = pc.APIVersion
			}
			if pc.Timeout > 0 {
				ccfg.Timeout = pc.Timeout
			}
			if pc.MaxRetries > 0 {
				ccfg.MaxRetries = pc.MaxRetries
			}
			if pc.RetryBaseDelay > 0 {
				ccfg.RetryBaseDelay = pc.RetryBaseDelay
			}
		}
		defaultModel := "claude-sonnet-4-20250514"
		if pc, ok := cfg.Providers[providerAnthropic]; ok && pc.DefaultModel != "" {
			defaultModel = pc.DefaultModel
		}
		return anthropic.NewClient(ccfg), defaultModel, nil

	case providerDeepSeek:
		key := os.Getenv("DEEPSEEK_API_KEY")
		if key == "" {
			return nil, "", fmt.Errorf("missing DEEPSEEK_API_KEY (export DEEPSEEK_API_KEY=sk-xxx)")
		}
		ccfg := deepseek.DefaultClientConfig(key)
		if pc, ok := cfg.Providers[providerDeepSeek]; ok {
			if pc.BaseURL != "" {
				ccfg.BaseURL = pc.BaseURL
			}
			if pc.Timeout > 0 {
				ccfg.Timeout = pc.Timeout
			}
			if pc.MaxRetries > 0 {
				ccfg.MaxRetries = pc.MaxRetries
			}
			if pc.RetryBaseDelay > 0 {
				ccfg.RetryBaseDelay = pc.RetryBaseDelay
			}
		}
		defaultModel := deepseek.ModelChat
		if pc, ok := cfg.Providers[providerDeepSeek]; ok && pc.DefaultModel != "" {
			defaultModel = pc.DefaultModel
		}
		return deepseek.NewClient(ccfg), defaultModel, nil

	default:
		return nil, "", fmt.Errorf("unsupported provider: %s (supported: anthropic, deepseek)", name)
	}
}

// firstNonEmpty 返回第一个非空字符串
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// handleInterrupt 监听 Ctrl+C 信号取消 ctx
func handleInterrupt(cancel context.CancelFunc) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	cancel()
}
