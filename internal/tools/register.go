package tools

import (
	"github.com/anthropics/goclaude/internal/domain/tool"
	"github.com/anthropics/goclaude/internal/infrastructure/filesystem"
	"github.com/anthropics/goclaude/internal/infrastructure/sandbox"
	"github.com/anthropics/goclaude/internal/infrastructure/shell"
)

// RegisterAll 注册所有工具到注册表
//
// sbCfg 为沙箱配置；当其为 nil 或未启用时，Bash 工具使用直接执行（原始行为）。
// 当沙箱启用时，按当前平台（Linux: bwrap / macOS: sandbox-exec）包装命令执行；
// 若沙箱初始化失败，则降级为直接执行以保证可用性。
func RegisterAll(registry *tool.Registry, workDir string, sbCfg *sandbox.Config) {
	fs := filesystem.NewService(nil)

	shellExec := shell.NewExecutor(workDir, 0)
	if sbCfg != nil && sbCfg.Enabled {
		if ex, err := shell.NewExecutorWithSandbox(workDir, 0, sbCfg); err == nil {
			shellExec = ex
		}
	}

	// 文件操作工具
	registry.MustRegister(NewFileReadTool(fs))
	registry.MustRegister(NewFileWriteTool(fs))
	registry.MustRegister(NewFileEditTool(fs))

	// 搜索工具
	registry.MustRegister(NewGlobTool(workDir))
	registry.MustRegister(NewGrepTool(workDir))

	// Shell工具
	registry.MustRegister(NewBashTool(shellExec))

	// 交互工具
	registry.MustRegister(NewAskUserTool())
	registry.MustRegister(NewTodoWriteTool())

	// Agent工具
	registry.MustRegister(NewAgentTool())

	// 网络工具
	registry.MustRegister(NewWebSearchTool())
	registry.MustRegister(NewWebFetchTool())
}
