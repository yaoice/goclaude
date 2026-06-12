// Package rules 实现 CLAUDE.md / .claude/rules/*.md 的规则加载系统
// 参考 TypeScript 实现：src/utils/claudemd.ts
package rules

import "strings"

// MemoryInstructionPrompt 注入系统提示词的头部文本
const MemoryInstructionPrompt = "Codebase and user instructions are shown below. Be sure to adhere to these instructions. IMPORTANT: These instructions OVERRIDE any default behavior and you MUST follow them exactly as written."

// MAX_MEMORY_CHARACTER_COUNT 单个记忆文件推荐最大字符数
const MaxMemoryCharacterCount = 40000

// MAX_INCLUDE_DEPTH @include 最大递归深度
const MaxIncludeDepth = 5

// FRONTMATTER_MAX_LINES frontmatter 最大行数
const FrontmatterMaxLines = 30

// MAX_MEMORY_FILES 单次扫描最大文件数
const MaxMemoryFiles = 200

// EntrypointName 记忆入口文件名
const EntrypointName = "MEMORY.md"

// MAX_ENTRYPOINT_LINES 最大行数
const MaxEntrypointLines = 200

// MAX_ENTRYPOINT_BYTES 最大字节数
const MaxEntrypointBytes = 25000

// TextFileExtensions 允许 @include 的文本文件扩展名
var TextFileExtensions = map[string]bool{
	".md": true, ".txt": true, ".text": true,
	".json": true, ".yaml": true, ".yml": true, ".toml": true, ".xml": true, ".csv": true,
	".html": true, ".htm": true, ".css": true, ".scss": true, ".sass": true, ".less": true,
	".js": true, ".ts": true, ".tsx": true, ".jsx": true, ".mjs": true, ".cjs": true, ".mts": true, ".cts": true,
	".py": true, ".pyi": true, ".pyw": true,
	".rb": true, ".erb": true, ".rake": true,
	".go":   true,
	".rs":   true,
	".java": true, ".kt": true, ".kts": true, ".scala": true,
	".c": true, ".cpp": true, ".cc": true, ".cxx": true, ".h": true, ".hpp": true, ".hxx": true,
	".cs":    true,
	".swift": true,
	".sh":    true, ".bash": true, ".zsh": true, ".fish": true, ".ps1": true, ".bat": true, ".cmd": true,
	".env": true, ".ini": true, ".cfg": true, ".conf": true, ".config": true, ".properties": true,
	".sql": true, ".graphql": true, ".gql": true,
	".proto": true,
	".vue":   true, ".svelte": true, ".astro": true,
	".ejs": true, ".hbs": true, ".pug": true, ".jade": true,
	".php": true, ".pl": true, ".pm": true, ".lua": true, ".r": true, ".R": true,
	".dart": true, ".ex": true, ".exs": true, ".erl": true, ".hrl": true,
	".clj": true, ".cljs": true, ".cljc": true, ".edn": true,
	".hs": true, ".lhs": true, ".elm": true,
	".ml": true, ".mli": true, ".f": true, ".f90": true, ".f95": true, ".for": true,
	".cmake": true, ".make": true, ".makefile": true, ".gradle": true, ".sbt": true,
	".rst": true, ".adoc": true, ".asciidoc": true, ".org": true, ".tex": true, ".latex": true,
	".lock": true,
	".log":  true, ".diff": true, ".patch": true,
}

// IsTextFile 检查是否为文本文件（可用于 @include）
func IsTextFile(ext string) bool {
	if len(ext) == 0 {
		return false
	}
	return TextFileExtensions[ext]
}

// NormalizePath 标准化路径（用于比较）
func NormalizePath(path string) string {
	path = strings.ToLower(path)
	path = strings.TrimSuffix(path, "/")
	return path
}

// IsValidIncludePath 检查是否为有效的 @include 路径
func IsValidIncludePath(path string) bool {
	if path == "" {
		return false
	}

	if strings.HasPrefix(path, "./") ||
		strings.HasPrefix(path, "~/") ||
		(strings.HasPrefix(path, "/") && len(path) > 1) {
		return true
	}

	if len(path) > 0 {
		c := path[0]
		if (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '.' || c == '_' || c == '-' {
			return true
		}
	}

	return false
}

// FormatMemoryContent 格式化记忆文件内容用于注入系统提示词
func FormatMemoryContent(files []MemoryFileInfo, filter func(MemoryType) bool) string {
	var memories []string

	for _, file := range files {
		if filter != nil && !filter(file.Type) {
			continue
		}

		content := file.Content
		if len(content) == 0 {
			continue
		}
		content = strings.TrimSpace(content)

		var description string
		switch file.Type {
		case MemoryTypeProject:
			description = " (project instructions, checked into the codebase)"
		case MemoryTypeLocal:
			description = " (user's private project instructions, not checked in)"
		case MemoryTypeTeamMem:
			description = " (shared team memory, synced across the organization)"
		case MemoryTypeAutoMem:
			description = " (user's auto-memory, persists across conversations)"
		default:
			description = " (user's private global instructions for all projects)"
		}

		if file.Type == MemoryTypeTeamMem {
			memories = append(memories, "Contents of "+file.Path+" "+description+":\n\n<team-memory-content source=\"shared\">\n"+content+"\n</team-memory-content>")
		} else {
			memories = append(memories, "Contents of "+file.Path+" "+description+":\n\n"+content)
		}
	}

	if len(memories) == 0 {
		return ""
	}

	return MemoryInstructionPrompt + "\n\n" + strings.Join(memories, "\n\n")
}
