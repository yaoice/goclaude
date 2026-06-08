package workflow_test

import (
	"testing"

	wf "github.com/anthropics/goclaude/pkg/domain/workflow"
)

func TestWorkflowState_NodeLifecycle(t *testing.T) {
	state := wf.NewWorkflowState("test-wf", []string{"A", "B", "C"}, 2)

	// Initial state
	if state.GetStatus() != wf.WorkflowStatusPending {
		t.Fatalf("expected pending, got %s", state.GetStatus())
	}

	// Queue nodes and start
	state.QueueNode("A")
	state.QueueNode("B")

	if err := state.StartNode("A"); err != nil {
		t.Fatalf("start node A: %v", err)
	}
	ns, ok := state.GetNodeState("A")
	if !ok {
		t.Fatal("node A not found")
	}
	if ns.Status != wf.NodeStatusRunning {
		t.Fatalf("expected running, got %s", ns.Status)
	}

	// Complete node A
	state.CompleteNode("A", "output A")
	ns, ok = state.GetNodeState("A")
	if !ok || ns.Status != wf.NodeStatusCompleted {
		t.Fatalf("expected completed, got %s", ns.Status)
	}
	if ns.Output != "output A" {
		t.Fatalf("expected 'output A', got %s", ns.Output)
	}

	// Fail node B
	state.FailNode("B", "error B")
	ns, ok = state.GetNodeState("B")
	if !ok || ns.Status != wf.NodeStatusFailed {
		t.Fatalf("expected failed, got %s", ns.Status)
	}

	// Skip node C
	state.SkipNode("C", "reason")
	ns, ok = state.GetNodeState("C")
	if !ok || ns.Status != wf.NodeStatusSkipped {
		t.Fatalf("expected skipped, got %s", ns.Status)
	}

	// Check overall status
	state.SetStatus(wf.WorkflowStatusFailed)
	if state.GetStatus() != wf.WorkflowStatusFailed {
		t.Fatalf("expected failed, got %s", state.GetStatus())
	}
}

func TestWorkflowState_CountByStatus(t *testing.T) {
	state := wf.NewWorkflowState("test", []string{"A", "B", "C", "D"}, 1)

	state.QueueNode("A")
	state.QueueNode("B")
	state.QueueNode("C")
	// D stays pending (never queued)

	state.StartNode("A") // ignore error
	state.StartNode("B") // ignore error
	state.CompleteNode("A", "ok")
	state.FailNode("B", "err")
	state.SkipNode("C", "skipped")

	counts := state.CountByStatus()
	if counts[wf.NodeStatusCompleted] != 1 {
		t.Fatalf("expected 1 completed, got %d", counts[wf.NodeStatusCompleted])
	}
	if counts[wf.NodeStatusFailed] != 1 {
		t.Fatalf("expected 1 failed, got %d", counts[wf.NodeStatusFailed])
	}
	if counts[wf.NodeStatusSkipped] != 1 {
		t.Fatalf("expected 1 skipped, got %d", counts[wf.NodeStatusSkipped])
	}
	if counts[wf.NodeStatusPending] != 1 {
		t.Fatalf("expected 1 pending, got %d (statuses: %v)", counts[wf.NodeStatusPending], counts)
	}
}

func TestWorkflowState_Progress(t *testing.T) {
	state := wf.NewWorkflowState("test", []string{"A", "B"}, 1)

	if state.Progress() != 0 {
		t.Fatalf("expected 0%%, got %.1f%%", state.Progress())
	}

	state.QueueNode("A")
	state.QueueNode("B")
	state.StartNode("A") // ignore error
	state.CompleteNode("A", "ok")

	// 1/2 done
	if state.Progress() != 50.0 {
		t.Fatalf("expected 50%%, got %.1f%%", state.Progress())
	}

	state.StartNode("B") // ignore error
	state.FailNode("B", "err")

	// 2/2 done
	if state.Progress() != 100.0 {
		t.Fatalf("expected 100%%, got %.1f%%", state.Progress())
	}
}

func TestWorkflowState_AllNodesTerminal(t *testing.T) {
	state := wf.NewWorkflowState("test", []string{"A", "B"}, 1)

	if state.AllNodesTerminal() {
		t.Fatal("should not be all terminal yet")
	}

	state.QueueNode("A")
	state.QueueNode("B")
	state.StartNode("A") // ignore
	state.CompleteNode("A", "ok")

	if state.AllNodesTerminal() {
		t.Fatal("only A is done, B not started")
	}

	state.StartNode("B") // ignore
	state.CompleteNode("B", "ok")

	if !state.AllNodesTerminal() {
		t.Fatal("all should be terminal")
	}
}

func TestWorkflowState_Snapshot(t *testing.T) {
	state := wf.NewWorkflowState("test", []string{"A"}, 1)
	state.QueueNode("A")
	state.StartNode("A") // ignore
	state.CompleteNode("A", "hello")

	snap := state.Snapshot()
	if snap.Name != "test" {
		t.Fatalf("expected 'test', got %s", snap.Name)
	}
	ns, ok := snap.Nodes["A"]
	if !ok {
		t.Fatal("node A not in snapshot")
	}
	if ns.Status != wf.NodeStatusCompleted {
		t.Fatalf("expected completed, got %s", ns.Status)
	}

	// Snapshot is a copy: modifying state should not affect snapshot
	state.FailNode("A", "modified")
	if ns.Status != wf.NodeStatusCompleted {
		t.Fatalf("snapshot should not be affected by later mutation")
	}
}

func TestNodeStatus_IsTerminal(t *testing.T) {
	tests := []struct {
		status   wf.NodeStatus
		terminal bool
	}{
		{wf.NodeStatusPending, false},
		{wf.NodeStatusQueued, false},
		{wf.NodeStatusRunning, false},
		{wf.NodeStatusCompleted, true},
		{wf.NodeStatusFailed, true},
		{wf.NodeStatusSkipped, true},
		{wf.NodeStatusCanceled, true},
	}

	for _, tt := range tests {
		if tt.status.IsTerminal() != tt.terminal {
			t.Errorf("NodeStatus(%s).IsTerminal() = %v, want %v", tt.status, tt.status.IsTerminal(), tt.terminal)
		}
	}
}

func TestWorkflowValidation(t *testing.T) {
	// Test a valid workflow
	w := &wf.Workflow{
		Name:    "valid",
		Version: "1.0",
		Nodes: []*wf.Node{
			{ID: "A", Name: "A", SubagentType: "Explore"},
			{ID: "B", Name: "B", SubagentType: "Explore", DependsOn: []string{"A"}},
		},
	}
	if err := w.Validate(); err != nil {
		t.Errorf("valid workflow should not error: %v", err)
	}

	// Test NodeMap
	m := w.NodeMap()
	if len(m) != 2 || m["A"] == nil || m["B"] == nil {
		t.Errorf("NodeMap should contain both nodes")
	}
}
