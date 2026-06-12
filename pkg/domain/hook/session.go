// Package hook domain models for session and connection hooks.
// Aligns with:
//   - src/hooks/useIdeConnectionStatus.ts (IDE connection status)
//   - src/remote/SessionsWebSocket.ts (reconnection logic)
//   - src/hooks/useDirectConnect.ts (direct connect management)
package hook

import (
	"sync"
	"time"
)

// ConnectionStatus 连接状态
type ConnectionStatus string

const (
	ConnectionDisconnected ConnectionStatus = "disconnected"
	ConnectionConnecting   ConnectionStatus = "connecting"
	ConnectionConnected    ConnectionStatus = "connected"
	ConnectionError        ConnectionStatus = "error"
)

// SessionContext 会话上下文
type SessionContext struct {
	SessionID string
	StartedAt time.Time
	IsDirect  bool
}

// IDEConnectionInfo IDE 连接信息
type IDEConnectionInfo struct {
	Status  IDEStatus
	IDEName string
}

// IDEStatus IDE 连接状态 — 对齐 src/hooks/useIdeConnectionStatus.ts
type IDEStatus string

const (
	IDEStatusConnected    IDEStatus = "connected"
	IDEStatusDisconnected IDEStatus = "disconnected"
	IDEStatusPending      IDEStatus = "pending"
	IDEStatusNone         IDEStatus = "" // null equivalent
)

// IDEStatusTracker IDE 连接状态追踪器
// 对齐 src/hooks/useIdeConnectionStatus.ts 的行为：
//   - 查找名为 "ide" 的 MCP client
//   - 根据 client.type 返回 connected/pending/disconnected
type IDEStatusTracker struct {
	mu       sync.RWMutex
	status   ConnectionStatus
	ideName  string
	onChange func(ConnectionStatus)
}

// NewIDEStatusTracker 创建 IDE 连接状态追踪器
func NewIDEStatusTracker() *IDEStatusTracker {
	return &IDEStatusTracker{status: ConnectionDisconnected}
}

// Status 返回当前连接状态
func (t *IDEStatusTracker) Status() ConnectionStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.status
}

// IDEName 返回 IDE 名称（如果可用）
func (t *IDEStatusTracker) IDEName() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.ideName
}

// SetStatus 设置连接状态（仅在变化时触发回调）
func (t *IDEStatusTracker) SetStatus(s ConnectionStatus, ideName string) {
	t.mu.Lock()
	changed := t.status != s || t.ideName != ideName
	t.status = s
	t.ideName = ideName
	cb := t.onChange
	t.mu.Unlock()
	if changed && cb != nil {
		cb(s)
	}
}

// OnChange 注册状态变更回调
func (t *IDEStatusTracker) OnChange(fn func(ConnectionStatus)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onChange = fn
}

// ReconnectConfig 重连配置 — 对齐 SessionsWebSocket.ts 的常量
type ReconnectConfig struct {
	// InitialDelay 首次重连延迟（默认 2000ms）
	InitialDelay time.Duration
	// MaxAttempts 最大重连次数（默认 5）
	MaxAttempts int
	// MaxBackoffDelay 最大退避延迟
	MaxBackoffDelay time.Duration
	// PingInterval ping 间隔（默认 30000ms）
	PingInterval time.Duration
	// MaxSessionNotFoundRetries 4001 特殊重试次数（默认 3）
	MaxSessionNotFoundRetries int
}

// DefaultReconnectConfig 返回与 SessionsWebSocket.ts 对齐的默认配置
func DefaultReconnectConfig() ReconnectConfig {
	return ReconnectConfig{
		InitialDelay:              2000 * time.Millisecond,
		MaxAttempts:               5,
		MaxBackoffDelay:           30000 * time.Millisecond,
		PingInterval:              30000 * time.Millisecond,
		MaxSessionNotFoundRetries: 3,
	}
}

// ReconnectEvent 重连事件
type ReconnectEvent struct {
	Attempt    int
	MaxAttempt int
	Delay      time.Duration
	Reason     string
}

// ReconnectState 重连状态
type ReconnectState string

const (
	ReconnectStateIdle         ReconnectState = "idle"
	ReconnectStateConnected    ReconnectState = "connected"
	ReconnectStateReconnecting ReconnectState = "reconnecting"
	ReconnectStateFailed       ReconnectState = "failed"
	ReconnectStateClosed       ReconnectState = "closed"
)
