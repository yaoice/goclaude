package mcpinfra

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/anthropics/goclaude/internal/domain/mcp"
)

// StdioTransport 基于子进程 stdin/stdout 的 MCP 传输
type StdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	sendMu  sync.Mutex
	reader  *bufio.Reader
	started bool
}

// NewStdioTransport 创建 stdio 传输
func NewStdioTransport(command string, args []string, env map[string]string) *StdioTransport {
	cmd := exec.Command(command, args...)
	if len(env) > 0 {
		cmd.Env = cmd.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}
	return &StdioTransport{cmd: cmd}
}

// Start 启动子进程
func (t *StdioTransport) Start(ctx context.Context) error {
	if t.started {
		return nil
	}
	var err error
	t.stdin, err = t.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	t.stdout, err = t.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	if err := t.cmd.Start(); err != nil {
		return fmt.Errorf("start process: %w", err)
	}
	t.reader = bufio.NewReaderSize(t.stdout, 64*1024)
	t.started = true
	return nil
}

// Send 发送一条 JSON-RPC 消息（newline-delimited）
func (t *StdioTransport) Send(ctx context.Context, msg *mcp.JSONRPCMessage) error {
	t.sendMu.Lock()
	defer t.sendMu.Unlock()

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err := t.stdin.Write(data); err != nil {
		return err
	}
	_, err = t.stdin.Write([]byte{'\n'})
	return err
}

// Recv 读取一条 JSON-RPC 消息（阻塞，直到行可读或 EOF）
func (t *StdioTransport) Recv(ctx context.Context) (*mcp.JSONRPCMessage, error) {
	for {
		line, err := t.reader.ReadBytes('\n')
		if err != nil && len(line) == 0 {
			return nil, err
		}
		// 去掉尾部换行
		for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
			line = line[:len(line)-1]
		}
		if len(line) == 0 {
			continue
		}
		var msg mcp.JSONRPCMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			// 跳过无效 JSON（MCP 标准要求 newline-delimited JSON，这里宽容处理）
			continue
		}
		return &msg, nil
	}
}

// Close 关闭子进程
func (t *StdioTransport) Close() error {
	if t.stdin != nil {
		_ = t.stdin.Close()
	}
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
		_, _ = t.cmd.Process.Wait()
	}
	return nil
}
