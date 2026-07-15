/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package auth_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/auth"
	"github.com/kuroky/nginx-uix/internal/store"
)

func TestBootstrapUsesSecretAndIgnoresEnvironmentAfterInitialization(t *testing.T) {
	database := openAuthDatabase(t)
	clock := &mutableClock{now: time.Date(2026, time.July, 14, 10, 0, 0, 0, time.UTC)}
	service := newAuthService(t, database, clock, 0x31)
	secretPath := filepath.Join(t.TempDir(), "admin-password")
	if err := os.WriteFile(secretPath, []byte("secret-file-password\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := service.Bootstrap(context.Background(), auth.BootstrapInput{
		Username: "Operator", PasswordFile: secretPath, Password: "environment-password",
	}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	user, err := database.UserByNormalizedName(context.Background(), "operator")
	if err != nil {
		t.Fatalf("UserByNormalizedName() error = %v", err)
	}
	verified, err := auth.VerifyPassword(user.PasswordHash, "secret-file-password")
	if err != nil || !verified {
		t.Fatalf("VerifyPassword(secret) = %v, %v", verified, err)
	}
	verified, err = auth.VerifyPassword(user.PasswordHash, "environment-password")
	if err != nil || verified {
		t.Fatalf("VerifyPassword(fallback) = %v, %v", verified, err)
	}

	if err := service.Bootstrap(context.Background(), auth.BootstrapInput{
		Username: "!", PasswordFile: filepath.Join(t.TempDir(), "does-not-exist"), Password: "short",
	}); err != nil {
		t.Fatalf("Bootstrap() after initialization error = %v, want ignored input", err)
	}
	count, err := database.UserCount(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("UserCount() = %d, %v, want 1", count, err)
	}
}

func TestLoginPersistsOnlyDigestsAndCurrentDerivesCSRF(t *testing.T) {
	service, database, clock := bootstrappedService(t)
	issued, err := service.Login(context.Background(), auth.LoginInput{
		Username: "OPERATOR", Password: "correct-password-123", SourceIP: netip.MustParseAddr("192.0.2.10"),
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if issued.Token == "" || issued.CSRFToken == "" {
		t.Fatalf("Login() returned empty secrets: %+v", issued)
	}

	tokenDigest := sha256.Sum256([]byte(issued.Token))
	stored, err := database.SessionByDigest(context.Background(), tokenDigest)
	if err != nil {
		t.Fatalf("SessionByDigest() error = %v", err)
	}
	if stored.TokenDigest != tokenDigest {
		t.Errorf("stored token digest mismatch")
	}
	if got, want := stored.CSRFDigest, sha256.Sum256([]byte(issued.CSRFToken)); got != want {
		t.Errorf("stored CSRF digest mismatch")
	}
	if bytes.Contains(stored.TokenDigest[:], []byte(issued.Token)) || bytes.Contains(stored.CSRFDigest[:], []byte(issued.CSRFToken)) {
		t.Fatal("stored digest contains a raw session secret")
	}

	current, err := service.Current(context.Background(), issued.Token)
	if err != nil {
		t.Fatalf("Current() error = %v", err)
	}
	if current.User.Username != "Operator" || current.CSRFToken != issued.CSRFToken {
		t.Errorf("Current() = %+v, want original user and CSRF", current)
	}
	if err := service.VerifyCSRF(context.Background(), issued.Token, issued.CSRFToken); err != nil {
		t.Fatalf("VerifyCSRF(correct) error = %v", err)
	}
	if err := service.VerifyCSRF(context.Background(), issued.Token, "wrong-csrf"); !errors.Is(err, auth.ErrInvalidCSRF) {
		t.Fatalf("VerifyCSRF(wrong) error = %v, want ErrInvalidCSRF", err)
	}

	clock.Advance(4 * time.Minute)
	if _, err := service.Current(context.Background(), issued.Token); err != nil {
		t.Fatalf("Current(before touch interval) error = %v", err)
	}
	untouched, err := database.SessionByDigest(context.Background(), tokenDigest)
	if err != nil {
		t.Fatalf("SessionByDigest(before touch) error = %v", err)
	}
	if !untouched.LastSeenAt.Equal(issued.CreatedAt) {
		t.Errorf("LastSeenAt changed before five minutes: %s", untouched.LastSeenAt)
	}
	clock.Advance(time.Minute)
	if _, err := service.Current(context.Background(), issued.Token); err != nil {
		t.Fatalf("Current(at touch interval) error = %v", err)
	}
	touched, err := database.SessionByDigest(context.Background(), tokenDigest)
	if err != nil {
		t.Fatalf("SessionByDigest(after touch) error = %v", err)
	}
	if !touched.LastSeenAt.Equal(clock.Now()) {
		t.Errorf("LastSeenAt = %s, want %s", touched.LastSeenAt, clock.Now())
	}
}

func TestCurrentExpiresAndDeletesIdleSession(t *testing.T) {
	service, database, clock := bootstrappedService(t)
	issued, err := service.Login(context.Background(), auth.LoginInput{
		Username: "operator", Password: "correct-password-123", SourceIP: netip.MustParseAddr("192.0.2.20"),
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	clock.Advance(8 * time.Hour)
	if _, err := service.Current(context.Background(), issued.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("Current(expired) error = %v, want ErrUnauthenticated", err)
	}
	digest := sha256.Sum256([]byte(issued.Token))
	if _, err := database.SessionByDigest(context.Background(), digest); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("SessionByDigest(expired) error = %v, want deleted", err)
	}
}

func TestCurrentEnforcesAbsoluteLifetimeAcrossTouches(t *testing.T) {
	service, database, clock := bootstrappedService(t)
	issued, err := service.Login(context.Background(), auth.LoginInput{
		Username: "operator", Password: "correct-password-123", SourceIP: netip.MustParseAddr("192.0.2.21"),
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	for range 3 {
		clock.Advance(7 * time.Hour)
		if _, err := service.Current(context.Background(), issued.Token); err != nil {
			t.Fatalf("Current(during absolute lifetime) error = %v", err)
		}
	}
	clock.Advance(3 * time.Hour)
	if _, err := service.Current(context.Background(), issued.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("Current(at absolute expiration) error = %v, want ErrUnauthenticated", err)
	}
	digest := sha256.Sum256([]byte(issued.Token))
	if _, err := database.SessionByDigest(context.Background(), digest); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("SessionByDigest(absolute expired) error = %v, want deleted", err)
	}
}

func TestSessionSurvivesServiceRestart(t *testing.T) {
	service, database, clock := bootstrappedService(t)
	issued, err := service.Login(context.Background(), auth.LoginInput{
		Username: "operator", Password: "correct-password-123", SourceIP: netip.MustParseAddr("192.0.2.22"),
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	restarted := newAuthService(t, database, clock, 0x62)
	current, err := restarted.Current(context.Background(), issued.Token)
	if err != nil {
		t.Fatalf("Current() after service restart error = %v", err)
	}
	if current.User.Username != issued.User.Username || current.CSRFToken != issued.CSRFToken {
		t.Errorf("Current() after restart = %+v, want same identity and CSRF", current)
	}
}

func TestLoginThrottlePersistsAndSuccessfulLoginClearsExactKey(t *testing.T) {
	service, database, clock := bootstrappedService(t)
	input := auth.LoginInput{
		Username: "operator", Password: "wrong-password-123", SourceIP: netip.MustParseAddr("192.0.2.30"),
	}
	for attempt := 1; attempt <= 4; attempt++ {
		if _, err := service.Login(context.Background(), input); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("Login() attempt %d error = %v, want ErrInvalidCredentials", attempt, err)
		}
	}
	_, err := service.Login(context.Background(), input)
	var limited *auth.RateLimitError
	if !errors.As(err, &limited) {
		t.Fatalf("fifth Login() error = %v, want RateLimitError", err)
	}
	if limited.RetryAfter != 15*time.Minute {
		t.Fatalf("RetryAfter = %s, want 15m", limited.RetryAfter)
	}
	if _, err := service.Login(context.Background(), auth.LoginInput{
		Username: "operator", Password: "correct-password-123", SourceIP: netip.MustParseAddr("192.0.2.31"),
	}); err != nil {
		t.Fatalf("Login() from independent source error = %v", err)
	}

	restarted := newAuthService(t, database, clock, 0x52)
	input.Password = "correct-password-123"
	if _, err := restarted.Login(context.Background(), input); !errors.Is(err, auth.ErrRateLimited) {
		t.Fatalf("Login() after service restart error = %v, want persisted rate limit", err)
	}
	clock.Advance(15 * time.Minute)
	if _, err := restarted.Login(context.Background(), input); err != nil {
		t.Fatalf("Login() after block expiration error = %v", err)
	}
	key := auth.ThrottleKey{NormalizedName: "operator", SourceIP: input.SourceIP.String()}
	if _, err := database.Throttle(context.Background(), key); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("Throttle() after successful login error = %v, want cleared", err)
	}
}

func TestAuthenticationErrorsDoNotContainSecrets(t *testing.T) {
	service, _, _ := bootstrappedService(t)
	password := "highly-sensitive-wrong-password"
	_, err := service.Login(context.Background(), auth.LoginInput{
		Username: "operator", Password: password, SourceIP: netip.MustParseAddr("192.0.2.50"),
	})
	if err == nil {
		t.Fatal("Login() error = nil, want invalid credentials")
	}
	for _, secret := range []string{password, "correct-password-123", "$argon2id$"} {
		if bytes.Contains([]byte(err.Error()), []byte(secret)) {
			t.Fatalf("Login() error exposes sensitive value %q: %v", secret, err)
		}
	}
}

func TestLoginUsesOneGenericErrorForMissingDisabledAndWrongPassword(t *testing.T) {
	service, database, clock := bootstrappedService(t)
	user, err := database.UserByNormalizedName(context.Background(), "operator")
	if err != nil {
		t.Fatalf("UserByNormalizedName() error = %v", err)
	}
	disabledUser := user
	disabledUser.Disabled = true
	disabledService := newAuthService(t, disabledUserRepository{Repository: database, user: disabledUser}, clock, 0x72)

	tests := []struct {
		name    string
		service *auth.Service
		input   auth.LoginInput
	}{
		{
			name: "missing user", service: service,
			input: auth.LoginInput{Username: "missing", Password: "correct-password-123", SourceIP: netip.MustParseAddr("192.0.2.61")},
		},
		{
			name: "disabled user", service: disabledService,
			input: auth.LoginInput{Username: "operator", Password: "correct-password-123", SourceIP: netip.MustParseAddr("192.0.2.62")},
		},
		{
			name: "wrong password", service: service,
			input: auth.LoginInput{Username: "operator", Password: "wrong-password-123", SourceIP: netip.MustParseAddr("192.0.2.63")},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.service.Login(context.Background(), test.input)
			if !errors.Is(err, auth.ErrInvalidCredentials) {
				t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
			}
			if got, want := err.Error(), auth.ErrInvalidCredentials.Error(); got != want {
				t.Fatalf("Login() error text = %q, want generic %q", got, want)
			}
		})
	}
}

func TestLogoutIsIdempotentAndInvalidatesSession(t *testing.T) {
	service, _, _ := bootstrappedService(t)
	issued, err := service.Login(context.Background(), auth.LoginInput{
		Username: "operator", Password: "correct-password-123", SourceIP: netip.MustParseAddr("192.0.2.40"),
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if err := service.Logout(context.Background(), issued.Token); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if err := service.Logout(context.Background(), issued.Token); err != nil {
		t.Fatalf("Logout() second error = %v", err)
	}
	if _, err := service.Current(context.Background(), issued.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("Current(after logout) error = %v, want ErrUnauthenticated", err)
	}
}

func TestServiceCleanupExpiredAuthStateUsesClockAndThrottleWindow(t *testing.T) {
	now := time.Date(2026, time.July, 15, 13, 0, 0, 0, time.UTC)
	repository := &cleanupCapturingRepository{
		Repository: openAuthDatabase(t),
		result:     auth.CleanupResult{SessionsDeleted: 2, ThrottlesDeleted: 3},
	}
	service := newAuthService(t, repository, &mutableClock{now: now}, 0x73)

	result, err := service.CleanupExpired(context.Background())
	if err != nil {
		t.Fatalf("CleanupExpired() error = %v", err)
	}
	if result != repository.result {
		t.Fatalf("CleanupExpired() = %+v, want %+v", result, repository.result)
	}
	if !repository.now.Equal(now) {
		t.Errorf("cleanup time = %s, want %s", repository.now, now)
	}
	if got, want := repository.throttleWindow, 5*time.Minute; got != want {
		t.Errorf("throttle window = %s, want %s", got, want)
	}
}

func bootstrappedService(t *testing.T) (*auth.Service, *store.DB, *mutableClock) {
	t.Helper()
	database := openAuthDatabase(t)
	clock := &mutableClock{now: time.Date(2026, time.July, 14, 10, 0, 0, 0, time.UTC)}
	service := newAuthService(t, database, clock, 0x41)
	if err := service.Bootstrap(context.Background(), auth.BootstrapInput{
		Username: "Operator", Password: "correct-password-123",
	}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	return service, database, clock
}

func newAuthService(t *testing.T, repository auth.Repository, clock auth.Clock, seed byte) *auth.Service {
	t.Helper()
	service, err := auth.NewService(repository, clock, bytes.NewReader(bytes.Repeat([]byte{seed}, 4096)))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

func openAuthDatabase(t *testing.T) *store.DB {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	database, err := store.Open(context.Background(), filepath.Join(directory, "nginx-uix.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return database
}

type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

type disabledUserRepository struct {
	auth.Repository
	user auth.User
}

type cleanupCapturingRepository struct {
	auth.Repository
	result         auth.CleanupResult
	now            time.Time
	throttleWindow time.Duration
}

func (r *cleanupCapturingRepository) CleanupExpiredAuthState(
	_ context.Context,
	now time.Time,
	throttleWindow time.Duration,
) (auth.CleanupResult, error) {
	r.now = now
	r.throttleWindow = throttleWindow
	return r.result, nil
}

func (r disabledUserRepository) UserByNormalizedName(context.Context, string) (auth.User, error) {
	return r.user, nil
}

func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mutableClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}
