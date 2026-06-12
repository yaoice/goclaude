// Package hook — 异步 Hook 注册表
//
// 对齐 src/utils/hooks/AsyncHookRegistry.ts：
//   - 注册待处理的异步 hook 进程
//   - 定时轮询已完成进程的输出
//   - JSON 输出解析（sync 响应 vs async 登记）
//   - 超时 / 终止 / 清理管理
package hook

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// PendingAsyncHook 待处理的异步 hook
type PendingAsyncHook struct {
	ProcessID              string
	HookID                 string
	HookName               string
	HookEvent              string // Event 类型 或 "StatusLine" / "FileSuggestion"
	ToolName               string
	PluginID               string
	StartTime              time.Time
	Timeout                time.Duration
	Command                string
	ResponseAttachmentSent bool
	StopProgressInterval   func()

	// 进程输出访问器
	GetStdout func() string
	GetStderr func() string
	GetStatus func() string // "running" / "completed" / "killed"
	GetResult func() (exitCode int, err error)
	Kill      func()
	Cleanup   func()
}

// AsyncHookRegistry 异步 hook 全局注册表
type AsyncHookRegistry struct {
	mu     sync.RWMutex
	hooks  map[string]*PendingAsyncHook
	logger *slog.Logger
}

// NewAsyncHookRegistry 创建异步 hook 注册表
func NewAsyncHookRegistry(logger *slog.Logger) *AsyncHookRegistry {
	if logger == nil {
		logger = slog.Default()
	}
	return &AsyncHookRegistry{
		hooks:  make(map[string]*PendingAsyncHook),
		logger: logger,
	}
}

// Register 注册一个待处理的异步 hook
func (r *AsyncHookRegistry) Register(hook *PendingAsyncHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks[hook.ProcessID] = hook
}

// GetPending 返回所有尚未发送响应的 hook
func (r *AsyncHookRegistry) GetPending() []*PendingAsyncHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []*PendingAsyncHook
	for _, h := range r.hooks {
		if !h.ResponseAttachmentSent {
			result = append(result, h)
		}
	}
	return result
}

// SyncHookJSONOutput 同步 hook JSON 响应结构
type SyncHookJSONOutput struct {
	AdditionalContext string                 `json:"additionalContext,omitempty"`
	Decision          string                 `json:"decision,omitempty"`
	SystemMessage     string                 `json:"systemMessage,omitempty"`
	Extra             map[string]interface{} `json:"-"`
}

// AsyncResponse 异步 hook 检查结果
type AsyncResponse struct {
	ProcessID string
	Response  SyncHookJSONOutput
	HookName  string
	HookEvent string
	ToolName  string
	PluginID  string
	Stdout    string
	Stderr    string
	ExitCode  *int
}

type checkResult struct {
	response       *AsyncResponse // non-nil means response found
	removeID       string         // non-empty means remove this hook
	isSkip         bool
	isSessionStart bool
}

// CheckForResponses 检查所有待处理 hook 的响应
// 对齐 AsyncHookRegistry.ts:checkForAsyncHookResponses
func (r *AsyncHookRegistry) CheckForResponses() []AsyncResponse {
	r.mu.RLock()
	hooks := make([]*PendingAsyncHook, 0, len(r.hooks))
	for _, h := range r.hooks {
		hooks = append(hooks, h)
	}
	r.mu.RUnlock()

	var results []AsyncResponse
	var sessionStartCompleted bool

	for _, hook := range hooks {
		res := r.processHook(hook)
		switch {
		case res.response != nil:
			results = append(results, *res.response)
			r.mu.Lock()
			delete(r.hooks, hook.ProcessID)
			r.mu.Unlock()
			if res.isSessionStart {
				sessionStartCompleted = true
			}
		case res.removeID != "":
			r.mu.Lock()
			delete(r.hooks, hook.ProcessID)
			r.mu.Unlock()
		case res.isSkip:
			// keep waiting
		}
	}

	if sessionStartCompleted {
		_ = sessionStartCompleted // 上层处理
	}

	return results
}

func (r *AsyncHookRegistry) processHook(hook *PendingAsyncHook) checkResult {
	stdout := hook.GetStdout()
	stderr := hook.GetStderr()

	if hook.GetStatus == nil {
		hook.StopProgressInterval()
		return checkResult{removeID: hook.ProcessID}
	}

	status := hook.GetStatus()

	// 被终止的 hook
	if status == "killed" {
		hook.StopProgressInterval()
		if hook.Cleanup != nil {
			hook.Cleanup()
		}
		return checkResult{removeID: hook.ProcessID}
	}

	// 尚未完成
	if status != "completed" {
		return checkResult{isSkip: true}
	}

	// 已完成但已发送或 stdout 为空
	if hook.ResponseAttachmentSent || strings.TrimSpace(stdout) == "" {
		hook.StopProgressInterval()
		return checkResult{removeID: hook.ProcessID}
	}

	// 解析 stdout 寻找 sync 响应 JSON
	var response SyncHookJSONOutput
	lines := strings.Split(stdout, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "{") {
			var parsed map[string]interface{}
			if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
				continue
			}
			// 非 async 标记的行视为 sync 响应
			if _, isAsync := parsed["async"]; !isAsync {
				if raw, err := json.Marshal(parsed); err == nil {
					_ = json.Unmarshal(raw, &response)
				}
				break
			}
		}
	}

	hook.ResponseAttachmentSent = true

	// 发送响应事件
	exitCode, _ := hook.GetResult()
	exitCodePtr := &exitCode
	finalizeOutcome := func() string {
		if exitCode == 0 {
			return "success"
		}
		return "error"
	}()

	EmitHookResponse(
		hook.HookID, hook.HookName, hook.HookEvent,
		stdout, stderr, stdout+stderr,
		exitCodePtr, finalizeOutcome,
	)

	return checkResult{
		response: &AsyncResponse{
			ProcessID: hook.ProcessID,
			Response:  response,
			HookName:  hook.HookName,
			HookEvent: hook.HookEvent,
			ToolName:  hook.ToolName,
			PluginID:  hook.PluginID,
			Stdout:    stdout,
			Stderr:    stderr,
			ExitCode:  exitCodePtr,
		},
		isSessionStart: hook.HookEvent == "SessionStart",
	}
}

// RemoveDelivered 移除已发送的 hook
func (r *AsyncHookRegistry) RemoveDelivered(processIDs []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, pid := range processIDs {
		if hook, ok := r.hooks[pid]; ok && hook.ResponseAttachmentSent {
			hook.StopProgressInterval()
			delete(r.hooks, pid)
		}
	}
}

// FinalizeAll 终止所有 pending hook
// 对齐 AsyncHookRegistry.ts:finalizePendingAsyncHooks
func (r *AsyncHookRegistry) FinalizeAll(ctx context.Context) {
	r.mu.Lock()
	hooks := make([]*PendingAsyncHook, 0, len(r.hooks))
	for _, h := range r.hooks {
		hooks = append(hooks, h)
	}
	r.mu.Unlock()

	for _, hook := range hooks {
		if hook.GetStatus != nil && hook.GetStatus() == "completed" {
			exitCode, _ := hook.GetResult()
			exitCodePtr := &exitCode
			outcome := "success"
			if exitCode != 0 {
				outcome = "error"
			}
			EmitHookResponse(
				hook.HookID, hook.HookName, hook.HookEvent,
				hook.GetStdout(), hook.GetStderr(), hook.GetStdout()+hook.GetStderr(),
				exitCodePtr, outcome,
			)
		} else {
			if hook.GetStatus != nil && hook.GetStatus() != "killed" && hook.Kill != nil {
				hook.Kill()
			}
			exitCode := 1
			EmitHookResponse(
				hook.HookID, hook.HookName, hook.HookEvent,
				hook.GetStdout(), hook.GetStderr(), hook.GetStdout()+hook.GetStderr(),
				&exitCode, "cancelled",
			)
		}
	}

	r.mu.Lock()
	r.hooks = make(map[string]*PendingAsyncHook)
	r.mu.Unlock()
}

// ClearAll 清空所有 hook（测试用）
func (r *AsyncHookRegistry) ClearAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, hook := range r.hooks {
		hook.StopProgressInterval()
	}
	r.hooks = make(map[string]*PendingAsyncHook)
}

// Count 返回注册数量
func (r *AsyncHookRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.hooks)
}
