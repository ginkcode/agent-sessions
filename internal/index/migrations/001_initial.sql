CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL                    -- Unix milliseconds UTC
);
CREATE TABLE IF NOT EXISTS sessions (
    ref TEXT PRIMARY KEY NOT NULL,
    agent TEXT NOT NULL,
    id TEXT NOT NULL,
    parent_id TEXT NOT NULL DEFAULT '',
    source_path TEXT NOT NULL,
    meta_json TEXT NOT NULL,                        -- complete SessionMeta JSON
    title TEXT NOT NULL DEFAULT '',
    cwd TEXT NOT NULL DEFAULT '',
    repo_root TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL DEFAULT 0,         -- Unix milliseconds UTC
    updated_at INTEGER NOT NULL DEFAULT 0,
    msg_total INTEGER NOT NULL DEFAULT 0,          -- Counts.Total()
    tokens INTEGER NOT NULL DEFAULT 0,             -- sum of TokenUsage fields
    cost REAL NOT NULL DEFAULT 0,
    archived INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0,1)),
    model TEXT NOT NULL DEFAULT '',
    fts_revision INTEGER NOT NULL DEFAULT 0,       -- bump on source/metadata change
    fts_indexed_revision INTEGER NOT NULL DEFAULT 0,
    UNIQUE(agent, id)
);
CREATE INDEX IF NOT EXISTS sessions_agent_updated
    ON sessions(agent, updated_at DESC);
CREATE INDEX IF NOT EXISTS sessions_cwd_updated
    ON sessions(cwd, updated_at DESC);
CREATE INDEX IF NOT EXISTS sessions_repo_updated
    ON sessions(repo_root, updated_at DESC);
CREATE INDEX IF NOT EXISTS sessions_created ON sessions(created_at DESC);
CREATE INDEX IF NOT EXISTS sessions_parent ON sessions(agent, parent_id);
CREATE INDEX IF NOT EXISTS sessions_filter
    ON sessions(archived, model, msg_total DESC);
CREATE INDEX IF NOT EXISTS sessions_cost ON sessions(cost DESC);
CREATE INDEX IF NOT EXISTS sessions_tokens ON sessions(tokens DESC);
CREATE TABLE IF NOT EXISTS provider_state (
    agent TEXT PRIMARY KEY NOT NULL,
    state_json TEXT NOT NULL                         -- full ScanState JSON
);
-- Durable, resumable index queue; stale revisions are not allowed to publish.
CREATE TABLE IF NOT EXISTS fts_jobs (
    ref TEXT PRIMARY KEY NOT NULL REFERENCES sessions(ref) ON DELETE CASCADE,
    revision INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT ''
);
-- One title row at message_index=-1; one text row per indexed message.
-- This ordinary table is the external content and maps rowid -> ref/index.
CREATE TABLE IF NOT EXISTS fts_docs (
    rowid INTEGER PRIMARY KEY,
    ref TEXT NOT NULL REFERENCES sessions(ref) ON DELETE CASCADE,
    message_index INTEGER NOT NULL,
    kind TEXT NOT NULL DEFAULT 'text',
    body TEXT NOT NULL,
    UNIQUE(ref, message_index)
);
CREATE INDEX IF NOT EXISTS fts_docs_ref ON fts_docs(ref);
CREATE VIRTUAL TABLE IF NOT EXISTS fts_messages USING fts5(
    body, content='fts_docs', content_rowid='rowid',
    tokenize='unicode61 remove_diacritics 2', prefix='2 3 4'
);
-- External-content FTS requires delete commands with the *old* text.
CREATE TRIGGER IF NOT EXISTS fts_docs_ai AFTER INSERT ON fts_docs BEGIN
    INSERT INTO fts_messages(rowid,body) VALUES (new.rowid,new.body);
END;
CREATE TRIGGER IF NOT EXISTS fts_docs_ad AFTER DELETE ON fts_docs BEGIN
    INSERT INTO fts_messages(fts_messages,rowid,body)
        VALUES('delete',old.rowid,old.body);
END;
CREATE TRIGGER IF NOT EXISTS fts_docs_au AFTER UPDATE ON fts_docs BEGIN
    INSERT INTO fts_messages(fts_messages,rowid,body)
        VALUES('delete',old.rowid,old.body);
    INSERT INTO fts_messages(rowid,body) VALUES(new.rowid,new.body);
END;
