package sqlite

import (
	"strings"
	"testing"
)

func TestSanitizeFTSQuery(t *testing.T) {
	tests := []struct {
		input string
		hasOR bool
	}{
		{"Go backend development", true},
		{"simple", false},
		{"", false},
		{"(parens) ^boost wildcard", true},
	}

	for _, tt := range tests {
		result := sanitizeFTSQuery(tt.input)
		if tt.hasOR && !strings.Contains(result, " OR ") {
			t.Errorf("sanitizeFTSQuery(%q) = %q, expected OR connector", tt.input, result)
		}
		if !tt.hasOR && tt.input != "" && strings.Contains(result, " OR ") {
			t.Errorf("sanitizeFTSQuery(%q) = %q, did not expect OR", tt.input, result)
		}
		// FTS5 special chars ( ) ^ should be stripped from input;
		// output may contain " for phrase delimiting and * for prefix matching
		illegalInput := []string{"(", ")", "^"}
		for _, ch := range illegalInput {
			if strings.Contains(result, ch) {
				t.Errorf("sanitizeFTSQuery(%q) contains illegal input char %s: %q", tt.input, ch, result)
			}
		}
	}

	// 关键词不足 2 字符时返回空匹配
	short := sanitizeFTSQuery("a b c d")
	if short != `""` {
		t.Errorf("short words should produce empty match, got %q", short)
	}
}

func TestSanitizeFTSQuery_Chinese(t *testing.T) {
	// 中文词可能被视为单个 token
	result := sanitizeFTSQuery("你好")
	if result == "" {
		t.Error("Chinese query returned empty")
	}
}
