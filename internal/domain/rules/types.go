// Package rules 定义规则系统领域模型
package rules

// MemoryType 记忆文件类型
type MemoryType string

const (
	MemoryTypeManaged MemoryType = "Managed"
	MemoryTypeUser    MemoryType = "User"
	MemoryTypeProject MemoryType = "Project"
	MemoryTypeLocal   MemoryType = "Local"
	MemoryTypeAutoMem MemoryType = "AutoMem"
	MemoryTypeTeamMem MemoryType = "TeamMem"
)

// MemoryFileInfo 记忆文件信息
type MemoryFileInfo struct {
	Path                string     `json:"path"`
	Type                MemoryType `json:"type"`
	Content             string     `json:"content"`
	Parent              string     `json:"parent,omitempty"`
	Globs               []string   `json:"globs,omitempty"`
	ModTime            int64      `json:"mtime_ms,omitempty"`
	ContentDiffersFromDisk bool       `json:"content_differs_from_disk,omitempty"`
	RawContent          string     `json:"raw_content,omitempty"`
}

// Frontmatter 前置元数据
type Frontmatter struct {
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"`
	Paths       string `json:"paths,omitempty"`
	Raw         string `json:"-"`
}

// DirEntry 目录条目
type DirEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
}

// FileInfo 文件信息
type FileInfo struct {
	Path     string    `json:"path"`
	IsDir    bool      `json:"is_dir"`
	IsSymlink bool      `json:"is_symlink"`
	ModTime  int64     `json:"mod_time"`
	Size     int64     `json:"size"`
}

// LoadOptions 加载选项
type LoadOptions struct {
	OriginalCwd           string
	GitRoot               string
	CanonicalRoot         string
	UserSettingsEnabled   bool
	ProjectSettingsEnabled bool
	LocalSettingsEnabled  bool
	AutoMemoryEnabled     bool
	TeamMemoryEnabled     bool
	IncludeExternal       bool
	SkipProject           bool
	ManagedClaudeMdPath   string
	UserClaudeMdPath      string
	ManagedClaudeRulesDir string
	UserClaudeRulesDir    string
	AutoMemDir            string
	TeamMemDir            string
	ClaudeMdExcludes     []string
}
