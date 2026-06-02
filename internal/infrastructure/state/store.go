// Package state 提供内存状态Store实现
package state

import (
	"sync"
)

// Observer 状态观察者
type Observer func(key string, value interface{})

// Store 内存状态Store（线程安全）
// 使用 sync.RWMutex 保护，通过 channel 实现观察者通知
type Store struct {
	mu        sync.RWMutex
	data      map[string]interface{}
	observers []Observer
}

// NewStore 创建状态Store
func NewStore() *Store {
	return &Store{
		data:      make(map[string]interface{}),
		observers: make([]Observer, 0),
	}
}

// Get 获取状态值
func (s *Store) Get(key string) (interface{}, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	return v, ok
}

// GetString 获取字符串状态值
func (s *Store) GetString(key string) string {
	v, ok := s.Get(key)
	if !ok {
		return ""
	}
	if str, ok := v.(string); ok {
		return str
	}
	return ""
}

// GetInt 获取整数状态值
func (s *Store) GetInt(key string) int {
	v, ok := s.Get(key)
	if !ok {
		return 0
	}
	if n, ok := v.(int); ok {
		return n
	}
	return 0
}

// Set 设置状态值（触发观察者通知）
func (s *Store) Set(key string, value interface{}) {
	s.mu.Lock()
	s.data[key] = value
	observers := make([]Observer, len(s.observers))
	copy(observers, s.observers)
	s.mu.Unlock()

	// 通知观察者（在锁外执行，避免死锁）
	for _, obs := range observers {
		obs(key, value)
	}
}

// Delete 删除状态值
func (s *Store) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
}

// Subscribe 订阅状态变更
func (s *Store) Subscribe(observer Observer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observers = append(s.observers, observer)
}

// Snapshot 获取状态快照
func (s *Store) Snapshot() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := make(map[string]interface{}, len(s.data))
	for k, v := range s.data {
		snapshot[k] = v
	}
	return snapshot
}
