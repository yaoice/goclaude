package plugininfra

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yaoice/goclaude/pkg/domain/plugin"
	"github.com/yaoice/goclaude/pkg/infrastructure/configdir"
)

// Store 实现 plugin.Store：把插件系统状态持久化为 JSON。
//
// 目录布局（默认 ~/.goclaude/plugins/）：
//
//	plugins/
//	  state.json                 # 市场与已装插件清单
//	  installed/<plugin-name>/   # 已安装插件根目录
//	  marketplaces/<mkt-name>/   # 远程市场内容缓存
type Store struct {
	baseDir string
}

// NewStore 创建 Store；baseDir 为空时默认 ~/.goclaude/plugins。
func NewStore(baseDir string) *Store {
	if baseDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			baseDir = configdir.JoinPrimary(home, "plugins")
		} else {
			baseDir = filepath.Join(".goclaude", "plugins")
		}
	}
	return &Store{baseDir: baseDir}
}

// BaseDir 返回插件系统根目录。
func (s *Store) BaseDir() string { return s.baseDir }

// InstalledDir 返回某插件的安装目标目录。
func (s *Store) InstalledDir(pluginName string) string {
	return filepath.Join(s.baseDir, "installed", sanitizeName(pluginName))
}

// MarketplaceDir 返回某市场的本地缓存目录。
func (s *Store) MarketplaceDir(marketplaceName string) string {
	return filepath.Join(s.baseDir, "marketplaces", sanitizeName(marketplaceName))
}

func (s *Store) statePath() string {
	return filepath.Join(s.baseDir, "state.json")
}

// Load 读取持久化状态；文件不存在时返回空 State。
func (s *Store) Load() (*plugin.State, error) {
	data, err := os.ReadFile(s.statePath())
	if err != nil {
		if os.IsNotExist(err) {
			return &plugin.State{}, nil
		}
		return nil, fmt.Errorf("读取插件状态失败: %w", err)
	}
	var st plugin.State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("解析插件状态失败: %w", err)
	}
	return &st, nil
}

// Save 写入持久化状态（原子写）。
func (s *Store) Save(st *plugin.State) error {
	if st == nil {
		st = &plugin.State{}
	}
	if err := os.MkdirAll(s.baseDir, 0o755); err != nil {
		return fmt.Errorf("创建插件目录失败: %w", err)
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化插件状态失败: %w", err)
	}
	tmp := s.statePath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("写入插件状态失败: %w", err)
	}
	if err := os.Rename(tmp, s.statePath()); err != nil {
		return fmt.Errorf("提交插件状态失败: %w", err)
	}
	return nil
}

// sanitizeName 把插件/市场名转为安全的目录名，防止路径穿越。
func sanitizeName(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out = append(out, r)
		case r == '-', r == '_', r == '.':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	s := string(out)
	if s == "" || s == "." || s == ".." {
		return "_"
	}
	return s
}
