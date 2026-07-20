-- @author hanchao <hanchao@66yunlian.com>
-- @since 0.2.3
CREATE TABLE config_production_lease (
    singleton INTEGER PRIMARY KEY CHECK(singleton = 1),
    owner_type TEXT CHECK(owner_type IS NULL OR owner_type IN ('release', 'restore', 'restart', 'retention')),
    owner_id TEXT CHECK(owner_id IS NULL OR length(owner_id) = 32),
    acquired_at TEXT,
    CHECK(
        (owner_type IS NULL AND owner_id IS NULL AND acquired_at IS NULL)
        OR
        (owner_type IS NOT NULL AND owner_id IS NOT NULL AND acquired_at IS NOT NULL)
    )
);
INSERT INTO config_production_lease(singleton, owner_type, owner_id, acquired_at)
VALUES (1, NULL, NULL, NULL);
UPDATE config_production_lease
SET owner_type = 'release',
    owner_id = (
        SELECT id FROM config_releases
        WHERE state IN ('queued', 'running', 'rolling_back')
        ORDER BY created_at ASC, id ASC
        LIMIT 1
    ),
    acquired_at = (
        SELECT created_at FROM config_releases
        WHERE state IN ('queued', 'running', 'rolling_back')
        ORDER BY created_at ASC, id ASC
        LIMIT 1
    )
WHERE EXISTS (
    SELECT 1 FROM config_releases
    WHERE state IN ('queued', 'running', 'rolling_back')
);

ALTER TABLE config_backups RENAME TO config_backups_v3;

CREATE TABLE config_backups (
    id TEXT PRIMARY KEY CHECK(length(id) = 32),
    origin_type TEXT NOT NULL CHECK(origin_type IN ('release', 'restore')),
    origin_id TEXT NOT NULL CHECK(length(origin_id) = 32),
    release_id TEXT REFERENCES config_releases(id) ON DELETE RESTRICT,
    production_digest BLOB NOT NULL CHECK(length(production_digest) = 32),
    tree_digest BLOB CHECK(tree_digest IS NULL OR length(tree_digest) = 32),
    state TEXT NOT NULL CHECK(state IN ('creating', 'complete', 'invalid', 'deleting', 'deleted')),
    entry_count INTEGER NOT NULL CHECK(entry_count >= 0),
    total_bytes INTEGER NOT NULL CHECK(total_bytes >= 0),
    manually_protected INTEGER NOT NULL DEFAULT 0 CHECK(manually_protected IN (0, 1)),
    protection_reason TEXT NOT NULL DEFAULT '' CHECK(length(protection_reason) <= 256),
    protected_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    protected_at TEXT,
    body_present INTEGER NOT NULL DEFAULT 1 CHECK(body_present IN (0, 1)),
    delete_run_id TEXT CHECK(delete_run_id IS NULL OR length(delete_run_id) = 32),
    delete_reason TEXT NOT NULL DEFAULT '' CHECK(length(delete_reason) <= 128),
    created_at TEXT NOT NULL,
    verified_at TEXT,
    deleted_at TEXT,
    CHECK(
        (manually_protected = 0 AND protection_reason = '' AND protected_by IS NULL AND protected_at IS NULL)
        OR
        (manually_protected = 1 AND protection_reason <> '' AND protected_by IS NOT NULL AND protected_at IS NOT NULL)
    ),
    CHECK(
        (state = 'deleted' AND body_present = 0 AND deleted_at IS NOT NULL)
        OR
        (state <> 'deleted' AND body_present = 1 AND deleted_at IS NULL)
    ),
    CHECK(
        (origin_type = 'release' AND release_id = origin_id)
        OR
        (origin_type = 'restore' AND release_id IS NULL)
    )
);

INSERT INTO config_backups(
    id, origin_type, origin_id, release_id, production_digest, tree_digest, state,
    entry_count, total_bytes, manually_protected, protection_reason, protected_by,
    protected_at, body_present, delete_run_id, delete_reason, created_at, verified_at, deleted_at
)
SELECT
    b.id, 'release', b.release_id, b.release_id, r.production_digest, NULL, b.state,
    b.entry_count, b.total_bytes, 0, '', NULL, NULL, 1, NULL, '', b.created_at, b.verified_at, NULL
FROM config_backups_v3 AS b
JOIN config_releases AS r ON r.id = b.release_id;

DROP TABLE config_backups_v3;
CREATE UNIQUE INDEX config_backups_release_idx
ON config_backups(release_id)
WHERE release_id IS NOT NULL;
CREATE UNIQUE INDEX config_backups_origin_idx ON config_backups(origin_type, origin_id);
CREATE INDEX config_backups_order_idx ON config_backups(created_at DESC, id DESC);
CREATE INDEX config_backups_retention_idx
ON config_backups(state, manually_protected, created_at ASC, id ASC);

CREATE TABLE config_restores (
    id TEXT PRIMARY KEY CHECK(length(id) = 32),
    target_backup_id TEXT NOT NULL REFERENCES config_backups(id) ON DELETE RESTRICT,
    safety_backup_id TEXT NOT NULL CHECK(length(safety_backup_id) = 32),
    attention_case_id TEXT CHECK(attention_case_id IS NULL OR length(attention_case_id) = 32),
    state TEXT NOT NULL CHECK(state IN (
        'queued', 'running', 'rolling_back', 'succeeded', 'failed', 'rolled_back',
        'needs_attention', 'cancelled'
    )),
    stage TEXT NOT NULL,
    source_digest BLOB NOT NULL CHECK(length(source_digest) = 32),
    target_digest BLOB NOT NULL CHECK(length(target_digest) = 32),
    last_error_code TEXT NOT NULL DEFAULT '',
    created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    reason TEXT NOT NULL CHECK(length(reason) BETWEEN 1 AND 256),
    request_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    finished_at TEXT
);
CREATE INDEX config_restores_order_idx ON config_restores(created_at DESC, id DESC);
CREATE UNIQUE INDEX config_restores_active_idx
ON config_restores((1))
WHERE state IN ('queued', 'running', 'rolling_back');

CREATE TABLE config_restore_stages (
    restore_id TEXT NOT NULL REFERENCES config_restores(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL CHECK(sequence >= 1),
    stage TEXT NOT NULL,
    result TEXT NOT NULL CHECK(result IN ('pending', 'running', 'success', 'failed', 'warning')),
    code TEXT NOT NULL DEFAULT '',
    public_details_json TEXT NOT NULL CHECK(length(public_details_json) <= 65536),
    occurred_at TEXT NOT NULL,
    PRIMARY KEY(restore_id, sequence)
);

CREATE TABLE config_restarts (
    id TEXT PRIMARY KEY CHECK(length(id) = 32),
    attention_case_id TEXT CHECK(attention_case_id IS NULL OR length(attention_case_id) = 32),
    state TEXT NOT NULL CHECK(state IN ('queued', 'running', 'succeeded', 'failed', 'needs_attention', 'cancelled')),
    stage TEXT NOT NULL,
    production_digest BLOB NOT NULL CHECK(length(production_digest) = 32),
    before_master_pid INTEGER CHECK(before_master_pid IS NULL OR before_master_pid > 0),
    after_master_pid INTEGER CHECK(after_master_pid IS NULL OR after_master_pid > 0),
    worker_count INTEGER NOT NULL DEFAULT 0 CHECK(worker_count >= 0),
    http_status INTEGER CHECK(http_status IS NULL OR http_status BETWEEN 100 AND 599),
    last_error_code TEXT NOT NULL DEFAULT '',
    created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    reason TEXT NOT NULL CHECK(length(reason) BETWEEN 1 AND 256),
    request_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    finished_at TEXT
);
CREATE INDEX config_restarts_order_idx ON config_restarts(created_at DESC, id DESC);
CREATE UNIQUE INDEX config_restarts_active_idx
ON config_restarts((1))
WHERE state IN ('queued', 'running');

CREATE TABLE config_restart_stages (
    restart_id TEXT NOT NULL REFERENCES config_restarts(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL CHECK(sequence >= 1),
    stage TEXT NOT NULL,
    result TEXT NOT NULL CHECK(result IN ('pending', 'running', 'success', 'failed', 'warning')),
    code TEXT NOT NULL DEFAULT '',
    public_details_json TEXT NOT NULL CHECK(length(public_details_json) <= 65536),
    occurred_at TEXT NOT NULL,
    PRIMARY KEY(restart_id, sequence)
);

CREATE TABLE config_retention_runs (
    id TEXT PRIMARY KEY CHECK(length(id) = 32),
    state TEXT NOT NULL CHECK(state IN ('planned', 'executing', 'succeeded', 'failed', 'needs_attention', 'expired')),
    minimum_complete INTEGER NOT NULL CHECK(minimum_complete >= 1),
    maximum_complete INTEGER NOT NULL CHECK(maximum_complete >= minimum_complete),
    maximum_total_bytes INTEGER NOT NULL CHECK(maximum_total_bytes > 0),
    minimum_age_seconds INTEGER NOT NULL CHECK(minimum_age_seconds >= 0),
    backup_count INTEGER NOT NULL CHECK(backup_count >= 0),
    total_bytes INTEGER NOT NULL CHECK(total_bytes >= 0),
    protected_count INTEGER NOT NULL CHECK(protected_count >= 0),
    delete_count INTEGER NOT NULL CHECK(delete_count >= 0),
    delete_bytes INTEGER NOT NULL CHECK(delete_bytes >= 0),
    deleted_count INTEGER NOT NULL DEFAULT 0 CHECK(deleted_count >= 0),
    deleted_bytes INTEGER NOT NULL DEFAULT 0 CHECK(deleted_bytes >= 0),
    last_error_code TEXT NOT NULL DEFAULT '',
    created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    request_id TEXT NOT NULL,
    execution_request_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT
);
CREATE INDEX config_retention_runs_order_idx ON config_retention_runs(created_at DESC, id DESC);
CREATE UNIQUE INDEX config_retention_runs_active_idx
ON config_retention_runs((1))
WHERE state = 'executing';

CREATE TABLE config_retention_items (
    run_id TEXT NOT NULL REFERENCES config_retention_runs(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL CHECK(ordinal >= 0),
    backup_id TEXT NOT NULL REFERENCES config_backups(id) ON DELETE RESTRICT,
    decision TEXT NOT NULL CHECK(decision IN ('keep', 'delete')),
    reason_code TEXT NOT NULL,
    state TEXT NOT NULL CHECK(state IN (
        'planned', 'kept', 'deleting', 'deleted', 'skipped_protected', 'failed', 'needs_attention'
    )),
    snapshot_created_at TEXT NOT NULL,
    snapshot_total_bytes INTEGER NOT NULL CHECK(snapshot_total_bytes >= 0),
    updated_at TEXT NOT NULL,
    PRIMARY KEY(run_id, ordinal),
    UNIQUE(run_id, backup_id)
);

CREATE TABLE config_attention_cases (
    id TEXT PRIMARY KEY CHECK(length(id) = 32),
    subject_type TEXT NOT NULL CHECK(subject_type IN ('workspace', 'release', 'restore', 'restart')),
    subject_id TEXT NOT NULL CHECK(length(subject_id) = 32),
    workspace_id TEXT REFERENCES config_workspaces(id) ON DELETE RESTRICT,
    backup_id TEXT REFERENCES config_backups(id) ON DELETE RESTRICT,
    state TEXT NOT NULL CHECK(state IN ('open', 'resolved')),
    reason_code TEXT NOT NULL,
    public_evidence_json TEXT NOT NULL DEFAULT '{}' CHECK(length(public_evidence_json) <= 65536),
    opened_at TEXT NOT NULL,
    resolved_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    resolved_at TEXT,
    resolution_type TEXT CHECK(resolution_type IS NULL OR resolution_type IN ('restore', 'restart', 'verification')),
    resolution_id TEXT CHECK(resolution_id IS NULL OR length(resolution_id) = 32),
    CHECK(
        (state = 'open' AND resolved_by IS NULL AND resolved_at IS NULL AND resolution_type IS NULL AND resolution_id IS NULL)
        OR
        (state = 'resolved' AND resolved_by IS NOT NULL AND resolved_at IS NOT NULL AND resolution_type IS NOT NULL AND resolution_id IS NOT NULL)
    ),
    UNIQUE(subject_type, subject_id)
);
CREATE INDEX config_attention_cases_order_idx ON config_attention_cases(state, opened_at DESC, id DESC);

CREATE TABLE config_verifications (
    id TEXT PRIMARY KEY CHECK(length(id) = 32),
    attention_case_id TEXT NOT NULL REFERENCES config_attention_cases(id) ON DELETE RESTRICT,
    state TEXT NOT NULL CHECK(state IN ('succeeded', 'failed')),
    production_digest BLOB NOT NULL CHECK(length(production_digest) = 32),
    master_pid INTEGER CHECK(master_pid IS NULL OR master_pid > 0),
    worker_count INTEGER NOT NULL DEFAULT 0 CHECK(worker_count >= 0),
    http_status INTEGER CHECK(http_status IS NULL OR http_status BETWEEN 100 AND 599),
    last_error_code TEXT NOT NULL DEFAULT '',
    created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    request_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    finished_at TEXT NOT NULL,
    CHECK(
        (state = 'succeeded' AND master_pid IS NOT NULL AND worker_count > 0
            AND http_status BETWEEN 200 AND 299 AND last_error_code = '')
        OR
        (state = 'failed' AND last_error_code <> '')
    )
);
CREATE INDEX config_verifications_order_idx ON config_verifications(created_at DESC, id DESC);

INSERT INTO config_attention_cases(
    id, subject_type, subject_id, workspace_id, backup_id, state, reason_code,
    public_evidence_json, opened_at, resolved_by, resolved_at, resolution_type, resolution_id
)
SELECT
    r.id, 'release', r.id, r.workspace_id, r.backup_id, 'open',
    CASE WHEN r.last_error_code = '' THEN 'release_needs_attention' ELSE r.last_error_code END,
    '{}', r.updated_at, NULL, NULL, NULL, NULL
FROM config_releases AS r
WHERE r.state = 'needs_attention';

INSERT INTO config_attention_cases(
    id, subject_type, subject_id, workspace_id, backup_id, state, reason_code,
    public_evidence_json, opened_at, resolved_by, resolved_at, resolution_type, resolution_id
)
SELECT
    w.id, 'workspace', w.id, w.id, NULL, 'open',
    CASE WHEN w.state_reason_code = '' THEN 'workspace_needs_attention' ELSE w.state_reason_code END,
    '{}', w.updated_at, NULL, NULL, NULL, NULL
FROM config_workspaces AS w
WHERE w.state = 'needs_attention'
  AND NOT EXISTS (
      SELECT 1 FROM config_attention_cases AS c WHERE c.workspace_id = w.id AND c.state = 'open'
  );
