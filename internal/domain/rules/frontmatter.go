// Package rules implements frontmatter parsing
// Reference TS implementation: src/utils/frontmatterParser.ts
package rules

import (
	"strings"
)

// ParseFrontmatter parses the frontmatter in a Markdown file
func ParseFrontmatter(content string, filePath string) (Frontmatter, string) {
	fm := Frontmatter{}
	
	trimmed := strings.TrimLeft(content, "\n\r\t ")
	if !strings.HasPrefix(trimmed, "---") {
		return fm, content
	}

	afterOpen := trimmed[3:]
	lines := strings.Split(afterOpen, "\n")
	var frontmatterLines []string
	var remainingLines []string
	foundClosing := false
	
	for i, line := range lines {
		if foundClosing {
			remainingLines = lines[i:]
			break
		}
		
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "---" {
			foundClosing = true
			continue
		}
		
		frontmatterLines = append(frontmatterLines, line)
	}
	
	if !foundClosing {
		return fm, content
	}
	
	rawFrontmatter := strings.Join(frontmatterLines, "\n")
	fm.Raw = rawFrontmatter
	
	for _, line := range frontmatterLines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		value = strings.Trim(value, "\"'")

		switch key {
		case "description":
			fm.Description = value
		case "type":
			fm.Type = value
		case "paths":
			fm.Paths = value
		}
	}
	
	remaining := strings.Join(remainingLines, "\n")
	remaining = strings.TrimPrefix(remaining, "\n")
	remaining = strings.TrimPrefix(remaining, "\r\n")
	
	return fm, remaining
}

// ParseFrontmatterPaths parses the paths field in frontmatter
func ParseFrontmatterPaths(pathsStr string) []string {
	if pathsStr == "" {
		return nil
	}

	pathsStr = strings.TrimSpace(pathsStr)
	if pathsStr == "**" {
		return nil
	}

	var patterns []string

	parts := strings.Split(pathsStr, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || p == "**" {
			continue
		}

		if strings.HasSuffix(p, "/**") {
			p = strings.TrimSuffix(p, "/**")
			if p != "" {
				patterns = append(patterns, p)
			}
		}
	}

	if len(patterns) == 0 {
		return nil
	}

	return patterns
}

// StripHtmlComments removes HTML comments from Markdown content
func StripHtmlComments(content string) (string, bool) {
	if !strings.Contains(content, "<!--") {
		return content, false
	}

	lines := strings.Split(content, "\n")
	var result []string
	inComment := false
	stripped := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.Contains(line, "<!--") && strings.Contains(line, "-->") {
			idx := strings.Index(line, "<!--")
			if idx >= 0 {
				before := line[:idx]
				afterIdx := strings.Index(line[idx:], "-->")
				if afterIdx >= 0 {
					after := line[idx+afterIdx+3:]
					cleaned := strings.TrimSpace(before + after)
					if cleaned != "" {
						result = append(result, cleaned)
					}
				}
			}
			stripped = true
			continue
		}

		if strings.HasPrefix(trimmed, "<!--") {
			inComment = true
			stripped = true
			continue
		}

		if inComment && strings.Contains(line, "-->") {
			inComment = false
			stripped = true
			continue
		}

		if inComment {
			stripped = true
			continue
		}

		result = append(result, line)
	}

	output := strings.Join(result, "\n")
	
	// Collapse multiple consecutive newlines to at most 2 (one blank line)
	for strings.Contains(output, "\n\n\n") {
		output = strings.Replace(output, "\n\n\n", "\n\n", -1)
	}
	
	if strings.HasSuffix(content, "\n") {
		output = output + "\n"
	}
	
	return output, stripped
}

// ExpandPath expands a path (handles ~/ prefix)
func ExpandPath(path string, baseDir string) string {
	if strings.HasPrefix(path, "~/") {
		return path
	}

	if strings.HasPrefix(path, "./") {
		return baseDir + "/" + path[2:]
	}

	if strings.HasPrefix(path, "/") {
		return path
	}

	return baseDir + "/" + path
}

// SplitPathInFrontmatter splits the paths field in frontmatter
func SplitPathInFrontmatter(pathsStr string) []string {
	return ParseFrontmatterPaths(pathsStr)
}
