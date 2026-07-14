/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenMigratesEmptyDatabase(t *testing.T) {
	t.Parallel()

	directory := secureTempDir(t)
	path := filepath.Join(directory, "nginx-uix.db")
	database, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	for _, table := range []string{"schema_migrations", "users", "sessions", "login_throttles", "audit_events"} {
		var count int
		if err := database.sql.QueryRowContext(
			context.Background(),
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
			table,
		).Scan(&count); err != nil {
			t.Fatalf("query table %q: %v", table, err)
		}
		if count != 1 {
			t.Errorf("table %q count = %d, want 1", table, count)
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got&0o077 != 0 {
		t.Fatalf("database mode = %04o, want no group/other bits", got)
	}
}

func TestOpenRejectsUnsafeDatabasePaths(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T) string
	}{
		{
			name: "relative path",
			prepare: func(_ *testing.T) string {
				return "relative.db"
			},
		},
		{
			name: "permissive directory",
			prepare: func(t *testing.T) string {
				directory := t.TempDir()
				if err := os.Chmod(directory, 0o755); err != nil {
					t.Fatalf("Chmod() error = %v", err)
				}
				return filepath.Join(directory, "nginx-uix.db")
			},
		},
		{
			name: "permissive database file",
			prepare: func(t *testing.T) string {
				directory := secureTempDir(t)
				path := filepath.Join(directory, "nginx-uix.db")
				if err := os.WriteFile(path, nil, 0o644); err != nil {
					t.Fatalf("WriteFile() error = %v", err)
				}
				return path
			},
		},
		{
			name: "database symlink",
			prepare: func(t *testing.T) string {
				directory := secureTempDir(t)
				target := filepath.Join(directory, "target.db")
				if err := os.WriteFile(target, nil, 0o600); err != nil {
					t.Fatalf("WriteFile() error = %v", err)
				}
				path := filepath.Join(directory, "nginx-uix.db")
				if err := os.Symlink(target, path); err != nil {
					t.Fatalf("Symlink() error = %v", err)
				}
				return path
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if database, err := Open(context.Background(), test.prepare(t)); err == nil {
				_ = database.Close()
				t.Fatal("Open() error = nil, want unsafe path rejection")
			}
		})
	}
}

func secureTempDir(t *testing.T) string {
	t.Helper()

	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("Chmod(temp directory) error = %v", err)
	}
	return directory
}
