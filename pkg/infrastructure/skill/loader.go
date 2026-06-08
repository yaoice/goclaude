// Package skillinfra 实现 Skills 的文件系统加载逻辑
//
// 对齐 src/skills/loadSkillsDir.ts 的核心语义：
//   - 目录格式：<skill-name>/SKILL.md
//   - 三级目录优先级：policySettings > userSettings > projectSettings
//   - 同一文件按 realpath 去重
//   - 支持 paths frontmatter 触发的条件激活
package skillinfra

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropics/goclaude/pkg/domain/skill"
	"github.com/anthropics/goclaude/pkg/infrastructure/configdir"
	"github.com/anthropics/goclaude/pkg/util/frontmatter"
)

// Loader 文件系统 Skill 加载器
type Loader struct {
	// HomeDir 用户 home，默认从 os.UserHomeDir 获取
	HomeDir string
}

// NewLoader 创建加载器
func NewLoader() *Loader {
	home, _ := os.UserHomeDir()
	return &Loader{HomeDir: home}
}

// LoadFromDir 加载单个 skills 根目录中的所有 skill
//
// 仅支持目录格式：dir/<skill-name>/SKILL.md
// （对齐 src loadSkillsFromSkillsDir：单个 .md 文件不在 /skills/ 目录中受支持）
func (l *Loader) LoadFromDir(ctx context.Context, dir string, source skill.Source) ([]*skill.Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var skills []*skill.Skill
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// 只接受目录或符号链接到目录
		if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			continue
		}
		skillDir := filepath.Join(dir, entry.Name())
		skillFile := filepath.Join(skillDir, "SKILL.md")

		raw, err := os.ReadFile(skillFile)
		if err != nil {
			// 不存在 SKILL.md 静默跳过
			continue
		}
		s, err := parseSkill(string(raw), entry.Name(), skillDir, skillFile, source, skill.LoadedFromSkills)
		if err != nil {
			continue
		}
		skills = append(skills, s)
	}
	return skills, nil
}

// LoadAll 按 src 的优先级（managed > user > project）加载并合并所有 skill。
// projectDirs 为项目从 cwd 向上到 home 之间的 .claude/skills 目录列表（最近的在前）。
//
// 与 src 一致：合并顺序后通过 realpath 去重，确保符号链接/重复挂载只被加载一次。
func (l *Loader) LoadAll(
	ctx context.Context,
	managedDir string,
	userDir string,
	projectDirs []string,
) ([]*skill.Skill, error) {
	var groups [][]*skill.Skill

	if managedDir != "" {
		managed, err := l.LoadFromDir(ctx, managedDir, skill.SourceManaged)
		if err != nil {
			return nil, err
		}
		groups = append(groups, managed)
	}
	if userDir != "" {
		user, err := l.LoadFromDir(ctx, userDir, skill.SourceUser)
		if err != nil {
			return nil, err
		}
		groups = append(groups, user)
	}
	for _, dir := range projectDirs {
		project, err := l.LoadFromDir(ctx, dir, skill.SourceProject)
		if err != nil {
			return nil, err
		}
		groups = append(groups, project)
	}

	// 拍平 + 按 realpath 去重（先到先得）
	seen := make(map[string]bool)
	var out []*skill.Skill
	for _, g := range groups {
		for _, s := range g {
			id := canonicalPath(s.FilePath)
			if id != "" && seen[id] {
				continue
			}
			if id != "" {
				seen[id] = true
			}
			out = append(out, s)
		}
	}
	return out, nil
}

// DefaultUserSkillsDir 默认用户 skills 目录：~/.goclaude/skills（优先），~/.claude/skills（兜底）
func (l *Loader) DefaultUserSkillsDir() string {
	if l.HomeDir == "" {
		return ""
	}
	return configdir.JoinPrimary(l.HomeDir, "skills")
}

// ProjectSkillsDirs 从 cwd 向上查找到 home 的所有 skills 目录（最近的在前，新老目录各一份）。
//
// 对齐 src/utils/markdownConfigLoader.getProjectDirsUpToHome 的语义。
// 当 cwd 不在 home 之下时，最多向上 16 层避免 stat 系统级路径。
func (l *Loader) ProjectSkillsDirs(cwd string) []string {
	var dirs []string
	current := filepath.Clean(cwd)
	home := ""
	if l.HomeDir != "" {
		home = filepath.Clean(l.HomeDir)
	}
	const maxDepth = 16
	for i := 0; i < maxDepth; i++ {
		dirs = append(dirs,
			configdir.JoinPrimary(current, "skills"),
			configdir.JoinLegacy(current, "skills"),
		)
		if home != "" && current == home {
			break
		}
		parent := filepath.Dir(current)
		if parent == current { // 到达根
			break
		}
		current = parent
	}
	return dirs
}

// canonicalPath 返回去除符号链接后的绝对路径用于去重
func canonicalPath(p string) string {
	if p == "" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		// 取不到 realpath 时退化为原路径
		abs, err2 := filepath.Abs(p)
		if err2 != nil {
			return p
		}
		return abs
	}
	return resolved
}

// parseSkill 解析单个 SKILL.md 文件
func parseSkill(
	raw, skillName, skillDir, filePath string,
	source skill.Source,
	loadedFrom skill.LoadedFrom,
) (*skill.Skill, error) {
	fm, body, err := frontmatter.Parse(raw)
	if err != nil {
		return nil, err
	}

	userInvocable := true
	if p := frontmatter.GetBoolPtr(fm, "user-invocable"); p != nil {
		userInvocable = *p
	}

	displayName := frontmatter.GetString(fm, "name")
	description := frontmatter.GetString(fm, "description")
	if description == "" {
		// 与 src extractDescriptionFromMarkdown 兼容：取 body 首行非空文本
		description = firstNonEmptyLine(body)
	}

	s := &skill.Skill{
		Name:                   skillName,
		DisplayName:            displayName,
		Description:            description,
		Aliases:                frontmatter.GetStringSlice(fm, "aliases"),
		WhenToUse:              frontmatter.GetString(fm, "when_to_use"),
		ArgumentHint:           frontmatter.GetString(fm, "argument-hint"),
		ArgumentNames:          frontmatter.GetStringSlice(fm, "arguments"),
		AllowedTools:           frontmatter.GetStringSlice(fm, "allowed-tools"),
		Model:                  frontmatter.GetString(fm, "model"),
		Version:                frontmatter.GetString(fm, "version"),
		DisableModelInvocation: frontmatter.GetBool(fm, "disable-model-invocation"),
		UserInvocable:          userInvocable,
		IsHidden:               !userInvocable,
		ExecutionContext:       normalizeContext(frontmatter.GetString(fm, "context")),
		Agent:                  frontmatter.GetString(fm, "agent"),
		Paths:                  parsePaths(frontmatter.GetStringSlice(fm, "paths")),
		Content:                body,
		ContentLength:          len(body),
		SkillRoot:              skillDir,
		FilePath:               filePath,
		Source:                 source,
		LoadedFrom:             loadedFrom,
		IsEnabled:              true,
	}
	return s, nil
}

func firstNonEmptyLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		// 跳过 markdown 标题前缀
		t = strings.TrimLeft(t, "# ")
		if t != "" {
			return t
		}
	}
	return ""
}

func normalizeContext(s string) string {
	if s == "fork" {
		return "fork"
	}
	return ""
}

// parsePaths 去掉 /** 后缀并过滤掉 ** 这种全匹配项
func parsePaths(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	allMatchAll := true
	for _, p := range in {
		p = strings.TrimSuffix(p, "/**")
		if p == "" {
			continue
		}
		if p != "**" {
			allMatchAll = false
		}
		out = append(out, p)
	}
	if allMatchAll || len(out) == 0 {
		return nil
	}
	return out
}
