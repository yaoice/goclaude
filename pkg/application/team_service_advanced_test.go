package application

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/goclaude/pkg/domain/team"
	teamfs "github.com/anthropics/goclaude/pkg/infrastructure/team"
)

// AssignTask + ReportTask 的端到端：leader 派任务 → worker 收到 → 汇报 working/resolved →
// leader 通过 ProcessLeaderInbox 拿到回执，且 worker 状态保持。
func TestTeamService_TaskRoundTrip(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.CreateTeam(CreateTeamInput{Name: "T1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.JoinTeam(JoinTeamInput{TeamName: "T1", AgentName: "alice", AgentType: "researcher"}); err != nil {
		t.Fatal(err)
	}

	// leader assign → alice
	res, err := s.AssignTask(AssignTaskInput{
		TeamName: "T1", From: team.LeaderName, To: "alice",
		TaskID: "T-1", Subject: "调研 X", Description: "请整理 X 的现状",
	})
	if err != nil {
		t.Fatalf("AssignTask: %v", err)
	}
	if res.MessageID == "" {
		t.Fatalf("expected non-empty MessageID")
	}

	// alice drain → 拿到 task_assign
	got, err := s.ReadInbox("T1", "alice", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Type != team.MessageTaskAssign || got[0].TaskID != "T-1" {
		t.Fatalf("alice inbox = %+v, want one task_assign T-1", got)
	}
	originalID := got[0].ID

	// alice report working → leader
	if _, err := s.ReportTask(ReportTaskInput{
		TeamName: "T1", From: "alice", TaskID: "T-1",
		ReplyTo: originalID, Status: team.TaskWorking, Summary: "进度 30%",
	}); err != nil {
		t.Fatal(err)
	}
	// alice report resolved
	if _, err := s.ReportTask(ReportTaskInput{
		TeamName: "T1", From: "alice", TaskID: "T-1",
		ReplyTo: originalID, Status: team.TaskResolved,
		Summary: "完成", Detail: "结果详情……",
	}); err != nil {
		t.Fatal(err)
	}

	// leader 处理 inbox
	leaderMsgs, err := s.ProcessLeaderInbox("T1")
	if err != nil {
		t.Fatal(err)
	}
	if len(leaderMsgs) != 2 {
		t.Fatalf("leader received %d messages, want 2", len(leaderMsgs))
	}
	if leaderMsgs[0].TaskStatus != team.TaskWorking {
		t.Errorf("first reply status = %q, want working", leaderMsgs[0].TaskStatus)
	}
	if leaderMsgs[1].TaskStatus != team.TaskResolved {
		t.Errorf("second reply status = %q, want resolved", leaderMsgs[1].TaskStatus)
	}
	if leaderMsgs[0].ReplyTo != originalID || leaderMsgs[1].ReplyTo != originalID {
		t.Errorf("replyTo not preserved: %q / %q", leaderMsgs[0].ReplyTo, leaderMsgs[1].ReplyTo)
	}
}

// 状态机：SetMemberStatus 推导 IsActive；done/idle/error 都视为非活跃。
func TestTeamService_SetMemberStatus_Derivation(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.CreateTeam(CreateTeamInput{Name: "S"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.JoinTeam(JoinTeamInput{TeamName: "S", AgentName: "w"}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		st     team.MemberStatus
		active bool
	}{
		{team.StatusWorking, true},
		{team.StatusBlocked, true},
		{team.StatusIdle, false},
		{team.StatusDone, false},
		{team.StatusError, false},
	}
	for _, tc := range tests {
		if err := s.SetMemberStatus("S", "w", tc.st); err != nil {
			t.Fatalf("SetMemberStatus(%s): %v", tc.st, err)
		}
		f, _ := s.GetTeam("S")
		m := f.FindMemberByName("w")
		if m.IsActive != tc.active {
			t.Errorf("status=%s → IsActive=%v, want %v", tc.st, m.IsActive, tc.active)
		}
		if m.Status != tc.st {
			t.Errorf("status not persisted: got %q, want %q", m.Status, tc.st)
		}
	}
	if err := s.SetMemberStatus("S", "w", team.MemberStatus("bogus")); err == nil {
		t.Errorf("expected error for invalid status")
	}
}

// 心跳过期：worker 不再心跳后，ActiveNonLeaderCount 应将其视为已死，
// 允许 DeleteTeam 通过（不必 force）。
func TestTeamService_StaleHeartbeatAllowsDelete(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.CreateTeam(CreateTeamInput{Name: "H"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.JoinTeam(JoinTeamInput{TeamName: "H", AgentName: "zombie"}); err != nil {
		t.Fatal(err)
	}
	// 直接把 zombie 的 LastHeartbeatAt 改成远古，模拟崩溃成员
	f, err := s.GetTeam("H")
	if err != nil {
		t.Fatal(err)
	}
	zombie := f.FindMemberByName("zombie")
	zombie.LastHeartbeatAt = time.Now().Add(-10 * time.Minute).UnixMilli()
	if err := s.Store.Write(f); err != nil {
		t.Fatal(err)
	}
	if got := f.ActiveNonLeaderCount(); got != 0 {
		t.Errorf("stale member should not count as active: got %d, want 0", got)
	}
	deleted, err := s.DeleteTeam("H", DeleteTeamOptions{})
	if err != nil || !deleted {
		t.Errorf("expected delete success despite zombie member, got deleted=%v err=%v", deleted, err)
	}
}

// ProcessLeaderInbox: shutdown_response{approve:true} 后 worker 应被自动 SetMemberActive(false)。
func TestTeamService_ProcessShutdownResponse(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.CreateTeam(CreateTeamInput{Name: "P"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.JoinTeam(JoinTeamInput{TeamName: "P", AgentName: "worker"}); err != nil {
		t.Fatal(err)
	}
	// worker → leader：approve shutdown
	resp := team.NewShutdownResponse("worker", "req-1", true, "done")
	if _, err := s.Send(SendInput{
		TeamName: "P", From: "worker", To: team.LeaderName, Structured: &resp,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProcessLeaderInbox("P"); err != nil {
		t.Fatal(err)
	}
	f, _ := s.GetTeam("P")
	w := f.FindMemberByName("worker")
	if w.IsActive {
		t.Errorf("worker IsActive should be false after approved shutdown_response, got true")
	}
}

// ProcessLeaderInbox: idle_notification 应被翻译成 SetMemberStatus(idle)。
func TestTeamService_ProcessIdleNotification(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.CreateTeam(CreateTeamInput{Name: "I"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.JoinTeam(JoinTeamInput{TeamName: "I", AgentName: "w"}); err != nil {
		t.Fatal(err)
	}
	idle := team.NewIdleNotification("w", "round complete")
	if _, err := s.Send(SendInput{
		TeamName: "I", From: "w", To: team.LeaderName, Structured: &idle,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProcessLeaderInbox("I"); err != nil {
		t.Fatal(err)
	}
	f, _ := s.GetTeam("I")
	if got := f.FindMemberByName("w").Status; got != team.StatusIdle {
		t.Errorf("status after idle_notification = %q, want idle", got)
	}
}

// WaitForUnread：先订阅再投递，应在投递后立即唤醒并 drain。
func TestTeamService_WaitForUnread_Wakes(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.CreateTeam(CreateTeamInput{Name: "W"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.JoinTeam(JoinTeamInput{TeamName: "W", AgentName: "alice"}); err != nil {
		t.Fatal(err)
	}

	type recv struct {
		msgs []team.Message
		err  error
	}
	ch := make(chan recv, 1)
	go func() {
		msgs, err := s.WaitForUnread(context.Background(), "W", "alice", 3*time.Second)
		ch <- recv{msgs, err}
	}()
	// 给 goroutine 进入轮询循环的时间
	time.Sleep(200 * time.Millisecond)
	if _, err := s.Send(SendInput{
		TeamName: "W", From: team.LeaderName, To: "alice",
		Summary: "ping", Text: "wake up",
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("WaitForUnread err: %v", r.err)
		}
		if len(r.msgs) != 1 || r.msgs[0].Text != "wake up" {
			t.Errorf("expected single wake-up msg, got %+v", r.msgs)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("WaitForUnread did not wake within 2s")
	}
}

// WaitForUnread 超时返回 ErrWaitTimeout（不报 ctx err）。
func TestTeamService_WaitForUnread_Timeout(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.CreateTeam(CreateTeamInput{Name: "WT"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.JoinTeam(JoinTeamInput{TeamName: "WT", AgentName: "a"}); err != nil {
		t.Fatal(err)
	}
	_, err := s.WaitForUnread(context.Background(), "WT", "a", 200*time.Millisecond)
	if !errors.Is(err, teamfs.ErrWaitTimeout) {
		t.Errorf("WaitForUnread timeout: got %v, want ErrWaitTimeout", err)
	}
}

// 并发 Join 同名 agent：service 层 mutex 应保证最终只有一份成员记录。
func TestTeamService_JoinTeam_ConcurrentSameName(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.CreateTeam(CreateTeamInput{Name: "C"}); err != nil {
		t.Fatal(err)
	}
	const N = 10
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = s.JoinTeam(JoinTeamInput{TeamName: "C", AgentName: "alice"})
		}()
	}
	wg.Wait()
	f, _ := s.GetTeam("C")
	count := 0
	for _, m := range f.Members {
		if m.Name == "alice" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected single alice member after concurrent join, got %d", count)
	}
}

// Rejoin 后旧 inbox 仍可读：alice → leave → 投递 → rejoin → 应能 drain 到那条消息。
func TestTeamService_RejoinPreservesInbox(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.CreateTeam(CreateTeamInput{Name: "R"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.JoinTeam(JoinTeamInput{TeamName: "R", AgentName: "alice"}); err != nil {
		t.Fatal(err)
	}
	if err := s.LeaveTeam("R", "alice"); err != nil {
		t.Fatal(err)
	}
	// 即使 alice 不在 members 里，leader 仍可绕过 service 直接 Append 到 inbox
	// （这是 src 端"重新激活后能读到旧消息"的前置）
	if err := s.Mailbox.Append("R", "alice",
		team.NewTextMessage(team.LeaderName, "for-later", "saved while offline")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.JoinTeam(JoinTeamInput{TeamName: "R", AgentName: "alice"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadInbox("R", "alice", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "saved while offline" {
		t.Errorf("expected preserved inbox after rejoin, got %+v", got)
	}
}

// MarkReadByIDs 精细标记：drain 之外也能按 ID 把已选中的几条置为 read。
func TestMailbox_MarkReadByIDs(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.CreateTeam(CreateTeamInput{Name: "M"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.JoinTeam(JoinTeamInput{TeamName: "M", AgentName: "a"}); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 0; i < 3; i++ {
		res, err := s.Send(SendInput{
			TeamName: "M", From: team.LeaderName, To: "a",
			Summary: "s", Text: "hello",
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, res.MessageID)
	}
	n, err := s.Mailbox.MarkReadByIDs("M", "a", []string{ids[0], ids[2]})
	if err != nil || n != 2 {
		t.Errorf("MarkReadByIDs n=%d err=%v, want n=2", n, err)
	}
	// 还剩 1 条未读
	left, _ := s.Mailbox.ReadUnread("M", "a")
	if len(left) != 1 || left[0].ID != ids[1] {
		t.Errorf("after MarkReadByIDs, leftover unread = %+v, want only ids[1]", left)
	}
}

// AssignTask 校验：subject 必填、task_id 必填、status 必须合法。
func TestTeamService_AssignTask_Validation(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.CreateTeam(CreateTeamInput{Name: "V"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.JoinTeam(JoinTeamInput{TeamName: "V", AgentName: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AssignTask(AssignTaskInput{
		TeamName: "V", From: team.LeaderName, To: "x", TaskID: "", Subject: "x",
	}); err == nil {
		t.Errorf("expected error for empty task_id")
	}
	if _, err := s.AssignTask(AssignTaskInput{
		TeamName: "V", From: team.LeaderName, To: "x", TaskID: "t1", Subject: "",
	}); err == nil {
		t.Errorf("expected error for empty subject")
	}
	if _, err := s.ReportTask(ReportTaskInput{
		TeamName: "V", From: "x", TaskID: "t1", Status: "bogus",
	}); err == nil {
		t.Errorf("expected error for invalid status")
	}
}
