// Package memory — 长期记忆领域模型
//
// 对齐 claude-mem 的核心概念：Observation, Session, Summary 作为持久化记忆单元。
// 提供 LongTermRepository 接口，与现有 file-based Repository 接口并行共存，互不侵入。
package memory

import (
	"context"
	"time"
)

// ============================================================
// 一、长期记忆类型
// ============================================================

// LongTermMemory 长期记忆实体 —— 对齐 claude-mem 的 observation + summary 概念
type LongTermMemory struct {
	ID          int64     `json:"id"`
	SessionID   string    `json:"session_id"`
	Type        string    `json:"type"` // "observation" | "summary" | "preference"
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	Category    string    `json:"category"` // "project" | "user" | "reference" | "feedback"
	Source      string    `json:"source"`   // "user_directive" | "auto_extract" | "agent_note" | "tool_use"
	ToolName    string    `json:"tool_name,omitempty"`
	Priority    int       `json:"priority"` // 0-100
	Tags        string    `json:"tags"`     // comma-separated
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"` // 零值表示永不过期
	ByteSize    int       `json:"byte_size"`
	AccessCount int       `json:"access_count"`
	LastAccessed time.Time `json:"last_accessed"`
}

// IsExpired 检查记忆是否过期
func (m *LongTermMemory) IsExpired(now time.Time) bool {
	if m.ExpiresAt.IsZero() {
		return false
	}
	return now.After(m.ExpiresAt)
}

// TotalScore 综合评分 = priority*0.5 + recency*0.3 + access*0.2
func (m *LongTermMemory) TotalScore(now time.Time) float64 {
	daysSinceCreate := now.Sub(m.CreatedAt).Hours() / 24
	recency := 1.0
	if daysSinceCreate > 0 {
		recency = 1.0 / (1.0 + daysSinceCreate/30.0)
	}
	accessScore := float64(m.AccessCount) / 100.0
	if accessScore > 1.0 {
		accessScore = 1.0
	}
	return float64(m.Priority)*0.5/100 + recency*0.3 + accessScore*0.2
}

// LongTermSession 记忆会话 —— 对齐 claude-mem 的 sessions 表
type LongTermSession struct {
	ID             string    `json:"id"`
	WorkingDir     string    `json:"working_dir"`
	ProjectRoot    string    `json:"project_root"`
	Model          string    `json:"model"`
	Summary        string    `json:"summary"` // AI 生成的会话摘要
	InputTokens    int       `json:"input_tokens"`
	OutputTokens   int       `json:"output_tokens"`
	TurnCount      int       `json:"turn_count"`
	ObservationCount int     `json:"observation_count"`
	StartedAt      time.Time `json:"started_at"`
	EndedAt        time.Time `json:"ended_at,omitempty"`
}

// ============================================================
// 二、渐进式搜索 —— 三层模型
// ============================================================

// SearchIndexItem 第一层：紧凑索引条目 (~50-100 tokens/条)
type SearchIndexItem struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Type      string    `json:"type"`
	Category  string    `json:"category"`
	Priority  int       `json:"priority"`
	CreatedAt time.Time `json:"created_at"`
	Snippet   string    `json:"snippet"` // 内容前 200 字摘要
}

// TimelineItem 第二层：时间线上下文（批量返回）
type TimelineItem struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Type      string    `json:"type"`
	Content   string    `json:"content"` // 完整内容
	SessionID string    `json:"session_id"`
	CreatedAt time.Time `json:"created_at"`
}

// SearchResult 聚合搜索结果
type SearchResult struct {
	Index    []SearchIndexItem `json:"index"`
	Timeline []TimelineItem    `json:"timeline,omitempty"`
	Total    int               `json:"total"`
}

// ============================================================
// 三、LongTermRepository 接口
// ============================================================

// LongTermRepository 长期记忆持久化仓库接口
//
// 与现有 Repository 接口（file-based MEMORY.md）完全独立，无任何侵入。
// 基于 SQLite+FTS5 实现。
type LongTermRepository interface {
	// ----- 记忆 CRUD -----
	// SaveObservation 保存一条观察记录
	SaveObservation(ctx context.Context, memory *LongTermMemory) (int64, error)
	// GetObservation 按 ID 获取完整详情
	GetObservation(ctx context.Context, id int64) (*LongTermMemory, error)
	// GetObservations 批量获取完整详情（三层搜索第三层）
	GetObservations(ctx context.Context, ids []int64) ([]*LongTermMemory, error)
	// UpdateObservation 更新记忆（如提升优先级、续期）
	UpdateObservation(ctx context.Context, memory *LongTermMemory) error
	// DeleteObservation 按 ID 删除记忆
	DeleteObservation(ctx context.Context, id int64) error
	// RecordAccess 记录访问（增加 access_count + 更新 last_accessed）
	RecordAccess(ctx context.Context, id int64) error

	// ----- 三层渐进式搜索 -----
	// SearchIndex 第一层：FTS5 全文搜索 → 返回紧凑索引
	SearchIndex(ctx context.Context, query string, opts SearchOptions) (*SearchResult, error)
	// SearchTimeline 第二层：返回指定批次的时间线上下文
	SearchTimeline(ctx context.Context, ids []int64) ([]TimelineItem, error)

	// ----- 会话管理 -----
	// SaveSession 保存会话记录
	SaveSession(ctx context.Context, session *LongTermSession) error
	// UpdateSessionEnd 标记会话结束并保存摘要
	UpdateSessionEnd(ctx context.Context, sessionID string, summary string, endedAt time.Time) error
	// GetRecentSessions 获取最近 N 个会话摘要
	GetRecentSessions(ctx context.Context, limit int) ([]*LongTermSession, error)

	// ----- 维护操作 -----
	// ExpireMemories 清理所有过期记忆，返回清理数量
	ExpireMemories(ctx context.Context, now time.Time) (int64, error)
	// EvictByScore 按综合评分淘汰，保留 topN 条高优先级记忆
	EvictByScore(ctx context.Context, topN int) (int64, error)
	// EvictByLRU 按最近访问时间淘汰，保留 topN 条最近访问
	EvictByLRU(ctx context.Context, topN int) (int64, error)
	// Count 返回当前总记忆数
	Count(ctx context.Context) (int64, error)
	// TotalBytes 返回当前记忆总字节数
	TotalBytes(ctx context.Context) (int64, error)
	// Vacuum 数据库空间回收
	Vacuum(ctx context.Context) error

	// ----- 生命周期 -----
	// Close 关闭数据库连接
	Close() error
}

// SearchOptions 搜索参数
type SearchOptions struct {
	// Type 按类型过滤 ("observation" | "summary" | "preference")，空表示全部
	Type string
	// Category 按分类过滤，空表示全部
	Category string
	// SessionID 按会话过滤，空表示全部
	SessionID string
	// Before 过滤指定日期之前创建的记录
	Before time.Time
	// After 过滤指定日期之后创建的记录
	After time.Time
	// Limit 返回上限（默认 20）
	Limit int
	// Offset 分页偏移
	Offset int
}
