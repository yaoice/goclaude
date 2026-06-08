package teamfs

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/goclaude/pkg/domain/team"
)

func newTestLayout(t *testing.T) Layout {
	t.Helper()
	return Layout{HomeDir: t.TempDir()}
}

// MarkReadByIDs：未命中 ID 不报错，命中数返回正确。
func TestMailbox_MarkReadByIDs_PartialMatch(t *testing.T) {
	t.Parallel()
	l := newTestLayout(t)
	if err := os.MkdirAll(l.InboxesDir("X"), 0o755); err != nil {
		t.Fatal(err)
	}
	mb := NewMailbox(l)
	for i := 0; i < 3; i++ {
		if err := mb.Append("X", "a", team.NewTextMessage("leader", "s", "hi")); err != nil {
			t.Fatal(err)
		}
	}
	all, _ := mb.Read("X", "a")
	n, err := mb.MarkReadByIDs("X", "a", []string{all[0].ID, "non-existent"})
	if err != nil || n != 1 {
		t.Errorf("partial match: n=%d err=%v, want n=1", n, err)
	}
}

// ReadSince：游标过滤后续消息（边界严格使用 > sinceMs）。
func TestMailbox_ReadSince(t *testing.T) {
	t.Parallel()
	l := newTestLayout(t)
	if err := os.MkdirAll(l.InboxesDir("X"), 0o755); err != nil {
		t.Fatal(err)
	}
	mb := NewMailbox(l)
	first := team.NewTextMessage("leader", "s", "first")
	if err := mb.Append("X", "a", first); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	second := team.NewTextMessage("leader", "s", "second")
	if err := mb.Append("X", "a", second); err != nil {
		t.Fatal(err)
	}
	got, err := mb.ReadSince("X", "a", first.Timestamp)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "second" {
		t.Errorf("ReadSince(first.ts) = %+v, want only 'second'", got)
	}
	all, _ := mb.ReadSince("X", "a", 0)
	if len(all) != 2 {
		t.Errorf("ReadSince(0) should return all, got %d", len(all))
	}
}

// WaitForUnread ctx 取消：返回 ctx.Err（非 ErrWaitTimeout）。
func TestMailbox_WaitForUnread_CtxCancel(t *testing.T) {
	t.Parallel()
	l := newTestLayout(t)
	if err := os.MkdirAll(l.InboxesDir("X"), 0o755); err != nil {
		t.Fatal(err)
	}
	mb := NewMailbox(l)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := mb.WaitForUnread(ctx, "X", "a", 0)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("got %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitForUnread did not return after ctx cancel")
	}
}

// 并发 Append 同一 inbox：所有消息无丢失。
func TestMailbox_ConcurrentAppend_NoLoss_Advanced(t *testing.T) {
	t.Parallel()
	l := newTestLayout(t)
	if err := os.MkdirAll(l.InboxesDir("X"), 0o755); err != nil {
		t.Fatal(err)
	}
	mb := NewMailbox(l)
	const N = 50
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = mb.Append("X", "a",
				team.NewTextMessage("leader", "s", "m"))
		}(i)
	}
	wg.Wait()
	all, _ := mb.Read("X", "a")
	if len(all) != N {
		t.Errorf("got %d messages, want %d (lost on concurrent append)", len(all), N)
	}
}

// 锁孤儿：手动写入 lock 文件后 Append 应在 timeout 内返回带诊断信息的错误。
func TestMailbox_LockTimeout_DiagInfo(t *testing.T) {
	t.Parallel()
	l := newTestLayout(t)
	if err := os.MkdirAll(l.InboxesDir("X"), 0o755); err != nil {
		t.Fatal(err)
	}
	mb := &Mailbox{Layout: l, LockTimeout: 200 * time.Millisecond}
	// 模拟孤儿 lock
	lockPath := l.inboxLockPath("X", "a")
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(lockPath)

	err := mb.Append("X", "a", team.NewTextMessage("l", "s", "x"))
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") || !strings.Contains(err.Error(), "orphan") {
		t.Errorf("expected diagnostic with 'timeout' & 'orphan', got %q", err.Error())
	}
}
