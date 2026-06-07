// Package configdir 提供统一的配置目录路径解析。
//
// goclaude 使用 .goclaude/ 作为主配置目录，同时保持对 .claude/ 的后向兼容读取。
//
// 规则：
//   - 写入：仅使用 .goclaude/
//   - 读取：优先 .goclaude/，兜底 .claude/
package configdir

import "path/filepath"

const (
	// PrimaryConfigDir 主配置目录名
	PrimaryConfigDir = ".goclaude"
	// LegacyConfigDir 旧配置目录名（仅用于兼容读取）
	LegacyConfigDir = ".claude"
)

// DirNames 返回配置目录名列表（优先级从高到低）。
func DirNames() []string {
	return []string{PrimaryConfigDir, LegacyConfigDir}
}

// AllReadDirs 返回某 root 下的所有读取候选路径（新路径优先，旧路径兜底）。
//
// 示例：
//
//	AllReadDirs(home, "settings.json") → [
//	  "~/.goclaude/settings.json",
//	  "~/.claude/settings.json",
//	]
func AllReadDirs(root string, subPath ...string) []string {
	return []string{
		JoinPrimary(root, subPath...),
		JoinLegacy(root, subPath...),
	}
}

// WriteDir 返回主写入路径（仅 .goclaude/）。
func WriteDir(root string, subPath ...string) string {
	return JoinPrimary(root, subPath...)
}

// JoinPrimary 返回 root/.goclaude/elem...
func JoinPrimary(root string, elem ...string) string {
	args := append([]string{root, PrimaryConfigDir}, elem...)
	return filepath.Join(args...)
}

// JoinLegacy 返回 root/.claude/elem...
func JoinLegacy(root string, elem ...string) string {
	args := append([]string{root, LegacyConfigDir}, elem...)
	return filepath.Join(args...)
}
