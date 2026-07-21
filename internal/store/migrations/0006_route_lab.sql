-- @author hanchao <hanchao@66yunlian.com>
-- @since 0.4.0

CREATE TABLE route_lab_runs (
    id TEXT PRIMARY KEY CHECK(length(id) = 32),
    workspace_id TEXT NOT NULL CHECK(length(workspace_id) = 32),
    workspace_revision INTEGER NOT NULL CHECK(workspace_revision >= 1),
    workspace_etag TEXT NOT NULL CHECK(length(workspace_etag) BETWEEN 1 AND 128),
    production_digest BLOB NOT NULL CHECK(length(production_digest) = 32),
    draft_digest BLOB NOT NULL CHECK(length(draft_digest) = 32),
    candidate_digest BLOB CHECK(candidate_digest IS NULL OR length(candidate_digest) = 32),
    state TEXT NOT NULL CHECK(state IN (
        'queued', 'running', 'succeeded', 'failed', 'cancelled', 'timed_out'
    )),
    stage TEXT NOT NULL CHECK(stage IN (
        'queued', 'preparing', 'validating', 'starting', 'requesting', 'collecting',
        'completed', 'failed', 'cancelled', 'timed_out'
    )),
    safe_request_json TEXT NOT NULL CHECK(length(safe_request_json) BETWEEN 2 AND 131072),
    static_analysis_json TEXT NOT NULL CHECK(length(static_analysis_json) BETWEEN 2 AND 2097152),
    terminal_result_json TEXT NOT NULL DEFAULT '' CHECK(length(terminal_result_json) <= 2097152),
    replayable INTEGER NOT NULL CHECK(replayable IN (0, 1)),
    side_effecting INTEGER NOT NULL CHECK(side_effecting IN (0, 1)),
    body_bytes INTEGER NOT NULL CHECK(body_bytes BETWEEN 0 AND 65536),
    body_digest BLOB NOT NULL CHECK(length(body_digest) = 32),
    sensitive_header_names_json TEXT NOT NULL CHECK(length(sensitive_header_names_json) BETWEEN 2 AND 8192),
    last_error_code TEXT NOT NULL DEFAULT '' CHECK(length(last_error_code) <= 128),
    created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    request_id TEXT NOT NULL CHECK(length(request_id) BETWEEN 1 AND 128),
    cancel_requested_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    CHECK(
        (state IN ('queued', 'running') AND finished_at IS NULL)
        OR
        (state IN ('succeeded', 'failed', 'cancelled', 'timed_out') AND finished_at IS NOT NULL)
    ),
    CHECK(
        (state = 'succeeded' AND stage = 'completed' AND terminal_result_json <> '' AND last_error_code = '')
        OR
        (state = 'failed' AND stage = 'failed' AND last_error_code <> '')
        OR
        (state = 'cancelled' AND stage = 'cancelled' AND last_error_code <> '')
        OR
        (state = 'timed_out' AND stage = 'timed_out' AND last_error_code <> '')
        OR
        (state IN ('queued', 'running') AND terminal_result_json = '' AND last_error_code = '')
    )
);

CREATE INDEX route_lab_runs_order_idx
ON route_lab_runs(created_at DESC, id DESC);
CREATE INDEX route_lab_runs_workspace_order_idx
ON route_lab_runs(workspace_id, created_at DESC, id DESC);
CREATE INDEX route_lab_runs_state_order_idx
ON route_lab_runs(state, created_at DESC, id DESC);
CREATE INDEX route_lab_runs_active_idx
ON route_lab_runs(created_at ASC, id ASC)
WHERE state IN ('queued', 'running');

CREATE TABLE route_lab_stages (
    run_id TEXT NOT NULL REFERENCES route_lab_runs(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL CHECK(sequence >= 1),
    stage TEXT NOT NULL CHECK(stage IN (
        'queued', 'preparing', 'validating', 'starting', 'requesting', 'collecting',
        'completed', 'failed', 'cancelled', 'timed_out'
    )),
    result TEXT NOT NULL CHECK(result IN ('pending', 'running', 'success', 'failed', 'warning')),
    code TEXT NOT NULL DEFAULT '' CHECK(length(code) <= 128),
    public_details_json TEXT NOT NULL CHECK(length(public_details_json) BETWEEN 2 AND 65536),
    occurred_at TEXT NOT NULL,
    PRIMARY KEY(run_id, sequence)
);
