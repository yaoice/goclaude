// Package sqlite 提供基于 SQLite+FTS5 的长期记忆持久化实现
//
// 表结构对齐 claude-mem 的数据模型：
//   - observations: 核心记忆表（观察记录、摘要、偏好）
//   - sessions: 会话记录表
//   - observations_fts: FTS5 全文搜索索引
//
// 使用 modernc.org/sqlite（纯 Go 实现，无 CGO 依赖）。
package sqlite

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// SchemaVersion 当前数据库迁移版本
const SchemaVersion = 1

// schema 数据库建表语句
const schema = `
-- 会话表：每次对话会话的摘要与统计
CREATE TABLE IF NOT EXISTS sessions (
    id              TEXT PRIMARY KEY,
    working_dir     TEXT NOT NULL DEFAULT '',
    project_root    TEXT NOT NULL DEFAULT '',
    model           TEXT NOT NULL DEFAULT '',
    summary         TEXT NOT NULL DEFAULT '',
    input_tokens    INTEGER NOT NULL DEFAULT 0,
    output_tokens   INTEGER NOT NULL DEFAULT 0,
    turn_count      INTEGER NOT NULL DEFAULT 0,
    observation_count INTEGER NOT NULL DEFAULT 0,
    started_at      TEXT NOT NULL DEFAULT (datetime('now')),
    ended_at        TEXT
);

CREATE INDEX IF NOT EXISTS idx_sessions_started ON sessions(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_project ON sessions(project_root);

-- 核心记忆表：存储 observation / summary / preference
CREATE TABLE IF NOT EXISTS observations (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id      TEXT NOT NULL DEFAULT '',
    type            TEXT NOT NULL DEFAULT 'observation',  -- observation | summary | preference
    title           TEXT NOT NULL DEFAULT '',
    content         TEXT NOT NULL DEFAULT '',
    category        TEXT NOT NULL DEFAULT '',              -- project | user | reference | feedback
    source          TEXT NOT NULL DEFAULT '',              -- user_directive | auto_extract | agent_note | tool_use
    tool_name       TEXT NOT NULL DEFAULT '',
    priority        INTEGER NOT NULL DEFAULT 50,
    tags            TEXT NOT NULL DEFAULT '',              -- comma-separated
    byte_size       INTEGER NOT NULL DEFAULT 0,
    access_count    INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    expires_at      TEXT,                                  -- NULL = 永不过期
    last_accessed   TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_obs_type ON observations(type);
CREATE INDEX IF NOT EXISTS idx_obs_category ON observations(category);
CREATE INDEX IF NOT EXISTS idx_obs_session ON observations(session_id);
CREATE INDEX IF NOT EXISTS idx_obs_priority ON observations(priority DESC);
CREATE INDEX IF NOT EXISTS idx_obs_created ON observations(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_obs_expires ON observations(expires_at);
CREATE INDEX IF NOT EXISTS idx_obs_last_accessed ON observations(last_accessed DESC);

-- FTS5 全文搜索索引：内容存储在外部（content='observations'）
-- 使用触发器自动同步，保证 FTS 索引与主表数据一致
CREATE VIRTUAL TABLE IF NOT EXISTS observations_fts USING fts5(
    title,
    content,
    category,
    tags,
    content='observations',
    content_rowid='id',
    tokenize='porter unicode61'
);

-- 触发器：INSERT → FTS 同步
CREATE TRIGGER IF NOT EXISTS obs_fts_insert AFTER INSERT ON observations BEGIN
    INSERT INTO observations_fts(rowid, title, content, category, tags)
    VALUES (new.id, new.title, new.content, new.category, new.tags);
END;

-- 触发器：DELETE → FTS 同步
CREATE TRIGGER IF NOT EXISTS obs_fts_delete AFTER DELETE ON observations BEGIN
    INSERT INTO observations_fts(observations_fts, rowid, title, content, category, tags)
    VALUES ('delete', old.id, old.title, old.content, old.category, old.tags);
END;

-- 触发器：UPDATE → FTS 同步（先删旧，再插新）
CREATE TRIGGER IF NOT EXISTS obs_fts_update AFTER UPDATE ON observations BEGIN
    INSERT INTO observations_fts(observations_fts, rowid, title, content, category, tags)
    VALUES ('delete', old.id, old.title, old.content, old.category, old.tags);
    INSERT INTO observations_fts(rowid, title, content, category, tags)
    VALUES (new.id, new.title, new.content, new.category, new.tags);
END;

-- 迁移版本追踪表
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);
`

// OpenDB 打开 SQLite 数据库并执行自动迁移
func OpenDB(dbPath string) (*sql.DB, error) {
	// modernc.org/sqlite 使用 _pragma 格式设置 PRAGMA
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite open %s: %w", dbPath, err)
	}

	// 连接池配置：最多 5 连接，保持 FTS5 搜索低延迟
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite migrate: %w", err)
	}

	return db, nil
}

// migrate 执行数据库迁移
func migrate(db *sql.DB) error {
	// 检查当前迁移版本
	var currentVersion int
	row := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations")
	if err := row.Scan(&currentVersion); err != nil {
		// 表不存在 → 从 0 开始
		currentVersion = 0
	}

	if currentVersion >= SchemaVersion {
		return nil
	}

	// 执行建表语句（幂等：CREATE TABLE IF NOT EXISTS）
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}

	// 记录迁移版本
	if _, err := db.Exec(
		"INSERT OR REPLACE INTO schema_migrations (version) VALUES (?)",
		SchemaVersion,
	); err != nil {
		return fmt.Errorf("record migration version: %w", err)
	}

	return nil
}

// RebuildFTS 重建 FTS5 索引（维护用：FTS 索引可能因异常而不同步）
func RebuildFTS(db *sql.DB) error {
	_, err := db.Exec(`INSERT INTO observations_fts(observations_fts) VALUES('rebuild')`)
	return err
}
