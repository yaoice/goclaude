package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/yaoice/goclaude/pkg/domain/hook"
)

// 本文件实现"命令式 hook"的加载与执行：把 Claude 风格的 hooks.json
// （插件贡献或用户配置）解析为 hook.Handler 并注册到 hook.Registry。
//
// hooks.json 结构（对齐 Claude Code）：
//
//	{
//	  "hooks": {
//	    "PreToolUse": [
//	      { "matcher": "Bash|Edit",
//	        "hooks": [ {"type":"command","command":"./scripts/audit.sh","timeout":5000} ] }
//	    ],
//	    "SessionStart": [ { "hooks": [ {"type":"command","command":"echo hi"} ] } ]
//	  }
//	}
//
// 执行约定：
//   - 命令通过 stdin 收到 JSON 化的 hook.Context（含 sessionId/toolName 等）；
//   - 命令通过 stdout 输出 JSON {"additionalContext":"...","decision":"block","reason":"..."}；
//     非 JSON 输出则整体作为 additionalContext；
//   - matcher 为正则，匹配 ToolName（PreToolUse/PostToolUse）；为空表示匹配全部。
//
// 安全说明：命令式 hook 仅执行用户显式安装并启用的插件/配置中声明的命令，
// 属于受信任来源；执行带超时上限，环境变量注入 CLAUDE_PLUGIN_ROOT。

// hooksFile 对应 hooks.json 顶层结构。
type hooksFile struct {
	Hooks map[string][]hookMatcher `json:"hooks"`
}

type hookMatcher struct {
	Matcher string          `json:"matcher,omitempty"`
	Hooks   []hookCommand   `json:"hooks"`
	Raw     json.RawMessage `json:"-"`
}

type hookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"` // 毫秒
}

// commandHookOutput hook 命令的标准 stdout JSON。
type commandHookOutput struct {
	AdditionalContext string `json:"additionalContext,omitempty"`
	Decision          string `json:"decision,omitempty"` // "block" 表示阻断
	Reason            string `json:"reason,omitempty"`
}

// LoadHooksFile 解析 hooks.json 并把命令式 hook 注册到 reg。
//
// pluginRoot 用于注入 CLAUDE_PLUGIN_ROOT 环境变量与解析相对命令；可为空。
// 返回注册的 handler 数量。
func LoadHooksFile(path, pluginRoot string, reg *hook.Registry, logger *slog.Logger) (int, error) {
	if reg == nil {
		return 0, fmt.Errorf("hook registry 为空")
	}
	if logger == nil {
		logger = slog.Default()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("读取 hooks 文件失败: %w", err)
	}
	var hf hooksFile
	if err := json.Unmarshal(data, &hf); err != nil {
		return 0, fmt.Errorf("解析 hooks 文件失败: %w", err)
	}

	count := 0
	for eventName, matchers := range hf.Hooks {
		event := hook.Event(eventName)
		for _, mt := range matchers {
			var re *regexp.Regexp
			if strings.TrimSpace(mt.Matcher) != "" {
				re, err = regexp.Compile(mt.Matcher)
				if err != nil {
					logger.Warn("hook matcher 正则无效，跳过", "matcher", mt.Matcher, "error", err)
					continue
				}
			}
			for _, hc := range mt.Hooks {
				if hc.Type != "" && hc.Type != "command" {
					continue // 当前仅支持 command 型 hook
				}
				if strings.TrimSpace(hc.Command) == "" {
					continue
				}
				reg.Register(event, makeCommandHandler(hc, re, pluginRoot, logger))
				count++
			}
		}
	}
	return count, nil
}

// makeCommandHandler 构造一个执行外部命令的 hook.Handler。
func makeCommandHandler(hc hookCommand, matcher *regexp.Regexp, pluginRoot string, logger *slog.Logger) hook.Handler {
	timeout := 30 * time.Second
	if hc.Timeout > 0 {
		timeout = time.Duration(hc.Timeout) * time.Millisecond
	}
	command := hc.Command
	return func(ctx context.Context, hookCtx *hook.Context) (*hook.Result, error) {
		// matcher 仅对带工具名的事件生效；不匹配则跳过
		if matcher != nil && hookCtx != nil && hookCtx.ToolName != "" && !matcher.MatchString(hookCtx.ToolName) {
			return nil, nil
		}

		runCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		cmd := exec.CommandContext(runCtx, "bash", "-c", command)
		cmd.Env = append(os.Environ(), buildHookEnv(pluginRoot, hookCtx)...)
		if payload, err := json.Marshal(hookCtx); err == nil {
			cmd.Stdin = bytes.NewReader(payload)
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			logger.Warn("hook 命令执行失败",
				"command", truncate(command, 80),
				"error", err,
				"stderr", truncate(stderr.String(), 200),
			)
			return nil, fmt.Errorf("hook 命令失败: %w", err)
		}
		return parseHookOutput(stdout.Bytes()), nil
	}
}

func buildHookEnv(pluginRoot string, hookCtx *hook.Context) []string {
	env := []string{}
	if pluginRoot != "" {
		env = append(env, "CLAUDE_PLUGIN_ROOT="+pluginRoot, "GOCLAUDE_PLUGIN_ROOT="+pluginRoot)
	}
	if hookCtx != nil {
		if hookCtx.SessionID != "" {
			env = append(env, "CLAUDE_SESSION_ID="+hookCtx.SessionID)
		}
		if hookCtx.ToolName != "" {
			env = append(env, "CLAUDE_TOOL_NAME="+hookCtx.ToolName)
		}
	}
	return env
}

// parseHookOutput 解析 hook 命令 stdout：优先 JSON，否则整体作为 additionalContext。
func parseHookOutput(out []byte) *hook.Result {
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		return &hook.Result{}
	}
	res := &hook.Result{}
	if trimmed[0] == '{' {
		var parsed commandHookOutput
		if err := json.Unmarshal(trimmed, &parsed); err == nil {
			if parsed.AdditionalContext != "" {
				res.AdditionalContexts = append(res.AdditionalContexts, parsed.AdditionalContext)
			}
			if strings.EqualFold(parsed.Decision, "block") {
				res.Block = true
				res.BlockReason = parsed.Reason
			}
			return res
		}
	}
	res.AdditionalContexts = append(res.AdditionalContexts, string(trimmed))
	return res
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
