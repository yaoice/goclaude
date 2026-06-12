// Package sqlite 提供基于 SQLite+FTS5 的 LongTermRepository 实现
//
// 对齐 claude-mem 的三层渐进式搜索：
//
//	Layer 1: SearchIndex  → FTS5 全文搜索，返回紧凑索引
//	Layer 2: SearchTimeline → 按 ID 批量获取时间线上下文
//	Layer 3: GetObservations → 获取完整记忆详情
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/yaoice/goclaude/pkg/domain/memory"
)

// Repository 基于 SQLite 的 LongTermRepository 实现
type Repository struct {
	db *sql.DB
}

// NewRepository 创建 SQLite 长期记忆仓库
func NewRepository(dbPath string) (*Repository, error) {
	db, err := OpenDB(dbPath)
	if err != nil {
		return nil, err
	}
	return &Repository{db: db}, nil
}

// ============================================================
// CRUD
// ============================================================

// SaveObservation 保存一条观察记录，返回自增 ID
func (r *Repository) SaveObservation(ctx context.Context, mem *memory.LongTermMemory) (int64, error) {
	var expiresAt interface{}
	if !mem.ExpiresAt.IsZero() {
		expiresAt = mem.ExpiresAt.UTC().Format(time.RFC3339)
	} else {
		expiresAt = nil
	}

	result, err := r.db.ExecContext(ctx, `
		INSERT INTO observations (
			session_id, type, title, content, category, source,
			tool_name, priority, tags, byte_size, created_at, expires_at, last_accessed
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
	`,
		mem.SessionID, mem.Type, mem.Title, mem.Content, mem.Category, mem.Source,
		mem.ToolName, mem.Priority, mem.Tags, mem.ByteSize,
		mem.CreatedAt.UTC().Format(time.RFC3339), expiresAt,
	)
	if err != nil {
		return 0, fmt.Errorf("save observation: %w", err)
	}
	return result.LastInsertId()
}

// GetObservation 按 ID 获取完整详情
func (r *Repository) GetObservation(ctx context.Context, id int64) (*memory.LongTermMemory, error) {
	memories, err := r.GetObservations(ctx, []int64{id})
	if err != nil {
		return nil, err
	}
	if len(memories) == 0 {
		return nil, nil
	}
	return memories[0], nil
}

// GetObservations 批量获取完整详情（三层搜索第三层）
func (r *Repository) GetObservations(ctx context.Context, ids []int64) ([]*memory.LongTermMemory, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT id, session_id, type, title, content, category, source,
		       tool_name, priority, tags, byte_size, access_count,
		       created_at, expires_at, last_accessed
		FROM observations
		WHERE id IN (%s)
		ORDER BY id DESC
	`, strings.Join(placeholders, ","))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get observations: %w", err)
	}
	defer rows.Close()

	var memories []*memory.LongTermMemory
	for rows.Next() {
		mem, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		memories = append(memories, mem)
	}
	return memories, rows.Err()
}

// UpdateObservation 更新记忆
func (r *Repository) UpdateObservation(ctx context.Context, mem *memory.LongTermMemory) error {
	var expiresAt interface{}
	if !mem.ExpiresAt.IsZero() {
		expiresAt = mem.ExpiresAt.UTC().Format(time.RFC3339)
	} else {
		expiresAt = nil
	}

	_, err := r.db.ExecContext(ctx, `
		UPDATE observations SET
			title = ?, content = ?, category = ?,
			priority = ?, tags = ?, expires_at = ?, byte_size = ?
		WHERE id = ?
	`,
		mem.Title, mem.Content, mem.Category,
		mem.Priority, mem.Tags, expiresAt, mem.ByteSize,
		mem.ID,
	)
	if err != nil {
		return fmt.Errorf("update observation %d: %w", mem.ID, err)
	}
	return nil
}

// DeleteObservation 按 ID 删除记忆
func (r *Repository) DeleteObservation(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM observations WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete observation %d: %w", id, err)
	}
	return nil
}

// RecordAccess 记录访问
func (r *Repository) RecordAccess(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE observations SET
			access_count = access_count + 1,
			last_accessed = datetime('now')
		WHERE id = ?
	`, id)
	return err
}

// ============================================================
// 三层渐进式搜索
// ============================================================

// SearchIndex 第一层：FTS5 全文搜索 → 返回紧凑索引
func (r *Repository) SearchIndex(ctx context.Context, query string, opts memory.SearchOptions) (*memory.SearchResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	// FTS5 查询，处理特殊字符（转义）
	safeQuery := sanitizeFTSQuery(query)
	useFTS := safeQuery != `""`

	var whereClauses []string
	var args []interface{}

	if useFTS {
		whereClauses = []string{"observations_fts MATCH ?"}
		args = []interface{}{safeQuery}
	} else {
		whereClauses = []string{"1=1"}
	}

	// 可选的类型过滤
	if opts.Type != "" {
		whereClauses = append(whereClauses, "o.type = ?")
		args = append(args, opts.Type)
	}
	if opts.Category != "" {
		whereClauses = append(whereClauses, "o.category = ?")
		args = append(args, opts.Category)
	}
	if opts.SessionID != "" {
		whereClauses = append(whereClauses, "o.session_id = ?")
		args = append(args, opts.SessionID)
	}
	if !opts.Before.IsZero() {
		whereClauses = append(whereClauses, "o.created_at <= ?")
		args = append(args, opts.Before.UTC().Format(time.RFC3339))
	}
	if !opts.After.IsZero() {
		whereClauses = append(whereClauses, "o.created_at >= ?")
		args = append(args, opts.After.UTC().Format(time.RFC3339))
	}

	whereStr := strings.Join(whereClauses, " AND ")

	var countQuery, dataQuery string
	if useFTS {
		countQuery = fmt.Sprintf(`
			SELECT COUNT(*) FROM observations o
			JOIN observations_fts fts ON o.id = fts.rowid
			WHERE %s
		`, whereStr)
		dataQuery = fmt.Sprintf(`
			SELECT o.id, o.title, o.type, o.category, o.priority, o.created_at,
			       SUBSTR(o.content, 1, 200) as snippet
			FROM observations o
			JOIN observations_fts fts ON o.id = fts.rowid
			WHERE %s
			ORDER BY o.priority DESC, o.created_at DESC
			LIMIT ? OFFSET ?
		`, whereStr)
	} else {
		countQuery = fmt.Sprintf(`
			SELECT COUNT(*) FROM observations o WHERE %s
		`, whereStr)
		dataQuery = fmt.Sprintf(`
			SELECT o.id, o.title, o.type, o.category, o.priority, o.created_at,
			       SUBSTR(o.content, 1, 200) as snippet
			FROM observations o
			WHERE %s
			ORDER BY o.priority DESC, o.created_at DESC
			LIMIT ? OFFSET ?
		`, whereStr)
	}

	var total int
	if err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		total = 0
	}

	dataArgs := append(args, limit, opts.Offset)
	rows, err := r.db.QueryContext(ctx, dataQuery, dataArgs...)
	if err != nil {
		return nil, fmt.Errorf("search index: %w", err)
	}
	defer rows.Close()

	var items []memory.SearchIndexItem
	for rows.Next() {
		var item memory.SearchIndexItem
		var createdAt string
		var snippet sql.NullString
		if err := rows.Scan(
			&item.ID, &item.Title, &item.Type, &item.Category,
			&item.Priority, &createdAt, &snippet,
		); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		item.Snippet = snippet.String
		items = append(items, item)
	}

	return &memory.SearchResult{
		Index: items,
		Total: total,
	}, rows.Err()
}

// SearchTimeline 第二层：返回指定批次的时间线上下文
func (r *Repository) SearchTimeline(ctx context.Context, ids []int64) ([]memory.TimelineItem, error) {
	memories, err := r.GetObservations(ctx, ids)
	if err != nil {
		return nil, err
	}

	items := make([]memory.TimelineItem, len(memories))
	for i, mem := range memories {
		items[i] = memory.TimelineItem{
			ID:        mem.ID,
			Title:     mem.Title,
			Type:      mem.Type,
			Content:   mem.Content,
			SessionID: mem.SessionID,
			CreatedAt: mem.CreatedAt,
		}
	}
	return items, nil
}

// ============================================================
// 会话管理
// ============================================================

// SaveSession 保存会话记录
func (r *Repository) SaveSession(ctx context.Context, session *memory.LongTermSession) error {
	startedAt := session.StartedAt.UTC().Format(time.RFC3339)
	var endedAt interface{}
	if !session.EndedAt.IsZero() {
		endedAt = session.EndedAt.UTC().Format(time.RFC3339)
	} else {
		endedAt = nil
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO sessions (
			id, working_dir, project_root, model, summary,
			input_tokens, output_tokens, turn_count, observation_count,
			started_at, ended_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		session.ID, session.WorkingDir, session.ProjectRoot, session.Model,
		session.Summary, session.InputTokens, session.OutputTokens,
		session.TurnCount, session.ObservationCount,
		startedAt, endedAt,
	)
	if err != nil {
		return fmt.Errorf("save session %s: %w", session.ID, err)
	}
	return nil
}

// UpdateSessionEnd 标记会话结束并保存摘要
func (r *Repository) UpdateSessionEnd(ctx context.Context, sessionID string, summary string, endedAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE sessions SET summary = ?, ended_at = ? WHERE id = ?
	`, summary, endedAt.UTC().Format(time.RFC3339), sessionID)
	if err != nil {
		return fmt.Errorf("update session end %s: %w", sessionID, err)
	}
	return nil
}

// GetRecentSessions 获取最近 N 个会话摘要
func (r *Repository) GetRecentSessions(ctx context.Context, limit int) ([]*memory.LongTermSession, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, working_dir, project_root, model, summary,
		       input_tokens, output_tokens, turn_count, observation_count,
		       started_at, COALESCE(ended_at, '')
		FROM sessions
		ORDER BY started_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("get recent sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*memory.LongTermSession
	for rows.Next() {
		var s memory.LongTermSession
		var startedAt, endedAtStr string
		if err := rows.Scan(
			&s.ID, &s.WorkingDir, &s.ProjectRoot, &s.Model, &s.Summary,
			&s.InputTokens, &s.OutputTokens, &s.TurnCount, &s.ObservationCount,
			&startedAt, &endedAtStr,
		); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		s.StartedAt, _ = time.Parse(time.RFC3339, startedAt)
		if endedAtStr != "" {
			s.EndedAt, _ = time.Parse(time.RFC3339, endedAtStr)
		}
		sessions = append(sessions, &s)
	}
	return sessions, rows.Err()
}

// ============================================================
// 维护操作
// ============================================================

// ExpireMemories 清理所有过期记忆
func (r *Repository) ExpireMemories(ctx context.Context, now time.Time) (int64, error) {
	result, err := r.db.ExecContext(ctx,
		"DELETE FROM observations WHERE expires_at IS NOT NULL AND expires_at <= ?",
		now.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, fmt.Errorf("expire memories: %w", err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

// EvictByScore 按综合评分淘汰，保留 topN
func (r *Repository) EvictByScore(ctx context.Context, topN int) (int64, error) {
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM observations WHERE id NOT IN (
			SELECT id FROM observations
			ORDER BY priority DESC, created_at DESC
			LIMIT ?
		)
	`, topN)
	if err != nil {
		return 0, fmt.Errorf("evict by score: %w", err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

// EvictByLRU 按最近访问时间淘汰，保留 topN
func (r *Repository) EvictByLRU(ctx context.Context, topN int) (int64, error) {
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM observations WHERE id NOT IN (
			SELECT id FROM observations
			ORDER BY last_accessed DESC
			LIMIT ?
		)
	`, topN)
	if err != nil {
		return 0, fmt.Errorf("evict by LRU: %w", err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

// Count 返回当前总记忆数
func (r *Repository) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations").Scan(&count)
	return count, err
}

// TotalBytes 返回当前记忆总字节数
func (r *Repository) TotalBytes(ctx context.Context) (int64, error) {
	var total int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(byte_size), 0) FROM observations",
	).Scan(&total)
	return total, err
}

// Vacuum 数据库空间回收
func (r *Repository) Vacuum(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, "VACUUM")
	return err
}

// Close 关闭数据库连接
func (r *Repository) Close() error {
	return r.db.Close()
}

// ============================================================
// 辅助函数
// ============================================================

func scanMemory(rows *sql.Rows) (*memory.LongTermMemory, error) {
	var m memory.LongTermMemory
	var createdAt, lastAccessed string
	var expiresAt sql.NullString

	if err := rows.Scan(
		&m.ID, &m.SessionID, &m.Type, &m.Title, &m.Content,
		&m.Category, &m.Source, &m.ToolName, &m.Priority,
		&m.Tags, &m.ByteSize, &m.AccessCount,
		&createdAt, &expiresAt, &lastAccessed,
	); err != nil {
		return nil, fmt.Errorf("scan memory: %w", err)
	}

	m.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	m.LastAccessed, _ = time.Parse(time.RFC3339, lastAccessed)
	if expiresAt.Valid && expiresAt.String != "" {
		m.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAt.String)
	}

	return &m, nil
}

// sanitizeFTSQuery FTS5 查询字符串清洗：转义特殊字符并构造合理查询
func sanitizeFTSQuery(query string) string {
	// 移除 FTS5 特殊字符 + 路径/标记分隔符，替换为空格以分开词汇
	q := strings.NewReplacer(
		"\"", " ",
		"*", " ",
		"(", " ",
		")", " ",
		"^", " ",
		"/", " ",
		"-", " ",
		".", " ",
	).Replace(query)

	// 分词 + OR 连接
	words := strings.Fields(q)
	if len(words) == 0 {
		return `""`
	}

	// 每词加前缀匹配后缀 *
	var terms []string
	for _, w := range words {
		if len(w) >= 2 {
			terms = append(terms, `"`+w+`"`+"*")
		}
	}
	if len(terms) == 0 {
		return `""`
	}

	return strings.Join(terms, " OR ")
}
