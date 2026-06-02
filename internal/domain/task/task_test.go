package task

import (
	"context"
	"testing"
)

func TestTask_Lifecycle(t *testing.T) {
	task := NewTask("task-1", TaskTypeLocalBash, "test task")

	// 初始状态
	if task.GetStatus() != TaskStatusPending {
		t.Errorf("expected pending, got %s", task.GetStatus())
	}
	if task.IsTerminal() {
		t.Error("new task should not be terminal")
	}

	// 启动
	_, cancel := context.WithCancel(context.Background())
	if err := task.Start(cancel); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if task.GetStatus() != TaskStatusRunning {
		t.Errorf("expected running, got %s", task.GetStatus())
	}
	if task.StartedAt == nil {
		t.Error("StartedAt should be set")
	}

	// 重复启动应失败
	if err := task.Start(cancel); err == nil {
		t.Error("expected error on double start")
	}

	// 完成
	task.Complete("output data")
	if task.GetStatus() != TaskStatusCompleted {
		t.Errorf("expected completed, got %s", task.GetStatus())
	}
	if !task.IsTerminal() {
		t.Error("completed task should be terminal")
	}
	if task.Output != "output data" {
		t.Errorf("expected 'output data', got %q", task.Output)
	}
}

func TestTask_Kill(t *testing.T) {
	task := NewTask("task-2", TaskTypeLocalAgent, "agent task")

	cancelled := false
	_, cancel := context.WithCancel(context.Background())
	task.cancel = func() {
		cancelled = true
		cancel()
	}
	task.Status = TaskStatusRunning

	task.Kill()

	if task.GetStatus() != TaskStatusKilled {
		t.Errorf("expected killed, got %s", task.GetStatus())
	}
	if !cancelled {
		t.Error("cancel should have been called")
	}
	if !task.IsTerminal() {
		t.Error("killed task should be terminal")
	}
}

func TestTask_Fail(t *testing.T) {
	task := NewTask("task-3", TaskTypeLocalBash, "failing task")
	task.Status = TaskStatusRunning

	task.Fail(context.DeadlineExceeded)

	if task.GetStatus() != TaskStatusFailed {
		t.Errorf("expected failed, got %s", task.GetStatus())
	}
	if task.Error == "" {
		t.Error("error message should be set")
	}
}

func TestManager_CRUD(t *testing.T) {
	mgr := NewManager()

	task1 := NewTask("1", TaskTypeLocalBash, "task 1")
	task2 := NewTask("2", TaskTypeLocalAgent, "task 2")

	mgr.Add(task1)
	mgr.Add(task2)

	// Get
	found, ok := mgr.Get("1")
	if !ok || found.Name != "task 1" {
		t.Error("Get failed")
	}

	_, ok = mgr.Get("999")
	if ok {
		t.Error("should not find nonexistent")
	}

	// List all
	all := mgr.List()
	if len(all) != 2 {
		t.Errorf("expected 2, got %d", len(all))
	}

	// List by status
	task1.Status = TaskStatusRunning
	running := mgr.List(TaskStatusRunning)
	if len(running) != 1 {
		t.Errorf("expected 1 running, got %d", len(running))
	}

	// RunningCount
	if mgr.RunningCount() != 1 {
		t.Errorf("expected 1 running count")
	}

	// Remove
	mgr.Remove("1")
	all = mgr.List()
	if len(all) != 1 {
		t.Errorf("expected 1 after remove, got %d", len(all))
	}
}
