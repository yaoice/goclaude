// Package command 定义命令系统领域模型
package command

import (
	"context"
	"fmt"
	"sync"
)

// CommandType 命令类型
type CommandType string

const (
	// CommandTypePrompt 提示词命令（将内容注入AI上下文）
	CommandTypePrompt CommandType = "prompt"
	// CommandTypeLocal 本地命令（直接执行本地操作）
	CommandTypeLocal CommandType = "local"
)

// Command 命令接口
type Command interface {
	// Name 命令名称（如 "commit", "review"）
	Name() string
	// Aliases 命令别名
	Aliases() []string
	// Description 命令描述
	Description() string
	// Type 命令类型
	Type() CommandType
	// IsEnabled 命令是否启用
	IsEnabled() bool
}

// PromptCommand 提示词命令 - 将内容注入AI上下文
type PromptCommand interface {
	Command
	// GetPrompt 获取注入的提示词内容
	GetPrompt(ctx context.Context, args string) ([]ContentBlock, error)
}

// LocalCommand 本地命令 - 直接执行操作
type LocalCommand interface {
	Command
	// Execute 执行命令
	Execute(ctx context.Context, args string) (*CommandResult, error)
}

// ContentBlock 命令产生的内容块
type ContentBlock struct {
	Type string `json:"type"` // "text", "image"
	Text string `json:"text,omitempty"`
}

// CommandResult 命令执行结果
type CommandResult struct {
	// Output 输出文本
	Output string
	// ShouldExit 是否退出会话
	ShouldExit bool
	// Messages 追加到对话的消息
	Messages []ContentBlock
}

// Registry 命令注册表
type Registry struct {
	mu       sync.RWMutex
	commands map[string]Command
	aliases  map[string]string
}

// NewRegistry 创建命令注册表
func NewRegistry() *Registry {
	return &Registry{
		commands: make(map[string]Command),
		aliases:  make(map[string]string),
	}
}

// Register 注册命令
func (r *Registry) Register(cmd Command) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := cmd.Name()
	if _, exists := r.commands[name]; exists {
		return fmt.Errorf("command %q already registered", name)
	}

	r.commands[name] = cmd
	for _, alias := range cmd.Aliases() {
		r.aliases[alias] = name
	}
	return nil
}

// Get 获取命令
func (r *Registry) Get(name string) (Command, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if cmd, ok := r.commands[name]; ok {
		return cmd, true
	}
	if realName, ok := r.aliases[name]; ok {
		return r.commands[realName], true
	}
	return nil, false
}

// GetAll 获取所有命令
func (r *Registry) GetAll() []Command {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cmds := make([]Command, 0, len(r.commands))
	for _, cmd := range r.commands {
		if cmd.IsEnabled() {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// ParseCommand 解析命令字符串（如 "/commit fix bug"）
// 返回命令名和参数
func ParseCommand(input string) (name string, args string, isCommand bool) {
	if len(input) == 0 || input[0] != '/' {
		return "", "", false
	}

	// 去掉前缀 /
	input = input[1:]

	// 分割命令名和参数
	for i, ch := range input {
		if ch == ' ' {
			return input[:i], input[i+1:], true
		}
	}
	return input, "", true
}
