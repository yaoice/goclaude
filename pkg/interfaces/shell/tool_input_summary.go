package shell

// tool_input_summary.go —— 从工具调用的 JSON 参数中提取可读摘要。
//
// 设计动机：
//   REPL 流式渲染时，工具调用行只显示 "→ bash  …"，用户无法看到实际执行
//   的命令是什么、操作的是哪个文件、搜索的是什么模式。本文件把 partial JSON
//   解析为一段紧凑的单行摘要，拼到工具名后面：
//
//     →  bash  ls -la src/  …
//     →  write  src/main.go  …
//     →  grep  "func New"  …
//     →  Agent  coder: implement login  …
//
//   提取逻辑按工具名走独立分支，未知工具则取参数中第一个非空字符串值兜底。
//   所有返回值均截断到 termWidth，保证不破坏行格式。

import (
	"encoding/json"
	"strings"
)

// extractToolSummary 从完整或 partial JSON 参数字符串中提取可读摘要。
//
// 参数：
//   - toolName: 工具名（小写无空格）
//   - partialJSON: 从流式事件累积的 partial JSON（可能未闭合，尽力解析）
//   - maxRunes: 摘要最大 rune 数（0 表示不限制，调用方通常传 termWidth - 30）
//
// 返回空串表示无有效摘要（调用方保持原有 "…" 占位）。
func extractToolSummary(toolName, partialJSON string, maxRunes int) string {
	if partialJSON == "" {
		return ""
	}

	// 尝试解析为 map；partial JSON 可能不完整，先补右括号强行解析。
	m := parsePartialJSON(partialJSON)
	if len(m) == 0 {
		return ""
	}

	summary := dispatchSummary(strings.ToLower(strings.TrimSpace(toolName)), m)
	if summary == "" {
		summary = firstStringValue(m)
	}

	// 安全保证：摘要必须是单行，折叠任何残留换行为空格
	summary = collapseToSingleLine(summary)
	return limitRunes(summary, maxRunes)
}

// collapseToSingleLine 将多行文本折叠为单行：换行替换为空格，合并连续空格。
func collapseToSingleLine(s string) string {
	// 替换所有换行符为空格
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	// 合并连续空格
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

// dispatchSummary 按工具名分发摘要提取逻辑。
func dispatchSummary(name string, m map[string]interface{}) string {
	switch name {
	// ---- 执行类 ----
	case "bash", "shell", "exec", "run", "execute_command":
		return summarizeBashCommand(strField(m, "command", "cmd", "script"))

	// ---- 文件读写类 ----
	case "write", "file_write", "write_file", "write_to_file", "create":
		path := strField(m, "path", "file_path", "filePath", "filename", "file")
		return fmtFileOp("write", path)

	case "read", "file_read", "read_file":
		path := strField(m, "path", "file_path", "filePath", "filename", "file")
		return fmtFileOp("read", path)

	case "delete_file", "deletefile":
		path := strField(m, "path", "file_path", "filePath", "target_file")
		return fmtFileOp("delete", path)

	// ---- 文件编辑类 ----
	case "edit", "str_replace", "strreplace", "replace_in_file",
		"file_edit", "multiedit", "multi_edit", "apply_patch":
		path := strField(m, "path", "file_path", "filePath", "filename", "file")
		// 尝试显示被替换的旧文本的首行
		oldStr := strField(m, "old_str", "old_string", "search")
		if path != "" && oldStr != "" {
			return path + "  «" + firstLine(oldStr, 40) + "»"
		}
		return fmtFileOp("edit", path)

	case "notebook_edit", "notebookedit":
		path := strField(m, "path", "notebook_path", "file_path")
		return fmtFileOp("notebook", path)

	// ---- 搜索类 ----
	case "grep", "search_content":
		pat := strField(m, "pattern", "regex", "query", "search")
		path := strField(m, "path", "directory", "dir", "glob")
		if pat != "" && path != "" {
			return "\"" + pat + "\"  in  " + path
		}
		if pat != "" {
			return "\"" + pat + "\""
		}
		return path

	case "glob", "search_file":
		pat := strField(m, "pattern", "glob", "query")
		dir := strField(m, "path", "directory", "dir", "target_directory")
		if pat != "" && dir != "" {
			return pat + "  in  " + dir
		}
		return pat

	case "codebase_search":
		return strField(m, "query", "pattern", "text")

	// ---- 网络类 ----
	case "web_fetch", "webfetch":
		return strField(m, "url", "uri")

	case "web_search", "websearch":
		return strField(m, "query", "q", "search")

	// ---- Agent/Task 类 ----
	case "agent", "task":
		agentType := strField(m, "subagent_type", "agent_type", "type")
		prompt := strField(m, "prompt", "description", "task")
		if agentType != "" && prompt != "" {
			return agentType + ": " + firstLine(prompt, 50)
		}
		if agentType != "" {
			return agentType
		}
		return firstLine(prompt, 60)

	// ---- Team 类 ----
	case "team_create":
		return strField(m, "team_name", "name")
	case "send_message":
		to := strField(m, "to", "recipient")
		summary := strField(m, "summary", "content")
		if to != "" && summary != "" {
			return "→" + to + "  " + firstLine(summary, 40)
		}
		return to

	// ---- 目录/列表类 ----
	case "ls", "list_dir", "listdir":
		return strField(m, "path", "directory", "target_directory", "dir")

	// ---- Todo 类 ----
	case "todo_write", "todowrite":
		// 显示第一个 todo 的 content
		if arr, ok := m["todos"].([]interface{}); ok && len(arr) > 0 {
			if item, ok := arr[0].(map[string]interface{}); ok {
				return firstLine(strField(item, "content", "text"), 50)
			}
		}
		return ""

	// ---- Skill 类 ----
	case "skill":
		return strField(m, "name", "skill_name")
	}
	return ""
}

// ---- helpers ----

// summarizeBashCommand 把多行 bash 命令折叠为单行可读摘要。
//
// 策略：
//   1. 去除开头的注释行（# ...），但保留注释内容作为"意图说明"
//   2. 将多行命令折叠为首个有意义的命令行
//   3. 如果命令以注释开头，显示 "# 注释 → 首条命令"
//
// 示例：
//   输入：  "# 安装依赖\ncd /tmp && npm install\necho done"
//   输出：  "# 安装依赖 → cd /tmp && npm install"
//
//   输入：  "cd /data/workspace && go build ./..."
//   输出：  "cd /data/workspace && go build ./..."
func summarizeBashCommand(cmd string) string {
	if cmd == "" {
		return ""
	}

	lines := strings.Split(cmd, "\n")

	// 收集注释行和命令行
	var comment string
	var firstCmd string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			if comment == "" {
				// 保留首个注释（去掉 # 后的空格）
				comment = strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
			}
			continue
		}
		// 首个非注释、非空行就是实际命令
		if firstCmd == "" {
			firstCmd = trimmed
		}
		break
	}

	if firstCmd == "" && comment != "" {
		// 只有注释没有命令（不太可能，但兜底）
		return "# " + comment
	}
	if firstCmd == "" {
		return ""
	}

	// 如果有注释说明意图，组合 "# 注释 → 命令"
	if comment != "" {
		return "# " + comment + " → " + firstCmd
	}
	return firstCmd
}

// parsePartialJSON 尽力把不完整 JSON 解析为 map。
// 策略：直接解析 → 补 } 后解析 → 补 "} 后解析（三级退路）。
func parsePartialJSON(s string) map[string]interface{} {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var m map[string]interface{}
	// 尝试直接解析
	if json.Unmarshal([]byte(s), &m) == nil {
		return m
	}
	// 补一个右括号
	if json.Unmarshal([]byte(s+"}"), &m) == nil {
		return m
	}
	// 补引号 + 右括号（末尾未闭合字符串）
	if json.Unmarshal([]byte(s+`"}`), &m) == nil {
		return m
	}
	return nil
}

// strField 从 map 按候选 key 顺序取第一个非空字符串。
func strField(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

// firstStringValue 取 map 中第一个非空字符串值（key 不确定时兜底）。
func firstStringValue(m map[string]interface{}) string {
	for _, v := range m {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return firstLine(strings.TrimSpace(s), 60)
		}
	}
	return ""
}

// firstLine 取字符串首个非空行，截断到 max 个 rune。
func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return limitRunes(s, max)
}

// fmtFileOp 格式化文件操作摘要；路径为空时返回空串。
func fmtFileOp(op, path string) string {
	if path == "" {
		return ""
	}
	_ = op // op 信息已由工具名传达，不重复显示
	return path
}

// limitRunes 截断到 max 个 rune（0 = 不限制）。
func limitRunes(s string, max int) string {
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

// summarizeResult 为工具结果生成紧凑的语义摘要。
//
// 规则：
//   - 空结果：返回 ""（调用方用 "done"/"failed" 兜底）
//   - 单行且非 JSON 碎片：直接返回，截断到 hardMax
//   - 多行：返回统计描述（N lines），不展示碎片内容
//   - JSON 内容：返回统计描述，避免显示无意义的 key/bracket 碎片
func summarizeResult(text string, hardMax int) string {
	if text == "" {
		return ""
	}
	// 统计行数
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	totalLines := len(lines)
	first := strings.TrimSpace(lines[0])

	// 多行结果：只返回行数统计，由调用方决定是否展开
	if totalLines > 1 {
		return itoa(totalLines) + " lines"
	}
	// 单行：如果是 JSON 碎片或结构噪声则用统计描述
	if isJSONFragment(first) {
		return "1 line"
	}
	return limitRunes(first, hardMax)
}

// isStructuralNoise 判断一行是否仅为结构性噪声（单独的括号/大括号等），
// 不具备语义信息。
func isStructuralNoise(s string) bool {
	switch s {
	case "{", "}", "(", ")", "[", "]", "{}", "[]", "()", "({", "})", "({})":
		return true
	}
	return false
}

// isJSONFragment 判断一行文本是否看起来像 JSON 碎片（key-value 对、括号开头等），
// 这些碎片在单行摘要中无意义且产生歧义。
func isJSONFragment(s string) bool {
	if s == "" {
		return false
	}
	// 以 { [ 开头的都是 JSON 结构
	if s[0] == '{' || s[0] == '[' {
		return true
	}
	// 以 } ] ) 结尾的也是结构碎片
	last := s[len(s)-1]
	if last == '}' || last == ']' || last == ')' {
		// 但不包括正常英文句子以 ) 结尾的情况
		if last == ')' && !strings.Contains(s, ":") && !strings.HasPrefix(s, "\"") {
			return false
		}
		return true
	}
	// 以引号开头的 key: value 格式（如 "intent": {）
	if strings.HasPrefix(s, "\"") && strings.Contains(s, ":") {
		return true
	}
	// 单独的括号
	return isStructuralNoise(s)
}

// findMeaningfulLine 在多行文本中找到第一行有意义的内容（跳过噪声行）。
func findMeaningfulLine(lines []string) string {
	for _, l := range lines {
		tl := strings.TrimSpace(l)
		if tl == "" || isStructuralNoise(tl) || isJSONFragment(tl) {
			continue
		}
		return tl
	}
	// 全是噪声行则返回统计描述
	return itoa(len(lines)) + " lines"
}

// summarizeError 为错误输出生成摘要，保留更多内容以便定位问题。
//
// 与 summarizeResult 的区别：错误摘要优先显示关键错误行（含 "error"/"Error"
// 关键字的行），而非首行。
func summarizeError(text string, hardMax int) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")

	// 找第一个含错误关键字的行（跳过噪声和 JSON 碎片）
	bestLine := ""
	for _, l := range lines {
		tl := strings.TrimSpace(l)
		if tl == "" || isStructuralNoise(tl) {
			continue
		}
		low := strings.ToLower(tl)
		if strings.Contains(low, "error") ||
			strings.Contains(low, "fatal") ||
			strings.Contains(low, "panic") ||
			strings.Contains(low, "failed") {
			bestLine = tl
			break
		}
	}
	// 若未找到含关键字的行，取第一个非噪声非 JSON 碎片行
	if bestLine == "" {
		bestLine = findMeaningfulLine(lines)
	}
	// 错误摘要只显示关键信息，不附加 lines hidden 后缀
	return limitRunes(strings.TrimSpace(bestLine), hardMax)
}

// itoa 把非负整数转字符串（避免引入 fmt/strconv 冗余 import）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 10)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}
