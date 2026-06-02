// Package appconfig 是 goclaude 的统一应用配置中心
//
// # 单一信息源（Single Source of Truth）
//
// 自 2026-05 起，所有"参数类"配置（model / temperature / max_tokens /
// timeouts / 各 provider base_url / permission mode 等）统一来自 YAML 文件，
// 不再混用环境变量与硬编码默认值。
//
// 唯一例外：API Key（凭证）仍走环境变量（DEEPSEEK_API_KEY/ANTHROPIC_API_KEY），
// 避免凭证进入配置文件被提交到代码仓库。
//
// # 加载链路（先加载者优先级低）
//
//  1. internal default     —— DefaultConfig() 返回的内置兜底值
//  2. configs/default.yaml —— 工程内置（与二进制一同发布的默认）
//  3. ~/.goclaude/config.yaml         —— 用户级覆盖（可选）
//  4. <project>/.goclaude.yaml        —— 项目级覆盖（可选）
//  5. CLI flags                        —— 一次性临时覆盖（在调用方做合并）
//
// 后加载者**深度合并**进结果，标量型字段直接覆盖。
//
// # 用法
//
//	cfg, err := appconfig.Load(projectDir)
//	if err != nil { ... }
//	fmt.Println(cfg.API.Model, cfg.Permissions.Mode)
package appconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是 goclaude 全部参数配置
//
// 字段命名采用大写 Go 风格；YAML 键采用 snake_case，由本包内部映射处理。
type Config struct {
	API         APIConfig                  `yaml:"api"`
	Providers   map[string]ProviderConfig  `yaml:"providers"`
	Engine      EngineConfig               `yaml:"engine"`
	Tools       ToolsConfig                `yaml:"tools"`
	MCP         MCPConfig                  `yaml:"mcp"`
	AgentTeams  AgentTeamsConfig           `yaml:"agent_teams"`
	Permissions PermissionsConfig          `yaml:"permissions"`
	Sandbox     SandboxConfig              `yaml:"sandbox"`
	Session     SessionConfig              `yaml:"session"`
	TUI         TUIConfig                  `yaml:"tui"`

	// LoadedFrom 记录配置实际从哪些文件加载（按加载顺序）
	// 仅诊断用途（goclaude doctor 展示）。
	LoadedFrom []string `yaml:"-"`
}

// APIConfig 主 Provider 与模型参数
type APIConfig struct {
	Provider    string  `yaml:"provider"`     // anthropic | deepseek
	Model       string  `yaml:"model"`        // 默认模型名
	MaxTokens   int     `yaml:"max_tokens"`   // 单次最大输出 token
	Temperature float64 `yaml:"temperature"`  // 采样温度
	TopP        float64 `yaml:"top_p"`        // 核采样
	Stream      bool    `yaml:"stream"`       // 是否使用流式
}

// ProviderConfig 各 Provider 的传输与重试参数
type ProviderConfig struct {
	BaseURL        string        `yaml:"base_url"`
	APIVersion     string        `yaml:"api_version"`
	DefaultModel   string        `yaml:"default_model"`
	Timeout        time.Duration `yaml:"timeout"`
	MaxRetries     int           `yaml:"max_retries"`
	RetryBaseDelay time.Duration `yaml:"retry_base_delay"`
}

// EngineConfig 查询引擎参数
type EngineConfig struct {
	MaxTurns       int           `yaml:"max_turns"`
	TokenBudget    int           `yaml:"token_budget"`
	AutoCompact    bool          `yaml:"auto_compact"`
	MaxRetries     int           `yaml:"max_retries"`
	RetryBaseDelay time.Duration `yaml:"retry_base_delay"`
}

// ToolsConfig 工具运行参数
type ToolsConfig struct {
	MaxConcurrency  int           `yaml:"max_concurrency"`
	MaxResultSize   int           `yaml:"max_result_size"`
	Timeout         time.Duration `yaml:"timeout"`
	UseBuiltinGrep  bool          `yaml:"use_builtin_grep"`
}

// MCPConfig MCP 子系统参数
type MCPConfig struct {
	Enabled        bool          `yaml:"enabled"`
	ConnectTimeout time.Duration `yaml:"connect_timeout"`
	RequestTimeout time.Duration `yaml:"request_timeout"`
}

// AgentTeamsConfig 控制"子任务执行模式"的功能开关。
//
// 这是 agent-teams 模块的总闸：
//   - Enabled = true  → 多智能体团队协作模式：注册全部 team 工具
//     （team_create / send_message / create_task / ... 共 21 个），任务可由
//     多个 agent 组成的团队协同处理；leader 每轮自动同步成员进展。
//   - Enabled = false → 单一 subagent 模式：不注册任何 team 工具，主 agent 只能
//     通过 Agent 工具把任务下发给单一 subagent 独立执行（上下文隔离的纯读 RPC）。
//
// 默认 true（保持历史行为）。优先级（高→低）：
//   CLI --agent-teams > 环境变量 GOCLAUDE_AGENT_TEAMS > YAML agent_teams.enabled
type AgentTeamsConfig struct {
	Enabled bool `yaml:"enabled"`
}

// PermissionsConfig 权限/审批参数
type PermissionsConfig struct {
	Mode             string `yaml:"mode"` // default | acceptEdits | plan | bypass
	AutoApproveRead  bool   `yaml:"auto_approve_read"`
}

// SandboxConfig Bash 沙箱参数（Linux: bwrap / macOS: sandbox-exec）
//
// 此结构镜像 configs/default.yaml 的 sandbox 段；运行时由 cli 转换为
// infrastructure/sandbox.Config 后注入 Shell 执行器。
type SandboxConfig struct {
	Enabled                   bool                 `yaml:"enabled"`
	FilesystemRead            SandboxFSConfig      `yaml:"filesystem_read"`
	FilesystemWrite           SandboxFSConfig      `yaml:"filesystem_write"`
	Network                   SandboxNetworkConfig `yaml:"network"`
	AllowUnsandboxedCommands  bool                 `yaml:"allow_unsandboxed_commands"`
	ExcludedCommands          []string             `yaml:"excluded_commands"`
	EnableWeakerNestedSandbox bool                 `yaml:"enable_weaker_nested_sandbox"`
	IgnoreViolations          bool                 `yaml:"ignore_violations"`
}

// SandboxFSConfig 文件系统读/写白名单与黑名单
type SandboxFSConfig struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

// SandboxNetworkConfig 网络访问限制
type SandboxNetworkConfig struct {
	DisableNetwork    bool     `yaml:"disable_network"`
	AllowedDomains    []string `yaml:"allowed_domains"`
	DeniedDomains     []string `yaml:"denied_domains"`
	AllowUnixSockets  bool     `yaml:"allow_unix_sockets"`
	AllowLocalBinding bool     `yaml:"allow_local_binding"`
}

// SessionConfig 会话与记忆持久化参数
type SessionConfig struct {
	HistoryDir     string `yaml:"history_dir"`
	MemoryFile     string `yaml:"memory_file"`
	MaxMemoryLines int    `yaml:"max_memory_lines"`
	MaxMemoryBytes int    `yaml:"max_memory_bytes"`
}

// TUIConfig 终端 UI 参数
type TUIConfig struct {
	Theme          string `yaml:"theme"`
	ShowTokenCount bool   `yaml:"show_token_count"`
	ShowCost       bool   `yaml:"show_cost"`
}

// DefaultConfig 返回内置兜底默认值
//
// 这些值是"YAML 文件全部缺失"时的安全保底，与官方 claude / 历史代码兼容。
func DefaultConfig() *Config {
	return &Config{
		API: APIConfig{
			Provider:    "deepseek",
			Model:       "deepseek-chat",
			MaxTokens:   8192,
			Temperature: 1.0,
			TopP:        1.0,
			Stream:      true,
		},
		Providers: map[string]ProviderConfig{
			"deepseek": {
				BaseURL:        "https://api.deepseek.com",
				DefaultModel:   "deepseek-chat",
				Timeout:        300 * time.Second,
				MaxRetries:     3,
				RetryBaseDelay: 1 * time.Second,
			},
			"anthropic": {
				BaseURL:        "https://api.anthropic.com",
				APIVersion:     "2023-06-01",
				DefaultModel:   "claude-sonnet-4-20250514",
				Timeout:        300 * time.Second,
				MaxRetries:     3,
				RetryBaseDelay: 1 * time.Second,
			},
		},
		Engine: EngineConfig{
			MaxTurns:    100,
			TokenBudget: 200000,
			AutoCompact: true,
			MaxRetries:  3,
		},
		Tools: ToolsConfig{
			MaxConcurrency: 10,
			MaxResultSize:  30000,
			Timeout:        120 * time.Second,
		},
		MCP: MCPConfig{
			Enabled:        true,
			ConnectTimeout: 30 * time.Second,
			RequestTimeout: 60 * time.Second,
		},
		AgentTeams: AgentTeamsConfig{
			Enabled: true, // 默认开启多智能体团队协作（保持历史行为）
		},
		Permissions: PermissionsConfig{
			Mode:            "default",
			AutoApproveRead: true,
		},
		Sandbox: SandboxConfig{
			Enabled: false, // 默认关闭，作为安全基线；由 YAML 显式开启
			FilesystemRead: SandboxFSConfig{
				Allow: []string{".", "~/.claude"},
			},
			FilesystemWrite: SandboxFSConfig{
				Allow: []string{".", "~/.claude/tmp"},
				Deny:  []string{"~/.ssh", "~/.aws", "~/.config/gcloud"},
			},
			Network: SandboxNetworkConfig{
				AllowLocalBinding: true,
			},
			AllowUnsandboxedCommands: true,
		},
		Session: SessionConfig{
			HistoryDir:     "~/.claude/sessions",
			MemoryFile:     "MEMORY.md",
			MaxMemoryLines: 200,
			MaxMemoryBytes: 25000,
		},
		TUI: TUIConfig{
			Theme:          "default",
			ShowTokenCount: true,
			ShowCost:       true,
		},
	}
}

// Load 按文档优先级链路加载配置
//
// projectDir 为空时跳过项目级配置；定义参考包注释。
func Load(projectDir string) (*Config, error) {
	cfg := DefaultConfig()

	// 候选路径，先加载者优先级低（后加载覆盖前者）
	type candidate struct {
		path string
		// required: false 时文件不存在不报错（仅记录跳过）
		required bool
	}
	var paths []candidate

	// 1) 工程内置 default.yaml
	//    优先在 cwd/configs/default.yaml 找；找不到再尝试 exe 同级（兼容打包）
	if p := findBuiltinDefault(); p != "" {
		paths = append(paths, candidate{p, false})
	}

	// 2) 用户级
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, candidate{filepath.Join(home, ".goclaude", "config.yaml"), false})
	}

	// 3) 项目级
	if projectDir != "" {
		paths = append(paths, candidate{filepath.Join(projectDir, ".goclaude.yaml"), false})
	}

	for _, c := range paths {
		if _, err := os.Stat(c.path); err != nil {
			if c.required {
				return nil, fmt.Errorf("config file required but not found: %s", c.path)
			}
			continue
		}
		raw, err := loadYAMLFile(c.path)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", c.path, err)
		}
		applyMap(cfg, raw)
		cfg.LoadedFrom = append(cfg.LoadedFrom, c.path)
	}

	return cfg, nil
}

// LoadFromPath 仅从单个 YAML 文件加载（不走 default 链路）
//
// 主要用于测试与 --config <path> 这种显式覆盖场景。
func LoadFromPath(path string) (*Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		return cfg, nil
	}
	raw, err := loadYAMLFile(path)
	if err != nil {
		return nil, err
	}
	applyMap(cfg, raw)
	cfg.LoadedFrom = []string{path}
	return cfg, nil
}

// findBuiltinDefault 寻找 configs/default.yaml
//
// 优先在 cwd 与可执行文件目录的 configs/ 子目录里找，覆盖：
//   - 开发：在源码根目录 `go run`
//   - 安装：与 ./bin/goclaude 二进制平级 ./configs/default.yaml
func findBuiltinDefault() string {
	cwd, _ := os.Getwd()
	candidates := []string{
		filepath.Join(cwd, "configs", "default.yaml"),
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, "configs", "default.yaml"),
			filepath.Join(filepath.Dir(exeDir), "configs", "default.yaml"),
		)
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func loadYAMLFile(path string) (map[string]interface{}, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	result := map[string]interface{}{}
	decoder := yaml.NewDecoder(f)
	// yaml.v3 直接解析到 map[string]interface{}
	err = decoder.Decode(&result)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	return result, nil
}
