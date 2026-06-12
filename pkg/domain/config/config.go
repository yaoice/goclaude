// Package config 定义配置系统领域模型
package config

import (
	"encoding/json"
	"time"
)

// ProjectConfig 项目级配置
type ProjectConfig struct {
	// AllowedTools 白名单工具列表
	AllowedTools []string `json:"allowedTools,omitempty"`
	// McpServers MCP服务器配置
	McpServers map[string]McpServerConfig `json:"mcpServers,omitempty"`
	// LastAPIDuration 上次API调用耗时
	LastAPIDuration *time.Duration `json:"lastAPIDuration,omitempty"`
	// LastCost 上次成本
	LastCost *float64 `json:"lastCost,omitempty"`
	// HasTrustDialogAccepted 是否已接受信任对话框
	HasTrustDialogAccepted bool `json:"hasTrustDialogAccepted,omitempty"`
}

// GlobalConfig 全局配置
type GlobalConfig struct {
	// APIKeyHelper API Key获取帮助命令
	APIKeyHelper string `json:"apiKeyHelper,omitempty"`
	// Projects 项目配置映射
	Projects map[string]*ProjectConfig `json:"projects,omitempty"`
	// NumStartups 启动次数
	NumStartups int `json:"numStartups"`
	// UserID 用户标识
	UserID string `json:"userID,omitempty"`
	// Theme UI主题
	Theme string `json:"theme,omitempty"`
	// McpServers 全局MCP服务器配置
	McpServers map[string]McpServerConfig `json:"mcpServers,omitempty"`
	// AutoCompactEnabled 自动压缩开关
	AutoCompactEnabled bool `json:"autoCompactEnabled"`
	// Env 环境变量
	Env map[string]string `json:"env,omitempty"`
}

// McpServerConfig MCP服务器配置
type McpServerConfig struct {
	Command       string            `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	URL           string            `json:"url,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	TransportType string            `json:"transportType,omitempty"`
}

// Settings 设置接口（多层配置合并后的统一访问入口）
type Settings interface {
	// Get 获取设置值
	Get(key string) (interface{}, bool)
	// GetString 获取字符串设置
	GetString(key string) string
	// GetBool 获取布尔设置
	GetBool(key string) bool
	// GetInt 获取整数设置
	GetInt(key string) int
	// GetStringSlice 获取字符串列表设置
	GetStringSlice(key string) []string
}

// SettingsSource 设置来源
type SettingsSource string

const (
	SourcePlugin  SettingsSource = "plugin"  // 最低优先级
	SourceUser    SettingsSource = "user"    // ~/.goclaude/settings.json
	SourceProject SettingsSource = "project" // .goclaude/settings.json
	SourceLocal   SettingsSource = "local"   // .goclaude/settings.local.json
	SourceFlag    SettingsSource = "flag"    // SDK inline / file
	SourcePolicy  SettingsSource = "policy"  // 最高优先级
)

// SettingsData 单层设置数据
type SettingsData struct {
	Source SettingsSource         `json:"source"`
	Data   map[string]interface{} `json:"data"`
}

// Repository 配置持久化接口
type Repository interface {
	// LoadGlobal 加载全局配置
	LoadGlobal() (*GlobalConfig, error)
	// SaveGlobal 保存全局配置
	SaveGlobal(config *GlobalConfig) error
	// LoadProject 加载项目配置
	LoadProject(projectPath string) (*ProjectConfig, error)
	// SaveProject 保存项目配置
	SaveProject(projectPath string, config *ProjectConfig) error
	// LoadSettings 加载指定来源的设置
	LoadSettings(source SettingsSource, path string) (*SettingsData, error)
}

// Merger 配置合并器 - 实现5层优先级合并策略
type Merger struct {
	layers []SettingsData
}

// NewMerger 创建配置合并器
func NewMerger() *Merger {
	return &Merger{
		layers: make([]SettingsData, 0),
	}
}

// AddLayer 添加配置层（按优先级从低到高添加）
func (m *Merger) AddLayer(layer SettingsData) {
	m.layers = append(m.layers, layer)
}

// Merge 合并所有配置层，返回最终合并结果
// 高优先级层的值覆盖低优先级层
// 数组类型使用 concat+dedup 策略
func (m *Merger) Merge() map[string]interface{} {
	result := make(map[string]interface{})

	for _, layer := range m.layers {
		for key, value := range layer.Data {
			existing, exists := result[key]
			if !exists {
				result[key] = value
				continue
			}

			// 数组合并策略：concat + dedup
			if existingArr, ok := toStringSlice(existing); ok {
				if newArr, ok := toStringSlice(value); ok {
					result[key] = dedup(append(existingArr, newArr...))
					continue
				}
			}

			// 对象合并策略：deep merge
			if existingMap, ok := existing.(map[string]interface{}); ok {
				if newMap, ok := value.(map[string]interface{}); ok {
					result[key] = deepMerge(existingMap, newMap)
					continue
				}
			}

			// 标量类型：高优先级覆盖
			result[key] = value
		}
	}

	return result
}

// deepMerge 深度合并两个map
func deepMerge(base, override map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range base {
		result[k] = v
	}
	for k, v := range override {
		if baseMap, ok := result[k].(map[string]interface{}); ok {
			if overMap, ok := v.(map[string]interface{}); ok {
				result[k] = deepMerge(baseMap, overMap)
				continue
			}
		}
		result[k] = v
	}
	return result
}

// toStringSlice 尝试转换为字符串切片
func toStringSlice(v interface{}) ([]string, bool) {
	switch arr := v.(type) {
	case []string:
		return arr, true
	case []interface{}:
		result := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok {
				result = append(result, s)
			} else {
				return nil, false
			}
		}
		return result, true
	}
	return nil, false
}

// dedup 字符串切片去重
func dedup(items []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	return result
}

// MarshalConfig 序列化配置为JSON
func MarshalConfig(v interface{}) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

// UnmarshalConfig 反序列化JSON配置
func UnmarshalConfig(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
