package application

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yaoice/goclaude/pkg/domain/plugin"
	plugininfra "github.com/yaoice/goclaude/pkg/infrastructure/plugin"
)

// PluginService 插件系统应用服务。
//
// 编排领域 Registry 与基础设施 Installer / MarketplaceLoader / Store，
// 对外提供市场与插件的全生命周期管理：
//   - marketplace add / remove / list
//   - plugin install / uninstall / enable / disable / list
//   - Load：装配阶段从持久化状态重建注册表并解析各插件 manifest
//   - Contributions：汇总所有已启用插件的扩展贡献（供 wiring 分发到各注册表）
//
// 对齐 Claude Code 的插件机制。
type PluginService struct {
	registry  *plugin.Registry
	installer plugin.Installer
	loader    *plugininfra.MarketplaceLoader
	store     *plugininfra.Store
	logger    *slog.Logger
}

// NewPluginService 创建插件服务。baseDir 为空时使用 ~/.goclaude/plugins。
func NewPluginService(baseDir string, logger *slog.Logger) *PluginService {
	if logger == nil {
		logger = slog.Default()
	}
	inst := plugininfra.NewInstaller()
	return &PluginService{
		registry:  plugin.NewRegistry(),
		installer: inst,
		loader:    plugininfra.NewMarketplaceLoader(inst),
		store:     plugininfra.NewStore(baseDir),
		logger:    logger,
	}
}

// Registry 暴露底层注册表（只读遍历用）。
func (s *PluginService) Registry() *plugin.Registry { return s.registry }

// Load 从持久化状态重建注册表，并为每个插件解析 manifest（best-effort）。
func (s *PluginService) Load(_ context.Context) error {
	st, err := s.store.Load()
	if err != nil {
		return fmt.Errorf("加载插件状态失败: %w", err)
	}
	s.registry.Load(st)
	// 重新解析已安装插件的 manifest，便于后续 Contributions 使用
	for _, p := range s.registry.Plugins() {
		if p.InstallPath == "" {
			continue
		}
		m, err := s.loader.ParseManifest(p.InstallPath)
		if err != nil {
			s.logger.Warn("解析插件 manifest 失败", "plugin", p.Name, "error", err)
			continue
		}
		p.Manifest = m
	}
	return nil
}

// persist 把当前注册表状态写回磁盘。
func (s *PluginService) persist() error {
	if err := s.store.Save(s.registry.Snapshot()); err != nil {
		return fmt.Errorf("保存插件状态失败: %w", err)
	}
	return nil
}

// ---- Marketplace 管理 ----

// AddMarketplace 添加一个市场来源（本地/git/http），拉取并解析其清单。
func (s *PluginService) AddMarketplace(ctx context.Context, source string) (*plugin.Marketplace, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, fmt.Errorf("市场来源不能为空")
	}
	// 先用一个临时缓存目录拉取以读取市场名，再迁移到以市场名命名的目录
	staging := filepath.Join(s.store.BaseDir(), "marketplaces", fmt.Sprintf(".staging-%d", time.Now().UnixNano()))
	m, err := s.loader.Load(ctx, source, staging)
	if err != nil {
		_ = os.RemoveAll(staging)
		return nil, err
	}
	// 远程类型需要把缓存迁移到以市场名命名的稳定目录
	if m.Type != plugin.SourceLocal {
		finalDir := s.store.MarketplaceDir(m.Name)
		if finalDir != staging {
			_ = os.RemoveAll(finalDir)
			if err := os.Rename(staging, finalDir); err != nil {
				_ = os.RemoveAll(staging)
				return nil, fmt.Errorf("迁移市场缓存失败: %w", err)
			}
			m.LocalPath = finalDir
		}
	} else {
		_ = os.RemoveAll(staging)
	}
	s.registry.AddMarketplace(m)
	if err := s.persist(); err != nil {
		return nil, err
	}
	s.logger.Info("已添加插件市场", "name", m.Name, "type", m.Type, "plugins", len(m.Entries))
	return m, nil
}

// RemoveMarketplace 移除市场（同时清理远程缓存目录）。
func (s *PluginService) RemoveMarketplace(name string) error {
	m, ok := s.registry.GetMarketplace(name)
	if !ok {
		return fmt.Errorf("市场 %q 不存在", name)
	}
	if m.Type != plugin.SourceLocal && m.LocalPath != "" {
		_ = os.RemoveAll(m.LocalPath)
	}
	s.registry.RemoveMarketplace(name)
	return s.persist()
}

// ListMarketplaces 返回所有市场。
func (s *PluginService) ListMarketplaces() []*plugin.Marketplace {
	mkts := s.registry.Marketplaces()
	// 运行时补全 entries（启动后内存中可能为空）
	for _, m := range mkts {
		if len(m.Entries) == 0 && m.LocalPath != "" {
			if err := s.loader.Reload(m); err != nil {
				s.logger.Debug("重载市场清单失败", "name", m.Name, "error", err)
			}
		}
	}
	return mkts
}

// PluginSearchResult 市场检索命中的单个插件条目。
type PluginSearchResult struct {
	// Name 插件名（市场条目名）。
	Name string
	// Description 插件描述。
	Description string
	// Version 版本（可能为空）。
	Version string
	// Marketplace 该条目所属市场名。
	Marketplace string
	// Installed 本地是否已安装同名插件。
	Installed bool
}

// SearchPlugins 在所有已添加市场的条目中按关键词检索插件。
//
// query 为空时返回全部条目；否则对插件名与描述做大小写不敏感的子串匹配。
// 结果按市场名、插件名排序，便于稳定展示与测试。
func (s *PluginService) SearchPlugins(query string) []PluginSearchResult {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []PluginSearchResult
	for _, m := range s.registry.Marketplaces() {
		// 启动后内存中 entries 可能为空，按需重载市场清单
		if len(m.Entries) == 0 && m.LocalPath != "" {
			if err := s.loader.Reload(m); err != nil {
				s.logger.Debug("重载市场清单失败", "name", m.Name, "error", err)
			}
		}
		for _, e := range m.Entries {
			if e.Name == "" {
				continue
			}
			if q != "" &&
				!strings.Contains(strings.ToLower(e.Name), q) &&
				!strings.Contains(strings.ToLower(e.Description), q) {
				continue
			}
			_, installed := s.registry.GetPlugin(e.Name)
			out = append(out, PluginSearchResult{
				Name:        e.Name,
				Description: e.Description,
				Version:     e.Version,
				Marketplace: m.Name,
				Installed:   installed,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Marketplace != out[j].Marketplace {
			return out[i].Marketplace < out[j].Marketplace
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ---- Plugin 生命周期 ----

// Install 安装一个插件。
//
// ref 支持三种形式：
//   - "<plugin>@<marketplace>"：从指定市场安装
//   - 裸插件名（非路径/URL，且本地无同名目录）：跨所有已添加市场按名解析
//   - 直接来源（本地路径 / git URL / http 压缩包 URL）：脱离市场直接安装
func (s *PluginService) Install(ctx context.Context, ref string) (*plugin.Plugin, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("插件引用不能为空")
	}
	// 1) 显式 name@marketplace
	if name, mkt, ok := parsePluginRef(ref); ok {
		return s.installFromMarketplace(ctx, name, mkt)
	}
	// 2) 裸名（不是路径/URL、本地也无同名目录）→ 跨市场按名解析
	if isBarePluginName(ref) {
		return s.installByName(ctx, ref)
	}
	// 3) 其余视为直接来源（本地路径 / git / http）
	return s.installFromSource(ctx, ref, "")
}

// installByName 在所有已添加市场中按插件名解析并安装。
//
//   - 命中唯一市场：直接安装；
//   - 命中多个市场：报错并提示用 name@marketplace 消歧；
//   - 无任何市场命中：报错并列出当前已添加市场，引导用户使用 name@marketplace
//     或先 marketplace add。
func (s *PluginService) installByName(ctx context.Context, name string) (*plugin.Plugin, error) {
	var hits []string
	for _, m := range s.registry.Marketplaces() {
		// 启动后内存中 entries 可能为空，按需重载市场清单
		if len(m.Entries) == 0 && m.LocalPath != "" {
			_ = s.loader.Reload(m)
		}
		if _, ok := m.FindEntry(name); ok {
			hits = append(hits, m.Name)
		}
	}
	switch len(hits) {
	case 1:
		return s.installFromMarketplace(ctx, name, hits[0])
	case 0:
		return nil, fmt.Errorf("未找到插件 %q：它既不是有效的本地路径/URL，也不在任何已添加市场中。\n"+
			"请改用 `plugin install %s@<市场>` 指定市场，或先 `plugin marketplace add <来源>`。%s",
			name, name, s.marketplaceHint())
	default:
		return nil, fmt.Errorf("插件 %q 在多个市场中存在：%s\n请用 `plugin install %s@<市场>` 明确指定。",
			name, strings.Join(hits, ", "), name)
	}
}

// marketplaceHint 返回当前已添加市场的提示串（供错误信息引导用户）。
func (s *PluginService) marketplaceHint() string {
	mkts := s.registry.Marketplaces()
	if len(mkts) == 0 {
		return "\n（当前没有已添加的市场）"
	}
	names := make([]string, 0, len(mkts))
	for _, m := range mkts {
		names = append(names, m.Name)
	}
	return "\n已添加市场：" + strings.Join(names, ", ")
}

// installFromMarketplace 从市场条目安装插件。
func (s *PluginService) installFromMarketplace(ctx context.Context, name, marketplace string) (*plugin.Plugin, error) {
	m, ok := s.registry.GetMarketplace(marketplace)
	if !ok {
		return nil, fmt.Errorf("市场 %q 不存在（先用 plugin marketplace add 添加）", marketplace)
	}
	if len(m.Entries) == 0 && m.LocalPath != "" {
		_ = s.loader.Reload(m)
	}
	entry, ok := m.FindEntry(name)
	if !ok {
		return nil, fmt.Errorf("市场 %q 中没有插件 %q", marketplace, name)
	}
	source := resolveEntrySource(m, entry)
	return s.installFromSource(ctx, source, m.Name)
}

// installFromSource 把来源安装到本地并注册。
func (s *PluginService) installFromSource(ctx context.Context, source, marketplace string) (*plugin.Plugin, error) {
	// 先安装到 staging，读取 manifest 得到插件名后再迁移到稳定目录
	staging := filepath.Join(s.store.BaseDir(), "installed", fmt.Sprintf(".staging-%d", time.Now().UnixNano()))
	root, err := s.installer.Install(ctx, source, staging)
	if err != nil {
		return nil, err
	}
	manifest, err := s.loader.ParseManifest(root)
	if err != nil {
		_ = os.RemoveAll(staging)
		return nil, fmt.Errorf("无效的插件（缺少有效 plugin.json）: %w", err)
	}
	finalDir := s.store.InstalledDir(manifest.Name)
	if finalDir != staging {
		_ = os.RemoveAll(finalDir)
		if err := os.Rename(staging, finalDir); err != nil {
			_ = os.RemoveAll(staging)
			return nil, fmt.Errorf("迁移插件目录失败: %w", err)
		}
	}
	p := &plugin.Plugin{
		Name:        manifest.Name,
		Version:     manifest.Version,
		Description: manifest.Description,
		Marketplace: marketplace,
		InstallPath: finalDir,
		Enabled:     true, // 安装后默认启用，可用 disable 关闭
		Manifest:    manifest,
	}
	s.registry.AddPlugin(p)
	if err := s.persist(); err != nil {
		return nil, err
	}
	s.logger.Info("已安装插件", "name", p.Name, "version", p.Version, "marketplace", marketplace)
	return p, nil
}

// Uninstall 卸载插件（删除本地目录并从注册表移除）。
func (s *PluginService) Uninstall(ctx context.Context, name string) error {
	p, ok := s.registry.GetPlugin(name)
	if !ok {
		return fmt.Errorf("插件 %q 未安装", name)
	}
	if p.InstallPath != "" {
		if err := s.installer.Uninstall(ctx, p.InstallPath); err != nil {
			return err
		}
	}
	s.registry.RemovePlugin(name)
	return s.persist()
}

// Enable 启用插件。
func (s *PluginService) Enable(name string) error {
	if !s.registry.SetEnabled(name, true) {
		return fmt.Errorf("插件 %q 未安装", name)
	}
	return s.persist()
}

// Disable 禁用插件。
func (s *PluginService) Disable(name string) error {
	if !s.registry.SetEnabled(name, false) {
		return fmt.Errorf("插件 %q 未安装", name)
	}
	return s.persist()
}

// ListPlugins 返回所有已安装插件。
func (s *PluginService) ListPlugins() []*plugin.Plugin { return s.registry.Plugins() }

// Contributions 汇总所有已启用插件的扩展贡献（绝对路径集合）。
func (s *PluginService) Contributions() []plugin.Contributions {
	var out []plugin.Contributions
	for _, p := range s.registry.EnabledPlugins() {
		if p.InstallPath == "" {
			continue
		}
		m := p.Manifest
		if m == nil {
			parsed, err := s.loader.ParseManifest(p.InstallPath)
			if err != nil {
				s.logger.Warn("跳过无效插件贡献", "plugin", p.Name, "error", err)
				continue
			}
			m = parsed
			p.Manifest = parsed
		}
		c := plugininfra.ResolveContributions(p.InstallPath, m)
		if c.PluginName == "" {
			c.PluginName = p.Name
		}
		out = append(out, c)
	}
	return out
}

// ---- helpers ----

// isBarePluginName 判断 ref 是否为"裸插件名"（应跨市场按名解析，而非作为来源）。
//
// 条件（全部满足）：被 ClassifySource 归为本地、不含路径分隔符、不以 . 或 ~ 开头、
// 且本地不存在同名文件/目录。这样既能让 `plugin install foo` 走市场解析，又不会
// 误伤真实存在的本地目录来源（后者仍按 source 安装）。
func isBarePluginName(ref string) bool {
	if plugin.ClassifySource(ref) != plugin.SourceLocal {
		return false
	}
	if strings.ContainsAny(ref, `/\`) {
		return false
	}
	if strings.HasPrefix(ref, ".") || strings.HasPrefix(ref, "~") {
		return false
	}
	if _, err := os.Stat(ref); err == nil {
		return false
	}
	return true
}

// parsePluginRef 解析 "name@marketplace"。
func parsePluginRef(ref string) (name, marketplace string, ok bool) {
	i := strings.LastIndex(ref, "@")
	if i <= 0 || i == len(ref)-1 {
		return "", "", false
	}
	// 避免把 git@host:... 误判为 ref
	if strings.HasPrefix(ref, "git@") {
		return "", "", false
	}
	name = strings.TrimSpace(ref[:i])
	marketplace = strings.TrimSpace(ref[i+1:])
	if name == "" || marketplace == "" {
		return "", "", false
	}
	// marketplace 段不应包含路径分隔符/scheme
	if strings.ContainsAny(marketplace, "/:\\") {
		return "", "", false
	}
	return name, marketplace, true
}

// resolveEntrySource 计算市场条目的实际安装来源。
//
// 条目 source 为相对路径时相对市场根解析；否则（绝对路径/git/http）原样使用。
// 对象形式的 git 来源若带 ref，则以 "<url>#<ref>" 形式附加，供 installer 解析。
func resolveEntrySource(m *plugin.Marketplace, entry plugin.MarketplaceEntry) string {
	src, ref := entry.Source.Locator()
	src = strings.TrimSpace(src)
	if src == "" {
		// 缺省按条目名作为市场根下的子目录
		return filepath.Join(m.LocalPath, entry.Name)
	}
	if plugin.ClassifySource(src) == plugin.SourceLocal && !filepath.IsAbs(src) {
		return filepath.Join(m.LocalPath, src)
	}
	// git 来源附加可选 ref（分支/标签），由 installer 解析后 checkout。
	if ref != "" && plugin.ClassifySource(src) == plugin.SourceGit {
		return src + "#" + ref
	}
	return src
}
