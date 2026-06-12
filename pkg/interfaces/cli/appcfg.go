// Package cli - appcfg.go：把 appconfig.Config 暴露为 cli 包级全局
//
// main.go 在启动早期调用 InitAppConfig() 完成加载；之后所有 cli 子命令通过
// AppConfig() 读取统一配置。
//
// 单线程使用：cobra Execute 串行解析；不存在并发写。读取在子命令运行期间
// 也只读不写（运行时若需修改，请使用 deep copy 后再变更）。
package cli

import (
	"github.com/yaoice/goclaude/pkg/infrastructure/appconfig"
)

var loadedAppConfig *appconfig.Config

// InitAppConfig 由 main 在启动早期调用
//
// projectDir 通常传 cwd；空字符串表示跳过项目级覆盖（仅 default + user）。
// 加载失败时返回错误；调用方应当 fail-fast 并打印诊断信息。
func InitAppConfig(projectDir string) error {
	c, err := appconfig.Load(projectDir)
	if err != nil {
		return err
	}
	loadedAppConfig = c
	return nil
}

// AppConfig 返回已加载的全局配置；未初始化时返回 DefaultConfig 兜底。
//
// 调用方不应修改返回的 Config（浅拷贝即可避免误改全局）。
func AppConfig() *appconfig.Config {
	if loadedAppConfig == nil {
		return appconfig.DefaultConfig()
	}
	return loadedAppConfig
}
