/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/kuroky/nginx-uix/internal/certificate"
	"github.com/kuroky/nginx-uix/internal/config"
)

const certificateListLimit = 100

const certificateSelect = `SELECT id, primary_identifier, identifiers_json, challenge,
	account_id, dns_credential_id, state, active_version_id, auto_renew, renew_before_seconds,
	next_renewal_at, retry_count, retry_at, not_before, not_after, last_error_code,
	created_by, request_id, created_at, updated_at FROM certificates`

const certificateTaskSelect = `SELECT id, kind, state, stage, plan_id, certificate_id, version_id,
	account_id, dns_credential_id, challenge, release_id, last_error_code, created_by, request_id,
	cancel_requested_at, created_at, updated_at, started_at, finished_at FROM certificate_tasks`

const certificateBindingPlanSelect = `SELECT id, state, certificate_id, version_id, server_refs_json,
	production_digest, binding_diff_json, expires_at, created_by, request_id, created_at, executed_at
	FROM certificate_binding_plans`

// CreateCertificateAccount atomically persists safe ACME metadata and an audit event.
func (database *DB) CreateCertificateAccount(ctx context.Context, account certificate.Account) error {
	if ctx == nil || certificate.ValidateAccount(account) != nil {
		return fmt.Errorf("create certificate account: invalid input")
	}
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		_, err := connection.ExecContext(ctx, `INSERT INTO certificate_accounts(
			id, environment, directory_url, account_uri, email, status, terms_url,
			terms_agreed_at, terms_agreed_by, created_by, request_id, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			account.ID, account.Environment, account.DirectoryURL, account.URI, account.Email,
			account.Status, account.TermsURL, formatTime(account.TermsAgreedAt), account.TermsAgreedBy,
			account.CreatedBy, account.RequestID, formatTime(account.CreatedAt), formatTime(account.UpdatedAt),
		)
		if err != nil {
			return mapConfigConstraint("insert certificate account", err)
		}
		return insertCertificateAudit(ctx, connection, "acme_account", string(account.ID),
			"certificate.account.create", account.CreatedBy, account.RequestID, account.CreatedAt,
			map[string]any{"environment": account.Environment})
	})
	if err != nil {
		return fmt.Errorf("create certificate account: %w", err)
	}
	return nil
}

// CertificateAccount returns one exact safe account resource.
func (database *DB) CertificateAccount(ctx context.Context, id certificate.AccountID) (certificate.Account, error) {
	if ctx == nil || parseCertificateID(string(id)) != nil {
		return certificate.Account{}, fmt.Errorf("read certificate account: invalid id")
	}
	account, err := scanCertificateAccount(database.sql.QueryRowContext(ctx, `SELECT id, environment,
		directory_url, account_uri, email, status, terms_url, terms_agreed_at, terms_agreed_by,
		created_by, request_id, created_at, updated_at FROM certificate_accounts WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return certificate.Account{}, fmt.Errorf("read certificate account: %w", fs.ErrNotExist)
	}
	if err != nil {
		return certificate.Account{}, fmt.Errorf("read certificate account: %w", err)
	}
	return account, nil
}

// CertificateAccounts returns the bounded newest-first account inventory.
func (database *DB) CertificateAccounts(ctx context.Context) ([]certificate.Account, error) {
	rows, err := database.sql.QueryContext(ctx, `SELECT id, environment, directory_url, account_uri,
		email, status, terms_url, terms_agreed_at, terms_agreed_by, created_by, request_id,
		created_at, updated_at FROM certificate_accounts ORDER BY created_at DESC, id DESC LIMIT ?`, certificateListLimit)
	if err != nil {
		return nil, fmt.Errorf("list certificate accounts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	accounts := make([]certificate.Account, 0)
	for rows.Next() {
		account, scanErr := scanCertificateAccount(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("list certificate accounts: %w", scanErr)
		}
		accounts = append(accounts, account)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list certificate accounts: %w", err)
	}
	return accounts, nil
}

// BeginCertificateAccountDeactivation blocks new work before any remote account mutation.
func (database *DB) BeginCertificateAccountDeactivation(
	ctx context.Context,
	id certificate.AccountID,
	actorUserID int64,
	requestID string,
	at time.Time,
) (certificate.Account, error) {
	if ctx == nil || parseCertificateID(string(id)) != nil || actorUserID <= 0 ||
		requestID == "" || len(requestID) > 128 || at.IsZero() {
		return certificate.Account{}, fmt.Errorf("begin certificate account deactivation: invalid input")
	}
	var updated certificate.Account
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		account, err := scanCertificateAccount(connection.QueryRowContext(ctx, `SELECT id, environment,
			directory_url, account_uri, email, status, terms_url, terms_agreed_at, terms_agreed_by,
			created_by, request_id, created_at, updated_at FROM certificate_accounts WHERE id = ?`, id))
		if err != nil {
			return err
		}
		if account.Status == certificate.AccountStatusDeactivating ||
			account.Status == certificate.AccountStatusDeactivated {
			updated = account
			return nil
		}
		if account.Status != certificate.AccountStatusValid || at.Before(account.UpdatedAt) {
			return config.ErrConflict
		}
		var active int
		if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM certificate_tasks
			WHERE account_id = ? AND state IN ('queued', 'running', 'cancelling')`, id).Scan(&active); err != nil {
			return err
		}
		if active != 0 {
			return certificate.ErrTaskActive
		}
		result, err := connection.ExecContext(ctx, `UPDATE certificate_accounts
			SET status = 'deactivating', updated_at = ? WHERE id = ? AND status = 'valid'`, formatTime(at), id)
		if err != nil {
			return mapConfigConstraint("begin certificate account deactivation", err)
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		account.Status = certificate.AccountStatusDeactivating
		account.UpdatedAt = at.UTC()
		updated = account
		return insertCertificateAudit(ctx, connection, "acme_account", string(id),
			"certificate.account.deactivate.begin", actorUserID, requestID, at,
			map[string]any{"environment": account.Environment})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return certificate.Account{}, fmt.Errorf("begin certificate account deactivation: %w", fs.ErrNotExist)
	}
	if err != nil {
		return certificate.Account{}, fmt.Errorf("begin certificate account deactivation: %w", err)
	}
	return updated, nil
}

// CompleteCertificateAccountDeactivation records the local terminal state after remote success.
func (database *DB) CompleteCertificateAccountDeactivation(
	ctx context.Context,
	id certificate.AccountID,
	actorUserID int64,
	requestID string,
	at time.Time,
) (certificate.Account, error) {
	if ctx == nil || parseCertificateID(string(id)) != nil || actorUserID <= 0 ||
		requestID == "" || len(requestID) > 128 || at.IsZero() {
		return certificate.Account{}, fmt.Errorf("complete certificate account deactivation: invalid input")
	}
	var updated certificate.Account
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		account, err := scanCertificateAccount(connection.QueryRowContext(ctx, `SELECT id, environment,
			directory_url, account_uri, email, status, terms_url, terms_agreed_at, terms_agreed_by,
			created_by, request_id, created_at, updated_at FROM certificate_accounts WHERE id = ?`, id))
		if err != nil {
			return err
		}
		if account.Status == certificate.AccountStatusDeactivated {
			updated = account
			return nil
		}
		if account.Status != certificate.AccountStatusDeactivating || at.Before(account.UpdatedAt) {
			return config.ErrConflict
		}
		result, err := connection.ExecContext(ctx, `UPDATE certificate_accounts
			SET status = 'deactivated', updated_at = ? WHERE id = ? AND status = 'deactivating'`, formatTime(at), id)
		if err != nil {
			return mapConfigConstraint("complete certificate account deactivation", err)
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		account.Status = certificate.AccountStatusDeactivated
		account.UpdatedAt = at.UTC()
		updated = account
		return insertCertificateAudit(ctx, connection, "acme_account", string(id),
			"certificate.account.deactivate.complete", actorUserID, requestID, at,
			map[string]any{"environment": account.Environment})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return certificate.Account{}, fmt.Errorf("complete certificate account deactivation: %w", fs.ErrNotExist)
	}
	if err != nil {
		return certificate.Account{}, fmt.Errorf("complete certificate account deactivation: %w", err)
	}
	return updated, nil
}

// CreateCertificateDNSCredential persists only safe Token metadata and an audit event.
func (database *DB) CreateCertificateDNSCredential(ctx context.Context, credential certificate.DNSCredential) error {
	if ctx == nil || certificate.ValidateDNSCredential(credential) != nil {
		return fmt.Errorf("create certificate DNS credential: invalid input")
	}
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		_, err := connection.ExecContext(ctx, `INSERT INTO certificate_dns_credentials(
			id, name, provider, fingerprint, status, verified_at, last_used_at,
			created_by, request_id, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, credential.ID, credential.Name,
			credential.Provider, credential.Fingerprint, credential.Status, formatTime(credential.VerifiedAt),
			nullableTime(credential.LastUsedAt), credential.CreatedBy, credential.RequestID,
			formatTime(credential.CreatedAt), formatTime(credential.UpdatedAt))
		if err != nil {
			return mapConfigConstraint("insert certificate DNS credential", err)
		}
		return insertCertificateAudit(ctx, connection, "certificate_dns_credential", string(credential.ID),
			"certificate.credential.create", credential.CreatedBy, credential.RequestID, credential.CreatedAt,
			map[string]any{"provider": credential.Provider})
	})
	if err != nil {
		return fmt.Errorf("create certificate DNS credential: %w", err)
	}
	return nil
}

// CertificateDNSCredential returns one exact secret-free credential resource.
func (database *DB) CertificateDNSCredential(
	ctx context.Context,
	id certificate.DNSCredentialID,
) (certificate.DNSCredential, error) {
	if ctx == nil || parseCertificateID(string(id)) != nil {
		return certificate.DNSCredential{}, fmt.Errorf("read certificate DNS credential: invalid id")
	}
	credential, err := scanCertificateDNSCredential(database.sql.QueryRowContext(ctx, `SELECT id, name,
		provider, fingerprint, status, verified_at, last_used_at, created_by, request_id,
		created_at, updated_at FROM certificate_dns_credentials WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return certificate.DNSCredential{}, fmt.Errorf("read certificate DNS credential: %w", fs.ErrNotExist)
	}
	if err != nil {
		return certificate.DNSCredential{}, fmt.Errorf("read certificate DNS credential: %w", err)
	}
	return credential, nil
}

// CertificateDNSCredentials returns the bounded newest-first credential inventory.
func (database *DB) CertificateDNSCredentials(ctx context.Context) ([]certificate.DNSCredential, error) {
	rows, err := database.sql.QueryContext(ctx, `SELECT id, name, provider, fingerprint, status,
		verified_at, last_used_at, created_by, request_id, created_at, updated_at
		FROM certificate_dns_credentials WHERE status <> 'deleted'
		ORDER BY created_at DESC, id DESC LIMIT ?`, certificateListLimit)
	if err != nil {
		return nil, fmt.Errorf("list certificate DNS credentials: %w", err)
	}
	defer func() { _ = rows.Close() }()
	credentials := make([]certificate.DNSCredential, 0)
	for rows.Next() {
		credential, scanErr := scanCertificateDNSCredential(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("list certificate DNS credentials: %w", scanErr)
		}
		credentials = append(credentials, credential)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list certificate DNS credentials: %w", err)
	}
	return credentials, nil
}

// MarkCertificateDNSCredentialUsed records safe operational metadata only while the credential remains usable.
func (database *DB) MarkCertificateDNSCredentialUsed(
	ctx context.Context,
	id certificate.DNSCredentialID,
	at time.Time,
) error {
	if ctx == nil || parseCertificateID(string(id)) != nil || at.IsZero() {
		return fmt.Errorf("mark certificate DNS credential used: invalid input")
	}
	result, err := database.sql.ExecContext(ctx, `UPDATE certificate_dns_credentials
		SET last_used_at = ?, updated_at = ?
		WHERE id = ? AND status = 'valid' AND (last_used_at IS NULL OR last_used_at <= ?)`,
		formatTime(at), formatTime(at), id, formatTime(at))
	if err != nil {
		return fmt.Errorf("mark certificate DNS credential used: %w",
			mapConfigConstraint("update certificate DNS credential use", err))
	}
	matched, err := result.RowsAffected()
	if err != nil || matched != 1 {
		return fmt.Errorf("mark certificate DNS credential used: %w",
			errors.Join(certificate.ErrCloudflarePermission, err))
	}
	return nil
}

// DeleteCertificateDNSCredential soft-deletes unreferenced metadata while retaining historical foreign keys.
func (database *DB) DeleteCertificateDNSCredential(
	ctx context.Context,
	id certificate.DNSCredentialID,
	actorUserID int64,
	requestID string,
	at time.Time,
) (certificate.DNSCredential, error) {
	if ctx == nil || parseCertificateID(string(id)) != nil || actorUserID <= 0 ||
		requestID == "" || len(requestID) > 128 || at.IsZero() {
		return certificate.DNSCredential{}, fmt.Errorf("delete certificate DNS credential: invalid input")
	}
	var deleted certificate.DNSCredential
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		credential, err := scanCertificateDNSCredential(connection.QueryRowContext(ctx, `SELECT id, name,
			provider, fingerprint, status, verified_at, last_used_at, created_by, request_id,
			created_at, updated_at FROM certificate_dns_credentials WHERE id = ?`, id))
		if err != nil {
			return err
		}
		if credential.Status == certificate.CredentialStatusDeleted {
			deleted = credential
			return nil
		}
		var references int
		if err := connection.QueryRowContext(ctx, `SELECT
			(SELECT COUNT(*) FROM certificates WHERE dns_credential_id = ? AND state <> 'deleted') +
			(SELECT COUNT(*) FROM certificate_tasks WHERE dns_credential_id = ? AND state IN ('queued','running','cancelling')) +
			(SELECT COUNT(*) FROM certificate_order_plans WHERE dns_credential_id = ? AND state = 'planned' AND expires_at > ?) +
			(SELECT COUNT(*) FROM certificate_challenge_artifacts WHERE dns_credential_id = ? AND state = 'created')`,
			id, id, id, formatTime(at), id).Scan(&references); err != nil {
			return err
		}
		if references != 0 {
			return certificate.ErrTaskActive
		}
		result, err := connection.ExecContext(ctx, `UPDATE certificate_dns_credentials
			SET status = 'deleted', updated_at = ? WHERE id = ? AND status <> 'deleted'`, formatTime(at), id)
		if err != nil {
			return mapConfigConstraint("delete certificate DNS credential", err)
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		credential.Status = certificate.CredentialStatusDeleted
		credential.UpdatedAt = at.UTC()
		deleted = credential
		return insertCertificateAudit(ctx, connection, "certificate_dns_credential", string(id),
			"certificate.credential.delete", actorUserID, requestID, at,
			map[string]any{"provider": credential.Provider})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return certificate.DNSCredential{}, fmt.Errorf("delete certificate DNS credential: %w", fs.ErrNotExist)
	}
	if err != nil {
		return certificate.DNSCredential{}, fmt.Errorf("delete certificate DNS credential: %w", err)
	}
	return deleted, nil
}

// CreateCertificateOrderPlan persists an exact expiring plan before external changes.
func (database *DB) CreateCertificateOrderPlan(ctx context.Context, plan certificate.OrderPlan) error {
	if ctx == nil || certificate.ValidateOrderPlan(plan) != nil || plan.State != certificate.PlanStatePlanned {
		return fmt.Errorf("create certificate order plan: invalid input")
	}
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		_, err := connection.ExecContext(ctx, `INSERT INTO certificate_order_plans(
			id, state, environment, challenge, account_id, staging_account_id, dns_credential_id,
			certificate_id, version_id, primary_identifier, identifiers_json, server_refs_json,
			production_digest, binding_diff_json, staging_evidence, requires_risk_confirm,
			expires_at, created_by, request_id, created_at, executed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			plan.ID, plan.State, plan.Environment, plan.Challenge, plan.AccountID,
			nullableCertificateString(string(plan.StagingAccountID)), nullableCertificateString(string(plan.DNSCredentialID)),
			nullableCertificateString(string(plan.CertificateID)), nullableCertificateString(string(plan.VersionID)),
			plan.PrimaryIdentifier, plan.IdentifiersJSON, plan.ServerRefsJSON, plan.ProductionDigest[:],
			plan.BindingDiffJSON, boolInt(plan.StagingEvidence), boolInt(plan.RequiresRiskConfirm),
			formatTime(plan.ExpiresAt), plan.CreatedBy, plan.RequestID, formatTime(plan.CreatedAt), nil)
		if err != nil {
			return mapConfigConstraint("insert certificate order plan", err)
		}
		return insertCertificateAudit(ctx, connection, "certificate_order_plan", string(plan.ID),
			"certificate.plan.create", plan.CreatedBy, plan.RequestID, plan.CreatedAt,
			map[string]any{"environment": plan.Environment, "challenge": plan.Challenge})
	})
	if err != nil {
		return fmt.Errorf("create certificate order plan: %w", err)
	}
	return nil
}

// CertificateOrderPlan returns an unexpired exact plan.
func (database *DB) CertificateOrderPlan(
	ctx context.Context,
	id certificate.OrderPlanID,
	now time.Time,
) (certificate.OrderPlan, error) {
	if ctx == nil || parseCertificateID(string(id)) != nil || now.IsZero() {
		return certificate.OrderPlan{}, fmt.Errorf("read certificate order plan: invalid input")
	}
	plan, err := scanCertificateOrderPlan(database.sql.QueryRowContext(ctx, `SELECT id, state,
		environment, challenge, account_id, staging_account_id, dns_credential_id, certificate_id,
		version_id, primary_identifier, identifiers_json, server_refs_json, production_digest,
		binding_diff_json, staging_evidence, requires_risk_confirm, expires_at, created_by,
		request_id, created_at, executed_at FROM certificate_order_plans WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return certificate.OrderPlan{}, fmt.Errorf("read certificate order plan: %w", fs.ErrNotExist)
	}
	if err != nil {
		return certificate.OrderPlan{}, fmt.Errorf("read certificate order plan: %w", err)
	}
	if plan.State == certificate.PlanStatePlanned && !now.Before(plan.ExpiresAt) {
		return certificate.OrderPlan{}, fmt.Errorf("read certificate order plan: %w", certificate.ErrPlanExpired)
	}
	return plan, nil
}

// CreateCertificateBindingPlan persists one exact standalone binding review.
func (database *DB) CreateCertificateBindingPlan(ctx context.Context, plan certificate.BindingPlan) error {
	if ctx == nil || certificate.ValidateBindingPlan(plan) != nil || plan.State != certificate.PlanStatePlanned {
		return fmt.Errorf("create certificate binding plan: invalid input")
	}
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		var activeVersionID string
		if err := connection.QueryRowContext(ctx, `SELECT active_version_id FROM certificates
			WHERE id = ? AND state IN ('active','expiring','expired','unbound')`, plan.CertificateID).
			Scan(&activeVersionID); err != nil {
			return err
		}
		if activeVersionID != string(plan.VersionID) {
			return config.ErrConflict
		}
		_, err := connection.ExecContext(ctx, `INSERT INTO certificate_binding_plans(
			id, state, certificate_id, version_id, server_refs_json, production_digest,
			binding_diff_json, expires_at, created_by, request_id, created_at, executed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`, plan.ID, plan.State, plan.CertificateID,
			plan.VersionID, plan.ServerRefsJSON, plan.ProductionDigest[:], plan.BindingDiffJSON,
			formatTime(plan.ExpiresAt), plan.CreatedBy, plan.RequestID, formatTime(plan.CreatedAt))
		if err != nil {
			return mapConfigConstraint("insert certificate binding plan", err)
		}
		return insertCertificateAudit(ctx, connection, "certificate_binding_plan", string(plan.ID),
			"certificate.binding_plan.create", plan.CreatedBy, plan.RequestID, plan.CreatedAt,
			map[string]any{"certificate_id": plan.CertificateID})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("create certificate binding plan: %w", fs.ErrNotExist)
	}
	if err != nil {
		return fmt.Errorf("create certificate binding plan: %w", err)
	}
	return nil
}

// CertificateBindingPlan returns one exact plan and rejects expired unconsumed reviews.
func (database *DB) CertificateBindingPlan(
	ctx context.Context,
	id certificate.BindingPlanID,
	now time.Time,
) (certificate.BindingPlan, error) {
	if ctx == nil || parseCertificateID(string(id)) != nil || now.IsZero() {
		return certificate.BindingPlan{}, fmt.Errorf("read certificate binding plan: invalid input")
	}
	plan, err := scanCertificateBindingPlan(database.sql.QueryRowContext(
		ctx, certificateBindingPlanSelect+" WHERE id = ?", id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return certificate.BindingPlan{}, fmt.Errorf("read certificate binding plan: %w", fs.ErrNotExist)
	}
	if err != nil {
		return certificate.BindingPlan{}, fmt.Errorf("read certificate binding plan: %w", err)
	}
	if plan.State == certificate.PlanStatePlanned && !now.Before(plan.ExpiresAt) {
		return certificate.BindingPlan{}, fmt.Errorf("read certificate binding plan: %w", certificate.ErrPlanExpired)
	}
	return plan, nil
}

// ExecuteCertificateBindingPlan consumes one plan and creates its queued task in the same transaction.
func (database *DB) ExecuteCertificateBindingPlan(
	ctx context.Context,
	planID certificate.BindingPlanID,
	at time.Time,
	task certificate.Task,
	stage certificate.TaskStage,
) error {
	if ctx == nil || parseCertificateID(string(planID)) != nil || at.IsZero() ||
		certificate.ValidateTask(task) != nil || task.Kind != certificate.TaskKindBind ||
		certificate.BindingPlanID(task.PlanID) != planID || task.State != certificate.TaskStateQueued ||
		task.Stage != certificate.TaskStageQueued || certificate.ValidateTaskStage(stage) != nil ||
		stage.TaskID != task.ID || stage.Sequence != 1 || stage.Stage != task.Stage ||
		stage.Result != certificate.StageResultPending {
		return fmt.Errorf("execute certificate binding plan: invalid input")
	}
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		var state certificate.PlanState
		var certificateID, versionID, expiresAt string
		if err := connection.QueryRowContext(ctx, `SELECT state, certificate_id, version_id, expires_at
			FROM certificate_binding_plans WHERE id = ?`, planID).
			Scan(&state, &certificateID, &versionID, &expiresAt); err != nil {
			return err
		}
		expiry, err := parseTime("certificate binding plan expiry", expiresAt)
		if err != nil {
			return err
		}
		if state != certificate.PlanStatePlanned || certificateID != string(task.CertificateID) ||
			versionID != string(task.VersionID) {
			return config.ErrConflict
		}
		if !at.Before(expiry) {
			return certificate.ErrPlanExpired
		}
		var activeVersionID string
		if err := connection.QueryRowContext(ctx, `SELECT active_version_id FROM certificates
			WHERE id = ? AND state IN ('active','expiring','expired','unbound')`, task.CertificateID).
			Scan(&activeVersionID); err != nil {
			return err
		}
		if activeVersionID != string(task.VersionID) {
			return config.ErrConflict
		}
		result, err := connection.ExecContext(ctx, `UPDATE certificate_binding_plans
			SET state = 'executed', executed_at = ? WHERE id = ? AND state = 'planned'`, formatTime(at), planID)
		if err != nil {
			return mapConfigConstraint("execute certificate binding plan", err)
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		if err := insertCertificateTaskRecord(ctx, connection, task); err != nil {
			return err
		}
		if err := insertCertificateTaskStage(ctx, connection, stage); err != nil {
			return err
		}
		if err := insertCertificateAudit(ctx, connection, "certificate_binding_plan", string(planID),
			"certificate.binding_plan.execute", task.CreatedBy, task.RequestID, at,
			map[string]any{"task_id": task.ID}); err != nil {
			return err
		}
		return insertCertificateAudit(ctx, connection, "certificate_task", string(task.ID),
			"certificate.bind.queue", task.CreatedBy, task.RequestID, at,
			map[string]any{"certificate_id": task.CertificateID})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("execute certificate binding plan: %w", fs.ErrNotExist)
	}
	if err != nil {
		return fmt.Errorf("execute certificate binding plan: %w", err)
	}
	return nil
}

// HasCertificateStagingEvidence reports a prior successful exact staging validation.
func (database *DB) HasCertificateStagingEvidence(
	ctx context.Context,
	identifiersJSON string,
	challenge certificate.ChallengeType,
	now time.Time,
) (bool, error) {
	if ctx == nil || identifiersJSON == "" || now.IsZero() ||
		(challenge != certificate.ChallengeHTTP01 && challenge != certificate.ChallengeCloudflareDNS01) {
		return false, fmt.Errorf("read certificate staging evidence: invalid input")
	}
	cutoff := now.UTC().Add(-24 * time.Hour)
	var exists int
	err := database.sql.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM certificate_tasks task
		JOIN certificate_order_plans plan ON plan.id = task.plan_id
		WHERE plan.environment = 'staging' AND plan.identifiers_json = ? AND plan.challenge = ?
			AND task.state = 'succeeded' AND task.finished_at IS NOT NULL AND task.finished_at >= ?
	)`, identifiersJSON, challenge, formatTime(cutoff)).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("read certificate staging evidence: %w", err)
	}
	return exists != 0, nil
}

// CreateCertificateTask persists a queued task and its first stage in one transaction.
func (database *DB) CreateCertificateTask(
	ctx context.Context,
	task certificate.Task,
	stage certificate.TaskStage,
) error {
	if ctx == nil || certificate.ValidateTask(task) != nil || task.State != certificate.TaskStateQueued ||
		task.Stage != certificate.TaskStageQueued || certificate.ValidateTaskStage(stage) != nil ||
		stage.TaskID != task.ID || stage.Sequence != 1 || stage.Stage != task.Stage ||
		stage.Result != certificate.StageResultPending {
		return fmt.Errorf("create certificate task: invalid input")
	}
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		if err := insertCertificateTaskRecord(ctx, connection, task); err != nil {
			return err
		}
		if err := insertCertificateTaskStage(ctx, connection, stage); err != nil {
			return err
		}
		return insertCertificateAudit(ctx, connection, "certificate_task", string(task.ID),
			"certificate.task.create", task.CreatedBy, task.RequestID, task.CreatedAt,
			map[string]any{"kind": task.Kind, "challenge": task.Challenge})
	})
	if err != nil {
		return fmt.Errorf("create certificate task: %w", err)
	}
	return nil
}

// ExecuteCertificateOrderPlan consumes one unexpired plan and creates its queued task atomically.
func (database *DB) ExecuteCertificateOrderPlan(
	ctx context.Context,
	planID certificate.OrderPlanID,
	at time.Time,
	task certificate.Task,
	stage certificate.TaskStage,
) error {
	if ctx == nil || parseCertificateID(string(planID)) != nil || at.IsZero() ||
		certificate.ValidateTask(task) != nil || task.PlanID != planID || task.State != certificate.TaskStateQueued ||
		task.Stage != certificate.TaskStageQueued || certificate.ValidateTaskStage(stage) != nil ||
		stage.TaskID != task.ID || stage.Sequence != 1 || stage.Stage != task.Stage {
		return fmt.Errorf("execute certificate order plan: invalid input")
	}
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		var state certificate.PlanState
		var expiresAt string
		if err := connection.QueryRowContext(ctx, `SELECT state, expires_at FROM certificate_order_plans WHERE id = ?`, planID).
			Scan(&state, &expiresAt); err != nil {
			return err
		}
		expiry, err := parseTime("certificate plan expiry", expiresAt)
		if err != nil {
			return err
		}
		if state != certificate.PlanStatePlanned {
			return config.ErrConflict
		}
		if !at.Before(expiry) {
			return certificate.ErrPlanExpired
		}
		result, err := connection.ExecContext(ctx, `UPDATE certificate_order_plans
			SET state = 'executed', executed_at = ? WHERE id = ? AND state = 'planned'`, formatTime(at), planID)
		if err != nil {
			return mapConfigConstraint("execute certificate order plan", err)
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		if err := insertCertificateTaskRecord(ctx, connection, task); err != nil {
			return err
		}
		if err := insertCertificateTaskStage(ctx, connection, stage); err != nil {
			return err
		}
		if err := insertCertificateAudit(ctx, connection, "certificate_order_plan", string(planID),
			"certificate.plan.execute", task.CreatedBy, task.RequestID, at,
			map[string]any{"task_id": task.ID}); err != nil {
			return err
		}
		return insertCertificateAudit(ctx, connection, "certificate_task", string(task.ID),
			"certificate.task.create", task.CreatedBy, task.RequestID, at,
			map[string]any{"kind": task.Kind, "challenge": task.Challenge})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("execute certificate order plan: %w", fs.ErrNotExist)
	}
	if err != nil {
		return fmt.Errorf("execute certificate order plan: %w", err)
	}
	return nil
}

// CreateCertificateRenewal atomically persists an already-reviewed renewal plan and its queued task.
func (database *DB) CreateCertificateRenewal(
	ctx context.Context,
	plan certificate.OrderPlan,
	task certificate.Task,
	stage certificate.TaskStage,
) error {
	if ctx == nil || certificate.ValidateOrderPlan(plan) != nil || plan.State != certificate.PlanStateExecuted ||
		certificate.ValidateTask(task) != nil || task.Kind != certificate.TaskKindRenew ||
		task.State != certificate.TaskStateQueued || task.Stage != certificate.TaskStageQueued ||
		task.PlanID != plan.ID || task.CertificateID != plan.CertificateID || task.VersionID != plan.VersionID ||
		task.AccountID != plan.AccountID || task.DNSCredentialID != plan.DNSCredentialID || task.Challenge != plan.Challenge ||
		certificate.ValidateTaskStage(stage) != nil || stage.TaskID != task.ID || stage.Sequence != 1 ||
		stage.Stage != task.Stage || stage.Result != certificate.StageResultPending {
		return fmt.Errorf("create certificate renewal: invalid input")
	}
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		var activeVersionID string
		if err := connection.QueryRowContext(ctx, `SELECT active_version_id FROM certificates
			WHERE id = ? AND state IN ('active', 'expiring', 'expired', 'unbound')`, task.CertificateID).
			Scan(&activeVersionID); err != nil {
			return err
		}
		if activeVersionID == "" || activeVersionID == string(task.VersionID) {
			return config.ErrConflict
		}
		_, err := connection.ExecContext(ctx, `INSERT INTO certificate_order_plans(
			id, state, environment, challenge, account_id, staging_account_id, dns_credential_id,
			certificate_id, version_id, primary_identifier, identifiers_json, server_refs_json,
			production_digest, binding_diff_json, staging_evidence, requires_risk_confirm,
			expires_at, created_by, request_id, created_at, executed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			plan.ID, plan.State, plan.Environment, plan.Challenge, plan.AccountID,
			nullableCertificateString(string(plan.StagingAccountID)), nullableCertificateString(string(plan.DNSCredentialID)),
			plan.CertificateID, plan.VersionID, plan.PrimaryIdentifier, plan.IdentifiersJSON, plan.ServerRefsJSON,
			plan.ProductionDigest[:], plan.BindingDiffJSON, boolInt(plan.StagingEvidence), boolInt(plan.RequiresRiskConfirm),
			formatTime(plan.ExpiresAt), plan.CreatedBy, plan.RequestID, formatTime(plan.CreatedAt), formatTime(plan.ExecutedAt))
		if err != nil {
			return mapConfigConstraint("insert certificate renewal plan", err)
		}
		if err := insertCertificateTaskRecord(ctx, connection, task); err != nil {
			return err
		}
		if err := insertCertificateTaskStage(ctx, connection, stage); err != nil {
			return err
		}
		if err := insertCertificateAudit(ctx, connection, "certificate_order_plan", string(plan.ID),
			"certificate.renew.plan", task.CreatedBy, task.RequestID, task.CreatedAt,
			map[string]any{"certificate_id": task.CertificateID}); err != nil {
			return err
		}
		return insertCertificateAudit(ctx, connection, "certificate_task", string(task.ID),
			"certificate.renew.queue", task.CreatedBy, task.RequestID, task.CreatedAt,
			map[string]any{"certificate_id": task.CertificateID, "challenge": task.Challenge})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("create certificate renewal: %w", fs.ErrNotExist)
	}
	if err != nil {
		return fmt.Errorf("create certificate renewal: %w", err)
	}
	return nil
}

// TransitionCertificateTask performs an exact state/stage CAS and appends one stage atomically.
func (database *DB) TransitionCertificateTask(
	ctx context.Context,
	expectedState certificate.TaskState,
	expectedStage certificate.TaskStageName,
	next certificate.Task,
	stage certificate.TaskStage,
) error {
	if ctx == nil || !expectedState.Valid() || !expectedStage.Valid() || certificate.ValidateTask(next) != nil ||
		certificate.ValidateTaskStage(stage) != nil || stage.TaskID != next.ID || stage.Stage != next.Stage {
		return fmt.Errorf("transition certificate task: invalid input")
	}
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		var currentState certificate.TaskState
		var currentStage certificate.TaskStageName
		var lastSequence uint64
		if err := connection.QueryRowContext(ctx, `SELECT state, stage,
			COALESCE((SELECT MAX(sequence) FROM certificate_task_stages WHERE task_id = certificate_tasks.id), 0)
			FROM certificate_tasks WHERE id = ?`, next.ID).Scan(&currentState, &currentStage, &lastSequence); err != nil {
			return err
		}
		if currentState != expectedState || currentStage != expectedStage || currentState.Terminal() ||
			lastSequence+1 != stage.Sequence {
			return config.ErrConflict
		}
		result, err := connection.ExecContext(ctx, `UPDATE certificate_tasks SET
			state = ?, stage = ?, certificate_id = ?, version_id = ?, release_id = ?,
			last_error_code = ?, cancel_requested_at = COALESCE(cancel_requested_at, ?),
			updated_at = ?, started_at = ?, finished_at = ?
			WHERE id = ? AND state = ? AND stage = ?`, next.State, next.Stage,
			nullableCertificateString(string(next.CertificateID)), nullableCertificateString(string(next.VersionID)),
			next.ReleaseID, next.LastErrorCode, nullableTime(next.CancelRequestedAt), formatTime(next.UpdatedAt),
			nullableTime(next.StartedAt), nullableTime(next.FinishedAt), next.ID, expectedState, expectedStage)
		if err != nil {
			return mapConfigConstraint("update certificate task", err)
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		return insertCertificateTaskStage(ctx, connection, stage)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("transition certificate task: %w", fs.ErrNotExist)
	}
	if err != nil {
		return fmt.Errorf("transition certificate task: %w", err)
	}
	return nil
}

// CertificateTask returns one task with its immutable ordered stages.
func (database *DB) CertificateTask(ctx context.Context, id certificate.TaskID) (certificate.Task, error) {
	if ctx == nil || parseCertificateID(string(id)) != nil {
		return certificate.Task{}, fmt.Errorf("read certificate task: invalid id")
	}
	task, err := scanCertificateTask(database.sql.QueryRowContext(ctx, certificateTaskSelect+" WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return certificate.Task{}, fmt.Errorf("read certificate task: %w", fs.ErrNotExist)
	}
	if err != nil {
		return certificate.Task{}, fmt.Errorf("read certificate task: %w", err)
	}
	stages, err := database.certificateTaskStages(ctx, id)
	if err != nil {
		return certificate.Task{}, err
	}
	task.Stages = stages
	return task, nil
}

// RequestCertificateTaskCancellation durably records intent without racing the task owner's stage CAS.
func (database *DB) RequestCertificateTaskCancellation(
	ctx context.Context,
	id certificate.TaskID,
	actorUserID int64,
	requestID string,
	at time.Time,
) (certificate.Task, error) {
	if ctx == nil || parseCertificateID(string(id)) != nil || actorUserID <= 0 || requestID == "" ||
		len(requestID) > 128 || at.IsZero() {
		return certificate.Task{}, fmt.Errorf("request certificate task cancellation: invalid input")
	}
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		var state certificate.TaskState
		var cancelRequestedAt sql.NullString
		if err := connection.QueryRowContext(ctx, `SELECT state, cancel_requested_at
			FROM certificate_tasks WHERE id = ?`, id).Scan(&state, &cancelRequestedAt); err != nil {
			return err
		}
		if state.Terminal() {
			return config.ErrConflict
		}
		if cancelRequestedAt.Valid {
			return nil
		}
		result, err := connection.ExecContext(ctx, `UPDATE certificate_tasks
			SET cancel_requested_at = ?, updated_at = ?
			WHERE id = ? AND state IN ('queued', 'running', 'cancelling') AND cancel_requested_at IS NULL`,
			formatTime(at), formatTime(at), id)
		if err != nil {
			return mapConfigConstraint("request certificate task cancellation", err)
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		return insertCertificateAudit(ctx, connection, "certificate_task", string(id),
			"certificate.task.cancel", actorUserID, requestID, at, map[string]any{})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return certificate.Task{}, fmt.Errorf("request certificate task cancellation: %w", fs.ErrNotExist)
	}
	if err != nil {
		return certificate.Task{}, fmt.Errorf("request certificate task cancellation: %w", err)
	}
	return database.CertificateTask(ctx, id)
}

// CertificateTasks returns a bounded newest-first task history with stages.
func (database *DB) CertificateTasks(ctx context.Context, limit int) ([]certificate.Task, error) {
	if ctx == nil || limit <= 0 || limit > certificateListLimit {
		return nil, fmt.Errorf("list certificate tasks: invalid input")
	}
	rows, err := database.sql.QueryContext(ctx, certificateTaskSelect+" ORDER BY created_at DESC, id DESC LIMIT ?", limit)
	if err != nil {
		return nil, fmt.Errorf("list certificate tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	tasks := make([]certificate.Task, 0)
	for rows.Next() {
		task, scanErr := scanCertificateTask(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("list certificate tasks: %w", scanErr)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list certificate tasks: %w", err)
	}
	for index := range tasks {
		stages, stageErr := database.certificateTaskStages(ctx, tasks[index].ID)
		if stageErr != nil {
			return nil, stageErr
		}
		tasks[index].Stages = stages
	}
	return tasks, nil
}

// ActiveCertificateTasks returns bounded oldest-first work requiring an owner or startup reconciliation.
func (database *DB) ActiveCertificateTasks(ctx context.Context, limit int) ([]certificate.Task, error) {
	if ctx == nil || limit <= 0 || limit > certificateListLimit {
		return nil, fmt.Errorf("list active certificate tasks: invalid input")
	}
	rows, err := database.sql.QueryContext(ctx, certificateTaskSelect+`
		WHERE state IN ('queued', 'running', 'cancelling') ORDER BY created_at ASC, id ASC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list active certificate tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	tasks := make([]certificate.Task, 0)
	for rows.Next() {
		task, scanErr := scanCertificateTask(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("list active certificate tasks: %w", scanErr)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list active certificate tasks: %w", err)
	}
	for index := range tasks {
		stages, stageErr := database.certificateTaskStages(ctx, tasks[index].ID)
		if stageErr != nil {
			return nil, stageErr
		}
		tasks[index].Stages = stages
	}
	return tasks, nil
}

// CreateCertificateChallengeArtifact persists an exact cleanup target before challenge acceptance.
func (database *DB) CreateCertificateChallengeArtifact(
	ctx context.Context,
	artifact certificate.ChallengeArtifact,
) error {
	if ctx == nil || certificate.ValidateArtifact(artifact) != nil || artifact.State != certificate.ArtifactStateCreated {
		return fmt.Errorf("create certificate challenge artifact: invalid input")
	}
	_, err := database.sql.ExecContext(ctx, `INSERT INTO certificate_challenge_artifacts(
		id, task_id, kind, state, dns_credential_id, zone_id, record_id, record_name,
		config_path, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, artifact.ID, artifact.TaskID, artifact.Kind,
		artifact.State, nullableCertificateString(string(artifact.DNSCredentialID)), artifact.ZoneID,
		artifact.RecordID, artifact.RecordName, artifact.ConfigPath, formatTime(artifact.CreatedAt),
		formatTime(artifact.UpdatedAt))
	if err != nil {
		return mapConfigConstraint("create certificate challenge artifact", err)
	}
	return nil
}

// CertificateChallengeArtifacts returns exact task-owned cleanup targets in ID order.
func (database *DB) CertificateChallengeArtifacts(
	ctx context.Context,
	taskID certificate.TaskID,
) ([]certificate.ChallengeArtifact, error) {
	if ctx == nil || parseCertificateID(string(taskID)) != nil {
		return nil, fmt.Errorf("list certificate challenge artifacts: invalid input")
	}
	rows, err := database.sql.QueryContext(ctx, `SELECT id, task_id, kind, state, dns_credential_id,
		zone_id, record_id, record_name, config_path, created_at, updated_at
		FROM certificate_challenge_artifacts WHERE task_id = ? ORDER BY id ASC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list certificate challenge artifacts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	artifacts := make([]certificate.ChallengeArtifact, 0)
	for rows.Next() {
		artifact, scanErr := scanCertificateArtifact(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("list certificate challenge artifacts: %w", scanErr)
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list certificate challenge artifacts: %w", err)
	}
	return artifacts, nil
}

// UpdateCertificateChallengeArtifact applies a one-way exact cleanup state transition.
func (database *DB) UpdateCertificateChallengeArtifact(
	ctx context.Context,
	id certificate.ArtifactID,
	state certificate.ArtifactState,
	at time.Time,
) error {
	if ctx == nil || parseCertificateID(string(id)) != nil || at.IsZero() ||
		(state != certificate.ArtifactStateCleaned && state != certificate.ArtifactStateNeedsAttention) {
		return fmt.Errorf("update certificate challenge artifact: invalid input")
	}
	result, err := database.sql.ExecContext(ctx, `UPDATE certificate_challenge_artifacts
		SET state = ?, updated_at = ? WHERE id = ? AND state = 'created'`, state, formatTime(at), id)
	if err != nil {
		return mapConfigConstraint("update certificate challenge artifact", err)
	}
	matched, err := result.RowsAffected()
	if err != nil || matched != 1 {
		return fmt.Errorf("update certificate challenge artifact: %w", errors.Join(config.ErrConflict, err))
	}
	return nil
}

// CompleteIssuedCertificateTask atomically activates metadata and commits the task terminal stage.
func (database *DB) CompleteIssuedCertificateTask(
	ctx context.Context,
	expectedState certificate.TaskState,
	expectedStage certificate.TaskStageName,
	next certificate.Task,
	stage certificate.TaskStage,
	item certificate.Certificate,
	version certificate.Version,
	bindings []certificate.Binding,
) error {
	if ctx == nil || expectedState.Terminal() || !expectedStage.Valid() || certificate.ValidateTask(next) != nil ||
		next.State != certificate.TaskStateSucceeded || next.Stage != certificate.TaskStageCompleted ||
		certificate.ValidateTaskStage(stage) != nil || stage.TaskID != next.ID || stage.Stage != next.Stage ||
		certificate.ValidateCertificate(item) != nil || certificate.ValidateVersion(version) != nil ||
		version.CertificateID != item.ID || version.ID != item.ActiveVersionID || version.State != certificate.VersionStateActive {
		return fmt.Errorf("complete issued certificate task: invalid input")
	}
	for _, binding := range bindings {
		if certificate.ValidateBinding(binding) != nil || binding.CertificateID != item.ID || binding.VersionID != version.ID {
			return fmt.Errorf("complete issued certificate task: invalid binding")
		}
	}
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		var currentState certificate.TaskState
		var currentStage certificate.TaskStageName
		var lastSequence uint64
		if err := connection.QueryRowContext(ctx, `SELECT state, stage,
			COALESCE((SELECT MAX(sequence) FROM certificate_task_stages WHERE task_id = certificate_tasks.id), 0)
			FROM certificate_tasks WHERE id = ?`, next.ID).Scan(&currentState, &currentStage, &lastSequence); err != nil {
			return err
		}
		if currentState != expectedState || currentStage != expectedStage || lastSequence+1 != stage.Sequence {
			return config.ErrConflict
		}
		if err := insertCertificateRecord(ctx, connection, item); err != nil {
			return err
		}
		if err := insertCertificateVersion(ctx, connection, version); err != nil {
			return err
		}
		for _, binding := range bindings {
			if err := insertCertificateBinding(ctx, connection, binding); err != nil {
				return err
			}
		}
		result, err := connection.ExecContext(ctx, `UPDATE certificate_tasks SET
			state = ?, stage = ?, certificate_id = ?, version_id = ?, release_id = ?,
			last_error_code = '', updated_at = ?, finished_at = ?
			WHERE id = ? AND state = ? AND stage = ?`, next.State, next.Stage, next.CertificateID,
			next.VersionID, next.ReleaseID, formatTime(next.UpdatedAt), formatTime(next.FinishedAt),
			next.ID, expectedState, expectedStage)
		if err != nil {
			return mapConfigConstraint("complete certificate task", err)
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		if err := insertCertificateTaskStage(ctx, connection, stage); err != nil {
			return err
		}
		return insertCertificateAudit(ctx, connection, "certificate", string(item.ID),
			"certificate.issue", item.CreatedBy, item.RequestID, item.CreatedAt,
			map[string]any{"challenge": item.Challenge, "binding_count": len(bindings)})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("complete issued certificate task: %w", fs.ErrNotExist)
	}
	if err != nil {
		return fmt.Errorf("complete issued certificate task: %w", err)
	}
	return nil
}

// CompleteRenewedCertificateTask atomically supersedes the old active version after deployment succeeds.
func (database *DB) CompleteRenewedCertificateTask(
	ctx context.Context,
	expectedState certificate.TaskState,
	expectedStage certificate.TaskStageName,
	next certificate.Task,
	stage certificate.TaskStage,
	item certificate.Certificate,
	version certificate.Version,
	bindings []certificate.Binding,
	oldVersionID certificate.VersionID,
) error {
	if ctx == nil || expectedState.Terminal() || !expectedStage.Valid() ||
		certificate.ValidateTask(next) != nil || next.Kind != certificate.TaskKindRenew ||
		next.State != certificate.TaskStateSucceeded || next.Stage != certificate.TaskStageCompleted ||
		certificate.ValidateTaskStage(stage) != nil || stage.TaskID != next.ID || stage.Stage != next.Stage ||
		certificate.ValidateCertificate(item) != nil || certificate.ValidateVersion(version) != nil ||
		version.CertificateID != item.ID || version.ID != item.ActiveVersionID || version.State != certificate.VersionStateActive ||
		oldVersionID == "" || oldVersionID == version.ID || next.CertificateID != item.ID || next.VersionID != version.ID {
		return fmt.Errorf("complete renewed certificate task: invalid input")
	}
	for _, binding := range bindings {
		if certificate.ValidateBinding(binding) != nil || binding.CertificateID != item.ID || binding.VersionID != version.ID {
			return fmt.Errorf("complete renewed certificate task: invalid binding")
		}
	}
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		var currentState certificate.TaskState
		var currentStage certificate.TaskStageName
		var lastSequence uint64
		if err := connection.QueryRowContext(ctx, `SELECT state, stage,
			COALESCE((SELECT MAX(sequence) FROM certificate_task_stages WHERE task_id = certificate_tasks.id), 0)
			FROM certificate_tasks WHERE id = ?`, next.ID).Scan(&currentState, &currentStage, &lastSequence); err != nil {
			return err
		}
		if currentState != expectedState || currentStage != expectedStage || lastSequence+1 != stage.Sequence {
			return config.ErrConflict
		}
		current, err := scanCertificate(connection.QueryRowContext(ctx, certificateSelect+" WHERE id = ?", item.ID))
		if err != nil {
			return err
		}
		if current.ActiveVersionID != oldVersionID || current.PrimaryIdentifier != item.PrimaryIdentifier ||
			current.IdentifiersJSON != item.IdentifiersJSON || current.Challenge != item.Challenge ||
			current.AccountID != item.AccountID || current.DNSCredentialID != item.DNSCredentialID {
			return config.ErrConflict
		}
		result, err := connection.ExecContext(ctx, `UPDATE certificate_versions SET state = 'superseded'
			WHERE id = ? AND certificate_id = ? AND state = 'active'`, oldVersionID, item.ID)
		if err != nil {
			return mapConfigConstraint("supersede certificate version", err)
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		if err := insertCertificateVersion(ctx, connection, version); err != nil {
			return err
		}
		if _, err := connection.ExecContext(ctx, `DELETE FROM certificate_bindings
			WHERE certificate_id = ? AND version_id = ?`, item.ID, oldVersionID); err != nil {
			return fmt.Errorf("delete superseded certificate bindings: %w", err)
		}
		for _, binding := range bindings {
			if err := insertCertificateBinding(ctx, connection, binding); err != nil {
				return err
			}
		}
		result, err = connection.ExecContext(ctx, `UPDATE certificates SET state = ?, active_version_id = ?,
			auto_renew = ?, renew_before_seconds = ?, next_renewal_at = ?, retry_count = ?, retry_at = ?,
			not_before = ?, not_after = ?, last_error_code = ?, updated_at = ?
			WHERE id = ? AND active_version_id = ?`, item.State, item.ActiveVersionID, boolInt(item.AutoRenew),
			item.RenewBeforeSeconds, nullableTime(item.NextRenewalAt), item.RetryCount, nullableTime(item.RetryAt),
			formatTime(item.NotBefore), formatTime(item.NotAfter), item.LastErrorCode, formatTime(item.UpdatedAt),
			item.ID, oldVersionID)
		if err != nil {
			return mapConfigConstraint("activate renewed certificate", err)
		}
		matched, err = result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		result, err = connection.ExecContext(ctx, `UPDATE certificate_tasks SET
			state = ?, stage = ?, certificate_id = ?, version_id = ?, release_id = ?,
			last_error_code = '', updated_at = ?, finished_at = ?
			WHERE id = ? AND state = ? AND stage = ?`, next.State, next.Stage, next.CertificateID,
			next.VersionID, next.ReleaseID, formatTime(next.UpdatedAt), formatTime(next.FinishedAt),
			next.ID, expectedState, expectedStage)
		if err != nil {
			return mapConfigConstraint("complete renewed certificate task", err)
		}
		matched, err = result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		if err := insertCertificateTaskStage(ctx, connection, stage); err != nil {
			return err
		}
		return insertCertificateAudit(ctx, connection, "certificate", string(item.ID),
			"certificate.renew", next.CreatedBy, next.RequestID, item.UpdatedAt,
			map[string]any{"old_version_id": oldVersionID, "new_version_id": version.ID, "binding_count": len(bindings)})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("complete renewed certificate task: %w", fs.ErrNotExist)
	}
	if err != nil {
		return fmt.Errorf("complete renewed certificate task: %w", err)
	}
	return nil
}

// FailCertificateRenewalTask atomically terminates the task and fixes its persisted retry schedule.
func (database *DB) FailCertificateRenewalTask(
	ctx context.Context,
	expectedState certificate.TaskState,
	expectedStage certificate.TaskStageName,
	next certificate.Task,
	stage certificate.TaskStage,
	item certificate.Certificate,
) error {
	if ctx == nil || expectedState.Terminal() || !expectedStage.Valid() ||
		certificate.ValidateTask(next) != nil || next.Kind != certificate.TaskKindRenew ||
		next.State != certificate.TaskStateFailed || next.Stage != certificate.TaskStageFailed ||
		certificate.ValidateTaskStage(stage) != nil || stage.TaskID != next.ID || stage.Stage != next.Stage ||
		certificate.ValidateCertificate(item) != nil || item.ID != next.CertificateID || item.RetryCount <= 0 ||
		item.RetryAt.IsZero() || !item.RetryAt.After(item.UpdatedAt) || item.LastErrorCode == "" ||
		item.LastErrorCode != next.LastErrorCode {
		return fmt.Errorf("fail certificate renewal task: invalid input")
	}
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		var currentState certificate.TaskState
		var currentStage certificate.TaskStageName
		var lastSequence uint64
		if err := connection.QueryRowContext(ctx, `SELECT state, stage,
			COALESCE((SELECT MAX(sequence) FROM certificate_task_stages WHERE task_id = certificate_tasks.id), 0)
			FROM certificate_tasks WHERE id = ?`, next.ID).Scan(&currentState, &currentStage, &lastSequence); err != nil {
			return err
		}
		if currentState != expectedState || currentStage != expectedStage || lastSequence+1 != stage.Sequence {
			return config.ErrConflict
		}
		current, err := scanCertificate(connection.QueryRowContext(ctx, certificateSelect+" WHERE id = ?", item.ID))
		if err != nil {
			return err
		}
		if current.ActiveVersionID != item.ActiveVersionID || current.RetryCount+1 != item.RetryCount ||
			current.PrimaryIdentifier != item.PrimaryIdentifier || current.IdentifiersJSON != item.IdentifiersJSON ||
			current.AccountID != item.AccountID || current.DNSCredentialID != item.DNSCredentialID {
			return config.ErrConflict
		}
		result, err := connection.ExecContext(ctx, `UPDATE certificate_tasks SET
			state = ?, stage = ?, last_error_code = ?, updated_at = ?, finished_at = ?
			WHERE id = ? AND state = ? AND stage = ?`, next.State, next.Stage, next.LastErrorCode,
			formatTime(next.UpdatedAt), formatTime(next.FinishedAt), next.ID, expectedState, expectedStage)
		if err != nil {
			return mapConfigConstraint("fail certificate renewal task", err)
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		result, err = connection.ExecContext(ctx, `UPDATE certificates SET state = ?, retry_count = ?,
			retry_at = ?, last_error_code = ?, updated_at = ?
			WHERE id = ? AND active_version_id = ? AND retry_count = ?`, item.State, item.RetryCount,
			formatTime(item.RetryAt), item.LastErrorCode, formatTime(item.UpdatedAt), item.ID,
			item.ActiveVersionID, current.RetryCount)
		if err != nil {
			return mapConfigConstraint("schedule certificate renewal retry", err)
		}
		matched, err = result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		if err := insertCertificateTaskStage(ctx, connection, stage); err != nil {
			return err
		}
		return insertCertificateAudit(ctx, connection, "certificate", string(item.ID),
			"certificate.renew.failure", next.CreatedBy, next.RequestID, item.UpdatedAt,
			map[string]any{"retry_count": item.RetryCount, "retry_at": item.RetryAt, "error_code": item.LastErrorCode})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("fail certificate renewal task: %w", fs.ErrNotExist)
	}
	if err != nil {
		return fmt.Errorf("fail certificate renewal task: %w", err)
	}
	return nil
}

// CreateIssuedCertificate atomically publishes validated metadata after material is durable.
func (database *DB) CreateIssuedCertificate(
	ctx context.Context,
	item certificate.Certificate,
	version certificate.Version,
	bindings []certificate.Binding,
) error {
	if ctx == nil || certificate.ValidateCertificate(item) != nil || certificate.ValidateVersion(version) != nil ||
		version.CertificateID != item.ID || version.ID != item.ActiveVersionID || version.State != certificate.VersionStateActive {
		return fmt.Errorf("create issued certificate: invalid input")
	}
	for _, binding := range bindings {
		if certificate.ValidateBinding(binding) != nil || binding.CertificateID != item.ID || binding.VersionID != version.ID {
			return fmt.Errorf("create issued certificate: invalid binding")
		}
	}
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		if err := insertCertificateRecord(ctx, connection, item); err != nil {
			return err
		}
		if err := insertCertificateVersion(ctx, connection, version); err != nil {
			return err
		}
		for _, binding := range bindings {
			if err := insertCertificateBinding(ctx, connection, binding); err != nil {
				return err
			}
		}
		return insertCertificateAudit(ctx, connection, "certificate", string(item.ID),
			"certificate.issue", item.CreatedBy, item.RequestID, item.CreatedAt,
			map[string]any{"challenge": item.Challenge, "binding_count": len(bindings)})
	})
	if err != nil {
		return fmt.Errorf("create issued certificate: %w", err)
	}
	return nil
}

// Certificates returns a bounded newest-first inventory with no secret material.
func (database *DB) Certificates(ctx context.Context, limit int) ([]certificate.Certificate, error) {
	if limit <= 0 || limit > certificateListLimit {
		return nil, fmt.Errorf("list certificates: invalid limit")
	}
	rows, err := database.sql.QueryContext(ctx, certificateSelect+" WHERE state <> 'deleted' ORDER BY created_at DESC, id DESC LIMIT ?", limit)
	if err != nil {
		return nil, fmt.Errorf("list certificates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]certificate.Certificate, 0)
	for rows.Next() {
		item, scanErr := scanCertificate(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("list certificates: %w", scanErr)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list certificates: %w", err)
	}
	return items, nil
}

// Certificate returns one exact safe certificate resource.
func (database *DB) Certificate(ctx context.Context, id certificate.CertificateID) (certificate.Certificate, error) {
	if ctx == nil || parseCertificateID(string(id)) != nil {
		return certificate.Certificate{}, fmt.Errorf("read certificate: invalid id")
	}
	item, err := scanCertificate(database.sql.QueryRowContext(ctx, certificateSelect+" WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return certificate.Certificate{}, fmt.Errorf("read certificate: %w", fs.ErrNotExist)
	}
	if err != nil {
		return certificate.Certificate{}, fmt.Errorf("read certificate: %w", err)
	}
	return item, nil
}

// CertificateVersions returns immutable versions newest-first.
func (database *DB) CertificateVersions(
	ctx context.Context,
	id certificate.CertificateID,
) ([]certificate.Version, error) {
	if ctx == nil || parseCertificateID(string(id)) != nil {
		return nil, fmt.Errorf("list certificate versions: invalid id")
	}
	rows, err := database.sql.QueryContext(ctx, `SELECT id, certificate_id, state, fullchain_digest,
		private_key_digest, leaf_fingerprint, serial_number, issuer, not_before, not_after, created_at
		FROM certificate_versions WHERE certificate_id = ? ORDER BY created_at DESC, id DESC`, id)
	if err != nil {
		return nil, fmt.Errorf("list certificate versions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	versions := make([]certificate.Version, 0)
	for rows.Next() {
		version, scanErr := scanCertificateVersion(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("list certificate versions: %w", scanErr)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list certificate versions: %w", err)
	}
	return versions, nil
}

// CertificateMaterialInventory returns bounded active-version pairs in stable certificate-ID order.
func (database *DB) CertificateMaterialInventory(
	ctx context.Context,
	limit int,
) ([]certificate.CertificateMaterialRecord, error) {
	if ctx == nil || limit <= 0 || limit > 1001 {
		return nil, fmt.Errorf("list certificate material inventory: invalid input")
	}
	rows, err := database.sql.QueryContext(ctx, `SELECT id, active_version_id FROM certificates
		WHERE active_version_id IS NOT NULL AND state <> 'deleted' ORDER BY id ASC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list certificate material inventory: %w", err)
	}
	type materialID struct {
		certificateID certificate.CertificateID
		versionID     certificate.VersionID
	}
	ids := make([]materialID, 0)
	for rows.Next() {
		var id materialID
		if err := rows.Scan(&id.certificateID, &id.versionID); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("list certificate material inventory: %w", err)
		}
		ids = append(ids, id)
	}
	iterationErr := rows.Err()
	closeErr := rows.Close()
	if iterationErr != nil || closeErr != nil {
		return nil, fmt.Errorf("list certificate material inventory: %w", errors.Join(iterationErr, closeErr))
	}
	records := make([]certificate.CertificateMaterialRecord, 0, len(ids))
	for _, id := range ids {
		item, err := database.Certificate(ctx, id.certificateID)
		if err != nil {
			return nil, fmt.Errorf("list certificate material inventory: %w", err)
		}
		version, err := scanCertificateVersion(database.sql.QueryRowContext(ctx, `SELECT id, certificate_id,
			state, fullchain_digest, private_key_digest, leaf_fingerprint, serial_number, issuer,
			not_before, not_after, created_at FROM certificate_versions WHERE id = ? AND certificate_id = ?`,
			id.versionID, id.certificateID))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("list certificate material inventory: %w", fs.ErrNotExist)
		}
		if err != nil {
			return nil, fmt.Errorf("list certificate material inventory: %w", err)
		}
		records = append(records, certificate.CertificateMaterialRecord{Certificate: item, Version: version})
	}
	return records, nil
}

// MarkCertificateMaterialNeedsAttention atomically pauses scheduling evidence for invalid active material.
func (database *DB) MarkCertificateMaterialNeedsAttention(
	ctx context.Context,
	id certificate.CertificateID,
	versionID certificate.VersionID,
	code string,
	at time.Time,
) error {
	if ctx == nil || parseCertificateID(string(id)) != nil || parseCertificateID(string(versionID)) != nil ||
		code != "certificate_material_invalid" || at.IsZero() {
		return fmt.Errorf("mark certificate material needs attention: invalid input")
	}
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		item, err := scanCertificate(connection.QueryRowContext(ctx, certificateSelect+" WHERE id = ?", id))
		if err != nil {
			return err
		}
		var versionState certificate.VersionState
		if err := connection.QueryRowContext(ctx, `SELECT state FROM certificate_versions
			WHERE id = ? AND certificate_id = ?`, versionID, id).Scan(&versionState); err != nil {
			return err
		}
		if item.ActiveVersionID != versionID || item.State == certificate.CertificateStateDeleted {
			return config.ErrConflict
		}
		if item.State == certificate.CertificateStateNeedsAttention && item.LastErrorCode == code &&
			versionState == certificate.VersionStateNeedsAttention {
			return nil
		}
		if _, err := connection.ExecContext(ctx, `UPDATE certificate_versions SET state = 'needs_attention'
			WHERE id = ? AND certificate_id = ?`, versionID, id); err != nil {
			return mapConfigConstraint("mark certificate version needs attention", err)
		}
		result, err := connection.ExecContext(ctx, `UPDATE certificates SET state = 'needs_attention',
			last_error_code = ?, updated_at = ? WHERE id = ? AND active_version_id = ? AND state <> 'deleted'`,
			code, formatTime(at), id, versionID)
		if err != nil {
			return mapConfigConstraint("mark certificate material needs attention", err)
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		return insertCertificateAudit(ctx, connection, "certificate", string(id),
			"certificate.material.needs_attention", item.CreatedBy,
			"certificate-reconcile-"+string(id[:8]), at, map[string]any{"error_code": code})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("mark certificate material needs attention: %w", fs.ErrNotExist)
	}
	if err != nil {
		return fmt.Errorf("mark certificate material needs attention: %w", err)
	}
	return nil
}

// CertificateBindings returns exact current binding ownership in stable source order.
func (database *DB) CertificateBindings(
	ctx context.Context,
	id certificate.CertificateID,
) ([]certificate.Binding, error) {
	if ctx == nil || parseCertificateID(string(id)) != nil {
		return nil, fmt.Errorf("list certificate bindings: invalid id")
	}
	rows, err := database.sql.QueryContext(ctx, `SELECT id, certificate_id, version_id, config_path,
		server_start_offset, server_names_json, listeners_json, server_fingerprint, created_at, updated_at
		FROM certificate_bindings WHERE certificate_id = ?
		ORDER BY config_path ASC, server_start_offset ASC, id ASC`, id)
	if err != nil {
		return nil, fmt.Errorf("list certificate bindings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	bindings := make([]certificate.Binding, 0)
	for rows.Next() {
		binding, scanErr := scanCertificateBinding(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("list certificate bindings: %w", scanErr)
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list certificate bindings: %w", err)
	}
	return bindings, nil
}

// CertificateBindingByFingerprint returns the current owner of one exact source-derived server.
func (database *DB) CertificateBindingByFingerprint(
	ctx context.Context,
	fingerprint string,
) (certificate.Binding, error) {
	if ctx == nil || len(fingerprint) != 64 {
		return certificate.Binding{}, fmt.Errorf("read certificate binding owner: invalid fingerprint")
	}
	binding, err := scanCertificateBinding(database.sql.QueryRowContext(ctx, `SELECT id, certificate_id,
		version_id, config_path, server_start_offset, server_names_json, listeners_json,
		server_fingerprint, created_at, updated_at FROM certificate_bindings WHERE server_fingerprint = ?`, fingerprint))
	if errors.Is(err, sql.ErrNoRows) {
		return certificate.Binding{}, fmt.Errorf("read certificate binding owner: %w", fs.ErrNotExist)
	}
	if err != nil {
		return certificate.Binding{}, fmt.Errorf("read certificate binding owner: %w", err)
	}
	return binding, nil
}

// CompleteCertificateBindingTask atomically records release evidence, new ownership and terminal task state.
func (database *DB) CompleteCertificateBindingTask(
	ctx context.Context,
	expectedState certificate.TaskState,
	expectedStage certificate.TaskStageName,
	next certificate.Task,
	stage certificate.TaskStage,
	item certificate.Certificate,
	bindings []certificate.Binding,
) error {
	if ctx == nil || expectedState.Terminal() || !expectedStage.Valid() ||
		certificate.ValidateTask(next) != nil || next.Kind != certificate.TaskKindBind ||
		next.State != certificate.TaskStateSucceeded || next.Stage != certificate.TaskStageCompleted ||
		next.ReleaseID == "" || certificate.ValidateTaskStage(stage) != nil || stage.TaskID != next.ID ||
		stage.Stage != next.Stage || stage.Result != certificate.StageResultSuccess ||
		certificate.ValidateCertificate(item) != nil || item.ID != next.CertificateID ||
		item.ActiveVersionID != next.VersionID || item.State == certificate.CertificateStateUnbound ||
		len(bindings) == 0 || len(bindings) > certificateListLimit {
		return fmt.Errorf("complete certificate binding task: invalid input")
	}
	for _, binding := range bindings {
		if certificate.ValidateBinding(binding) != nil || binding.CertificateID != item.ID ||
			binding.VersionID != item.ActiveVersionID {
			return fmt.Errorf("complete certificate binding task: invalid binding")
		}
	}
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		var currentState certificate.TaskState
		var currentStage certificate.TaskStageName
		var lastSequence uint64
		if err := connection.QueryRowContext(ctx, `SELECT state, stage,
			COALESCE((SELECT MAX(sequence) FROM certificate_task_stages WHERE task_id = certificate_tasks.id), 0)
			FROM certificate_tasks WHERE id = ?`, next.ID).
			Scan(&currentState, &currentStage, &lastSequence); err != nil {
			return err
		}
		if currentState != expectedState || currentStage != expectedStage || lastSequence+1 != stage.Sequence {
			return config.ErrConflict
		}
		current, err := scanCertificate(connection.QueryRowContext(ctx, certificateSelect+" WHERE id = ?", item.ID))
		if err != nil {
			return err
		}
		if current.ActiveVersionID != item.ActiveVersionID || current.PrimaryIdentifier != item.PrimaryIdentifier ||
			current.IdentifiersJSON != item.IdentifiersJSON || current.Challenge != item.Challenge ||
			current.AccountID != item.AccountID || current.DNSCredentialID != item.DNSCredentialID ||
			current.State == certificate.CertificateStateDeleted || item.UpdatedAt.Before(current.UpdatedAt) {
			return config.ErrConflict
		}
		for _, binding := range bindings {
			if err := insertCertificateBinding(ctx, connection, binding); err != nil {
				return err
			}
		}
		result, err := connection.ExecContext(ctx, `UPDATE certificates SET state = ?, updated_at = ?
			WHERE id = ? AND active_version_id = ? AND state IN ('active','expiring','expired','unbound')`,
			item.State, formatTime(item.UpdatedAt), item.ID, item.ActiveVersionID)
		if err != nil {
			return mapConfigConstraint("activate certificate binding", err)
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		result, err = connection.ExecContext(ctx, `UPDATE certificate_tasks SET state = ?, stage = ?,
			release_id = ?, last_error_code = '', updated_at = ?, finished_at = ?
			WHERE id = ? AND state = ? AND stage = ?`, next.State, next.Stage, next.ReleaseID,
			formatTime(next.UpdatedAt), formatTime(next.FinishedAt), next.ID, expectedState, expectedStage)
		if err != nil {
			return mapConfigConstraint("complete certificate binding task", err)
		}
		matched, err = result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		if err := insertCertificateTaskStage(ctx, connection, stage); err != nil {
			return err
		}
		return insertCertificateAudit(ctx, connection, "certificate", string(item.ID),
			"certificate.bind", next.CreatedBy, next.RequestID, item.UpdatedAt,
			map[string]any{"release_id": next.ReleaseID, "binding_count": len(bindings)})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("complete certificate binding task: %w", fs.ErrNotExist)
	}
	if err != nil {
		return fmt.Errorf("complete certificate binding task: %w", err)
	}
	return nil
}

// CompleteCertificateUnbinding atomically clears exact binding ownership after a successful config release.
func (database *DB) CompleteCertificateUnbinding(
	ctx context.Context,
	id certificate.CertificateID,
	actor config.Actor,
	releaseID string,
	at time.Time,
) (certificate.Certificate, error) {
	if ctx == nil || parseCertificateID(string(id)) != nil || actor.UserID <= 0 || actor.RequestID == "" ||
		len(actor.RequestID) > 128 || parseCertificateID(releaseID) != nil || at.IsZero() {
		return certificate.Certificate{}, fmt.Errorf("complete certificate unbinding: invalid input")
	}
	var updated certificate.Certificate
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		item, err := scanCertificate(connection.QueryRowContext(ctx, certificateSelect+" WHERE id = ?", id))
		if err != nil {
			return err
		}
		if item.State == certificate.CertificateStateDeleted || item.ActiveVersionID == "" || at.Before(item.UpdatedAt) {
			return certificate.ErrCertificateReferenced
		}
		var active int
		if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM certificate_tasks
			WHERE certificate_id = ? AND state IN ('queued','running','cancelling')`, id).Scan(&active); err != nil {
			return err
		}
		if active != 0 {
			return certificate.ErrTaskActive
		}
		if _, err := connection.ExecContext(ctx, `DELETE FROM certificate_bindings WHERE certificate_id = ?`, id); err != nil {
			return mapConfigConstraint("delete certificate bindings", err)
		}
		result, err := connection.ExecContext(ctx, `UPDATE certificates SET state = 'unbound', updated_at = ?
			WHERE id = ? AND state <> 'deleted'`, formatTime(at), id)
		if err != nil {
			return mapConfigConstraint("update unbound certificate", err)
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		item.State = certificate.CertificateStateUnbound
		item.UpdatedAt = at.UTC()
		updated = item
		return insertCertificateAudit(ctx, connection, "certificate", string(id),
			"certificate.unbind", actor.UserID, actor.RequestID, at,
			map[string]any{"release_id": releaseID})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return certificate.Certificate{}, fmt.Errorf("complete certificate unbinding: %w", fs.ErrNotExist)
	}
	if err != nil {
		return certificate.Certificate{}, fmt.Errorf("complete certificate unbinding: %w", err)
	}
	return updated, nil
}

// RecordCertificateExport persists only whether private material was included, never the response bytes.
func (database *DB) RecordCertificateExport(
	ctx context.Context,
	id certificate.CertificateID,
	actor config.Actor,
	includedPrivateKey bool,
	at time.Time,
) error {
	if ctx == nil || parseCertificateID(string(id)) != nil || actor.UserID <= 0 || actor.RequestID == "" ||
		len(actor.RequestID) > 128 || at.IsZero() {
		return fmt.Errorf("record certificate export: invalid input")
	}
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		var state certificate.CertificateState
		if err := connection.QueryRowContext(ctx, `SELECT state FROM certificates WHERE id = ?`, id).Scan(&state); err != nil {
			return err
		}
		if state == certificate.CertificateStateDeleted {
			return fs.ErrNotExist
		}
		return insertCertificateAudit(ctx, connection, "certificate", string(id),
			"certificate.export", actor.UserID, actor.RequestID, at,
			map[string]any{"included_private_key": includedPrivateKey})
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = fs.ErrNotExist
	}
	if err != nil {
		return fmt.Errorf("record certificate export: %w", err)
	}
	return nil
}

// UpdateCertificateRenewalPolicy atomically changes one schedule and clears stale retry metadata.
func (database *DB) UpdateCertificateRenewalPolicy(
	ctx context.Context,
	id certificate.CertificateID,
	actor config.Actor,
	autoRenew bool,
	renewBeforeSeconds int64,
	nextRenewalAt time.Time,
	at time.Time,
) (certificate.Certificate, error) {
	if ctx == nil || parseCertificateID(string(id)) != nil || actor.UserID <= 0 || actor.RequestID == "" ||
		len(actor.RequestID) > 128 || renewBeforeSeconds <= 0 || renewBeforeSeconds > 90*24*60*60 ||
		at.IsZero() || autoRenew != !nextRenewalAt.IsZero() {
		return certificate.Certificate{}, fmt.Errorf("update certificate renewal policy: invalid input")
	}
	var updated certificate.Certificate
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		item, err := scanCertificate(connection.QueryRowContext(ctx, certificateSelect+" WHERE id = ?", id))
		if err != nil {
			return err
		}
		if item.State == certificate.CertificateStatePending || item.State == certificate.CertificateStateDeleted ||
			at.Before(item.UpdatedAt) {
			return certificate.ErrRenewalPolicyInvalid
		}
		var active int
		if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM certificate_tasks
			WHERE certificate_id = ? AND state IN ('queued','running','cancelling')`, id).Scan(&active); err != nil {
			return err
		}
		if active != 0 {
			return certificate.ErrTaskActive
		}
		result, err := connection.ExecContext(ctx, `UPDATE certificates SET auto_renew = ?,
			renew_before_seconds = ?, next_renewal_at = ?, retry_count = 0, retry_at = NULL,
			last_error_code = '', updated_at = ? WHERE id = ? AND state NOT IN ('pending','deleted')`,
			boolInt(autoRenew), renewBeforeSeconds, nullableTime(nextRenewalAt), formatTime(at), id)
		if err != nil {
			return mapConfigConstraint("update certificate renewal policy", err)
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		item.AutoRenew = autoRenew
		item.RenewBeforeSeconds = renewBeforeSeconds
		item.NextRenewalAt = nextRenewalAt.UTC()
		item.RetryCount = 0
		item.RetryAt = time.Time{}
		item.LastErrorCode = ""
		item.UpdatedAt = at.UTC()
		updated = item
		return insertCertificateAudit(ctx, connection, "certificate", string(id),
			"certificate.renewal_policy", actor.UserID, actor.RequestID, at,
			map[string]any{"auto_renew": autoRenew, "renew_before_seconds": renewBeforeSeconds})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return certificate.Certificate{}, fmt.Errorf("update certificate renewal policy: %w", fs.ErrNotExist)
	}
	if err != nil {
		return certificate.Certificate{}, fmt.Errorf("update certificate renewal policy: %w", err)
	}
	return updated, nil
}

// DeleteCertificate soft-deletes unbound metadata and removes version rows while retaining history FKs.
func (database *DB) DeleteCertificate(
	ctx context.Context,
	id certificate.CertificateID,
	actor config.Actor,
	at time.Time,
) (certificate.Certificate, error) {
	if ctx == nil || parseCertificateID(string(id)) != nil || actor.UserID <= 0 || actor.RequestID == "" ||
		len(actor.RequestID) > 128 || at.IsZero() {
		return certificate.Certificate{}, fmt.Errorf("delete certificate: invalid input")
	}
	var deleted certificate.Certificate
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		item, err := scanCertificate(connection.QueryRowContext(ctx, certificateSelect+" WHERE id = ?", id))
		if err != nil {
			return err
		}
		if item.State == certificate.CertificateStateDeleted {
			deleted = item
			return nil
		}
		if item.State != certificate.CertificateStateUnbound || at.Before(item.UpdatedAt) {
			return certificate.ErrCertificateReferenced
		}
		var references int
		if err := connection.QueryRowContext(ctx, `SELECT
			(SELECT COUNT(*) FROM certificate_bindings WHERE certificate_id = ?) +
			(SELECT COUNT(*) FROM certificate_tasks WHERE certificate_id = ? AND state IN ('queued','running','cancelling'))`,
			id, id).Scan(&references); err != nil {
			return err
		}
		if references != 0 {
			return certificate.ErrCertificateReferenced
		}
		if _, err := connection.ExecContext(ctx, `DELETE FROM certificate_versions WHERE certificate_id = ?`, id); err != nil {
			return mapConfigConstraint("delete certificate versions", err)
		}
		result, err := connection.ExecContext(ctx, `UPDATE certificates SET state = 'deleted',
			active_version_id = NULL, auto_renew = 0, next_renewal_at = NULL, retry_at = NULL,
			retry_count = 0, last_error_code = '', updated_at = ? WHERE id = ? AND state = 'unbound'`,
			formatTime(at), id)
		if err != nil {
			return mapConfigConstraint("soft delete certificate", err)
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		item.State = certificate.CertificateStateDeleted
		item.ActiveVersionID = ""
		item.AutoRenew = false
		item.NextRenewalAt = time.Time{}
		item.RetryAt = time.Time{}
		item.RetryCount = 0
		item.LastErrorCode = ""
		item.UpdatedAt = at.UTC()
		deleted = item
		return insertCertificateAudit(ctx, connection, "certificate", string(id),
			"certificate.delete", actor.UserID, actor.RequestID, at, map[string]any{})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return certificate.Certificate{}, fmt.Errorf("delete certificate: %w", fs.ErrNotExist)
	}
	if err != nil {
		return certificate.Certificate{}, fmt.Errorf("delete certificate: %w", err)
	}
	return deleted, nil
}

// DueCertificates returns stable bounded renewal work without an active task.
func (database *DB) DueCertificates(ctx context.Context, now time.Time, limit int) ([]certificate.Certificate, error) {
	if ctx == nil || now.IsZero() || limit <= 0 || limit > certificateListLimit {
		return nil, fmt.Errorf("list due certificates: invalid input")
	}
	rows, err := database.sql.QueryContext(ctx, certificateSelect+` WHERE auto_renew = 1
		AND state IN ('active', 'expiring', 'unbound')
		AND COALESCE(retry_at, next_renewal_at) IS NOT NULL
		AND COALESCE(retry_at, next_renewal_at) <= ?
		AND NOT EXISTS (SELECT 1 FROM certificate_tasks task WHERE task.certificate_id = certificates.id
			AND task.state IN ('queued', 'running', 'cancelling'))
		ORDER BY COALESCE(retry_at, next_renewal_at) ASC, id ASC LIMIT ?`, formatTime(now), limit)
	if err != nil {
		return nil, fmt.Errorf("list due certificates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]certificate.Certificate, 0)
	for rows.Next() {
		item, scanErr := scanCertificate(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("list due certificates: %w", scanErr)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list due certificates: %w", err)
	}
	return items, nil
}

func insertCertificateVersion(ctx context.Context, connection *sql.Conn, version certificate.Version) error {
	_, err := connection.ExecContext(ctx, `INSERT INTO certificate_versions(
		id, certificate_id, state, fullchain_digest, private_key_digest, leaf_fingerprint,
		serial_number, issuer, not_before, not_after, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, version.ID, version.CertificateID, version.State,
		version.FullchainDigest, version.PrivateKeyDigest, version.LeafFingerprint, version.SerialNumber,
		version.Issuer, formatTime(version.NotBefore), formatTime(version.NotAfter), formatTime(version.CreatedAt))
	if err != nil {
		return mapConfigConstraint("insert certificate version", err)
	}
	return nil
}

func insertCertificateRecord(ctx context.Context, connection *sql.Conn, item certificate.Certificate) error {
	_, err := connection.ExecContext(ctx, `INSERT INTO certificates(
		id, primary_identifier, identifiers_json, challenge, account_id, dns_credential_id,
		state, active_version_id, auto_renew, renew_before_seconds, next_renewal_at,
		retry_count, retry_at, not_before, not_after, last_error_code, created_by,
		request_id, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.PrimaryIdentifier, item.IdentifiersJSON, item.Challenge, item.AccountID,
		nullableCertificateString(string(item.DNSCredentialID)), item.State, item.ActiveVersionID,
		boolInt(item.AutoRenew), item.RenewBeforeSeconds, nullableTime(item.NextRenewalAt), item.RetryCount,
		nullableTime(item.RetryAt), formatTime(item.NotBefore), formatTime(item.NotAfter), item.LastErrorCode,
		item.CreatedBy, item.RequestID, formatTime(item.CreatedAt), formatTime(item.UpdatedAt))
	if err != nil {
		return mapConfigConstraint("insert certificate", err)
	}
	return nil
}

func insertCertificateTaskRecord(ctx context.Context, connection *sql.Conn, task certificate.Task) error {
	var accountStatus certificate.AccountStatus
	if err := connection.QueryRowContext(ctx, `SELECT status FROM certificate_accounts WHERE id = ?`, task.AccountID).
		Scan(&accountStatus); err != nil {
		return err
	}
	if accountStatus != certificate.AccountStatusValid {
		return certificate.ErrACMEAccountInvalid
	}
	if task.DNSCredentialID != "" {
		var credentialStatus certificate.CredentialStatus
		if err := connection.QueryRowContext(ctx, `SELECT status FROM certificate_dns_credentials WHERE id = ?`,
			task.DNSCredentialID).Scan(&credentialStatus); err != nil {
			return err
		}
		if credentialStatus != certificate.CredentialStatusValid {
			return certificate.ErrCloudflarePermission
		}
	}
	_, err := connection.ExecContext(ctx, `INSERT INTO certificate_tasks(
		id, kind, state, stage, plan_id, certificate_id, version_id, account_id,
		dns_credential_id, challenge, release_id, last_error_code, created_by, request_id,
		cancel_requested_at, created_at, updated_at, started_at, finished_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID, task.Kind, task.State, task.Stage, nullableCertificateString(string(task.PlanID)),
		nullableCertificateString(string(task.CertificateID)), nullableCertificateString(string(task.VersionID)),
		nullableCertificateString(string(task.AccountID)), nullableCertificateString(string(task.DNSCredentialID)),
		task.Challenge, task.ReleaseID, task.LastErrorCode, task.CreatedBy, task.RequestID,
		nullableTime(task.CancelRequestedAt), formatTime(task.CreatedAt), formatTime(task.UpdatedAt),
		nullableTime(task.StartedAt), nullableTime(task.FinishedAt))
	if err != nil {
		return mapCertificateTaskConstraint(err)
	}
	return nil
}

func insertCertificateBinding(ctx context.Context, connection *sql.Conn, binding certificate.Binding) error {
	_, err := connection.ExecContext(ctx, `INSERT INTO certificate_bindings(
		id, certificate_id, version_id, config_path, server_start_offset, server_names_json,
		listeners_json, server_fingerprint, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, binding.ID, binding.CertificateID, binding.VersionID,
		binding.ConfigPath, binding.ServerStartOffset, binding.ServerNamesJSON, binding.ListenersJSON,
		binding.ServerFingerprint, formatTime(binding.CreatedAt), formatTime(binding.UpdatedAt))
	if err != nil {
		return mapConfigConstraint("insert certificate binding", err)
	}
	return nil
}

func insertCertificateTaskStage(ctx context.Context, connection *sql.Conn, stage certificate.TaskStage) error {
	if certificate.ValidateTaskStage(stage) != nil {
		return fmt.Errorf("insert certificate task stage: invalid input")
	}
	_, err := connection.ExecContext(ctx, `INSERT INTO certificate_task_stages(
		task_id, sequence, stage, result, code, public_details_json, occurred_at
	) VALUES (?, ?, ?, ?, ?, ?, ?)`, stage.TaskID, stage.Sequence, stage.Stage, stage.Result,
		stage.Code, stage.PublicDetailsJSON, formatTime(stage.OccurredAt))
	if err != nil {
		return mapConfigConstraint("insert certificate task stage", err)
	}
	return nil
}

func (database *DB) certificateTaskStages(ctx context.Context, id certificate.TaskID) ([]certificate.TaskStage, error) {
	rows, err := database.sql.QueryContext(ctx, `SELECT task_id, sequence, stage, result, code,
		public_details_json, occurred_at FROM certificate_task_stages WHERE task_id = ? ORDER BY sequence ASC`, id)
	if err != nil {
		return nil, fmt.Errorf("list certificate task stages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	stages := make([]certificate.TaskStage, 0)
	for rows.Next() {
		var stage certificate.TaskStage
		var occurredAt string
		if err := rows.Scan(&stage.TaskID, &stage.Sequence, &stage.Stage, &stage.Result,
			&stage.Code, &stage.PublicDetailsJSON, &occurredAt); err != nil {
			return nil, fmt.Errorf("list certificate task stages: %w", err)
		}
		stage.OccurredAt, err = parseTime("certificate task stage occurrence", occurredAt)
		if err != nil {
			return nil, err
		}
		stages = append(stages, stage)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list certificate task stages: %w", err)
	}
	return stages, nil
}

func insertCertificateAudit(
	ctx context.Context,
	connection *sql.Conn,
	objectType, objectID, action string,
	actor int64,
	requestID string,
	at time.Time,
	detailValues map[string]any,
) error {
	details, err := json.Marshal(detailValues)
	if err != nil {
		return fmt.Errorf("encode certificate audit: %w", err)
	}
	operation := config.OperationRecord{
		ID:         certificateAuditOperationID(objectType, objectID, action, actor, requestID, at, details),
		ObjectType: objectType, ObjectID: objectID,
		Action: action, Result: "success", RequestID: requestID, OccurredAt: at,
	}
	audit := config.AuditEvent{
		OperationID: operation.ID, OccurredAt: at, ActorUserID: actor, Action: action,
		ObjectType: objectType, ObjectID: objectID, Result: "success", RequestID: requestID,
		DetailsJSON: string(details),
	}
	return insertOperationAndAudit(ctx, connection, operation, audit)
}

func certificateAuditOperationID(
	objectType, objectID, action string,
	actor int64,
	requestID string,
	at time.Time,
	details []byte,
) string {
	payload := fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s\x00%s\x00%s",
		objectType, objectID, action, actor, requestID, formatTime(at), details)
	digest := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("certificate:%x", digest)
}

func mapCertificateTaskConstraint(err error) error {
	mapped := mapConfigConstraint("insert certificate task", err)
	if errors.Is(mapped, config.ErrConflict) {
		return certificate.ErrTaskActive
	}
	return mapped
}

func nullableCertificateString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func parseCertificateID(value string) error {
	_, err := certificate.ParseCertificateID(value)
	return err
}

type certificateScanner interface{ Scan(...any) error }

func scanCertificateAccount(scanner certificateScanner) (certificate.Account, error) {
	var value certificate.Account
	var termsAgreedAt, createdAt, updatedAt string
	if err := scanner.Scan(&value.ID, &value.Environment, &value.DirectoryURL, &value.URI,
		&value.Email, &value.Status, &value.TermsURL, &termsAgreedAt, &value.TermsAgreedBy,
		&value.CreatedBy, &value.RequestID, &createdAt, &updatedAt); err != nil {
		return certificate.Account{}, err
	}
	var err error
	if value.TermsAgreedAt, err = parseTime("certificate account terms agreement", termsAgreedAt); err != nil {
		return certificate.Account{}, err
	}
	if value.CreatedAt, err = parseTime("certificate account creation", createdAt); err != nil {
		return certificate.Account{}, err
	}
	if value.UpdatedAt, err = parseTime("certificate account update", updatedAt); err != nil {
		return certificate.Account{}, err
	}
	return value, nil
}

func scanCertificateDNSCredential(scanner certificateScanner) (certificate.DNSCredential, error) {
	var value certificate.DNSCredential
	var verifiedAt, createdAt, updatedAt string
	var lastUsedAt sql.NullString
	if err := scanner.Scan(&value.ID, &value.Name, &value.Provider, &value.Fingerprint, &value.Status,
		&verifiedAt, &lastUsedAt, &value.CreatedBy, &value.RequestID, &createdAt, &updatedAt); err != nil {
		return certificate.DNSCredential{}, err
	}
	var err error
	if value.VerifiedAt, err = parseTime("certificate credential verification", verifiedAt); err != nil {
		return certificate.DNSCredential{}, err
	}
	if value.LastUsedAt, err = parseNullableCertificateTime("certificate credential use", lastUsedAt); err != nil {
		return certificate.DNSCredential{}, err
	}
	if value.CreatedAt, err = parseTime("certificate credential creation", createdAt); err != nil {
		return certificate.DNSCredential{}, err
	}
	if value.UpdatedAt, err = parseTime("certificate credential update", updatedAt); err != nil {
		return certificate.DNSCredential{}, err
	}
	return value, nil
}

func scanCertificateOrderPlan(scanner certificateScanner) (certificate.OrderPlan, error) {
	var value certificate.OrderPlan
	var stagingAccountID, credentialID, certificateID, versionID sql.NullString
	var productionDigest []byte
	var stagingEvidence, riskConfirm int
	var expiresAt, createdAt string
	var executedAt sql.NullString
	if err := scanner.Scan(&value.ID, &value.State, &value.Environment, &value.Challenge,
		&value.AccountID, &stagingAccountID, &credentialID, &certificateID, &versionID,
		&value.PrimaryIdentifier, &value.IdentifiersJSON, &value.ServerRefsJSON, &productionDigest,
		&value.BindingDiffJSON, &stagingEvidence, &riskConfirm, &expiresAt, &value.CreatedBy,
		&value.RequestID, &createdAt, &executedAt); err != nil {
		return certificate.OrderPlan{}, err
	}
	if len(productionDigest) != len(value.ProductionDigest) {
		return certificate.OrderPlan{}, fmt.Errorf("scan certificate order plan: invalid production digest")
	}
	copy(value.ProductionDigest[:], productionDigest)
	value.StagingAccountID = certificate.AccountID(stagingAccountID.String)
	value.DNSCredentialID = certificate.DNSCredentialID(credentialID.String)
	value.CertificateID = certificate.CertificateID(certificateID.String)
	value.VersionID = certificate.VersionID(versionID.String)
	value.StagingEvidence = stagingEvidence != 0
	value.RequiresRiskConfirm = riskConfirm != 0
	var err error
	if value.ExpiresAt, err = parseTime("certificate plan expiry", expiresAt); err != nil {
		return certificate.OrderPlan{}, err
	}
	if value.CreatedAt, err = parseTime("certificate plan creation", createdAt); err != nil {
		return certificate.OrderPlan{}, err
	}
	if value.ExecutedAt, err = parseNullableCertificateTime("certificate plan execution", executedAt); err != nil {
		return certificate.OrderPlan{}, err
	}
	return value, nil
}

func scanCertificateBindingPlan(scanner certificateScanner) (certificate.BindingPlan, error) {
	var value certificate.BindingPlan
	var productionDigest []byte
	var expiresAt, createdAt string
	var executedAt sql.NullString
	if err := scanner.Scan(&value.ID, &value.State, &value.CertificateID, &value.VersionID,
		&value.ServerRefsJSON, &productionDigest, &value.BindingDiffJSON, &expiresAt,
		&value.CreatedBy, &value.RequestID, &createdAt, &executedAt); err != nil {
		return certificate.BindingPlan{}, err
	}
	if len(productionDigest) != len(value.ProductionDigest) {
		return certificate.BindingPlan{}, fmt.Errorf("scan certificate binding plan: invalid production digest")
	}
	copy(value.ProductionDigest[:], productionDigest)
	var err error
	if value.ExpiresAt, err = parseTime("certificate binding plan expiry", expiresAt); err != nil {
		return certificate.BindingPlan{}, err
	}
	if value.CreatedAt, err = parseTime("certificate binding plan creation", createdAt); err != nil {
		return certificate.BindingPlan{}, err
	}
	if value.ExecutedAt, err = parseNullableCertificateTime("certificate binding plan execution", executedAt); err != nil {
		return certificate.BindingPlan{}, err
	}
	return value, nil
}

func scanCertificate(scanner certificateScanner) (certificate.Certificate, error) {
	var value certificate.Certificate
	var credentialID, activeVersionID sql.NullString
	var autoRenew int
	var nextRenewalAt, retryAt sql.NullString
	var notBefore, notAfter, createdAt, updatedAt string
	if err := scanner.Scan(&value.ID, &value.PrimaryIdentifier, &value.IdentifiersJSON, &value.Challenge,
		&value.AccountID, &credentialID, &value.State, &activeVersionID, &autoRenew,
		&value.RenewBeforeSeconds, &nextRenewalAt, &value.RetryCount, &retryAt, &notBefore,
		&notAfter, &value.LastErrorCode, &value.CreatedBy, &value.RequestID, &createdAt, &updatedAt); err != nil {
		return certificate.Certificate{}, err
	}
	value.DNSCredentialID = certificate.DNSCredentialID(credentialID.String)
	value.ActiveVersionID = certificate.VersionID(activeVersionID.String)
	value.AutoRenew = autoRenew != 0
	var err error
	if value.NextRenewalAt, err = parseNullableCertificateTime("certificate next renewal", nextRenewalAt); err != nil {
		return certificate.Certificate{}, err
	}
	if value.RetryAt, err = parseNullableCertificateTime("certificate retry", retryAt); err != nil {
		return certificate.Certificate{}, err
	}
	if value.NotBefore, err = parseTime("certificate not before", notBefore); err != nil {
		return certificate.Certificate{}, err
	}
	if value.NotAfter, err = parseTime("certificate not after", notAfter); err != nil {
		return certificate.Certificate{}, err
	}
	if value.CreatedAt, err = parseTime("certificate creation", createdAt); err != nil {
		return certificate.Certificate{}, err
	}
	if value.UpdatedAt, err = parseTime("certificate update", updatedAt); err != nil {
		return certificate.Certificate{}, err
	}
	return value, nil
}

func scanCertificateVersion(scanner certificateScanner) (certificate.Version, error) {
	var value certificate.Version
	var notBefore, notAfter, createdAt string
	if err := scanner.Scan(&value.ID, &value.CertificateID, &value.State, &value.FullchainDigest,
		&value.PrivateKeyDigest, &value.LeafFingerprint, &value.SerialNumber, &value.Issuer,
		&notBefore, &notAfter, &createdAt); err != nil {
		return certificate.Version{}, err
	}
	var err error
	if value.NotBefore, err = parseTime("certificate version not before", notBefore); err != nil {
		return certificate.Version{}, err
	}
	if value.NotAfter, err = parseTime("certificate version not after", notAfter); err != nil {
		return certificate.Version{}, err
	}
	if value.CreatedAt, err = parseTime("certificate version creation", createdAt); err != nil {
		return certificate.Version{}, err
	}
	return value, nil
}

func scanCertificateBinding(scanner certificateScanner) (certificate.Binding, error) {
	var value certificate.Binding
	var createdAt, updatedAt string
	if err := scanner.Scan(&value.ID, &value.CertificateID, &value.VersionID, &value.ConfigPath,
		&value.ServerStartOffset, &value.ServerNamesJSON, &value.ListenersJSON, &value.ServerFingerprint,
		&createdAt, &updatedAt); err != nil {
		return certificate.Binding{}, err
	}
	var err error
	if value.CreatedAt, err = parseTime("certificate binding creation", createdAt); err != nil {
		return certificate.Binding{}, err
	}
	if value.UpdatedAt, err = parseTime("certificate binding update", updatedAt); err != nil {
		return certificate.Binding{}, err
	}
	return value, nil
}

func scanCertificateTask(scanner certificateScanner) (certificate.Task, error) {
	var value certificate.Task
	var planID, certificateID, versionID, accountID, credentialID sql.NullString
	var cancelAt, startedAt, finishedAt sql.NullString
	var createdAt, updatedAt string
	if err := scanner.Scan(&value.ID, &value.Kind, &value.State, &value.Stage, &planID,
		&certificateID, &versionID, &accountID, &credentialID, &value.Challenge, &value.ReleaseID,
		&value.LastErrorCode, &value.CreatedBy, &value.RequestID, &cancelAt, &createdAt,
		&updatedAt, &startedAt, &finishedAt); err != nil {
		return certificate.Task{}, err
	}
	value.PlanID = certificate.OrderPlanID(planID.String)
	value.CertificateID = certificate.CertificateID(certificateID.String)
	value.VersionID = certificate.VersionID(versionID.String)
	value.AccountID = certificate.AccountID(accountID.String)
	value.DNSCredentialID = certificate.DNSCredentialID(credentialID.String)
	var err error
	if value.CancelRequestedAt, err = parseNullableCertificateTime("certificate task cancellation", cancelAt); err != nil {
		return certificate.Task{}, err
	}
	if value.CreatedAt, err = parseTime("certificate task creation", createdAt); err != nil {
		return certificate.Task{}, err
	}
	if value.UpdatedAt, err = parseTime("certificate task update", updatedAt); err != nil {
		return certificate.Task{}, err
	}
	if value.StartedAt, err = parseNullableCertificateTime("certificate task start", startedAt); err != nil {
		return certificate.Task{}, err
	}
	if value.FinishedAt, err = parseNullableCertificateTime("certificate task finish", finishedAt); err != nil {
		return certificate.Task{}, err
	}
	return value, nil
}

func scanCertificateArtifact(scanner certificateScanner) (certificate.ChallengeArtifact, error) {
	var value certificate.ChallengeArtifact
	var credentialID sql.NullString
	var createdAt, updatedAt string
	if err := scanner.Scan(&value.ID, &value.TaskID, &value.Kind, &value.State, &credentialID,
		&value.ZoneID, &value.RecordID, &value.RecordName, &value.ConfigPath, &createdAt, &updatedAt); err != nil {
		return certificate.ChallengeArtifact{}, err
	}
	value.DNSCredentialID = certificate.DNSCredentialID(credentialID.String)
	var err error
	if value.CreatedAt, err = parseTime("certificate artifact creation", createdAt); err != nil {
		return certificate.ChallengeArtifact{}, err
	}
	if value.UpdatedAt, err = parseTime("certificate artifact update", updatedAt); err != nil {
		return certificate.ChallengeArtifact{}, err
	}
	return value, nil
}

func parseNullableCertificateTime(label string, value sql.NullString) (time.Time, error) {
	if !value.Valid {
		return time.Time{}, nil
	}
	return parseTime(label, value.String)
}
