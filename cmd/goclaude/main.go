// Package main 是 GoClaude 的应用入口
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/anthropics/goclaude/pkg/infrastructure/configdir"
	"github.com/anthropics/goclaude/pkg/interfaces/cli"
	"github.com/anthropics/goclaude/pkg/util/dotenv"
	"github.com/anthropics/goclaude/pkg/util/settingsenv"
)

// version 通过编译时 ldflags 注入
var version = "dev"

func main() {
	// 1) 先处理 --env-file（在 cobra 解析前生效，因为环境变量影响子命令初始化）
	//    支持多次：--env-file a.env --env-file b.env（后者优先 / 覆盖）
	envFiles, leftover := extractEnvFileFlags(os.Args[1:])
	os.Args = append(os.Args[:1], leftover...)

	// 2) 自动加载默认 .env 链（pkg/dotenv：与官方 claude 行为对齐）
	loadDotEnvFiles()

	// 3) 加载 settings.json 中的 env 字段（pkg/settingsenv）。
	//    放在 .env 之后是有意为之：.env / shell export 拥有更高优先级，
	//    settings.json 仅作为兜底默认。这样 `--env-file` 与 shell flag
	//    始终可以覆盖团队级 settings 配置。
	loadSettingsEnv()

	// 4) 用户显式指定的 --env-file 拥有最高优先级（用 Overload）
	for _, f := range envFiles {
		if err := dotenv.Overload(f); err != nil {
			fmt.Fprintf(os.Stderr, "警告: 加载 --env-file %s 失败: %v\n", f, err)
		}
	}

	// 5) 加载 YAML 应用配置（参数类配置的唯一信息源）
	//    顺序：configs/default.yaml → ~/.goclaude/config.yaml → <cwd>/.goclaude.yaml
	//    凭证（API Key）仍走 env，不进 YAML。
	cwd, _ := os.Getwd()
	if err := cli.InitAppConfig(cwd); err != nil {
		fmt.Fprintf(os.Stderr, "警告: 加载 YAML 配置失败（将使用内置默认值）: %v\n", err)
	}

	// 6) 为进程根 context 注册 SIGTERM 信号监听。
	//    SIGTERM 是容器/系统级优雅终止标准信号，必须在此统一处理。
	//    SIGINT（Ctrl+C）由 REPL 内部按"生成中取消 / 空闲两次退出"语义自管，
	//    此处不注册，避免与 REPL 的细粒度控制冲突。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
	}
}

// run 执行应用主逻辑
func run(ctx context.Context) error {
	rootCmd := cli.NewRootCmd(version)
	rootCmd.SetContext(ctx)
	return rootCmd.Execute()
}

// loadDotEnvFiles 按优先级加载 .env 文件
//
// 优先级（高 -> 低，先加载的优先；后加载的若用 Load 不会覆盖已存在变量）：
//  1. 已设置的进程环境变量（最高，永不被默认链覆盖）
//  2. 当前工作目录 ./.env.local
//  3. 当前工作目录 ./.env
//  4. 向上查找最近的 .env（处理在子目录执行的场景）
//  5. ~/.claude/.env（用户全局配置）
//
// 注：`.env.local` 通常是开发者本地覆盖（不入版本库），所以优先级最高。
func loadDotEnvFiles() {
	// 1. 当前目录 .env.local（最高优先；非首次加载）
	_ = dotenv.Load(".env.local")
	// 2. 当前目录 .env
	_ = dotenv.Load(".env")
	// 3. 向上查找
	_ = dotenv.LoadFromWorkdir()
	// 4. 用户主目录（优先 .goclaude/.env，兜底 .claude/.env）
	if home, err := os.UserHomeDir(); err == nil {
		_ = dotenv.Load(configdir.JoinPrimary(home, ".env"))
		_ = dotenv.Load(configdir.JoinLegacy(home, ".env"))
	}
}

// loadSettingsEnv 把 settings.json 里的 env 字段桥接为进程环境。
//
// 与官方 claude 的 settings.json 字段对齐（参见 src 端 SettingsSchema），
// 让用户通过结构化配置文件就能配 GOCLAUDE_PERMISSION_MODE / GOCLAUDE_USE_BUILTIN_GREP
// 等运行时开关——无需手写 shell export。
//
// 加载顺序与 dotenv 一致：先加载者优先；.env 与 shell flag 已存在的 key 不会被覆盖。
func loadSettingsEnv() {
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	// projectCwd 与 homeDir 任一为空都没问题：LoadDefaults 内部会跳过空路径。
	_ = settingsenv.LoadDefaults(home, cwd)
}

// extractEnvFileFlags 在 args 中解析 `--env-file <path>` 与 `--env-file=<path>`
//
// 返回 (paths, 剩余 args)。原始顺序保留。
//
// 该函数在 cobra 之前运行：因为 .env 影响 cobra 子命令构造（如 provider 默认值、
// API key 检测）。我们不能等到 cobra 解析后再加载。
func extractEnvFileFlags(args []string) (paths []string, leftover []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--env-file":
			if i+1 < len(args) {
				paths = append(paths, args[i+1])
				i++
			}
			// 末尾孤立 --env-file：忽略（既不入 leftover，也不报错）
			continue
		case strings.HasPrefix(a, "--env-file="):
			paths = append(paths, strings.TrimPrefix(a, "--env-file="))
			continue
		}
		leftover = append(leftover, a)
	}
	return paths, leftover
}
