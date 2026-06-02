package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// ExecutionRequest 工具执行请求
type ExecutionRequest struct {
	ToolUseID string
	ToolName  string
	Input     interface{}
}

// ExecutionResult 工具执行结果
type ExecutionResult struct {
	ToolUseID string
	Content   string
	IsError   bool
}

// Executor 工具执行器 - 负责并发调度与串行化控制
// 核心策略：只读工具可并发（最大N），写入工具串行执行
type Executor struct {
	registry       *Registry
	maxConcurrency int
	logger         *slog.Logger
	permCtx        *PermissionContext

	// askPermission 询问用户权限的回调函数
	askPermission func(ctx context.Context, toolName string, input Input, reason string) (bool, error)

	// useContextTemplate 由上层注入的 UseContext 模板，
	// 每次 Call 时拷贝并填入 WorkingDir/ProjectRoot 后传给工具。
	// 用于把 AskUser/TodoStore/WebSearch 等回调透传到工具内部。
	useContextTemplate UseContext

	// listenerMu 保护 listener 注入；HandleToolEvent 调用在锁外执行，避免回调阻塞调度
	listenerMu sync.RWMutex
	listener   ToolEventListener
}

// NewExecutor 创建工具执行器
func NewExecutor(registry *Registry, maxConcurrency int, logger *slog.Logger) *Executor {
	if maxConcurrency <= 0 {
		maxConcurrency = 10 // 默认最大并发数
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{
		registry:       registry,
		maxConcurrency: maxConcurrency,
		logger:         logger,
	}
}

// SetUseContextTemplate 注入工具执行上下文的模板
//
// 调用方在 wire 阶段填入 AskUser/TodoStore/WebSearch 等回调；
// Executor 每次 Call 时基于该模板生成具体 UseContext 传给工具。
func (e *Executor) SetUseContextTemplate(tpl UseContext) {
	e.useContextTemplate = tpl
}

// SetPermissionContext 设置权限上下文
func (e *Executor) SetPermissionContext(ctx *PermissionContext) {
	e.permCtx = ctx
}

// SetAskPermission 设置权限询问回调
func (e *Executor) SetAskPermission(fn func(ctx context.Context, toolName string, input Input, reason string) (bool, error)) {
	e.askPermission = fn
}

// SetToolEventListener 注入工具事件监听器；nil 表示禁用。
//
// CLI/REPL 用此把工具调用渲染为对齐的进度行（⏵/✔/✗），
// 替代默认 slog.Info("执行工具", ...) 多 goroutine 并发输出造成的乱序。
func (e *Executor) SetToolEventListener(l ToolEventListener) {
	e.listenerMu.Lock()
	defer e.listenerMu.Unlock()
	e.listener = l
}

// publishToolEvent 在锁外回调监听器，避免回调阻塞调度
func (e *Executor) publishToolEvent(ev ToolEvent) {
	e.listenerMu.RLock()
	l := e.listener
	e.listenerMu.RUnlock()
	if l == nil {
		return
	}
	l.HandleToolEvent(ev)
}

// Execute 执行一批工具调用
// 内部自动分区：只读工具并发执行，写入工具按顺序串行执行
func (e *Executor) Execute(ctx context.Context, requests []ExecutionRequest) ([]ExecutionResult, error) {
	if len(requests) == 0 {
		return nil, nil
	}

	// 分区：只读 vs 写入
	var readOnly []ExecutionRequest
	var readWrite []ExecutionRequest

	for _, req := range requests {
		t, ok := e.registry.Get(req.ToolName)
		if !ok {
			// 工具未找到，直接记录错误结果
			readWrite = append(readWrite, req) // 作为写入处理
			continue
		}
		input := toInput(req.Input)
		if t.IsReadOnly(input) && t.IsConcurrencySafe(input) {
			readOnly = append(readOnly, req)
		} else {
			readWrite = append(readWrite, req)
		}
	}

	results := make([]ExecutionResult, 0, len(requests))
	var mu sync.Mutex

	// 并发执行只读工具
	if len(readOnly) > 0 {
		g, gCtx := errgroup.WithContext(ctx)
		g.SetLimit(e.maxConcurrency)

		for _, req := range readOnly {
			req := req // 闭包捕获
			g.Go(func() error {
				result := e.executeSingle(gCtx, req)
				mu.Lock()
				results = append(results, result)
				mu.Unlock()
				return nil // 不传播单个工具错误
			})
		}

		if err := g.Wait(); err != nil {
			return nil, fmt.Errorf("concurrent tool execution failed: %w", err)
		}
	}

	// 串行执行写入工具
	for _, req := range readWrite {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			result := e.executeSingle(ctx, req)
			results = append(results, result)
		}
	}

	// 按原始请求顺序排列结果
	return e.orderResults(requests, results), nil
}

// executeSingle 执行单个工具调用
func (e *Executor) executeSingle(ctx context.Context, req ExecutionRequest) ExecutionResult {
	// 查找工具
	t, ok := e.registry.Get(req.ToolName)
	if !ok {
		return ExecutionResult{
			ToolUseID: req.ToolUseID,
			Content:   fmt.Sprintf("tool %q not found", req.ToolName),
			IsError:   true,
		}
	}

	input := toInput(req.Input)

	// 验证输入
	if err := t.ValidateInput(input); err != nil {
		return ExecutionResult{
			ToolUseID: req.ToolUseID,
			Content:   fmt.Sprintf("invalid input: %v", err),
			IsError:   true,
		}
	}

	// 权限检查
	if e.permCtx != nil {
		permResult, err := t.CheckPermissions(ctx, input, e.permCtx)
		if err != nil {
			return ExecutionResult{
				ToolUseID: req.ToolUseID,
				Content:   fmt.Sprintf("permission check failed: %v", err),
				IsError:   true,
			}
		}

		switch permResult.Behavior {
		case PermissionDeny:
			return ExecutionResult{
				ToolUseID: req.ToolUseID,
				Content:   fmt.Sprintf("permission denied: %s", permResult.Reason),
				IsError:   true,
			}
		case PermissionAsk:
			if e.askPermission != nil {
				allowed, err := e.askPermission(ctx, req.ToolName, input, permResult.Reason)
				if err != nil || !allowed {
					return ExecutionResult{
						ToolUseID: req.ToolUseID,
						Content:   "user denied permission",
						IsError:   true,
					}
				}
			}
		}
	}

	// 构建工具执行上下文：基于模板拷贝（保留 AskUser/TodoStore/WebSearch 等回调）
	toolCtx := e.useContextTemplate
	toolCtx.WorkingDir = e.getWorkingDir()
	toolCtx.ProjectRoot = e.getProjectRoot()

	// 发布 start 事件供 CLI 渲染器把工具调用对齐为单行（⏵ tool ...）。
	// InputSummary 从 Input 序列化后提取，让用户即时看到"在做什么"。
	// Debug 级日志保留，方便排障；非 verbose 模式下默认 Info 不再打印工具调用，
	// 解决多协程同时打 INFO 造成终端乱序输出的问题。
	startEvent := ToolEvent{
		Phase:     ToolPhaseStart,
		ToolName:  req.ToolName,
		ToolUseID: req.ToolUseID,
		Input:     input,
	}
	// 将 Input 序列化为 JSON，提取工具参数摘要
	if len(input) > 0 {
		if jsonBytes, err := json.Marshal(map[string]interface{}(input)); err == nil {
			startEvent.InputSummary = extractInputSummary(req.ToolName, string(jsonBytes), 80)
		}
	}
	e.publishToolEvent(startEvent)
	e.logger.Debug("执行工具", "tool", req.ToolName, "id", req.ToolUseID)
	startedAt := time.Now()
	result, err := t.Call(ctx, input, &toolCtx)
	elapsed := time.Since(startedAt)
	if err != nil {
		e.logger.Debug("工具执行失败", "tool", req.ToolName, "error", err)
		e.publishToolEvent(ToolEvent{
			Phase:        ToolPhaseFinish,
			ToolName:     req.ToolName,
			ToolUseID:    req.ToolUseID,
			Status:       ToolStatusError,
			Elapsed:      elapsed,
			ErrorMessage: err.Error(),
		})
		return ExecutionResult{
			ToolUseID: req.ToolUseID,
			Content:   fmt.Sprintf("tool execution error: %v", err),
			IsError:   true,
		}
	}

	status := ToolStatusSuccess
	errMsg := ""
	resultLen := 0
	if result != nil {
		resultLen = len(result.Content)
		if result.IsError {
			status = ToolStatusError
			errMsg = result.Content
		}
	}
	e.publishToolEvent(ToolEvent{
		Phase:        ToolPhaseFinish,
		ToolName:     req.ToolName,
		ToolUseID:    req.ToolUseID,
		Status:       status,
		Elapsed:      elapsed,
		ResultLen:    resultLen,
		ErrorMessage: errMsg,
	})

	return ExecutionResult{
		ToolUseID: req.ToolUseID,
		Content:   result.Content,
		IsError:   result.IsError,
	}
}

// orderResults 按原始请求顺序排列结果
func (e *Executor) orderResults(requests []ExecutionRequest, results []ExecutionResult) []ExecutionResult {
	resultMap := make(map[string]ExecutionResult)
	for _, r := range results {
		resultMap[r.ToolUseID] = r
	}

	ordered := make([]ExecutionResult, 0, len(requests))
	for _, req := range requests {
		if r, ok := resultMap[req.ToolUseID]; ok {
			ordered = append(ordered, r)
		}
	}
	return ordered
}

// GetToolDefinitions 获取所有已启用工具的API定义
func (e *Executor) GetToolDefinitions() []interface{} {
	tools := e.registry.GetEnabled()
	defs := make([]interface{}, 0, len(tools))
	for _, t := range tools {
		def := map[string]interface{}{
			"name":         t.Name(),
			"description":  t.Description(),
			"input_schema": t.InputSchema(),
		}
		defs = append(defs, def)
	}
	return defs
}

func (e *Executor) getWorkingDir() string {
	if e.permCtx != nil {
		return e.permCtx.WorkingDir
	}
	return "."
}

func (e *Executor) getProjectRoot() string {
	if e.permCtx != nil {
		return e.permCtx.ProjectRoot
	}
	return "."
}

// toInput 将 interface{} 转换为 Input
func toInput(v interface{}) Input {
	if v == nil {
		return Input{}
	}
	if m, ok := v.(map[string]interface{}); ok {
		return Input(m)
	}
	if m, ok := v.(Input); ok {
		return m
	}
	return Input{}
}
