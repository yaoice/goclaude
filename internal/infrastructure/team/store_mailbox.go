package teamfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/anthropics/goclaude/internal/domain/team"
)

// 本文件聚合 Mailbox（单个 agent 的 inbox 读写）：未读/追加/标记已读/等待未读。
// 从 store.go 拆出以提升可读性；逻辑保持不变。

// Mailbox 负责单个 agent 的 inbox 文件读写。
type Mailbox struct {
	Layout Layout
	// LockTimeout 抢锁的最长等待时间。0 走默认（5s）。
	LockTimeout time.Duration
}

// NewMailbox 默认 5s 锁等待。
func NewMailbox(l Layout) *Mailbox {
	return &Mailbox{Layout: l, LockTimeout: 5 * time.Second}
}

// Read 读取整个 inbox（含已读 + 未读）。
//
// 文件不存在时返回空切片。
func (m *Mailbox) Read(teamName, agentName string) ([]team.Message, error) {
	return readInboxFile(m.Layout.InboxPath(teamName, agentName))
}

// ReadUnread 仅返回 read=false 的消息。
func (m *Mailbox) ReadUnread(teamName, agentName string) ([]team.Message, error) {
	all, err := m.Read(teamName, agentName)
	if err != nil {
		return nil, err
	}
	out := make([]team.Message, 0, len(all))
	for _, msg := range all {
		if !msg.Read {
			out = append(out, msg)
		}
	}
	return out, nil
}

// Append 在 inbox 末尾追加一条消息。
//
// 持有 file lock 期间：read 现状 → append → write。其它写入者会被阻塞或重试。
func (m *Mailbox) Append(teamName, agentName string, msg team.Message) error {
	if msg.Timestamp == 0 {
		msg.Timestamp = time.Now().UnixMilli()
	}
	inboxPath := m.Layout.InboxPath(teamName, agentName)
	if err := os.MkdirAll(filepath.Dir(inboxPath), 0o755); err != nil {
		return err
	}
	release, err := acquireLock(m.Layout.inboxLockPath(teamName, agentName), m.lockTimeout())
	if err != nil {
		return err
	}
	defer release()

	msgs, err := readInboxFile(inboxPath)
	if err != nil {
		return err
	}
	msgs = append(msgs, msg)
	return writeInboxFile(inboxPath, msgs)
}

// MarkAllRead 把所有未读消息标记为已读，返回被修改的条数。
func (m *Mailbox) MarkAllRead(teamName, agentName string) (int, error) {
	inboxPath := m.Layout.InboxPath(teamName, agentName)
	release, err := acquireLock(m.Layout.inboxLockPath(teamName, agentName), m.lockTimeout())
	if err != nil {
		return 0, err
	}
	defer release()

	msgs, err := readInboxFile(inboxPath)
	if err != nil {
		return 0, err
	}
	n := 0
	for i := range msgs {
		if !msgs[i].Read {
			msgs[i].Read = true
			n++
		}
	}
	if n == 0 {
		return 0, nil
	}
	return n, writeInboxFile(inboxPath, msgs)
}

// DrainUnread 一次性读取并标记所有未读消息（典型的"轮询"消费模式）。
//
// 返回的消息按时间顺序；磁盘上对应消息的 read 字段已置为 true。
func (m *Mailbox) DrainUnread(teamName, agentName string) ([]team.Message, error) {
	inboxPath := m.Layout.InboxPath(teamName, agentName)
	release, err := acquireLock(m.Layout.inboxLockPath(teamName, agentName), m.lockTimeout())
	if err != nil {
		return nil, err
	}
	defer release()

	msgs, err := readInboxFile(inboxPath)
	if err != nil {
		return nil, err
	}
	unread := make([]team.Message, 0, len(msgs))
	changed := false
	for i := range msgs {
		if !msgs[i].Read {
			unread = append(unread, msgs[i])
			msgs[i].Read = true
			changed = true
		}
	}
	if !changed {
		return unread, nil
	}
	if err := writeInboxFile(inboxPath, msgs); err != nil {
		return nil, err
	}
	return unread, nil
}

// ReadSince 仅返回 timestamp > sinceMs 的消息（read 状态保持不变）。
//
// sinceMs <= 0 时退化为返回全部消息。用于"按游标分页拉取"的只读视图。
func (m *Mailbox) ReadSince(teamName, agentName string, sinceMs int64) ([]team.Message, error) {
	all, err := m.Read(teamName, agentName)
	if err != nil {
		return nil, err
	}
	if sinceMs <= 0 {
		return all, nil
	}
	out := make([]team.Message, 0, len(all))
	for _, msg := range all {
		if msg.Timestamp > sinceMs {
			out = append(out, msg)
		}
	}
	return out, nil
}

// MarkReadByIDs 按消息 ID 精确标记已读，返回实际命中的条数。
//
// 与 src `markMessageAsReadByIndex` 行为对应（ID 比 index 更稳定）。
// 找不到的 ID 静默忽略——业务侧通常用 drain/peek 也能完成同样的事。
func (m *Mailbox) MarkReadByIDs(teamName, agentName string, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id != "" {
			want[id] = struct{}{}
		}
	}
	if len(want) == 0 {
		return 0, nil
	}
	inboxPath := m.Layout.InboxPath(teamName, agentName)
	release, err := acquireLock(m.Layout.inboxLockPath(teamName, agentName), m.lockTimeout())
	if err != nil {
		return 0, err
	}
	defer release()
	msgs, err := readInboxFile(inboxPath)
	if err != nil {
		return 0, err
	}
	n := 0
	for i := range msgs {
		if msgs[i].Read {
			continue
		}
		if _, ok := want[msgs[i].ID]; !ok {
			continue
		}
		msgs[i].Read = true
		n++
	}
	if n == 0 {
		return 0, nil
	}
	return n, writeInboxFile(inboxPath, msgs)
}

// HasUnread 报告 inbox 中是否存在未读消息。
//
// 不持锁——脏读，用于轮询前的快速短路；调用方需用 DrainUnread / ReadUnread
// 做权威读取。
func (m *Mailbox) HasUnread(teamName, agentName string) (bool, error) {
	msgs, err := m.Read(teamName, agentName)
	if err != nil {
		return false, err
	}
	for _, msg := range msgs {
		if !msg.Read {
			return true, nil
		}
	}
	return false, nil
}

// WaitForUnread 阻塞等待该 agent 的 inbox 出现未读消息或 ctx 取消。
//
// 内部用 100ms 短轮询（poll interval）+ 文件 mtime 回退策略——避免引入
// fsnotify 依赖。命中后立即 DrainUnread 返回。
//
//   - timeout <= 0 时：仅受 ctx 控制（无超时上限）。
//   - 命中未读但 drain 失败时返回 (nil, err)。
//   - 超时 / 取消时返回 (nil, ctx.Err()) 或 ErrWaitTimeout。
func (m *Mailbox) WaitForUnread(ctx context.Context, teamName, agentName string, timeout time.Duration) ([]team.Message, error) {
	const pollInterval = 100 * time.Millisecond

	var deadlineCtx context.Context = ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		deadlineCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// 先做一次立即检查，热消息无需等 100ms。
	if has, err := m.HasUnread(teamName, agentName); err != nil {
		return nil, err
	} else if has {
		return m.DrainUnread(teamName, agentName)
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-deadlineCtx.Done():
			if errors.Is(deadlineCtx.Err(), context.DeadlineExceeded) {
				return nil, ErrWaitTimeout
			}
			return nil, deadlineCtx.Err()
		case <-ticker.C:
			has, err := m.HasUnread(teamName, agentName)
			if err != nil {
				return nil, err
			}
			if has {
				return m.DrainUnread(teamName, agentName)
			}
		}
	}
}

// ErrWaitTimeout 由 WaitForUnread 在超时（非 ctx 取消）时返回。
var ErrWaitTimeout = errors.New("wait for unread: timeout")

func (m *Mailbox) lockTimeout() time.Duration {
	if m.LockTimeout <= 0 {
		return 5 * time.Second
	}
	return m.LockTimeout
}
