package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/anthropics/goclaude/pkg/application"
	"github.com/anthropics/goclaude/pkg/domain/tool"
	"github.com/anthropics/goclaude/pkg/infrastructure/appconfig"
)

// AgentTool 子 Agent 工具
//
// 对齐 src/tools/AgentTool/AgentTool.tsx 的对外接口：
//   - 输入 subagent_type + description + prompt
//   - 内部委托 AgentService.Run 执行 subagent
//
// 当未注入 service / factory 时退化为返回错误（保持工具仍可被 List 但调用必失败）。
type AgentTool struct {
	service *application.AgentService
	factory application.AgentEngineFactory

	// defaults 默认运行时上下文（由 wire 时填入）
	defaults AgentToolDefaults
}

// AgentToolDefaults AgentTool 启动时注入的运行时默认值
type AgentToolDefaults struct {
	ParentSessionID string
	WorkingDir      string
	ProjectRoot     string
	DefaultModel    string
	// WorkspaceRoot 任务产物统一输出目录；非空时 subagent 会将输出文件写到此目录
	WorkspaceRoot string
}

// NewAgentTool 创建未注入依赖的占位 AgentTool（兼容旧代码路径）
func NewAgentTool() *AgentTool { return &AgentTool{} }

// NewAgentToolWithService 创建带依赖的可执行 AgentTool
func NewAgentToolWithService(svc *application.AgentService, factory application.AgentEngineFactory, defaults AgentToolDefaults) *AgentTool {
	return &AgentTool{service: svc, factory: factory, defaults: defaults}
}

func (t *AgentTool) Name() string { return "Agent" }

// Aliases 兼容历史 wire name 与小写形式
//
// "Task"  → src `LEGACY_AGENT_TOOL_NAME`，Claude 训练数据里熟悉
// "agent" → 旧版 go shell 注册的小写名，确保用户/旧 system prompt 仍可用
func (t *AgentTool) Aliases() []string { return []string{"Task", "agent"} }
func (t *AgentTool) Description() string {
	return "在隔离的上下文中启动一个 subagent 执行子任务。支持的 subagent_type 由 /agents 命令查看。"
}
func (t *AgentTool) IsEnabled() bool { return true }

// IsReadOnly 在**主 agent 视角**下，调用 Agent 工具是"只读"的：
// subagent 拥有完全独立的 Engine / Registry / Executor / 消息历史，
// 所有副作用（文件修改、Bash、网络请求等）都封闭在子上下文里。主 agent
// 仅看到 subagent 回传的最终摘要（FinalText + metadata），自身的上下文、
// 工具栈、PermissionContext 不会被 subagent 写入。
//
// 这与文章"上下文隔离：主对话不会被数十个文件的内容占满，收到的是提炼后的发现"
// 的设计直接对齐——把 Agent 工具视作主上下文的纯读 RPC。
//
// 注：subagent 内部当然可能对**磁盘**做写操作；但那不在主 executor 的并发
// 冲突领域内（每个 subagent 各有独立 executor，且文章明确"并行 subagent 不
// 应编辑同一文件"是调用方约束）。
func (t *AgentTool) IsReadOnly(_ tool.Input) bool { return true }

// IsConcurrencySafe 允许主 executor 在一轮里并发派发多个 Agent 调用——
// 这是文章"并发模型"的核心：
//
//	"一次调用触发三个并行 subagent，分别进行安全、性能、风格审查，
//	 最后综合为一份报告。总耗时约等于单个文件的修改时间（而非三倍）"
//
// 安全性保障：
//   - AgentService.Run 内部对每个 agentID 生成独立的 worktree（若启用）、
//     独立 Engine 与 Executor，不共享可变状态
//   - SubagentEventListener 通过 sync.RWMutex 保护监听器指针，可并发回调
//   - FilterTools 默认禁止 subagent 再启动 subagent，杜绝二级 fan-out
func (t *AgentTool) IsConcurrencySafe(_ tool.Input) bool { return true }

func (t *AgentTool) Prompt() string { return t.Description() }
func (t *AgentTool) ValidateInput(input tool.Input) error {
	if input.GetString("subagent_type") == "" {
		return fmt.Errorf("subagent_type is required")
	}
	if input.GetString("prompt") == "" {
		return fmt.Errorf("prompt is required")
	}
	return nil
}

func (t *AgentTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"subagent_type": map[string]interface{}{
				"type":        "string",
				"description": "subagent 类型名（如 Explore / Plan / general-purpose 或自定义）",
			},
			"description": map[string]interface{}{
				"type":        "string",
				"description": "对该 subagent 任务的一句话描述（3-5 词）",
			},
			"prompt": map[string]interface{}{
				"type":        "string",
				"description": "下发给 subagent 的具体任务提示词",
			},
		},
		"required": []string{"subagent_type", "prompt"},
	}
}

func (t *AgentTool) CheckPermissions(_ context.Context, _ tool.Input, _ *tool.PermissionContext) (tool.PermissionResult, error) {
	return tool.PermissionResult{Behavior: tool.PermissionAllow}, nil
}

// Call 执行 subagent 并返回其最终输出
func (t *AgentTool) Call(ctx context.Context, input tool.Input, uc *tool.UseContext) (*tool.Result, error) {
	if t.service == nil || t.factory == nil {
		return tool.NewErrorResult("AgentTool 未注入 AgentService/Factory，请在启动时通过 NewAgentToolWithService 注册"), nil
	}
	subagentType := input.GetString("subagent_type")
	prompt := input.GetString("prompt")

	workspaceRoot := t.defaults.WorkspaceRoot

	// 若父 workspace 已配置，则在其中创建 subagent-<type>-<timestamp> 子目录
	// 使 subagent 输出与主 agent 输出隔离，目录前缀 subagent 一眼可识别
	if workspaceRoot != "" {
		cfg := appconfig.DefaultConfig()
		subDir, err := cfg.EnsureSubWorkspace(workspaceRoot, appconfig.TaskKindSubagent, subagentType)
		if err != nil {
			// 创建失败不阻塞 subagent 执行，回退到父目录
			subDir = workspaceRoot
		}
		workspaceRoot = subDir
		// 同时写入 .identity 文件标记该目录身份
		identityFile := filepath.Join(subDir, ".identity")
		identity := fmt.Sprintf("subagent subagent:%s\ncreated:%s\n", subagentType, "auto")
		_ = os.WriteFile(identityFile, []byte(identity), 0644)
	}

	opts := application.RunOptions{
		Prompt:          prompt,
		ParentSessionID: t.defaults.ParentSessionID,
		WorkingDir:      t.defaults.WorkingDir,
		ProjectRoot:     t.defaults.ProjectRoot,
		DefaultModel:    t.defaults.DefaultModel,
		WorkspaceRoot:   workspaceRoot,
	}
	result, err := t.service.Run(ctx, subagentType, t.factory, opts)
	if err != nil {
		return tool.NewErrorResult(fmt.Sprintf("subagent %q failed: %v", subagentType, err)), nil
	}
	// 把元数据序列化进 metadata
	out := tool.NewResult(result.FinalText)
	out.WithMetadata("agent_id", result.AgentID)
	out.WithMetadata("agent_type", result.AgentType)
	out.WithMetadata("turns", result.TurnCount)
	out.WithMetadata("stop_reason", string(result.StopReason))
	return out, nil
}

// 兼容旧接口：序列化 metadata 时使用
var _ = json.Marshal
