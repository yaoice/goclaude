package tool

// input_summary.go —— 从工具的 JSON 参数中提取可读摘要（domain 层）。
//
// 此文件实现与 shell.extractToolSummary / application.extractAgentToolSummary
// 相同的语义，用于在 Executor 派发 ToolPhaseStart 事件时填充 InputSummary 字段。
//
// 为什么在 domain 层重复实现：
//   - domain 包不能依赖 shell/application 包（分层约束）
//   - 逻辑极简（只做字符串提取），内联在 domain 层成本低、无副作用
//
// 规则：按工具名走独立分支，未知工具取第一个非空字符串值兜底；
// 结果截断到 maxRunes，保证不超出终端可用宽度。

import (
	"encoding/json"
	"strings"
)

// extractInputSummary 从工具的完整 JSON 参数字符串提取单行可读摘要。
// maxRunes ≤ 0 时不截断。
func extractInputSummary(toolName, jsonStr string, maxRunes int) string {
	if jsonStr == "" {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil || len(m) == 0 {
		return ""
	}
	s := inputDispatch(strings.ToLower(strings.TrimSpace(toolName)), m)
	if s == "" {
		s = inputFirstString(m)
	}
	return inputLimit(s, maxRunes)
}

func inputDispatch(name string, m map[string]interface{}) string {
	switch name {
	case "bash", "shell", "exec", "run", "execute_command":
		return inputStr(m, "command", "cmd", "script")
	case "write", "file_write", "write_file", "write_to_file", "create":
		return inputStr(m, "path", "file_path", "filePath", "filename", "file")
	case "read", "file_read", "read_file":
		return inputStr(m, "path", "file_path", "filePath", "filename", "file")
	case "delete_file", "deletefile":
		return inputStr(m, "path", "file_path", "filePath", "target_file")
	case "edit", "str_replace", "strreplace", "replace_in_file",
		"file_edit", "multiedit", "multi_edit", "apply_patch":
		path := inputStr(m, "path", "file_path", "filePath", "filename", "file")
		oldStr := inputStr(m, "old_str", "old_string", "search")
		if path != "" && oldStr != "" {
			return path + "  «" + inputFirstLine(oldStr, 30) + "»"
		}
		return path
	case "grep", "search_content":
		pat := inputStr(m, "pattern", "regex", "query", "search")
		path := inputStr(m, "path", "directory", "dir", "glob")
		if pat != "" && path != "" {
			return "\"" + pat + "\"  in  " + path
		}
		if pat != "" {
			return "\"" + pat + "\""
		}
		return path
	case "glob", "search_file":
		pat := inputStr(m, "pattern", "glob", "query")
		dir := inputStr(m, "path", "directory", "dir", "target_directory")
		if pat != "" && dir != "" {
			return pat + "  in  " + dir
		}
		return pat
	case "codebase_search":
		return inputStr(m, "query", "pattern", "text")
	case "web_fetch", "webfetch":
		return inputStr(m, "url", "uri")
	case "web_search", "websearch":
		return inputStr(m, "query", "q", "search")
	case "agent", "task":
		agentType := inputStr(m, "subagent_type", "agent_type", "type")
		prompt := inputStr(m, "prompt", "description", "task")
		if agentType != "" && prompt != "" {
			return agentType + ": " + inputFirstLine(prompt, 40)
		}
		return agentType
	case "ls", "list_dir", "listdir":
		return inputStr(m, "path", "directory", "target_directory", "dir")
	case "team_create":
		return inputStr(m, "team_name", "name")
	case "send_message":
		to := inputStr(m, "to", "recipient")
		summary := inputStr(m, "summary", "content")
		if to != "" && summary != "" {
			return "→" + to + " " + inputFirstLine(summary, 30)
		}
		return to
	case "skill":
		return inputStr(m, "name", "skill_name")
	case "todo_write", "todowrite":
		if arr, ok := m["todos"].([]interface{}); ok && len(arr) > 0 {
			if item, ok := arr[0].(map[string]interface{}); ok {
				return inputFirstLine(inputStr(item, "content", "text"), 50)
			}
		}
		return ""
	}
	return ""
}

// inputStr 从 map 按候选 key 顺序取第一个非空字符串。
func inputStr(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

// inputFirstString 取 map 中第一个非空字符串值（key 不确定时兜底）。
func inputFirstString(m map[string]interface{}) string {
	for _, v := range m {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return inputFirstLine(strings.TrimSpace(s), 60)
		}
	}
	return ""
}

// inputFirstLine 取字符串首个非空行，截断到 max 个 rune。
func inputFirstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return inputLimit(s, max)
}

// inputLimit 截断到 max 个 rune（0 = 不限制）。
func inputLimit(s string, max int) string {
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
