// Package cli - permission_mode.go 集中处理 REPL / run 启动时的初始 permission mode。
//
// 解决用户痛点："为何每次都触发 Permission required，默认都开不行吗"
//
// 答疑：默认弹窗是有意为之，与 src（Claude Code）安全模型对齐——LLM 不该在用户
// 没看见的情况下做 file_edit / file_write / bash 写操作。但确实需要给用户一个
// 明确的"我相信我的工具，别每次都问"的开关，避免每次重启 REPL 都重新点 [s]。
//
// 优先级（高 → 低）：
//  1. --dangerously-skip-permissions   （命令行 flag，最高优先；与官方 CLI 对齐）
//  2. configs/default.yaml 等 permissions.mode 字段（统一 YAML 配置）
//  3. tool.PermissionModeDefault       （未设置时的安全默认）
//
// 配置错值（如 mode: foo）会回退到 default 并打 Warn，
// 而不是启动失败——避免 typo 让用户被锁在外面。
package cli

import (
	"log/slog"
	"strings"

	"github.com/anthropics/goclaude/internal/domain/tool"
)

// resolveInitialPermissionMode 按 flag → yaml → default 优先级返回初始模式。
//
// 解析后会以 slog.Default() 打 Debug，便于 -v 启动时排障。
func resolveInitialPermissionMode(bypassFlag bool) tool.PermissionMode {
	// 1) flag 最高优先：与官方 CLI "--dangerously-skip-permissions" 对齐
	if bypassFlag {
		slog.Default().Debug("permission mode resolved",
			"source", "flag",
			"mode", string(tool.PermissionModeBypass),
		)
		return tool.PermissionModeBypass
	}
	// 2) YAML 配置
	if raw := AppConfig().Permissions.Mode; raw != "" {
		mode, ok := parsePermissionMode(raw)
		if !ok {
			slog.Default().Warn("ignoring invalid permission mode in config",
				"key", "permissions.mode",
				"value", raw,
				"valid", []string{"default", "acceptEdits", "plan", "bypass"},
			)
			return tool.PermissionModeDefault
		}
		slog.Default().Debug("permission mode resolved",
			"source", "yaml",
			"mode", string(mode),
		)
		return mode
	}
	// 3) 安全默认：写入工具弹窗
	return tool.PermissionModeDefault
}

// parsePermissionMode 解析环境变量字符串为 tool.PermissionMode；大小写/驼峰不敏感。
//
// 接受的别名：
//   - "default" / ""                      → Default
//   - "acceptedits" / "accept-edits" / "auto-edit" → AcceptEdits
//   - "plan"                              → Plan
//   - "bypass" / "skip" / "yolo"          → Bypass
//
// 这些别名是为了用户体验：很多人记不住 camelCase；也支持 kebab-case 与口语词。
func parsePermissionMode(s string) (tool.PermissionMode, bool) {
	norm := strings.ToLower(strings.TrimSpace(s))
	// 一并去掉 "-" / "_" 让 "accept-edits" / "accept_edits" / "acceptedits" 都成立
	norm = strings.ReplaceAll(norm, "-", "")
	norm = strings.ReplaceAll(norm, "_", "")
	switch norm {
	case "", "default":
		return tool.PermissionModeDefault, true
	case "acceptedits", "autoedit", "autoedits":
		return tool.PermissionModeAcceptEdits, true
	case "plan":
		return tool.PermissionModePlan, true
	case "bypass", "skip", "yolo":
		return tool.PermissionModeBypass, true
	}
	return tool.PermissionModeDefault, false
}
