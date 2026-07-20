/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
 */
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kuroky/nginx-uix/internal/config"
)

const abandonedCandidatePrefix = ".candidate-"

// ReconcileReleaseArtifacts removes only startup artifacts that cannot be needed for recovery.
func (s *Service) ReconcileReleaseArtifacts(ctx context.Context) error {
	if ctx == nil || s == nil {
		return errors.New("reconcile release artifacts: service is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("reconcile release artifacts: %w", err)
	}
	options := s.release
	if options.ReleaseRoot == "" {
		options = defaultReleaseOptions()
	}
	if err := validateReleaseArtifactRoot(options.ReleaseRoot); err != nil {
		return fmt.Errorf("reconcile release artifacts: release root: %w", err)
	}
	if err := validateReleaseArtifactRoot(options.BackupRoot); err != nil {
		return fmt.Errorf("reconcile release artifacts: backup root: %w", err)
	}

	references, uncertain, err := scanReleaseBackupReferences(ctx, options.ReleaseRoot)
	if err != nil {
		return fmt.Errorf("reconcile release artifacts: scan release journals: %w", err)
	}
	if err := removeAbandonedCandidates(ctx, options.ReleaseRoot); err != nil {
		return fmt.Errorf("reconcile release artifacts: remove candidates: %w", err)
	}
	if uncertain {
		return nil
	}
	if err := removeUnreferencedIncompleteBackups(ctx, options.BackupRoot, references); err != nil {
		return fmt.Errorf("reconcile release artifacts: remove incomplete backups: %w", err)
	}
	return nil
}

func validateReleaseArtifactRoot(root string) error {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return config.ErrPathInvalid
	}
	information, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if information.Mode()&fs.ModeSymlink != 0 || !information.IsDir() {
		return config.ErrPathInvalid
	}
	return nil
}

func scanReleaseBackupReferences(
	ctx context.Context,
	releaseRoot string,
) (map[config.BackupID]struct{}, bool, error) {
	entries, err := os.ReadDir(releaseRoot)
	if err != nil {
		return nil, false, err
	}
	references := make(map[config.BackupID]struct{})
	uncertain := false
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		releaseID, err := config.ParseReleaseID(entry.Name())
		if err != nil {
			continue
		}
		releasePath := filepath.Join(releaseRoot, entry.Name())
		information, err := os.Lstat(releasePath)
		if err != nil {
			return nil, false, err
		}
		if information.Mode()&fs.ModeSymlink != 0 || !information.IsDir() {
			uncertain = true
			continue
		}
		controlPath := filepath.Join(releasePath, "control")
		controlInformation, err := os.Lstat(controlPath)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		if controlInformation.Mode()&fs.ModeSymlink != 0 || !controlInformation.IsDir() {
			uncertain = true
			continue
		}
		root, err := config.OpenScopedRoot(controlPath)
		if err != nil {
			uncertain = true
			continue
		}
		payload, _, readErr := root.ReadRegular(ctx, "state.json", releaseJournalLimit)
		closeErr := root.Close()
		if errors.Is(readErr, fs.ErrNotExist) && closeErr == nil {
			continue
		}
		if readErr != nil || closeErr != nil {
			uncertain = true
			continue
		}
		var journal releaseJournal
		if err := decodeReleaseJSON(payload, &journal); err != nil || journal.SchemaVersion != 1 || journal.ReleaseID != releaseID {
			uncertain = true
			continue
		}
		backupID, err := config.ParseBackupID(string(journal.BackupID))
		if err != nil {
			uncertain = true
			continue
		}
		references[backupID] = struct{}{}
	}
	return references, uncertain, nil
}

func removeAbandonedCandidates(ctx context.Context, releaseRoot string) error {
	entries, err := os.ReadDir(releaseRoot)
	if err != nil {
		return err
	}
	removed := false
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !strings.HasPrefix(entry.Name(), abandonedCandidatePrefix) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(releaseRoot, entry.Name())); err != nil {
			return err
		}
		removed = true
	}
	if removed {
		return syncDirectory(releaseRoot)
	}
	return nil
}

func removeUnreferencedIncompleteBackups(
	ctx context.Context,
	backupRoot string,
	references map[config.BackupID]struct{},
) error {
	entries, err := os.ReadDir(backupRoot)
	if err != nil {
		return err
	}
	removed := false
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		backupID, err := config.ParseBackupID(entry.Name())
		if err != nil {
			continue
		}
		if _, referenced := references[backupID]; referenced {
			continue
		}
		backupPath := filepath.Join(backupRoot, entry.Name())
		complete, err := backupHasCompleteMarker(backupPath)
		if err != nil {
			return err
		}
		if complete {
			continue
		}
		if err := removeBackupTree(backupPath); err != nil {
			return err
		}
		removed = true
	}
	if removed {
		return syncDirectory(backupRoot)
	}
	return nil
}

func backupHasCompleteMarker(backupPath string) (bool, error) {
	information, err := os.Lstat(backupPath)
	if err != nil {
		return false, err
	}
	if information.Mode()&fs.ModeSymlink != 0 || !information.IsDir() {
		return false, nil
	}
	controlPath := filepath.Join(backupPath, "control")
	controlInformation, err := os.Lstat(controlPath)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if controlInformation.Mode()&fs.ModeSymlink != 0 || !controlInformation.IsDir() {
		return false, nil
	}
	completeInformation, err := os.Lstat(filepath.Join(controlPath, "complete.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return completeInformation.Mode().IsRegular(), nil
}
