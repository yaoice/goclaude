// Package memory 实现记忆提示词构建
// 参考：src/memdir/memdir.ts
package memory

import (
	"context"
	"fmt"
	"strings"
)

const (
	DirExistsGuidance = "This directory already exists — write to it directly with the Write tool (do not run mkdir or check for its existence)."
	DirsExistGuidance = "Both directories already exist — write to them directly with the Write tool (do not run mkdir or check for their existence)."
)

func BuildMemoryLines(displayName string, memoryDir string, extraGuidelines []string, skipIndex bool) string {
	var lines []string
	lines = append(lines, "# "+displayName)
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("You have a persistent, file-based memory system at `%s`. %s", memoryDir, DirExistsGuidance))
	lines = append(lines, "")
	lines = append(lines, "## What to save in memories")
	lines = append(lines, "- `user` — Facts about the user")
	lines = append(lines, "- `feedback` — Corrections from the user")
	lines = append(lines, "- `project` — Context about this project")
	lines = append(lines, "- `reference` — Pointers to external systems")
	lines = append(lines, "")
	lines = append(lines, "## How to save memories")
	lines = append(lines, "Write each memory to its own file with frontmatter.")
	lines = append(lines, "")
	return strings.Join(lines, "\n")
}

func BuildMemoryPrompt(displayName string, memoryDir string, extraGuidelines []string) string {
	return BuildMemoryLines("auto memory", memoryDir, extraGuidelines, false)
}

func BuildAssistantDailyLogPrompt(autoMemDir string, skipIndex bool) string {
	return "# auto memory\n\nDaily log mode."
}

func BuildSearchingPastContextSection(autoMemDir string) string {
	return "## Searching past context\n\nUse grep to search."
}

func EnsureMemoryDirExists(ctx context.Context, repo Repository, memoryDir string) error {
	return repo.MkdirAll(ctx, memoryDir)
}

func LoadMemoryPrompt(ctx context.Context, repo Repository, autoMemDir string, skipIndex bool) (string, error) {
	if !IsAutoMemoryEnabled() {
		return "", nil
	}
	err := EnsureMemoryDirExists(ctx, repo, autoMemDir)
	if err != nil {
		return "", err
	}
	return BuildMemoryLines("auto memory", autoMemDir, nil, skipIndex), nil
}
