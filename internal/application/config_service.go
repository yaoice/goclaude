package application

import (
	"context"
	"log/slog"
	"os"

	"github.com/anthropics/goclaude/internal/domain/config"
	"github.com/anthropics/goclaude/internal/infrastructure/configdir"
	"github.com/anthropics/goclaude/internal/infrastructure/persistence"
)

// ConfigService 配置应用服务
type ConfigService struct {
	store       *persistence.ConfigStore
	merger      *config.Merger
	homeDir     string
	projectRoot string
	logger      *slog.Logger
}

// NewConfigService 创建配置服务
func NewConfigService(homeDir, projectRoot string, logger *slog.Logger) *ConfigService {
	return &ConfigService{
		store:       persistence.NewConfigStore(homeDir),
		merger:      config.NewMerger(),
		homeDir:     homeDir,
		projectRoot: projectRoot,
		logger:      logger,
	}
}

// LoadAll 加载所有配置层并合并
func (s *ConfigService) LoadAll(ctx context.Context) (map[string]interface{}, error) {
	merger := config.NewMerger()

	// 加载用户设置（优先 .goclaude/，兜底 .claude/）
	for _, p := range configdir.AllReadDirs(s.homeDir, "settings.json") {
		userData, err := s.store.LoadSettings(config.SourceUser, p)
		if err == nil && len(userData.Data) > 0 {
			merger.AddLayer(*userData)
			break
		}
	}

	// 加载项目设置（优先 .goclaude/，兜底 .claude/）
	for _, p := range configdir.AllReadDirs(s.projectRoot, "settings.json") {
		projectData, err := s.store.LoadSettings(config.SourceProject, p)
		if err == nil && len(projectData.Data) > 0 {
			merger.AddLayer(*projectData)
			break
		}
	}

	// 加载本地设置（不提交到git，优先 .goclaude/，兜底 .claude/）
	for _, p := range configdir.AllReadDirs(s.projectRoot, "settings.local.json") {
		localData, err := s.store.LoadSettings(config.SourceLocal, p)
		if err == nil && len(localData.Data) > 0 {
			merger.AddLayer(*localData)
			break
		}
	}

	return merger.Merge(), nil
}

// GetGlobalConfig 获取全局配置
func (s *ConfigService) GetGlobalConfig() (*config.GlobalConfig, error) {
	return s.store.LoadGlobal()
}

// SaveGlobalConfig 保存全局配置
func (s *ConfigService) SaveGlobalConfig(cfg *config.GlobalConfig) error {
	return s.store.SaveGlobal(cfg)
}

// GetProjectConfig 获取项目配置
func (s *ConfigService) GetProjectConfig() (*config.ProjectConfig, error) {
	return s.store.LoadProject(s.projectRoot)
}

// GetHomeDir 获取主目录
func (s *ConfigService) GetHomeDir() string {
	if s.homeDir != "" {
		return s.homeDir
	}
	home, _ := os.UserHomeDir()
	return home
}
