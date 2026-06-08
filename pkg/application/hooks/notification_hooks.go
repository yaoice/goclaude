// Package hooks — 应用层通知钩子
//
// 对齐 src/hooks/notifs/ 中的通知钩子实现：
//   - RateLimitWarning: 等价于 useRateLimitWarningNotification
//   - PluginInstallStatus: 等价于 usePluginInstallationStatus
//   - MCPConnectivityTracker: 等价于 useMcpConnectivityStatus
//   - SettingsErrorTracker: 等价于 useSettingsErrors
//
// 所有追踪器均依赖 hook.NotificationBus 进行通知发布。
package hooks

import (
	"fmt"
	"sync"
	"time"

	"github.com/anthropics/goclaude/pkg/domain/hook"
)

// RateLimitWarning 速率限制警告追踪器
//
// 对齐 src/hooks/notifs/useRateLimitWarningNotification：
// 监控 API token 用量和速率限制头，当剩余量低于阈值时发布警告通知。
type RateLimitWarning struct {
	bus         *hook.NotificationBus
	mu          sync.Mutex
	limit       int
	remaining   int
	resetAt     time.Time
	warnAt      float64       // 0.5 = warn at 50% remaining
	lastWarned  time.Time
	minInterval time.Duration
}

// NewRateLimitWarning 创建速率限制警告追踪器
//
// warnAt 为触发警告的剩余比例阈值（0~1），默认 0.5。
// minInterval 为两次警告之间的最小间隔，默认 5 分钟。
func NewRateLimitWarning(bus *hook.NotificationBus, warnAt float64, minInterval time.Duration) *RateLimitWarning {
	if bus == nil {
		bus = hook.NewNotificationBus(100)
	}
	if warnAt <= 0 {
		warnAt = 0.5
	}
	if minInterval <= 0 {
		minInterval = 5 * time.Minute
	}
	return &RateLimitWarning{bus: bus, warnAt: warnAt, minInterval: minInterval}
}

// Update 更新速率限制状态
//
// 当 remaining/limit 比值低于 warnAt 且距上次警告超过 minInterval 时，
// 发布一条 rate_limit 类型警告通知。
func (r *RateLimitWarning) Update(limit, remaining int, resetAt time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.limit = limit
	r.remaining = remaining
	r.resetAt = resetAt

	if limit == 0 {
		return
	}

	ratio := float64(remaining) / float64(limit)
	if ratio <= r.warnAt && time.Since(r.lastWarned) >= r.minInterval {
		r.lastWarned = time.Now()
		r.bus.Publish(&hook.Notification{
			ID:    "rate-limit-warning",
			Type:  "rate_limit",
			Title: "Rate limit warning",
			Level: "warning",
			Body:  fmt.Sprintf("Rate limit: %d/%d remaining, resets at %s", remaining, limit, resetAt.Format(time.RFC3339)),
			Metadata: map[string]interface{}{
				"remaining": remaining,
				"limit":     limit,
				"resetAt":   resetAt,
			},
		})
	}
}

// State 返回当前速率限制状态（测试/调试用）
func (r *RateLimitWarning) State() (limit, remaining int, resetAt time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.limit, r.remaining, r.resetAt
}

// PluginInstallStatus 插件安装状态追踪器
//
// 对齐 src/hooks/notifs/usePluginInstallationStatus：
// 追踪插件安装生命周期（installing → installed / failed），并在状态变更时发布通知。
type PluginInstallStatus struct {
	bus    *hook.NotificationBus
	mu     sync.Mutex
	status map[string]string // pluginID → "installing" | "installed" | "failed" | "updating"
}

// NewPluginInstallStatus 创建插件安装状态追踪器
func NewPluginInstallStatus(bus *hook.NotificationBus) *PluginInstallStatus {
	if bus == nil {
		bus = hook.NewNotificationBus(100)
	}
	return &PluginInstallStatus{bus: bus, status: make(map[string]string)}
}

// SetInstalling 标记插件开始安装
func (p *PluginInstallStatus) SetInstalling(pluginID, name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status[pluginID] = "installing"
	p.bus.Publish(&hook.Notification{
		ID:    "plugin-install-" + pluginID,
		Type:  "plugin_install",
		Title: "Installing plugin",
		Level: "info",
		Body:  name + " is being installed...",
	})
}

// SetInstalled 标记插件安装成功
func (p *PluginInstallStatus) SetInstalled(pluginID, name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status[pluginID] = "installed"
	p.bus.Publish(&hook.Notification{
		ID:    "plugin-install-" + pluginID,
		Type:  "plugin_install",
		Title: "Plugin installed",
		Level: "info",
		Body:  name + " installed successfully",
	})
}

// SetFailed 标记插件安装失败
func (p *PluginInstallStatus) SetFailed(pluginID, name, err string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status[pluginID] = "failed"
	p.bus.Publish(&hook.Notification{
		ID:    "plugin-install-" + pluginID,
		Type:  "plugin_install",
		Title: "Plugin installation failed",
		Level: "error",
		Body:  name + ": " + err,
	})
}

// SetUpdating 标记插件正在更新
func (p *PluginInstallStatus) SetUpdating(pluginID, name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status[pluginID] = "updating"
	p.bus.Publish(&hook.Notification{
		ID:    "plugin-install-" + pluginID,
		Type:  "plugin_install",
		Title: "Updating plugin",
		Level: "info",
		Body:  name + " is being updated...",
	})
}

// Status 返回指定插件的安装状态
func (p *PluginInstallStatus) Status(pluginID string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status[pluginID]
}

// MCPConnectivityTracker MCP 连接状态追踪器
//
// 对齐 src/hooks/notifs/useMcpConnectivityStatus：
// 追踪 MCP 服务器连接状态变更，并在状态变化时发布通知。
type MCPConnectivityTracker struct {
	bus      *hook.NotificationBus
	mu       sync.Mutex
	statuses map[string]string // serverID → "connected" | "disconnected" | "error"
}

// NewMCPConnectivityTracker 创建 MCP 连接状态追踪器
func NewMCPConnectivityTracker(bus *hook.NotificationBus) *MCPConnectivityTracker {
	if bus == nil {
		bus = hook.NewNotificationBus(100)
	}
	return &MCPConnectivityTracker{bus: bus, statuses: make(map[string]string)}
}

// SetStatus 设置 MCP 服务器连接状态
//
// 仅当状态发生变更时才发布通知，避免重复通知。
func (m *MCPConnectivityTracker) SetStatus(serverID, name, status string, reason string) {
	m.mu.Lock()
	prev := m.statuses[serverID]
	m.statuses[serverID] = status
	m.mu.Unlock()

	if prev != status {
		title := "MCP server " + status
		level := "info"
		if status == "error" || status == "disconnected" {
			level = "warning"
		}
		n := &hook.Notification{
			ID:    "mcp-" + serverID,
			Type:  "mcp_connectivity",
			Title: title,
			Level: level,
			Body:  name + ": " + status,
		}
		if reason != "" {
			n.Body += " (" + reason + ")"
		}
		m.bus.Publish(n)
	}
}

// Status 返回指定 MCP 服务器的连接状态
func (m *MCPConnectivityTracker) Status(serverID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statuses[serverID]
}

// AllStatuses 返回所有 MCP 服务器状态（测试/调试用）
func (m *MCPConnectivityTracker) AllStatuses() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]string, len(m.statuses))
	for k, v := range m.statuses {
		result[k] = v
	}
	return result
}

// SettingsErrorTracker 设置错误追踪器
//
// 对齐 src/hooks/notifs/useSettingsErrors：
// 收集并追踪配置文件/设置相关的错误，支持设置/清除/列出。
type SettingsErrorTracker struct {
	bus    *hook.NotificationBus
	mu     sync.Mutex
	errors map[string]string // path → error message
}

// NewSettingsErrorTracker 创建设置错误追踪器
func NewSettingsErrorTracker(bus *hook.NotificationBus) *SettingsErrorTracker {
	if bus == nil {
		bus = hook.NewNotificationBus(100)
	}
	return &SettingsErrorTracker{bus: bus, errors: make(map[string]string)}
}

// SetError 设置某个路径的错误信息，并发布通知
func (s *SettingsErrorTracker) SetError(path, errMsg string) {
	s.mu.Lock()
	s.errors[path] = errMsg
	s.mu.Unlock()
	s.bus.Publish(&hook.Notification{
		ID:    "settings-error-" + path,
		Type:  "settings_error",
		Title: "Settings error",
		Level: "error",
		Body:  path + ": " + errMsg,
	})
}

// Clear 清除某个路径的错误
func (s *SettingsErrorTracker) Clear(path string) {
	s.mu.Lock()
	delete(s.errors, path)
	s.mu.Unlock()
}

// ClearAll 清除所有错误
func (s *SettingsErrorTracker) ClearAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errors = make(map[string]string)
}

// List 返回所有设置错误的副本
func (s *SettingsErrorTracker) List() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string]string, len(s.errors))
	for k, v := range s.errors {
		result[k] = v
	}
	return result
}

// Count 返回错误数量（测试/调试用）
func (s *SettingsErrorTracker) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.errors)
}
