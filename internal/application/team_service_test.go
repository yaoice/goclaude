package application

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/anthropics/goclaude/internal/domain/team"
	teamfs "github.com/anthropics/goclaude/internal/infrastructure/team"
)

func newTestService(t *testing.T) *TeamService {
	t.Helper()
	return NewTeamServiceWithLayout(teamfs.Layout{HomeDir: t.TempDir()})
}

// 创建团队 → 必含 leader 一个成员，且 LeadAgentID 形如 team-lead@<sanitized>。
func TestTeamService_CreateTeam(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	f, err := s.CreateTeam(CreateTeamInput{Name: "Research Squad"})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if f.LeadAgentID != "team-lead@research-squad" {
		t.Errorf("LeadAgentID = %q, want team-lead@research-squad", f.LeadAgentID)
	}
	if len(f.Members) != 1 || f.Members[0].Name != team.LeaderName {
		t.Errorf("expected single leader member, got %+v", f.Members)
	}
}

// 重复创建同名 team 必须返回 ErrTeamExists（让上层选择重命名）。
func TestTeamService_CreateTeam_Duplicate(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.CreateTeam(CreateTeamInput{Name: "Dup"}); err != nil {
		t.Fatal(err)
	}
	_, err := s.CreateTeam(CreateTeamInput{Name: "Dup"})
	if !errors.Is(err, ErrTeamExists) {
		t.Errorf("got %v, want ErrTeamExists", err)
	}
}

// JoinTeam 重复加入同 name 不报错——视为重新激活。
func TestTeamService_JoinTeam_Reactivate(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.CreateTeam(CreateTeamInput{Name: "X"}); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.JoinTeam(JoinTeamInput{TeamName: "X", AgentName: "alice", AgentType: "researcher"})
	if err != nil {
		t.Fatal(err)
	}
	// 设为 idle
	if err := s.SetMemberActive("X", "alice", false); err != nil {
		t.Fatal(err)
	}
	// 重新 join → 应被复活
	_, m, err := s.JoinTeam(JoinTeamInput{TeamName: "X", AgentName: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsActive {
		t.Errorf("rejoin should reactivate (IsActive=true), got %+v", m)
	}
}

// 非法发送方 / 收件方 → ErrMemberNotFound。
func TestTeamService_Send_UnknownPeer(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.CreateTeam(CreateTeamInput{Name: "T"}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Send(SendInput{TeamName: "T", From: "ghost", To: team.LeaderName, Text: "hi"})
	if !errors.Is(err, ErrMemberNotFound) {
		t.Errorf("ghost sender: got %v, want ErrMemberNotFound", err)
	}
}

// Broadcast：from=leader → 应送达所有非 leader 成员，且不送给自己。
func TestTeamService_Broadcast(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.CreateTeam(CreateTeamInput{Name: "B"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alice", "bob", "carol"} {
		if _, _, err := s.JoinTeam(JoinTeamInput{TeamName: "B", AgentName: name}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := s.Send(SendInput{
		TeamName: "B", From: team.LeaderName, To: "*",
		Summary: "kickoff", Text: "go!",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Recipients) != 3 {
		t.Errorf("recipients = %v, want 3", res.Recipients)
	}
	for _, r := range res.Recipients {
		if r == team.LeaderName {
			t.Errorf("broadcast should not include sender; got %q in recipients", r)
		}
	}
	// Leader 自己 inbox 应为空（没人发给他）
	mine, _ := s.ReadInbox("B", team.LeaderName, false)
	if len(mine) != 0 {
		t.Errorf("leader inbox should be empty after own broadcast, got %d", len(mine))
	}
	// alice 应收到 1 条
	hers, _ := s.ReadInbox("B", "alice", false)
	if len(hers) != 1 || hers[0].Type != team.MessageBroadcast {
		t.Errorf("alice should have 1 broadcast, got %+v", hers)
	}
}

// 结构化消息 round-trip：发送 shutdown_request → 接收方应解析出 RequestID + Type。
func TestTeamService_StructuredShutdownRequest(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.CreateTeam(CreateTeamInput{Name: "S"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.JoinTeam(JoinTeamInput{TeamName: "S", AgentName: "worker"}); err != nil {
		t.Fatal(err)
	}
	req := team.NewShutdownRequest(team.LeaderName, "req-123", "work done")
	if _, err := s.Send(SendInput{
		TeamName: "S", From: team.LeaderName, To: "worker", Structured: &req,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadInbox("S", "worker", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Type != team.MessageShutdownReq || got[0].RequestID != "req-123" {
		t.Errorf("got %+v, want shutdown_request with id req-123", got)
	}
}

// DeleteTeam 默认拒绝在仍有 IsActive 非 leader 成员时清理；force=true 时强制清理。
func TestTeamService_DeleteTeam_GuardActiveMembers(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.CreateTeam(CreateTeamInput{Name: "G"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.JoinTeam(JoinTeamInput{TeamName: "G", AgentName: "busy"}); err != nil {
		t.Fatal(err)
	}
	_, err := s.DeleteTeam("G", DeleteTeamOptions{})
	if !errors.Is(err, ErrTeamHasActiveMembers) {
		t.Errorf("non-force delete: got %v, want ErrTeamHasActiveMembers", err)
	}
	deleted, err := s.DeleteTeam("G", DeleteTeamOptions{Force: true})
	if err != nil || !deleted {
		t.Errorf("force delete failed: deleted=%v err=%v", deleted, err)
	}
}

// 多 goroutine 并发对同一收件人投递：count 守恒（验证锁层在 service 之上仍正确）。
func TestTeamService_ConcurrentSend_NoLoss(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.CreateTeam(CreateTeamInput{Name: "P"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c"} {
		if _, _, err := s.JoinTeam(JoinTeamInput{TeamName: "P", AgentName: name}); err != nil {
			t.Fatal(err)
		}
	}
	const writers = 20
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		from := []string{"a", "b", "c"}[i%3]
		go func(from string, idx int) {
			defer wg.Done()
			_, err := s.Send(SendInput{
				TeamName: "P", From: from, To: team.LeaderName,
				Summary: "concurrent", Text: strings.Repeat("x", idx+1),
			})
			if err != nil {
				t.Errorf("send #%d from %s: %v", idx, from, err)
			}
		}(from, i)
	}
	wg.Wait()
	got, _ := s.ReadInbox("P", team.LeaderName, false)
	if len(got) != writers {
		t.Errorf("leader inbox got %d, want %d (lost writes)", len(got), writers)
	}
}
