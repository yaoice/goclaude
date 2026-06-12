package memory

import (
	"strings"
	"testing"
)

func TestTruncateEntrypointContent_NoTruncation(t *testing.T) {
	content := "Line 1\nLine 2\nLine 3"
	result := TruncateEntrypointContent(content)

	if result.WasLineTruncated {
		t.Error("should not be line truncated")
	}
	if result.WasByteTruncated {
		t.Error("should not be byte truncated")
	}
	if result.Content != content {
		t.Errorf("content should be unchanged")
	}
}

func TestTruncateEntrypointContent_LineTruncation(t *testing.T) {
	// 创建超过200行的内容
	lines := make([]string, 250)
	for i := range lines {
		lines[i] = "line content"
	}
	content := strings.Join(lines, "\n")

	result := TruncateEntrypointContent(content)

	if !result.WasLineTruncated {
		t.Error("should be line truncated")
	}
	if result.LineCount != MaxEntrypointLines {
		t.Errorf("expected %d lines, got %d", MaxEntrypointLines, result.LineCount)
	}
	if !strings.Contains(result.Content, "WARNING") {
		t.Error("should contain WARNING comment")
	}
}

func TestTruncateEntrypointContent_ByteTruncation(t *testing.T) {
	// 创建超过25KB的内容（少于200行）
	line := strings.Repeat("x", 300) // 300 bytes per line
	lines := make([]string, 100)     // 100 lines * 300 bytes = 30KB
	for i := range lines {
		lines[i] = line
	}
	content := strings.Join(lines, "\n")

	result := TruncateEntrypointContent(content)

	if !result.WasByteTruncated {
		t.Error("should be byte truncated")
	}
	if result.ByteCount > MaxEntrypointBytes {
		t.Errorf("expected %d bytes max, got %d", MaxEntrypointBytes, result.ByteCount)
	}
}

func TestTruncateEntrypointContent_BothTruncations(t *testing.T) {
	// 创建既超行又超字节的内容
	line := strings.Repeat("x", 200)
	lines := make([]string, 250)
	for i := range lines {
		lines[i] = line
	}
	content := strings.Join(lines, "\n")

	result := TruncateEntrypointContent(content)

	if !result.WasLineTruncated {
		t.Error("should be line truncated")
	}
	// 字节截断取决于行截断后的大小
	if !strings.Contains(result.Content, "WARNING") {
		t.Error("should contain WARNING")
	}
}

func TestParseMemoryType(t *testing.T) {
	tests := []struct {
		input    string
		expected MemoryType
	}{
		{"user", MemoryTypeUser},
		{"project", MemoryTypeProject},
		{"auto", MemoryTypeAuto},
		{"team", MemoryTypeTeam},
		{"invalid", ""},
	}

	for _, tt := range tests {
		result := ParseMemoryType(tt.input)
		if result != tt.expected {
			t.Errorf("ParseMemoryType(%s) = %s, want %s", tt.input, result, tt.expected)
		}
	}
}

func TestFormatFileSize(t *testing.T) {
	tests := []struct {
		bytes    int
		expected string
	}{
		{500, "500 B"},
		{1500, "1.5 KB"},
		{1500000, "1.4 MB"},
	}

	for _, tt := range tests {
		result := FormatFileSize(tt.bytes)
		if result != tt.expected {
			t.Errorf("FormatFileSize(%d) = %s, want %s", tt.bytes, result, tt.expected)
		}
	}
}
