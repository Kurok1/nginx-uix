/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

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
