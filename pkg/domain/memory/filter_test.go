package memory

import (
	"strings"
	"testing"
	"time"
)

// ============================================================
// Rule Engine: Match
// ============================================================

func TestFilterRule_Match_Keyword(t *testing.T) {
	rule := &FilterRule{MatchType: MatchKeyword, Pattern: "postgres", Action: ActionExclude}
	entry := &MemoryEntry{Title: "DB Config", Content: "We use PostgreSQL 15", Source: "auto_extract"}

	match, err := rule.Match(entry)
	if err != nil {
		t.Fatal(err)
	}
	if !match {
		t.Fatal("keyword should match")
	}

	match, _ = rule.Match(&MemoryEntry{Title: "Config", Content: "nothing here"})
	if match {
		t.Fatal("should not match")
	}
}

func TestFilterRule_Match_Regex(t *testing.T) {
	rule := &FilterRule{MatchType: MatchRegex, Pattern: `(?i)(password|secret)\s*[:=]\s*\S+`, Action: ActionExclude}
	entry := &MemoryEntry{Title: "DB", Content: "password: hunter2", Source: "auto_extract"}

	match, err := rule.Match(entry)
	if err != nil {
		t.Fatal(err)
	}
	if !match {
		t.Fatal("regex should match password pattern")
	}

	// 无凭证内容
	match, _ = rule.Match(&MemoryEntry{Title: "DB", Content: "uses PostgreSQL", Source: "auto_extract"})
	if match {
		t.Fatal("should not match without credentials")
	}
}

func TestFilterRule_Match_Category(t *testing.T) {
	rule := &FilterRule{MatchType: MatchCategory, Pattern: "project", Action: ActionBoost}
	entry := &MemoryEntry{Title: "X", Content: "X", Category: "project"}

	match, err := rule.Match(entry)
	if err != nil {
		t.Fatal(err)
	}
	if !match {
		t.Fatal("category should match")
	}
}

func TestFilterRule_Match_Tag(t *testing.T) {
	rule := &FilterRule{MatchType: MatchTag, Pattern: "security", Action: ActionTag, Tags: []string{"important"}}
	entry := &MemoryEntry{Title: "X", Content: "X", Tags: []string{"security"}}

	match, err := rule.Match(entry)
	if err != nil {
		t.Fatal(err)
	}
	if !match {
		t.Fatal("tag should match")
	}
}

func TestFilterRule_Match_Source(t *testing.T) {
	rule := &FilterRule{MatchType: MatchSource, Pattern: "user_directive", Action: ActionBoost, BoostBy: 20}
	entry := &MemoryEntry{Title: "X", Content: "X", Source: "user_directive"}

	match, err := rule.Match(entry)
	if err != nil {
		t.Fatal(err)
	}
	if !match {
		t.Fatal("source should match")
	}
}

// ============================================================
// Rule Engine: Apply Full Pipeline
// ============================================================

func TestRuleEngine_Exclude(t *testing.T) {
	engine := NewRuleEngine([]*FilterRule{
		{Name: "exclude-secrets", MatchType: MatchRegex, Pattern: `(?i)password\s*[:=]`, Action: ActionExclude},
	})

	entry := &MemoryEntry{Title: "Secret", Content: "password=admin123", Source: "agent_note"}
	keep, _, ruleName := engine.Apply(entry)

	if keep {
		t.Fatal("should exclude password")
	}
	if ruleName != "exclude-secrets" {
		t.Fatalf("rule name=%q", ruleName)
	}
}

func TestRuleEngine_Include(t *testing.T) {
	engine := NewRuleEngine([]*FilterRule{
		{Name: "force-keep", MatchType: MatchKeyword, Pattern: "critical", Action: ActionInclude},
		{Name: "exclude-all", MatchType: MatchKeyword, Pattern: "", Action: ActionExclude}, // 不匹配
	})

	entry := &MemoryEntry{Title: "Critical Fix", Content: "This is critical", Source: "agent_note"}
	keep, _, _ := engine.Apply(entry)

	if !keep {
		t.Fatal("should include critical entry")
	}
}

func TestRuleEngine_Boost(t *testing.T) {
	engine := NewRuleEngine([]*FilterRule{
		{Name: "boost-user", MatchType: MatchSource, Pattern: "user_directive", Action: ActionBoost, BoostBy: 25},
	})

	entry := &MemoryEntry{Title: "User choice", Content: "prefer Go", Source: "user_directive", Priority: 50}
	keep, modified, _ := engine.Apply(entry)

	if !keep {
		t.Fatal("should keep")
	}
	if modified.Priority != 75 {
		t.Fatalf("priority should be 75, got %d", modified.Priority)
	}
}

func TestRuleEngine_Demote(t *testing.T) {
	engine := NewRuleEngine([]*FilterRule{
		{Name: "demote-noise", MatchType: MatchRegex, Pattern: `TODO`, Action: ActionDemote, BoostBy: 30},
	})

	entry := &MemoryEntry{Title: "TODO item", Content: "TODO: fix this", Priority: 50}
	_, modified, _ := engine.Apply(entry)

	if modified.Priority != 20 {
		t.Fatalf("priority should be 20, got %d", modified.Priority)
	}
}

func TestRuleEngine_Tag(t *testing.T) {
	engine := NewRuleEngine([]*FilterRule{
		{Name: "tag-security-kw", MatchType: MatchKeyword, Pattern: "security", Action: ActionTag, Tags: []string{"security", "important"}},
	})

	entry := &MemoryEntry{Title: "Security Review", Content: "security audit results", Tags: []string{"existing"}}
	_, modified, _ := engine.Apply(entry)

	found := 0
	for _, tag := range modified.Tags {
		if tag == "security" || tag == "important" {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("tags should include security + important, got %v", modified.Tags)
	}
}

func TestBuiltInRules_SecretsExcluded(t *testing.T) {
	engine := NewRuleEngine(BuiltInRules())

	entries := []*MemoryEntry{
		{Title: "Secret", Content: "SECRET=abc123xyz", Source: "agent_note"},
		{Title: "OK", Content: "use PostgreSQL 15", Source: "agent_note"},
	}

	for _, e := range entries {
		keep, _, _ := engine.Apply(e)
		if strings.Contains(strings.ToLower(e.Title), "secret") && keep {
			t.Error("secret entry should be excluded")
		}
		if e.Title == "OK" && !keep {
			t.Error("normal entry should be kept")
		}
	}
}

// ============================================================
// MemoryEntry: TotalScore
// ============================================================

func TestMemoryEntry_TotalScore(t *testing.T) {
	now := time.Now()

	highPriority := &MemoryEntry{
		Priority:  80,
		Relevance: 0.8,
		UpdatedAt: now,
	}
	lowPriority := &MemoryEntry{
		Priority:  20,
		Relevance: 0.2,
		UpdatedAt: now.Add(-100 * 24 * time.Hour), // 100 days old
	}

	if highPriority.TotalScore(now) <= lowPriority.TotalScore(now) {
		t.Fatal("high priority entry should score higher than old low priority")
	}
}

// ============================================================
// AssignPriority
// ============================================================

func TestAssignPriority(t *testing.T) {
	userDirective := &MemoryEntry{Source: "user_directive", Category: "project"}
	if p := AssignPriority(userDirective); p != 90 {
		t.Fatalf("user_directive+project should be 90, got %d", p)
	}

	autoExtract := &MemoryEntry{Source: "auto_extract"}
	if p := AssignPriority(autoExtract); p != 30 {
		t.Fatalf("auto_extract should be 30, got %d", p)
	}
}
