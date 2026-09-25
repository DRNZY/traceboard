CREATE TABLE sources (
    name TEXT PRIMARY KEY CHECK (length(name) > 0),
    source_version TEXT NOT NULL DEFAULT '',
    capture_mode TEXT NOT NULL DEFAULT 'metadata' CHECK (capture_mode IN ('off', 'metadata', 'detailed')),
    connected INTEGER NOT NULL DEFAULT 0 CHECK (connected IN (0, 1)),
    last_heartbeat_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE projects (
    id TEXT PRIMARY KEY CHECK (length(id) > 0),
    name TEXT,
    path TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE runs (
    id TEXT PRIMARY KEY CHECK (length(id) > 0),
    source TEXT NOT NULL REFERENCES sources(name) ON DELETE RESTRICT,
    project_id TEXT REFERENCES projects(id) ON DELETE SET NULL,
    parent_run_id TEXT REFERENCES runs(id) ON DELETE CASCADE,
    title TEXT,
    status TEXT NOT NULL DEFAULT 'unknown' CHECK (status IN ('started', 'completed', 'failed', 'cancelled', 'incomplete', 'unknown')),
    started_at INTEGER,
    ended_at INTEGER,
    last_event_at INTEGER,
    event_count INTEGER NOT NULL DEFAULT 0 CHECK (event_count >= 0),
    error_count INTEGER NOT NULL DEFAULT 0 CHECK (error_count >= 0),
    next_sequence INTEGER NOT NULL DEFAULT 1 CHECK (next_sequence > 0),
    capture_modes TEXT NOT NULL DEFAULT '[]',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE steps (
    run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    id TEXT NOT NULL CHECK (length(id) > 0),
    parent_step_id TEXT,
    type TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('started', 'completed', 'failed', 'cancelled', 'incomplete', 'unknown')),
    started_at INTEGER,
    ended_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (run_id, id)
);

CREATE TABLE events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE CHECK (length(event_id) > 0),
    source_event_id TEXT NOT NULL CHECK (length(source_event_id) > 0),
    source TEXT NOT NULL REFERENCES sources(name) ON DELETE RESTRICT,
    source_version TEXT NOT NULL DEFAULT '',
    run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    step_id TEXT,
    parent_step_id TEXT,
    source_sequence INTEGER,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    occurred_at INTEGER NOT NULL,
    received_at INTEGER NOT NULL,
    type TEXT NOT NULL CHECK (length(type) > 0),
    status TEXT NOT NULL CHECK (status IN ('started', 'completed', 'failed', 'cancelled', 'incomplete', 'unknown')),
    capture TEXT NOT NULL,
    attributes TEXT NOT NULL,
    content TEXT,
    raw TEXT,
    search_text TEXT NOT NULL DEFAULT '',
    UNIQUE (run_id, id),
    FOREIGN KEY (run_id, step_id) REFERENCES steps(run_id, id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED
);

CREATE UNIQUE INDEX ux_events_source_source_event_id ON events(source, source_event_id);
CREATE UNIQUE INDEX ux_events_run_sequence ON events(run_id, sequence);
CREATE INDEX ix_runs_page ON runs(started_at DESC, id DESC);
CREATE INDEX ix_runs_source_page ON runs(source, started_at DESC, id DESC);
CREATE INDEX ix_runs_project_page ON runs(project_id, started_at DESC, id DESC);
CREATE INDEX ix_runs_status_page ON runs(status, started_at DESC, id DESC);
CREATE INDEX ix_steps_run ON steps(run_id);
CREATE INDEX ix_events_run_timeline ON events(run_id, occurred_at, source_sequence, sequence);
CREATE INDEX ix_events_run_received ON events(run_id, received_at, id);

CREATE VIRTUAL TABLE events_fts USING fts5(run_id UNINDEXED, source UNINDEXED, text);

CREATE TRIGGER events_fts_insert AFTER INSERT ON events BEGIN
    INSERT INTO events_fts(rowid, run_id, source, text) VALUES (new.id, new.run_id, new.source, new.search_text);
END;

CREATE TRIGGER events_fts_delete AFTER DELETE ON events BEGIN
    DELETE FROM events_fts WHERE rowid = old.id;
END;

CREATE TRIGGER events_fts_update AFTER UPDATE OF search_text, run_id, source ON events BEGIN
    DELETE FROM events_fts WHERE rowid = old.id;
    INSERT INTO events_fts(rowid, run_id, source, text) VALUES (new.id, new.run_id, new.source, new.search_text);
END;

CREATE TABLE alerts (
    id TEXT PRIMARY KEY CHECK (length(id) > 0),
    run_id TEXT REFERENCES runs(id) ON DELETE CASCADE,
    source TEXT REFERENCES sources(name) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (length(type) > 0),
    message TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    acknowledged_at INTEGER,
    resolved_at INTEGER
);

CREATE INDEX ix_alerts_run ON alerts(run_id);
CREATE UNIQUE INDEX ux_alerts_active ON alerts(type, COALESCE(run_id, ''), COALESCE(source, '')) WHERE resolved_at IS NULL;

CREATE TABLE quarantine (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source TEXT,
    source_event_id TEXT,
    payload TEXT NOT NULL,
    reason TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE INDEX ix_quarantine_created_at ON quarantine(created_at, id);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY CHECK (length(id) > 0),
    token_hash TEXT NOT NULL UNIQUE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    consumed_at INTEGER
);

CREATE INDEX ix_sessions_expires_at ON sessions(expires_at);
