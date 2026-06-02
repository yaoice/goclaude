package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/goclaude/internal/domain/agent"
	"github.com/anthropics/goclaude/internal/domain/hook"
	"github.com/anthropics/goclaude/internal/domain/query"
	"github.com/anthropics/goclaude/internal/domain/tool"
	"github.com/anthropics/goclaude/internal/infrastructure/memory"
	"github.com/anthropics/goclaude/internal/infrastructure/worktree"
)

// 本文件聚合 AgentService 的执行路径与相关 helper：
//   - Run / RunOptions / RunResult / AgentEngineFactory（subagent 执行编排）
//   - 进度探针（installProgressProbe / closeProgressProbe / previewFirstLine）
//   - 工具过滤与模型解析（FilterTools / reservedSubagentTools / resolveModel / newAgentID）
//
// 从 agent_service.go 拆出以提升可读性；逻辑保持不变。

// RunOptions 执行 subagent 的可选参数
type RunOptions struct {
	// Prompt 给 subagent 的用户输入
	Prompt string
	// ParentSessionID 父会话 ID（用于追踪）
	ParentSessionID string
	// WorkingDir 工作目录
	WorkingDir string
	// ProjectRoot 项目根目录
	ProjectRoot string
	// ModelOverride 显式指定模型；为空且 Definition.Model = "inherit" 时用 defaultModel
	ModelOverride string
	// DefaultModel 父 agent 当前使用的模型
	DefaultModel string
	// MaxTurns 上限；0 表示用 definition 或引擎默认
	MaxTurns int
	// EventSink 接收流式事件的可选 channel；nil 时丢弃
	EventSink chan<- query.StreamEvent
	// ForkContextMessages 父 agent 的消息历史；非空则 subagent 继承上下文。
	// 内部会先过滤掉未配对的 tool_use 消息以避免 API 错误。
	// 对齐 src runAgent.forkContextMessages 的语义。
	ForkContextMessages []query.Message
	// PreloadedSkills 启动时作为 user message 注入的 skill 内容（已渲染）
	PreloadedSkills []PreloadedSkill
}

// PreloadedSkill subagent 启动前预加载的 skill 内容
type PreloadedSkill struct {
	Name    string
	Content string
}

// definitionSkills 合并调用方显式预加载的 skill 与 agent 定义中声明的 skills。
func (s *AgentService) definitionSkills(def *agent.Definition, opts RunOptions) []PreloadedSkill {
	out := append([]PreloadedSkill(nil), opts.PreloadedSkills...)
	if def == nil || len(def.Skills) == 0 || s.skillSvc == nil {
		return out
	}
	seen := make(map[string]bool, len(out)+len(def.Skills))
	for _, sk := range out {
		seen[sk.Name] = true
	}
	for _, name := range def.Skills {
		if name == "" || seen[name] {
			continue
		}
		content, ok := s.skillSvc.RenderWith(name, RenderContext{
			SessionID:  opts.ParentSessionID,
			ProjectDir: opts.ProjectRoot,
			Cwd:        opts.WorkingDir,
		})
		if !ok || strings.TrimSpace(content) == "" {
			s.logger.Warn("subagent skill 预加载失败", "agent", def.AgentType, "skill", name)
			continue
		}
		out = append(out, PreloadedSkill{Name: name, Content: content})
		seen[name] = true
	}
	return out
}

// RunResult subagent 执行结果
type RunResult struct {
	AgentID    string
	AgentType  string
	FinalText  string
	StopReason query.StopReason
	TurnCount  int
}

// AgentEngineFactory 用于按 subagent 配置生成定制 query.Engine 的工厂
//
// 调用方负责：
//   - 用 subagent 的 SystemPrompt 与工具白名单/黑名单装配新 Engine
//   - 复用底层 AIProvider 与 token budget
type AgentEngineFactory interface {
	NewSubagentEngine(def *agent.Definition, opts RunOptions) (*query.Engine, error)
}

// Run 执行一个 subagent 并返回最终结果
//
// 这是 src runAgent 的简化版：核心循环 + 可选 Memory/Worktree。
func (s *AgentService) Run(
	ctx context.Context,
	agentType string,
	factory AgentEngineFactory,
	opts RunOptions,
) (*RunResult, error) {
	def, ok := s.registry.Get(agentType)
	if !ok {
		return nil, fmt.Errorf("agent %q not found", agentType)
	}
	agentID := newAgentID()

	// === 可选：Worktree 隔离 ===
	var wt *worktree.Worktree
	if def.Isolation == "worktree" && s.wtSvc != nil {
		srcDir := opts.WorkingDir
		if srcDir == "" {
			srcDir = opts.ProjectRoot
		}
		w, err := s.wtSvc.Create(ctx, srcDir, agentID)
		if err != nil {
			s.logger.Warn("worktree 创建失败，回退到原目录", "error", err)
		} else {
			wt = w
			opts.WorkingDir = w.Path
			s.logger.Debug("worktree 已创建", "path", w.Path, "branch", w.Branch)
		}
	}
	defer func() {
		if wt != nil {
			if err := wt.Cleanup(context.Background()); err != nil {
				s.logger.Warn("worktree 清理失败", "error", err)
			}
		}
	}()

	// === 可选：Memory 加载 ===
	memoryPrompt := ""
	if def.Memory != "" && s.memorySvc != nil {
		scope := memory.Scope(def.Memory)
		mp, err := s.memorySvc.LoadPrompt(def.AgentType, scope)
		if err != nil {
			s.logger.Warn("加载 memory 失败", "agent", def.AgentType, "scope", def.Memory, "error", err)
		} else if mp != "" {
			memoryPrompt = mp
			s.logger.Debug("memory 已加载", "agent", def.AgentType, "scope", def.Memory, "len", len(mp))
		}
	}

	// 把 memory 内容拼到 Definition 的 system prompt 之后（不修改原对象，做一份副本）
	defForRun := *def
	if memoryPrompt != "" {
		defForRun.SystemPrompt = strings.TrimSpace(def.ResolvedSystemPrompt()) +
			"\n\n## Persistent Memory\n\n" + memoryPrompt
		defForRun.GetSystemPrompt = nil // 副本里关掉动态版本，避免覆盖
	}

	resolvedModel := resolveModel(&defForRun, opts)
	s.logger.Debug("subagent 启动",
		"agent_id", agentID,
		"agent_type", agentType,
		"parent_session", opts.ParentSessionID,
		"model", resolvedModel,
		"isolation", def.Isolation,
		"memory", def.Memory,
	)
	s.publishSubagent(SubagentEvent{
		Phase:         SubagentPhaseStart,
		AgentID:       agentID,
		AgentType:     agentType,
		Model:         resolvedModel,
		Memory:        def.Memory,
		Isolation:     def.Isolation,
		ParentSession: opts.ParentSessionID,
	})
	startedAt := time.Now()

	engine, err := factory.NewSubagentEngine(&defForRun, opts)
	if err != nil {
		return nil, fmt.Errorf("build subagent engine: %w", err)
	}

	// 组装 initial messages：fork 上下文 → 预加载 skill → 用户 prompt
	var initialMessages []query.Message
	if len(opts.ForkContextMessages) > 0 {
		initialMessages = append(initialMessages, FilterIncompleteToolCalls(opts.ForkContextMessages)...)
	}
	for _, sk := range s.definitionSkills(&defForRun, opts) {
		initialMessages = append(initialMessages, query.NewTextMessage(
			query.RoleUser,
			fmt.Sprintf("<skill name=%q>\n%s\n</skill>", sk.Name, sk.Content),
		))
	}
	initialMessages = append(initialMessages, query.NewTextMessage(query.RoleUser, opts.Prompt))

	// 触发 SubagentStart hooks，把额外 context 注入到 messages 头部
	if s.hooks != nil {
		hookCtx := &hook.Context{
			SessionID: opts.ParentSessionID,
			AgentID:   agentID,
			AgentType: agentType,
		}
		if res := s.hooks.Run(ctx, hook.EventSubagentStart, hookCtx); res != nil {
			for _, extra := range res.AdditionalContexts {
				initialMessages = append(
					[]query.Message{query.NewTextMessage(query.RoleUser, extra)},
					initialMessages...,
				)
			}
		}
	}

	// SubagentStop 在退出（无论成功失败）时触发，用 defer 保证执行
	defer func() {
		if s.hooks == nil {
			return
		}
		s.hooks.Run(context.Background(), hook.EventSubagentStop, &hook.Context{
			SessionID: opts.ParentSessionID,
			AgentID:   agentID,
			AgentType: agentType,
		})
	}()

	// 包一层 EventSink：转发到调用方的同时观测节拍，向 SubagentEventListener
	// 推送 Progress 事件（携带轮数 + 本轮最后调用的工具名），让 UI 有"心跳"。
	// 此 wrapper 在 Engine.Execute 返回前完成所有发送；之后再关闭 wrapper 通道，
	// 让转发 goroutine 退出。
	wrappedSink, progressDone := s.installProgressProbe(opts.EventSink, SubagentEvent{
		AgentID:       agentID,
		AgentType:     agentType,
		Model:         resolvedModel,
		Memory:        def.Memory,
		Isolation:     def.Isolation,
		ParentSession: opts.ParentSessionID,
	})

	qr, err := engine.Execute(ctx, initialMessages, wrappedSink)
	// 关闭 wrapper 让转发 goroutine 终止；用 helper 屏蔽 nil 与 send-on-closed
	closeProgressProbe(wrappedSink, progressDone)
	if err != nil {
		s.logger.Warn("subagent 执行失败", "agent_id", agentID, "error", err)
		s.publishSubagent(SubagentEvent{
			Phase:         SubagentPhaseFinish,
			AgentID:       agentID,
			AgentType:     agentType,
			Model:         resolvedModel,
			Memory:        def.Memory,
			Isolation:     def.Isolation,
			ParentSession: opts.ParentSessionID,
			Status:        SubagentStatusError,
			Elapsed:       time.Since(startedAt),
			ErrorMessage:  err.Error(),
		})
		return nil, err
	}

	finalText := qr.Response.GetTextContent()
	result := &RunResult{
		AgentID:    agentID,
		AgentType:  agentType,
		FinalText:  finalText,
		StopReason: qr.StopReason,
		TurnCount:  qr.TurnCount,
	}
	s.logger.Debug("subagent 完成",
		"agent_id", agentID,
		"turns", qr.TurnCount,
		"stop_reason", qr.StopReason,
	)
	s.publishSubagent(SubagentEvent{
		Phase:         SubagentPhaseFinish,
		AgentID:       agentID,
		AgentType:     agentType,
		Model:         resolvedModel,
		Memory:        def.Memory,
		Isolation:     def.Isolation,
		ParentSession: opts.ParentSessionID,
		Status:        SubagentStatusSuccess,
		Elapsed:       time.Since(startedAt),
		Turns:         qr.TurnCount,
		ResultPreview: previewFirstLine(finalText, 80),
	})
	return result, nil
}

// installProgressProbe 包一层 EventSink 用于探测 subagent 每轮节拍。
//
// 返回：
//   - wrapped: 替代 Engine.Execute 使用的 chan，转发到原 sink 同时被探针观测；
//     若调用方传入了 nil sink，仍会返回一个非 nil 内部 chan，让 Engine 正常工作。
//   - done: 转发 goroutine 退出信号；由 closeProgressProbe 触发 close(wrapped) 后被关闭。
//
// 探针语义：
//   - 看到 ContentTypeToolUse 的 BlockStart → 记录为本轮 LastTool（覆盖式）
//   - 看到 ContentTypeToolUse 的 BlockDelta → 累积 partial JSON，解析参数摘要
//   - 看到 EventMessageDelta + 非空 StopReason → 一轮完成，发 Progress；
//     Turns 由探针自己累加（每收到一个 MessageDelta+StopReason 自增 1）
//   - 看到 EventError → 静默吞掉（Engine 自己会把 err 通过返回值上抛）
func (s *AgentService) installProgressProbe(
	caller chan<- query.StreamEvent,
	base SubagentEvent,
) (chan query.StreamEvent, chan struct{}) {
	// 缓冲足够大：Engine 一轮平均几十个 delta，64 已留余量；
	// 进一步的背压由调用方 sink 处理（若 caller=nil 则丢弃，不阻塞）。
	wrapped := make(chan query.StreamEvent, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		var lastTool string
		var lastToolPartial string   // 当前轮次最后工具的累积 partial JSON
		var lastToolDetail string    // 从 partial JSON 提取的参数摘要
		currentToolIdx := -1        // 正在追踪的工具 block index
		turn := 0
		for ev := range wrapped {
			// 1) 探测：tool_use 开始 → 记下工具名，重置 partial
			if ev.Type == query.EventContentBlockStart &&
				ev.ContentBlock != nil &&
				ev.ContentBlock.Type == query.ContentTypeToolUse {
				lastTool = ev.ContentBlock.ToolName
				lastToolPartial = ""
				lastToolDetail = ""
				currentToolIdx = ev.Index
			}
			// 2) 探测：tool_use delta → 累积 partial JSON
			if ev.Type == query.EventContentBlockDelta &&
				ev.Delta != nil &&
				ev.Delta.Type == query.ContentTypeToolUse &&
				ev.Index == currentToolIdx &&
				ev.Delta.PartialJSON != "" {
				lastToolPartial += ev.Delta.PartialJSON
			}
			// 3) 探测：tool_use block 结束 → 解析参数摘要
			if ev.Type == query.EventContentBlockStop &&
				ev.Index == currentToolIdx &&
				lastTool != "" &&
				lastToolPartial != "" {
				lastToolDetail = extractAgentToolSummary(lastTool, lastToolPartial, 60)
			}
			// 4) 探测：一轮 stop_reason 到达 → publish Progress
			if ev.Type == query.EventMessageDelta && ev.StopReason != "" {
				turn++
				e := base
				e.Phase = SubagentPhaseProgress
				e.Turns = turn
				e.LastTool = lastTool
				e.LastToolDetail = lastToolDetail
				s.publishSubagent(e)
				// 进入下一轮，清空 LastTool（避免上一轮的工具残留到没有工具调用的轮次）
				lastTool = ""
				lastToolPartial = ""
				lastToolDetail = ""
				currentToolIdx = -1
			}
			// 5) 透传给调用方（若有）
			if caller != nil {
				// 非阻塞转发：调用方堵了不让 subagent 也堵
				select {
				case caller <- ev:
				default:
				}
			}
		}
	}()
	return wrapped, done
}

// closeProgressProbe 安全关闭探针；nil-safe 且只关闭一次。
func closeProgressProbe(wrapped chan query.StreamEvent, done chan struct{}) {
	if wrapped == nil {
		return
	}
	close(wrapped)
	if done != nil {
		<-done
	}
}

// previewFirstLine 取字符串首个非空行并截断到 max 个 rune（保留可读性）。
// 用于 SubagentEvent.ResultPreview：让 UI 在主对话之外快速看到 subagent 输出摘要。
func previewFirstLine(s string, max int) string {
	if max <= 0 || s == "" {
		return ""
	}
	// 找首个非空行
	line := ""
	for _, ln := range strings.Split(s, "\n") {
		t := strings.TrimSpace(ln)
		if t != "" {
			line = t
			break
		}
	}
	if line == "" {
		return ""
	}
	rs := []rune(line)
	if len(rs) <= max {
		return line
	}
	if max <= 1 {
		return string(rs[:max])
	}
	return string(rs[:max-1]) + "…"
}

// reservedSubagentTools 列出**会破坏 subagent 上下文隔离**的工具名。
//
// 这些工具一旦让 subagent 持有：
//   - "Agent"/"Task"/"agent"：subagent 又能启动 subagent → 二级 fan-out 无法被 UI 跟踪、
//     深度不受控；同时违反"子 ↔ 子不直接通信"的设计约束。
//   - "team_create"/"team_delete"/"send_message"：team 工具组允许 subagent 之间发消息，
//     直接打破"主进程是唯一调度者"的核心契约。
//
// 默认情况下，subagent 的工具集会移除这些条目（即使 Definition.Tools 显式列出也无效）。
// 仅当 Definition 显式设置 `AllowSubagentChaining=true`（极少数高阶编排场景，比如
// 主 agent 想让某个"调度型 subagent"分发工作）时，才允许保留。
//
// 注：所有比对均按工具 Name() 做精确匹配；MCP 工具命名为 mcp__<server>__<tool>，
// 不会落入此集合。
var reservedSubagentTools = map[string]struct{}{
	"Agent":        {},
	"Task":         {},
	"agent":        {},
	"team_create":  {},
	"team_delete":  {},
	"send_message": {},
}

// IsReservedSubagentTool 暴露给外部（如自定义 Factory）做对齐检查。
func IsReservedSubagentTool(name string) bool {
	_, ok := reservedSubagentTools[name]
	return ok
}

// FilterTools 按 subagent 的 Tools / DisallowedTools 过滤可用工具
//
// 规则（与 src resolveAgentTools 对齐，并叠加隔离保留集）：
//  1. 隔离保留集（reservedSubagentTools）默认硬剔除——不论 Definition.Tools 是否
//     显式列出。仅当 def.AllowSubagentChaining == true 时跳过此剔除，
//     用于极少数"主 agent 想让 subagent 再分发"的高阶编排。
//  2. Definition.DisallowedTools 在保留集之后再做一次剔除。
//  3. 若 Definition.Tools 非空 → 取白名单交集；为 nil → 继承父全部工具。
//
// 设计动机：上下文隔离的核心是"子 ↔ 子不通信、不递归调度"。在工具层强制移除
// Agent/Task/team_*/send_message，比依赖 Definition 显式列举 DisallowedTools 更安全，
// 也对齐文章中"权限过松：审查代理意外修改代码 → 规避方法：严格配置 tools 白名单"的最佳实践。
func FilterTools(parent []tool.Tool, def *agent.Definition) []tool.Tool {
	if def == nil {
		return parent
	}
	deniedSet := make(map[string]bool, len(def.DisallowedTools))
	for _, n := range def.DisallowedTools {
		deniedSet[strings.TrimSpace(n)] = true
	}

	var allowed map[string]bool
	if len(def.Tools) > 0 {
		allowed = make(map[string]bool, len(def.Tools))
		for _, n := range def.Tools {
			allowed[strings.TrimSpace(n)] = true
		}
	}

	out := make([]tool.Tool, 0, len(parent))
	for _, t := range parent {
		name := t.Name()
		// 隔离保留集硬剔除（默认）
		if !def.AllowSubagentChaining {
			if _, reserved := reservedSubagentTools[name]; reserved {
				continue
			}
		}
		if deniedSet[name] {
			continue
		}
		if allowed != nil && !allowed[name] {
			continue
		}
		out = append(out, t)
	}
	return out
}

// resolveModel 选择 subagent 实际使用的模型
func resolveModel(def *agent.Definition, opts RunOptions) string {
	if opts.ModelOverride != "" {
		return opts.ModelOverride
	}
	if def.Model == "" || def.Model == "inherit" {
		return opts.DefaultModel
	}
	return def.Model
}

// ResolveModel 暴露给 factory 使用
func ResolveModel(def *agent.Definition, opts RunOptions) string {
	return resolveModel(def, opts)
}

// newAgentID 生成简短 agent ID
func newAgentID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "agent-unknown"
	}
	return "agent-" + hex.EncodeToString(b)
}
