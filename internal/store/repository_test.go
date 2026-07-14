/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/auth"
)

func TestCreateInitialUserSucceedsExactlyOnceConcurrently(t *testing.T) {
	database := openTestDatabase(t)
	createdAt := time.Date(2026, time.July, 14, 8, 0, 0, 0, time.UTC)
	users := []auth.NewUser{
		{Username: "operator-a", NormalizedName: "operator-a", PasswordHash: "hash-a", CreatedAt: createdAt},
		{Username: "operator-b", NormalizedName: "operator-b", PasswordHash: "hash-b", CreatedAt: createdAt},
	}

	start := make(chan struct{})
	errorsByCall := make(chan error, len(users))
	var callers sync.WaitGroup
	for _, user := range users {
		callers.Add(1)
		go func() {
			defer callers.Done()
			<-start
			_, err := database.CreateInitialUser(context.Background(), user)
			errorsByCall <- err
		}()
	}
	close(start)
	callers.Wait()
	close(errorsByCall)

	var successCount, initializedCount int
	for err := range errorsByCall {
		switch {
		case err == nil:
			successCount++
		case errors.Is(err, auth.ErrAlreadyInitialized):
			initializedCount++
		default:
			t.Errorf("CreateInitialUser() unexpected error = %v", err)
		}
	}
	if successCount != 1 || initializedCount != 1 {
		t.Fatalf("successes = %d, already initialized = %d, want 1 and 1", successCount, initializedCount)
	}
	count, err := database.UserCount(context.Background())
	if err != nil {
		t.Fatalf("UserCount() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("UserCount() = %d, want 1", count)
	}
}

func TestRepositorySessionAndThrottleSurviveReopen(t *testing.T) {
	directory := secureTempDir(t)
	path := filepath.Join(directory, "nginx-uix.db")
	database, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	createdAt := time.Date(2026, time.July, 14, 8, 0, 0, 123456789, time.UTC)
	user, err := database.CreateInitialUser(context.Background(), auth.NewUser{
		Username:       "Operator",
		NormalizedName: "operator",
		PasswordHash:   "argon2id-hash",
		CreatedAt:      createdAt,
	})
	if err != nil {
		t.Fatalf("CreateInitialUser() error = %v", err)
	}

	key := auth.ThrottleKey{NormalizedName: "operator", SourceIP: "192.0.2.10"}
	wantThrottle, err := database.RecordLoginFailure(context.Background(), auth.LoginFailure{
		Key: key, At: createdAt, Window: 5 * time.Minute, Limit: 5, BlockDuration: 15 * time.Minute,
	})
	if err != nil {
		t.Fatalf("RecordLoginFailure() error = %v", err)
	}

	var tokenDigest, csrfDigest [32]byte
	tokenDigest[0] = 0x11
	csrfDigest[0] = 0x22
	wantSession := auth.NewSession{
		TokenDigest:       tokenDigest,
		UserID:            user.ID,
		CSRFDigest:        csrfDigest,
		CreatedAt:         createdAt,
		LastSeenAt:        createdAt,
		IdleExpiresAt:     createdAt.Add(8 * time.Hour),
		AbsoluteExpiresAt: createdAt.Add(24 * time.Hour),
	}
	if _, err := database.CreateSession(context.Background(), wantSession); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	database, err = Open(context.Background(), path)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	gotUser, err := database.UserByNormalizedName(context.Background(), "operator")
	if err != nil {
		t.Fatalf("UserByNormalizedName() error = %v", err)
	}
	if gotUser.ID != user.ID || gotUser.Username != "Operator" || !gotUser.CreatedAt.Equal(createdAt) {
		t.Errorf("user after reopen = %+v, want ID/name/time preserved", gotUser)
	}
	gotThrottle, err := database.Throttle(context.Background(), key)
	if err != nil {
		t.Fatalf("Throttle() error = %v", err)
	}
	if gotThrottle.FailureCount != wantThrottle.FailureCount || !gotThrottle.WindowStartedAt.Equal(wantThrottle.WindowStartedAt) {
		t.Errorf("throttle after reopen = %+v, want %+v", gotThrottle, wantThrottle)
	}
	gotSession, err := database.SessionByDigest(context.Background(), tokenDigest)
	if err != nil {
		t.Fatalf("SessionByDigest() error = %v", err)
	}
	if gotSession != wantSession {
		t.Errorf("session after reopen = %+v, want %+v", gotSession, wantSession)
	}
}

func TestRepositoryMapsMissingRecordsAndMutatesOnlyExactKeys(t *testing.T) {
	database := openTestDatabase(t)
	missingDigest := [32]byte{0xff}
	if _, err := database.SessionByDigest(context.Background(), missingDigest); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("SessionByDigest() error = %v, want ErrNotFound", err)
	}
	missingKey := auth.ThrottleKey{NormalizedName: "missing", SourceIP: "192.0.2.1"}
	if _, err := database.Throttle(context.Background(), missingKey); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("Throttle() error = %v, want ErrNotFound", err)
	}
	if _, err := database.UserByNormalizedName(context.Background(), "missing"); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("UserByNormalizedName() error = %v, want ErrNotFound", err)
	}
}

func TestRepositoryDeletesOnlyTheRequestedSessionAndThrottle(t *testing.T) {
	database := openTestDatabase(t)
	now := time.Date(2026, time.July, 14, 9, 0, 0, 0, time.UTC)
	user, err := database.CreateInitialUser(context.Background(), auth.NewUser{
		Username: "operator", NormalizedName: "operator", PasswordHash: "hash", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateInitialUser() error = %v", err)
	}

	digests := [][32]byte{{0x01}, {0x02}}
	for _, digest := range digests {
		if _, err := database.CreateSession(context.Background(), auth.NewSession{
			TokenDigest: digest, UserID: user.ID, CSRFDigest: [32]byte{digest[0]},
			CreatedAt: now, LastSeenAt: now, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(2 * time.Hour),
		}); err != nil {
			t.Fatalf("CreateSession(%x) error = %v", digest[0], err)
		}
	}
	if err := database.DeleteSession(context.Background(), digests[0]); err != nil {
		t.Fatalf("DeleteSession() error = %v", err)
	}
	if err := database.DeleteSession(context.Background(), digests[0]); err != nil {
		t.Fatalf("DeleteSession() second call error = %v", err)
	}
	if _, err := database.SessionByDigest(context.Background(), digests[0]); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("deleted SessionByDigest() error = %v, want ErrNotFound", err)
	}
	if _, err := database.SessionByDigest(context.Background(), digests[1]); err != nil {
		t.Errorf("unrelated SessionByDigest() error = %v", err)
	}

	keys := []auth.ThrottleKey{
		{NormalizedName: "operator", SourceIP: "192.0.2.1"},
		{NormalizedName: "operator", SourceIP: "192.0.2.2"},
	}
	for _, key := range keys {
		if _, err := database.RecordLoginFailure(context.Background(), auth.LoginFailure{
			Key: key, At: now, Window: 5 * time.Minute, Limit: 5, BlockDuration: 15 * time.Minute,
		}); err != nil {
			t.Fatalf("RecordLoginFailure(%s) error = %v", key.SourceIP, err)
		}
	}
	if err := database.ClearLoginFailures(context.Background(), keys[0]); err != nil {
		t.Fatalf("ClearLoginFailures() error = %v", err)
	}
	if _, err := database.Throttle(context.Background(), keys[0]); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("cleared Throttle() error = %v, want ErrNotFound", err)
	}
	if _, err := database.Throttle(context.Background(), keys[1]); err != nil {
		t.Errorf("unrelated Throttle() error = %v", err)
	}
}
