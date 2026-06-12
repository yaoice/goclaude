// Package hooks — IDE integration hooks
//
// 对齐 src/hooks/ 中的 IDE 集成相关 hooks：
//   - useIDEIntegration  → IDEIntegration
//   - useIdeSelection    → IDEIntegration (selections 字段)
//   - useIdeLogging      → (由 ide_hooks 内部日志追踪)
//   - useIdeConnectionStatus → IDEConnectionStatus
package hooks

import (
	"sync"

	"github.com/yaoice/goclaude/pkg/domain/hook"
)

// IDEIntegration IDE 集成管理器
// Equivalent of useIDEIntegration, useIdeSelection, useIdeLogging
type IDEIntegration struct {
	mu                sync.RWMutex
	connected         bool
	selections        map[string]IDESelection // filePath -> selection
	onSelectionChange func(string, IDESelection)
	onConnect         func()
	onDisconnect      func()
}

// IDESelection IDE 选中内容
type IDESelection struct {
	FilePath string
	Start    int
	End      int
	Text     string
}

// NewIDEIntegration 创建 IDE 集成管理器
func NewIDEIntegration() *IDEIntegration {
	return &IDEIntegration{
		selections: make(map[string]IDESelection),
	}
}

// SetConnected 设置连接状态
func (i *IDEIntegration) SetConnected(connected bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.connected == connected {
		return
	}
	i.connected = connected
	if connected && i.onConnect != nil {
		i.onConnect()
	}
	if !connected && i.onDisconnect != nil {
		i.onDisconnect()
	}
}

// IsConnected 是否已连接
func (i *IDEIntegration) IsConnected() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.connected
}

// UpdateSelection 更新 IDE 选中内容
func (i *IDEIntegration) UpdateSelection(sel IDESelection) {
	i.mu.Lock()
	i.selections[sel.FilePath] = sel
	cb := i.onSelectionChange
	i.mu.Unlock()
	if cb != nil {
		cb(sel.FilePath, sel)
	}
}

// GetSelection 获取文件选中内容
func (i *IDEIntegration) GetSelection(filePath string) (IDESelection, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	sel, ok := i.selections[filePath]
	return sel, ok
}

// ClearSelection 清空选中
func (i *IDEIntegration) ClearSelection(filePath string) {
	i.mu.Lock()
	delete(i.selections, filePath)
	i.mu.Unlock()
}

// OnConnect 注册连接回调
func (i *IDEIntegration) OnConnect(fn func()) { i.onConnect = fn }

// OnDisconnect 注册断连回调
func (i *IDEIntegration) OnDisconnect(fn func()) { i.onDisconnect = fn }

// OnSelectionChange 注册选中变更回调
func (i *IDEIntegration) OnSelectionChange(fn func(string, IDESelection)) { i.onSelectionChange = fn }

// IDEConnectionStatus IDE 连接状态追踪器
// Equivalent of useIdeConnectionStatus
type IDEConnectionStatus struct {
	tracker *hook.IDEStatusTracker
}

// NewIDEConnectionStatus 创建 IDE 连接状态追踪器
func NewIDEConnectionStatus() *IDEConnectionStatus {
	return &IDEConnectionStatus{tracker: hook.NewIDEStatusTracker()}
}

// Tracker 返回底层 IDEStatusTracker
func (s *IDEConnectionStatus) Tracker() *hook.IDEStatusTracker { return s.tracker }

// SetConnected 设置已连接
func (s *IDEConnectionStatus) SetConnected() { s.tracker.SetStatus(hook.ConnectionConnected, "") }

// SetConnecting 设置连接中
func (s *IDEConnectionStatus) SetConnecting() { s.tracker.SetStatus(hook.ConnectionConnecting, "") }

// SetDisconnected 设置已断开
func (s *IDEConnectionStatus) SetDisconnected() {
	s.tracker.SetStatus(hook.ConnectionDisconnected, "")
}

// SetError 设置错误状态
func (s *IDEConnectionStatus) SetError() { s.tracker.SetStatus(hook.ConnectionError, "") }
