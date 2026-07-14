/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	sessionTokenBytes = 32
	idleLifetime      = 8 * time.Hour
	absoluteLifetime  = 24 * time.Hour
	sessionTouchEvery = 5 * time.Minute
	throttleWindow    = 5 * time.Minute
	throttleLimit     = 5
	throttleBlock     = 15 * time.Minute
	csrfContext       = "nginx-uix-csrf-v1"
	// #nosec G101 -- this fixed non-secret input equalizes missing-user verification work.
	dummyPassword = "nginx-uix-dummy-password-value"
)

// Service implements administrator bootstrap, login and session lifecycle rules.
type Service struct {
	repository Repository
	clock      Clock
	random     io.Reader
	dummyHash  string
}

// NewService creates a service and its process-lifetime dummy password hash.
func NewService(repository Repository, clock Clock, random io.Reader) (*Service, error) {
	if repository == nil || clock == nil || random == nil {
		return nil, fmt.Errorf("create authentication service: repository, clock and random reader are required")
	}
	dummyHash, err := HashPassword(random, dummyPassword)
	if err != nil {
		return nil, fmt.Errorf("create authentication dummy hash: %w", err)
	}
	return &Service{repository: repository, clock: clock, random: random, dummyHash: dummyHash}, nil
}

// Bootstrap creates the first administrator and ignores all bootstrap input thereafter.
func (s *Service) Bootstrap(ctx context.Context, input BootstrapInput) error {
	count, err := s.repository.UserCount(ctx)
	if err != nil {
		return fmt.Errorf("check administrator bootstrap state: %w", err)
	}
	if count != 0 {
		return nil
	}

	normalizedName, err := NormalizeUsername(input.Username)
	if err != nil {
		return err
	}
	password, err := ReadPassword(ctx, input.PasswordFile, input.Password)
	if err != nil {
		return err
	}
	passwordHash, err := HashPassword(s.random, password)
	if err != nil {
		return fmt.Errorf("hash bootstrap password: %w", err)
	}
	_, err = s.repository.CreateInitialUser(ctx, NewUser{
		Username:       input.Username,
		NormalizedName: normalizedName,
		PasswordHash:   passwordHash,
		CreatedAt:      s.clock.Now().UTC(),
	})
	if errors.Is(err, ErrAlreadyInitialized) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create bootstrap administrator: %w", err)
	}
	return nil
}

// Login validates one credential attempt and creates a digest-only session.
func (s *Service) Login(ctx context.Context, input LoginInput) (IssuedSession, error) {
	if !input.SourceIP.IsValid() {
		return IssuedSession{}, fmt.Errorf("validate login source address: valid IP required")
	}
	normalizedName, validName := normalizeLoginName(input.Username)
	key := ThrottleKey{NormalizedName: normalizedName, SourceIP: input.SourceIP.String()}
	now := s.clock.Now().UTC()
	if limited, err := s.activeRateLimit(ctx, key, now); err != nil {
		return IssuedSession{}, err
	} else if limited != nil {
		return IssuedSession{}, limited
	}

	user, lookupErr := s.repository.UserByNormalizedName(ctx, normalizedName)
	passwordHash := s.dummyHash
	if lookupErr == nil && validName && !user.Disabled {
		passwordHash = user.PasswordHash
	} else if lookupErr != nil && !errors.Is(lookupErr, ErrNotFound) {
		return IssuedSession{}, fmt.Errorf("find login user: %w", lookupErr)
	}
	verified, err := VerifyPassword(passwordHash, input.Password)
	if err != nil {
		return IssuedSession{}, fmt.Errorf("verify login password: %w", err)
	}
	if !validName || lookupErr != nil || user.Disabled || !verified {
		throttle, err := s.repository.RecordLoginFailure(ctx, LoginFailure{
			Key: key, At: now, Window: throttleWindow, Limit: throttleLimit, BlockDuration: throttleBlock,
		})
		if err != nil {
			return IssuedSession{}, fmt.Errorf("record login failure: %w", err)
		}
		if throttle.BlockedUntil != nil && now.Before(*throttle.BlockedUntil) {
			return IssuedSession{}, newRateLimitError(throttle.BlockedUntil.Sub(now))
		}
		return IssuedSession{}, ErrInvalidCredentials
	}

	if err := s.repository.ClearLoginFailures(ctx, key); err != nil {
		return IssuedSession{}, fmt.Errorf("clear login failures: %w", err)
	}
	tokenBytes := make([]byte, sessionTokenBytes)
	if _, err := io.ReadFull(s.random, tokenBytes); err != nil {
		return IssuedSession{}, fmt.Errorf("generate session token: %w", err)
	}
	rawToken := base64.RawURLEncoding.EncodeToString(tokenBytes)
	csrfToken := deriveCSRF(rawToken)
	tokenDigest := sha256.Sum256([]byte(rawToken))
	csrfDigest := sha256.Sum256([]byte(csrfToken))
	session := NewSession{
		TokenDigest:       tokenDigest,
		UserID:            user.ID,
		CSRFDigest:        csrfDigest,
		CreatedAt:         now,
		LastSeenAt:        now,
		IdleExpiresAt:     now.Add(idleLifetime),
		AbsoluteExpiresAt: now.Add(absoluteLifetime),
		User:              user,
	}
	created, err := s.repository.CreateSession(ctx, session)
	if err != nil {
		return IssuedSession{}, fmt.Errorf("persist session: %w", err)
	}
	return issueSession(created, rawToken, csrfToken), nil
}

// Current validates, optionally touches and returns one current session.
func (s *Service) Current(ctx context.Context, rawToken string) (IssuedSession, error) {
	if !validRawToken(rawToken) {
		return IssuedSession{}, ErrUnauthenticated
	}
	digest := sha256.Sum256([]byte(rawToken))
	session, err := s.repository.SessionByDigest(ctx, digest)
	if errors.Is(err, ErrNotFound) {
		return IssuedSession{}, ErrUnauthenticated
	}
	if err != nil {
		return IssuedSession{}, fmt.Errorf("read current session: %w", err)
	}
	csrfToken := deriveCSRF(rawToken)
	csrfDigest := sha256.Sum256([]byte(csrfToken))
	if subtle.ConstantTimeCompare(csrfDigest[:], session.CSRFDigest[:]) != 1 || session.User.Disabled {
		return IssuedSession{}, ErrUnauthenticated
	}

	now := s.clock.Now().UTC()
	if !now.Before(session.IdleExpiresAt) || !now.Before(session.AbsoluteExpiresAt) {
		if err := s.repository.DeleteSession(ctx, digest); err != nil {
			return IssuedSession{}, fmt.Errorf("delete expired session: %w", err)
		}
		return IssuedSession{}, ErrUnauthenticated
	}
	if now.Sub(session.LastSeenAt) >= sessionTouchEvery {
		idleExpiresAt := now.Add(idleLifetime)
		if idleExpiresAt.After(session.AbsoluteExpiresAt) {
			idleExpiresAt = session.AbsoluteExpiresAt
		}
		if err := s.repository.TouchSession(ctx, SessionTouch{
			TokenDigest: digest, LastSeenAt: now, IdleExpiresAt: idleExpiresAt,
		}); err != nil {
			return IssuedSession{}, fmt.Errorf("touch current session: %w", err)
		}
		session.LastSeenAt = now
		session.IdleExpiresAt = idleExpiresAt
	}
	return issueSession(session, rawToken, csrfToken), nil
}

// VerifyCSRF validates the derived token for an authenticated session.
func (s *Service) VerifyCSRF(ctx context.Context, rawToken, csrfToken string) error {
	current, err := s.Current(ctx, rawToken)
	if err != nil {
		return err
	}
	want := sha256.Sum256([]byte(current.CSRFToken))
	got := sha256.Sum256([]byte(csrfToken))
	if subtle.ConstantTimeCompare(want[:], got[:]) != 1 {
		return ErrInvalidCSRF
	}
	return nil
}

// Logout idempotently removes one well-formed session token.
func (s *Service) Logout(ctx context.Context, rawToken string) error {
	if !validRawToken(rawToken) {
		return nil
	}
	digest := sha256.Sum256([]byte(rawToken))
	if err := s.repository.DeleteSession(ctx, digest); err != nil {
		return fmt.Errorf("delete logout session: %w", err)
	}
	return nil
}

func (s *Service) activeRateLimit(ctx context.Context, key ThrottleKey, now time.Time) (*RateLimitError, error) {
	throttle, err := s.repository.Throttle(ctx, key)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read login throttle: %w", err)
	}
	if throttle.BlockedUntil != nil && now.Before(*throttle.BlockedUntil) {
		return newRateLimitError(throttle.BlockedUntil.Sub(now)), nil
	}
	return nil, nil
}

func normalizeLoginName(username string) (string, bool) {
	normalized, err := NormalizeUsername(username)
	if err == nil {
		return normalized, true
	}
	digest := sha256.Sum256([]byte(username))
	return "invalid-" + base64.RawURLEncoding.EncodeToString(digest[:]), false
}

func validRawToken(rawToken string) bool {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(rawToken)
	return err == nil && len(decoded) == sessionTokenBytes
}

func deriveCSRF(rawToken string) string {
	mac := hmac.New(sha256.New, []byte(rawToken))
	// hash.Hash writes always consume the full input and return a nil error.
	_, _ = io.WriteString(mac, csrfContext)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func issueSession(session Session, rawToken, csrfToken string) IssuedSession {
	user := session.User
	user.PasswordHash = ""
	return IssuedSession{
		User:              user,
		Token:             rawToken,
		CSRFToken:         csrfToken,
		CreatedAt:         session.CreatedAt,
		LastSeenAt:        session.LastSeenAt,
		IdleExpiresAt:     session.IdleExpiresAt,
		AbsoluteExpiresAt: session.AbsoluteExpiresAt,
	}
}

func newRateLimitError(duration time.Duration) *RateLimitError {
	seconds := (duration + time.Second - 1) / time.Second
	if seconds < 1 {
		seconds = 1
	}
	return &RateLimitError{RetryAfter: seconds * time.Second}
}
