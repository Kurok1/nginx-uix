/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
	"golang.org/x/sys/unix"
)

const (
	defaultConfigNginxRoot     = "/etc/nginx"
	defaultConfigWorkspaceRoot = "/var/lib/nginx-uix/workspaces"
	defaultConfigDataUID       = 10001
	defaultConfigDataGID       = 10001
	configSnapshotTimeout      = 60 * time.Second
	configDigestTimeout        = 15 * time.Second
	configWorkspaceMode        = fs.FileMode(0o700)
	configBaseFileMode         = fs.FileMode(0o400)
	configStageRandomBytes     = 8
	configStageAttempts        = 8
)

type configSnapshotOptions struct {
	NginxRoot     string
	WorkspaceRoot string
	DataUID       int
	DataGID       int
	Entry         config.RelativePath
	Limits        config.Limits

	operations configSnapshotOperations
}

type configSnapshotOperations struct {
	random         io.Reader
	openScopedRoot func(string) (*config.ScopedRoot, error)
	snapshotTo     func(context.Context, *config.ScopedRoot, *config.ScopedRoot, config.SnapshotOptions) (config.Snapshot, error)
	digestRoot     func(context.Context, *config.ScopedRoot, config.SnapshotOptions) (config.ProductionState, error)
	fchown         func(int, int, int) error
	fchmod         func(int, uint32) error
	fsync          func(int) error
	rename         func(int, string, int, string) error
	removeStage    func(context.Context, int, string, int) error
}

func defaultConfigSnapshotOptions() configSnapshotOptions {
	return normalizedConfigSnapshotOptions(configSnapshotOptions{
		NginxRoot:     defaultConfigNginxRoot,
		WorkspaceRoot: defaultConfigWorkspaceRoot,
		DataUID:       defaultConfigDataUID,
		DataGID:       defaultConfigDataGID,
		Entry:         "nginx.conf",
		Limits:        config.DefaultLimits(),
	})
}

func normalizedConfigSnapshotOptions(options configSnapshotOptions) configSnapshotOptions {
	if options.operations.random == nil {
		options.operations.random = rand.Reader
	}
	if options.operations.openScopedRoot == nil {
		options.operations.openScopedRoot = config.OpenScopedRoot
	}
	if options.operations.snapshotTo == nil {
		options.operations.snapshotTo = config.SnapshotTo
	}
	if options.operations.digestRoot == nil {
		options.operations.digestRoot = config.DigestRoot
	}
	if options.operations.fchown == nil {
		options.operations.fchown = unix.Fchown
	}
	if options.operations.fchmod == nil {
		options.operations.fchmod = unix.Fchmod
	}
	if options.operations.fsync == nil {
		options.operations.fsync = unix.Fsync
	}
	if options.operations.rename == nil {
		options.operations.rename = unix.Renameat
	}
	if options.operations.removeStage == nil {
		options.operations.removeStage = removeConfigSnapshotStage
	}
	return options
}

func newConfigSnapshotService(options configSnapshotOptions) (*Service, error) {
	options = normalizedConfigSnapshotOptions(options)
	if err := validateConfigSnapshotOptions(options); err != nil {
		return nil, err
	}
	service := newServiceWithExecutor(executeCommand)
	service.configSnapshot = options
	return service, nil
}

func validateConfigSnapshotOptions(options configSnapshotOptions) error {
	if options.DataUID < 0 || options.DataGID < 0 {
		return fmt.Errorf("configure config snapshot: %w", config.ErrPathInvalid)
	}
	for _, root := range []string{options.NginxRoot, options.WorkspaceRoot} {
		if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
			return fmt.Errorf("configure config snapshot root: %w", config.ErrPathInvalid)
		}
	}
	if options.NginxRoot == options.WorkspaceRoot {
		return fmt.Errorf("configure config snapshot roots: %w", config.ErrPathInvalid)
	}
	if _, err := config.ParseRelativePath(string(options.Entry), options.Limits); err != nil {
		return fmt.Errorf("configure config snapshot entry: %w", err)
	}
	if options.Limits.MaxEntries <= 0 || options.Limits.MaxFileBytes <= 0 || options.Limits.MaxManagedBytes < 0 {
		return fmt.Errorf("configure config snapshot limits: %w", config.ErrLimitExceeded)
	}
	return nil
}

// ConfigSnapshot creates one immutable base snapshot beneath the fixed workspace root.
func (s *Service) ConfigSnapshot(ctx context.Context, id config.WorkspaceID) (_ config.Snapshot, returnErr error) {
	if ctx == nil || s == nil {
		return config.Snapshot{}, fmt.Errorf("create config snapshot: service is unavailable")
	}
	parsedID, err := config.ParseWorkspaceID(string(id))
	if err != nil || parsedID != id {
		return config.Snapshot{}, fmt.Errorf("create config snapshot: %w", err)
	}
	options := normalizedConfigSnapshotOptions(s.configSnapshot)
	if err := validateConfigSnapshotOptions(options); err != nil {
		return config.Snapshot{}, fmt.Errorf("create config snapshot: %w", err)
	}
	operationCtx, cancel := context.WithTimeout(ctx, configSnapshotTimeout)
	defer cancel()

	workspaceRoot, err := openConfigSnapshotDirectory(options.WorkspaceRoot)
	if err != nil {
		return config.Snapshot{}, fmt.Errorf("open config workspace root: %w", err)
	}
	defer func() {
		if closeErr := unix.Close(workspaceRoot); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close config workspace root: %w", closeErr))
		}
	}()
	if err := requireConfigSnapshotDirectory(workspaceRoot, options.DataUID, options.DataGID, configWorkspaceMode); err != nil {
		return config.Snapshot{}, fmt.Errorf("verify config workspace root: %w", err)
	}

	workspace, err := openConfigSnapshotDirectoryAt(workspaceRoot, string(id))
	if err != nil {
		return config.Snapshot{}, fmt.Errorf("open config workspace: %w", err)
	}
	defer func() {
		if closeErr := unix.Close(workspace); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close config workspace: %w", closeErr))
		}
	}()
	if err := requireConfigSnapshotDirectory(workspace, options.DataUID, options.DataGID, configWorkspaceMode); err != nil {
		return config.Snapshot{}, fmt.Errorf("verify config workspace: %w", err)
	}
	if err := requireConfigSnapshotControl(workspace, options); err != nil {
		return config.Snapshot{}, err
	}
	if err := requireConfigSnapshotBaseAbsent(workspace); err != nil {
		return config.Snapshot{}, err
	}

	stageName, err := createConfigSnapshotStage(operationCtx, workspace, options)
	if err != nil {
		if stageName == "" {
			return config.Snapshot{}, err
		}
		cleanupErr := options.operations.removeStage(context.WithoutCancel(operationCtx), workspace, stageName, options.Limits.MaxEntries)
		return config.Snapshot{}, errors.Join(err, wrapConfigSnapshotError("clean config snapshot stage", cleanupErr))
	}
	stagePath := filepath.Join(options.WorkspaceRoot, string(id), stageName)
	complete := false
	defer func() {
		if complete {
			return
		}
		if cleanupErr := options.operations.removeStage(context.WithoutCancel(operationCtx), workspace, stageName, options.Limits.MaxEntries); cleanupErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("clean config snapshot stage: %w", cleanupErr))
		}
	}()

	snapshot, err := executeConfigSnapshot(operationCtx, options, stagePath)
	if err != nil {
		return config.Snapshot{}, fmt.Errorf("create config snapshot: %w", err)
	}
	if err := operationCtx.Err(); err != nil {
		return config.Snapshot{}, fmt.Errorf("create config snapshot: %w", err)
	}
	if err := requireConfigSnapshotBaseAbsent(workspace); err != nil {
		return config.Snapshot{}, err
	}
	if err := options.operations.rename(workspace, stageName, workspace, "base"); err != nil {
		return config.Snapshot{}, fmt.Errorf("publish config snapshot base: %w", err)
	}
	if err := options.operations.fsync(workspace); err != nil {
		return config.Snapshot{}, fmt.Errorf("sync config workspace: %w", err)
	}
	complete = true
	return snapshot, nil
}

// ConfigDigest returns the immutable digest of only the fixed production root.
func (s *Service) ConfigDigest(ctx context.Context) (config.ProductionState, error) {
	if ctx == nil || s == nil {
		return config.ProductionState{}, fmt.Errorf("digest config root: service is unavailable")
	}
	options := normalizedConfigSnapshotOptions(s.configSnapshot)
	if err := validateConfigSnapshotOptions(options); err != nil {
		return config.ProductionState{}, fmt.Errorf("digest config root: %w", err)
	}
	operationCtx, cancel := context.WithTimeout(ctx, configDigestTimeout)
	defer cancel()
	root, err := options.operations.openScopedRoot(options.NginxRoot)
	if err != nil {
		return config.ProductionState{}, fmt.Errorf("open production config root: %w", err)
	}
	state, digestErr := options.operations.digestRoot(operationCtx, root, snapshotOptions(options, false))
	if digestErr == nil {
		digestErr = operationCtx.Err()
	}
	closeErr := root.Close()
	if digestErr != nil || closeErr != nil {
		return config.ProductionState{}, errors.Join(
			wrapConfigSnapshotError("digest production config", digestErr),
			wrapConfigSnapshotError("close production config root", closeErr),
		)
	}
	return state, nil
}

func executeConfigSnapshot(ctx context.Context, options configSnapshotOptions, stagePath string) (config.Snapshot, error) {
	source, err := options.operations.openScopedRoot(options.NginxRoot)
	if err != nil {
		return config.Snapshot{}, fmt.Errorf("open production config root: %w", err)
	}
	target, err := options.operations.openScopedRoot(stagePath)
	if err != nil {
		return config.Snapshot{}, errors.Join(fmt.Errorf("open config snapshot stage: %w", err), source.Close())
	}
	snapshot, snapshotErr := options.operations.snapshotTo(ctx, source, target, snapshotOptions(options, true))
	closeErr := errors.Join(target.Close(), source.Close())
	if snapshotErr != nil || closeErr != nil {
		return config.Snapshot{}, errors.Join(
			wrapConfigSnapshotError("copy production config snapshot", snapshotErr),
			wrapConfigSnapshotError("close config snapshot roots", closeErr),
		)
	}
	return snapshot, nil
}

func snapshotOptions(options configSnapshotOptions, owned bool) config.SnapshotOptions {
	snapshot := config.SnapshotOptions{
		Entry:         options.Entry,
		Limits:        options.Limits,
		Policy:        config.NewPolicy(),
		FileMode:      configBaseFileMode,
		DirectoryMode: configWorkspaceMode,
	}
	if owned {
		snapshot.Owner = &config.Owner{UID: options.DataUID, GID: options.DataGID}
	}
	return snapshot
}

func openConfigSnapshotDirectory(path string) (int, error) {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, classifyConfigSnapshotPathError(err)
	}
	return descriptor, nil
}

func openConfigSnapshotDirectoryAt(parent int, name string) (int, error) {
	descriptor, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, classifyConfigSnapshotPathError(err)
	}
	return descriptor, nil
}

func requireConfigSnapshotDirectory(descriptor, uid, gid int, mode fs.FileMode) error {
	var stat unix.Stat_t
	if err := unix.Fstat(descriptor, &stat); err != nil {
		return fmt.Errorf("inspect directory: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || int(stat.Uid) != uid || int(stat.Gid) != gid || fs.FileMode(stat.Mode).Perm() != mode {
		return config.ErrPathInvalid
	}
	return nil
}

func requireConfigSnapshotControl(workspace int, options configSnapshotOptions) error {
	control, err := openConfigSnapshotDirectoryAt(workspace, "control")
	if err != nil {
		return fmt.Errorf("open config workspace control: %w", err)
	}
	verifyErr := requireConfigSnapshotDirectory(control, options.DataUID, options.DataGID, configWorkspaceMode)
	closeErr := unix.Close(control)
	if verifyErr != nil || closeErr != nil {
		return errors.Join(
			wrapConfigSnapshotError("verify config workspace control", verifyErr),
			wrapConfigSnapshotError("close config workspace control", closeErr),
		)
	}
	return nil
}

func requireConfigSnapshotBaseAbsent(workspace int) error {
	var stat unix.Stat_t
	err := unix.Fstatat(workspace, "base", &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect config snapshot base: %w", classifyConfigSnapshotPathError(err))
	}
	if stat.Mode&unix.S_IFMT == unix.S_IFLNK {
		return fmt.Errorf("inspect config snapshot base: %w", config.ErrPathInvalid)
	}
	return fmt.Errorf("inspect config snapshot base: %w", fs.ErrExist)
}

func createConfigSnapshotStage(ctx context.Context, workspace int, options configSnapshotOptions) (string, error) {
	for range configStageAttempts {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		random := make([]byte, configStageRandomBytes)
		if _, err := io.ReadFull(options.operations.random, random); err != nil {
			return "", fmt.Errorf("generate config snapshot stage: %w", err)
		}
		name := "base.stage-" + hex.EncodeToString(random)
		if err := unix.Mkdirat(workspace, name, uint32(configWorkspaceMode)); errors.Is(err, unix.EEXIST) {
			continue
		} else if err != nil {
			return "", fmt.Errorf("create config snapshot stage: %w", classifyConfigSnapshotPathError(err))
		}
		stage, err := openConfigSnapshotDirectoryAt(workspace, name)
		if err != nil {
			return name, err
		}
		configureErr := options.operations.fchown(stage, options.DataUID, options.DataGID)
		if configureErr == nil {
			configureErr = options.operations.fchmod(stage, uint32(configWorkspaceMode))
		}
		closeErr := unix.Close(stage)
		if configureErr != nil || closeErr != nil {
			return name, errors.Join(
				wrapConfigSnapshotError("configure config snapshot stage", configureErr),
				wrapConfigSnapshotError("close config snapshot stage", closeErr),
			)
		}
		if err := options.operations.fsync(workspace); err != nil {
			return name, fmt.Errorf("sync config snapshot stage parent: %w", err)
		}
		return name, nil
	}
	return "", fmt.Errorf("create config snapshot stage: %w", fs.ErrExist)
}

func classifyConfigSnapshotPathError(err error) error {
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
		return errors.Join(config.ErrPathInvalid, err)
	}
	return err
}

func removeConfigSnapshotStage(ctx context.Context, workspace int, name string, limit int) error {
	if limit <= 0 {
		return fmt.Errorf("remove config snapshot stage: %w", config.ErrLimitExceeded)
	}
	var stat unix.Stat_t
	if err := unix.Fstatat(workspace, name, &stat, unix.AT_SYMLINK_NOFOLLOW); errors.Is(err, unix.ENOENT) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect config snapshot stage: %w", classifyConfigSnapshotPathError(err))
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		if err := unix.Unlinkat(workspace, name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
			return fmt.Errorf("remove replaced config snapshot stage: %w", err)
		}
		if err := unix.Fsync(workspace); err != nil {
			return fmt.Errorf("sync removed config snapshot stage: %w", err)
		}
		return nil
	}
	stage, err := openConfigSnapshotDirectoryAt(workspace, name)
	if err != nil {
		return fmt.Errorf("open config snapshot stage for cleanup: %w", err)
	}
	remaining := limit
	clearErr := clearConfigSnapshotStage(ctx, stage, &remaining)
	closeErr := unix.Close(stage)
	if clearErr != nil || closeErr != nil {
		return errors.Join(clearErr, wrapConfigSnapshotError("close config snapshot stage cleanup", closeErr))
	}
	if err := unix.Unlinkat(workspace, name, unix.AT_REMOVEDIR); err != nil && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("remove config snapshot stage directory: %w", err)
	}
	if err := unix.Fsync(workspace); err != nil {
		return fmt.Errorf("sync removed config snapshot stage: %w", err)
	}
	return nil
}

func clearConfigSnapshotStage(ctx context.Context, directory int, remaining *int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if *remaining < 0 {
		return config.ErrLimitExceeded
	}
	readDescriptor, err := unix.Openat(directory, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open config snapshot stage reader: %w", err)
	}
	// #nosec G115 -- readDescriptor is a non-negative descriptor returned by unix.Openat.
	reader := os.NewFile(uintptr(readDescriptor), "config snapshot stage cleanup")
	entries, readErr := reader.ReadDir(*remaining + 1)
	closeErr := reader.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return errors.Join(fmt.Errorf("read config snapshot stage: %w", readErr), closeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close config snapshot stage reader: %w", closeErr)
	}
	if len(entries) > *remaining {
		return fmt.Errorf("remove config snapshot stage: %w", config.ErrLimitExceeded)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if *remaining <= 0 {
			return fmt.Errorf("remove config snapshot stage: %w", config.ErrLimitExceeded)
		}
		(*remaining)--
		var stat unix.Stat_t
		if err := unix.Fstatat(directory, entry.Name(), &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return fmt.Errorf("inspect config snapshot stage entry: %w", err)
		}
		if stat.Mode&unix.S_IFMT == unix.S_IFDIR {
			child, err := openConfigSnapshotDirectoryAt(directory, entry.Name())
			if err != nil {
				return err
			}
			childErr := clearConfigSnapshotStage(ctx, child, remaining)
			childCloseErr := unix.Close(child)
			if childErr != nil || childCloseErr != nil {
				return errors.Join(childErr, wrapConfigSnapshotError("close config snapshot cleanup child", childCloseErr))
			}
			if err := unix.Unlinkat(directory, entry.Name(), unix.AT_REMOVEDIR); err != nil {
				return fmt.Errorf("remove config snapshot cleanup directory: %w", err)
			}
		} else if err := unix.Unlinkat(directory, entry.Name(), 0); err != nil {
			return fmt.Errorf("remove config snapshot cleanup entry: %w", err)
		}
	}
	if err := unix.Fsync(directory); err != nil {
		return fmt.Errorf("sync config snapshot cleanup directory: %w", err)
	}
	return nil
}

func wrapConfigSnapshotError(action string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", action, err)
}
