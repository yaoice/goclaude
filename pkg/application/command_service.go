package application

import (
	"context"
	"log/slog"

	"github.com/anthropics/goclaude/pkg/domain/command"
)

// CommandService 命令应用服务
type CommandService struct {
	registry *command.Registry
	logger   *slog.Logger
}

// NewCommandService 创建命令服务
func NewCommandService(registry *command.Registry, logger *slog.Logger) *CommandService {
	return &CommandService{
		registry: registry,
		logger:   logger,
	}
}

// HandleInput 处理用户输入（判断是否为命令）
// 返回 true 表示是命令并已处理，false 表示需要发送给AI
func (s *CommandService) HandleInput(ctx context.Context, input string) (bool, *command.CommandResult, error) {
	name, args, isCmd := command.ParseCommand(input)
	if !isCmd {
		return false, nil, nil
	}

	cmd, ok := s.registry.Get(name)
	if !ok {
		return false, nil, nil // 未知命令，当作普通输入
	}

	s.logger.Debug("执行命令", "command", name, "args", args)

	switch c := cmd.(type) {
	case command.LocalCommand:
		result, err := c.Execute(ctx, args)
		return true, result, err
	case command.PromptCommand:
		// 提示词命令：获取内容注入到上下文
		blocks, err := c.GetPrompt(ctx, args)
		if err != nil {
			return true, nil, err
		}
		return true, &command.CommandResult{
			Messages: blocks,
		}, nil
	}

	return false, nil, nil
}

// ListCommands 列出所有可用命令
func (s *CommandService) ListCommands() []command.Command {
	return s.registry.GetAll()
}
