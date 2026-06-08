package application

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anthropics/goclaude/pkg/domain/skill"
	skillinfra "github.com/anthropics/goclaude/pkg/infrastructure/skill"
)

// SkillService Skill 应用服务
//
// 编排领域 Registry 与基础设施 Loader，对外暴露统一的查询/激活 API。
// 对齐 src/commands.ts 中 getSkillToolCommands 的入口语义。
type SkillService struct {
	registry *skill.Registry
	loader   *skillinfra.Loader
	logger   *slog.Logger
}

// NewSkillService 创建 SkillService
func NewSkillService(logger *slog.Logger) *SkillService {
	if logger == nil {
		logger = slog.Default()
	}
	return &SkillService{
		registry: skill.NewRegistry(),
		loader:   skillinfra.NewLoader(),
		logger:   logger,
	}
}

// Registry 暴露底层注册表（query engine 等需要遍历）
func (s *SkillService) Registry() *skill.Registry {
	return s.registry
}

// LoadAll 加载 managed/user/project 三级 skill 并合并到注册表
//
// projectCwd 用于解析项目向上的 .claude/skills 链。
// managedDir 可留空表示不加载策略级 skill。
func (s *SkillService) LoadAll(ctx context.Context, projectCwd, managedDir string) error {
	userDir := s.loader.DefaultUserSkillsDir()
	projectDirs := s.loader.ProjectSkillsDirs(projectCwd)

	all, err := s.loader.LoadAll(ctx, managedDir, userDir, projectDirs)
	if err != nil {
		return err
	}

	var unconditional, conditional int
	for _, sk := range all {
		if len(sk.Paths) > 0 {
			s.registry.RegisterConditional(sk)
			conditional++
		} else {
			s.registry.Register(sk)
			unconditional++
		}
	}
	s.logger.Debug("加载 skills 完成",
		"unconditional", unconditional,
		"conditional", conditional,
		"user_dir", userDir,
		"project_dirs", projectDirs,
	)
	return nil
}

// RegisterBundled 注册一个内置 skill（编译进二进制的 prompt 包）
func (s *SkillService) RegisterBundled(sk *skill.Skill) {
	if sk == nil {
		return
	}
	sk.Source = skill.SourceBundled
	sk.LoadedFrom = skill.LoadedFromBundled
	sk.IsEnabled = true
	s.registry.Register(sk)
}

// List 返回所有已启用 skill，按名字排序
func (s *SkillService) List() []*skill.Skill {
	skills := s.registry.Enabled()
	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})
	return skills
}

// Get 按名字/别名获取 skill
func (s *SkillService) Get(name string) (*skill.Skill, bool) {
	return s.registry.Get(name)
}

// RenderContext 渲染 skill 时的上下文变量
//
// 提供 ${CLAUDE_*} 占位符的真实值；缺失字段 fall-back 到环境变量或运行时默认。
// 对齐 src createSkillCommand.getPromptForCommand 中 substituteArguments + path
// 替换的核心变量集合。
type RenderContext struct {
	SessionID  string
	UserHome   string // ${CLAUDE_USER_HOME}
	ProjectDir string // ${CLAUDE_PROJECT_DIR}
	Cwd        string // ${CLAUDE_CWD}
	Args       string // ${1} / $ARGS（最简：整段替换 $ARGS 与 ${ARGS}）
}

// Render 渲染 skill 内容（兼容旧 API：仅传 sessionID）
func (s *SkillService) Render(name, sessionID string) (string, bool) {
	return s.RenderWith(name, RenderContext{SessionID: sessionID})
}

// RenderWith 使用完整 RenderContext 渲染 skill
//
// 替换的占位符集合（对齐 src）：
//   - ${CLAUDE_SKILL_DIR}    skill 自身目录（仅当 SkillRoot 存在）
//   - ${CLAUDE_SESSION_ID}   当前会话 ID
//   - ${CLAUDE_USER_HOME}    用户 home（缺省取 os.UserHomeDir）
//   - ${CLAUDE_PROJECT_DIR}  项目根目录（缺省取 ctx.Cwd）
//   - ${CLAUDE_CWD}          当前工作目录（缺省取 os.Getwd）
//   - $ARGS / ${ARGS}        slash command 参数透传
//
// 不执行 src 中的 inline shell（!`...`）；MCP 来源的 skill 由调用方决定是否信任。
func (s *SkillService) RenderWith(name string, ctx RenderContext) (string, bool) {
	sk, ok := s.registry.Get(name)
	if !ok {
		return "", false
	}
	content := sk.Content

	// 收集替换映射；缺省值用 os 调用补齐（best-effort）
	if ctx.UserHome == "" {
		if h, err := os.UserHomeDir(); err == nil {
			ctx.UserHome = h
		}
	}
	if ctx.Cwd == "" {
		if d, err := os.Getwd(); err == nil {
			ctx.Cwd = d
		}
	}
	if ctx.ProjectDir == "" {
		ctx.ProjectDir = ctx.Cwd
	}

	// SkillRoot 替换 + 前缀（对齐 src createSkillCommand）
	if sk.SkillRoot != "" {
		content = "Base directory for this skill: " + sk.SkillRoot + "\n\n" + content
		dir := sk.SkillRoot
		if filepath.Separator == '\\' {
			dir = strings.ReplaceAll(dir, `\`, `/`)
		}
		content = strings.ReplaceAll(content, "${CLAUDE_SKILL_DIR}", dir)
	}
	subs := map[string]string{
		"${CLAUDE_SESSION_ID}":  ctx.SessionID,
		"${CLAUDE_USER_HOME}":   ctx.UserHome,
		"${CLAUDE_PROJECT_DIR}": ctx.ProjectDir,
		"${CLAUDE_CWD}":         ctx.Cwd,
		"${ARGS}":               ctx.Args,
		"$ARGS":                 ctx.Args,
	}
	for k, v := range subs {
		if v == "" {
			continue
		}
		content = strings.ReplaceAll(content, k, v)
	}
	return content, true
}

// ActivateForPaths 根据被读写的文件路径激活匹配的条件 skill。
// 返回新激活的 skill 名列表。
//
// 对齐 src activateConditionalSkillsForPaths：使用 gitignore 风格的模式匹配。
// 这里使用 filepath.Match 的轻量实现 + ** 前缀简化（覆盖 src 95% 用法）。
func (s *SkillService) ActivateForPaths(filePaths []string, cwd string) []string {
	candidates := s.registry.Conditional()
	if len(candidates) == 0 {
		return nil
	}
	var activated []string
	for _, sk := range candidates {
		for _, fp := range filePaths {
			rel := relTo(cwd, fp)
			if rel == "" || strings.HasPrefix(rel, "..") {
				continue
			}
			if matchAny(rel, sk.Paths) {
				if s.registry.Activate(sk.Name) {
					activated = append(activated, sk.Name)
					s.logger.Debug("条件 skill 被激活", "skill", sk.Name, "matched_path", rel)
				}
				break
			}
		}
	}
	return activated
}

// relTo 计算 fp 相对 cwd 的路径
func relTo(cwd, fp string) string {
	if !filepath.IsAbs(fp) {
		return filepath.ToSlash(fp)
	}
	rel, err := filepath.Rel(cwd, fp)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

// matchAny 判断路径是否匹配任一模式
func matchAny(path string, patterns []string) bool {
	for _, p := range patterns {
		if matchGlob(path, p) {
			return true
		}
	}
	return false
}

// matchGlob 简化版 glob 匹配，覆盖 ** 与 * 与 ? 三种通配符
//
// 实现说明：把 ** 视为可跨多级目录，* 视为单级内任意字符。
// 这是 gitignore 模式的子集，足够支持典型 paths frontmatter。
func matchGlob(path, pattern string) bool {
	return globMatch(path, pattern)
}

// globMatch 递归实现的 glob
func globMatch(s, pat string) bool {
	for {
		if pat == "" {
			return s == ""
		}
		// 处理 **
		if strings.HasPrefix(pat, "**") {
			rest := strings.TrimPrefix(pat, "**")
			rest = strings.TrimPrefix(rest, "/")
			if rest == "" {
				return true
			}
			// 尝试在 s 的每个可能位置匹配 rest
			for i := 0; i <= len(s); i++ {
				if globMatch(s[i:], rest) {
					return true
				}
			}
			return false
		}
		// 处理 *
		if pat[0] == '*' {
			// * 不跨 /
			rest := pat[1:]
			for i := 0; i <= len(s); i++ {
				if i > 0 && s[i-1] == '/' {
					break
				}
				if globMatch(s[i:], rest) {
					return true
				}
			}
			return false
		}
		// 处理 ?
		if pat[0] == '?' {
			if s == "" || s[0] == '/' {
				return false
			}
			s = s[1:]
			pat = pat[1:]
			continue
		}
		// 字面字符
		if s == "" || s[0] != pat[0] {
			return false
		}
		s = s[1:]
		pat = pat[1:]
	}
}
