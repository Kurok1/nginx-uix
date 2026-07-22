/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func TestMigrateAddsConfigSchemaWithoutChangingV1Data(t *testing.T) {
	database := openV1Fixture(t)
	insertV1UserAndSession(t, database)

	if err := database.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	assertTables(t, database,
		"config_workspaces", "config_group_collection", "config_groups", "config_group_members",
		"config_publish_checks", "config_releases", "config_release_stages", "config_backups",
		"config_production_lease", "config_restores", "config_restore_stages", "config_restarts",
		"config_restart_stages", "config_retention_runs", "config_retention_items", "config_attention_cases",
		"config_verifications", "route_lab_runs", "route_lab_stages", "certificate_accounts",
		"certificate_dns_credentials", "certificate_order_plans", "certificates", "certificate_versions",
		"certificate_bindings", "certificate_binding_plans", "certificate_tasks", "certificate_task_stages",
		"certificate_challenge_artifacts",
	)
	assertV1UserAndSession(t, database)
	if got := migrationVersions(t, database); !reflect.DeepEqual(got, []int{1, 2, 3, 4, 5, 6, 7}) {
		t.Fatalf("versions = %v, want [1 2 3 4 5 6 7]", got)
	}
}

func TestMigrateV021WorkspaceDataIntoReleaseSchema(t *testing.T) {
	path := filepath.Join(secureTempDir(t), "v021.db")
	connection, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	database := &DB{sql: connection}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	first, err := fs.ReadFile(embeddedMigrations, "migrations/0001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	second, err := fs.ReadFile(embeddedMigrations, "migrations/0002_auth_cleanup_indexes.sql")
	if err != nil {
		t.Fatal(err)
	}
	third, err := fs.ReadFile(embeddedMigrations, "migrations/0003_config_workspaces.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.migrate(context.Background(), fstest.MapFS{
		"migrations/0001_initial.sql":              {Data: first},
		"migrations/0002_auth_cleanup_indexes.sql": {Data: second},
		"migrations/0003_config_workspaces.sql":    {Data: third},
	}); err != nil {
		t.Fatalf("create v0.2.1 fixture: %v", err)
	}
	digest := bytes.Repeat([]byte{0x44}, 32)
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users(id, username, normalized_name, password_hash, disabled, created_at)
			VALUES (41, 'operator', 'operator', 'hash', 0, '2026-07-17T01:00:00Z')`, nil},
		{`INSERT INTO config_workspaces(
			id, name, state, state_reason_code, production_digest, base_digest, draft_digest,
			manifest_version, policy_version, entry_count, managed_bytes, workspace_bytes,
			revision, created_by, created_at, updated_at
		) VALUES ('11111111111111111111111111111111', 'Production review', 'ready', '', ?, ?, ?,
			1, 1, 2, 128, 256, 7, 41, '2026-07-17T01:01:00Z', '2026-07-17T01:02:00Z')`, []any{digest, digest, digest}},
		{`INSERT INTO config_groups(id, name, normalized_name, sort_order, created_by, created_at, updated_at)
			VALUES ('22222222222222222222222222222222', 'Entry points', 'entry points', 10, 41,
			'2026-07-17T01:03:00Z', '2026-07-17T01:03:00Z')`, nil},
		{`INSERT INTO config_group_members(group_id, ordinal, path)
			VALUES ('22222222222222222222222222222222', 0, 'nginx.conf')`, nil},
		{`INSERT INTO config_operations(id, object_type, object_id, action, result, request_id, occurred_at)
			VALUES ('legacy-operation', 'config_workspace', '11111111111111111111111111111111',
			'config.workspace.create', 'success', 'legacy-request', '2026-07-17T01:01:00Z')`, nil},
		{`INSERT INTO audit_events(
			occurred_at, actor_user_id, action, object_type, object_id, result, request_id, details_json, operation_id
		) VALUES ('2026-07-17T01:01:00Z', 41, 'config.workspace.create', 'config_workspace',
			'11111111111111111111111111111111', 'success', 'legacy-request', '{}', 'legacy-operation')`, nil},
	}
	for _, statement := range statements {
		if _, err := database.sql.ExecContext(context.Background(), statement.query, statement.args...); err != nil {
			t.Fatalf("seed v0.2.1 fixture: %v", err)
		}
	}

	if err := database.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	var name, state string
	var revision int
	var lastRelease sql.NullString
	if err := database.sql.QueryRowContext(context.Background(), `SELECT name, state, revision, last_release_id
		FROM config_workspaces WHERE id = '11111111111111111111111111111111'`).Scan(
		&name, &state, &revision, &lastRelease,
	); err != nil {
		t.Fatal(err)
	}
	if name != "Production review" || state != "ready" || revision != 7 || lastRelease.Valid {
		t.Fatalf("migrated workspace = %q/%q/%d/%v", name, state, revision, lastRelease)
	}
	for query, want := range map[string]int{
		"SELECT COUNT(*) FROM config_groups WHERE id = '22222222222222222222222222222222'":              1,
		"SELECT COUNT(*) FROM config_group_members WHERE group_id = '22222222222222222222222222222222'": 1,
		"SELECT COUNT(*) FROM config_operations WHERE id = 'legacy-operation'":                          1,
		"SELECT COUNT(*) FROM audit_events WHERE operation_id = 'legacy-operation'":                     1,
	} {
		var count int
		if err := database.sql.QueryRowContext(context.Background(), query).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("query %q count = %d, want %d", query, count, want)
		}
	}
	rows, err := database.sql.QueryContext(context.Background(), "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("foreign_key_check reported a violation after v0.2.1 migration")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateV022RecoveryEvidenceIntoControlSchema(t *testing.T) {
	database := openMigrationFixture(t, 4)
	digest := bytes.Repeat([]byte{0x55}, 32)
	workspaceID := "11111111111111111111111111111111"
	checkID := "22222222222222222222222222222222"
	releaseID := "33333333333333333333333333333333"
	backupID := "44444444444444444444444444444444"
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users(id, username, normalized_name, password_hash, disabled, created_at)
			VALUES (41, 'operator', 'operator', 'hash', 0, '2026-07-18T01:00:00Z')`, nil},
		{`INSERT INTO config_workspaces(
			id, name, state, state_reason_code, production_digest, base_digest, draft_digest,
			manifest_version, policy_version, entry_count, managed_bytes, workspace_bytes,
			revision, last_release_id, created_by, created_at, updated_at
		) VALUES (?, 'Production recovery', 'needs_attention', 'rollback_health_failed', ?, ?, ?,
			1, 1, 2, 128, 256, 7, NULL, 41, '2026-07-18T01:01:00Z', '2026-07-18T01:02:00Z')`,
			[]any{workspaceID, digest, digest, digest}},
		{`INSERT INTO config_publish_checks(
			id, workspace_id, workspace_revision, production_digest, base_digest, draft_digest,
			candidate_digest, manifest_version, policy_version, validator_version, validator_build_id,
			state, diagnostic_count, public_details_json, created_by, request_id, started_at, finished_at, expires_at
		) VALUES (?, ?, 7, ?, ?, ?, ?, 1, 1, 1, 'build-v1', 'valid', 0, '[]', 41,
			'check-request', '2026-07-18T01:03:00Z', '2026-07-18T01:03:01Z', '2026-07-18T01:13:01Z')`,
			[]any{checkID, workspaceID, digest, digest, digest, digest}},
		{`INSERT INTO config_releases(
			id, workspace_id, check_id, backup_id, state, stage, production_digest, draft_digest,
			candidate_digest, last_error_code, created_by, request_id, created_at, updated_at, finished_at
		) VALUES (?, ?, ?, ?, 'needs_attention', 'needs_attention', ?, ?, ?, 'rollback_health_failed',
			41, 'release-request', '2026-07-18T01:04:00Z', '2026-07-18T01:05:00Z', '2026-07-18T01:05:00Z')`,
			[]any{releaseID, workspaceID, checkID, backupID, digest, digest, digest}},
		{`INSERT INTO config_backups(id, release_id, state, entry_count, total_bytes, created_at, verified_at)
			VALUES (?, ?, 'complete', 7, 2048, '2026-07-18T01:04:10Z', '2026-07-18T01:04:20Z')`,
			[]any{backupID, releaseID}},
	}
	for _, statement := range statements {
		if _, err := database.sql.ExecContext(context.Background(), statement.query, statement.args...); err != nil {
			t.Fatalf("seed v0.2.2 fixture: %v", err)
		}
	}

	if err := database.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	var originType, originID, state string
	var productionDigest []byte
	var manuallyProtected, bodyPresent int
	if err := database.sql.QueryRowContext(context.Background(), `SELECT origin_type, origin_id,
		production_digest, state, manually_protected, body_present FROM config_backups WHERE id = ?`, backupID).Scan(
		&originType, &originID, &productionDigest, &state, &manuallyProtected, &bodyPresent,
	); err != nil {
		t.Fatalf("read migrated backup: %v", err)
	}
	if originType != "release" || originID != releaseID || !bytes.Equal(productionDigest, digest) ||
		state != "complete" || manuallyProtected != 0 || bodyPresent != 1 {
		t.Fatalf("migrated backup = %q/%q/%x/%q/%d/%d", originType, originID,
			productionDigest, state, manuallyProtected, bodyPresent)
	}
	var caseState, subjectType, subjectID, reasonCode string
	if err := database.sql.QueryRowContext(context.Background(), `SELECT state, subject_type, subject_id, reason_code
		FROM config_attention_cases WHERE workspace_id = ?`, workspaceID).Scan(
		&caseState, &subjectType, &subjectID, &reasonCode,
	); err != nil {
		t.Fatalf("read migrated attention case: %v", err)
	}
	if caseState != "open" || subjectType != "release" || subjectID != releaseID || reasonCode != "rollback_health_failed" {
		t.Fatalf("migrated attention case = %q/%q/%q/%q", caseState, subjectType, subjectID, reasonCode)
	}
	var leaseOwnerType, leaseOwnerID sql.NullString
	if err := database.sql.QueryRowContext(context.Background(), `SELECT owner_type, owner_id
		FROM config_production_lease WHERE singleton = 1`).Scan(&leaseOwnerType, &leaseOwnerID); err != nil {
		t.Fatalf("read production lease: %v", err)
	}
	if leaseOwnerType.Valid || leaseOwnerID.Valid {
		t.Fatalf("initial production lease = %v/%v, want empty", leaseOwnerType, leaseOwnerID)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(secureTempDir(t), "nginx-uix.db")
	database, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	database, err = Open(context.Background(), path)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	var count int
	if err := database.sql.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != 7 {
		t.Fatalf("migration count = %d, want 7", count)
	}
}

func TestMigrateCreatesAuthCleanupIndexes(t *testing.T) {
	t.Parallel()

	database := openTestDatabase(t)
	for _, index := range []string{
		"sessions_idle_expiration_idx",
		"sessions_absolute_expiration_idx",
		"login_throttles_window_expiration_idx",
	} {
		var count int
		if err := database.sql.QueryRowContext(
			context.Background(),
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?",
			index,
		).Scan(&count); err != nil {
			t.Fatalf("query index %q: %v", index, err)
		}
		if count != 1 {
			t.Errorf("index %q count = %d, want 1", index, count)
		}
	}
}

func TestAuthCleanupQueriesUseExpirationIndexes(t *testing.T) {
	t.Parallel()

	database := openTestDatabase(t)
	tests := []struct {
		name        string
		query       string
		arguments   []any
		wantIndexes []string
	}{
		{
			name: "sessions",
			query: `EXPLAIN QUERY PLAN
				DELETE FROM sessions
				WHERE julianday(idle_expires_at) <= julianday(?)
				   OR julianday(absolute_expires_at) <= julianday(?)`,
			arguments: []any{"2026-07-15T12:00:00Z", "2026-07-15T12:00:00Z"},
			wantIndexes: []string{
				"sessions_idle_expiration_idx",
				"sessions_absolute_expiration_idx",
			},
		},
		{
			name: "login throttles",
			query: `EXPLAIN QUERY PLAN
				DELETE FROM login_throttles
				WHERE julianday(window_started_at) <= julianday(?)
				  AND (blocked_until IS NULL OR julianday(blocked_until) <= julianday(?))`,
			arguments:   []any{"2026-07-15T11:55:00Z", "2026-07-15T12:00:00Z"},
			wantIndexes: []string{"login_throttles_window_expiration_idx"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rows, err := database.sql.QueryContext(context.Background(), test.query, test.arguments...)
			if err != nil {
				t.Fatalf("EXPLAIN QUERY PLAN error = %v", err)
			}
			defer func() {
				if err := rows.Close(); err != nil {
					t.Errorf("Close(rows) error = %v", err)
				}
			}()

			var details strings.Builder
			for rows.Next() {
				var identifier, parent, unused int
				var detail string
				if err := rows.Scan(&identifier, &parent, &unused, &detail); err != nil {
					t.Fatalf("Scan(query plan) error = %v", err)
				}
				details.WriteString(detail)
				details.WriteByte('\n')
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("iterate query plan: %v", err)
			}
			for _, index := range test.wantIndexes {
				if !strings.Contains(details.String(), index) {
					t.Errorf("query plan = %q, want index %q", details.String(), index)
				}
			}
		})
	}
}

func TestMigrateRejectsChangedChecksum(t *testing.T) {
	t.Parallel()

	database := openTestDatabase(t)
	changed := fstest.MapFS{
		"migrations/0001_initial.sql": {Data: []byte("CREATE TABLE changed_schema(id INTEGER PRIMARY KEY);")},
	}

	err := database.migrate(context.Background(), changed)
	if !errors.Is(err, ErrMigrationChecksum) {
		t.Fatalf("migrate() error = %v, want ErrMigrationChecksum", err)
	}
}

func TestMigrateRollsBackFailedMigration(t *testing.T) {
	t.Parallel()

	database := openTestDatabase(t)
	broken := fstest.MapFS{
		"migrations/0008_broken.sql": {Data: []byte(`
			CREATE TABLE migration_probe(id INTEGER PRIMARY KEY);
			INSERT INTO table_that_does_not_exist(id) VALUES (1);
		`)},
	}

	if err := database.migrate(context.Background(), broken); err == nil {
		t.Fatal("migrate() error = nil, want failed migration")
	}

	for query, label := range map[string]string{
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'migration_probe'": "probe table",
		"SELECT COUNT(*) FROM schema_migrations WHERE version = 8":                             "migration row",
	} {
		var count int
		if err := database.sql.QueryRowContext(context.Background(), query).Scan(&count); err != nil {
			t.Fatalf("query %s: %v", label, err)
		}
		if count != 0 {
			t.Errorf("%s count = %d, want 0 after rollback", label, count)
		}
	}
}

func TestMigrateDoesNotCreateNginxConfigBodyColumns(t *testing.T) {
	t.Parallel()

	database := openTestDatabase(t)
	rows, err := database.sql.QueryContext(context.Background(), `
		SELECT m.name, p.name
		FROM sqlite_master AS m, pragma_table_info(m.name) AS p
		WHERE m.type = 'table'
		ORDER BY m.name, p.cid
	`)
	if err != nil {
		t.Fatalf("query schema columns: %v", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("Close(rows) error = %v", err)
		}
	}()

	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatalf("Scan() error = %v", err)
		}
		normalized := strings.ToLower(column)
		if strings.Contains(normalized, "nginx") || strings.Contains(normalized, "config_body") {
			t.Errorf("forbidden config column %s.%s", table, column)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate schema columns: %v", err)
	}
}

func openTestDatabase(t *testing.T) *DB {
	t.Helper()

	database, err := Open(context.Background(), filepath.Join(secureTempDir(t), "nginx-uix.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return database
}

func openV1Fixture(t *testing.T) *DB {
	t.Helper()

	directory := secureTempDir(t)
	sourcePath := filepath.Join(directory, "v1-source.db")
	sourceConnection, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		t.Fatalf("open v1 source database: %v", err)
	}
	source := &DB{sql: sourceConnection}
	initialMigration, err := fs.ReadFile(embeddedMigrations, "migrations/0001_initial.sql")
	if err != nil {
		t.Fatalf("read initial migration: %v", err)
	}
	if err := source.migrate(context.Background(), fstest.MapFS{
		"migrations/0001_initial.sql": {Data: initialMigration},
	}); err != nil {
		t.Fatalf("create v1 source database: %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatalf("close v1 source database: %v", err)
	}

	contents, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read v1 source database: %v", err)
	}
	fixturePath := filepath.Join(directory, "v1-fixture.db")
	if err := os.WriteFile(fixturePath, contents, 0o600); err != nil {
		t.Fatalf("copy v1 fixture database: %v", err)
	}
	connection, err := sql.Open("sqlite", "file:"+fixturePath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open copied v1 fixture: %v", err)
	}
	database := &DB{sql: connection}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return database
}

func openMigrationFixture(t *testing.T, maximumVersion int) *DB {
	t.Helper()
	path := filepath.Join(secureTempDir(t), "migration-fixture.db")
	connection, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open migration fixture: %v", err)
	}
	database := &DB{sql: connection}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	files := make(fstest.MapFS, maximumVersion)
	for version := 1; version <= maximumVersion; version++ {
		matches, err := fs.Glob(embeddedMigrations, fmt.Sprintf("migrations/%04d_*.sql", version))
		if err != nil || len(matches) != 1 {
			t.Fatalf("find migration %d: matches=%v error=%v", version, matches, err)
		}
		payload, err := fs.ReadFile(embeddedMigrations, matches[0])
		if err != nil {
			t.Fatalf("read migration %d: %v", version, err)
		}
		files[matches[0]] = &fstest.MapFile{Data: payload}
	}
	if err := database.migrate(context.Background(), files); err != nil {
		t.Fatalf("create migration fixture through %d: %v", maximumVersion, err)
	}
	return database
}

func insertV1UserAndSession(t *testing.T, database *DB) {
	t.Helper()

	if _, err := database.sql.ExecContext(
		context.Background(),
		`INSERT INTO users(id, username, normalized_name, password_hash, disabled, created_at)
		 VALUES (41, 'operator', 'operator', 'argon2id-hash', 0, '2026-07-16T01:02:03Z')`,
	); err != nil {
		t.Fatalf("insert v1 user: %v", err)
	}
	if _, err := database.sql.ExecContext(
		context.Background(),
		`INSERT INTO sessions(
			token_digest, user_id, csrf_digest, created_at, last_seen_at, idle_expires_at, absolute_expires_at
		 ) VALUES (?, 41, ?, '2026-07-16T01:02:03Z', '2026-07-16T01:02:04Z',
			'2026-07-16T02:02:04Z', '2026-07-17T01:02:03Z')`,
		bytes.Repeat([]byte{0x11}, 32),
		bytes.Repeat([]byte{0x22}, 32),
	); err != nil {
		t.Fatalf("insert v1 session: %v", err)
	}
}

func assertTables(t *testing.T, database *DB, names ...string) {
	t.Helper()

	for _, name := range names {
		var count int
		if err := database.sql.QueryRowContext(
			context.Background(),
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
			name,
		).Scan(&count); err != nil {
			t.Fatalf("query table %q: %v", name, err)
		}
		if count != 1 {
			t.Fatalf("table %q count = %d, want 1", name, count)
		}
	}
}

func assertV1UserAndSession(t *testing.T, database *DB) {
	t.Helper()

	var username, passwordHash string
	if err := database.sql.QueryRowContext(
		context.Background(),
		"SELECT username, password_hash FROM users WHERE id = 41",
	).Scan(&username, &passwordHash); err != nil {
		t.Fatalf("read v1 user after migration: %v", err)
	}
	if username != "operator" || passwordHash != "argon2id-hash" {
		t.Fatalf("v1 user = %q/%q, want operator/argon2id-hash", username, passwordHash)
	}

	var userID int64
	var tokenDigest, csrfDigest []byte
	if err := database.sql.QueryRowContext(
		context.Background(),
		"SELECT user_id, token_digest, csrf_digest FROM sessions WHERE user_id = 41",
	).Scan(&userID, &tokenDigest, &csrfDigest); err != nil {
		t.Fatalf("read v1 session after migration: %v", err)
	}
	if userID != 41 || !bytes.Equal(tokenDigest, bytes.Repeat([]byte{0x11}, 32)) ||
		!bytes.Equal(csrfDigest, bytes.Repeat([]byte{0x22}, 32)) {
		t.Fatalf("v1 session was not preserved")
	}
}

func migrationVersions(t *testing.T, database *DB) []int {
	t.Helper()

	rows, err := database.sql.QueryContext(
		context.Background(),
		"SELECT version FROM schema_migrations ORDER BY version",
	)
	if err != nil {
		t.Fatalf("query migration versions: %v", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("Close(rows) error = %v", err)
		}
	}()

	var versions []int
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			t.Fatalf("scan migration version: %v", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migration versions: %v", err)
	}
	return versions
}
