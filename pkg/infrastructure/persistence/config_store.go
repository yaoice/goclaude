// Package persistence 提供配置与会话持久化
package persistence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yaoice/goclaude/pkg/domain/config"
	"github.com/yaoice/goclaude/pkg/infrastructure/configdir"
)

// ConfigStore 配置文件存储
type ConfigStore struct {
	// homeDir 用户主目录
	homeDir string
}

// NewConfigStore 创建配置存储
func NewConfigStore(homeDir string) *ConfigStore {
	return &ConfigStore{homeDir: homeDir}
}

// globalConfigPath 返回写入路径（仅 .goclaude/）
func (s *ConfigStore) globalConfigPath() string {
	return configdir.JoinPrimary(s.homeDir, "config.json")
}

// LoadGlobal 加载全局配置（优先 .goclaude/，兜底 .claude/）
func (s *ConfigStore) LoadGlobal() (*config.GlobalConfig, error) {
	for _, p := range configdir.AllReadDirs(s.homeDir, "config.json") {
		data, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read global config: %w", err)
		}
		var cfg config.GlobalConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parse global config: %w", err)
		}
		return &cfg, nil
	}
	return &config.GlobalConfig{}, nil
}

// SaveGlobal 保存全局配置（写入 .goclaude/）
func (s *ConfigStore) SaveGlobal(cfg *config.GlobalConfig) error {
	path := s.globalConfigPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// LoadProject 加载项目配置（优先 .goclaude/，兜底 .claude/）
func (s *ConfigStore) LoadProject(projectPath string) (*config.ProjectConfig, error) {
	for _, p := range configdir.AllReadDirs(projectPath, "config.json") {
		data, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		var cfg config.ProjectConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, err
		}
		return &cfg, nil
	}
	return &config.ProjectConfig{}, nil
}

// SaveProject 保存项目配置（写入 .goclaude/）
func (s *ConfigStore) SaveProject(projectPath string, cfg *config.ProjectConfig) error {
	path := configdir.JoinPrimary(projectPath, "config.json")
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// LoadSettings 加载指定来源的设置
func (s *ConfigStore) LoadSettings(source config.SettingsSource, path string) (*config.SettingsData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &config.SettingsData{Source: source, Data: make(map[string]interface{})}, nil
		}
		return nil, err
	}

	var settingsMap map[string]interface{}
	if err := json.Unmarshal(data, &settingsMap); err != nil {
		return nil, err
	}

	return &config.SettingsData{Source: source, Data: settingsMap}, nil
}

// SessionStore 会话持久化
type SessionStore struct {
	baseDir string
}

// NewSessionStore 创建会话存储
func NewSessionStore(baseDir string) *SessionStore {
	return &SessionStore{baseDir: baseDir}
}

// SaveSession 保存会话数据
func (s *SessionStore) SaveSession(sessionID string, data interface{}) error {
	dir := filepath.Join(s.baseDir, sessionID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	path := filepath.Join(dir, "session.json")
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, jsonData, 0644)
}

// LoadSession 加载会话数据
func (s *SessionStore) LoadSession(sessionID string, target interface{}) error {
	path := filepath.Join(s.baseDir, sessionID, "session.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
