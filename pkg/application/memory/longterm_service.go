// Package memory 提供长期记忆应用服务
//
// 对齐 claude-mem 的核心功能：
//   - 三层渐进式搜索（索引 → 时间线 → 详情）
//   - 容量管理与自动淘汰
//   - 记忆过期清理
//   - 隐私过滤（<private> 标签、敏感模式排除）
//   - 后台定期清理 goroutine
package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	domainmemory "github.com/anthropics/goclaude/pkg/domain/memory"
	"github.com/anthropics/goclaude/pkg/infrastructure/appconfig"
)

// LongTermMemoryService 长期记忆应用服务
type LongTermMemoryService struct {
	repo   domainmemory.LongTermRepository
	cfg    appconfig.LongTermMemoryConfig
	logger *slog.Logger

	mu       sync.Mutex
	started  bool
	stopCh   chan struct{}
}

// NewLongTermMemoryService 创建长期记忆服务
func NewLongTermMemoryService(
	repo domainmemory.LongTermRepository,
	cfg appconfig.LongTermMemoryConfig,
	logger *slog.Logger,
) *LongTermMemoryService {
	if logger == nil {
		logger = slog.Default()
	}
	return &LongTermMemoryService{
		repo:   repo,
		cfg:    cfg,
		logger: logger,
	}
}

// IsEnabled 返回长期记忆功能是否启用
func (s *LongTermMemoryService) IsEnabled() bool {
	return s.cfg.Enabled
}

// Start 启动后台清理 goroutine
func (s *LongTermMemoryService) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started || !s.cfg.Enabled {
		return
	}

	// 启动时清理一次
	if n, err := s.repo.ExpireMemories(ctx, time.Now()); err != nil {
		s.logger.Warn("initial memory expiration cleanup failed", "error", err)
	} else if n > 0 {
		s.logger.Info("cleaned expired memories on startup", "count", n)
	}

	// 按配置的间隔定期清理
	interval := time.Duration(s.cfg.Expiration.CleanupIntervalHours) * time.Hour
	if interval <= 0 {
		s.started = true
		return
	}

	s.stopCh = make(chan struct{})
	s.started = true

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if n, err := s.repo.ExpireMemories(context.Background(), time.Now()); err != nil {
					s.logger.Warn("periodic memory expiration cleanup failed", "error", err)
				} else if n > 0 {
					s.logger.Info("periodic memory cleanup", "removed", n)
				}
			case <-s.stopCh:
				return
			}
		}
	}()
}

// Stop 停止后台清理 goroutine
func (s *LongTermMemoryService) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stopCh != nil {
		close(s.stopCh)
		s.stopCh = nil
	}
	s.started = false
}

// Close 关闭服务（停止后台任务 + 关闭数据库连接）
func (s *LongTermMemoryService) Close() error {
	s.Stop()
	return s.repo.Close()
}

// ============================================================
// 记忆存储
// ============================================================

// SaveObservation 保存一条观察记录
func (s *LongTermMemoryService) SaveObservation(
	ctx context.Context,
	sessionID, title, content string,
	opts SaveOptions,
) (int64, error) {
	if !s.cfg.Enabled {
		return 0, nil
	}

	// 隐私过滤
	content = s.filterPrivate(content)
	if content == "" {
		return 0, nil
	}

	// 截断超长记忆
	if len(content) > s.cfg.Capture.MaxObservationSize {
		content = truncateToChars(content, s.cfg.Capture.MaxObservationSize)
	}

	mem := &domainmemory.LongTermMemory{
		SessionID:    sessionID,
		Type:         opts.ObsType,
		Title:        title,
		Content:      content,
		Category:     opts.Category,
		Source:       opts.Source,
		ToolName:     opts.ToolName,
		Priority:     opts.Priority,
		Tags:         strings.Join(opts.Tags, ","),
		ByteSize:     len(title) + len(content),
		CreatedAt:    time.Now(),
		LastAccessed: time.Now(),
	}

	// 设置过期时间
	// 1) Permanent 标记 → 永不过期
	// 2) 低优先级 + low_priority_ttl_days > 0 → 按低优先级 TTL
	// 3) default_ttl_days > 0 → 按默认 TTL
	// 4) 所有 TTL = 0 → 永不过期
	if opts.Permanent {
		// 明确标记永久，不设过期
	} else if mem.Priority <= s.cfg.Eviction.MinPriority && s.cfg.Expiration.LowPriorityTTLDays > 0 {
		mem.ExpiresAt = time.Now().Add(time.Duration(s.cfg.Expiration.LowPriorityTTLDays) * 24 * time.Hour)
	} else if s.cfg.Expiration.DefaultTTLDays > 0 {
		mem.ExpiresAt = time.Now().Add(time.Duration(s.cfg.Expiration.DefaultTTLDays) * 24 * time.Hour)
	}
	// else: 所有 TTL 均为 0 → 永不过期（ExpiresAt 保持零值）

	// 安全过滤：排除含敏感模式的内容
	if s.cfg.Privacy.AutoExcludePatterns && containsSecret(mem.Content+mem.Title) {
		s.logger.Debug("memory excluded by privacy filter", "title", title)
		return 0, nil
	}

	id, err := s.repo.SaveObservation(ctx, mem)
	if err != nil {
		return 0, fmt.Errorf("save observation: %w", err)
	}

	// 容量管理：超过上限时触发淘汰
	s.enforceCapacity(ctx)

	return id, nil
}

// SaveSessionSummary 保存会话摘要作为记忆
func (s *LongTermMemoryService) SaveSessionSummary(
	ctx context.Context,
	sessionID, projectRoot, summary string,
	stats SessionStats,
) error {
	if !s.cfg.Enabled {
		return nil
	}

	// 保存会话记录
	session := &domainmemory.LongTermSession{
		ID:               sessionID,
		ProjectRoot:      projectRoot,
		Summary:          summary,
		InputTokens:      stats.InputTokens,
		OutputTokens:     stats.OutputTokens,
		TurnCount:        stats.TurnCount,
		StartedAt:        stats.StartedAt,
		EndedAt:          time.Now(),
	}
	if err := s.repo.SaveSession(ctx, session); err != nil {
		return fmt.Errorf("save session: %w", err)
	}

	// 将会话摘要作为一条长期记忆保存
	_, err := s.SaveObservation(ctx, sessionID,
		"Session Summary: "+shortenID(sessionID),
		summary,
		SaveOptions{
			ObsType:  "summary",
			Category: "project",
			Source:   "auto_extract",
			Priority: 60,
		},
	)
	return err
}

// ============================================================
// 三层渐进式搜索
// ============================================================

// SearchIndex 第一层：FTS5 全文搜索 → 返回紧凑索引
func (s *LongTermMemoryService) SearchIndex(
	ctx context.Context,
	query string,
	opts domainmemory.SearchOptions,
) (*domainmemory.SearchResult, error) {
	if !s.cfg.Enabled {
		return &domainmemory.SearchResult{}, nil
	}

	if opts.Limit <= 0 {
		opts.Limit = s.cfg.Injection.SearchLimit
	}

	result, err := s.repo.SearchIndex(ctx, query, opts)
	if err != nil {
		return nil, fmt.Errorf("search index: %w", err)
	}

	// 记录访问
	for _, item := range result.Index {
		_ = s.repo.RecordAccess(ctx, item.ID)
	}

	s.logger.Debug("memory search index",
		"query", query,
		"results", len(result.Index),
		"total", result.Total,
	)
	return result, nil
}

// SearchTimeline 第二层：获取时间线上下文
func (s *LongTermMemoryService) SearchTimeline(
	ctx context.Context,
	ids []int64,
) ([]domainmemory.TimelineItem, error) {
	if !s.cfg.Enabled {
		return nil, nil
	}
	return s.repo.SearchTimeline(ctx, ids)
}

// GetObservations 第三层：获取完整详情
func (s *LongTermMemoryService) GetObservations(
	ctx context.Context,
	ids []int64,
) ([]*domainmemory.LongTermMemory, error) {
	if !s.cfg.Enabled {
		return nil, nil
	}
	return s.repo.GetObservations(ctx, ids)
}

// ============================================================
// 上下文注入
// ============================================================

// BuildInjectionContext 为 SessionStart 构建注入的上下文文本
//
// 从长期记忆中检索与当前项目/查询相关的记忆，格式化为可注入的上下文文本。
// 控制注入量不超过 max_inject_tokens（默认 2000 tokens，约 8000 字符）。
func (s *LongTermMemoryService) BuildInjectionContext(
	ctx context.Context,
	projectRoot string,
	currentQuery string,
) (string, error) {
	if !s.cfg.Enabled || !s.cfg.Injection.AutoInject {
		return "", nil
	}

	opts := domainmemory.SearchOptions{
		Limit:  s.cfg.Injection.SearchLimit,
		After:  time.Now().Add(-90 * 24 * time.Hour), // 最近 90 天
	}

	result, err := s.SearchIndex(ctx, currentQuery, opts)
	if err != nil {
		return "", err
	}

	if len(result.Index) == 0 {
		return "", nil
	}

	// 按最多 4 字符/token 估算 token 数，控制注入上限
	maxChars := s.cfg.Injection.MaxInjectTokens * 4

	var sb strings.Builder
	sb.WriteString("\n<long-term-memory>\n")
	sb.WriteString("The following context is retained from previous sessions:\n\n")

	count := 0
	for _, item := range result.Index {
		entry := fmt.Sprintf("- [%s] %s (priority: %d)\n",
			item.CreatedAt.Format("2006-01-02"),
			item.Title,
			item.Priority,
		)
		if sb.Len()+len(entry) > maxChars {
			break
		}
		sb.WriteString(entry)
		count++
	}

	sb.WriteString(fmt.Sprintf("\nTotal: %d relevant memories from past sessions.\n", count))
	sb.WriteString("</long-term-memory>\n")

	return sb.String(), nil
}

// ============================================================
// CRUD（用户主动管理）
// ============================================================

// ListRecent 列出最近的 N 条记忆
func (s *LongTermMemoryService) ListRecent(ctx context.Context, limit int) ([]*domainmemory.LongTermMemory, error) {
	if !s.cfg.Enabled {
		return nil, nil
	}
	opts := domainmemory.SearchOptions{Limit: limit}
	// 用空搜索获取最近的记忆
	result, err := s.repo.SearchIndex(ctx, `""`, opts)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(result.Index))
	for i, item := range result.Index {
		ids[i] = item.ID
	}
	return s.repo.GetObservations(ctx, ids)
}

// DeleteMemory 删除一条记忆
func (s *LongTermMemoryService) DeleteMemory(ctx context.Context, id int64) error {
	if !s.cfg.Enabled {
		return nil
	}
	return s.repo.DeleteObservation(ctx, id)
}

// UpdateMemory 更新记忆（用于用户手动修改优先级、分类等）
func (s *LongTermMemoryService) UpdateMemory(ctx context.Context, id int64, title, content, category string, priority int) error {
	if !s.cfg.Enabled {
		return nil
	}
	mem, err := s.repo.GetObservation(ctx, id)
	if err != nil {
		return err
	}
	if mem == nil {
		return fmt.Errorf("memory %d not found", id)
	}
	if title != "" {
		mem.Title = title
	}
	if content != "" {
		mem.Content = content
	}
	if category != "" {
		mem.Category = category
	}
	if priority >= 0 {
		mem.Priority = priority
	}
	return s.repo.UpdateObservation(ctx, mem)
}

// ============================================================
// 内部方法
// ============================================================

// enforceCapacity 检查并执行容量淘汰
func (s *LongTermMemoryService) enforceCapacity(ctx context.Context) {
	count, err := s.repo.Count(ctx)
	if err != nil {
		s.logger.Warn("count memories for capacity check", "error", err)
		return
	}

	// 条目数超限
	if count > int64(s.cfg.Capacity.MaxEntries) {
		n, err := s.repo.EvictByScore(ctx, s.cfg.Capacity.MaxEntries)
		if err != nil {
			s.logger.Warn("evict by score", "error", err)
		} else if n > 0 {
			s.logger.Info("evicted memories by score", "removed", n)
		}
	}

	// 字节数超限
	totalBytes, err := s.repo.TotalBytes(ctx)
	if err != nil {
		s.logger.Warn("check total bytes", "error", err)
		return
	}

	if totalBytes > int64(s.cfg.Capacity.MaxStorageBytes) {
		// 估算需要保留的条目数
		avgSize := totalBytes / max(count, 1)
		targetEntries := int64(s.cfg.Capacity.MaxStorageBytes) / max(avgSize, 1)
		if targetEntries < 100 {
			targetEntries = 100
		}
		n, err := s.repo.EvictByScore(ctx, int(targetEntries))
		if err != nil {
			s.logger.Warn("evict by bytes capacity", "error", err)
		} else if n > 0 {
			s.logger.Info("evicted memories by storage capacity", "removed", n, "total_bytes", totalBytes)
		}
	}

	// 数据库空间回收（每 100 条新记忆触发一次真空清理，概率约 1%）
	if count%100 == 0 {
		_ = s.repo.Vacuum(ctx)
	}
}

// ============================================================
// 隐私过滤
// ============================================================

// secretPatterns 敏感内容正则模式
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_-]?key|private[_-]?key|credential)\s*[:=]\s*\S{8,}`),
	regexp.MustCompile(`(?i)-----BEGIN\s+(RSA\s+)?PRIVATE\s+KEY-----`),
	regexp.MustCompile(`(?i)(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9_]{36,}`),
	regexp.MustCompile(`(?i)sk-[A-Za-z0-9]{32,}`),
	regexp.MustCompile(`(?i)eyJ[A-Za-z0-9\-_]{20,}\.eyJ[A-Za-z0-9\-_]{20,}`),
}

// privateTagRe <private>...</private> 标签内容匹配
var privateTagRe = regexp.MustCompile(`(?is)<private>.*?</private>`)

// filterPrivate 移除隐私标记内容
func (s *LongTermMemoryService) filterPrivate(content string) string {
	if s.cfg.Privacy.StripPrivateTags {
		content = privateTagRe.ReplaceAllString(content, "")
	}
	return strings.TrimSpace(content)
}

// containsSecret 检查内容是否包含敏感的密钥/凭证信息
func containsSecret(content string) bool {
	for _, p := range secretPatterns {
		if p.MatchString(content) {
			return true
		}
	}
	return false
}

// ============================================================
// 辅助函数与类型
// ============================================================

// SaveOptions 记忆保存选项
type SaveOptions struct {
	ObsType   string   // observation | summary | preference
	Category  string   // project | user | reference | feedback
	Source    string   // user_directive | auto_extract | agent_note | tool_use
	ToolName  string   // 触发记忆的工具名
	Priority  int      // 优先级 0-100
	Tags      []string // 标签列表
	Permanent bool     // 永久保存（即使配置了 TTL 也不过期）
}

// SessionStats 会话统计（用于摘要保存）
type SessionStats struct {
	InputTokens    int
	OutputTokens   int
	TurnCount      int
	StartedAt      time.Time
}

// generateID 生成 8 位 hex 随机 ID
func generateID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// shortenID 缩短会话 ID 用于显示
func shortenID(sessionID string) string {
	if len(sessionID) > 8 {
		return sessionID[:8]
	}
	return sessionID
}

// truncateToChars 截断字符串到指定字符数（尽量不切断词）
func truncateToChars(s string, maxChars int) string {
	if len(s) <= maxChars {
		return s
	}
	cutAt := strings.LastIndexAny(s[:maxChars], " .\n")
	if cutAt > maxChars/2 {
		return s[:cutAt] + "..."
	}
	return s[:maxChars-3] + "..."
}

// max 返回较大值
func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
