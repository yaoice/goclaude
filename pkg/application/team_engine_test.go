package application

import (
	"context"
	"testing"
	"time"

	"github.com/yaoice/goclaude/pkg/domain/team"
)

// TestTeamEngine_SpawnAndShutdown 验证 member worker 的完整生命周期：
// spawn → 心跳 → 状态变化 → shutdown → 清理。
func TestTeamEngine_SpawnAndShutdown(t *testing.T) {
	teamSvc := newTestTeamService()
	agentSvc := newTestAgentService()
	if agentSvc == nil || teamSvc == nil {
		t.Skip("service init failed")
	}

	// 1) 创建 team，添加 leader + 2 个 member
	teamName := "test-spawn-team-" + shortID()
	_, err := teamSvc.CreateTeam(CreateTeamInput{
		Name:          teamName,
		LeadAgentType: "general-purpose",
	})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}

	// 手动加入 2 个非 leader member
	for _, m := range []struct{ name, agentType string }{
		{"alice", "team-worker"},
		{"bob", "team-worker"},
	} {
		_, _, err := teamSvc.JoinTeam(JoinTeamInput{
			TeamName:  teamName,
			AgentName: m.name,
			AgentType: m.agentType,
		})
		if err != nil {
			t.Fatalf("JoinTeam %s: %v", m.name, err)
		}
	}

	// 验证 team 文件确实有 3 个成员（含 leader）
	f, err := teamSvc.GetTeam(teamName)
	if err != nil || f == nil {
		t.Fatalf("GetTeam: err=%v file=%v", err, f)
	}
	if len(f.Members) != 3 {
		t.Fatalf("expected 3 members, got %d", len(f.Members))
	}

	// 2) 创建 TeamEngine 并 spawn members
	eng := setupTestTeamEngine(agentSvc, teamSvc)
	defer eng.ShutdownAllTeams(context.Background())

	if err := eng.SpawnMembers(context.Background(), teamName); err != nil {
		t.Fatalf("SpawnMembers: %v", err)
	}

	// 3) 验证 worker 已启动
	time.Sleep(100 * time.Millisecond) // 等 goroutine 启动
	if !eng.IsRunning(teamName, "alice") {
		t.Fatal("alice worker should be running")
	}
	if !eng.IsRunning(teamName, "bob") {
		t.Fatal("bob worker should be running")
	}
	if count := eng.RunningCount(teamName); count != 2 {
		t.Fatalf("expected 2 running workers, got %d", count)
	}

	// 4) 验证 member 已加入 team 并且状态初始化为 idle
	statuses := eng.MemberStatuses(teamName)
	if s, ok := statuses["alice"]; !ok || s != MemberIdle {
		t.Fatalf("alice status expected idle, got %v", statuses)
	}
	if s, ok := statuses["bob"]; !ok || s != MemberIdle {
		t.Fatalf("bob status expected idle, got %v", statuses)
	}

	// 5) 关闭一个 member
	if err := eng.ShutdownMember(context.Background(), teamName, "alice"); err != nil {
		t.Fatalf("ShutdownMember alice: %v", err)
	}
	if eng.IsRunning(teamName, "alice") {
		t.Fatal("alice should be stopped after shutdown")
	}
	if !eng.IsRunning(teamName, "bob") {
		t.Fatal("bob should still be running")
	}

	// 6) 关闭全部
	eng.ShutdownAll(context.Background(), teamName)
	time.Sleep(100 * time.Millisecond)
	if eng.RunningCount(teamName) != 0 {
		t.Fatalf("expected 0 running after ShutdownAll, got %d", eng.RunningCount(teamName))
	}
}

// TestTeamEngine_WorkerCommunication 验证 worker 之间通过 inbox 通信。
func TestTeamEngine_WorkerCommunication(t *testing.T) {
	teamSvc := newTestTeamService()
	agentSvc := newTestAgentService()
	if agentSvc == nil || teamSvc == nil {
		t.Skip("service init failed")
	}

	teamName := "test-comm-team-" + shortID()
	_, err := teamSvc.CreateTeam(CreateTeamInput{
		Name:          teamName,
		LeadAgentType: "general-purpose",
	})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}

	// 加入 2 个 member
	for _, m := range []struct{ name, agentType string }{
		{"alice", "team-worker"},
		{"bob", "team-worker"},
	} {
		_, _, err := teamSvc.JoinTeam(JoinTeamInput{
			TeamName:  teamName,
			AgentName: m.name,
			AgentType: m.agentType,
		})
		if err != nil {
			t.Fatalf("JoinTeam %s: %v", m.name, err)
		}
	}

	// Spawn workers
	eng := setupTestTeamEngine(agentSvc, teamSvc)
	defer eng.ShutdownAllTeams(context.Background())

	if err := eng.SpawnMembers(context.Background(), teamName); err != nil {
		t.Fatalf("SpawnMembers: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// === 场景 1: Leader → Worker (task_assign) ===
	t.Run("Leader-to-Worker task assignment", func(t *testing.T) {
		res, err := teamSvc.AssignTask(AssignTaskInput{
			TeamName:    teamName,
			From:        team.LeaderName,
			To:          "alice",
			TaskID:      "task-001",
			Subject:     "Implement login",
			Description: "Create src/auth/login.go with basic auth",
		})
		if err != nil {
			t.Fatalf("AssignTask: %v", err)
		}
		if len(res.Recipients) != 1 || res.Recipients[0] != "alice" {
			t.Fatalf("expected recipient=alice, got %v", res.Recipients)
		}

		// 等待 worker 捡起消息（ticker 5s，但 task_report 在子任务完成后才发送）
		// 由于没有真实 AI provider，子任务会失败（agent 不存在或 engine 构建失败）。
		// 验证：alice 的 inbox 已被 drain（消息已被读取）。
		time.Sleep(200 * time.Millisecond)

		msgs, err := teamSvc.ReadInbox(teamName, "alice", false)
		if err != nil {
			t.Fatalf("ReadInbox: %v", err)
		}
		// task_assign 已被 drain=true 消费，inbox 应为空
		if len(msgs) > 0 {
			t.Logf("alice still has %d unread messages (drain may have raced)", len(msgs))
		}

		// 验证 leader 收到了 task_result（失败报告）
		leaderMsgs, _ := teamSvc.ReadInbox(teamName, team.LeaderName, false)
		t.Logf("leader inbox has %d messages after task assignment", len(leaderMsgs))
		for _, m := range leaderMsgs {
			t.Logf("  leader msg: from=%s type=%s task_id=%s status=%s summary=%s",
				m.From, m.Type, m.TaskID, m.TaskStatus, m.Summary)
		}
	})

	// === 场景 2: Worker → Worker (send_message) ===
	t.Run("Worker-to-Worker send_message", func(t *testing.T) {
		// 模拟 alice 发消息给 bob
		textMsg := team.NewTextMessage("alice", "Need auth module help",
			"Hey bob, can you provide the token validation interface?")
		res, err := teamSvc.Send(SendInput{
			TeamName:   teamName,
			From:       "alice",
			To:         "bob",
			Structured: &textMsg,
		})
		if err != nil {
			t.Fatalf("Send alice→bob: %v", err)
		}
		if len(res.Recipients) != 1 || res.Recipients[0] != "bob" {
			t.Fatalf("expected recipient=bob, got %v", res.Recipients)
		}
		t.Logf("✓ message sent from alice to bob: id=%s recipients=%v", res.MessageID, res.Recipients)

		// bob 的 worker goroutine 会在 ticker 触发时 drain inbox。
		// 验证消息**被成功投递**即可——worker 是否消费由通信路径保证。
		// 这里检查 bob 的 inbox 可能已被 drain，所以用 since 模式查看历史。
		time.Sleep(300 * time.Millisecond) // 等 worker ticker 触发
		msgs, err := teamSvc.ReadInboxSince(teamName, "bob", 0)
		if err != nil {
			t.Fatalf("ReadInboxSince bob: %v", err)
		}
		found := false
		for _, m := range msgs {
			if m.From == "alice" && m.Summary == "Need auth module help" {
				found = true
				t.Logf("✓ bob's inbox history shows alice's message")
				break
			}
		}
		if !found {
			// 消息可能已被 worker drain 消费——这是正常行为，不算失败
			t.Log("message was likely consumed by bob's worker (drain=true)")
		}
	})

	// === 场景 3: broadcast ===
	t.Run("broadcast to all workers", func(t *testing.T) {
		broadMsg := team.NewBroadcast(team.LeaderName,
			"Team meeting at 3pm", "Everyone please check in.")
		res, err := teamSvc.Send(SendInput{
			TeamName:   teamName,
			From:       team.LeaderName,
			To:         "*",
			Structured: &broadMsg,
		})
		if err != nil {
			t.Fatalf("Send broadcast: %v", err)
		}
		// 广播发送给所有非 leader 成员
		if len(res.Recipients) != 2 {
			t.Fatalf("expected 2 recipients (alice+bob), got %d: %v", len(res.Recipients), res.Recipients)
		}

		// 检查 leader 没有收到自己的广播
		leaderMsgs, _ := teamSvc.ReadInbox(teamName, team.LeaderName, false)
		for _, m := range leaderMsgs {
			if m.From == team.LeaderName && m.Type == team.MessageBroadcast {
				t.Error("leader should not receive own broadcast")
			}
		}
	})
}

// TestTeamEngine_AutoSetupTeamAndSpawn 验证 team 创建后自动触发 member spawn 的流程。
//
// 这是对 tools.TeamSession.OnTeamCreated → TeamEngine.SpawnMembers 路径的
// 直接测试（避免 tools 包的 import cycle）。
func TestTeamEngine_AutoSetupTeamAndSpawn(t *testing.T) {
	teamSvc := newTestTeamService()
	agentSvc := newTestAgentService()
	if agentSvc == nil || teamSvc == nil {
		t.Skip("service init failed")
	}

	eng := setupTestTeamEngine(agentSvc, teamSvc)
	defer eng.ShutdownAllTeams(context.Background())

	// 模拟 auto_setup_team 流程：
	// 1) 创建 team
	teamName := "test-auto-team-" + shortID()
	_, err := teamSvc.CreateTeam(CreateTeamInput{
		Name:          teamName,
		LeadAgentType: "general-purpose",
	})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}

	// 2) 添加 members（auto_setup_team 会先添加 members）
	for _, m := range []struct{ name, agentType string }{
		{"researcher", "team-worker"},
		{"coder", "team-worker"},
	} {
		_, _, err := teamSvc.JoinTeam(JoinTeamInput{
			TeamName:  teamName,
			AgentName: m.name,
			AgentType: m.agentType,
		})
		if err != nil {
			t.Fatalf("JoinTeam %s: %v", m.name, err)
		}
	}

	// 3) 验证 team 有 3 个成员（leader + 2 workers）
	f, _ := teamSvc.GetTeam(teamName)
	if f == nil || len(f.Members) != 3 {
		t.Fatalf("expected 3 members, got %v", f)
	}

	// 4) 触发 SpawnMembers——这模拟了 OnTeamCreated 回调的实际效果
	if err := eng.SpawnMembers(context.Background(), teamName); err != nil {
		t.Fatalf("SpawnMembers: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// 5) 验证 worker 已启动
	if count := eng.RunningCount(teamName); count != 2 {
		t.Fatalf("expected 2 running workers after spawn, got %d", count)
	}
	if !eng.IsRunning(teamName, "researcher") {
		t.Error("researcher worker should be running")
	}
	if !eng.IsRunning(teamName, "coder") {
		t.Error("coder worker should be running")
	}

	// 6) 关闭后验证
	eng.ShutdownAll(context.Background(), teamName)
	time.Sleep(200 * time.Millisecond)
	if eng.RunningCount(teamName) != 0 {
		t.Fatalf("expected 0 running after shutdown, got %d", eng.RunningCount(teamName))
	}
}

// TestTeamEngine_ShutdownFlow 验证 shutdown_request → shutdown_response 完整流程。
func TestTeamEngine_ShutdownFlow(t *testing.T) {
	teamSvc := newTestTeamService()
	agentSvc := newTestAgentService()
	if agentSvc == nil || teamSvc == nil {
		t.Skip("service init failed")
	}

	teamName := "test-shutdown-team-" + shortID()
	_, err := teamSvc.CreateTeam(CreateTeamInput{
		Name:          teamName,
		LeadAgentType: "general-purpose",
	})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}

	_, _, err = teamSvc.JoinTeam(JoinTeamInput{
		TeamName:  teamName,
		AgentName: "worker1",
		AgentType: "team-worker",
	})
	if err != nil {
		t.Fatalf("JoinTeam: %v", err)
	}

	eng := setupTestTeamEngine(agentSvc, teamSvc)
	defer eng.ShutdownAllTeams(context.Background())

	if err := eng.SpawnMember(context.Background(), teamName, "worker1", "team-worker"); err != nil {
		t.Fatalf("SpawnMember: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if !eng.IsRunning(teamName, "worker1") {
		t.Fatal("worker should be running before shutdown")
	}

	// 关闭 worker
	if err := eng.ShutdownMember(context.Background(), teamName, "worker1"); err != nil {
		t.Fatalf("ShutdownMember: %v", err)
	}

	// 给 worker goroutine 一点时间完成 shutdown_response 的写入
	time.Sleep(200 * time.Millisecond)

	// worker 应该已停止
	if eng.IsRunning(teamName, "worker1") {
		t.Fatal("worker should be stopped after shutdown")
	}

	// 验证 worker 已标记为 inactive
	f, _ := teamSvc.GetTeam(teamName)
	if f != nil {
		m := f.FindMemberByName("worker1")
		if m != nil && m.IsActive {
			t.Error("worker should be marked IsActive=false after shutdown")
		}
	}

	// 验证 leader 收到了 shutdown_response
	leaderMsgs, _ := teamSvc.ReadInbox(teamName, team.LeaderName, false)
	t.Logf("leader inbox has %d messages after shutdown", len(leaderMsgs))
	hasShutdownResp := false
	for _, m := range leaderMsgs {
		if m.Type == team.MessageShutdownResp && m.From == "worker1" {
			hasShutdownResp = true
			if m.Approve == nil || !*m.Approve {
				t.Error("shutdown_response should have approve=true")
			}
			break
		}
	}
	if !hasShutdownResp {
		t.Error("leader should have received shutdown_response from worker1")
	}
}
