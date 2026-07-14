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
	"time"

	"github.com/kuroky/nginx-uix/internal/auth"
)

var _ auth.Repository = (*DB)(nil)

// UserCount returns the bounded scalar administrator count.
func (d *DB) UserCount(ctx context.Context) (int64, error) {
	var count int64
	if err := d.sql.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return count, nil
}

// CreateInitialUser atomically creates the sole bootstrap administrator.
func (d *DB) CreateInitialUser(ctx context.Context, input auth.NewUser) (auth.User, error) {
	var created auth.User
	err := d.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		var count int64
		if err := connection.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
			return fmt.Errorf("count users during bootstrap: %w", err)
		}
		if count != 0 {
			return auth.ErrAlreadyInitialized
		}

		result, err := connection.ExecContext(
			ctx,
			`INSERT INTO users(username, normalized_name, password_hash, disabled, created_at)
			 VALUES (?, ?, ?, 0, ?)`,
			input.Username,
			input.NormalizedName,
			input.PasswordHash,
			formatTime(input.CreatedAt),
		)
		if err != nil {
			return fmt.Errorf("insert initial user: %w", err)
		}
		identifier, err := result.LastInsertId()
		if err != nil {
			return fmt.Errorf("read initial user identifier: %w", err)
		}
		created = auth.User{
			ID:             identifier,
			Username:       input.Username,
			NormalizedName: input.NormalizedName,
			PasswordHash:   input.PasswordHash,
			CreatedAt:      input.CreatedAt.UTC(),
		}
		return nil
	})
	if err != nil {
		return auth.User{}, fmt.Errorf("create initial user: %w", err)
	}
	return created, nil
}

// UserByNormalizedName finds one administrator using its unique normalized name.
func (d *DB) UserByNormalizedName(ctx context.Context, name string) (auth.User, error) {
	user, err := scanUser(d.sql.QueryRowContext(
		ctx,
		`SELECT id, username, normalized_name, password_hash, disabled, created_at
		 FROM users WHERE normalized_name = ? LIMIT 1`,
		name,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return auth.User{}, fmt.Errorf("find user: %w", auth.ErrNotFound)
	}
	if err != nil {
		return auth.User{}, fmt.Errorf("find user: %w", err)
	}
	return user, nil
}

// Throttle returns one exact username/address throttle record.
func (d *DB) Throttle(ctx context.Context, key auth.ThrottleKey) (auth.Throttle, error) {
	throttle, err := scanThrottle(d.sql.QueryRowContext(
		ctx,
		`SELECT normalized_name, source_ip, failure_count, window_started_at, blocked_until
		 FROM login_throttles WHERE normalized_name = ? AND source_ip = ? LIMIT 1`,
		key.NormalizedName,
		key.SourceIP,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return auth.Throttle{}, fmt.Errorf("find login throttle: %w", auth.ErrNotFound)
	}
	if err != nil {
		return auth.Throttle{}, fmt.Errorf("find login throttle: %w", err)
	}
	return throttle, nil
}

// RecordLoginFailure atomically advances one exact throttle state.
func (d *DB) RecordLoginFailure(ctx context.Context, failure auth.LoginFailure) (auth.Throttle, error) {
	if failure.Limit <= 0 || failure.Window <= 0 || failure.BlockDuration <= 0 {
		return auth.Throttle{}, fmt.Errorf("record login failure: positive transition limits required")
	}

	var updated auth.Throttle
	err := d.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		current, err := scanThrottle(connection.QueryRowContext(
			ctx,
			`SELECT normalized_name, source_ip, failure_count, window_started_at, blocked_until
			 FROM login_throttles WHERE normalized_name = ? AND source_ip = ? LIMIT 1`,
			failure.Key.NormalizedName,
			failure.Key.SourceIP,
		))
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read login throttle transition: %w", err)
		}

		now := failure.At.UTC()
		switch {
		case errors.Is(err, sql.ErrNoRows):
			updated = auth.Throttle{Key: failure.Key, FailureCount: 1, WindowStartedAt: now}
		case current.BlockedUntil != nil && now.Before(*current.BlockedUntil):
			updated = current
		case now.Before(current.WindowStartedAt) || now.Sub(current.WindowStartedAt) >= failure.Window:
			updated = auth.Throttle{Key: failure.Key, FailureCount: 1, WindowStartedAt: now}
		default:
			updated = current
			updated.FailureCount++
			updated.BlockedUntil = nil
		}
		if updated.FailureCount >= failure.Limit && updated.BlockedUntil == nil {
			blockedUntil := now.Add(failure.BlockDuration)
			updated.BlockedUntil = &blockedUntil
		}

		var blockedUntil any
		if updated.BlockedUntil != nil {
			blockedUntil = formatTime(*updated.BlockedUntil)
		}
		_, err = connection.ExecContext(
			ctx,
			`INSERT INTO login_throttles(
				normalized_name, source_ip, failure_count, window_started_at, blocked_until
			 ) VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(normalized_name, source_ip) DO UPDATE SET
				failure_count = excluded.failure_count,
				window_started_at = excluded.window_started_at,
				blocked_until = excluded.blocked_until`,
			updated.Key.NormalizedName,
			updated.Key.SourceIP,
			updated.FailureCount,
			formatTime(updated.WindowStartedAt),
			blockedUntil,
		)
		if err != nil {
			return fmt.Errorf("write login throttle transition: %w", err)
		}
		return nil
	})
	if err != nil {
		return auth.Throttle{}, fmt.Errorf("record login failure: %w", err)
	}
	return updated, nil
}

// ClearLoginFailures deletes only the supplied username/address key.
func (d *DB) ClearLoginFailures(ctx context.Context, key auth.ThrottleKey) error {
	if _, err := d.sql.ExecContext(
		ctx,
		"DELETE FROM login_throttles WHERE normalized_name = ? AND source_ip = ?",
		key.NormalizedName,
		key.SourceIP,
	); err != nil {
		return fmt.Errorf("clear login failures: %w", err)
	}
	return nil
}

// CreateSession persists only token and CSRF digests.
func (d *DB) CreateSession(ctx context.Context, session auth.NewSession) (auth.Session, error) {
	_, err := d.sql.ExecContext(
		ctx,
		`INSERT INTO sessions(
			token_digest, user_id, csrf_digest, created_at, last_seen_at, idle_expires_at, absolute_expires_at
		 ) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		session.TokenDigest[:],
		session.UserID,
		session.CSRFDigest[:],
		formatTime(session.CreatedAt),
		formatTime(session.LastSeenAt),
		formatTime(session.IdleExpiresAt),
		formatTime(session.AbsoluteExpiresAt),
	)
	if err != nil {
		return auth.Session{}, fmt.Errorf("create session: %w", err)
	}
	return normalizeSessionTimes(session), nil
}

// SessionByDigest finds one session without accepting a raw browser token.
func (d *DB) SessionByDigest(ctx context.Context, digest [32]byte) (auth.Session, error) {
	session, err := scanSession(d.sql.QueryRowContext(
		ctx,
		`SELECT token_digest, user_id, csrf_digest, created_at, last_seen_at, idle_expires_at, absolute_expires_at
		 FROM sessions WHERE token_digest = ? LIMIT 1`,
		digest[:],
	))
	if errors.Is(err, sql.ErrNoRows) {
		return auth.Session{}, fmt.Errorf("find session: %w", auth.ErrNotFound)
	}
	if err != nil {
		return auth.Session{}, fmt.Errorf("find session: %w", err)
	}
	return session, nil
}

// TouchSession advances the bounded idle timestamps for one session.
func (d *DB) TouchSession(ctx context.Context, touch auth.SessionTouch) error {
	result, err := d.sql.ExecContext(
		ctx,
		"UPDATE sessions SET last_seen_at = ?, idle_expires_at = ? WHERE token_digest = ?",
		formatTime(touch.LastSeenAt),
		formatTime(touch.IdleExpiresAt),
		touch.TokenDigest[:],
	)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read touched session count: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("touch session: %w", auth.ErrNotFound)
	}
	return nil
}

// DeleteSession removes one exact token digest and is idempotent.
func (d *DB) DeleteSession(ctx context.Context, digest [32]byte) error {
	if _, err := d.sql.ExecContext(ctx, "DELETE FROM sessions WHERE token_digest = ?", digest[:]); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (d *DB) withImmediateTransaction(ctx context.Context, operation func(*sql.Conn) error) (returnErr error) {
	connection, err := d.sql.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire transaction connection: %w", err)
	}
	defer func() {
		if err := connection.Close(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("close transaction connection: %w", err)
		}
	}()
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin immediate transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			rollbackContext, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, _ = connection.ExecContext(rollbackContext, "ROLLBACK")
		}
	}()
	if err := operation(connection); err != nil {
		return err
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit immediate transaction: %w", err)
	}
	committed = true
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(row rowScanner) (auth.User, error) {
	var user auth.User
	var disabled int
	var createdAt string
	if err := row.Scan(
		&user.ID,
		&user.Username,
		&user.NormalizedName,
		&user.PasswordHash,
		&disabled,
		&createdAt,
	); err != nil {
		return auth.User{}, err
	}
	parsed, err := parseTime("user created time", createdAt)
	if err != nil {
		return auth.User{}, err
	}
	user.Disabled = disabled != 0
	user.CreatedAt = parsed
	return user, nil
}

func scanThrottle(row rowScanner) (auth.Throttle, error) {
	var throttle auth.Throttle
	var windowStartedAt string
	var blockedUntil sql.NullString
	if err := row.Scan(
		&throttle.Key.NormalizedName,
		&throttle.Key.SourceIP,
		&throttle.FailureCount,
		&windowStartedAt,
		&blockedUntil,
	); err != nil {
		return auth.Throttle{}, err
	}
	parsedWindow, err := parseTime("throttle window start", windowStartedAt)
	if err != nil {
		return auth.Throttle{}, err
	}
	throttle.WindowStartedAt = parsedWindow
	if blockedUntil.Valid {
		parsedBlock, err := parseTime("throttle block expiration", blockedUntil.String)
		if err != nil {
			return auth.Throttle{}, err
		}
		throttle.BlockedUntil = &parsedBlock
	}
	return throttle, nil
}

func scanSession(row rowScanner) (auth.Session, error) {
	var session auth.Session
	var tokenDigest, csrfDigest []byte
	var createdAt, lastSeenAt, idleExpiresAt, absoluteExpiresAt string
	if err := row.Scan(
		&tokenDigest,
		&session.UserID,
		&csrfDigest,
		&createdAt,
		&lastSeenAt,
		&idleExpiresAt,
		&absoluteExpiresAt,
	); err != nil {
		return auth.Session{}, err
	}
	if len(tokenDigest) != len(session.TokenDigest) || len(csrfDigest) != len(session.CSRFDigest) {
		return auth.Session{}, fmt.Errorf("decode session: invalid digest length")
	}
	copy(session.TokenDigest[:], tokenDigest)
	copy(session.CSRFDigest[:], csrfDigest)

	parsedTimes := []*time.Time{&session.CreatedAt, &session.LastSeenAt, &session.IdleExpiresAt, &session.AbsoluteExpiresAt}
	encodedTimes := []string{createdAt, lastSeenAt, idleExpiresAt, absoluteExpiresAt}
	labels := []string{"session creation", "session last seen", "session idle expiration", "session absolute expiration"}
	for index := range parsedTimes {
		parsed, err := parseTime(labels[index], encodedTimes[index])
		if err != nil {
			return auth.Session{}, err
		}
		*parsedTimes[index] = parsed
	}
	return session, nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(label, value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse %s: %w", label, err)
	}
	return parsed.UTC(), nil
}

func normalizeSessionTimes(session auth.Session) auth.Session {
	session.CreatedAt = session.CreatedAt.UTC()
	session.LastSeenAt = session.LastSeenAt.UTC()
	session.IdleExpiresAt = session.IdleExpiresAt.UTC()
	session.AbsoluteExpiresAt = session.AbsoluteExpiresAt.UTC()
	return session
}
