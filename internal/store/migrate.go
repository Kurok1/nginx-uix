/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrMigrationChecksum indicates that an already-applied migration changed.
var ErrMigrationChecksum = errors.New("migration checksum mismatch")

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// Migrate applies the embedded migrations in monotonically increasing order.
func (d *DB) Migrate(ctx context.Context) error {
	return d.migrate(ctx, embeddedMigrations)
}

func (d *DB) migrate(ctx context.Context, migrationFS fs.FS) error {
	if _, err := d.sql.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			checksum BLOB NOT NULL CHECK(length(checksum) = 32),
			applied_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("create schema migrations table: %w", err)
	}

	paths, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(paths)

	previousVersion := 0
	for _, path := range paths {
		version, err := migrationVersion(path)
		if err != nil {
			return err
		}
		if version <= previousVersion {
			return fmt.Errorf("validate migration order: version %d is not monotonic", version)
		}
		previousVersion = version

		contents, err := fs.ReadFile(migrationFS, path)
		if err != nil {
			return fmt.Errorf("read migration %q: %w", path, err)
		}
		checksum := sha256.Sum256(contents)
		applied, err := d.migrationApplied(ctx, version, checksum[:])
		if err != nil {
			return fmt.Errorf("inspect migration %d: %w", version, err)
		}
		if applied {
			continue
		}
		if err := d.applyMigration(ctx, version, filepath.Base(path), checksum[:], contents); err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) migrationApplied(ctx context.Context, version int, checksum []byte) (bool, error) {
	var existing []byte
	err := d.sql.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE version = ?", version).Scan(&existing)
	switch {
	case err == nil:
		if !bytes.Equal(existing, checksum) {
			return false, fmt.Errorf("%w: version %d", ErrMigrationChecksum, version)
		}
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, err
	}
}

func (d *DB) applyMigration(ctx context.Context, version int, name string, checksum, contents []byte) (returnErr error) {
	connection, err := d.sql.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer func() {
		if err := connection.Close(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("close migration connection: %w", err)
		}
	}()

	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin migration %d: %w", version, err)
	}
	committed := false
	defer func() {
		if !committed {
			rollbackContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer cancel()
			// Rollback is best effort; the original migration error remains authoritative.
			_, _ = connection.ExecContext(rollbackContext, "ROLLBACK")
		}
	}()

	if _, err := connection.ExecContext(ctx, string(contents)); err != nil {
		return fmt.Errorf("execute migration %d: %w", version, err)
	}
	if _, err := connection.ExecContext(
		ctx,
		"INSERT INTO schema_migrations(version, name, checksum, applied_at) VALUES (?, ?, ?, ?)",
		version,
		name,
		checksum,
		time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("record migration %d: %w", version, err)
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit migration %d: %w", version, err)
	}
	committed = true
	return nil
}

func migrationVersion(path string) (int, error) {
	base := filepath.Base(path)
	prefix, _, found := strings.Cut(base, "_")
	if !found {
		return 0, fmt.Errorf("parse migration %q: numeric prefix required", base)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("parse migration %q: positive numeric prefix required", base)
	}
	return version, nil
}
