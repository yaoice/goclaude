// Package shell 实现交互式终端 shell（REPL）。
//
// History 提供持久化命令历史记录管理：
//   - 加载/保存到磁盘（~/.claude/shell_history）
//   - 内存中维护环形缓冲（默认 1000 条）
//   - 自动去重：连续重复的输入只保留一份
//   - 提供 ↑/↓ 导航接口（Prev/Next）与重置游标接口（Reset）
//
// 与 bash 的 history 行为对齐：会话内 Append 的新条目即时可用，
// 退出时再追加写入磁盘文件。
package shell

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	defaultHistoryMax  = 1000
	defaultHistoryFile = "shell_history"
)

// History 管理命令历史
type History struct {
	mu      sync.RWMutex
	entries []string
	cursor  int    // 导航游标：== len(entries) 表示"当前正在编辑"
	max     int    // 容量上限
	path    string // 持久化文件路径，空则不持久化
}

// NewHistory 构造一个历史记录器
//
// path 为空时仅在内存中保留（不持久化）。
func NewHistory(path string, max int) *History {
	if max <= 0 {
		max = defaultHistoryMax
	}
	h := &History{
		entries: make([]string, 0, 64),
		max:     max,
		path:    path,
	}
	h.cursor = 0
	return h
}

// DefaultHistoryPath 返回默认历史文件路径 ~/.claude/shell_history
func DefaultHistoryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", defaultHistoryFile)
}

// Load 从磁盘加载历史（不存在时返回 nil 而非错误）
func (h *History) Load() error {
	if h.path == "" {
		return nil
	}
	f, err := os.Open(h.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	h.mu.Lock()
	defer h.mu.Unlock()

	scanner := bufio.NewScanner(f)
	// 单行最长 1MB（防止 prompt 过长被截断）
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		h.entries = append(h.entries, line)
	}
	// 截断至 max
	if len(h.entries) > h.max {
		h.entries = h.entries[len(h.entries)-h.max:]
	}
	h.cursor = len(h.entries)
	return scanner.Err()
}

// Save 把历史完整覆盖写回磁盘（原子替换）
func (h *History) Save() error {
	if h.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(h.path), 0o755); err != nil {
		return err
	}

	h.mu.RLock()
	snapshot := make([]string, len(h.entries))
	copy(snapshot, h.entries)
	h.mu.RUnlock()

	tmp := h.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, line := range snapshot {
		_, _ = w.WriteString(line)
		_ = w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, h.path)
}

// Append 追加一条历史，自动去重（与上一条相同则忽略）
//
// 写入空字符串会被忽略。超过 max 时丢弃最旧条目。
func (h *History) Append(line string) {
	if line == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	if n := len(h.entries); n > 0 && h.entries[n-1] == line {
		// 连续重复 → 跳过
		h.cursor = len(h.entries)
		return
	}
	h.entries = append(h.entries, line)
	if len(h.entries) > h.max {
		h.entries = h.entries[len(h.entries)-h.max:]
	}
	h.cursor = len(h.entries)
}

// Reset 把导航游标移到末尾（每次新一轮输入开始时调用）
func (h *History) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cursor = len(h.entries)
}

// Prev 返回上一条历史（↑ 键）；到顶时返回最早一条且不再上移。
//
// ok=false 表示历史为空。
func (h *History) Prev() (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.entries) == 0 {
		return "", false
	}
	if h.cursor > 0 {
		h.cursor--
	}
	return h.entries[h.cursor], true
}

// Next 返回下一条历史（↓ 键）；越过末尾时返回空串（恢复用户当前编辑）。
func (h *History) Next() (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.entries) == 0 {
		return "", false
	}
	if h.cursor < len(h.entries) {
		h.cursor++
	}
	if h.cursor >= len(h.entries) {
		return "", true
	}
	return h.entries[h.cursor], true
}

// Snapshot 返回历史副本（用于调试/导出）
func (h *History) Snapshot() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, len(h.entries))
	copy(out, h.entries)
	return out
}

// Len 当前条目数
func (h *History) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.entries)
}

// SearchPrev 从索引 fromIdx-1 开始倒序查找首个包含 query 的条目（子串匹配）
//
// 调用约定：
//   - fromIdx 取上一次匹配的索引；首次搜索传 Len()
//   - query 为空时返回 ("", -1, false)
//   - 命中返回 (条目, 索引, true)；未命中返回 ("", -1, false)
//   - 大小写不敏感
func (h *History) SearchPrev(query string, fromIdx int) (string, int, bool) {
	if query == "" {
		return "", -1, false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if fromIdx > len(h.entries) {
		fromIdx = len(h.entries)
	}
	q := strings.ToLower(query)
	for i := fromIdx - 1; i >= 0; i-- {
		if strings.Contains(strings.ToLower(h.entries[i]), q) {
			return h.entries[i], i, true
		}
	}
	return "", -1, false
}
