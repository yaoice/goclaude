// Package application 提供 Plan Agent — AI 驱动的 workflow 定义生成器。
//
// 对齐 oh-my-openagent 的 Sisyphus orchestrator 中 Plan Agent 模式：
//   - 分析用户意图 → 分解为结构化的 task 列表
//   - 强制输出依赖图 + 并行执行波次 + 分类/技能推荐
//   - 输出纯 JSON（AI 生成友好），避免 YAML 缩进错误
//
// 与手工编写的 YAML workflow 互补：有预定义文件时直接执行；
// 无文件时自动调用 Plan Agent 生成定义 → 保存 → 执行。
package application

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// PlanAgentPrompt 构建 Plan Agent 的系统提示词。
//
// 核心要求（对齐 oh-my-openagent）：
//   1. 依赖图分析（每任务的 blocking/dependent 关系）
//   2. 并行波次分解（识别哪些任务可安全并行）
//   3. 分类推荐（subagent_type 路由）
//   4. 技能建议（预加载 skills）
//   5. 输出纯 JSON（无 markdown 包裹，可直接解析）
//
// availableAgents: 当前可用的 subagent 类型列表
// userRequest: 用户对 workflow 目标的自然语言描述
func PlanAgentPrompt(availableAgents []string, userRequest string) string {
	var sb strings.Builder

	sb.WriteString(`<system>
You are a Workflow Plan Agent. Your job is to analyze a user's request and produce a
structured workflow definition as a pure JSON object.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
MANDATORY: DEPENDENCY GRAPH ANALYSIS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

For EVERY task you create, you MUST:
1. Identify what it DEPENDS ON (blockers that must complete first)
2. Identify what DEPENDS ON IT (successors that wait for it)
3. Document the REASON for each dependency

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
MANDATORY: PARALLEL EXECUTION WAVES
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

You MUST organize tasks into parallel execution waves:
- Wave 1: All tasks with NO dependencies (can start immediately)
- Wave 2: Tasks that only depend on Wave 1 tasks
- Wave N: Tasks that only depend on earlier waves

Tasks in the same wave CAN run in parallel.
Tasks in different waves MUST wait for prior waves to complete.

This maximizes throughput and minimizes total wall-clock time.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
MANDATORY: EXACT OUTPUT FORMAT
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

You MUST output ONLY a valid JSON object. No markdown fences, no commentary.
The JSON must follow this schema exactly:

{
  "name": "kebab-case-workflow-name",
  "description": "Brief 1-line description of what this workflow does",
  "version": "1.0",
  "nodes": [
    {
      "id": "unique-node-id",
      "name": "Human-readable task name",
      "description": "3-5 word summary of this task",
      "subagent_type": "Explore|Plan|general-purpose",
      "prompt": "Detailed instructions for the subagent to execute",
      "depends_on": ["id-of-task-this-depends-on"],
      "timeout_sec": 0,
      "failure_strategy": "fail_fast|continue",
      "skills": ["optional-skill-name"],
      "model": "optional-model-override"
    }
  ]
}

Field descriptions:
- id: Lowercase, hyphenated, unique within workflow (e.g., "explore-codebase")
- name: Short human-readable name (1-5 words)
- description: What this subagent will do (3-5 words)
- subagent_type: Which subagent type to use for this task
- prompt: Detailed instructions for the subagent. Be specific about what to find/do.
- depends_on: Array of node IDs this task blocks on. Empty array = no dependencies.
- timeout_sec: 0 = no timeout. Set for long-running tasks.
- failure_strategy: "fail_fast" stops the workflow on failure (default).
                    "continue" allows independent siblings to keep running.
- skills: Optional list of skill names to preload for the subagent.
- model: Optional model override. Leave empty to use the default model.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
CONSTRAINTS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

1. NO MARKDOWN WRAPPERS. Output the JSON directly.
2. ALL depends_on IDs MUST reference existing node IDs in the nodes array.
3. NO circular dependencies (A→B→C→A is invalid).
4. The graph MUST be a valid DAG (Directed Acyclic Graph).
5. At least 2 nodes required. Typical workflows have 3-7 nodes.
6. Prefer EXPLICIT dependencies over sequential chains where tasks are independent.
7. Default failure_strategy to "fail_fast" unless you have a good reason for "continue".
</system>

`)

	sb.WriteString("\n")
	sb.WriteString(buildAgentReferenceSection(availableAgents))
	sb.WriteString("\n")

	sb.WriteString(fmt.Sprintf(`<user_request>
%s
</user_request>

Generate the workflow JSON now. Output ONLY the JSON object, nothing else.`, userRequest))

	return sb.String()
}

// buildAgentReferenceSection 生成可用 subagent 类型的参考说明
func buildAgentReferenceSection(agents []string) string {
	var sb strings.Builder
	sb.WriteString(`<available_subagent_types>
The following subagent types are available. Use them in the "subagent_type" field:

`)

	agentDescriptions := map[string]string{
		"Explore":         "Codebase exploration: search files, read code, find patterns, gather context",
		"Plan":            "Strategic planning: research solutions, evaluate approaches, design architecture",
		"general-purpose": "General task execution: write code, run commands, make changes, verify results",
	}

	for _, agentType := range agents {
		desc, ok := agentDescriptions[agentType]
		if !ok {
			desc = "General-purpose task execution"
		}
		sb.WriteString(fmt.Sprintf("  - %s: %s\n", agentType, desc))
	}

	sb.WriteString(`</available_subagent_types>`)
	return sb.String()
}

// ParsePlanAgentOutput 解析 Plan Agent 的 JSON 输出。
//
// 处理常见的 LLM 输出噪音：
//   - ```json ... ``` markdown 代码块包裹
//   - 前后空白字符
//   - 行内注释（// 或 #）
func ParsePlanAgentOutput(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)

	// 尝试提取 ```json ... ``` 代码块（循环剥离多层包裹和多余尾部噪声）
	if strings.HasPrefix(raw, "```json") {
		raw = strings.TrimPrefix(raw, "```json")
	} else if strings.HasPrefix(raw, "```") {
		raw = strings.TrimPrefix(raw, "```")
	}

	// 循环移除所有尾部的 ```（LLM 可能输出多个或额外文本后跟额外的 ```）
	for strings.HasSuffix(raw, "```") {
		raw = strings.TrimSuffix(raw, "```")
		raw = strings.TrimSpace(raw)
	}
	raw = strings.TrimSpace(raw)

	// 快速验证是否为有效 JSON
	if !strings.HasPrefix(raw, "{") {
		return nil, fmt.Errorf("Plan Agent output does not start with '{': %q", truncate(raw, 80))
	}

	// 验证可解析
	var tmp interface{}
	if err := json.Unmarshal([]byte(raw), &tmp); err != nil {
		return nil, fmt.Errorf("Plan Agent output is not valid JSON: %w\nRaw (first 200 chars): %s",
			err, truncate(raw, 200))
	}

	return []byte(raw), nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	// 安全截断：确保不会在多字节 UTF-8 字符中间切断
	// 从 maxLen 向前查找最近的合法 rune 边界
	truncated := s[:maxLen]
	for len(truncated) > 0 && !utf8.ValidString(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated + "..."
}
