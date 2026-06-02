package mcpinfra

import (
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/anthropics/goclaude/internal/domain/mcp"
)

// 模拟 readLoop 内部派发：直接构造 ClientImpl 并调用 dispatchNotification
func TestClient_NotificationDispatch(t *testing.T) {
	c := NewClient(&mcp.ServerConfig{Name: "x"})
	var hitMethod atomic.Value
	var hitWildcard atomic.Int32

	c.OnNotification("notifications/tools/list_changed", func(method string, _ json.RawMessage) {
		hitMethod.Store(method)
	})
	c.OnNotification("", func(_ string, _ json.RawMessage) {
		hitWildcard.Add(1)
	})

	c.dispatchNotification("notifications/tools/list_changed", nil)
	c.dispatchNotification("notifications/resources/updated", nil)

	if m, _ := hitMethod.Load().(string); m != "notifications/tools/list_changed" {
		t.Errorf("exact handler not called, got %v", hitMethod.Load())
	}
	if hitWildcard.Load() != 2 {
		t.Errorf("wildcard should fire 2 times, got %d", hitWildcard.Load())
	}
}

func TestManager_ToolsChangedFanOut(t *testing.T) {
	m := NewManager()
	var count atomic.Int32
	var seenName atomic.Value
	m.OnToolsChanged(func(name string) {
		count.Add(1)
		seenName.Store(name)
	})
	m.fireToolsChanged("github")

	if count.Load() != 1 {
		t.Errorf("expected 1 fire, got %d", count.Load())
	}
	if got, _ := seenName.Load().(string); got != "github" {
		t.Errorf("got %v", got)
	}
}
