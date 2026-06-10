// Package hooks — 长期记忆生命周期钩子
//
// 对齐 claude-mem 的 6 个生命周期钩子（SessionStart, UserPromptSubmit, PostToolUse, Stop, SessionEnd）。
// 当前实现覆盖核心三个：
//   - SessionStart:     检索相关历史记忆，注入 AdditionalContext
//   - PostToolUse:      自动捕获工具观察记录并保存为长期记忆
//   - SessionEnd:       保存当前会话摘要并标记会话结束
//
// 钩子通过现有 hook.Registry.Register() 注册，出错隔离，不阻塞主流程。
package hooks

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/anthropics/goclaude/pkg/domain/hook"
	memoryapp "github.com/anthropics/goclaude/pkg/application/memory"
	"github.com/anthropics/goclaude/pkg/infrastructure/appconfig"
)

// MemoryLifecycleHooks 长期记忆生命周期钩子
type MemoryLifecycleHooks struct {
	svc        *memoryapp.LongTermMemoryService
	cfg        appconfig.LongTermMemoryConfig
	logger     *slog.Logger
	projectRoot  string
	lastQuery    string
	toolCallCount int
}

// NewMemoryLifecycleHooks 创建记忆生命周期钩子
func NewMemoryLifecycleHooks(
	svc *memoryapp.LongTermMemoryService,
	cfg appconfig.LongTermMemoryConfig,
	logger *slog.Logger,
	projectRoot string,
) *MemoryLifecycleHooks {
	if logger == nil {
		logger = slog.Default()
	}
	return &MemoryLifecycleHooks{
		svc:        svc,
		cfg:        cfg,
		logger:     logger,
		projectRoot: projectRoot,
	}
}

// ============================================================
// SessionStart 钩子
// ============================================================

// SessionStartHandler 返回 SessionStart 事件处理器
//
// 行为：
//   - 检索与当前项目路径相关的历史记忆
//   - 以 AdditionalContext 形式注入到对话中
//   - 注入量受 max_inject_tokens 控制
func (h *MemoryLifecycleHooks) SessionStartHandler() hook.Handler {
	return func(ctx context.Context, hookCtx *hook.Context) (*hook.Result, error) {
		if !h.cfg.Enabled || !h.cfg.Injection.AutoInject {
			return nil, nil
		}

		// 构建检索查询：基于 session ID 和项目路径
		query := fmt.Sprintf("%s %s", h.projectRoot, hookCtx.SessionID)

		injectionCtx, err := h.svc.BuildInjectionContext(ctx, h.projectRoot, query)
		if err != nil {
			h.logger.Warn("session start memory injection failed", "error", err)
			return nil, nil
		}

		if injectionCtx == "" {
			return nil, nil
		}

		h.logger.Info("injected long-term memory context",
			"session", hookCtx.SessionID,
			"project", h.projectRoot,
		)

		return &hook.Result{
			AdditionalContexts: []string{injectionCtx},
		}, nil
	}
}

// ============================================================
// PostToolUse 钩子
// ============================================================

// toolNameTitleMap 工具名 → 观察记录标题模板
var toolNameTitleMap = map[string]string{
	"read_file":         "Read: %s",
	"write_to_file":     "Edited: %s",
	"replace_in_file":   "Edited: %s",
	"execute_command":   "Command: %s",
	"search_content":    "Searched: %s",
	"codebase_search":   "Explored: %s",
	"list_files":        "Listed: %s",
	"file_search":       "Found: %s",
	"web_search":        "Web: %s",
	"web_fetch":         "Fetched: %s",
	"delete_files":      "Deleted: %s",
	"task":              "Subagent: %s",
	"team_create":       "Team: %s",
	"preview_url":       "Preview: %s",
	"read_lints":        "Diagnostics: %s",
	"Skill":             "Skill: %s",
}

// PostToolUseHandler 返回 PostToolUse 事件处理器
//
// 行为：
//   - 自动捕获工具调用产生的观察记录
//   - 记录标题格式：<ToolName>: <key detail>
//   - 最小结果长度过滤，避免噪声
//   - 异步保存，不阻塞主流程
func (h *MemoryLifecycleHooks) PostToolUseHandler() hook.Handler {
	return func(ctx context.Context, hookCtx *hook.Context) (*hook.Result, error) {
		if !h.cfg.Enabled || !h.cfg.Capture.AutoCaptureTools {
			return nil, nil
		}

		h.toolCallCount++

		// 工具名未知或不被追踪的跳过
		if hookCtx.ToolName == "" {
			return nil, nil
		}

		// 从 Extra 中提取工具结果
		resultStr := extractResult(hookCtx.Extra)
		if len(resultStr) < h.cfg.Capture.MinCaptureChars {
			return nil, nil
		}

		// 构建标题
		title := buildObsTitle(hookCtx.ToolName, hookCtx.Extra)
		if title == "" {
			title = fmt.Sprintf("%s call #%d", hookCtx.ToolName, h.toolCallCount)
		}

		// 构建会话 ID
		sessionID := hookCtx.SessionID
		if sessionID == "" {
			sessionID = hookCtx.AgentID
		}

		// 异步保存（go routine 以避免阻塞主流程）
		go func() {
			saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_, err := h.svc.SaveObservation(saveCtx,
				sessionID,
				title,
				resultStr,
				memoryapp.SaveOptions{
					ObsType:  "observation",
					Category: "project",
					Source:   "tool_use",
					ToolName: hookCtx.ToolName,
					Priority: 40, // 工具观察默认中等优先级
					Tags:     []string{"tool:" + hookCtx.ToolName},
				},
			)
			if err != nil {
				h.logger.Warn("post tool use memory capture failed",
					"tool", hookCtx.ToolName,
					"error", err,
				)
			}
		}()

		return nil, nil
	}
}

// ============================================================
// SessionEnd 钩子
// ============================================================

// SessionEndHandler 返回 SessionEnd 事件处理器
//
// 行为：
//   - 保存当前会话摘要
//   - 更新会话统计（token、回合数等）
func (h *MemoryLifecycleHooks) SessionEndHandler() hook.Handler {
	return func(ctx context.Context, hookCtx *hook.Context) (*hook.Result, error) {
		if !h.cfg.Enabled {
			return nil, nil
		}

		// 从 Extra 中提取会话统计信息
		stats := extractSessionStats(hookCtx.Extra)
		summary := extractString(hookCtx.Extra, "summary")
		if summary == "" {
			summary = fmt.Sprintf("Session ended with %d tool calls.", h.toolCallCount)
		}

		sessionID := hookCtx.SessionID
		if sessionID == "" {
			sessionID = hookCtx.AgentID
		}

		// 同步保存会话摘要（SessionEnd 时阻塞，确保不丢失数据）
		if err := h.svc.SaveSessionSummary(ctx, sessionID, h.projectRoot, summary, stats); err != nil {
			h.logger.Warn("session end memory summary failed",
				"session", sessionID,
				"error", err,
			)
		} else {
			h.logger.Info("saved session summary to long-term memory",
				"session", sessionID,
				"turns", stats.TurnCount,
			)
		}

		return nil, nil
	}
}

// ============================================================
// UserPromptSubmit 钩子（记录用户最后查询）
// ============================================================

// UserPromptSubmitHandler 返回 UserPromptSubmit 事件处理器
//
// 行为：
//   - 记录用户最后提交的查询，用于 SessionStart 时更精准的检索
func (h *MemoryLifecycleHooks) UserPromptSubmitHandler() hook.Handler {
	return func(ctx context.Context, hookCtx *hook.Context) (*hook.Result, error) {
		if !h.cfg.Enabled {
			return nil, nil
		}

		// 从 Extra 中提取用户输入
		if prompt, ok := extractStringOk(hookCtx.Extra, "prompt"); ok && prompt != "" {
			h.lastQuery = prompt
		}
		return nil, nil
	}
}

// LastQuery 返回用户最近一次查询
func (h *MemoryLifecycleHooks) LastQuery() string {
	return h.lastQuery
}

// RegisterAll 向 Registry 注册所有长期记忆钩子
func (h *MemoryLifecycleHooks) RegisterAll(registry *hook.Registry) {
	if registry == nil {
		return
	}

	registry.Register(hook.EventSessionStart, h.SessionStartHandler())
	registry.Register(hook.EventPostToolUse, h.PostToolUseHandler())
	registry.Register(hook.EventSessionEnd, h.SessionEndHandler())
	registry.Register(hook.EventUserPromptSubmit, h.UserPromptSubmitHandler())

	h.logger.Info("registered long-term memory lifecycle hooks",
		"sessionStart", registry.Count(hook.EventSessionStart),
		"postToolUse", registry.Count(hook.EventPostToolUse),
		"sessionEnd", registry.Count(hook.EventSessionEnd),
		"userPromptSubmit", registry.Count(hook.EventUserPromptSubmit),
	)
}

// ============================================================
// 辅助函数：从 hookCtx.Extra 提取数据
// ============================================================

// extractResult 从 Extra 中提取工具执行结果文本
func extractResult(extra map[string]interface{}) string {
	if extra == nil {
		return ""
	}
	if v, ok := extractStringOk(extra, "result"); ok && v != "" {
		return v
	}
	if v, ok := extractStringOk(extra, "output"); ok && v != "" {
		return v
	}
	if v, ok := extractStringOk(extra, "content"); ok && v != "" {
		return v
	}
	return ""
}

// buildObsTitle 根据工具名和 Extra 构建观察记录标题
func buildObsTitle(toolName string, extra map[string]interface{}) string {
	tmpl, ok := toolNameTitleMap[toolName]
	if !ok {
		return ""
	}

	// 尝试提取路径/文件名作为标题内容
	detail := ""

	// 常见字段名（按优先级）
	detailKeys := []string{"path", "file_path", "filepath", "filePath", "target_file",
		"command", "query", "pattern", "url", "target_directory"}
	for _, key := range detailKeys {
		if v, ok := extractStringOk(extra, key); ok && v != "" {
			detail = v
			break
		}
	}
	// 从 tool_input 中提取
	if detail == "" {
		if input, ok := extra["tool_input"].(map[string]interface{}); ok {
			for _, key := range detailKeys {
				if v, ok := extractStringOk(input, key); ok && v != "" {
					detail = v
					break
				}
			}
		}
	}

	if detail == "" {
		return ""
	}

	// 截断过长的路径/命令
	if len(detail) > 80 {
		detail = "..." + detail[len(detail)-77:]
	}

	return fmt.Sprintf(tmpl, detail)
}

// extractSessionStats 从 Extra 提取会话统计信息
func extractSessionStats(extra map[string]interface{}) memoryapp.SessionStats {
	stats := memoryapp.SessionStats{
		StartedAt: time.Now().Add(-time.Hour), // 默认 1 小时前
	}

	if extra == nil {
		return stats
	}

	if v, ok := extra["input_tokens"]; ok {
		stats.InputTokens = toInt(v)
	}
	if v, ok := extra["output_tokens"]; ok {
		stats.OutputTokens = toInt(v)
	}
	if v, ok := extra["turn_count"]; ok {
		stats.TurnCount = toInt(v)
	}
	if v, ok := extra["started_at"]; ok {
		if t, ok := v.(time.Time); ok {
			stats.StartedAt = t
		}
	}

	return stats
}

func extractString(extra map[string]interface{}, key string) string {
	if v, ok := extractStringOk(extra, key); ok {
		return v
	}
	return ""
}

func extractStringOk(extra map[string]interface{}, key string) (string, bool) {
	if extra == nil {
		return "", false
	}
	v, ok := extra[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func toInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		// 简单解析
		var result int
		fmt.Sscanf(n, "%d", &result)
		return result
	}
	return 0
}
