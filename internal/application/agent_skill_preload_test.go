package application

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/anthropics/goclaude/internal/domain/agent"
	"github.com/anthropics/goclaude/internal/domain/query"
	"github.com/anthropics/goclaude/internal/domain/skill"
	"github.com/anthropics/goclaude/internal/domain/tool"
)

func TestAgentService_Run_PreloadsDefinitionSkills(t *testing.T) {
	skills := NewSkillService(slog.Default())
	skills.RegisterBundled(&skill.Skill{
		Name:      "guide",
		Content:   "Always mention the loaded guide.",
		IsEnabled: true,
	})

	svc := NewAgentService(slog.Default())
	svc.EnableSkills(skills)
	svc.Registry().Register(&agent.Definition{
		AgentType:    "worker",
		WhenToUse:    "test worker",
		Skills:       []string{"guide"},
		SystemPrompt: "worker system",
		Source:       agent.SourceBuiltIn,
	})

	prov := &scriptedProvider{turns: []scriptTurn{{text: "done"}}}
	factory := NewDefaultAgentEngineFactory(tool.NewRegistry(), prov, query.NewTokenBudget(100000, 0.8), slog.Default())
	_, err := svc.Run(context.Background(), "worker", factory, RunOptions{Prompt: "start", DefaultModel: "test"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(prov.seenMessagesAtTurn) == 0 {
		t.Fatal("provider did not receive messages")
	}
	joined := ""
	for _, m := range prov.seenMessagesAtTurn[0] {
		joined += m.GetTextContent() + "\n"
	}
	if !strings.Contains(joined, "<skill name=\"guide\">") || !strings.Contains(joined, "loaded guide") {
		t.Fatalf("definition skill was not preloaded into subagent messages: %q", joined)
	}
}
