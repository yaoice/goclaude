package shell

import "testing"

func TestClassifyToolPhase(t *testing.T) {
	cases := map[string]string{
		"team_create":       phaseTeamSetup,
		"auto_setup_team":   phaseTeamSetup,
		"parse_team_intent": phaseTeamSetup,
		"send_message":      phaseTaskAssign,
		"read_inbox":        phaseCoordination,
		"write":             phaseFileWrite,
		"edit":              phaseFileWrite,
		"bash":              phaseCodeGen,
		"read_file":         phaseExploration,
		"some_unknown_tool": phaseWorking,
		"":                  "",
		"  BASH  ":          phaseCodeGen,
	}
	for in, want := range cases {
		if got := classifyToolPhase(in); got != want {
			t.Errorf("classifyToolPhase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsNoisyCoordinationTool(t *testing.T) {
	for _, n := range []string{"parse_team_intent", "auto_setup_team", "read_inbox", "ReadInbox"} {
		if !isNoisyCoordinationTool(n) {
			t.Errorf("expected %q to be noisy", n)
		}
	}
	for _, n := range []string{"bash", "write", "team_create", ""} {
		if isNoisyCoordinationTool(n) {
			t.Errorf("expected %q to NOT be noisy", n)
		}
	}
}

// 每个阶段只在首次出现时报告 isFirst=true，后续（含阶段间往返）一律折叠。
func TestSubagentTracker_PhaseSeenOnce(t *testing.T) {
	tr := newSubagentTracker("coder", "sonnet")
	if p, first := tr.observe("bash"); p != phaseCodeGen || !first {
		t.Fatalf("first bash should be new phase, got %q first=%v", p, first)
	}
	if _, first := tr.observe("bash"); first {
		t.Fatal("second bash (same phase) must NOT report first")
	}
	if p, first := tr.observe("write"); p != phaseFileWrite || !first {
		t.Fatalf("write should be new phase, got %q first=%v", p, first)
	}
	if _, first := tr.observe("bash"); first {
		t.Fatal("returning to a seen phase must NOT report first")
	}
	if len(tr.order) != 2 {
		t.Fatalf("expected 2 distinct phases, got %d: %v", len(tr.order), tr.order)
	}
}

func TestSubagentTracker_IgnoresEmptyTool(t *testing.T) {
	tr := newSubagentTracker("x", "")
	if p, first := tr.observe(""); p != "" || first {
		t.Fatalf("empty tool should not enter a phase, got %q first=%v", p, first)
	}
	if tr.hasPhases() {
		t.Fatal("tracker should have no phases after empty observe")
	}
}
