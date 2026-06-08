// Package todo 提供 TodoStore 的内存实现
//
// 单进程内的 todo 持久化；按 sessionID 隔离。线程安全。
package todo

import (
	"context"
	"sync"
)

// MemoryStore 内存实现的 TodoStore
//
// 用于 claude run 的单会话场景。可换成基于文件的实现以跨进程持久化。
type MemoryStore struct {
	mu    sync.RWMutex
	state map[string]string // sessionID -> latest todos JSON
}

// NewMemoryStore 创建内存 store
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{state: make(map[string]string)}
}

// Write 实现 tool.TodoStore.Write
//
// merge=true 时简化处理：直接覆盖（合并语义需要 JSON parse；当前最小实现）。
// 后续可扩展为 jsonpatch / 合并 by id。
func (s *MemoryStore) Write(_ context.Context, sessionID, todosJSON string, _ bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sessionID == "" {
		sessionID = "default"
	}
	s.state[sessionID] = todosJSON
	return nil
}

// Read 实现 tool.TodoStore.Read
func (s *MemoryStore) Read(_ context.Context, sessionID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if sessionID == "" {
		sessionID = "default"
	}
	return s.state[sessionID], nil
}

// Snapshot 返回所有会话的 todo 快照（用于调试 / TUI 显示）
func (s *MemoryStore) Snapshot() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.state))
	for k, v := range s.state {
		out[k] = v
	}
	return out
}
