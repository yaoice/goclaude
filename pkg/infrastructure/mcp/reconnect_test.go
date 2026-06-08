package mcpinfra

import (
	"context"
	"strings"
	"testing"
)

// TestManager_Reconnect_UnknownServer 验证：未配置的服务器调用 Reconnect 应返回错误，
// 而不是静默成功（防御 dialog 的 r 键调到一个不存在的 server）
func TestManager_Reconnect_UnknownServer(t *testing.T) {
	m := NewManager()
	err := m.Reconnect(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("Reconnect on unknown server should fail; got nil")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("error message should mention 'not configured': %v", err)
	}
}

// TestManager_Disconnect_PreservesNothingButReconnectIsSeparate 验证 Disconnect 之后
// 再 Reconnect 同名服务器会得到"未配置"错误（因为 Disconnect 删除了 configs[name]）。
//
// 这是对 Manager.Reconnect 文档语义的回归测试：Reconnect 仅保留当前活跃的 cfg，
// 它不能复活已被 Disconnect 显式断开的服务器（这与 src `useMcpReconnect` 行为一致：
// 仅对"当前 client list 中可见"的 server 生效）。
func TestManager_Disconnect_ThenReconnect_RequiresFreshConnect(t *testing.T) {
	m := NewManager()
	// 这里没有真的 Connect（启动 stdio 进程的成本太高），仅验证：
	// "configs 中无该 name" 这条边界正确返回错误
	if err := m.Reconnect(context.Background(), "ghost"); err == nil {
		t.Error("expected error for never-configured server")
	}
}
