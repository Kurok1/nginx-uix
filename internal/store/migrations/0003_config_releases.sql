-- @author hanchao <hanchao@66yunlian.com>
-- @since 0.2.2
ALTER TABLE config_workspaces RENAME TO config_workspaces_v2;

CREATE TABLE config_workspaces (
    id TEXT PRIMARY KEY CHECK(length(id) = 32),
    name TEXT NOT NULL,
    state TEXT NOT NULL CHECK(state IN ('preparing', 'ready', 'stale', 'published', 'needs_attention')),
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
    last_release_id TEXT CHECK(last_release_id IS NULL OR length(last_release_id) = 32),
    created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

INSERT INTO config_workspaces(
    id, name, state, state_reason_code, production_digest, base_digest, draft_digest,
    manifest_version, policy_version, entry_count, managed_bytes, workspace_bytes,
    revision, last_release_id, created_by, created_at, updated_at
)
SELECT
    id, name, state, state_reason_code, production_digest, base_digest, draft_digest,
    manifest_version, policy_version, entry_count, managed_bytes, workspace_bytes,
    revision, NULL, created_by, created_at, updated_at
FROM config_workspaces_v2;

DROP TABLE config_workspaces_v2;
CREATE INDEX config_workspaces_order_idx ON config_workspaces(updated_at DESC, id ASC);

CREATE TABLE config_publish_checks (
    id TEXT PRIMARY KEY CHECK(length(id) = 32),
    workspace_id TEXT NOT NULL REFERENCES config_workspaces(id) ON DELETE RESTRICT,
    workspace_revision INTEGER NOT NULL CHECK(workspace_revision >= 1),
    production_digest BLOB NOT NULL CHECK(length(production_digest) = 32),
    base_digest BLOB NOT NULL CHECK(length(base_digest) = 32),
    draft_digest BLOB NOT NULL CHECK(length(draft_digest) = 32),
    candidate_digest BLOB CHECK(candidate_digest IS NULL OR length(candidate_digest) = 32),
    manifest_version INTEGER NOT NULL CHECK(manifest_version >= 1),
    policy_version INTEGER NOT NULL CHECK(policy_version >= 1),
    validator_version INTEGER NOT NULL CHECK(validator_version >= 1),
    validator_build_id TEXT NOT NULL,
    state TEXT NOT NULL CHECK(state IN ('running', 'valid', 'invalid', 'failed')),
    diagnostic_count INTEGER NOT NULL CHECK(diagnostic_count >= 0),
    public_details_json TEXT NOT NULL CHECK(length(public_details_json) <= 65536),
    created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    request_id TEXT NOT NULL,
    started_at TEXT NOT NULL,
    finished_at TEXT,
    expires_at TEXT NOT NULL
);
CREATE INDEX config_publish_checks_workspace_idx
ON config_publish_checks(workspace_id, started_at DESC, id ASC);

CREATE TABLE config_releases (
    id TEXT PRIMARY KEY CHECK(length(id) = 32),
    workspace_id TEXT NOT NULL REFERENCES config_workspaces(id) ON DELETE RESTRICT,
    check_id TEXT NOT NULL REFERENCES config_publish_checks(id) ON DELETE RESTRICT,
    backup_id TEXT CHECK(backup_id IS NULL OR length(backup_id) = 32),
    state TEXT NOT NULL CHECK(state IN (
        'queued', 'running', 'rolling_back', 'succeeded', 'failed', 'rolled_back',
        'needs_attention', 'cancelled'
    )),
    stage TEXT NOT NULL,
    production_digest BLOB NOT NULL CHECK(length(production_digest) = 32),
    draft_digest BLOB NOT NULL CHECK(length(draft_digest) = 32),
    candidate_digest BLOB NOT NULL CHECK(length(candidate_digest) = 32),
    last_error_code TEXT NOT NULL DEFAULT '',
    created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    request_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    finished_at TEXT
);
CREATE UNIQUE INDEX config_releases_active_idx
ON config_releases((1))
WHERE state IN ('queued', 'running', 'rolling_back');
CREATE INDEX config_releases_workspace_idx
ON config_releases(workspace_id, created_at DESC, id ASC);

CREATE TABLE config_release_stages (
    release_id TEXT NOT NULL REFERENCES config_releases(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL CHECK(sequence >= 1),
    stage TEXT NOT NULL,
    result TEXT NOT NULL CHECK(result IN ('pending', 'running', 'success', 'failed', 'warning')),
    code TEXT NOT NULL DEFAULT '',
    public_details_json TEXT NOT NULL CHECK(length(public_details_json) <= 65536),
    occurred_at TEXT NOT NULL,
    PRIMARY KEY(release_id, sequence)
);

CREATE TABLE config_backups (
    id TEXT PRIMARY KEY CHECK(length(id) = 32),
    release_id TEXT NOT NULL UNIQUE REFERENCES config_releases(id) ON DELETE RESTRICT,
    state TEXT NOT NULL CHECK(state IN ('creating', 'complete', 'invalid')),
    entry_count INTEGER NOT NULL CHECK(entry_count >= 0),
    total_bytes INTEGER NOT NULL CHECK(total_bytes >= 0),
    created_at TEXT NOT NULL,
    verified_at TEXT
);
