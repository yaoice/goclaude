package teamfs

import (
	"sync"
	"testing"

	"github.com/anthropics/goclaude/pkg/domain/team"
)

// 关键不变量：构造一个 leader-only team file 并存盘，再读回来必须等价。
func TestStore_ReadWriteRoundtrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := NewStore(Layout{HomeDir: dir})

	tf := &team.File{
		Name:        "Alpha Team",
		Description: "exploration",
		LeadAgentID: team.FormatAgentID(team.LeaderName, "Alpha Team"),
		Members: []team.Member{
			{
				AgentID:  team.FormatAgentID(team.LeaderName, "Alpha Team"),
				Name:     team.LeaderName,
				JoinedAt: 1,
				IsActive: true,
			},
		},
	}
	if err := s.Write(tf); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := s.Read("Alpha Team")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got == nil {
		t.Fatal("read returned nil for existing team")
	}
	if got.Name != tf.Name || got.LeadAgentID != tf.LeadAgentID {
		t.Errorf("mismatch: got %+v want %+v", got, tf)
	}
	if len(got.Members) != 1 {
		t.Errorf("members len = %d, want 1", len(got.Members))
	}
}

// 路径必须按 SanitizeName 规范化，确保跨进程引用同一个 team 不会因大小写/空格分裂。
func TestStore_PathSanitization(t *testing.T) {
	t.Parallel()
	l := Layout{HomeDir: "/h"}
	if got, want := l.TeamDir("Alpha Team"), "/h/.goclaude/teams/alpha-team"; got != want {
		t.Errorf("TeamDir = %q want %q", got, want)
	}
	if got, want := l.InboxPath("Alpha Team", "Bot.X"), "/h/.goclaude/teams/alpha-team/inboxes/bot-x.json"; got != want {
		t.Errorf("InboxPath = %q want %q", got, want)
	}
}

// Read 不存在的 team 应返回 (nil, nil) 而非错误。
func TestStore_ReadNonexistent(t *testing.T) {
	t.Parallel()
	s := NewStore(Layout{HomeDir: t.TempDir()})
	f, err := s.Read("nope")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if f != nil {
		t.Errorf("expected nil for missing team, got %+v", f)
	}
}

// Delete 不存在的 team 应返回 (false, nil)。
func TestStore_DeleteNonexistent(t *testing.T) {
	t.Parallel()
	s := NewStore(Layout{HomeDir: t.TempDir()})
	deleted, err := s.Delete("never")
	if err != nil || deleted {
		t.Errorf("expected (false, nil), got (%v, %v)", deleted, err)
	}
}

// 并发追加：N 个 goroutine 同时 Append，最终消息数必须等于 N（锁未失效）。
//
// 这是核心安全性测试 —— 如果 acquireLock 实现错误，多写者会互相覆盖，msgs.len < N。
func TestMailbox_ConcurrentAppend_NoLoss(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mb := NewMailbox(Layout{HomeDir: dir})
	const teamName, agent = "swarm", "worker"
	const writers = 30

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			err := mb.Append(teamName, agent, team.NewTextMessage(
				"sender",
				"concurrent",
				// id 隐含在 timestamp 区分，但显式 Text 让我们能验证内容
				string(rune('A'+id%26)),
			))
			if err != nil {
				t.Errorf("append #%d: %v", id, err)
			}
		}(i)
	}
	wg.Wait()

	got, err := mb.Read(teamName, agent)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != writers {
		t.Errorf("got %d messages, want %d (lost writes — lock failed)", len(got), writers)
	}
}

// DrainUnread 只对未读消息标记并返回；二次调用应返回 0 条。
func TestMailbox_DrainUnread_Idempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mb := NewMailbox(Layout{HomeDir: dir})
	const teamName, agent = "ops", "alice"

	for i := 0; i < 3; i++ {
		if err := mb.Append(teamName, agent, team.NewTextMessage("bob", "s", "m")); err != nil {
			t.Fatal(err)
		}
	}

	first, err := mb.DrainUnread(teamName, agent)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 3 {
		t.Errorf("first drain got %d, want 3", len(first))
	}

	second, err := mb.DrainUnread(teamName, agent)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Errorf("second drain got %d, want 0 (already marked read)", len(second))
	}

	// Read（含已读）应仍能看到全部 3 条
	all, _ := mb.Read(teamName, agent)
	if len(all) != 3 {
		t.Errorf("Read() after drain got %d, want 3 (drain shouldn't delete)", len(all))
	}
}

// ReadUnread 不修改 read 标记，可重复调用。
func TestMailbox_ReadUnread_NonDestructive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mb := NewMailbox(Layout{HomeDir: dir})
	if err := mb.Append("t", "a", team.NewTextMessage("x", "s", "y")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		got, _ := mb.ReadUnread("t", "a")
		if len(got) != 1 {
			t.Errorf("call %d: got %d unread, want 1", i, len(got))
		}
	}
}
