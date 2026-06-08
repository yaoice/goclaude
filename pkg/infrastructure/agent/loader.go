// Package agentinfra 实现 Subagent 加载、内置 agents 与执行编排
package agentinfra

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/anthropics/goclaude/pkg/domain/agent"
	"github.com/anthropics/goclaude/pkg/infrastructure/configdir"
	"github.com/anthropics/goclaude/pkg/util/frontmatter"
)

// Loader 文件系统 Agent 加载器
//
// 对齐 src parseAgentFromMarkdown：每个 .md 文件 = 一个 agent；
// frontmatter 必须含 name 与 description，否则跳过。
type Loader struct {
	HomeDir string
}

// NewLoader 创建 loader
func NewLoader() *Loader {
	home, _ := os.UserHomeDir()
	return &Loader{HomeDir: home}
}

// LoadFromDir 加载某目录下所有 .md agent 定义（不递归）
func (l *Loader) LoadFromDir(ctx context.Context, dir string, source agent.Source) ([]*agent.Definition, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []*agent.Definition
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}
		filePath := filepath.Join(dir, name)
		raw, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		d, ok := parseAgent(string(raw), filePath, dir, source)
		if !ok {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

// LoadAll 按优先级加载所有 agent 来源
//
// managedDir / userDir 可为空；projectDirs 是从 cwd 向上的 .claude/agents 链。
func (l *Loader) LoadAll(
	ctx context.Context,
	managedDir, userDir string,
	projectDirs []string,
) ([]*agent.Definition, error) {
	var all []*agent.Definition

	if managedDir != "" {
		ds, err := l.LoadFromDir(ctx, managedDir, agent.SourceManaged)
		if err != nil {
			return nil, err
		}
		all = append(all, ds...)
	}
	if userDir != "" {
		ds, err := l.LoadFromDir(ctx, userDir, agent.SourceUser)
		if err != nil {
			return nil, err
		}
		all = append(all, ds...)
	}
	for _, dir := range projectDirs {
		ds, err := l.LoadFromDir(ctx, dir, agent.SourceProject)
		if err != nil {
			return nil, err
		}
		all = append(all, ds...)
	}
	return all, nil
}

// DefaultUserAgentsDir 默认用户级目录：~/.goclaude/agents（优先），~/.claude/agents（兜底）
func (l *Loader) DefaultUserAgentsDir() string {
	if l.HomeDir == "" {
		return ""
	}
	return configdir.JoinPrimary(l.HomeDir, "agents")
}

// ProjectAgentsDirs 从 cwd 向上到 home 的 agents 目录链（最近的在前，新老目录各一份）
//
// 当 cwd 不在 home 之下时，最多向上 16 层避免 stat 系统级路径。
func (l *Loader) ProjectAgentsDirs(cwd string) []string {
	var dirs []string
	current := filepath.Clean(cwd)
	home := ""
	if l.HomeDir != "" {
		home = filepath.Clean(l.HomeDir)
	}
	const maxDepth = 16
	for i := 0; i < maxDepth; i++ {
		dirs = append(dirs,
			configdir.JoinPrimary(current, "agents"),
			configdir.JoinLegacy(current, "agents"),
		)
		if home != "" && current == home {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return dirs
}

// parseAgent 解析单个 .md 文件
func parseAgent(raw, filePath, baseDir string, source agent.Source) (*agent.Definition, bool) {
	fm, body, err := frontmatter.Parse(raw)
	if err != nil {
		return nil, false
	}
	name := frontmatter.GetString(fm, "name")
	desc := frontmatter.GetString(fm, "description")
	if name == "" || desc == "" {
		return nil, false
	}
	// 兼容 description 中的 \n 转义（对齐 src）
	desc = strings.ReplaceAll(desc, `\n`, "\n")

	maxTurns := 0
	if s := frontmatter.GetString(fm, "maxTurns"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			maxTurns = n
		}
	}

	systemPrompt := strings.TrimSpace(body)

	return &agent.Definition{
		AgentType:       name,
		WhenToUse:       desc,
		Tools:           frontmatter.GetStringSlice(fm, "tools"),
		DisallowedTools: frontmatter.GetStringSlice(fm, "disallowedTools"),
		Model:           frontmatter.GetString(fm, "model"),
		PermissionMode:  frontmatter.GetString(fm, "permissionMode"),
		MaxTurns:        maxTurns,
		Skills:          frontmatter.GetStringSlice(fm, "skills"),
		MCPServers:      frontmatter.GetStringSlice(fm, "mcpServers"),
		SystemPrompt:    systemPrompt,
		Source:          source,
		FilePath:        filePath,
		BaseDir:         baseDir,
		Color:           frontmatter.GetString(fm, "color"),
		Effort:          frontmatter.GetString(fm, "effort"),
		Memory:          frontmatter.GetString(fm, "memory"),
		Isolation:       frontmatter.GetString(fm, "isolation"),
	}, true
}
