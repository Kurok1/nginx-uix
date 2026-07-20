-- @author hanchao <hanchao@66yunlian.com>
-- @since 0.2.1
CREATE TABLE config_workspaces (
    id TEXT PRIMARY KEY CHECK(length(id) = 32),
    name TEXT NOT NULL,
    state TEXT NOT NULL CHECK(state IN ('preparing', 'ready', 'stale', 'needs_attention')),
    state_reason_code TEXT NOT NULL DEFAULT '',
    production_digest BLOB NOT NULL CHECK(length(production_digest) = 32),
    base_digest BLOB NOT NULL CHECK(length(base_digest) = 32),
    draft_digest BLOB NOT NULL CHECK(length(draft_digest) = 32),
    manifest_version INTEGER NOT NULL CHECK(manifest_version = 1),
    policy_version INTEGER NOT NULL CHECK(policy_version = 1),
    entry_count INTEGER NOT NULL CHECK(entry_count >= 0),
    managed_bytes INTEGER NOT NULL CHECK(managed_bytes >= 0),
    workspace_bytes INTEGER NOT NULL CHECK(workspace_bytes >= 0),
    revision INTEGER NOT NULL CHECK(revision >= 1),
    created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX config_workspaces_order_idx ON config_workspaces(updated_at DESC, id ASC);

CREATE TABLE config_group_collection (
    singleton INTEGER PRIMARY KEY CHECK(singleton = 1),
    revision INTEGER NOT NULL CHECK(revision >= 1),
    updated_at TEXT NOT NULL
);
INSERT INTO config_group_collection(singleton, revision, updated_at)
VALUES (1, 1, '1970-01-01T00:00:00Z');

CREATE TABLE config_groups (
    id TEXT PRIMARY KEY CHECK(length(id) = 32),
    name TEXT NOT NULL,
    normalized_name TEXT NOT NULL UNIQUE,
    sort_order INTEGER NOT NULL,
    created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX config_groups_order_idx ON config_groups(sort_order ASC, id ASC);

CREATE TABLE config_operations (
    id TEXT PRIMARY KEY,
    object_type TEXT NOT NULL,
    object_id TEXT NOT NULL,
    action TEXT NOT NULL,
    before_digest BLOB CHECK(before_digest IS NULL OR length(before_digest) = 32),
    after_digest BLOB CHECK(after_digest IS NULL OR length(after_digest) = 32),
    result TEXT NOT NULL,
    request_id TEXT NOT NULL,
    occurred_at TEXT NOT NULL
);
CREATE INDEX config_operations_object_idx
ON config_operations(object_type, object_id, occurred_at, id);

ALTER TABLE audit_events ADD COLUMN operation_id TEXT;
CREATE UNIQUE INDEX audit_events_operation_id_idx
ON audit_events(operation_id)
WHERE operation_id IS NOT NULL;

CREATE TABLE config_group_members (
    group_id TEXT NOT NULL REFERENCES config_groups(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL CHECK(ordinal >= 0),
    path TEXT NOT NULL,
    PRIMARY KEY(group_id, path),
    UNIQUE(group_id, ordinal)
);
