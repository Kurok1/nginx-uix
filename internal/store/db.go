/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	// Register the CGo-free SQLite database/sql driver used by Open.
	_ "modernc.org/sqlite"
)

// DB owns the SQLite connection pool and its schema lifecycle.
type DB struct {
	sql *sql.DB
}

// Open validates the persistent path, opens SQLite and applies all migrations.
func Open(ctx context.Context, path string) (*DB, error) {
	created, err := prepareDatabaseFile(path)
	if err != nil {
		return nil, err
	}
	cleanupCreated := true
	defer func() {
		if created && cleanupCreated {
			_ = os.Remove(path)
		}
	}()

	parameters := url.Values{}
	parameters.Add("_pragma", "busy_timeout(5000)")
	parameters.Add("_pragma", "foreign_keys(1)")
	parameters.Add("_pragma", "journal_mode(WAL)")
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: parameters.Encode()}).String()

	connection, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	connection.SetMaxOpenConns(4)
	connection.SetMaxIdleConns(4)

	database := &DB{sql: connection}
	if err := connection.PingContext(ctx); err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("ping sqlite database: %w", err)
	}
	if err := database.Migrate(ctx); err != nil {
		_ = connection.Close()
		return nil, err
	}

	cleanupCreated = false
	return database, nil
}

// Close releases all SQLite resources.
func (d *DB) Close() error {
	if err := d.sql.Close(); err != nil {
		return fmt.Errorf("close sqlite database: %w", err)
	}
	return nil
}

// Ping verifies that the live SQLite connection can answer a bounded scalar query.
func (d *DB) Ping(ctx context.Context) error {
	var value int
	if err := d.sql.QueryRowContext(ctx, "SELECT 1").Scan(&value); err != nil {
		return fmt.Errorf("ping sqlite database: %w", err)
	}
	if value != 1 {
		return fmt.Errorf("ping sqlite database: unexpected result")
	}
	return nil
}

func prepareDatabaseFile(path string) (bool, error) {
	if !filepath.IsAbs(path) {
		return false, fmt.Errorf("validate database path: absolute path required")
	}
	directory := filepath.Dir(path)
	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		return false, fmt.Errorf("inspect database directory: %w", err)
	}
	if directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
		return false, fmt.Errorf("validate database directory: regular directory required")
	}
	if directoryInfo.Mode().Perm()&0o077 != 0 {
		return false, fmt.Errorf("validate database directory permissions: group or other access is not allowed")
	}

	fileInfo, err := os.Lstat(path)
	switch {
	case err == nil:
		if fileInfo.Mode()&os.ModeSymlink != 0 || !fileInfo.Mode().IsRegular() {
			return false, fmt.Errorf("validate database file: regular file required")
		}
		if fileInfo.Mode().Perm()&0o077 != 0 {
			return false, fmt.Errorf("validate database file permissions: group or other access is not allowed")
		}
		return false, nil
	case !errors.Is(err, os.ErrNotExist):
		return false, fmt.Errorf("inspect database file: %w", err)
	}

	// #nosec G304 -- path is trusted process configuration and validated above.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return false, fmt.Errorf("create database file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return false, fmt.Errorf("close new database file: %w", err)
	}
	return true, nil
}
