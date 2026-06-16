package plugininfra

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yaoice/goclaude/pkg/domain/plugin"
)

// MarketplaceLoader 实现 plugin.MarketplaceLoader。
type MarketplaceLoader struct {
	installer *Installer
}

// NewMarketplaceLoader 创建市场加载器。
func NewMarketplaceLoader(installer *Installer) *MarketplaceLoader {
	if installer == nil {
		installer = NewInstaller()
	}
	return &MarketplaceLoader{installer: installer}
}

// Load 拉取市场来源到 cacheDir（远程）或就地引用（本地），并解析 marketplace.json。
func (l *MarketplaceLoader) Load(ctx context.Context, source, cacheDir string) (*plugin.Marketplace, error) {
	src := plugin.NormalizeSource(strings.TrimSpace(source))
	if src == "" {
		return nil, fmt.Errorf("空的市场来源")
	}
	typ := plugin.ClassifySource(src)

	var localRoot string
	switch typ {
	case plugin.SourceLocal:
		abs, err := filepath.Abs(src)
		if err != nil {
			return nil, fmt.Errorf("解析本地市场路径失败: %w", err)
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("本地市场不存在或不是目录: %s", abs)
		}
		localRoot = abs
	case plugin.SourceGit, plugin.SourceHTTP:
		root, err := l.installer.Install(ctx, src, cacheDir)
		if err != nil {
			return nil, fmt.Errorf("拉取市场失败: %w", err)
		}
		localRoot = root
	default:
		return nil, fmt.Errorf("不支持的市场来源类型: %q", src)
	}

	manifestPath, err := findMarketplaceManifest(localRoot)
	if err != nil {
		return nil, err
	}
	mm, err := parseMarketplaceManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	name := mm.Name
	if name == "" {
		name = filepath.Base(localRoot)
	}
	return &plugin.Marketplace{
		Name:      name,
		Type:      typ,
		Source:    src,
		LocalPath: localRoot,
		Entries:   mm.Plugins,
	}, nil
}

// Reload 重新解析一个已缓存到本地的市场（用于启动装配，不重新下载）。
func (l *MarketplaceLoader) Reload(m *plugin.Marketplace) error {
	if m == nil || m.LocalPath == "" {
		return fmt.Errorf("市场缺少本地路径")
	}
	manifestPath, err := findMarketplaceManifest(m.LocalPath)
	if err != nil {
		return err
	}
	mm, err := parseMarketplaceManifest(manifestPath)
	if err != nil {
		return err
	}
	m.Entries = mm.Plugins
	return nil
}

// ParseManifest 解析位于 rootPath 的插件 plugin.json。
func (l *MarketplaceLoader) ParseManifest(rootPath string) (*plugin.Manifest, error) {
	path, err := findPluginManifest(rootPath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 plugin.json 失败: %w", err)
	}
	var m plugin.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("解析 plugin.json 失败: %w", err)
	}
	if m.Name == "" {
		m.Name = filepath.Base(rootPath)
	}
	return &m, nil
}

// findMarketplaceManifest 在 root 下查找 marketplace.json。
func findMarketplaceManifest(root string) (string, error) {
	candidates := []string{
		filepath.Join(root, ".claude-plugin", "marketplace.json"),
		filepath.Join(root, "marketplace.json"),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("未找到 marketplace.json（查找于 %s/.claude-plugin/）", root)
}

// findPluginManifest 在 root 下查找 plugin.json。
func findPluginManifest(root string) (string, error) {
	candidates := []string{
		filepath.Join(root, ".claude-plugin", "plugin.json"),
		filepath.Join(root, "plugin.json"),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("未找到 plugin.json（查找于 %s/.claude-plugin/）", root)
}

func parseMarketplaceManifest(path string) (*plugin.MarketplaceManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 marketplace.json 失败: %w", err)
	}
	var mm plugin.MarketplaceManifest
	if err := json.Unmarshal(data, &mm); err != nil {
		return nil, fmt.Errorf("解析 marketplace.json 失败: %w", err)
	}
	return &mm, nil
}
