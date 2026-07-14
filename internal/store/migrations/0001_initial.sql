-- @author hanchao <hanchao@66yunlian.com>
-- @since 0.1.0
CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    username TEXT NOT NULL,
    normalized_name TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    disabled INTEGER NOT NULL DEFAULT 0 CHECK(disabled IN (0, 1)),
    created_at TEXT NOT NULL
);

CREATE TABLE sessions (
    token_digest BLOB PRIMARY KEY CHECK(length(token_digest) = 32),
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_digest BLOB NOT NULL CHECK(length(csrf_digest) = 32),
    created_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    idle_expires_at TEXT NOT NULL,
    absolute_expires_at TEXT NOT NULL
);

CREATE INDEX sessions_user_id_idx ON sessions(user_id);
CREATE INDEX sessions_expiration_idx ON sessions(absolute_expires_at, idle_expires_at);

CREATE TABLE login_throttles (
    normalized_name TEXT NOT NULL,
    source_ip TEXT NOT NULL,
    failure_count INTEGER NOT NULL CHECK(failure_count >= 0),
    window_started_at TEXT NOT NULL,
    blocked_until TEXT,
    PRIMARY KEY(normalized_name, source_ip)
);

CREATE INDEX login_throttles_blocked_until_idx ON login_throttles(blocked_until);

CREATE TABLE audit_events (
    id INTEGER PRIMARY KEY,
    occurred_at TEXT NOT NULL,
    actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    object_type TEXT NOT NULL,
    object_id TEXT NOT NULL,
    result TEXT NOT NULL,
    request_id TEXT NOT NULL,
    details_json TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX audit_events_occurred_at_idx ON audit_events(occurred_at, id);
