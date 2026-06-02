package application

import (
	"testing"

	"github.com/anthropics/goclaude/internal/domain/team"
)

// NotifyTaskAssigned 应把一条 task_assign 消息投递到被分配者的 inbox，
// 闭合"任务分配 → inbox 推送"的调度回路（被分配者无需轮询即可被唤醒）。
func TestTeamService_NotifyTaskAssigned_DeliversToInbox(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.CreateTeam(CreateTeamInput{Name: "N"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.JoinTeam(JoinTeamInput{TeamName: "N", AgentName: "alice"}); err != nil {
		t.Fatal(err)
	}

	if err := s.NotifyTaskAssigned("N", "task-1", "调研 X", "请整理 X 现状", "alice", team.LeaderName); err != nil {
		t.Fatalf("NotifyTaskAssigned: %v", err)
	}

	got, err := s.ReadInbox("N", "alice", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("alice inbox = %d msgs, want 1", len(got))
	}
	m := got[0]
	if m.Type != team.MessageTaskAssign {
		t.Errorf("msg type = %q, want task_assign", m.Type)
	}
	if m.TaskID != "task-1" {
		t.Errorf("task id = %q, want task-1", m.TaskID)
	}
	if m.From != team.LeaderName {
		t.Errorf("from = %q, want %q", m.From, team.LeaderName)
	}
	if m.Summary != "调研 X" {
		t.Errorf("summary = %q, want '调研 X'", m.Summary)
	}
}

// 自分配（assignedTo == assignedBy）不应产生通知；taskID/assignedTo 为空亦为 no-op。
func TestTeamService_NotifyTaskAssigned_SelfAndEmptyAreNoop(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.CreateTeam(CreateTeamInput{Name: "NS"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.JoinTeam(JoinTeamInput{TeamName: "NS", AgentName: "bob"}); err != nil {
		t.Fatal(err)
	}

	// 自分配
	if err := s.NotifyTaskAssigned("NS", "t1", "x", "", "bob", "bob"); err != nil {
		t.Fatalf("self-assign should be no-op, got err: %v", err)
	}
	// 空 assignedTo
	if err := s.NotifyTaskAssigned("NS", "t1", "x", "", "", team.LeaderName); err != nil {
		t.Fatalf("empty assignedTo should be no-op, got err: %v", err)
	}
	// 空 taskID
	if err := s.NotifyTaskAssigned("NS", "", "x", "", "bob", team.LeaderName); err != nil {
		t.Fatalf("empty taskID should be no-op, got err: %v", err)
	}

	got, err := s.ReadInbox("NS", "bob", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("bob inbox should be empty after no-op notifications, got %d", len(got))
	}
}

// 端到端调度闭环：CreateTask(assigned_to) → NotifyTaskAssigned 推送 →
// worker 收到 task_assign → ReportTask 回流 → ProcessLeaderInbox 同步进共享任务列表。
func TestTeamService_AssignmentClosure_EndToEnd(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	if _, err := s.CreateTeam(CreateTeamInput{Name: "E2E"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.JoinTeam(JoinTeamInput{TeamName: "E2E", AgentName: "carol"}); err != nil {
		t.Fatal(err)
	}

	// leader 在共享列表创建任务并指派给 carol
	taskID := "task-e2e"
	if err := s.CreateTask("E2E", team.SharedTask{
		ID: taskID, Title: "实现登录", Description: "登录 API", Status: team.SharedTaskWorking, AssignedTo: "carol",
	}); err != nil {
		t.Fatal(err)
	}
	// 模拟工具层的通知动作
	if err := s.NotifyTaskAssigned("E2E", taskID, "实现登录", "登录 API", "carol", team.LeaderName); err != nil {
		t.Fatal(err)
	}

	// carol 收到 task_assign（被主动唤醒）
	inbox, err := s.ReadInbox("E2E", "carol", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox) != 1 || inbox[0].TaskID != taskID {
		t.Fatalf("carol should receive task_assign for %s, got %+v", taskID, inbox)
	}

	// carol 完成并汇报
	if _, err := s.ReportTask(ReportTaskInput{
		TeamName: "E2E", From: "carol", TaskID: taskID,
		Status: team.TaskResolved, Summary: "完成", Detail: "已实现",
	}); err != nil {
		t.Fatal(err)
	}

	// leader 处理 inbox → 共享任务列表应被同步为 completed
	if _, err := s.ProcessLeaderInbox("E2E"); err != nil {
		t.Fatal(err)
	}
	tk, err := s.GetTask("E2E", taskID)
	if err != nil {
		t.Fatal(err)
	}
	if tk.Status != team.SharedTaskCompleted {
		t.Errorf("task status after report+process = %q, want completed", tk.Status)
	}
	if tk.Result != "已实现" {
		t.Errorf("task result = %q, want '已实现'", tk.Result)
	}
}
