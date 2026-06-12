// Package memory — MEMORY.md 入口文件管理器
//
// MEMORY.md 作为持久化记忆入口文件，存储用户手动通过 /remember 命令添加的记忆内容。
// 文件格式采用 HTML 注释作为条目分隔符，既保证人类可读，也便于机器解析。
//
// 格式示例：
//
//	# Auto Memory
//
//	<!-- MEMORY_ENTRY id=abc123 category=project created=2025-06-08T19:30:00Z -->
//	## 项目使用 Go 1.22
//	项目使用 Go 1.22 作为主要开发语言。
//
//	<!-- MEMORY_ENTRY id=def456 category=reference created=2025-06-08T19:31:00Z -->
//	## API 前缀约定
//	所有 API 接口使用 /api/v1 作为前缀。
package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ---------- 常量 ----------

// EntryDelimiter 条目分隔标记
const EntryDelimiter = "<!-- MEMORY_ENTRY "

// entryHeaderRe 解析条目分隔行的正则：
//
//	<!-- MEMORY_ENTRY id=<hex> category=<word> created=<RFC3339> -->
var entryHeaderRe = regexp.MustCompile(
	`^<!--\s*MEMORY_ENTRY\s+id=([a-f0-9]{16})\s+category=(\w+)\s+created=(\S+)\s*-->\s*$`,
)

// titleRe 解析 ## 标题行
var titleRe = regexp.MustCompile(`^##\s+(.+)$`)

// ---------- 模型 ----------

// EntryItem 单条记忆条目
type EntryItem struct {
	ID        string    `json:"id"`         // 16 位 hex 唯一 ID
	Title     string    `json:"title"`      // 记忆标题
	Content   string    `json:"content"`    // 记忆内容（不含标题和元数据）
	Category  string    `json:"category"`   // project | user | reference | feedback
	CreatedAt time.Time `json:"created_at"` // 创建时间
	UpdatedAt time.Time `json:"updated_at"` // 更新时间
}

// ---------- EntrypointManager ----------

// EntrypointManager 管理 MEMORY.md 的读写与条目操作
type EntrypointManager struct {
	repo           Repository
	memoryDir      string
	entrypointPath string
}

// NewEntrypointManager 创建入口文件管理器
// memoryDir 为记忆目录路径（如 ~/.claude/projects/<key>/memory），
// MEMORY.md 将位于该目录下。
func NewEntrypointManager(repo Repository, memoryDir string) *EntrypointManager {
	return &EntrypointManager{
		repo:           repo,
		memoryDir:      memoryDir,
		entrypointPath: memoryDir + "/" + EntrypointName,
	}
}

// EntrypointPath 返回 MEMORY.md 的绝对路径
func (m *EntrypointManager) EntrypointPath() string {
	return m.entrypointPath
}

// ---------- 读取 ----------

// GetRawContent 读取 MEMORY.md 原始内容（不存在时返回空字符串）
func (m *EntrypointManager) GetRawContent(ctx context.Context) (string, error) {
	content, err := m.repo.ReadFile(ctx, m.entrypointPath)
	if err != nil {
		// 文件不存在是正常情况——返回空
		return "", nil
	}
	return content, nil
}

// ParseEntries 从 MEMORY.md 原始内容解析条目列表
func (m *EntrypointManager) ParseEntries(raw string) []EntryItem {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	// 按条目分隔符切分（包含头部行 "# Auto Memory" 会作为首段被跳过）
	blocks := splitByEntryDelimiter(raw)

	var entries []EntryItem
	for _, block := range blocks {
		entry, ok := parseEntryBlock(block)
		if !ok {
			continue
		}
		entries = append(entries, entry)
	}

	return entries
}

// ListEntries 列出所有已存储的记忆条目
func (m *EntrypointManager) ListEntries(ctx context.Context) ([]EntryItem, error) {
	raw, err := m.GetRawContent(ctx)
	if err != nil {
		return nil, err
	}
	return m.ParseEntries(raw), nil
}

// ---------- 写入 ----------

// AppendEntry 向 MEMORY.md 追加一条新记忆
// title 和 content 不能为空；category 为空时默认 "user"
func (m *EntrypointManager) AppendEntry(ctx context.Context, title, content, category string) (*EntryItem, error) {
	title = strings.TrimSpace(title)
	content = strings.TrimSpace(content)
	if title == "" || content == "" {
		return nil, fmt.Errorf("title 和 content 不能为空")
	}

	if category == "" {
		category = "user"
	}

	// 确保目录存在
	if err := EnsureMemoryDirExists(ctx, m.repo, m.memoryDir); err != nil {
		return nil, fmt.Errorf("创建记忆目录失败: %w", err)
	}

	// 生成唯一 ID
	id, err := generateEntryID()
	if err != nil {
		return nil, fmt.Errorf("生成条目 ID 失败: %w", err)
	}

	now := time.Now().UTC()
	entry := EntryItem{
		ID:        id,
		Title:     title,
		Content:   content,
		Category:  category,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// 读取现有内容
	existing, err := m.GetRawContent(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取现有记忆失败: %w", err)
	}

	// 构建新条目文本
	newBlock := formatEntryBlock(entry)

	var newContent string
	if strings.TrimSpace(existing) == "" {
		// 新建文件：写入头部 + 条目
		newContent = "# Auto Memory\n\n" + newBlock
	} else {
		// 追加到现有文件末尾
		newContent = strings.TrimRight(existing, "\n") + "\n\n" + newBlock + "\n"
	}

	if err := m.repo.WriteFile(ctx, m.entrypointPath, newContent); err != nil {
		return nil, fmt.Errorf("写入 MEMORY.md 失败: %w", err)
	}

	return &entry, nil
}

// ---------- 删除 ----------

// DeleteEntry 按 ID 删除一条记忆（支持前缀匹配：输入 8 位短 ID 或 16 位全 ID 均可）
func (m *EntrypointManager) DeleteEntry(ctx context.Context, id string) (bool, error) {
	raw, err := m.GetRawContent(ctx)
	if err != nil {
		return false, err
	}

	entries := m.ParseEntries(raw)
	found := false
	var newEntries []EntryItem
	for _, e := range entries {
		if strings.HasPrefix(e.ID, id) {
			found = true
			continue
		}
		newEntries = append(newEntries, e)
	}

	if !found {
		return false, nil
	}

	// 重建文件
	rebuilt := "# Auto Memory\n"
	for _, e := range newEntries {
		rebuilt += "\n" + formatEntryBlock(e) + "\n"
	}

	if err := m.repo.WriteFile(ctx, m.entrypointPath, rebuilt); err != nil {
		return false, fmt.Errorf("写入 MEMORY.md 失败: %w", err)
	}

	return true, nil
}

// ---------- 搜索 ----------

// SearchEntries 按关键词搜索记忆条目（匹配标题和内容）
func (m *EntrypointManager) SearchEntries(ctx context.Context, keyword string) ([]EntryItem, error) {
	all, err := m.ListEntries(ctx)
	if err != nil {
		return nil, err
	}

	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		return all, nil
	}

	var matched []EntryItem
	for _, e := range all {
		if strings.Contains(strings.ToLower(e.Title), keyword) ||
			strings.Contains(strings.ToLower(e.Content), keyword) {
			matched = append(matched, e)
		}
	}
	return matched, nil
}

// ---------- 上下文注入 ----------

// BuildContextSection 构建用于注入系统上下文的记忆内容文本
// 返回格式化的记忆内容，可直接拼接到系统提示词中。
func (m *EntrypointManager) BuildContextSection(ctx context.Context) (string, error) {
	raw, err := m.GetRawContent(ctx)
	if err != nil {
		return "", err
	}

	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "# Auto Memory" {
		return "", nil
	}

	// 去除首行 "# Auto Memory" 标题（避免与系统提示重复）
	lines := strings.Split(trimmed, "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "# Auto Memory" {
		trimmed = strings.TrimSpace(strings.Join(lines[1:], "\n"))
	}

	if trimmed == "" {
		return "", nil
	}

	return "\n<auto-memory>\n" + trimmed + "\n</auto-memory>\n", nil
}

// ---------- 内部辅助 ----------

// generateEntryID 生成 16 位 hex 随机 ID
func generateEntryID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// formatEntryBlock 将条目格式化为 MEMORY.md 中的一个块
func formatEntryBlock(e EntryItem) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "<!-- MEMORY_ENTRY id=%s category=%s created=%s -->\n",
		e.ID, e.Category, e.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(&sb, "## %s\n", e.Title)
	sb.WriteString(e.Content)
	return sb.String()
}

// parseEntryBlock 从单个条目文本块解析 EntryItem
func parseEntryBlock(block string) (EntryItem, bool) {
	lines := strings.Split(strings.TrimSpace(block), "\n")
	if len(lines) < 2 {
		return EntryItem{}, false
	}

	// 第一行应为分隔标记
	headerLine := strings.TrimSpace(lines[0])
	matches := entryHeaderRe.FindStringSubmatch(headerLine)
	if matches == nil {
		return EntryItem{}, false
	}

	id := matches[1]
	category := matches[2]
	createdStr := matches[3]

	createdAt, err := time.Parse(time.RFC3339, createdStr)
	if err != nil {
		return EntryItem{}, false
	}

	// 剩余行：第二行是 ## Title，后面是内容
	remainingLines := lines[1:]
	titleLine := strings.TrimSpace(remainingLines[0])
	titleMatch := titleRe.FindStringSubmatch(titleLine)
	if titleMatch == nil {
		return EntryItem{}, false
	}
	title := strings.TrimSpace(titleMatch[1])

	var contentLines []string
	if len(remainingLines) > 1 {
		for _, l := range remainingLines[1:] {
			contentLines = append(contentLines, l)
		}
	}
	content := strings.TrimSpace(strings.Join(contentLines, "\n"))

	if title == "" && content == "" {
		return EntryItem{}, false
	}

	return EntryItem{
		ID:        id,
		Title:     title,
		Content:   content,
		Category:  category,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}, true
}

// splitByEntryDelimiter 按 <!-- MEMORY_ENTRY 分隔文本
func splitByEntryDelimiter(raw string) []string {
	// 找到第一个分隔标记的位置
	idx := strings.Index(raw, EntryDelimiter)
	if idx < 0 {
		return nil
	}

	// 把前缀（# Auto Memory 标题等）去掉
	raw = raw[idx:]

	// 在每个分隔标记前插入特殊分隔符再切分
	// 使用零宽断言的替代方案：先替换再切分
	placeholder := "\x00MEMSPLIT\x00"
	replaced := strings.ReplaceAll(raw, EntryDelimiter, placeholder+EntryDelimiter)

	parts := strings.Split(replaced, placeholder)
	var blocks []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" && strings.HasPrefix(p, EntryDelimiter) {
			blocks = append(blocks, p)
		}
	}
	return blocks
}

// ---------- 排序与过滤工具 ----------

// SortEntriesByTime 按创建时间降序排列
func SortEntriesByTime(entries []EntryItem) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].CreatedAt.After(entries[j].CreatedAt)
	})
}

// FilterEntriesByCategory 按分类过滤
func FilterEntriesByCategory(entries []EntryItem, category string) []EntryItem {
	if category == "" {
		return entries
	}
	var result []EntryItem
	for _, e := range entries {
		if strings.EqualFold(e.Category, category) {
			result = append(result, e)
		}
	}
	return result
}
