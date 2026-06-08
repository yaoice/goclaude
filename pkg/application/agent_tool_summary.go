package application

// agent_tool_summary.go —— 在 application 层从工具参数 partial JSON 提取可读摘要。
//
// 这是 shell.extractToolSummary 的 application 层轻量版本：
//   - 不依赖 shell 包（避免循环引用）
//   - 只关注对 subagent 进度有价值的核心工具（bash / file / grep / agent 等）
//   - 结果用于 SubagentEvent.LastToolDetail，由 REPL 渲染在步骤行末尾
//
// 解析策略与 shell.extractToolSummary 完全一致（共同维护），但实现独立。

import (
	"encoding/json"
	"strings"
)

// extractAgentToolSummary 从工具 partial JSON 中提取参数摘要（最多 maxRunes 个字符）。
// 空 partialJSON 或无法提取时返回 ""。
func extractAgentToolSummary(toolName, partialJSON string, maxRunes int) string {
	if partialJSON == "" {
		return ""
	}
	m := agentParsePartialJSON(partialJSON)
	if len(m) == 0 {
		return ""
	}
	summary := agentDispatchSummary(strings.ToLower(strings.TrimSpace(toolName)), m)
	if summary == "" {
		summary = agentFirstStringValue(m)
	}
	return agentLimitRunes(summary, maxRunes)
}

func agentDispatchSummary(name string, m map[string]interface{}) string {
	switch name {
	case "bash", "shell", "exec", "run", "execute_command":
		return agentStrField(m, "command", "cmd", "script")
	case "write", "file_write", "write_file", "write_to_file", "create":
		return agentStrField(m, "path", "file_path", "filePath", "filename", "file")
	case "read", "file_read", "read_file":
		return agentStrField(m, "path", "file_path", "filePath", "filename", "file")
	case "delete_file", "deletefile":
		return agentStrField(m, "path", "file_path", "filePath", "target_file")
	case "edit", "str_replace", "strreplace", "replace_in_file",
		"file_edit", "multiedit", "multi_edit", "apply_patch":
		path := agentStrField(m, "path", "file_path", "filePath", "filename", "file")
		oldStr := agentStrField(m, "old_str", "old_string", "search")
		if path != "" && oldStr != "" {
			return path + "  «" + agentFirstLine(oldStr, 30) + "»"
		}
		return path
	case "grep", "search_content":
		pat := agentStrField(m, "pattern", "regex", "query", "search")
		path := agentStrField(m, "path", "directory", "dir", "glob")
		if pat != "" && path != "" {
			return "\"" + pat + "\"  in  " + path
		}
		return "\"" + pat + "\""
	case "glob", "search_file":
		pat := agentStrField(m, "pattern", "glob", "query")
		dir := agentStrField(m, "path", "directory", "dir", "target_directory")
		if pat != "" && dir != "" {
			return pat + "  in  " + dir
		}
		return pat
	case "codebase_search":
		return agentStrField(m, "query", "pattern", "text")
	case "web_fetch", "webfetch":
		return agentStrField(m, "url", "uri")
	case "web_search", "websearch":
		return agentStrField(m, "query", "q", "search")
	case "agent", "task":
		agentType := agentStrField(m, "subagent_type", "agent_type", "type")
		prompt := agentStrField(m, "prompt", "description", "task")
		if agentType != "" && prompt != "" {
			return agentType + ": " + agentFirstLine(prompt, 40)
		}
		return agentType
	case "ls", "list_dir", "listdir":
		return agentStrField(m, "path", "directory", "target_directory", "dir")
	case "team_create":
		return agentStrField(m, "team_name", "name")
	case "send_message":
		to := agentStrField(m, "to", "recipient")
		summary := agentStrField(m, "summary", "content")
		if to != "" && summary != "" {
			return "→" + to + " " + agentFirstLine(summary, 30)
		}
		return to
	}
	return ""
}

// ---- helpers ----

func agentParsePartialJSON(s string) map[string]interface{} {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var m map[string]interface{}
	if json.Unmarshal([]byte(s), &m) == nil {
		return m
	}
	if json.Unmarshal([]byte(s+"}"), &m) == nil {
		return m
	}
	if json.Unmarshal([]byte(s+`"}`), &m) == nil {
		return m
	}
	return nil
}

func agentStrField(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func agentFirstStringValue(m map[string]interface{}) string {
	for _, v := range m {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return agentFirstLine(strings.TrimSpace(s), 60)
		}
	}
	return ""
}

func agentFirstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return agentLimitRunes(s, max)
}

func agentLimitRunes(s string, max int) string {
	if max <= 0 || s == "" {
		return s
	}
	rs := []rune(s)
	if len(rs) <= max {
		return s
	}
	if max <= 1 {
		return string(rs[:max])
	}
	return string(rs[:max-1]) + "…"
}
