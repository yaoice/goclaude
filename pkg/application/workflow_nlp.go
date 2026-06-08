// Package application 提供 workflow 相关自然语言解析能力。
//
// ParseWorkflowIntent 可以从自然语言文本中提取 workflow 创建意图：
//   - 判断用户是否想创建一个 workflow
//   - 提取 workflow 描述信息
//
// 用于 REPL 层预处理：检测到创建意图后自动路由到 PlanAgentService，
// 而不是将原始文本发送给 LLM 再做二次判断。
package application

import (
	"regexp"
	"strings"
)

// WorkflowIntent 是 ParseWorkflowIntent 的解析结果。
type WorkflowIntent struct {
	// Triggered 是否检测到创建 workflow 的意图
	Triggered bool
	// Description 提取出的 workflow 描述文本（触发词之后的内容）
	Description string
	// OriginalText 原始用户输入
	OriginalText string
}

// ParseWorkflowIntent 从自然语言文本中提取 workflow 创建意图。
//
// 支持的中文触发词：
//   - "创建workflow" / "新建workflow" / "生成workflow" / "定义workflow"
//   - "创建流程" / "新建流程" / "定义一个workflow"
//   - "帮我做一个workflow" / "帮我创建workflow"
//
// 支持的英文触发词：
//   - "create workflow" / "new workflow" / "generate workflow"
//   - "define workflow" / "make a workflow" / "set up a workflow"
//   - "build a workflow"
//
// 返回 nil 表示未识别到 workflow 创建意图。
func ParseWorkflowIntent(text string) *WorkflowIntent {
	lower := strings.ToLower(text)

	// 排除条件：如果文本是纯斜杠命令（/workflow ...），不处理
	// 因为 /workflow 命令已由 handleLocalCommand 处理
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "/") {
		return nil
	}

	// 排除条件：如果用户明确提到 subagent 或 team，不触发 workflow 意图
	excludeWords := []string{
		"subagent", "sub agent", "agent team", "agent-team", "teammate",
		"创建团队", "建团队", "新建 team",
	}
	for _, w := range excludeWords {
		if strings.Contains(lower, w) {
			return nil
		}
	}

	// 中文触发模式
	cnPatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?:创建|新建|生成|定义|制作|写|做|建立)\s*(?:一个|个)?\s*workflow`),
		regexp.MustCompile(`(?:创建|新建|生成|定义)\s*(?:一个|个)?\s*(?:新的)?\s*流程`),
		regexp.MustCompile(`帮我\s*(?:创建|新建|生成|做|写|定义)\s*(?:一个|个)?\s*workflow`),
		regexp.MustCompile(`(?:设计|规划|构建|搭建)\s*(?:一个|个)?\s*workflow`),
	}

	// 英文触发模式
	enPatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:create|new|generate|define|make|set\s*up|build)\s+(?:a\s+)?(?:new\s+)?workflow`),
		regexp.MustCompile(`(?i)(?:design|plan|craft)\s+(?:a\s+)?(?:new\s+)?workflow`),
	}

	// 尝试所有模式
	var matchedIdx, matchedStart, matchedLen int = -1, -1, -1
	allPatterns := append(cnPatterns, enPatterns...)

	// 在原始文本（未转小写）上匹配，以保留原文的精确位置
	for i, re := range allPatterns {
		if loc := re.FindStringIndex(text); loc != nil {
			// 也尝试在 lower 上匹配（大小写不敏感）
			if matchedIdx == -1 || loc[0] < matchedStart {
				matchedIdx = i
				matchedStart = loc[0]
				matchedLen = loc[1] - loc[0]
			}
		}
	}

	if matchedIdx == -1 {
		return nil
	}

	// 提取触发词后的描述
	var description string
	after := strings.TrimSpace(text[matchedStart+matchedLen:])

	// 去掉常见连接词
	after = removePrefixWords(after,
		"，", ",", "：", ":", "用于", "用来", "做", "for", "to", "that",
		"实现", "完成", "执行", "do", "does", "which",
	)

	if after != "" {
		description = after
	} else {
		// 如果触发词后没有描述，尝试提取整段文字中触发词前的描述
		before := strings.TrimSpace(text[:matchedStart])
		// 去掉常见前缀词
		before = removeSuffixWords(before,
			"请", "帮我", "帮忙", "please", "can you", "could you",
		)
		if before != "" {
			description = before
		} else {
			// 完全无法提取描述，使用原始文本
			description = text
		}
	}

	// 限制描述长度，避免过长的文本传给 Plan Agent
	if len([]rune(description)) > 500 {
		runes := []rune(description)
		description = string(runes[:500]) + "..."
	}

	return &WorkflowIntent{
		Triggered:    true,
		Description:  description,
		OriginalText: text,
	}
}

// removePrefixWords 去掉字符串开头的指定词语。
func removePrefixWords(s string, words ...string) string {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	for _, w := range words {
		if strings.HasPrefix(lower, strings.ToLower(w)) {
			remaining := strings.TrimSpace(s[len(w):])
			// 去掉剩下的空白连接词
			remaining = strings.TrimLeftFunc(remaining, func(r rune) bool {
				return r == ' ' || r == '\t' || r == '，' || r == ',' || r == '：' || r == ':'
			})
			return remaining
		}
	}
	return s
}

// removeSuffixWords 去掉字符串末尾的指定词语。
func removeSuffixWords(s string, words ...string) string {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	for _, w := range words {
		if strings.HasSuffix(lower, strings.ToLower(w)) {
			return strings.TrimSpace(s[:len(s)-len(w)])
		}
	}
	return s
}
