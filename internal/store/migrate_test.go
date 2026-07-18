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

	assertTables(t, database, "config_workspaces", "config_group_collection", "config_groups", "config_group_members")
	assertV1UserAndSession(t, database)
	if got := migrationVersions(t, database); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("versions = %v, want [1 2]", got)
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
	if count != 2 {
		t.Fatalf("migration count = %d, want 2", count)
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
		"migrations/0003_broken.sql": {Data: []byte(`
			CREATE TABLE migration_probe(id INTEGER PRIMARY KEY);
			INSERT INTO table_that_does_not_exist(id) VALUES (1);
		`)},
	}

	if err := database.migrate(context.Background(), broken); err == nil {
		t.Fatal("migrate() error = nil, want failed migration")
	}

	for query, label := range map[string]string{
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'migration_probe'": "probe table",
		"SELECT COUNT(*) FROM schema_migrations WHERE version = 3":                             "migration row",
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
