// Package hooks 应用层 session hook 实现。
//
// 对齐以下 TypeScript 源文件：
//   - src/hooks/useDirectConnect.ts        — DirectConnect 控制器
//   - src/hooks/useIdeConnectionStatus.ts  — IDE 连接状态追踪
//   - src/remote/SessionsWebSocket.ts      — WebSocket 重连逻辑
//   - src/utils/swarm/reconnection.ts      — Swarm 重连上下文初始化
package hooks

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/yaoice/goclaude/pkg/domain/hook"
)

// ─────────────────────────────────────────────────────────────────
// DirectConnect — 直接连接控制器
// ─────────────────────────────────────────────────────────────────

// DirectConnectConfig 直接连接配置
type DirectConnectConfig struct {
	WSURL     string
	SessionID string
	AuthToken string
	Enabled   bool
}

// DirectConnectCallbacks 直接连接回调
type DirectConnectCallbacks struct {
	// OnMessage 收到消息时的回调
	OnMessage func(message interface{})
	// OnConnected 连接建立时的回调
	OnConnected func()
	// OnDisconnected 连接断开时的回调（含是否为首次连接失败的判断）
	OnDisconnected func(wasConnected bool, reason string)
	// OnError 错误回调
	OnError func(err error)
	// OnReconnecting 重连进行中的回调
	OnReconnecting func()
	// OnLoadingChange loading 状态变化
	OnLoadingChange func(loading bool)
}

// DirectConnect 直接连接管理器
//
// 对齐 src/hooks/useDirectConnect.ts 的核心逻辑：
//   - 管理 DirectConnectSessionManager 的生命周期
//   - 处理 session end 消息
//   - 处理连接/断开/重连事件
//   - 连接失败时输出错误信息并优雅退出
type DirectConnect struct {
	config    *DirectConnectConfig
	callbacks DirectConnectCallbacks

	mu              sync.Mutex
	connected       bool
	hasReceivedInit bool
	cancel          context.CancelFunc
	logger          *slog.Logger
}

// NewDirectConnect 创建直接连接管理器
func NewDirectConnect(
	config *DirectConnectConfig,
	callbacks DirectConnectCallbacks,
	logger *slog.Logger,
) *DirectConnect {
	if logger == nil {
		logger = slog.Default()
	}
	return &DirectConnect{
		config:    config,
		callbacks: callbacks,
		logger:    logger,
	}
}

// IsRemoteMode 是否处于远程模式
func (dc *DirectConnect) IsRemoteMode() bool {
	return dc.config != nil && dc.config.Enabled
}

// Connect 建立连接
func (dc *DirectConnect) Connect(ctx context.Context) {
	if !dc.IsRemoteMode() {
		return
	}

	dc.mu.Lock()
	dc.connected = false
	dc.hasReceivedInit = false
	dc.mu.Unlock()

	dc.logger.Debug("[DirectConnect] Connecting", "url", dc.config.WSURL)

	ctx, dc.cancel = context.WithCancel(ctx)

	// 模拟连接成功
	// 在实际基础设施层实现中，这里会创建 WebSocket 连接
	dc.mu.Lock()
	dc.connected = true
	dc.mu.Unlock()

	if dc.callbacks.OnConnected != nil {
		dc.callbacks.OnConnected()
	}
}

// Disconnect 断开连接
func (dc *DirectConnect) Disconnect() {
	dc.logger.Debug("[DirectConnect] Disconnecting")

	dc.mu.Lock()
	wasConnected := dc.connected
	dc.connected = false
	if dc.cancel != nil {
		dc.cancel()
		dc.cancel = nil
	}
	dc.mu.Unlock()

	if wasConnected && dc.callbacks.OnDisconnected != nil {
		dc.callbacks.OnDisconnected(true, "manual disconnect")
	}
}

// SendMessage 发送消息
func (dc *DirectConnect) SendMessage(content interface{}) error {
	dc.mu.Lock()
	connected := dc.connected
	dc.mu.Unlock()

	if !connected {
		return &DirectConnectError{Reason: "not connected"}
	}

	if dc.callbacks.OnLoadingChange != nil {
		dc.callbacks.OnLoadingChange(true)
	}
	return nil
}

// CancelRequest 取消当前请求（发送中断信号）
func (dc *DirectConnect) CancelRequest() {
	dc.logger.Debug("[DirectConnect] Sending interrupt")
	if dc.callbacks.OnLoadingChange != nil {
		dc.callbacks.OnLoadingChange(false)
	}
}

// HandleConnectionFailure 处理连接失败
// 对齐 TS: process.stderr.write / gracefulShutdown
func (dc *DirectConnect) HandleConnectionFailure(reason string) {
	dc.mu.Lock()
	wasConnected := dc.connected
	dc.connected = false
	dc.mu.Unlock()

	if !wasConnected {
		dc.logger.Error("Failed to connect to server",
			"url", dc.config.WSURL,
			"reason", reason,
		)
	} else {
		dc.logger.Error("Server disconnected", "reason", reason)
	}

	if dc.callbacks.OnDisconnected != nil {
		dc.callbacks.OnDisconnected(wasConnected, reason)
	}
}

// ─────────────────────────────────────────────────────────────────
// DirectConnectError
// ─────────────────────────────────────────────────────────────────

// DirectConnectError 直接连接错误
type DirectConnectError struct {
	Reason string
}

func (e *DirectConnectError) Error() string {
	return "direct connect error: " + e.Reason
}

// ─────────────────────────────────────────────────────────────────
// ReconnectManager — 重连管理器
// ─────────────────────────────────────────────────────────────────
//
// 对齐 src/remote/SessionsWebSocket.ts 的重连逻辑：
//   - 线性退避重连（非指数，对齐 TS 的固定 RECONNECT_DELAY_MS）
//   - 最大重连次数限制（MAX_RECONNECT_ATTEMPTS = 5）
//   - 永久关闭码检测（4003 unauthorized → 立即停止重连）
//   - 4001 session not found 特殊处理（限次重试，用于 compaction 窗口期）
//   - Ping/Pong 心跳机制
//   - 强制重连（重置计数器）

// CloseCode WebSocket 关闭码常量 — 对齐 SessionsWebSocket.ts
const (
	CloseCodeSessionNotFound = 4001
	CloseCodeUnauthorized    = 4003
)

// permanentCloseCodes 永久关闭码集合 — 对齐 PERMANENT_CLOSE_CODES
var permanentCloseCodes = map[int]bool{
	CloseCodeUnauthorized: true,
}

// ReconnectManagerCallbacks 重连管理器回调
type ReconnectManagerCallbacks struct {
	OnConnected    func()
	OnDisconnected func()
	OnReconnecting func(event hook.ReconnectEvent)
	OnError        func(err error)
}

// ReconnectManager 重连管理器
//
// 对齐 src/remote/SessionsWebSocket.ts 的重连状态机。
// 使用线性退避（对齐 TS 固定延迟），而非指数退避。
type ReconnectManager struct {
	config    hook.ReconnectConfig
	callbacks ReconnectManagerCallbacks
	logger    *slog.Logger

	mu                     sync.Mutex
	state                  hook.ReconnectState
	reconnectAttempts      int
	sessionNotFoundRetries int
	connectFn              func() error
	disconnectFn           func()
	pingTicker             *time.Ticker
	reconnectTimer         *time.Timer
}

// NewReconnectManager 创建重连管理器
func NewReconnectManager(
	config hook.ReconnectConfig,
	callbacks ReconnectManagerCallbacks,
	logger *slog.Logger,
) *ReconnectManager {
	if config.MaxAttempts <= 0 {
		config.MaxAttempts = 5
	}
	if config.InitialDelay <= 0 {
		config.InitialDelay = 2000 * time.Millisecond
	}
	if config.PingInterval <= 0 {
		config.PingInterval = 30000 * time.Millisecond
	}
	if config.MaxSessionNotFoundRetries <= 0 {
		config.MaxSessionNotFoundRetries = 3
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ReconnectManager{
		config:    config,
		callbacks: callbacks,
		logger:    logger,
		state:     hook.ReconnectStateIdle,
	}
}

// SetConnectFunc 设置连接函数（在调用 Connect 前必须设置）
func (r *ReconnectManager) SetConnectFunc(fn func() error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.connectFn = fn
}

// SetDisconnectFunc 设置断开函数
func (r *ReconnectManager) SetDisconnectFunc(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.disconnectFn = fn
}

// Connect 开始连接
func (r *ReconnectManager) Connect() error {
	r.mu.Lock()
	if r.state == hook.ReconnectStateReconnecting {
		r.logger.Debug("[ReconnectManager] Already connecting")
		r.mu.Unlock()
		return nil
	}
	r.state = hook.ReconnectStateReconnecting
	connectFn := r.connectFn
	r.mu.Unlock()

	if connectFn == nil {
		return &ReconnectError{Reason: "connect function not set"}
	}

	err := connectFn()
	if err != nil {
		r.HandleConnectionError(err)
		return err
	}

	r.mu.Lock()
	r.state = hook.ReconnectStateConnected
	r.reconnectAttempts = 0
	r.sessionNotFoundRetries = 0
	r.mu.Unlock()

	r.startPing()
	if r.callbacks.OnConnected != nil {
		r.callbacks.OnConnected()
	}
	return nil
}

// HandleClose 处理连接关闭
// 对齐 SessionsWebSocket.handleClose 的逻辑。
func (r *ReconnectManager) HandleClose(closeCode int, reason string) {
	r.stopPing()
	r.cancelReconnectTimer()

	r.mu.Lock()
	previousState := r.state
	r.state = hook.ReconnectStateClosed
	r.mu.Unlock()

	// 永久关闭码 → 停止重连
	if permanentCloseCodes[closeCode] {
		r.logger.Debug("[ReconnectManager] Permanent close code, not reconnecting",
			"code", closeCode,
		)
		if r.callbacks.OnDisconnected != nil {
			r.callbacks.OnDisconnected()
		}
		return
	}

	// 4001 session not found → 限次重试（对齐 TS 的 compaction 窗口期处理）
	if closeCode == CloseCodeSessionNotFound {
		r.mu.Lock()
		r.sessionNotFoundRetries++
		retries := r.sessionNotFoundRetries
		maxRetries := r.config.MaxSessionNotFoundRetries
		r.mu.Unlock()

		if retries > maxRetries {
			r.logger.Debug("[ReconnectManager] 4001 retry budget exhausted",
				"retries", retries,
				"max", maxRetries,
			)
			if r.callbacks.OnDisconnected != nil {
				r.callbacks.OnDisconnected()
			}
			return
		}

		delay := r.config.InitialDelay * time.Duration(retries)
		r.scheduleReconnect(delay, reason, retries)
		return
	}

	// 通用重连逻辑（仅当之前处于 connected 状态时尝试）
	if previousState == hook.ReconnectStateConnected {
		r.mu.Lock()
		r.reconnectAttempts++
		attempt := r.reconnectAttempts
		r.mu.Unlock()

		if attempt <= r.config.MaxAttempts {
			r.scheduleReconnect(r.config.InitialDelay, reason, attempt)
			return
		}
	}

	r.logger.Debug("[ReconnectManager] Not reconnecting")
	if r.callbacks.OnDisconnected != nil {
		r.callbacks.OnDisconnected()
	}
}

// HandleConnectionError 处理连接错误
func (r *ReconnectManager) HandleConnectionError(err error) {
	r.logger.Error("[ReconnectManager] Connection error", "error", err)
	if r.callbacks.OnError != nil {
		r.callbacks.OnError(err)
	}
}

// Reconnect 强制重连（重置所有计数器）
// 对齐 SessionsWebSocket.reconnect()。
func (r *ReconnectManager) Reconnect() {
	r.logger.Debug("[ReconnectManager] Force reconnecting")

	r.mu.Lock()
	r.reconnectAttempts = 0
	r.sessionNotFoundRetries = 0
	disconnectFn := r.disconnectFn
	r.mu.Unlock()

	r.stopPing()
	r.cancelReconnectTimer()

	if disconnectFn != nil {
		disconnectFn()
	}

	// 短暂延迟后重连（对齐 TS 的 500ms 延迟）
	r.mu.Lock()
	r.reconnectTimer = time.AfterFunc(500*time.Millisecond, func() {
		if err := r.Connect(); err != nil {
			r.HandleConnectionError(err)
		}
	})
	r.mu.Unlock()
}

// State 返回当前重连状态
func (r *ReconnectManager) State() hook.ReconnectState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

// IsConnected 是否已连接
func (r *ReconnectManager) IsConnected() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state == hook.ReconnectStateConnected
}

// scheduleReconnect 调度重连
func (r *ReconnectManager) scheduleReconnect(delay time.Duration, reason string, attempt int) {
	event := hook.ReconnectEvent{
		Attempt:    attempt,
		MaxAttempt: r.config.MaxAttempts,
		Delay:      delay,
		Reason:     reason,
	}

	if r.callbacks.OnReconnecting != nil {
		r.callbacks.OnReconnecting(event)
	}

	r.logger.Debug("[ReconnectManager] Scheduling reconnect",
		"attempt", attempt,
		"max", r.config.MaxAttempts,
		"delay", delay,
	)

	r.mu.Lock()
	r.state = hook.ReconnectStateReconnecting
	r.reconnectTimer = time.AfterFunc(delay, func() {
		if err := r.Connect(); err != nil {
			r.HandleConnectionError(err)
		}
	})
	r.mu.Unlock()
}

// startPing 启动心跳
func (r *ReconnectManager) startPing() {
	r.stopPing()
	r.pingTicker = time.NewTicker(r.config.PingInterval)
	go func() {
		for range r.pingTicker.C {
			r.mu.Lock()
			connected := r.state == hook.ReconnectStateConnected
			r.mu.Unlock()
			if !connected {
				return
			}
			// Ping logic would go here in a real WebSocket implementation
		}
	}()
}

// stopPing 停止心跳
func (r *ReconnectManager) stopPing() {
	if r.pingTicker != nil {
		r.pingTicker.Stop()
		r.pingTicker = nil
	}
}

// cancelReconnectTimer 取消重连定时器
func (r *ReconnectManager) cancelReconnectTimer() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reconnectTimer != nil {
		r.reconnectTimer.Stop()
		r.reconnectTimer = nil
	}
}

// Close 完全关闭管理器
func (r *ReconnectManager) Close() {
	r.logger.Debug("[ReconnectManager] Closing")
	r.stopPing()
	r.cancelReconnectTimer()

	r.mu.Lock()
	r.state = hook.ReconnectStateClosed
	disconnectFn := r.disconnectFn
	r.mu.Unlock()

	if disconnectFn != nil {
		disconnectFn()
	}
}

// ─────────────────────────────────────────────────────────────────
// ReconnectError
// ─────────────────────────────────────────────────────────────────

// ReconnectError 重连错误
type ReconnectError struct {
	Reason string
}

func (e *ReconnectError) Error() string {
	return "reconnect error: " + e.Reason
}

// ─────────────────────────────────────────────────────────────────
// ExponentialBackoff — 指数退避辅助工具
// ─────────────────────────────────────────────────────────────────

// ExponentialBackoff 指数退避计算器
type ExponentialBackoff struct {
	BaseDelay time.Duration
	MaxDelay  time.Duration
	Factor    float64
}

// NewExponentialBackoff 创建指数退避计算器
func NewExponentialBackoff(baseDelay, maxDelay time.Duration) *ExponentialBackoff {
	return &ExponentialBackoff{
		BaseDelay: baseDelay,
		MaxDelay:  maxDelay,
		Factor:    2.0,
	}
}

// Delay 计算第 attempt 次重试的延迟
func (b *ExponentialBackoff) Delay(attempt int) time.Duration {
	if attempt <= 0 {
		return b.BaseDelay
	}
	delay := time.Duration(float64(b.BaseDelay) * math.Pow(b.Factor, float64(attempt-1)))
	if delay > b.MaxDelay {
		return b.MaxDelay
	}
	return delay
}

// ─────────────────────────────────────────────────────────────────
// IDEConnectionResolver — IDE 连接状态解析器
// ─────────────────────────────────────────────────────────────────
//
// 对齐 src/hooks/useIdeConnectionStatus.ts 的逻辑：
//   在 MCP client 列表中查找名为 "ide" 的 client，根据其连接类型返回状态。

// IDEConnectionResolver IDE 连接状态解析器
type IDEConnectionResolver struct {
	tracker *hook.IDEStatusTracker
}

// NewIDEConnectionResolver 创建 IDE 连接解析器
func NewIDEConnectionResolver(tracker *hook.IDEStatusTracker) *IDEConnectionResolver {
	return &IDEConnectionResolver{tracker: tracker}
}

// ResolveStatus 根据 MCP client 列表解析 IDE 连接状态
//
// 对齐 TS 逻辑：
//
//	const ideClient = mcpClients?.find(client => client.name === 'ide')
//	if (!ideClient) return { status: null, ideName: null }
//	const ideName = (config.type === 'sse-ide' || config.type === 'ws-ide') ? config.ideName : null
//	if (ideClient.type === 'connected') return { status: 'connected', ideName }
//	if (ideClient.type === 'pending') return { status: 'pending', ideName }
//	return { status: 'disconnected', ideName }
func (r *IDEConnectionResolver) ResolveStatus(clients []MCPClientInfo) hook.IDEConnectionInfo {
	for _, client := range clients {
		if client.Name != "ide" {
			continue
		}
		ideName := ""
		if client.ConfigType == "sse-ide" || client.ConfigType == "ws-ide" {
			ideName = client.IDEName
		}
		switch client.ConnectionType {
		case "connected":
			return hook.IDEConnectionInfo{Status: hook.IDEStatusConnected, IDEName: ideName}
		case "pending":
			return hook.IDEConnectionInfo{Status: hook.IDEStatusPending, IDEName: ideName}
		default:
			return hook.IDEConnectionInfo{Status: hook.IDEStatusDisconnected, IDEName: ideName}
		}
	}
	// 未找到 ide client → null
	return hook.IDEConnectionInfo{Status: hook.IDEStatusNone, IDEName: ""}
}

// MCPClientInfo MCP client 信息（用于 IDE 连接状态解析）
type MCPClientInfo struct {
	Name           string
	ConnectionType string // "connected" | "pending" | "disconnected"
	ConfigType     string // "sse-ide" | "ws-ide" | 其他
	IDEName        string
}

// ─────────────────────────────────────────────────────────────────
// SessionReconnection — Swarm 重连上下文初始化
// ─────────────────────────────────────────────────────────────────
//
// 对齐 src/utils/swarm/reconnection.ts 的核心逻辑：
//   - 从 teamName/agentName 初始化 teammate context
//   - 从持久化 session 恢复 teammate context

// SessionReconnection 会话重连上下文初始化器
type SessionReconnection struct {
	logger *slog.Logger
}

// NewSessionReconnection 创建会话重连初始化器
func NewSessionReconnection(logger *slog.Logger) *SessionReconnection {
	if logger == nil {
		logger = slog.Default()
	}
	return &SessionReconnection{logger: logger}
}

// TeammateContext teammate 上下文
type TeammateContext struct {
	TeamName      string
	TeamFilePath  string
	LeadAgentID   string
	SelfAgentID   string
	SelfAgentName string
	IsLeader      bool
}

// TeamFile 团队文件信息
type TeamFile struct {
	LeadAgentID string
	Members     []TeamMember
}

// TeamMember 团队成员
type TeamMember struct {
	Name    string
	AgentID string
}

// TeammateContextReader teammate 上下文读取接口
type TeammateContextReader interface {
	// GetDynamicContext 获取动态 teammate 上下文（对齐 getDynamicTeamContext）
	GetDynamicContext() *TeammateContext
	// ReadTeamFile 读取团队文件（对齐 readTeamFile）
	ReadTeamFile(teamName string) (*TeamFile, error)
	// GetTeamFilePath 获取团队文件路径（对齐 getTeamFilePath）
	GetTeamFilePath(teamName string) string
}

// ComputeInitialTeamContext 计算初始 team context
//
// 对齐 src/utils/swarm/reconnection.ts:computeInitialTeamContext
func (s *SessionReconnection) ComputeInitialTeamContext(
	reader TeammateContextReader,
) *TeammateContext {
	ctx := reader.GetDynamicContext()
	if ctx == nil || ctx.TeamName == "" || ctx.SelfAgentName == "" {
		s.logger.Debug("[SessionReconnection] No teammate context set (not a teammate)")
		return nil
	}

	teamFile, err := reader.ReadTeamFile(ctx.TeamName)
	if err != nil || teamFile == nil {
		s.logger.Error("[SessionReconnection] Could not read team file",
			"team", ctx.TeamName,
			"error", err,
		)
		return nil
	}

	teamFilePath := reader.GetTeamFilePath(ctx.TeamName)
	isLeader := ctx.SelfAgentID == ""

	s.logger.Debug("[SessionReconnection] Computed initial team context",
		"role", map[bool]string{true: "leader", false: "teammate"}[isLeader],
		"team", ctx.TeamName,
	)

	return &TeammateContext{
		TeamName:      ctx.TeamName,
		TeamFilePath:  teamFilePath,
		LeadAgentID:   teamFile.LeadAgentID,
		SelfAgentID:   ctx.SelfAgentID,
		SelfAgentName: ctx.SelfAgentName,
		IsLeader:      isLeader,
	}
}

// InitializeTeammateContextFromSession 从恢复的 session 初始化 teammate context
//
// 对齐 src/utils/swarm/reconnection.ts:initializeTeammateContextFromSession
func (s *SessionReconnection) InitializeTeammateContextFromSession(
	reader TeammateContextReader,
	teamName string,
	agentName string,
) *TeammateContext {
	teamFile, err := reader.ReadTeamFile(teamName)
	if err != nil || teamFile == nil {
		s.logger.Error("[SessionReconnection] Could not read team file for session restore",
			"team", teamName,
			"agent", agentName,
			"error", err,
		)
		return nil
	}

	// 查找成员的 agentId
	var agentID string
	for _, member := range teamFile.Members {
		if member.Name == agentName {
			agentID = member.AgentID
			break
		}
	}
	if agentID == "" {
		s.logger.Debug("[SessionReconnection] Member not found in team - may have been removed",
			"team", teamName,
			"agent", agentName,
		)
	}

	teamFilePath := reader.GetTeamFilePath(teamName)

	s.logger.Debug("[SessionReconnection] Initialized agent context from session",
		"team", teamName,
		"agent", agentName,
	)

	return &TeammateContext{
		TeamName:      teamName,
		TeamFilePath:  teamFilePath,
		LeadAgentID:   teamFile.LeadAgentID,
		SelfAgentID:   agentID,
		SelfAgentName: agentName,
		IsLeader:      false,
	}
}
