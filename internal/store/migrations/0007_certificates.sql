-- @author hanchao <hanchao@66yunlian.com>
-- @since 0.5.0

CREATE TABLE certificate_accounts (
    id TEXT PRIMARY KEY CHECK(length(id) = 32),
    environment TEXT NOT NULL CHECK(environment IN ('staging', 'production')),
    directory_url TEXT NOT NULL CHECK(length(directory_url) BETWEEN 1 AND 2048),
    account_uri TEXT NOT NULL CHECK(length(account_uri) BETWEEN 1 AND 2048),
    email TEXT NOT NULL CHECK(length(email) BETWEEN 1 AND 254),
    status TEXT NOT NULL CHECK(status IN ('valid', 'deactivating', 'deactivated')),
    terms_url TEXT NOT NULL CHECK(length(terms_url) BETWEEN 1 AND 2048),
    terms_agreed_at TEXT NOT NULL,
    terms_agreed_by INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    request_id TEXT NOT NULL CHECK(length(request_id) BETWEEN 1 AND 128),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(environment, account_uri)
);
CREATE INDEX certificate_accounts_order_idx
ON certificate_accounts(created_at DESC, id DESC);

CREATE TABLE certificate_dns_credentials (
    id TEXT PRIMARY KEY CHECK(length(id) = 32),
    name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 128),
    provider TEXT NOT NULL CHECK(provider = 'cloudflare'),
    fingerprint TEXT NOT NULL CHECK(length(fingerprint) = 16),
    status TEXT NOT NULL CHECK(status IN ('valid', 'needs_attention', 'deleted')),
    verified_at TEXT NOT NULL,
    last_used_at TEXT,
    created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    request_id TEXT NOT NULL CHECK(length(request_id) BETWEEN 1 AND 128),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(provider, name)
);
CREATE INDEX certificate_dns_credentials_order_idx
ON certificate_dns_credentials(created_at DESC, id DESC);

CREATE TABLE certificate_order_plans (
    id TEXT PRIMARY KEY CHECK(length(id) = 32),
    state TEXT NOT NULL CHECK(state IN ('planned', 'executed', 'expired')),
    environment TEXT NOT NULL CHECK(environment IN ('staging', 'production')),
    challenge TEXT NOT NULL CHECK(challenge IN ('http_01', 'cloudflare_dns_01')),
    account_id TEXT NOT NULL REFERENCES certificate_accounts(id) ON DELETE RESTRICT,
    staging_account_id TEXT REFERENCES certificate_accounts(id) ON DELETE RESTRICT,
    dns_credential_id TEXT REFERENCES certificate_dns_credentials(id) ON DELETE RESTRICT,
    certificate_id TEXT CHECK(certificate_id IS NULL OR length(certificate_id) = 32),
    version_id TEXT CHECK(version_id IS NULL OR length(version_id) = 32),
    primary_identifier TEXT NOT NULL CHECK(length(primary_identifier) BETWEEN 1 AND 255),
    identifiers_json TEXT NOT NULL CHECK(length(identifiers_json) BETWEEN 2 AND 65536),
    server_refs_json TEXT NOT NULL CHECK(length(server_refs_json) BETWEEN 2 AND 2097152),
    production_digest BLOB NOT NULL CHECK(length(production_digest) = 32),
    binding_diff_json TEXT NOT NULL CHECK(length(binding_diff_json) BETWEEN 2 AND 2097152),
    staging_evidence INTEGER NOT NULL CHECK(staging_evidence IN (0, 1)),
    requires_risk_confirm INTEGER NOT NULL CHECK(requires_risk_confirm IN (0, 1)),
    expires_at TEXT NOT NULL,
    created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    request_id TEXT NOT NULL CHECK(length(request_id) BETWEEN 1 AND 128),
    created_at TEXT NOT NULL,
    executed_at TEXT,
    CHECK(
        (challenge = 'http_01' AND dns_credential_id IS NULL)
        OR (challenge = 'cloudflare_dns_01' AND dns_credential_id IS NOT NULL)
    ),
    CHECK((state = 'executed' AND executed_at IS NOT NULL) OR (state <> 'executed' AND executed_at IS NULL))
);
CREATE INDEX certificate_order_plans_order_idx
ON certificate_order_plans(created_at DESC, id DESC);

CREATE TABLE certificates (
    id TEXT PRIMARY KEY CHECK(length(id) = 32),
    primary_identifier TEXT NOT NULL CHECK(length(primary_identifier) BETWEEN 1 AND 255),
    identifiers_json TEXT NOT NULL CHECK(length(identifiers_json) BETWEEN 2 AND 65536),
    challenge TEXT NOT NULL CHECK(challenge IN ('http_01', 'cloudflare_dns_01')),
    account_id TEXT NOT NULL REFERENCES certificate_accounts(id) ON DELETE RESTRICT,
    dns_credential_id TEXT REFERENCES certificate_dns_credentials(id) ON DELETE RESTRICT,
    state TEXT NOT NULL CHECK(state IN (
        'pending', 'active', 'expiring', 'expired', 'unbound', 'needs_attention', 'deleted'
    )),
    active_version_id TEXT CHECK(active_version_id IS NULL OR length(active_version_id) = 32),
    auto_renew INTEGER NOT NULL CHECK(auto_renew IN (0, 1)),
    renew_before_seconds INTEGER NOT NULL CHECK(renew_before_seconds BETWEEN 1 AND 7776000),
    next_renewal_at TEXT,
    retry_count INTEGER NOT NULL DEFAULT 0 CHECK(retry_count >= 0),
    retry_at TEXT,
    not_before TEXT NOT NULL,
    not_after TEXT NOT NULL,
    last_error_code TEXT NOT NULL DEFAULT '' CHECK(length(last_error_code) <= 128),
    created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    request_id TEXT NOT NULL CHECK(length(request_id) BETWEEN 1 AND 128),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK(
        (state IN ('pending', 'deleted') AND active_version_id IS NULL)
        OR (state NOT IN ('pending', 'deleted') AND active_version_id IS NOT NULL)
    ),
    CHECK((challenge = 'cloudflare_dns_01' AND dns_credential_id IS NOT NULL) OR challenge = 'http_01')
);
CREATE INDEX certificates_order_idx ON certificates(created_at DESC, id DESC);
CREATE INDEX certificates_renewal_idx
ON certificates(COALESCE(retry_at, next_renewal_at) ASC, id ASC)
WHERE auto_renew = 1 AND state IN ('active', 'expiring', 'unbound');

CREATE TABLE certificate_versions (
    id TEXT PRIMARY KEY CHECK(length(id) = 32),
    certificate_id TEXT NOT NULL REFERENCES certificates(id) ON DELETE RESTRICT,
    state TEXT NOT NULL CHECK(state IN ('ready', 'active', 'superseded', 'needs_attention')),
    fullchain_digest TEXT NOT NULL CHECK(length(fullchain_digest) = 64),
    private_key_digest TEXT NOT NULL CHECK(length(private_key_digest) = 64),
    leaf_fingerprint TEXT NOT NULL CHECK(length(leaf_fingerprint) = 64),
    serial_number TEXT NOT NULL CHECK(length(serial_number) BETWEEN 1 AND 256),
    issuer TEXT NOT NULL CHECK(length(issuer) BETWEEN 1 AND 512),
    not_before TEXT NOT NULL,
    not_after TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE(certificate_id, id)
);
CREATE UNIQUE INDEX certificate_versions_active_idx
ON certificate_versions(certificate_id)
WHERE state = 'active';

CREATE TABLE certificate_bindings (
    id TEXT PRIMARY KEY CHECK(length(id) = 32),
    certificate_id TEXT NOT NULL REFERENCES certificates(id) ON DELETE RESTRICT,
    version_id TEXT NOT NULL REFERENCES certificate_versions(id) ON DELETE RESTRICT,
    config_path TEXT NOT NULL CHECK(length(config_path) BETWEEN 1 AND 4096),
    server_start_offset INTEGER NOT NULL CHECK(server_start_offset >= 0),
    server_names_json TEXT NOT NULL CHECK(length(server_names_json) BETWEEN 2 AND 65536),
    listeners_json TEXT NOT NULL CHECK(length(listeners_json) BETWEEN 2 AND 65536),
    server_fingerprint TEXT NOT NULL CHECK(length(server_fingerprint) = 64),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(server_fingerprint)
);
CREATE INDEX certificate_bindings_certificate_idx
ON certificate_bindings(certificate_id, id);

CREATE TABLE certificate_binding_plans (
    id TEXT PRIMARY KEY CHECK(length(id) = 32),
    state TEXT NOT NULL CHECK(state IN ('planned', 'executed', 'expired')),
    certificate_id TEXT NOT NULL REFERENCES certificates(id) ON DELETE RESTRICT,
    version_id TEXT NOT NULL REFERENCES certificate_versions(id) ON DELETE RESTRICT,
    server_refs_json TEXT NOT NULL CHECK(length(server_refs_json) BETWEEN 2 AND 2097152),
    production_digest BLOB NOT NULL CHECK(length(production_digest) = 32),
    binding_diff_json TEXT NOT NULL CHECK(length(binding_diff_json) BETWEEN 2 AND 2097152),
    expires_at TEXT NOT NULL,
    created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    request_id TEXT NOT NULL CHECK(length(request_id) BETWEEN 1 AND 128),
    created_at TEXT NOT NULL,
    executed_at TEXT,
    CHECK((state = 'executed' AND executed_at IS NOT NULL) OR (state <> 'executed' AND executed_at IS NULL))
);
CREATE INDEX certificate_binding_plans_order_idx
ON certificate_binding_plans(created_at DESC, id DESC);

CREATE TABLE certificate_tasks (
    id TEXT PRIMARY KEY CHECK(length(id) = 32),
    kind TEXT NOT NULL CHECK(kind IN ('issue', 'renew', 'bind', 'unbind')),
    state TEXT NOT NULL CHECK(state IN (
        'queued', 'running', 'cancelling', 'succeeded', 'failed', 'cancelled', 'needs_attention'
    )),
    stage TEXT NOT NULL CHECK(stage IN (
        'queued', 'preparing', 'ordering', 'provisioning', 'propagating', 'authorizing',
        'finalizing', 'validating', 'deploying', 'cleaning', 'completed', 'failed',
        'cancelled', 'needs_attention'
    )),
    plan_id TEXT CHECK(plan_id IS NULL OR length(plan_id) = 32),
    certificate_id TEXT CHECK(certificate_id IS NULL OR length(certificate_id) = 32),
    version_id TEXT CHECK(version_id IS NULL OR length(version_id) = 32),
    account_id TEXT REFERENCES certificate_accounts(id) ON DELETE RESTRICT,
    dns_credential_id TEXT REFERENCES certificate_dns_credentials(id) ON DELETE RESTRICT,
    challenge TEXT NOT NULL CHECK(challenge IN ('http_01', 'cloudflare_dns_01')),
    release_id TEXT NOT NULL DEFAULT '' CHECK(length(release_id) <= 128),
    last_error_code TEXT NOT NULL DEFAULT '' CHECK(length(last_error_code) <= 128),
    created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    request_id TEXT NOT NULL CHECK(length(request_id) BETWEEN 1 AND 128),
    cancel_requested_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    CHECK(
        (state IN ('queued', 'running', 'cancelling') AND finished_at IS NULL)
        OR (state IN ('succeeded', 'failed', 'cancelled', 'needs_attention') AND finished_at IS NOT NULL)
    ),
    CHECK((state = 'succeeded' AND last_error_code = '') OR state <> 'succeeded'),
    CHECK((state IN ('failed', 'cancelled', 'needs_attention') AND last_error_code <> '') OR state NOT IN ('failed', 'cancelled', 'needs_attention'))
);
CREATE INDEX certificate_tasks_order_idx
ON certificate_tasks(created_at DESC, id DESC);
CREATE UNIQUE INDEX certificate_tasks_active_certificate_idx
ON certificate_tasks(certificate_id)
WHERE certificate_id IS NOT NULL AND state IN ('queued', 'running', 'cancelling');

CREATE TABLE certificate_task_stages (
    task_id TEXT NOT NULL REFERENCES certificate_tasks(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL CHECK(sequence BETWEEN 1 AND 512),
    stage TEXT NOT NULL CHECK(stage IN (
        'queued', 'preparing', 'ordering', 'provisioning', 'propagating', 'authorizing',
        'finalizing', 'validating', 'deploying', 'cleaning', 'completed', 'failed',
        'cancelled', 'needs_attention'
    )),
    result TEXT NOT NULL CHECK(result IN ('pending', 'running', 'success', 'failed', 'warning')),
    code TEXT NOT NULL DEFAULT '' CHECK(length(code) <= 128),
    public_details_json TEXT NOT NULL CHECK(length(public_details_json) BETWEEN 2 AND 65536),
    occurred_at TEXT NOT NULL,
    PRIMARY KEY(task_id, sequence)
);

CREATE TABLE certificate_challenge_artifacts (
    id TEXT PRIMARY KEY CHECK(length(id) = 32),
    task_id TEXT NOT NULL REFERENCES certificate_tasks(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK(kind IN ('cloudflare_txt', 'http_include')),
    state TEXT NOT NULL CHECK(state IN ('created', 'cleaned', 'needs_attention')),
    dns_credential_id TEXT REFERENCES certificate_dns_credentials(id) ON DELETE RESTRICT,
    zone_id TEXT NOT NULL DEFAULT '' CHECK(length(zone_id) <= 128),
    record_id TEXT NOT NULL DEFAULT '' CHECK(length(record_id) <= 128),
    record_name TEXT NOT NULL DEFAULT '' CHECK(length(record_name) <= 255),
    config_path TEXT NOT NULL DEFAULT '' CHECK(length(config_path) <= 4096),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK(
        (kind = 'cloudflare_txt' AND dns_credential_id IS NOT NULL AND zone_id <> '' AND record_id <> '' AND record_name <> '' AND config_path = '')
        OR
        (kind = 'http_include' AND dns_credential_id IS NULL AND zone_id = '' AND record_id = '' AND record_name = '' AND config_path <> '')
    )
);
CREATE INDEX certificate_challenge_artifacts_task_idx
ON certificate_challenge_artifacts(task_id, state, id);
