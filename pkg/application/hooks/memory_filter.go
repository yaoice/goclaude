// Package hooks — 记忆过滤应用服务
//
// 编排四层记忆过滤能力：
//  1. 规则引擎进行 include/exclude/boost/demote/tag
//  2. 上下文相关性评分（关键词重叠度 + 语义类别匹配）
//  3. 综合优先级排序
//  4. 容量管理（LRU 淘汰 + 低优先级摘要压缩）
package hooks

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/goclaude/pkg/domain/memory"
)

// ============================================================
// MemoryFilterConfig — 过滤器配置
// ============================================================

// MemoryFilterConfig 记忆过滤器完整配置
type MemoryFilterConfig struct {
	// 自定义规则（叠加内置规则之后）
	CustomRules []*memory.FilterRule `json:"custom_rules,omitempty"`

	// 容量限制
	MaxEntries     int `json:"max_entries"`     // 最大条目数（0=不限制，默认200）
	MaxTotalBytes  int `json:"max_total_bytes"` // 最大总字节数（0=不限制，默认256KB）

	// 旧内容处理策略
	EvictionPolicy string `json:"eviction_policy,omitempty"` // "lru" | "priority"（默认 "priority"）
	AutoSummarize  bool   `json:"auto_summarize"`             // 淘汰前是否摘要压缩

	// 相关性阈值
	MinRelevanceScore float64 `json:"min_relevance_score"` // 最小相关度（0-1，低于此分数标记低相关）
	MinPriorityScore  int     `json:"min_priority_score"`  // 最低优先级（低于此分数可被淘汰）

	// 上下文（当前会话关键词）
	ContextKeywords []string `json:"context_keywords,omitempty"`
}

// DefaultMemoryFilterConfig 返回推荐默认配置
func DefaultMemoryFilterConfig() MemoryFilterConfig {
	return MemoryFilterConfig{
		MaxEntries:     200,
		MaxTotalBytes:  256 * 1024, // 256KB
		EvictionPolicy: "priority",
		AutoSummarize:  true,
		MinRelevanceScore: 0.1,
		MinPriorityScore:  5,
	}
}

// ============================================================
// MemoryFilterService — 核心过滤服务
// ============================================================

// MemoryFilterService 记忆过滤服务
type MemoryFilterService struct {
	mu          sync.RWMutex
	cfg         MemoryFilterConfig
	ruleEngine  *memory.RuleEngine
	entries     map[string]*EntryWithMeta
	accessLog   []string // 访问顺序记录（用于 LRU）
}

// EntryWithMeta 带内部元数据的条目
type EntryWithMeta struct {
	*memory.MemoryEntry
	Filtered   bool   `json:"-"` // 是否被规则过滤
	MatchedRule string `json:"-"` // 匹配的规则名
}

// NewMemoryFilterService 创建过滤服务
func NewMemoryFilterService(cfg MemoryFilterConfig) *MemoryFilterService {
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = 200
	}
	if cfg.MaxTotalBytes <= 0 {
		cfg.MaxTotalBytes = 256 * 1024
	}
	// 内置规则 + 自定义规则
	rules := memory.BuiltInRules()
	rules = append(rules, cfg.CustomRules...)

	return &MemoryFilterService{
		cfg:        cfg,
		ruleEngine: memory.NewRuleEngine(rules),
		entries:    make(map[string]*EntryWithMeta),
	}
}

// ============================================================
// 1. 规则引擎入口
// ============================================================

// FilterEntry 对单条记忆应用规则引擎
//
// 返回：是否保留、修改后的条目、匹配的规则名
func (s *MemoryFilterService) FilterEntry(entry *memory.MemoryEntry) (keep bool, result *EntryWithMeta) {
	keep, modified, ruleName := s.ruleEngine.Apply(entry)
	return keep, &EntryWithMeta{
		MemoryEntry: modified,
		Filtered:    !keep,
		MatchedRule: ruleName,
	}
}

// FilterBatch 批量过滤，返回保留和被过滤的两组
func (s *MemoryFilterService) FilterBatch(entries []*memory.MemoryEntry) (kept, filtered []*EntryWithMeta) {
	for _, entry := range entries {
		keep, result := s.FilterEntry(entry)
		if keep {
			kept = append(kept, result)
		} else {
			filtered = append(filtered, result)
		}
	}
	return
}

// ============================================================
// 2. 上下文相关性评估
// ============================================================

// ScoreRelevance 计算条目与当前上下文的相关度（0.0-1.0）
//
// 算法：关键词重叠度加权
//   - 精确关键词在 title 命中 → 0.4
//   - 精确关键词在 content 命中 → 0.3
//   - 部分匹配（子串）→ 0.2
//   - 所有关键词都未命中 → 0.0
func (s *MemoryFilterService) ScoreRelevance(entry *memory.MemoryEntry) float64 {
	keywords := s.cfg.ContextKeywords
	if len(keywords) == 0 {
		return 0.5 // 无上下文关键词时默认中性分
	}

	titleLower := strings.ToLower(entry.Title)
	contentLower := strings.ToLower(entry.Content)

	score := 0.0
	hitCount := 0

	for _, kw := range keywords {
		kwLower := strings.ToLower(strings.TrimSpace(kw))
		if kwLower == "" {
			continue
		}
		hitCount++

		if strings.Contains(titleLower, kwLower) {
			score += 0.4
		} else if strings.Contains(contentLower, kwLower) {
			score += 0.3
		} else {
			// 部分匹配检查：keyword 的每个单词至少出现一个
			wordsInKW := strings.Fields(kwLower)
			partialHit := false
			for _, w := range wordsInKW {
				if len(w) >= 3 && (strings.Contains(titleLower, w) || strings.Contains(contentLower, w)) {
					partialHit = true
					break
				}
			}
			if partialHit {
				score += 0.15
			}
		}
	}

	if hitCount == 0 {
		return 0.5
	}
	return clamp(score/float64(hitCount), 0.0, 1.0)
}

// ScoreBatchRelevance 批量计算相关度并更新条目
func (s *MemoryFilterService) ScoreBatchRelevance(entries []*EntryWithMeta) {
	for _, e := range entries {
		e.Relevance = s.ScoreRelevance(e.MemoryEntry)
	}
}

// ============================================================
// 3. 优先级权重分配
// ============================================================

// AssignPriority 根据来源和类别计算默认优先级
//
// 来源权重：
//   - user_directive → 基础 80
//   - agent_note      → 基础 50
//   - auto_extract    → 基础 30
// 类别加成：
//   - project   → +10
//   - reference → +5
//   - feedback  → +5
func AssignPriority(entry *memory.MemoryEntry) int {
	base := 50 // 默认

	switch entry.Source {
	case "user_directive":
		base = 80
	case "agent_note":
		base = 50
	case "auto_extract":
		base = 30
	}

	switch entry.Category {
	case "project":
		base += 10
	case "reference":
		base += 5
	case "feedback":
		base += 5
	}

	// 标签有 "security" → 再加分
	for _, tag := range entry.Tags {
		if strings.EqualFold(tag, "security") {
			base += 10
			break
		}
	}

	return clampInt(base, 0, 100)
}

// SortByPriority 按综合评分降序排列
func SortByPriority(entries []*EntryWithMeta, now time.Time) {
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].TotalScore(now) > entries[j].TotalScore(now)
	})
}

// ============================================================
// 4. 容量管理策略
// ============================================================

// CapacityStatus 容量状态
type CapacityStatus struct {
	TotalEntries    int   `json:"total_entries"`
	MaxEntries      int   `json:"max_entries"`
	TotalBytes      int   `json:"total_bytes"`
	MaxBytes        int   `json:"max_bytes"`
	EntriesToEvict  int   `json:"entries_to_evict,omitempty"`
	BytesToFree     int   `json:"bytes_to_free,omitempty"`
	NeedsEviction   bool  `json:"needs_eviction"`
}

// CheckCapacity 检查容量状态
func (s *MemoryFilterService) CheckCapacity(entries []*EntryWithMeta) CapacityStatus {
	totalBytes := 0
	for _, e := range entries {
		totalBytes += e.ByteSize
		if e.ByteSize == 0 {
			totalBytes += len(e.Title) + len(e.Content)
		}
	}

	status := CapacityStatus{
		TotalEntries: len(entries),
		MaxEntries:   s.cfg.MaxEntries,
		TotalBytes:   totalBytes,
		MaxBytes:     s.cfg.MaxTotalBytes,
	}

	if len(entries) > s.cfg.MaxEntries || totalBytes > s.cfg.MaxTotalBytes {
		status.NeedsEviction = true
		status.EntriesToEvict = max(0, len(entries)-s.cfg.MaxEntries)
		status.BytesToFree = max(0, totalBytes-s.cfg.MaxTotalBytes)
	}

	return status
}

// Evict 执行淘汰策略
//
// 策略：
//   - "priority": 按综合评分升序淘汰（最低分先删），直到满足容量
//   - "lru":      按最近访问时间淘汰（最久未访问先删）
// 返回：保留的条目、被淘汰的条目
func (s *MemoryFilterService) Evict(entries []*EntryWithMeta, now time.Time) (kept, evicted []*EntryWithMeta) {
	if len(entries) <= s.cfg.MaxEntries {
		totalBytes := totalSize(entries)
		if totalBytes <= s.cfg.MaxTotalBytes {
			return entries, nil
		}
	}

	// 按策略排序
	switch s.cfg.EvictionPolicy {
	case "lru":
		s.sortByLRU(entries)
	default: // "priority"
		SortByPriority(entries, now) // 降序排列
	}

	// 从末尾开始淘汰（最低分 / 最久未访问）
	kept = make([]*EntryWithMeta, 0, s.cfg.MaxEntries)
	evicted = make([]*EntryWithMeta, 0)
	keptBytes := 0

	for i, entry := range entries {
		entrySize := entry.ByteSize
		if entrySize == 0 {
			entrySize = len(entry.Title) + len(entry.Content)
		}

		// 低于最低优先级直接淘汰
		if entry.Priority < s.cfg.MinPriorityScore {
			evicted = append(evicted, entry)
			continue
		}

		if len(kept) < s.cfg.MaxEntries && keptBytes+entrySize <= s.cfg.MaxTotalBytes {
			kept = append(kept, entry)
			keptBytes += entrySize
		} else {
			evicted = append(evicted, entry)
		}
		_ = i
	}

	return kept, evicted
}

func (s *MemoryFilterService) sortByLRU(entries []*EntryWithMeta) {
	s.mu.RLock()
	accessMap := make(map[string]int) // id → last access index
	for i, id := range s.accessLog {
		accessMap[id] = i
	}
	s.mu.RUnlock()

	sort.SliceStable(entries, func(i, j int) bool {
		idxI, okI := accessMap[entries[i].ID]
		idxJ, okJ := accessMap[entries[j].ID]
		if !okI && !okJ {
			return entries[i].UpdatedAt.Before(entries[j].UpdatedAt)
		}
		if !okI {
			return true // 从未访问 → 先淘汰
		}
		if !okJ {
			return false
		}
		return idxI < idxJ // 较早访问 → 先淘汰
	})
}

// RecordAccess 记录条目被访问（供 LRU 使用）
func (s *MemoryFilterService) RecordAccess(entryID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accessLog = append(s.accessLog, entryID)
	if len(s.accessLog) > 10000 {
		s.accessLog = s.accessLog[len(s.accessLog)-5000:]
	}
}

// ============================================================
// 5. 摘要压缩
// ============================================================

// SummarizeEvicted 对被淘汰条目生成摘要
//
// 将多条被淘汰条目合并为一条占位摘要，保留关键信息。
func SummarizeEvicted(evicted []*EntryWithMeta, now time.Time) *memory.MemoryEntry {
	if len(evicted) == 0 {
		return nil
	}

	titles := make([]string, 0, min(len(evicted), 10))
	tags := make(map[string]bool)
	totalCount := len(evicted)

	for i, e := range evicted {
		if i < 10 {
			titles = append(titles, e.Title)
		}
		for _, tag := range e.Tags {
			tags[tag] = true
		}
	}

	tagList := make([]string, 0, len(tags))
	for tag := range tags {
		tagList = append(tagList, tag)
	}

	more := ""
	if totalCount > 10 {
		more = "..."
	}

	return &memory.MemoryEntry{
		ID:        "summary_" + now.Format("20060102"),
		Title:     "Summary of evicted memories",
		Content:   strings.Join(titles, "; ") + more,
		Source:    "auto_summary",
		Category:  "reference",
		Tags:      tagList,
		Priority:  10,
		CreatedAt: now,
		UpdatedAt: now,
		ByteSize:  len(strings.Join(titles, "; ")),
	}
}

// ============================================================
// 6. 全流程编排
// ============================================================

// ProcessResult 全流程处理结果
type ProcessResult struct {
	Kept        []*EntryWithMeta   `json:"kept"`
	Filtered    []*EntryWithMeta   `json:"filtered"`
	Evicted     []*EntryWithMeta   `json:"evicted"`
	Summary     *memory.MemoryEntry `json:"summary,omitempty"`
	Capacity    CapacityStatus     `json:"capacity"`
}

// ProcessFull 执行全流程：
//   1. 规则引擎过滤 → 2. 相关性评分 → 3. 容量检查与淘汰 → 4. 摘要压缩
func (s *MemoryFilterService) ProcessFull(entries []*memory.MemoryEntry) ProcessResult {
	now := time.Now()

	// Step 1: 规则引擎
	kept, filtered := s.FilterBatch(entries)

	// 对保留的条目设置默认优先级（尚未设置时）
	for _, e := range kept {
		if e.Priority == 0 {
			e.Priority = AssignPriority(e.MemoryEntry)
		}
	}

	// Step 2: 相关性评分
	s.ScoreBatchRelevance(kept)

	// Step 3: 容量检查与淘汰
	capacity := s.CheckCapacity(kept)
	var evicted []*EntryWithMeta
	if capacity.NeedsEviction {
		kept, evicted = s.Evict(kept, time.Now())
	} else {
		evicted = nil
	}

	// Step 4: 摘要压缩（可选）
	var summary *memory.MemoryEntry
	if len(evicted) > 0 && s.cfg.AutoSummarize {
		summary = SummarizeEvicted(evicted, now)
		if summary != nil {
			summaryByteSize := summary.ByteSize
			_ = summaryByteSize
		}
	}

	return ProcessResult{
		Kept:     kept,
		Filtered: filtered,
		Evicted:  evicted,
		Summary:  summary,
		Capacity: capacity,
	}
}

// ============================================================
// 辅助工具
// ============================================================

func totalSize(entries []*EntryWithMeta) int {
	total := 0
	for _, e := range entries {
		s := e.ByteSize
		if s == 0 {
			s = len(e.Title) + len(e.Content)
		}
		total += s
	}
	return total
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a < b {
		return b
	}
	return a
}
