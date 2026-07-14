/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
	"unicode/utf8"
)

const (
	startupStateRoot        = "/run/nginx-uix"
	startupStateFilename    = "startup-state.json"
	startupStatePath        = startupStateRoot + "/" + startupStateFilename
	startupStateTempPattern = ".startup-state-*.tmp"
	startupStateReadLimit   = 6*startupDiagnosticLimit + 64*1024
	maxRecoveryEvents       = 5
)

type startupStateFile interface {
	io.Reader
	io.Writer
	Name() string
	Chmod(fs.FileMode) error
	Sync() error
	Close() error
	Stat() (fs.FileInfo, error)
}

type startupStateFileSystem interface {
	Lstat(string) (fs.FileInfo, error)
	EvalSymlinks(string) (string, error)
	CreateTemp(string, string) (startupStateFile, error)
	Open(string) (startupStateFile, error)
	Rename(string, string) error
	Remove(string) error
}

type osStartupStateFileSystem struct{}

func (osStartupStateFileSystem) Lstat(path string) (fs.FileInfo, error) {
	return os.Lstat(path)
}

func (osStartupStateFileSystem) EvalSymlinks(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}

func (osStartupStateFileSystem) CreateTemp(directory, pattern string) (startupStateFile, error) {
	return os.CreateTemp(directory, pattern)
}

func (osStartupStateFileSystem) Open(path string) (startupStateFile, error) {
	// #nosec G304 -- startupStateStore passes only canonical paths beneath its fixed root.
	return os.Open(path)
}

func (osStartupStateFileSystem) Rename(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}

func (osStartupStateFileSystem) Remove(path string) error {
	return os.Remove(path)
}

type startupStateStore struct {
	root       string
	target     string
	fileSystem startupStateFileSystem
	encode     func(io.Writer, *StartupState) error
	now        func() time.Time
}

func newStartupStateStore(root string) *startupStateStore {
	return &startupStateStore{
		root:       root,
		target:     filepath.Join(root, startupStateFilename),
		fileSystem: osStartupStateFileSystem{},
		encode: func(writer io.Writer, state *StartupState) error {
			return json.NewEncoder(writer).Encode(state)
		},
		now: func() time.Time { return time.Now().UTC() },
	}
}

func newProductionStartupStateStore() *startupStateStore {
	store := newStartupStateStore(startupStateRoot)
	store.target = startupStatePath
	return store
}

// WriteStartupState atomically persists bounded startup state at the fixed runtime path.
func WriteStartupState(ctx context.Context, state StartupState) error {
	return newProductionStartupStateStore().Write(ctx, state)
}

// RecordNginxExit records one Nginx death in the fixed five-minute recovery window.
func RecordNginxExit(ctx context.Context, exit ExitEvent) (RecoveryState, error) {
	return newProductionStartupStateStore().RecordNginxExit(ctx, exit)
}

func readStartupState(ctx context.Context) (*StartupState, error) {
	return newProductionStartupStateStore().Read(ctx)
}

func (s *startupStateStore) Write(ctx context.Context, state StartupState) (returnErr error) {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("write startup state: %w", err)
	}
	normalized := normalizeStartupState(&state, s.currentTime())
	canonicalRoot, canonicalTarget, _, err := s.resolvePaths()
	if err != nil {
		return fmt.Errorf("write startup state: %w", err)
	}

	temporary, err := s.fileSystem.CreateTemp(canonicalRoot, startupStateTempPattern)
	if err != nil {
		return fmt.Errorf("write startup state temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	closed := false
	removeTemporary := true
	defer func() {
		if !closed {
			if closeErr := temporary.Close(); closeErr != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("close startup state temporary file: %w", closeErr))
			}
		}
		if removeTemporary {
			if removeErr := s.fileSystem.Remove(temporaryPath); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
				returnErr = errors.Join(returnErr, fmt.Errorf("remove startup state temporary file: %w", removeErr))
			}
		}
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("set startup state permissions: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("write startup state: %w", err)
	}
	if err := s.encode(temporary, normalized); err != nil {
		return fmt.Errorf("encode startup state: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("write startup state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync startup state temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		closed = true
		return fmt.Errorf("close startup state temporary file: %w", err)
	}
	closed = true
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("write startup state: %w", err)
	}
	if err := s.rejectTargetSymlink(canonicalTarget); err != nil {
		return fmt.Errorf("write startup state: %w", err)
	}
	if err := s.fileSystem.Rename(temporaryPath, canonicalTarget); err != nil {
		return fmt.Errorf("publish startup state: %w", err)
	}
	removeTemporary = false

	directory, err := s.fileSystem.Open(canonicalRoot)
	if err != nil {
		return fmt.Errorf("open startup state directory: %w", err)
	}
	if err := directory.Sync(); err != nil {
		closeErr := directory.Close()
		return errors.Join(fmt.Errorf("sync startup state directory: %w", err), wrapCloseDirectoryError(closeErr))
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("close startup state directory: %w", err)
	}
	return nil
}

func (s *startupStateStore) Read(ctx context.Context) (*StartupState, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read startup state: %w", err)
	}
	_, canonicalTarget, targetInformation, err := s.resolvePaths()
	if err != nil {
		return nil, fmt.Errorf("read startup state: %w", err)
	}
	if targetInformation == nil {
		return nil, fmt.Errorf("read startup state: %w", fs.ErrNotExist)
	}
	if targetInformation.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("read startup state: mode must be 0600")
	}

	file, err := s.fileSystem.Open(canonicalTarget)
	if err != nil {
		return nil, fmt.Errorf("open startup state: %w", err)
	}
	openedInformation, statErr := file.Stat()
	if statErr != nil {
		closeErr := file.Close()
		return nil, errors.Join(fmt.Errorf("inspect opened startup state: %w", statErr), wrapCloseFileError(closeErr))
	}
	if !os.SameFile(targetInformation, openedInformation) {
		closeErr := file.Close()
		return nil, errors.Join(fmt.Errorf("read startup state: target changed during open"), wrapCloseFileError(closeErr))
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, startupStateReadLimit+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, errors.Join(fmt.Errorf("read startup state: %w", readErr), wrapCloseFileError(closeErr))
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close startup state: %w", closeErr)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read startup state: %w", err)
	}
	if len(contents) > startupStateReadLimit {
		return nil, fmt.Errorf("read startup state: size limit exceeded")
	}

	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var state StartupState
	if err := decoder.Decode(&state); err != nil {
		return nil, fmt.Errorf("decode startup state: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("decode startup state: trailing JSON value")
		}
		return nil, fmt.Errorf("decode startup state: %w", err)
	}
	if err := validateStartupState(&state); err != nil {
		return nil, err
	}
	return cloneStartupState(&state), nil
}

func (s *startupStateStore) RecordNginxExit(ctx context.Context, exit ExitEvent) (RecoveryState, error) {
	if err := ctx.Err(); err != nil {
		return RecoveryState{}, fmt.Errorf("record nginx exit: %w", err)
	}
	state, err := s.Read(ctx)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return RecoveryState{}, fmt.Errorf("record nginx exit: %w", err)
		}
		state = &StartupState{}
	}
	if state.Recovery == nil {
		state.Recovery = &RecoveryState{}
	}
	now := s.currentTime()
	exit.OccurredAt = now

	recovery := state.Recovery
	windowStart := now.Add(-recoveryWindow)
	currentEvents := make([]ExitEvent, 0, maxRecoveryEvents)
	for _, event := range recovery.Events {
		if event.OccurredAt.Before(windowStart) || event.OccurredAt.After(now) {
			continue
		}
		currentEvents = append(currentEvents, event)
	}
	currentEvents = append(currentEvents, exit)
	if len(currentEvents) > maxRecoveryEvents {
		currentEvents = currentEvents[len(currentEvents)-maxRecoveryEvents:]
	}
	recovery.Events = currentEvents
	recovery.Count = len(currentEvents)
	switch {
	case recovery.Permanent:
		if recovery.LastResult != RecoveryResultInvalidConfig {
			recovery.LastResult = RecoveryResultPermanent
		}
	case recovery.Count >= maxRecoveryEvents:
		recovery.Permanent = true
		recovery.LastResult = RecoveryResultPermanent
	default:
		recovery.LastResult = RecoveryResultRestarting
	}
	if err := s.Write(ctx, *state); err != nil {
		return RecoveryState{}, fmt.Errorf("record nginx exit: %w", err)
	}
	return *cloneStartupState(state).Recovery, nil
}

func (s *startupStateStore) resolvePaths() (string, string, fs.FileInfo, error) {
	root := filepath.Clean(s.root)
	target := filepath.Clean(s.target)
	if !filepath.IsAbs(root) || target != filepath.Join(root, startupStateFilename) {
		return "", "", nil, fmt.Errorf("startup state path escapes fixed root")
	}
	rootInformation, err := s.fileSystem.Lstat(root)
	if err != nil {
		return "", "", nil, fmt.Errorf("inspect startup state root: %w", err)
	}
	if rootInformation.Mode()&os.ModeSymlink != 0 || !rootInformation.IsDir() {
		return "", "", nil, fmt.Errorf("startup state root must be a directory without symlinks")
	}
	canonicalRoot, err := s.fileSystem.EvalSymlinks(root)
	if err != nil {
		return "", "", nil, fmt.Errorf("canonicalize startup state root: %w", err)
	}
	canonicalRoot = filepath.Clean(canonicalRoot)
	if !filepath.IsAbs(canonicalRoot) {
		return "", "", nil, fmt.Errorf("canonical startup state root is not absolute")
	}
	canonicalTarget := filepath.Join(canonicalRoot, startupStateFilename)
	relative, err := filepath.Rel(canonicalRoot, canonicalTarget)
	if err != nil || relative != startupStateFilename {
		return "", "", nil, fmt.Errorf("startup state path escapes canonical root")
	}
	targetInformation, err := s.fileSystem.Lstat(canonicalTarget)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return canonicalRoot, canonicalTarget, nil, nil
		}
		return "", "", nil, fmt.Errorf("inspect startup state target: %w", err)
	}
	if targetInformation.Mode()&os.ModeSymlink != 0 || !targetInformation.Mode().IsRegular() {
		return "", "", nil, fmt.Errorf("startup state target must be a regular file without symlinks")
	}
	return canonicalRoot, canonicalTarget, targetInformation, nil
}

func (s *startupStateStore) rejectTargetSymlink(target string) error {
	information, err := s.fileSystem.Lstat(target)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect startup state target: %w", err)
	}
	if information.Mode()&os.ModeSymlink != 0 || !information.Mode().IsRegular() {
		return fmt.Errorf("startup state target must be a regular file without symlinks")
	}
	return nil
}

func (s *startupStateStore) currentTime() time.Time {
	if s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}

func normalizeStartupState(state *StartupState, now time.Time) *StartupState {
	normalized := cloneStartupState(state)
	if normalized == nil {
		normalized = &StartupState{}
	}
	if normalized.Validation != nil {
		normalized.Validation.CheckedAt = normalized.Validation.CheckedAt.UTC()
		normalized.Validation.Diagnostic = truncateUTF8(
			sanitizeDiagnostic([]byte(normalized.Validation.Diagnostic)),
			startupDiagnosticLimit,
		)
	}
	if normalized.Recovery != nil {
		windowStart := now.Add(-recoveryWindow)
		events := make([]ExitEvent, 0, len(normalized.Recovery.Events))
		for _, event := range normalized.Recovery.Events {
			event.OccurredAt = event.OccurredAt.UTC()
			if event.OccurredAt.Before(windowStart) || event.OccurredAt.After(now) {
				continue
			}
			events = append(events, event)
		}
		sort.SliceStable(events, func(left, right int) bool {
			return events[left].OccurredAt.Before(events[right].OccurredAt)
		})
		if len(events) > maxRecoveryEvents {
			events = events[len(events)-maxRecoveryEvents:]
		}
		normalized.Recovery.Events = events
		normalized.Recovery.Count = len(events)
		if normalized.Recovery.Count >= maxRecoveryEvents {
			normalized.Recovery.Permanent = true
			normalized.Recovery.LastResult = RecoveryResultPermanent
		} else if normalized.Recovery.Count > 0 && !normalized.Recovery.Permanent {
			normalized.Recovery.LastResult = RecoveryResultRestarting
		}
	}
	if normalized.Validation != nil && !normalized.Validation.Valid {
		if normalized.Recovery == nil {
			normalized.Recovery = &RecoveryState{}
		}
		normalized.Recovery.Permanent = true
		normalized.Recovery.LastResult = RecoveryResultInvalidConfig
	}
	return normalized
}

func validateStartupState(state *StartupState) error {
	if state.Validation != nil && len([]byte(state.Validation.Diagnostic)) > startupDiagnosticLimit {
		return fmt.Errorf("validate startup state: diagnostic limit exceeded")
	}
	if state.Recovery == nil {
		return nil
	}
	recovery := state.Recovery
	if recovery.Count != len(recovery.Events) || recovery.Count > maxRecoveryEvents {
		return fmt.Errorf("validate startup state: invalid recovery count")
	}
	switch recovery.LastResult {
	case "", RecoveryResultRestarting, RecoveryResultInvalidConfig, RecoveryResultPermanent:
	default:
		return fmt.Errorf("validate startup state: invalid recovery result")
	}
	for index, event := range recovery.Events {
		if event.OccurredAt.IsZero() {
			return fmt.Errorf("validate startup state: recovery event time is required")
		}
		if index > 0 && event.OccurredAt.Before(recovery.Events[index-1].OccurredAt) {
			return fmt.Errorf("validate startup state: recovery events are not ordered")
		}
	}
	if recovery.LastResult == RecoveryResultInvalidConfig && (state.Validation == nil || state.Validation.Valid || !recovery.Permanent) {
		return fmt.Errorf("validate startup state: contradictory invalid configuration recovery")
	}
	if recovery.LastResult == RecoveryResultPermanent && !recovery.Permanent {
		return fmt.Errorf("validate startup state: contradictory permanent recovery")
	}
	return nil
}

func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	end := limit
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}

func wrapCloseFileError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("close startup state: %w", err)
}

func wrapCloseDirectoryError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("close startup state directory: %w", err)
}
