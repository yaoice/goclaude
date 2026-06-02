package tool

import (
	"fmt"
	"sync"
)

// FilterFunc 工具过滤函数
type FilterFunc func(Tool) bool

// Registry 工具注册表 - 管理所有工具的注册、查找与过滤
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool   // name -> tool 映射
	alias map[string]string // alias -> name 映射
}

// NewRegistry 创建工具注册表
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
		alias: make(map[string]string),
	}
}

// Register 注册工具
func (r *Registry) Register(t Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := t.Name()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool %q already registered", name)
	}

	r.tools[name] = t

	// 注册别名
	for _, alias := range t.Aliases() {
		r.alias[alias] = name
	}

	return nil
}

// MustRegister 注册工具，失败则panic
func (r *Registry) MustRegister(t Tool) {
	if err := r.Register(t); err != nil {
		panic(err)
	}
}

// Get 获取工具（支持名称或别名查找）
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// 先查找直接名称
	if t, ok := r.tools[name]; ok {
		return t, true
	}

	// 再通过别名查找
	if realName, ok := r.alias[name]; ok {
		if t, ok := r.tools[realName]; ok {
			return t, true
		}
	}

	return nil, false
}

// GetAll 获取所有工具（支持过滤）
func (r *Registry) GetAll(filters ...FilterFunc) []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []Tool
	for _, t := range r.tools {
		include := true
		for _, filter := range filters {
			if !filter(t) {
				include = false
				break
			}
		}
		if include {
			result = append(result, t)
		}
	}
	return result
}

// GetEnabled 获取所有已启用的工具
func (r *Registry) GetEnabled() []Tool {
	return r.GetAll(func(t Tool) bool {
		return t.IsEnabled()
	})
}

// Names 返回所有已注册工具的名称列表
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// Count 返回已注册工具数量
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// Unregister 移除工具
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if t, ok := r.tools[name]; ok {
		// 移除别名
		for _, alias := range t.Aliases() {
			delete(r.alias, alias)
		}
		delete(r.tools, name)
	}
}
