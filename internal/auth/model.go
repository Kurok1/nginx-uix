/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package auth

import (
	"context"
	"errors"
	"net/netip"
	"time"
)

var (
	// ErrNotFound is returned when an authentication record does not exist.
	ErrNotFound = errors.New("authentication record not found")
	// ErrAlreadyInitialized is returned after the first administrator exists.
	ErrAlreadyInitialized = errors.New("administrator already initialized")
	// ErrInvalidCredentials deliberately does not distinguish user and password failures.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrUnauthenticated indicates a missing, invalid or expired session.
	ErrUnauthenticated = errors.New("unauthenticated")
	// ErrRateLimited indicates an active login throttle block.
	ErrRateLimited = errors.New("login rate limited")
	// ErrInvalidCSRF indicates a missing or mismatched CSRF token.
	ErrInvalidCSRF = errors.New("invalid csrf token")
)

// User is the persisted administrator identity.
type User struct {
	ID             int64
	Username       string
	NormalizedName string
	PasswordHash   string
	Disabled       bool
	CreatedAt      time.Time
}

// NewUser contains the validated fields needed to create the first administrator.
type NewUser struct {
	Username       string
	NormalizedName string
	PasswordHash   string
	CreatedAt      time.Time
}

// ThrottleKey is scoped to one normalized username and direct peer address.
type ThrottleKey struct {
	NormalizedName string
	SourceIP       string
}

// Throttle is the persisted state for one login key.
type Throttle struct {
	Key             ThrottleKey
	FailureCount    int
	WindowStartedAt time.Time
	BlockedUntil    *time.Time
}

// LoginFailure supplies deterministic transition parameters to the repository.
type LoginFailure struct {
	Key           ThrottleKey
	At            time.Time
	Window        time.Duration
	Limit         int
	BlockDuration time.Duration
}

// Session stores only digests and bounded timestamps, never raw browser secrets.
type Session struct {
	TokenDigest       [32]byte
	UserID            int64
	CSRFDigest        [32]byte
	CreatedAt         time.Time
	LastSeenAt        time.Time
	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
	User              User
}

// NewSession contains the fields for an issued session.
type NewSession = Session

// SessionTouch advances the idle lifetime without changing the absolute lifetime.
type SessionTouch struct {
	TokenDigest   [32]byte
	LastSeenAt    time.Time
	IdleExpiresAt time.Time
}

// Clock supplies deterministic authentication timestamps.
type Clock interface {
	Now() time.Time
}

// BootstrapInput contains trusted process-level first-administrator settings.
type BootstrapInput struct {
	Username     string
	PasswordFile string
	Password     string
}

// LoginInput contains one credential attempt and the direct socket peer address.
type LoginInput struct {
	Username string
	Password string
	SourceIP netip.Addr
}

// IssuedSession carries raw secrets only across the immediate service/API boundary.
type IssuedSession struct {
	User              User
	Token             string
	CSRFToken         string
	CreatedAt         time.Time
	LastSeenAt        time.Time
	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
}

// RateLimitError supplies a stable retry duration without exposing credentials.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return ErrRateLimited.Error()
}

// Unwrap supports errors.Is with ErrRateLimited.
func (e *RateLimitError) Unwrap() error {
	return ErrRateLimited
}

// Repository is the persistence contract consumed by authentication services.
type Repository interface {
	UserCount(ctx context.Context) (int64, error)
	CreateInitialUser(ctx context.Context, user NewUser) (User, error)
	UserByNormalizedName(ctx context.Context, name string) (User, error)
	Throttle(ctx context.Context, key ThrottleKey) (Throttle, error)
	RecordLoginFailure(ctx context.Context, failure LoginFailure) (Throttle, error)
	ClearLoginFailures(ctx context.Context, key ThrottleKey) error
	CreateSession(ctx context.Context, session NewSession) (Session, error)
	SessionByDigest(ctx context.Context, digest [32]byte) (Session, error)
	TouchSession(ctx context.Context, touch SessionTouch) error
	DeleteSession(ctx context.Context, digest [32]byte) error
}
