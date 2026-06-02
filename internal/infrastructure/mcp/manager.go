// Package mcpinfra 提供 MCP 服务器管理与配置加载
package mcpinfra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/anthropics/goclaude/internal/domain/mcp"
)

// ReconnectPolicy 重连策略
type ReconnectPolicy struct {
	// MaxAttempts 最多尝试次数；0 表示不重连，<0 表示无限重试
	MaxAttempts int
	// InitialBackoff 第一次重连前的等待
	InitialBackoff time.Duration
	// MaxBackoff 退避上限
	MaxBackoff time.Duration
	// Multiplier 退避倍数
	Multiplier float64
}

// DefaultReconnectPolicy 提供合理默认（指数退避，1s → 30s，最多 5 次）
func DefaultReconnectPolicy() ReconnectPolicy {
	return ReconnectPolicy{
		MaxAttempts:    5,
		InitialBackoff: time.Second,
		MaxBackoff:     30 * time.Second,
		Multiplier:     2.0,
	}
}

// Manager 管理多个 MCP 客户端连接（线程安全）
//
// 对齐 src/services/mcp/MCPConnectionManager.tsx 的核心职责：
//   - 按名字记忆已连接 client
//   - 提供启动/停止/列出/获取
//   - 失败的连接保存错误状态而非整体崩溃
//   - 监听 notifications/tools/list_changed 并向订阅者派发
type Manager struct {
	mu       sync.RWMutex
	clients  map[string]*ClientImpl
	statuses map[string]ConnectionStatus
	configs  map[string]*mcp.ServerConfig // 用于重连

	// toolsChangedSubs 工具列表变更订阅者
	subsMu           sync.RWMutex
	toolsChangedSubs []ToolsChangedHandler

	// reconnect 重连策略；零值时取 DefaultReconnectPolicy
	reconnect ReconnectPolicy
	logger    *slog.Logger

	// disconnectedSubs 连接断开通知（用于上层日志/UI 提示）
	disconnectedSubs []DisconnectedHandler
}

// DisconnectedHandler 当某个 MCP 服务器在运行中失联时触发
//
// reason 来自底层 transport 的退出错误（io.EOF 表示对端关闭）。
// reconnecting 表示 Manager 已开始尝试重连。
type DisconnectedHandler func(serverName string, reason error, reconnecting bool)

// ToolsChangedHandler 工具列表变更通知
//
// 当 MCP 服务器主动推送 notifications/tools/list_changed 时被调用。
// serverName 标识哪个服务器的工具列表变了。
type ToolsChangedHandler func(serverName string)

// ConnectionStatus 连接状态
type ConnectionStatus struct {
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
	Error     string `json:"error,omitempty"`
}

// NewManager 创建管理器
func NewManager() *Manager {
	return &Manager{
		clients:   make(map[string]*ClientImpl),
		statuses:  make(map[string]ConnectionStatus),
		configs:   make(map[string]*mcp.ServerConfig),
		reconnect: DefaultReconnectPolicy(),
		logger:    slog.Default(),
	}
}

// SetReconnectPolicy 调整重连策略；将 MaxAttempts=0 可禁用重连
func (m *Manager) SetReconnectPolicy(p ReconnectPolicy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reconnect = p
}

// SetLogger 注入 logger
func (m *Manager) SetLogger(l *slog.Logger) {
	if l == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logger = l
}

// OnDisconnected 订阅服务器断开通知
func (m *Manager) OnDisconnected(h DisconnectedHandler) {
	if h == nil {
		return
	}
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	m.disconnectedSubs = append(m.disconnectedSubs, h)
}

func (m *Manager) fireDisconnected(name string, err error, reconnecting bool) {
	m.subsMu.RLock()
	subs := append([]DisconnectedHandler(nil), m.disconnectedSubs...)
	m.subsMu.RUnlock()
	for _, h := range subs {
		h(name, err, reconnecting)
	}
}

// Connect 连接到指定 MCP 服务器（已连接则复用）
//
// 行为：
//   - 已连接则直接返回缓存 client
//   - 连接失败时从 map 中删除（避免 Get 拿到死 client）
//   - 连接成功后启动 watchdog goroutine 监听 client.Done() 触发重连
func (m *Manager) Connect(ctx context.Context, cfg *mcp.ServerConfig) (*ClientImpl, error) {
	if cfg == nil || cfg.Name == "" {
		return nil, errors.New("invalid mcp server config")
	}
	m.mu.Lock()
	if c, ok := m.clients[cfg.Name]; ok && c.IsConnected() {
		m.mu.Unlock()
		return c, nil
	}
	client := NewClient(cfg)
	m.clients[cfg.Name] = client
	m.configs[cfg.Name] = cfg // 保留 config 给 watchdog 重连用
	m.mu.Unlock()

	// 订阅 tools/list_changed 通知，转发给 Manager 订阅者
	serverName := cfg.Name
	client.OnNotification("notifications/tools/list_changed", func(_ string, _ json.RawMessage) {
		m.fireToolsChanged(serverName)
	})

	if err := client.Connect(ctx); err != nil {
		// 失败：从 map 删除避免 Get/All 拿到死 client（修复 m3）
		m.mu.Lock()
		delete(m.clients, cfg.Name)
		m.mu.Unlock()
		m.setStatus(cfg.Name, ConnectionStatus{Name: cfg.Name, Connected: false, Error: err.Error()})
		return nil, err
	}
	m.setStatus(cfg.Name, ConnectionStatus{Name: cfg.Name, Connected: true})

	// 启动 watchdog：当 client 失联时按策略重连
	go m.watchAndReconnect(serverName)

	return client, nil
}

// watchAndReconnect 监视 client 断开，按 ReconnectPolicy 重连
//
// 当 ClientImpl 的读循环退出（recvErr 非 nil 或 closed）时本函数被唤醒：
//   - 若被显式 Disconnect：不重连
//   - 否则按 reconnect 策略重试，成功后重新启动 watchdog
func (m *Manager) watchAndReconnect(name string) {
	m.mu.RLock()
	client := m.clients[name]
	m.mu.RUnlock()
	if client == nil {
		return
	}
	// 等 client 退出（失联 / 主动 Close 都会触发）
	<-client.Done()

	// 显式被 Disconnect 调用过：configs 里已删除
	m.mu.RLock()
	cfg, stillManaged := m.configs[name]
	policy := m.reconnect
	logger := m.logger
	m.mu.RUnlock()
	if !stillManaged {
		return
	}

	reason := client.RecvErr()
	logger.Warn("MCP 服务器失联", "server", name, "error", reason)
	m.fireDisconnected(name, reason, policy.MaxAttempts != 0)

	if policy.MaxAttempts == 0 {
		m.setStatus(name, ConnectionStatus{Name: name, Connected: false, Error: "disconnected"})
		return
	}

	backoff := policy.InitialBackoff
	if backoff <= 0 {
		backoff = time.Second
	}
	for attempt := 1; policy.MaxAttempts < 0 || attempt <= policy.MaxAttempts; attempt++ {
		time.Sleep(backoff)

		// 重连前从 map 删旧 client，不影响调用方拿新 client
		m.mu.Lock()
		// 二次确认仍要重连（可能并发被 Disconnect）
		if _, ok := m.configs[name]; !ok {
			m.mu.Unlock()
			return
		}
		delete(m.clients, name)
		m.mu.Unlock()

		logger.Debug("MCP 重连尝试", "server", name, "attempt", attempt)
		_, err := m.Connect(context.Background(), cfg)
		if err == nil {
			logger.Debug("MCP 重连成功", "server", name, "attempt", attempt)
			// 触发 tools 变更，让上层重新刷新工具列表
			m.fireToolsChanged(name)
			return
		}
		logger.Warn("MCP 重连失败", "server", name, "attempt", attempt, "error", err)

		// 退避：指数 + 上限
		mult := policy.Multiplier
		if mult < 1 {
			mult = 2
		}
		backoff = time.Duration(float64(backoff) * mult)
		if policy.MaxBackoff > 0 && backoff > policy.MaxBackoff {
			backoff = policy.MaxBackoff
		}
	}
	logger.Error("MCP 达到最大重连次数，放弃", "server", name, "max_attempts", policy.MaxAttempts)
}

// OnToolsChanged 订阅工具列表变更通知（同进程内多个订阅者均被调用）
func (m *Manager) OnToolsChanged(h ToolsChangedHandler) {
	if h == nil {
		return
	}
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	m.toolsChangedSubs = append(m.toolsChangedSubs, h)
}

// fireToolsChanged 同步派发给所有订阅者
func (m *Manager) fireToolsChanged(serverName string) {
	m.subsMu.RLock()
	subs := append([]ToolsChangedHandler(nil), m.toolsChangedSubs...)
	m.subsMu.RUnlock()
	for _, h := range subs {
		h(serverName)
	}
}

// ConnectAll 批量连接，单个失败不影响其它（返回失败 map）
func (m *Manager) ConnectAll(ctx context.Context, configs []*mcp.ServerConfig) map[string]error {
	errs := make(map[string]error)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, cfg := range configs {
		if !cfg.IsEnabled() {
			continue
		}
		wg.Add(1)
		go func(c *mcp.ServerConfig) {
			defer wg.Done()
			if _, err := m.Connect(ctx, c); err != nil {
				mu.Lock()
				errs[c.Name] = err
				mu.Unlock()
			}
		}(cfg)
	}
	wg.Wait()
	return errs
}

// Get 获取已连接的 client
func (m *Manager) Get(name string) (*ClientImpl, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.clients[name]
	return c, ok
}

// All 返回所有 client（包括失败的）
func (m *Manager) All() []*ClientImpl {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*ClientImpl, 0, len(m.clients))
	for _, c := range m.clients {
		out = append(out, c)
	}
	return out
}

// Statuses 返回所有连接状态
func (m *Manager) Statuses() []ConnectionStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ConnectionStatus, 0, len(m.statuses))
	for _, s := range m.statuses {
		out = append(out, s)
	}
	return out
}

// Disconnect 断开指定服务器（停止重连）
func (m *Manager) Disconnect(name string) error {
	m.mu.Lock()
	c, ok := m.clients[name]
	delete(m.clients, name)
	delete(m.configs, name) // 重要：让 watchdog 看到 cfg 不存在 → 不再重连
	delete(m.statuses, name)
	m.mu.Unlock()
	if !ok {
		return nil
	}
	return c.Disconnect()
}

// Reconnect 重连指定服务器：保留缓存的 ServerConfig，先断开旧连接再重新建立。
//
// 行为：
//   - 配置不存在 → 返回错误（区别于 Disconnect 的幂等"无操作"）
//   - 已断开（无活跃 client）→ 直接 Connect
//   - 已连接 → 先 Disconnect 旧 client，再用同一份配置 Connect
//
// 与 src `useMcpReconnect()` 行为对齐。
func (m *Manager) Reconnect(ctx context.Context, name string) error {
	m.mu.RLock()
	cfg, hasCfg := m.configs[name]
	c, hasClient := m.clients[name]
	m.mu.RUnlock()
	if !hasCfg {
		return fmt.Errorf("mcp server %q not configured", name)
	}
	// 复制 cfg 防御：上层在 Disconnect 流程会清掉 configs[name]
	cfgCopy := *cfg
	if hasClient {
		// 直接调用 client.Disconnect 而不走 Manager.Disconnect，
		// 避免后者把 configs[name] 一并删除导致后续 Connect 没了配置。
		_ = c.Disconnect()
		m.mu.Lock()
		delete(m.clients, name)
		delete(m.statuses, name)
		m.mu.Unlock()
	}
	_, err := m.Connect(ctx, &cfgCopy)
	return err
}

// DisconnectAll 断开所有服务器
func (m *Manager) DisconnectAll() {
	m.mu.Lock()
	clients := m.clients
	m.clients = make(map[string]*ClientImpl)
	m.configs = make(map[string]*mcp.ServerConfig)
	m.statuses = make(map[string]ConnectionStatus)
	m.mu.Unlock()
	for _, c := range clients {
		_ = c.Disconnect()
	}
}

func (m *Manager) setStatus(name string, s ConnectionStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statuses[name] = s
}

// --- 配置加载 ----------------------------------------------------------------

// SettingsFile MCP 配置文件结构（对齐 .mcp.json 与 settings.json 中 mcpServers 字段）
type SettingsFile struct {
	McpServers map[string]ServerConfigRaw `json:"mcpServers"`
}

// ServerConfigRaw 原始配置（type 字段可省略，省略时按 command 是否存在推断）
type ServerConfigRaw struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Enabled *bool             `json:"enabled,omitempty"`
}

// LoadConfigFile 从单个 JSON 文件读取 mcpServers 配置；文件不存在返回 nil, nil
func LoadConfigFile(path string) ([]*mcp.ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var sf SettingsFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return rawToConfigs(sf.McpServers, scopeFromPath(path)), nil
}

// LoadDefault 从默认位置加载并合并 MCP 配置
//
// 优先级（按加载顺序，后加载者覆盖前者）：
//
//	~/.claude/settings.json            (user 全局)
//	<projectRoot>/.mcp.json            (legacy 项目根；保持向后兼容)
//	<projectRoot>/.claude/settings.json (project 专用 settings)
//	<projectRoot>/.claude/.mcp.json    (项目级 MCP，主路径，覆盖 legacy)
//
// 同名按后加载者覆盖。
func LoadDefault(projectRoot string) ([]*mcp.ServerConfig, error) {
	var paths []string
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".claude", "settings.json"))
	}
	if projectRoot != "" {
		paths = append(paths,
			filepath.Join(projectRoot, ".mcp.json"),
			filepath.Join(projectRoot, ".claude", "settings.json"),
			filepath.Join(projectRoot, ".claude", ".mcp.json"),
		)
	}
	merged := make(map[string]*mcp.ServerConfig)
	for _, p := range paths {
		configs, err := LoadConfigFile(p)
		if err != nil {
			// 跳过单个错误，让其它配置仍可工作
			continue
		}
		for _, c := range configs {
			merged[c.Name] = c
		}
	}
	out := make([]*mcp.ServerConfig, 0, len(merged))
	for _, c := range merged {
		out = append(out, c)
	}
	return out, nil
}

// rawToConfigs 把 raw map 转为 ServerConfig 切片
func rawToConfigs(raw map[string]ServerConfigRaw, scope string) []*mcp.ServerConfig {
	if len(raw) == 0 {
		return nil
	}
	out := make([]*mcp.ServerConfig, 0, len(raw))
	for name, r := range raw {
		t := mcp.TransportType(r.Type)
		if t == "" {
			// 推断：有 command 则 stdio，有 url 且包含 sse 则 sse，否则 http
			if r.Command != "" {
				t = mcp.TransportStdio
			} else if r.URL != "" {
				t = mcp.TransportHTTP
			}
		}
		out = append(out, &mcp.ServerConfig{
			Name:          name,
			TransportType: t,
			Command:       r.Command,
			Args:          r.Args,
			Env:           r.Env,
			URL:           r.URL,
			Headers:       r.Headers,
			Scope:         scope,
			Enabled:       r.Enabled,
		})
	}
	return out
}

func scopeFromPath(path string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(dir)
	if base == ".claude" {
		parent := filepath.Dir(dir)
		if home, err := os.UserHomeDir(); err == nil && parent == home {
			return "user"
		}
		return "project"
	}
	if filepath.Base(path) == ".mcp.json" {
		return "project"
	}
	return "local"
}
