package application_test

import (
	"testing"

	"github.com/anthropics/goclaude/pkg/application"
	"github.com/anthropics/goclaude/pkg/domain/team"
	teamfs "github.com/anthropics/goclaude/pkg/infrastructure/team"
)

// =============================================================================
// Plan-then-Execute Architecture Tests
// =============================================================================

func TestPlanThenExecute_FullLifecycle(t *testing.T) {
	svc := newTestSvc(t)
	teamName := "test-plan-execute"
	objective := "Build a user authentication system with login and registration"

	// === Phase 0: Create team + members ===
	_, err := svc.CreateTeam(application.CreateTeamInput{
		Name:          teamName,
		Description:   "Auth system project",
		LeadAgentType: "architect",
		LeadCwd:       ".",
	})
	if err != nil {
		t.Fatal("create team:", err)
	}

	// Verify team starts in Planning Phase
	f, err := svc.GetTeam(teamName)
	if err != nil {
		t.Fatal("get team:", err)
	}
	if f.Phase != team.PhasePlanning {
		t.Errorf("expected Phase=planning, got %q", f.Phase)
	}

	// Add members
	for _, m := range []struct{ name, atype string }{
		{"alice", "frontend-dev"},
		{"bob", "backend-dev"},
	} {
		_, _, err := svc.JoinTeam(application.JoinTeamInput{
			TeamName:  teamName,
			AgentName: m.name,
			AgentType: m.atype,
			Cwd:       ".",
		})
		if err != nil {
			t.Fatalf("join team (%s): %v", m.name, err)
		}
	}

	// === Phase 1: Initiate Planning ===
	t.Run("InitiatePlanning", func(t *testing.T) {
		f, err := svc.InitiatePlanning(application.InitiatePlanningInput{
			TeamName:  teamName,
			Objective: objective,
		})
		if err != nil {
			t.Fatal("initiate planning:", err)
		}
		if f.Phase != team.PhasePlanning {
			t.Errorf("expected Phase=planning, got %q", f.Phase)
		}
		if f.Plan == nil {
			t.Fatal("expected plan to be created")
		}
		if f.Plan.Objective != objective {
			t.Errorf("expected objective %q, got %q", objective, f.Plan.Objective)
		}
		if f.Plan.Status != team.PlanDrafting {
			t.Errorf("expected PlanDrafting, got %q", f.Plan.Status)
		}
	})

	// === Phase 2: Collect Proposals ===
	t.Run("CollectProposal", func(t *testing.T) {
		// Alice submits auth UI tasks
		aliceProposal := team.PlanProposal{
			Proposer:  "alice",
			Rationale: "Focus on frontend auth components",
			Tasks: []team.PlannedTask{
				{ID: "plan-task-login-ui", Title: "Build login form UI", Description: "React login form with validation", ProposedBy: "alice", EstimatedComplexity: "medium"},
				{ID: "plan-task-reg-ui", Title: "Build registration form UI", Description: "Registration form with field validation", ProposedBy: "alice", DependsOn: []string{"plan-task-login-ui"}, EstimatedComplexity: "medium"},
			},
		}
		if err := svc.CollectProposal(teamName, "alice", aliceProposal); err != nil {
			t.Fatal("alice proposal:", err)
		}

		// Bob submits backend auth tasks
		bobProposal := team.PlanProposal{
			Proposer:  "bob",
			Rationale: "Focus on backend auth API",
			Tasks: []team.PlannedTask{
				{ID: "plan-task-auth-api", Title: "Build auth API endpoints", Description: "JWT-based auth endpoints", ProposedBy: "bob", EstimatedComplexity: "high"},
				{ID: "plan-task-db-schema", Title: "Create user DB schema", Description: "Migration for users table", ProposedBy: "bob", EstimatedComplexity: "low"},
				{ID: "plan-task-integrate", Title: "Integrate frontend with backend", Description: "Wire up login/reg to API", ProposedBy: "bob", DependsOn: []string{"plan-task-login-ui", "plan-task-auth-api"}, EstimatedComplexity: "medium"},
			},
		}
		if err := svc.CollectProposal(teamName, "bob", bobProposal); err != nil {
			t.Fatal("bob proposal:", err)
		}

		// Verify plan has all tasks
		plan, err := svc.GetExecutionPlan(teamName)
		if err != nil {
			t.Fatal("get plan:", err)
		}
		if len(plan.Tasks) != 5 {
			t.Errorf("expected 5 tasks, got %d", len(plan.Tasks))
		}
	})

	// === Phase 3: Approve Plan ===
	t.Run("ApprovePlan", func(t *testing.T) {
		f, err := svc.ApprovePlan(application.ApprovePlanInput{
			TeamName: teamName,
			Assignments: []team.PlanAssignment{
				{TaskID: "plan-task-login-ui", MemberName: "alice", Role: "frontend"},
				{TaskID: "plan-task-reg-ui", MemberName: "alice", Role: "frontend"},
				{TaskID: "plan-task-auth-api", MemberName: "bob", Role: "backend"},
				{TaskID: "plan-task-db-schema", MemberName: "bob", Role: "backend"},
				{TaskID: "plan-task-integrate", MemberName: "bob", Role: "fullstack"},
			},
		})
		if err != nil {
			t.Fatal("approve plan:", err)
		}

		// Verify phase transition
		if f.Phase != team.PhaseExecuting {
			t.Errorf("expected Phase=executing, got %q", f.Phase)
		}
		if f.Plan.Status != team.PlanApproved {
			t.Errorf("expected PlanApproved, got %q", f.Plan.Status)
		}
		if f.Plan.ApprovedBy != team.LeaderName {
			t.Errorf("expected ApprovedBy=team-lead, got %q", f.Plan.ApprovedBy)
		}

		// Verify shared tasks were exported
		if len(f.Tasks) != 5 {
			t.Errorf("expected 5 shared tasks, got %d", len(f.Tasks))
		}
	})

	// === Phase 4: Execution gate ===
	t.Run("ExecutionPhaseGate", func(t *testing.T) {
		allowed, err := svc.IsExecutionAllowed(teamName)
		if err != nil {
			t.Fatal("check execution:", err)
		}
		if !allowed {
			t.Error("expected execution to be allowed in Executing Phase")
		}
	})

	// === Phase 5: Re-plan on failure ===
	t.Run("Replan", func(t *testing.T) {
		f, err := svc.InitiateReplan(application.InitiateReplanInput{
			TeamName:      teamName,
			FailedTaskID:  "plan-task-auth-api",
			FailedMember:  "bob",
			FailureReason: "JWT library incompatible with Go version",
		})
		if err != nil {
			t.Fatal("initiate replan:", err)
		}

		// Verify re-plan transition
		if f.Phase != team.PhasePlanning {
			t.Errorf("expected Phase=planning after replan, got %q", f.Phase)
		}
		if f.ReplanCount != 1 {
			t.Errorf("expected ReplanCount=1, got %d", f.ReplanCount)
		}
		if f.Plan.Status != team.PlanDrafting {
			t.Errorf("expected PlanDrafting after replan, got %q", f.Plan.Status)
		}

		// Verify the failed task is blocked
		task, err := svc.GetTask(teamName, "plan-task-auth-api")
		if err != nil {
			t.Fatal("get task:", err)
		}
		if task.Status != team.SharedTaskBlocked {
			t.Errorf("expected failed task to be blocked, got %q", task.Status)
		}
	})
}

// =============================================================================
// Plan Validation Tests
// =============================================================================

func TestExecutionPlan_Validate(t *testing.T) {
	members := []string{"alice", "bob"}

	t.Run("ValidPlan", func(t *testing.T) {
		plan := &team.ExecutionPlan{
			Objective: "Build feature X",
			Tasks: []team.PlannedTask{
				{ID: "t1", Title: "Task 1", ProposedBy: "alice"},
				{ID: "t2", Title: "Task 2", ProposedBy: "bob"},
			},
			Assignments: []team.PlanAssignment{
				{TaskID: "t1", MemberName: "alice"},
				{TaskID: "t2", MemberName: "bob"},
			},
		}
		if err := plan.Validate(members); err != nil {
			t.Errorf("expected valid, got: %v", err)
		}
	})

	t.Run("MissingObjective", func(t *testing.T) {
		plan := &team.ExecutionPlan{
			Tasks: []team.PlannedTask{{ID: "t1", Title: "Task 1"}},
		}
		if err := plan.Validate(members); err == nil {
			t.Error("expected error for missing objective")
		}
	})

	t.Run("EmptyTasks", func(t *testing.T) {
		plan := &team.ExecutionPlan{
			Objective: "Build something",
			Tasks:     []team.PlannedTask{},
		}
		if err := plan.Validate(members); err == nil {
			t.Error("expected error for empty tasks")
		}
	})

	t.Run("TaskWithEmptyTitle", func(t *testing.T) {
		plan := &team.ExecutionPlan{
			Objective: "Build something",
			Tasks:     []team.PlannedTask{{ID: "t1", Title: ""}},
		}
		if err := plan.Validate(members); err == nil {
			t.Error("expected error for empty task title")
		}
	})

	t.Run("UnknownAssignmentTask", func(t *testing.T) {
		plan := &team.ExecutionPlan{
			Objective: "Build something",
			Tasks:     []team.PlannedTask{{ID: "t1", Title: "Task 1"}},
			Assignments: []team.PlanAssignment{
				{TaskID: "t-nonexistent", MemberName: "alice"},
			},
		}
		if err := plan.Validate(members); err == nil {
			t.Error("expected error for unknown assignment task")
		}
	})

	t.Run("UnknownDependency", func(t *testing.T) {
		plan := &team.ExecutionPlan{
			Objective: "Build something",
			Tasks: []team.PlannedTask{
				{ID: "t1", Title: "Task 1", DependsOn: []string{"t-nonexistent"}},
			},
		}
		if err := plan.Validate(members); err == nil {
			t.Error("expected error for unknown dependency")
		}
	})

	t.Run("CircularDependency", func(t *testing.T) {
		plan := &team.ExecutionPlan{
			Objective: "Build something",
			Tasks: []team.PlannedTask{
				{ID: "t1", Title: "Task 1", DependsOn: []string{"t2"}},
				{ID: "t2", Title: "Task 2", DependsOn: []string{"t1"}},
			},
		}
		if err := plan.Validate(members); err == nil {
			t.Error("expected error for circular dependency")
		}
	})
}

// =============================================================================
// Phase Transition Tests
// =============================================================================

func TestTeamPhase_CanTransitionTo(t *testing.T) {
	tests := []struct {
		from   team.TeamPhase
		to     team.TeamPhase
		expect bool
	}{
		{team.PhasePlanning, team.PhaseExecuting, true},
		{team.PhasePlanning, team.PhaseFailed, true},
		{team.PhasePlanning, team.PhaseCompleted, false},
		{team.PhasePlanning, team.PhaseReplanning, false},
		{team.PhaseExecuting, team.PhaseCompleted, true},
		{team.PhaseExecuting, team.PhaseFailed, true},
		{team.PhaseExecuting, team.PhaseReplanning, true},
		{team.PhaseExecuting, team.PhasePlanning, false},
		{team.PhaseReplanning, team.PhasePlanning, true},
		{team.PhaseReplanning, team.PhaseExecuting, false},
		{team.PhaseCompleted, team.PhaseExecuting, false},
		{team.PhaseFailed, team.PhasePlanning, false},
	}
	for _, tt := range tests {
		got := tt.from.CanTransitionTo(tt.to)
		if got != tt.expect {
			t.Errorf("%q → %q: got %v, want %v", tt.from, tt.to, got, tt.expect)
		}
	}
}

func TestTeamPhase_ExecutionAllowed(t *testing.T) {
	if team.PhasePlanning.ExecutionAllowed() {
		t.Error("planning phase should not allow execution")
	}
	if !team.PhaseExecuting.ExecutionAllowed() {
		t.Error("executing phase should allow execution")
	}
	if team.PhaseReplanning.ExecutionAllowed() {
		t.Error("replanning phase should not allow execution")
	}
}

func TestTeamPhase_PlanningAllowed(t *testing.T) {
	if !team.PhasePlanning.PlanningAllowed() {
		t.Error("planning phase should allow planning")
	}
	if team.PhaseExecuting.PlanningAllowed() {
		t.Error("executing phase should not allow planning")
	}
	if !team.PhaseReplanning.PlanningAllowed() {
		t.Error("replanning phase should allow planning")
	}
}

// =============================================================================
// Phase Gate Tests
// =============================================================================

func TestPlanValidationSummary_AllAssigned(t *testing.T) {
	plan := &team.ExecutionPlan{
		Objective: "Test",
		Tasks: []team.PlannedTask{
			{ID: "t1", Title: "T1"},
			{ID: "t2", Title: "T2"},
		},
		Assignments: []team.PlanAssignment{
			{TaskID: "t1", MemberName: "alice"},
			{TaskID: "t2", MemberName: "bob"},
		},
	}
	summary := team.SummarizeValidation(plan, []string{"alice", "bob"})
	if !summary.Valid {
		t.Error("expected valid summary")
	}
	if len(summary.UnassignedTasks) != 0 {
		t.Errorf("expected 0 unassigned, got %d", len(summary.UnassignedTasks))
	}
}

func TestPlanValidationSummary_UnassignedTasks(t *testing.T) {
	plan := &team.ExecutionPlan{
		Objective: "Test",
		Tasks: []team.PlannedTask{
			{ID: "t1", Title: "T1"},
			{ID: "t2", Title: "T2"},
		},
		Assignments: []team.PlanAssignment{
			{TaskID: "t1", MemberName: "alice"},
		},
	}
	summary := team.SummarizeValidation(plan, []string{"alice"})
	if len(summary.UnassignedTasks) != 1 {
		t.Errorf("expected 1 unassigned, got %d: %v", len(summary.UnassignedTasks), summary.UnassignedTasks)
	}
}

func TestRejectPlan_Twice(t *testing.T) {
	svc := newTestSvc(t)
	teamName := "test-reject-plan"

	// Create + plan
	_, _ = svc.CreateTeam(application.CreateTeamInput{
		Name: teamName, LeadAgentType: "leader", LeadCwd: ".",
	})
	_, _, _ = svc.JoinTeam(application.JoinTeamInput{TeamName: teamName, AgentName: "alice", AgentType: "dev"})

	_, err := svc.InitiatePlanning(application.InitiatePlanningInput{
		TeamName: teamName, Objective: "Test",
	})
	if err != nil {
		t.Fatal("initiate planning:", err)
	}

	// Reject first time
	_, err = svc.RejectPlan(application.RejectPlanInput{
		TeamName: teamName, Reason: "Not enough tasks",
	})
	if err != nil {
		t.Fatal("first reject:", err)
	}

	// Verify still in Planning
	f, _ := svc.GetTeam(teamName)
	if f.Phase != team.PhasePlanning {
		t.Errorf("expected Phase=planning after reject, got %q", f.Phase)
	}

	// Submit proposals after rejection
	err = svc.CollectProposal(teamName, "alice", team.PlanProposal{
		Proposer: "alice",
		Tasks:    []team.PlannedTask{{ID: "t1", Title: "More tasks"}},
	})
	if err != nil {
		t.Fatal("collect after reject:", err)
	}

	// Approve the plan this time
	f, err = svc.ApprovePlan(application.ApprovePlanInput{
		TeamName:    teamName,
		Assignments: []team.PlanAssignment{{TaskID: "t1", MemberName: "alice"}},
	})
	if err != nil {
		t.Fatal("approve after revise:", err)
	}
	if f.Phase != team.PhaseExecuting {
		t.Errorf("expected Phase=executing after approve, got %q", f.Phase)
	}
}

func TestMaxReplansExceeded(t *testing.T) {
	svc := newTestSvc(t)
	teamName := "test-max-replans"

	// Create + plan + approve (default MaxReplanAttempts=3)
	_, _ = svc.CreateTeam(application.CreateTeamInput{
		Name: teamName, LeadAgentType: "leader", LeadCwd: ".",
	})
	_, _, _ = svc.JoinTeam(application.JoinTeamInput{TeamName: teamName, AgentName: "alice", AgentType: "dev"})
	_, _ = svc.InitiatePlanning(application.InitiatePlanningInput{TeamName: teamName, Objective: "Test"})
	_ = svc.CollectProposal(teamName, "alice", team.PlanProposal{
		Proposer: "alice",
		Tasks:    []team.PlannedTask{{ID: "t1", Title: "T1"}},
	})
	_, _ = svc.ApprovePlan(application.ApprovePlanInput{
		TeamName:    teamName,
		Assignments: []team.PlanAssignment{{TaskID: "t1", MemberName: "alice"}},
	})

	// First replan should succeed
	f, err := svc.InitiateReplan(application.InitiateReplanInput{
		TeamName:      teamName,
		FailedTaskID:  "t1",
		FailedMember:  "alice",
		FailureReason: "First failure",
	})
	if err != nil {
		t.Fatal("first replan should succeed:", err)
	}
	if f.ReplanCount != 1 {
		t.Errorf("expected ReplanCount=1, got %d", f.ReplanCount)
	}

	// Re-approve
	_ = svc.CollectProposal(teamName, "alice", team.PlanProposal{
		Proposer: "alice",
		Tasks:    []team.PlannedTask{{ID: "t1-revised", Title: "T1 revised"}},
	})
	_, _ = svc.ApprovePlan(application.ApprovePlanInput{
		TeamName:    teamName,
		Assignments: []team.PlanAssignment{{TaskID: "t1-revised", MemberName: "alice"}},
	})

	// Second replan should also succeed (default max is 3)
	f, err = svc.InitiateReplan(application.InitiateReplanInput{
		TeamName:      teamName,
		FailedTaskID:  "t1-revised",
		FailedMember:  "alice",
		FailureReason: "Second failure",
	})
	if err != nil {
		t.Fatal("second replan should succeed:", err)
	}
	if f.ReplanCount != 2 {
		t.Errorf("expected ReplanCount=2, got %d", f.ReplanCount)
	}
}

// =============================================================================
// Helpers
// =============================================================================

func newTestSvc(t *testing.T) *application.TeamService {
	t.Helper()
	dir := t.TempDir()
	l := teamfs.Layout{
		HomeDir: dir,
	}
	return application.NewTeamServiceWithLayout(l)
}
