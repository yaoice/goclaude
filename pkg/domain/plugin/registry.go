package plugin

import (
	"context"
	"sort"
	"sync"
)

// Registry 插件与市场的内存注册表（线程安全）。
//
// 仅维护运行时状态；持久化由 Store 负责。装配阶段从 Store 载入 State 后
// 调用 Load 填充本注册表。
type Registry struct {
	mu           sync.RWMutex
	marketplaces map[string]*Marketplace
	plugins      map[string]*Plugin
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{
		marketplaces: make(map[string]*Marketplace),
		plugins:      make(map[string]*Plugin),
	}
}

// Load 用持久化的 State 重建注册表。
func (r *Registry) Load(st *State) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.marketplaces = make(map[string]*Marketplace)
	r.plugins = make(map[string]*Plugin)
	if st == nil {
		return
	}
	for _, m := range st.Marketplaces {
		if m != nil && m.Name != "" {
			r.marketplaces[m.Name] = m
		}
	}
	for _, p := range st.Plugins {
		if p != nil && p.Name != "" {
			r.plugins[p.Name] = p
		}
	}
}

// Snapshot 导出当前状态用于持久化。
func (r *Registry) Snapshot() *State {
	r.mu.RLock()
	defer r.mu.RUnlock()
	st := &State{}
	for _, m := range r.marketplaces {
		st.Marketplaces = append(st.Marketplaces, m)
	}
	for _, p := range r.plugins {
		st.Plugins = append(st.Plugins, p)
	}
	sort.Slice(st.Marketplaces, func(i, j int) bool { return st.Marketplaces[i].Name < st.Marketplaces[j].Name })
	sort.Slice(st.Plugins, func(i, j int) bool { return st.Plugins[i].Name < st.Plugins[j].Name })
	return st
}

// AddMarketplace 注册或覆盖一个市场。
func (r *Registry) AddMarketplace(m *Marketplace) {
	if m == nil || m.Name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.marketplaces[m.Name] = m
}

// RemoveMarketplace 移除市场。返回是否存在。
func (r *Registry) RemoveMarketplace(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.marketplaces[name]; !ok {
		return false
	}
	delete(r.marketplaces, name)
	return true
}

// GetMarketplace 获取市场。
func (r *Registry) GetMarketplace(name string) (*Marketplace, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.marketplaces[name]
	return m, ok
}

// Marketplaces 返回所有市场，按名排序。
func (r *Registry) Marketplaces() []*Marketplace {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Marketplace, 0, len(r.marketplaces))
	for _, m := range r.marketplaces {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// AddPlugin 注册或覆盖一个已安装插件。
func (r *Registry) AddPlugin(p *Plugin) {
	if p == nil || p.Name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plugins[p.Name] = p
}

// RemovePlugin 移除插件。返回被移除的实例与是否存在。
func (r *Registry) RemovePlugin(name string) (*Plugin, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.plugins[name]
	if !ok {
		return nil, false
	}
	delete(r.plugins, name)
	return p, true
}

// GetPlugin 获取插件。
func (r *Registry) GetPlugin(name string) (*Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plugins[name]
	return p, ok
}

// SetEnabled 设置插件启用状态。返回是否存在。
func (r *Registry) SetEnabled(name string, enabled bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.plugins[name]
	if !ok {
		return false
	}
	p.Enabled = enabled
	return true
}

// Plugins 返回所有插件，按名排序。
func (r *Registry) Plugins() []*Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Plugin, 0, len(r.plugins))
	for _, p := range r.plugins {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// EnabledPlugins 返回所有已启用插件，按名排序。
func (r *Registry) EnabledPlugins() []*Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Plugin, 0, len(r.plugins))
	for _, p := range r.plugins {
		if p.Enabled {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ---- 依赖倒置接口（由 infrastructure 实现） ----

// Installer 负责把一个来源安装到本地目录，以及卸载。
//
// 实现必须保证安全：远程来源做 SSRF 校验、压缩包做 zip-slip 防护、限制大小与超时。
type Installer interface {
	// Install 把 source（local/git/http）安装到 destDir，返回插件根目录绝对路径。
	Install(ctx context.Context, source string, destDir string) (rootPath string, err error)
	// Uninstall 删除已安装的插件根目录。
	Uninstall(ctx context.Context, rootPath string) error
}

// MarketplaceLoader 负责把一个市场来源拉取到本地并解析其清单。
type MarketplaceLoader interface {
	// Load 把 source 拉取到 cacheDir 并解析 marketplace.json，返回 Marketplace。
	Load(ctx context.Context, source string, cacheDir string) (*Marketplace, error)
	// ParseManifest 解析已位于本地的插件根目录的 plugin.json。
	ParseManifest(rootPath string) (*Manifest, error)
}

// Store 负责插件系统状态的持久化。
type Store interface {
	Load() (*State, error)
	Save(*State) error
}
