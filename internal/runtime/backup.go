/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
 */
package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

const (
	backupManifestMagic    = "NUXB"
	backupManifestVersion  = uint16(1)
	backupCompleteV1       = uint16(1)
	backupCompleteV2       = uint16(2)
	backupOperationTimeout = 2 * time.Minute
	backupCompleteLimit    = 16 << 10
	backupManifestLimit    = 16 << 20
)

type backupOptions struct {
	NginxRoot   string
	BackupRoot  string
	Limits      config.Limits
	afterCopy   func() error
	afterFreeze func() error
}

type backupManifest struct {
	SchemaVersion uint16                `json:"schema_version"`
	Entries       []backupManifestEntry `json:"entries"`
	EntryCount    int                   `json:"entry_count"`
	TotalBytes    int64                 `json:"total_bytes"`
}

type backupManifestEntry struct {
	Path       config.RelativePath `json:"path"`
	Type       config.EntryType    `json:"type"`
	Mode       uint32              `json:"mode"`
	Size       int64               `json:"size"`
	LinkTarget config.RelativePath `json:"link_target,omitempty"`
	Digest     config.Digest       `json:"digest"`
}

type backupComplete struct {
	SchemaVersion    uint16                  `json:"schema_version"`
	BackupID         config.BackupID         `json:"backup_id"`
	OriginType       config.BackupOriginType `json:"origin_type,omitempty"`
	OriginID         string                  `json:"origin_id,omitempty"`
	ReleaseID        config.ReleaseID        `json:"release_id,omitempty"`
	ProductionDigest config.Digest           `json:"production_digest"`
	TreeDigest       config.Digest           `json:"tree_digest"`
	EntryCount       int                     `json:"entry_count"`
	TotalBytes       int64                   `json:"total_bytes"`
	VerifiedAt       time.Time               `json:"verified_at"`
}

type backupCreation struct {
	backupID         config.BackupID
	originType       config.BackupOriginType
	originID         string
	releaseID        config.ReleaseID
	productionDigest config.Digest
}

func defaultBackupOptions() backupOptions {
	return backupOptions{NginxRoot: defaultConfigNginxRoot, BackupRoot: "/var/lib/nginx-uix/backups", Limits: config.DefaultLimits()}
}

func newBackupService(options backupOptions) (*Service, error) {
	if err := validateBackupOptions(options); err != nil {
		return nil, err
	}
	service := newServiceWithExecutor(executeCommand)
	service.backup = options
	service.configSnapshot.NginxRoot = options.NginxRoot
	service.configSnapshot.Limits = options.Limits
	return service, nil
}

func validateBackupOptions(options backupOptions) error {
	for _, root := range []string{options.NginxRoot, options.BackupRoot} {
		if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
			return fmt.Errorf("configure backup root: %w", config.ErrPathInvalid)
		}
		information, err := os.Lstat(root)
		if err != nil || information.Mode()&fs.ModeSymlink != 0 || !information.IsDir() {
			return errors.Join(fmt.Errorf("configure backup root: %w", config.ErrPathInvalid), err)
		}
	}
	if options.NginxRoot == options.BackupRoot || options.Limits.MaxEntries <= 0 || options.Limits.MaxWorkspaceBytes <= 0 {
		return fmt.Errorf("configure backup: %w", config.ErrLimitExceeded)
	}
	return nil
}

// CreateBackup creates and verifies one immutable complete copy of the fixed production root.
func (s *Service) CreateBackup(ctx context.Context, request config.BackupRequest) (config.BackupEvidence, error) {
	if ctx == nil || s == nil {
		return config.BackupEvidence{}, errors.New("create backup: service is unavailable")
	}
	if _, err := config.ParseReleaseID(string(request.ReleaseID)); err != nil {
		return config.BackupEvidence{}, err
	}
	if _, err := config.ParseBackupID(string(request.BackupID)); err != nil {
		return config.BackupEvidence{}, err
	}
	if request.ProductionDigest == (config.Digest{}) {
		return config.BackupEvidence{}, config.ErrDigestInvalid
	}
	return s.createBackup(ctx, backupCreation{
		backupID: request.BackupID, originType: config.BackupOriginRelease,
		originID: string(request.ReleaseID), releaseID: request.ReleaseID,
		productionDigest: request.ProductionDigest,
	})
}

// CreateRestoreBackup creates a v2 immutable safety backup owned by one manual restore.
func (s *Service) CreateRestoreBackup(ctx context.Context, request config.RestoreBackupRequest) (config.BackupEvidence, error) {
	if ctx == nil || s == nil {
		return config.BackupEvidence{}, errors.New("create restore backup: service is unavailable")
	}
	if _, err := config.ParseRestoreID(string(request.RestoreID)); err != nil {
		return config.BackupEvidence{}, err
	}
	if _, err := config.ParseBackupID(string(request.BackupID)); err != nil {
		return config.BackupEvidence{}, err
	}
	if request.ProductionDigest == (config.Digest{}) {
		return config.BackupEvidence{}, config.ErrDigestInvalid
	}
	return s.createBackup(ctx, backupCreation{
		backupID: request.BackupID, originType: config.BackupOriginRestore,
		originID: string(request.RestoreID), productionDigest: request.ProductionDigest,
	})
}

func (s *Service) createBackup(ctx context.Context, request backupCreation) (_ config.BackupEvidence, returnErr error) {
	options := s.backup
	if options.NginxRoot == "" {
		options = defaultBackupOptions()
	}
	if err := validateBackupOptions(options); err != nil {
		return config.BackupEvidence{}, err
	}
	operationCtx, cancel := context.WithTimeout(ctx, backupOperationTimeout)
	defer cancel()
	production, err := s.ConfigDigest(operationCtx)
	if err != nil || production.Digest != request.productionDigest {
		return config.BackupEvidence{}, errors.Join(fmt.Errorf("verify backup production digest: %w", config.ErrSnapshotChanged), err)
	}
	source, err := config.OpenScopedRoot(options.NginxRoot)
	if err != nil {
		return config.BackupEvidence{}, err
	}
	before, err := buildBackupManifest(operationCtx, source, options.Limits)
	if err != nil {
		return config.BackupEvidence{}, errors.Join(err, source.Close())
	}
	backupPath := filepath.Join(options.BackupRoot, string(request.backupID))
	if err := os.Mkdir(backupPath, 0o700); err != nil {
		return config.BackupEvidence{}, errors.Join(err, source.Close())
	}
	owned := true
	defer func() {
		if owned {
			cleanupErr := removeBackupTree(backupPath)
			if cleanupErr == nil {
				cleanupErr = syncDirectory(options.BackupRoot)
			}
			returnErr = errors.Join(returnErr, cleanupErr)
		}
	}()
	treePath := filepath.Join(backupPath, "tree")
	controlPath := filepath.Join(backupPath, "control")
	if err := os.Mkdir(treePath, 0o700); err != nil {
		return config.BackupEvidence{}, errors.Join(err, source.Close())
	}
	if err := os.Mkdir(controlPath, 0o700); err != nil {
		return config.BackupEvidence{}, errors.Join(err, source.Close())
	}
	if err := copyCandidateProduction(operationCtx, source, treePath, options.Limits); err != nil {
		return config.BackupEvidence{}, errors.Join(err, source.Close())
	}
	if options.afterCopy != nil {
		if err := options.afterCopy(); err != nil {
			return config.BackupEvidence{}, errors.Join(err, source.Close())
		}
	}
	after, err := buildBackupManifest(operationCtx, source, options.Limits)
	closeErr := source.Close()
	if err != nil || closeErr != nil || !equalBackupManifests(before, after) {
		return config.BackupEvidence{}, errors.Join(fmt.Errorf("verify stable backup source: %w", config.ErrSnapshotChanged), err, closeErr)
	}
	production, err = s.ConfigDigest(operationCtx)
	if err != nil || production.Digest != request.productionDigest {
		return config.BackupEvidence{}, errors.Join(fmt.Errorf("verify backup production digest after copy: %w", config.ErrSnapshotChanged), err)
	}
	backupRoot, err := config.OpenScopedRoot(treePath)
	if err != nil {
		return config.BackupEvidence{}, err
	}
	if err := verifyBackupManifest(operationCtx, backupRoot, before, false, options.Limits); err != nil {
		return config.BackupEvidence{}, errors.Join(err, backupRoot.Close())
	}
	if err := backupRoot.Close(); err != nil {
		return config.BackupEvidence{}, err
	}
	manifestPayload, treeDigest, err := marshalBackupManifest(before)
	if err != nil {
		return config.BackupEvidence{}, err
	}
	manifestPath := filepath.Join(controlPath, "manifest.bin")
	if err := writeSyncedFile(manifestPath, manifestPayload, 0o400); err != nil {
		return config.BackupEvidence{}, err
	}
	if err := syncFilesystemTree(treePath); err != nil {
		return config.BackupEvidence{}, err
	}
	if err := freezeBackupTree(treePath); err != nil {
		return config.BackupEvidence{}, err
	}
	if options.afterFreeze != nil {
		if err := options.afterFreeze(); err != nil {
			return config.BackupEvidence{}, err
		}
	}
	verifiedAt := s.currentTime()
	complete := backupComplete{
		SchemaVersion: backupCompleteV2, BackupID: request.backupID, OriginType: request.originType,
		OriginID: request.originID, ReleaseID: request.releaseID,
		ProductionDigest: request.productionDigest, TreeDigest: treeDigest,
		EntryCount: before.EntryCount, TotalBytes: before.TotalBytes, VerifiedAt: verifiedAt,
	}
	completePayload, err := json.Marshal(complete)
	if err != nil {
		return config.BackupEvidence{}, err
	}
	completePayload = append(completePayload, '\n')
	if err := writeAtomicSyncedFile(controlPath, "complete.json", completePayload, 0o400); err != nil {
		return config.BackupEvidence{}, err
	}
	// #nosec G302 -- this is an owner-only directory; execute permission is required for traversal.
	if err := os.Chmod(controlPath, 0o500); err != nil {
		return config.BackupEvidence{}, err
	}
	// #nosec G302 -- this is an owner-only directory; execute permission is required for traversal.
	if err := os.Chmod(backupPath, 0o500); err != nil {
		return config.BackupEvidence{}, err
	}
	if err := syncDirectory(options.BackupRoot); err != nil {
		return config.BackupEvidence{}, err
	}
	owned = false
	return config.BackupEvidence{
		BackupID: request.backupID, OriginType: request.originType, OriginID: request.originID,
		ReleaseID: request.releaseID, ProductionDigest: request.productionDigest, TreeDigest: treeDigest,
		EntryCount: before.EntryCount, TotalBytes: before.TotalBytes, VerifiedAt: verifiedAt,
	}, nil
}

// VerifyBackup re-reads private metadata and every protected tree entry.
func (s *Service) VerifyBackup(ctx context.Context, id config.BackupID) (config.BackupEvidence, error) {
	if ctx == nil || s == nil {
		return config.BackupEvidence{}, errors.New("verify backup: service is unavailable")
	}
	if _, err := config.ParseBackupID(string(id)); err != nil {
		return config.BackupEvidence{}, err
	}
	options := s.backup
	if options.BackupRoot == "" {
		options = defaultBackupOptions()
	}
	backupPath := filepath.Join(options.BackupRoot, string(id))
	if err := verifyBackupEnvelopeModes(backupPath); err != nil {
		return config.BackupEvidence{}, errors.Join(config.ErrSnapshotChanged, err)
	}
	manifest, treeDigest, err := readBackupManifest(ctx, filepath.Join(backupPath, "control", "manifest.bin"), options.Limits)
	if err != nil {
		return config.BackupEvidence{}, errors.Join(config.ErrSnapshotChanged, err)
	}
	controlRoot, err := config.OpenScopedRoot(filepath.Join(backupPath, "control"))
	if err != nil {
		return config.BackupEvidence{}, errors.Join(config.ErrSnapshotChanged, err)
	}
	completePayload, _, readErr := controlRoot.ReadRegular(ctx, "complete.json", backupCompleteLimit)
	controlCloseErr := controlRoot.Close()
	if readErr != nil || controlCloseErr != nil {
		return config.BackupEvidence{}, errors.Join(config.ErrSnapshotChanged, readErr, controlCloseErr)
	}
	var complete backupComplete
	if err := decodeReleaseJSON(completePayload, &complete); err != nil ||
		(complete.SchemaVersion != backupCompleteV1 && complete.SchemaVersion != backupCompleteV2) ||
		complete.BackupID != id || complete.ProductionDigest == (config.Digest{}) || complete.TreeDigest != treeDigest ||
		complete.EntryCount != manifest.EntryCount || complete.TotalBytes != manifest.TotalBytes ||
		complete.VerifiedAt.IsZero() || complete.VerifiedAt.Location() != time.UTC {
		return config.BackupEvidence{}, errors.Join(config.ErrSnapshotChanged, err)
	}
	if err := normalizeBackupCompleteOrigin(&complete); err != nil {
		return config.BackupEvidence{}, errors.Join(config.ErrSnapshotChanged, err)
	}
	root, err := config.OpenScopedRoot(filepath.Join(backupPath, "tree"))
	if err != nil {
		return config.BackupEvidence{}, errors.Join(config.ErrSnapshotChanged, err)
	}
	verifyErr := verifyBackupManifest(ctx, root, manifest, true, options.Limits)
	closeErr := root.Close()
	if verifyErr != nil || closeErr != nil {
		return config.BackupEvidence{}, errors.Join(config.ErrSnapshotChanged, verifyErr, closeErr)
	}
	return config.BackupEvidence{
		BackupID: id, OriginType: complete.OriginType, OriginID: complete.OriginID, ReleaseID: complete.ReleaseID,
		ProductionDigest: complete.ProductionDigest, TreeDigest: treeDigest,
		EntryCount: manifest.EntryCount, TotalBytes: manifest.TotalBytes, VerifiedAt: complete.VerifiedAt,
	}, nil
}

func normalizeBackupCompleteOrigin(complete *backupComplete) error {
	if complete == nil {
		return config.ErrSnapshotChanged
	}
	if complete.SchemaVersion == backupCompleteV1 {
		if _, err := config.ParseReleaseID(string(complete.ReleaseID)); err != nil ||
			complete.OriginType != "" || complete.OriginID != "" {
			return config.ErrSnapshotChanged
		}
		complete.OriginType = config.BackupOriginRelease
		complete.OriginID = string(complete.ReleaseID)
		return nil
	}
	switch complete.OriginType {
	case config.BackupOriginRelease:
		if _, err := config.ParseReleaseID(complete.OriginID); err != nil ||
			string(complete.ReleaseID) != complete.OriginID {
			return config.ErrSnapshotChanged
		}
	case config.BackupOriginRestore:
		if _, err := config.ParseRestoreID(complete.OriginID); err != nil || complete.ReleaseID != "" {
			return config.ErrSnapshotChanged
		}
	default:
		return config.ErrSnapshotChanged
	}
	return nil
}

// DeleteBackup removes one already-authorized fixed-root backup body after exact integrity verification.
func (s *Service) DeleteBackup(ctx context.Context, request config.BackupDeletionRequest) error {
	if ctx == nil || s == nil {
		return errors.New("delete backup: service is unavailable")
	}
	if _, err := config.ParseRetentionRunID(string(request.RunID)); err != nil {
		return err
	}
	if _, err := config.ParseBackupID(string(request.BackupID)); err != nil {
		return err
	}
	if request.ProductionDigest == (config.Digest{}) || request.TreeDigest == (config.Digest{}) ||
		request.SnapshotCreatedAt.IsZero() || request.SnapshotTotalBytes < 0 {
		return config.ErrDigestInvalid
	}
	select {
	case s.releaseLock <- struct{}{}:
		defer func() { <-s.releaseLock }()
	default:
		return config.ErrReleaseInProgress
	}
	options := s.backup
	if options.BackupRoot == "" {
		options = defaultBackupOptions()
	}
	backupPath := filepath.Join(options.BackupRoot, string(request.BackupID))
	if _, err := os.Lstat(backupPath); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect backup deletion target: %w", err)
	}
	operationCtx, cancel := context.WithTimeout(ctx, backupOperationTimeout)
	defer cancel()
	evidence, err := s.VerifyBackup(operationCtx, request.BackupID)
	if err != nil {
		return fmt.Errorf("verify backup deletion target: %w", err)
	}
	if evidence.ProductionDigest != request.ProductionDigest || evidence.TreeDigest != request.TreeDigest ||
		evidence.TotalBytes != request.SnapshotTotalBytes {
		return config.ErrSnapshotChanged
	}
	if err := removeBackupTree(backupPath); err != nil {
		return fmt.Errorf("remove backup body: %w", err)
	}
	if err := syncDirectory(options.BackupRoot); err != nil {
		return fmt.Errorf("sync backup deletion: %w", err)
	}
	return nil
}

func verifyBackupEnvelopeModes(backupPath string) error {
	for relative, expected := range map[string]struct {
		mode      fs.FileMode
		directory bool
	}{
		".":                     {mode: 0o500, directory: true},
		"control":               {mode: 0o500, directory: true},
		"tree":                  {mode: 0o500, directory: true},
		"control/manifest.bin":  {mode: 0o400},
		"control/complete.json": {mode: 0o400},
	} {
		information, err := os.Lstat(filepath.Join(backupPath, filepath.FromSlash(relative)))
		if err != nil || information.Mode()&fs.ModeSymlink != 0 || information.IsDir() != expected.directory ||
			information.Mode().Perm() != expected.mode {
			return errors.Join(config.ErrPathInvalid, err)
		}
	}
	return nil
}

func buildBackupManifest(ctx context.Context, root *config.ScopedRoot, limits config.Limits) (backupManifest, error) {
	rawEntries, err := root.Walk(ctx, limits.MaxEntries)
	if err != nil {
		return backupManifest{}, err
	}
	manifest := backupManifest{SchemaVersion: backupManifestVersion, Entries: make([]backupManifestEntry, 0, len(rawEntries)), EntryCount: len(rawEntries)}
	for _, raw := range rawEntries {
		if raw.Type == config.EntrySpecial || raw.Type == config.EntrySymlink && raw.LinkClass != config.EntrySymlinkInternal {
			return backupManifest{}, config.ErrPathInvalid
		}
		entry := backupManifestEntry{Path: raw.Path, Type: raw.Type, Mode: uint32(raw.Mode.Perm()), Size: raw.Size, LinkTarget: raw.SafeLinkTarget}
		if raw.Type == config.EntryRegular {
			contents, information, err := root.ReadRegular(ctx, raw.Path, min(candidateFileLimit, limits.MaxWorkspaceBytes))
			if err != nil || int64(len(contents)) != raw.Size || information.Mode().Perm() != raw.Mode.Perm() {
				return backupManifest{}, errors.Join(config.ErrSnapshotChanged, err)
			}
			entry.Digest = config.Digest(sha256.Sum256(contents))
			if manifest.TotalBytes > limits.MaxWorkspaceBytes-int64(len(contents)) {
				return backupManifest{}, config.ErrLimitExceeded
			}
			manifest.TotalBytes += int64(len(contents))
		}
		manifest.Entries = append(manifest.Entries, entry)
	}
	return manifest, nil
}

func verifyBackupManifest(ctx context.Context, root *config.ScopedRoot, manifest backupManifest, frozen bool, limits config.Limits) error {
	actual, err := buildBackupManifest(ctx, root, limits)
	if err != nil || len(actual.Entries) != len(manifest.Entries) {
		return errors.Join(config.ErrSnapshotChanged, err)
	}
	for index, expected := range manifest.Entries {
		actualEntry := actual.Entries[index]
		wantMode := expected.Mode
		if frozen && expected.Type != config.EntrySymlink {
			wantMode &^= 0o222
		}
		if actualEntry.Path != expected.Path || actualEntry.Type != expected.Type || actualEntry.Mode != wantMode || actualEntry.Size != expected.Size || actualEntry.LinkTarget != expected.LinkTarget || actualEntry.Digest != expected.Digest {
			return config.ErrSnapshotChanged
		}
	}
	return nil
}

func equalBackupManifests(left, right backupManifest) bool {
	return left.SchemaVersion == right.SchemaVersion && left.EntryCount == right.EntryCount && left.TotalBytes == right.TotalBytes && slices.Equal(left.Entries, right.Entries)
}

func marshalBackupManifest(manifest backupManifest) ([]byte, config.Digest, error) {
	payload, err := json.Marshal(manifest)
	if err != nil {
		return nil, config.Digest{}, err
	}
	if uint64(len(payload)) > uint64(^uint32(0)) {
		return nil, config.Digest{}, config.ErrLimitExceeded
	}
	result := make([]byte, 10+len(payload))
	copy(result, backupManifestMagic)
	binary.BigEndian.PutUint16(result[4:6], backupManifestVersion)
	// #nosec G115 -- the explicit uint32 bound above precedes this manifest encoding conversion.
	binary.BigEndian.PutUint32(result[6:10], uint32(len(payload)))
	copy(result[10:], payload)
	return result, config.Digest(sha256.Sum256(result)), nil
}

func readBackupManifest(ctx context.Context, path string, limits config.Limits) (backupManifest, config.Digest, error) {
	root, err := config.OpenScopedRoot(filepath.Dir(path))
	if err != nil {
		return backupManifest{}, config.Digest{}, errors.Join(config.ErrSnapshotChanged, err)
	}
	payload, _, readErr := root.ReadRegular(ctx, config.RelativePath(filepath.Base(path)), backupManifestLimit)
	closeErr := root.Close()
	err = errors.Join(readErr, closeErr)
	if err != nil || len(payload) < 10 || string(payload[:4]) != backupManifestMagic || binary.BigEndian.Uint16(payload[4:6]) != backupManifestVersion || int(binary.BigEndian.Uint32(payload[6:10])) != len(payload)-10 {
		return backupManifest{}, config.Digest{}, errors.Join(config.ErrSnapshotChanged, err)
	}
	var manifest backupManifest
	if err := decodeReleaseJSON(payload[10:], &manifest); err != nil || manifest.SchemaVersion != backupManifestVersion || manifest.EntryCount != len(manifest.Entries) || manifest.EntryCount > limits.MaxEntries || manifest.TotalBytes < 0 || manifest.TotalBytes > limits.MaxWorkspaceBytes {
		return backupManifest{}, config.Digest{}, errors.Join(config.ErrSnapshotChanged, err)
	}
	return manifest, config.Digest(sha256.Sum256(payload)), nil
}

func syncFilesystemTree(root string) error {
	paths := make([]string, 0)
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, path)
		return nil
	}); err != nil {
		return err
	}
	for index := len(paths) - 1; index >= 0; index-- {
		information, err := os.Lstat(paths[index])
		if err != nil {
			return err
		}
		if information.Mode()&fs.ModeSymlink != 0 {
			continue
		}
		file, err := os.Open(paths[index])
		if err != nil {
			return err
		}
		syncErr := file.Sync()
		closeErr := file.Close()
		if syncErr != nil || closeErr != nil {
			return errors.Join(syncErr, closeErr)
		}
	}
	return nil
}

func freezeBackupTree(root string) error {
	paths := make([]string, 0)
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, path)
		return nil
	}); err != nil {
		return err
	}
	for index := len(paths) - 1; index >= 0; index-- {
		information, err := os.Lstat(paths[index])
		if err != nil {
			return err
		}
		if information.Mode()&fs.ModeSymlink != 0 {
			continue
		}
		if err := os.Chmod(paths[index], information.Mode().Perm()&^0o222); err != nil {
			return err
		}
	}
	return nil
}

func removeBackupTree(root string) error {
	information, err := os.Lstat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if information.Mode()&fs.ModeSymlink != 0 || !information.IsDir() {
		return os.Remove(root)
	}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&fs.ModeSymlink == 0 && entry.IsDir() {
			// #nosec G122 G302 -- this is an Agent-owned 0700 backup root selected by a parsed opaque ID.
			return os.Chmod(path, 0o700)
		}
		return nil
	}); err != nil {
		return err
	}
	return os.RemoveAll(root)
}

func writeSyncedFile(path string, payload []byte, mode fs.FileMode) error {
	// #nosec G304 -- callers pass paths below newly created, fixed Agent-owned candidate or backup roots.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(payload); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Sync(); err != nil {
		return errors.Join(err, file.Close())
	}
	return file.Close()
}

func writeAtomicSyncedFile(directory, name string, payload []byte, mode fs.FileMode) error {
	temporary, err := os.CreateTemp(directory, "."+name+"-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	clean := true
	defer func() {
		if clean {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(mode); err != nil {
		return errors.Join(err, temporary.Close())
	}
	if _, err := temporary.Write(payload); err != nil {
		return errors.Join(err, temporary.Close())
	}
	if err := temporary.Sync(); err != nil {
		return errors.Join(err, temporary.Close())
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, filepath.Join(directory, name)); err != nil {
		return err
	}
	clean = false
	return syncDirectory(directory)
}

func syncDirectory(path string) error {
	// #nosec G703 G304 -- callers pass fixed validated roots or directories created beneath those roots.
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
