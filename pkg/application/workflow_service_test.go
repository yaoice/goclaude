package application_test

import (
	"testing"

	"github.com/yaoice/goclaude/pkg/application"
	wf "github.com/yaoice/goclaude/pkg/domain/workflow"
)

func TestParseAndValidate_LinearDependency(t *testing.T) {
	svc := application.NewWorkflowService(nil, nil, application.WorkflowDefaults{}, nil)
	workflow := &wf.Workflow{
		Name:        "linear-test",
		Description: "simple linear dependency chain",
		Nodes: []*wf.Node{
			{ID: "A", Name: "Task A", SubagentType: "Explore", DependsOn: []string{}},
			{ID: "B", Name: "Task B", SubagentType: "Explore", DependsOn: []string{"A"}},
			{ID: "C", Name: "Task C", SubagentType: "Explore", DependsOn: []string{"B"}},
		},
	}

	plan, err := svc.ParseAndValidate(workflow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Waves) != 3 {
		t.Fatalf("expected 3 waves, got %d", len(plan.Waves))
	}
	if len(plan.Waves[0]) != 1 || plan.Waves[0][0] != "A" {
		t.Fatalf("wave 0 should be [A], got %v", plan.Waves[0])
	}
	if len(plan.Waves[1]) != 1 || plan.Waves[1][0] != "B" {
		t.Fatalf("wave 1 should be [B], got %v", plan.Waves[1])
	}
	if len(plan.Waves[2]) != 1 || plan.Waves[2][0] != "C" {
		t.Fatalf("wave 2 should be [C], got %v", plan.Waves[2])
	}
	// Critical path should be A → B → C
	if len(plan.CriticalPath) != 3 {
		t.Fatalf("critical path should have 3 nodes, got %d", len(plan.CriticalPath))
	}
}

func TestParseAndValidate_ParallelWaves(t *testing.T) {
	svc := application.NewWorkflowService(nil, nil, application.WorkflowDefaults{}, nil)
	workflow := &wf.Workflow{
		Name: "parallel-test",
		Nodes: []*wf.Node{
			{ID: "A", Name: "Task A", SubagentType: "Explore", DependsOn: []string{}},
			{ID: "B", Name: "Task B", SubagentType: "Explore", DependsOn: []string{}},
			{ID: "C", Name: "Task C", SubagentType: "Explore", DependsOn: []string{"A"}},
			{ID: "D", Name: "Task D", SubagentType: "Explore", DependsOn: []string{"B"}},
			{ID: "E", Name: "Task E", SubagentType: "Explore", DependsOn: []string{"C", "D"}},
		},
	}

	plan, err := svc.ParseAndValidate(workflow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Wave 0: A, B (both no deps, can run in parallel)
	// Wave 1: C, D (C depends on A, D depends on B)
	// Wave 2: E (depends on C and D)
	if len(plan.Waves) != 3 {
		t.Fatalf("expected 3 waves, got %d", len(plan.Waves))
	}
	if len(plan.Waves[0]) != 2 {
		t.Fatalf("wave 0 should have 2 nodes, got %d", len(plan.Waves[0]))
	}
	if len(plan.Waves[1]) != 2 {
		t.Fatalf("wave 1 should have 2 nodes, got %d", len(plan.Waves[1]))
	}
	if len(plan.Waves[2]) != 1 || plan.Waves[2][0] != "E" {
		t.Fatalf("wave 2 should be [E], got %v", plan.Waves[2])
	}
}

func TestParseAndValidate_CircularDependency(t *testing.T) {
	svc := application.NewWorkflowService(nil, nil, application.WorkflowDefaults{}, nil)
	workflow := &wf.Workflow{
		Name: "cycle-test",
		Nodes: []*wf.Node{
			{ID: "A", Name: "Task A", SubagentType: "Explore", DependsOn: []string{"B"}},
			{ID: "B", Name: "Task B", SubagentType: "Explore", DependsOn: []string{"A"}},
		},
	}

	_, err := svc.ParseAndValidate(workflow)
	if err == nil {
		t.Fatal("expected error for circular dependency, got nil")
	}
}

func TestParseAndValidate_DiamondDependency(t *testing.T) {
	svc := application.NewWorkflowService(nil, nil, application.WorkflowDefaults{}, nil)
	// Diamond: A → B, A → C, B → D, C → D
	workflow := &wf.Workflow{
		Name: "diamond-test",
		Nodes: []*wf.Node{
			{ID: "A", Name: "A", SubagentType: "Explore", DependsOn: []string{}},
			{ID: "B", Name: "B", SubagentType: "Explore", DependsOn: []string{"A"}},
			{ID: "C", Name: "C", SubagentType: "Explore", DependsOn: []string{"A"}},
			{ID: "D", Name: "D", SubagentType: "Explore", DependsOn: []string{"B", "C"}},
		},
	}

	plan, err := svc.ParseAndValidate(workflow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.Waves) != 3 {
		t.Fatalf("expected 3 waves, got %d", len(plan.Waves))
	}
	// Wave 0: A
	// Wave 1: B, C
	// Wave 2: D
	if len(plan.Waves[0]) != 1 {
		t.Fatalf("wave 0 should have 1 node, got %d", len(plan.Waves[0]))
	}
	if len(plan.Waves[1]) != 2 {
		t.Fatalf("wave 1 should have 2 nodes, got %d", len(plan.Waves[1]))
	}
	if len(plan.Waves[2]) != 1 {
		t.Fatalf("wave 2 should have 1 node, got %d", len(plan.Waves[2]))
	}
}

func TestValidation_EmptyName(t *testing.T) {
	workflow := &wf.Workflow{
		Name: "",
		Nodes: []*wf.Node{
			{ID: "A", Name: "A", SubagentType: "Explore"},
		},
	}
	err := workflow.Validate()
	if err == nil {
		t.Fatal("expected validation error for empty name")
	}
}

func TestValidation_EmptyNodes(t *testing.T) {
	workflow := &wf.Workflow{
		Name:  "test",
		Nodes: []*wf.Node{},
	}
	err := workflow.Validate()
	if err == nil {
		t.Fatal("expected validation error for empty nodes")
	}
}

func TestValidation_DuplicateNodeID(t *testing.T) {
	workflow := &wf.Workflow{
		Name: "test",
		Nodes: []*wf.Node{
			{ID: "A", Name: "A", SubagentType: "Explore"},
			{ID: "A", Name: "A2", SubagentType: "Explore"},
		},
	}
	err := workflow.Validate()
	if err == nil {
		t.Fatal("expected validation error for duplicate node id")
	}
}

func TestValidation_SelfDependency(t *testing.T) {
	workflow := &wf.Workflow{
		Name: "test",
		Nodes: []*wf.Node{
			{ID: "A", Name: "A", SubagentType: "Explore", DependsOn: []string{"A"}},
		},
	}
	err := workflow.Validate()
	if err == nil {
		t.Fatal("expected validation error for self-dependency")
	}
}

func TestValidation_MissingDependency(t *testing.T) {
	workflow := &wf.Workflow{
		Name: "test",
		Nodes: []*wf.Node{
			{ID: "A", Name: "A", SubagentType: "Explore", DependsOn: []string{"Z"}},
		},
	}
	err := workflow.Validate()
	if err == nil {
		t.Fatal("expected validation error for missing dependency")
	}
}
