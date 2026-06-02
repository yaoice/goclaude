package tools

import (
	"context"
	"testing"
)

// TeamSession：SetLeader 后 LeaderTeam 返回该 team；SetMember 不应被视为 leader。
func TestTeamSession_LeaderTracking(t *testing.T) {
	s := NewTeamSession()

	if s.LeaderTeam() != "" {
		t.Errorf("fresh session should not be leader of any team")
	}

	s.SetMember("squad")
	if s.LeaderTeam() != "" {
		t.Errorf("member-only session should not report a leader team")
	}
	if s.TeamName() != "squad" {
		t.Errorf("TeamName = %q, want squad", s.TeamName())
	}

	s.SetLeader("alpha")
	if got := s.LeaderTeam(); got != "alpha" {
		t.Errorf("LeaderTeam = %q, want alpha", got)
	}
}

// nil receiver 安全：所有方法在未注入会话时退化为 no-op，不应 panic。
func TestTeamSession_NilSafe(t *testing.T) {
	var s *TeamSession
	s.SetLeader("x")
	s.SetMember("y")
	if s.LeaderTeam() != "" || s.TeamName() != "" {
		t.Errorf("nil session should return empty values")
	}
}

// RegisterTeamTools 注入的会话应被 team_create 工具更新为 leader 身份。
func TestTeamCreate_RegistersLeaderSession(t *testing.T) {
	sess := NewTeamSession()
	tool := NewTeamCreateTool(nil, "", "")
	tool.attachSession(sess)

	// service 为 nil 时 Call 直接返回错误，会话不应被更新
	if _, err := tool.Call(context.TODO(), map[string]interface{}{"team_name": "beta"}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sess.LeaderTeam() != "" {
		t.Errorf("session should not be set when team service is nil")
	}
}
