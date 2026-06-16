// Package plugin 定义 Plugin 系统的领域模型与接口（零外部依赖）。
//
// 对齐 Claude Code 的插件机制：
//   - Marketplace（插件市场）：一个包含若干插件的来源，支持本地目录 / 远程 git /
//     HTTP(S) 压缩包三种类型。其根目录含 .claude-plugin/marketplace.json。
//   - Plugin（插件）：一个目录，根含 .claude-plugin/plugin.json（Manifest），
//     可贡献 commands / agents / skills / hooks / MCP servers 五类扩展。
//   - 生命周期：marketplace add/remove → plugin install/uninstall → enable/disable。
//
// 本包仅定义纯数据模型、注册表与依赖倒置接口；具体的下载、解析与持久化由
// infrastructure 层实现，编排由 application 层完成。
package plugin

import (
	"encoding/json"
	"regexp"
	"strings"
)

// SourceType 描述一个 marketplace（或插件来源）的获取方式。
type SourceType string

const (
	// SourceLocal 本地目录路径（纯文件系统，最安全）。
	SourceLocal SourceType = "local"
	// SourceGit 远程 git 仓库（git clone）。
	SourceGit SourceType = "git"
	// SourceHTTP 远程 HTTP(S) tar/zip 压缩包。
	SourceHTTP SourceType = "http"
)

// Author 插件/市场作者；兼容 JSON 中字符串或对象两种写法。
type Author struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

// UnmarshalJSON 兼容 "author": "name" 与 "author": {"name": "..."} 两种形式。
func (a *Author) UnmarshalJSON(data []byte) error {
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		a.Name = s
		return nil
	}
	// 用别名类型避免递归调用本方法
	type authorAlias Author
	var alias authorAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	*a = Author(alias)
	return nil
}

// StringList 兼容 JSON 中字符串或字符串数组两种写法（如 commands 字段）。
type StringList []string

// UnmarshalJSON 兼容 "commands": "./a" 与 "commands": ["./a", "./b"]。
func (s *StringList) UnmarshalJSON(data []byte) error {
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '[' {
		var arr []string
		if err := json.Unmarshal(data, &arr); err != nil {
			return err
		}
		*s = arr
		return nil
	}
	var single string
	if err := json.Unmarshal(data, &single); err != nil {
		return err
	}
	*s = []string{single}
	return nil
}

// Manifest 对应插件根目录下 .claude-plugin/plugin.json。
//
// 贡献路径字段（Commands/Agents/Skills）省略时使用约定默认目录
// （commands/ agents/ skills/）；Hooks 省略时默认 hooks/hooks.json；
// MCPServers 省略时默认 .mcp.json。所有路径相对插件根。
type Manifest struct {
	Name        string     `json:"name"`
	Version     string     `json:"version,omitempty"`
	Description string     `json:"description,omitempty"`
	Author      Author     `json:"author,omitempty"`
	Homepage    string     `json:"homepage,omitempty"`
	License     string     `json:"license,omitempty"`
	Keywords    []string   `json:"keywords,omitempty"`
	Commands    StringList `json:"commands,omitempty"`
	Agents      StringList `json:"agents,omitempty"`
	Skills      StringList `json:"skills,omitempty"`
	Hooks       string     `json:"hooks,omitempty"`
	MCPServers  string     `json:"mcpServers,omitempty"`
}

// Plugin 表示一个已安装到本地的插件实例。
type Plugin struct {
	// Name 插件唯一名（来自 manifest 或 marketplace 条目）。
	Name string `json:"name"`
	// Version 版本。
	Version string `json:"version,omitempty"`
	// Description 描述。
	Description string `json:"description,omitempty"`
	// Marketplace 来源市场名（直接 install 本地路径时可为空）。
	Marketplace string `json:"marketplace,omitempty"`
	// InstallPath 安装到本地后的插件根目录绝对路径。
	InstallPath string `json:"installPath"`
	// Enabled 是否启用（启用后其贡献才会被装配）。
	Enabled bool `json:"enabled"`
	// Manifest 解析后的 manifest（运行时填充，不持久化全部细节）。
	Manifest *Manifest `json:"-"`
}

// EntrySource 市场条目的插件来源，兼容 JSON 中"字符串"与"对象"两种写法。
//
// 字符串形式（最常见）：相对市场根的目录、git URL 或 http(s) 压缩包 URL。
// 对象形式（对齐 Claude Code）：
//
//	{"source": "git"|"url",    "url": "https://...", "ref": "branch"}
//	{"source": "github",        "repo": "owner/repo", "ref": "tag"}
//	{"source": "local"|"path", "path": "relative/dir"}
type EntrySource struct {
	// Type 对象形式的 source 子字段（git/url/github/local 等）；字符串形式为空。
	Type string `json:"source,omitempty"`
	// URL git/http(s) 地址。
	URL string `json:"url,omitempty"`
	// Repo "owner/repo" 简写（github 形式）。
	Repo string `json:"repo,omitempty"`
	// Path 相对市场根（或仓库内）的子目录。
	Path string `json:"path,omitempty"`
	// Ref git 分支/标签/commit（仅 git 来源有意义）。
	Ref string `json:"ref,omitempty"`

	// raw 字符串形式的原始来源（当 JSON 为字符串时填充）。
	raw string
}

// UnmarshalJSON 兼容 "source": "./p" 与 "source": {"source":"url","url":"..."} 两种形式。
func (s *EntrySource) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if trimmed[0] == '"' {
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return err
		}
		s.raw = strings.TrimSpace(str)
		return nil
	}
	// 用别名类型避免递归调用本方法
	type sourceAlias EntrySource
	var alias sourceAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	*s = EntrySource(alias)
	return nil
}

// MarshalJSON 字符串形式回写为字符串，对象形式回写为对象。
func (s EntrySource) MarshalJSON() ([]byte, error) {
	if s.raw != "" && s.Type == "" && s.URL == "" && s.Repo == "" && s.Path == "" {
		return json.Marshal(s.raw)
	}
	type sourceAlias EntrySource
	return json.Marshal(sourceAlias(s))
}

// IsZero 判断来源是否为空（无任何可用定位信息）。
func (s EntrySource) IsZero() bool {
	return s.raw == "" && s.URL == "" && s.Repo == "" && s.Path == ""
}

// Locator 返回该来源对应的"原始来源字符串"与可选 git ref。
//
// 返回的 source 可能仍是相对市场根的路径（需调用方按需与市场根拼接）；
// git/http URL 与绝对路径则原样返回。github 简写会展开为 https git URL。
func (s EntrySource) Locator() (source, ref string) {
	switch {
	case s.URL != "":
		return strings.TrimSpace(s.URL), s.Ref
	case s.Repo != "":
		repo := strings.TrimSuffix(strings.TrimSpace(s.Repo), ".git")
		return "https://github.com/" + repo + ".git", s.Ref
	case s.Path != "":
		return strings.TrimSpace(s.Path), s.Ref
	default:
		return s.raw, s.Ref
	}
}

// MarketplaceEntry 市场清单中列出的单个可安装插件。
type MarketplaceEntry struct {
	// Name 插件名。
	Name string `json:"name"`
	// Description 描述。
	Description string `json:"description,omitempty"`
	// Version 版本。
	Version string `json:"version,omitempty"`
	// Source 插件来源：相对市场根的目录、git URL 或 http(s) 压缩包 URL，
	// 亦兼容 Claude Code 的对象形式（见 EntrySource）。
	Source EntrySource `json:"source"`
}

// MarketplaceManifest 对应 .claude-plugin/marketplace.json。
type MarketplaceManifest struct {
	Name    string             `json:"name"`
	Owner   Author             `json:"owner,omitempty"`
	Plugins []MarketplaceEntry `json:"plugins"`
}

// Marketplace 表示一个已注册的插件市场。
type Marketplace struct {
	// Name 市场名（来自 manifest）。
	Name string `json:"name"`
	// Type 来源类型。
	Type SourceType `json:"type"`
	// Source 用户添加时提供的原始来源（本地路径 / git URL / http URL）。
	Source string `json:"source"`
	// LocalPath 市场内容缓存到本地后的根目录绝对路径。
	LocalPath string `json:"localPath"`
	// Entries 市场中列出的插件清单（运行时填充）。
	Entries []MarketplaceEntry `json:"-"`
}

// FindEntry 在市场条目中按名查找。
func (m *Marketplace) FindEntry(name string) (MarketplaceEntry, bool) {
	for _, e := range m.Entries {
		if e.Name == name {
			return e, true
		}
	}
	return MarketplaceEntry{}, false
}

// State 持久化的插件系统状态（marketplaces + 已装插件）。
type State struct {
	Marketplaces []*Marketplace `json:"marketplaces"`
	Plugins      []*Plugin      `json:"plugins"`
}

// Contributions 解析单个插件 manifest 后得到的、可投喂给各注册表的绝对路径集合。
//
// 由 application 层在装配阶段消费：
//   - CommandDirs / AgentDirs / SkillDirs 交给对应 Loader 以 plugin 来源加载
//   - HookFiles 交给 hook 命令执行器解析 hooks.json
//   - MCPConfigFiles 交给 MCP 服务加载 .mcp.json
type Contributions struct {
	PluginName     string
	Root           string
	CommandDirs    []string
	AgentDirs      []string
	SkillDirs      []string
	HookFiles      []string
	MCPConfigFiles []string
}

// githubShorthandRe 匹配 "owner/repo" 形式的 GitHub 简写（恰好一个斜杠，两段均为
// 合法的 GitHub 名称字符）。形如 "obra/superpowers-marketplace"。
var githubShorthandRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

// NormalizeSource 将 "owner/repo" 的 GitHub 简写展开为完整 git URL，其余原样返回。
//
// 仅当字符串不含 scheme、不以 git@ / . / / / ~ / \ 开头，且恰好匹配 owner/repo
// 形态时才视为 GitHub 简写。这样 "obra/superpowers-marketplace" 默认走 GitHub，
// 而 "./a/b"、"/abs/path"、"git@host:repo.git"、含多级路径的相对目录不受影响。
func NormalizeSource(source string) string {
	s := strings.TrimSpace(source)
	if s == "" {
		return s
	}
	lower := strings.ToLower(s)
	if strings.Contains(s, "://") ||
		strings.HasPrefix(lower, "git@") ||
		strings.HasPrefix(s, ".") ||
		strings.HasPrefix(s, "/") ||
		strings.HasPrefix(s, "~") ||
		strings.HasPrefix(s, `\`) {
		return s
	}
	if githubShorthandRe.MatchString(s) {
		repo := strings.TrimSuffix(s, ".git")
		return "https://github.com/" + repo + ".git"
	}
	return s
}

// ClassifySource 根据用户提供的来源字符串推断 SourceType。
//
// 规则（保守优先本地）：
//   - "owner/repo" 的 GitHub 简写 → git（先经 NormalizeSource 展开）
//   - 以 .git 结尾、git@ 前缀、或 git+ 前缀 → git
//   - http(s):// 且以 .tar/.tar.gz/.tgz/.zip 结尾 → http（压缩包）
//   - http(s):// 其它（含 github.com 仓库 URL）→ git（按仓库克隆）
//   - 其余 → local
func ClassifySource(source string) SourceType {
	s := NormalizeSource(strings.TrimSpace(source))
	lower := strings.ToLower(s)
	switch {
	case strings.HasPrefix(lower, "git@"),
		strings.HasPrefix(lower, "git+"),
		strings.HasSuffix(lower, ".git"):
		return SourceGit
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		if hasArchiveExt(lower) {
			return SourceHTTP
		}
		return SourceGit
	default:
		return SourceLocal
	}
}

func hasArchiveExt(lower string) bool {
	for _, ext := range []string{".tar.gz", ".tgz", ".tar", ".zip"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}
