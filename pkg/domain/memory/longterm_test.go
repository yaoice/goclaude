package memory

import (
	"testing"
	"time"
)

func TestLongTermMemory_IsExpired(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

	m := &LongTermMemory{ExpiresAt: time.Time{}}
	if m.IsExpired(now) {
		t.Error("zero ExpiresAt should mean never expired")
	}

	m2 := &LongTermMemory{ExpiresAt: now.Add(-1 * time.Hour)}
	if !m2.IsExpired(now) {
		t.Error("past ExpiresAt should be expired")
	}

	m3 := &LongTermMemory{ExpiresAt: now.Add(24 * time.Hour)}
	if m3.IsExpired(now) {
		t.Error("future ExpiresAt should not be expired")
	}

	m4 := &LongTermMemory{ExpiresAt: now}
	if m4.IsExpired(now) {
		t.Error("equal time should not be expired (After, not BeforeOrEqual)")
	}
}

func TestLongTermMemory_TotalScore(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

	highP := &LongTermMemory{Priority: 100, CreatedAt: now, AccessCount: 10}
	if highP.TotalScore(now) < 0.5 {
		t.Errorf("fresh high-priority score too low: %f", highP.TotalScore(now))
	}

	old := &LongTermMemory{Priority: 10, CreatedAt: now.Add(-365 * 24 * time.Hour), AccessCount: 0}
	if old.TotalScore(now) > 0.35 {
		t.Errorf("old low-priority score too high: %f", old.TotalScore(now))
	}

	lowAcc := &LongTermMemory{Priority: 50, CreatedAt: now.Add(-30 * 24 * time.Hour), AccessCount: 0}
	highAcc := &LongTermMemory{Priority: 50, CreatedAt: now.Add(-30 * 24 * time.Hour), AccessCount: 100}
	if highAcc.TotalScore(now) <= lowAcc.TotalScore(now) {
		t.Error("higher access count should give higher score")
	}

	recent := &LongTermMemory{Priority: 50, CreatedAt: now, AccessCount: 0}
	old2 := &LongTermMemory{Priority: 50, CreatedAt: now.Add(-90 * 24 * time.Hour), AccessCount: 0}
	if recent.TotalScore(now) <= old2.TotalScore(now) {
		t.Error("recent item should score higher than old")
	}
}

func TestLongTermMemory_Fields(t *testing.T) {
	now := time.Now()
	m := &LongTermMemory{
		ID:           1,
		SessionID:    "session-abc",
		Type:         "observation",
		Title:        "Test",
		Content:      "Content",
		Category:     "project",
		Source:       "tool_use",
		ToolName:     "read_file",
		Priority:     75,
		Tags:         "tag1,tag2",
		CreatedAt:    now,
		ByteSize:     100,
		AccessCount:  3,
		LastAccessed: now,
	}
	if m.ID != 1 {
		t.Errorf("ID = %d, want 1", m.ID)
	}
	if m.Type != "observation" {
		t.Errorf("Type = %s, want observation", m.Type)
	}
	if m.Priority != 75 {
		t.Errorf("Priority = %d, want 75", m.Priority)
	}
}
