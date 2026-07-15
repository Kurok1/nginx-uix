/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// InitializeOptions contains trusted container filesystem and ownership values.
type InitializeOptions struct {
	DefaultsRoot string
	NginxRoot    string
	DataRoot     string
	RunRoot      string
	DataUID      int
	DataGID      int
}

type initializeFileCopier func(
	context.Context,
	string,
	string,
	fs.FileMode,
	*initializeCopyTransaction,
) error

type containerInitializer struct {
	copyFile initializeFileCopier
}

func newContainerInitializer() *containerInitializer {
	return &containerInitializer{copyFile: copyInitializeFile}
}

// InitializeContainer initializes an empty Nginx configuration volume.
func InitializeContainer(ctx context.Context, options InitializeOptions) error {
	return newContainerInitializer().initialize(ctx, options)
}

func (i *containerInitializer) initialize(ctx context.Context, options InitializeOptions) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("initialize container: %w", err)
	}
	if err := validateInitializeRootSeparation(options); err != nil {
		return fmt.Errorf("initialize container: %w", err)
	}
	if options.DataUID < 0 || options.DataGID < 0 {
		return fmt.Errorf("initialize container: data owner IDs must be nonnegative")
	}
	nginxRoot, err := resolveInitializeDirectory(options.NginxRoot, "nginx configuration root")
	if err != nil {
		return fmt.Errorf("initialize container: %w", err)
	}
	entries, err := os.ReadDir(nginxRoot)
	if err != nil {
		return fmt.Errorf("read nginx configuration root: %w", err)
	}
	dataRoot, runRoot, err := preflightContainerDirectories(options)
	if err != nil {
		return fmt.Errorf("initialize container: %w", err)
	}
	if initializePathsOverlap(nginxRoot, dataRoot) || initializePathsOverlap(nginxRoot, runRoot) ||
		initializePathsOverlap(dataRoot, runRoot) {
		return fmt.Errorf("initialize container: canonical writable roots must not overlap")
	}
	var transaction *initializeCopyTransaction
	if len(entries) == 0 {
		defaultsRoot, resolveErr := resolveInitializeDirectory(options.DefaultsRoot, "default nginx source root")
		if resolveErr != nil {
			return fmt.Errorf("initialize container: %w", resolveErr)
		}
		if initializePathsOverlap(defaultsRoot, nginxRoot) || initializePathsOverlap(defaultsRoot, dataRoot) ||
			initializePathsOverlap(defaultsRoot, runRoot) {
			return fmt.Errorf("initialize container: canonical default source must not overlap writable roots")
		}
		targetInformation, statErr := os.Stat(nginxRoot)
		if statErr != nil {
			return fmt.Errorf("inspect nginx configuration root: %w", statErr)
		}
		transaction = &initializeCopyTransaction{root: nginxRoot, rootModTime: targetInformation.ModTime()}
		copyErr := copyInitializeDirectory(ctx, defaultsRoot, nginxRoot, transaction, i.copyFile)
		if copyErr == nil {
			copyErr = ctx.Err()
		}
		if copyErr == nil {
			copyErr = transaction.validateExclusiveTree()
		}
		if copyErr != nil {
			rollbackErr := transaction.rollback()
			return errors.Join(fmt.Errorf("copy default nginx configuration: %w", copyErr), rollbackErr)
		}
	}

	if err := prepareContainerDirectories(ctx, options); err != nil {
		var rollbackErr error
		if transaction != nil {
			rollbackErr = transaction.rollback()
		}
		return errors.Join(fmt.Errorf("prepare container directories: %w", err), rollbackErr)
	}
	return nil
}

func preflightContainerDirectories(options InitializeOptions) (string, string, error) {
	dataRoot, dataExists, err := inspectInitializeDirectoryCandidate(options.DataRoot, "data root")
	if err != nil {
		return "", "", err
	}
	if dataExists {
		databasePath := filepath.Join(dataRoot, "nginx-uix.db")
		if err := requireInitializeChild(dataRoot, databasePath); err != nil {
			return "", "", fmt.Errorf("resolve sqlite database path: %w", err)
		}
		databaseInformation, databaseErr := os.Lstat(databasePath)
		if databaseErr == nil && (databaseInformation.Mode()&os.ModeSymlink != 0 || !databaseInformation.Mode().IsRegular()) {
			return "", "", fmt.Errorf("sqlite database must be a regular file without symlinks")
		}
		if databaseErr != nil && !errors.Is(databaseErr, fs.ErrNotExist) {
			return "", "", fmt.Errorf("inspect sqlite database: %w", databaseErr)
		}
	}
	runRoot, _, err := inspectInitializeDirectoryCandidate(options.RunRoot, "runtime root")
	if err != nil {
		return "", "", err
	}
	return dataRoot, runRoot, nil
}

func inspectInitializeDirectoryCandidate(path, description string) (string, bool, error) {
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		return "", false, fmt.Errorf("%s must be absolute", description)
	}
	_, err := os.Lstat(cleaned)
	if err == nil {
		canonical, resolveErr := resolveInitializeDirectory(cleaned, description)
		return canonical, true, resolveErr
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", false, fmt.Errorf("inspect %s: %w", description, err)
	}
	canonicalParent, err := resolveInitializeDirectory(filepath.Dir(cleaned), description+" parent")
	if err != nil {
		return "", false, err
	}
	canonical := filepath.Join(canonicalParent, filepath.Base(cleaned))
	if err := requireInitializeChild(canonicalParent, canonical); err != nil {
		return "", false, fmt.Errorf("resolve %s: %w", description, err)
	}
	return canonical, false, nil
}

func validateInitializeRootSeparation(options InitializeOptions) error {
	roots := []struct {
		name string
		path string
	}{
		{name: "default nginx source root", path: options.DefaultsRoot},
		{name: "nginx configuration root", path: options.NginxRoot},
		{name: "data root", path: options.DataRoot},
		{name: "runtime root", path: options.RunRoot},
	}
	for index := range roots {
		roots[index].path = filepath.Clean(roots[index].path)
		if !filepath.IsAbs(roots[index].path) {
			return fmt.Errorf("%s must be absolute", roots[index].name)
		}
	}
	for left := range roots {
		for right := left + 1; right < len(roots); right++ {
			if initializePathsOverlap(roots[left].path, roots[right].path) {
				return fmt.Errorf("%s and %s must not overlap", roots[left].name, roots[right].name)
			}
		}
	}
	return nil
}

func initializePathsOverlap(left, right string) bool {
	return initializePathContains(left, right) || initializePathContains(right, left)
}

func initializePathContains(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}

func prepareContainerDirectories(ctx context.Context, options InitializeOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dataRoot, err := ensureInitializeDirectory(options.DataRoot, "data root", 0o700)
	if err != nil {
		return err
	}
	if err := os.Chown(dataRoot, options.DataUID, options.DataGID); err != nil {
		return fmt.Errorf("set data root ownership: %w", err)
	}
	// #nosec G302 -- the persistent data directory is deliberately owner-only 0700.
	if err := os.Chmod(dataRoot, 0o700); err != nil {
		return fmt.Errorf("set data root permissions: %w", err)
	}
	if err := secureInitializeDatabase(dataRoot, options.DataUID, options.DataGID); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := recreateInitializeRunRoot(options.RunRoot); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func ensureInitializeDirectory(path, description string, mode fs.FileMode) (string, error) {
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("%s must be absolute", description)
	}
	_, err := os.Lstat(cleaned)
	if err == nil {
		canonical, resolveErr := resolveInitializeDirectory(cleaned, description)
		if resolveErr != nil {
			return "", resolveErr
		}
		if err := os.Chmod(canonical, mode); err != nil {
			return "", fmt.Errorf("set %s permissions: %w", description, err)
		}
		return canonical, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("inspect %s: %w", description, err)
	}
	canonicalParent, err := resolveInitializeDirectory(filepath.Dir(cleaned), description+" parent")
	if err != nil {
		return "", err
	}
	canonical := filepath.Join(canonicalParent, filepath.Base(cleaned))
	if err := requireInitializeChild(canonicalParent, canonical); err != nil {
		return "", fmt.Errorf("resolve %s: %w", description, err)
	}
	if err := os.Mkdir(canonical, mode); err != nil {
		return "", fmt.Errorf("create %s: %w", description, err)
	}
	if err := os.Chmod(canonical, mode); err != nil {
		return "", fmt.Errorf("set %s permissions: %w", description, err)
	}
	return canonical, nil
}

func secureInitializeDatabase(dataRoot string, uid, gid int) error {
	databasePath := filepath.Join(dataRoot, "nginx-uix.db")
	if err := requireInitializeChild(dataRoot, databasePath); err != nil {
		return fmt.Errorf("resolve sqlite database path: %w", err)
	}
	information, err := os.Lstat(databasePath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect sqlite database: %w", err)
	}
	if information.Mode()&os.ModeSymlink != 0 || !information.Mode().IsRegular() {
		return fmt.Errorf("sqlite database must be a regular file without symlinks")
	}
	if err := os.Chown(databasePath, uid, gid); err != nil {
		return fmt.Errorf("set sqlite database ownership: %w", err)
	}
	mode := information.Mode().Perm() & 0o600
	if err := os.Chmod(databasePath, mode); err != nil {
		return fmt.Errorf("set sqlite database permissions: %w", err)
	}
	return nil
}

func recreateInitializeRunRoot(path string) error {
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		return fmt.Errorf("runtime root must be absolute")
	}
	canonicalParent, err := resolveInitializeDirectory(filepath.Dir(cleaned), "runtime root parent")
	if err != nil {
		return err
	}
	canonical := filepath.Join(canonicalParent, filepath.Base(cleaned))
	if err := requireInitializeChild(canonicalParent, canonical); err != nil {
		return fmt.Errorf("resolve runtime root: %w", err)
	}
	information, err := os.Lstat(cleaned)
	if err == nil {
		if information.Mode()&os.ModeSymlink != 0 || !information.IsDir() {
			return fmt.Errorf("runtime root must be a directory without symlinks")
		}
		resolved, resolveErr := filepath.EvalSymlinks(cleaned)
		if resolveErr != nil {
			return fmt.Errorf("canonicalize runtime root: %w", resolveErr)
		}
		if filepath.Clean(resolved) != canonical {
			return fmt.Errorf("runtime root escapes its canonical parent")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect runtime root: %w", err)
	}
	if err == nil {
		if err := os.RemoveAll(canonical); err != nil {
			return fmt.Errorf("remove stale runtime root: %w", err)
		}
	}
	// #nosec G301 -- the fixed runtime root must be traversable by UI and Nginx;
	// sensitive socket and state files use their own stricter modes.
	if err := os.Mkdir(canonical, 0o755); err != nil {
		return fmt.Errorf("create runtime root: %w", err)
	}
	// #nosec G302 -- see the runtime-root traversal justification above.
	if err := os.Chmod(canonical, 0o755); err != nil {
		return fmt.Errorf("set runtime root permissions: %w", err)
	}
	return nil
}

func requireInitializeChild(root, path string) error {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) ||
		strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || strings.ContainsRune(relative, os.PathSeparator) {
		return fmt.Errorf("path escapes canonical root")
	}
	return nil
}

func resolveInitializeDirectory(path, description string) (string, error) {
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("%s must be absolute", description)
	}
	information, err := os.Lstat(cleaned)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", description, err)
	}
	if information.Mode()&os.ModeSymlink != 0 || !information.IsDir() {
		return "", fmt.Errorf("%s must be a directory without symlinks", description)
	}
	canonical, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return "", fmt.Errorf("canonicalize %s: %w", description, err)
	}
	canonical = filepath.Clean(canonical)
	if !filepath.IsAbs(canonical) {
		return "", fmt.Errorf("canonical %s must be absolute", description)
	}
	return canonical, nil
}

type initializeCopyTransaction struct {
	root        string
	rootModTime time.Time
	created     []string
}

func (t *initializeCopyTransaction) record(path string) {
	t.created = append(t.created, path)
}

func (t *initializeCopyTransaction) validateExclusiveTree() error {
	expected := make(map[string]struct{}, len(t.created))
	for _, path := range t.created {
		expected[filepath.Clean(path)] = struct{}{}
	}
	seen := 0
	err := filepath.WalkDir(t.root, func(path string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == t.root {
			return nil
		}
		if _, found := expected[filepath.Clean(path)]; !found {
			return fmt.Errorf("nginx configuration target became nonempty during initialization")
		}
		seen++
		return nil
	})
	if err != nil {
		return err
	}
	if seen != len(expected) {
		return fmt.Errorf("nginx configuration target changed during initialization")
	}
	return nil
}

func (t *initializeCopyTransaction) rollback() error {
	var rollbackErrors []error
	for index := len(t.created) - 1; index >= 0; index-- {
		path := t.created[index]
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("remove partially copied path: %w", err))
		}
	}
	entries, err := os.ReadDir(t.root)
	if err != nil {
		rollbackErrors = append(rollbackErrors, fmt.Errorf("inspect nginx configuration root after rollback: %w", err))
	} else if len(entries) == 0 {
		if err := os.Chtimes(t.root, t.rootModTime, t.rootModTime); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore nginx configuration root time: %w", err))
		}
	}
	return errors.Join(rollbackErrors...)
}

func copyInitializeDirectory(
	ctx context.Context,
	sourceRoot string,
	targetRoot string,
	transaction *initializeCopyTransaction,
	copyFile initializeFileCopier,
) error {
	entries, err := os.ReadDir(sourceRoot)
	if err != nil {
		return fmt.Errorf("read source directory: %w", err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		sourcePath := filepath.Join(sourceRoot, entry.Name())
		targetPath := filepath.Join(targetRoot, entry.Name())
		information, err := os.Lstat(sourcePath)
		if err != nil {
			return fmt.Errorf("inspect source entry %q: %w", entry.Name(), err)
		}
		switch {
		case information.IsDir():
			if err := os.Mkdir(targetPath, information.Mode().Perm()); err != nil {
				return fmt.Errorf("create target directory %q: %w", entry.Name(), err)
			}
			transaction.record(targetPath)
			if err := os.Chmod(targetPath, information.Mode().Perm()); err != nil {
				return fmt.Errorf("set target directory permissions %q: %w", entry.Name(), err)
			}
			if err := copyInitializeDirectory(ctx, sourcePath, targetPath, transaction, copyFile); err != nil {
				return err
			}
		case information.Mode().IsRegular():
			if err := copyFile(ctx, sourcePath, targetPath, information.Mode().Perm(), transaction); err != nil {
				return fmt.Errorf("copy source file %q: %w", entry.Name(), err)
			}
		default:
			return fmt.Errorf("source entry %q must be a regular file or directory", entry.Name())
		}
	}
	return nil
}

func copyInitializeFile(
	ctx context.Context,
	sourcePath string,
	targetPath string,
	mode fs.FileMode,
	transaction *initializeCopyTransaction,
) (returnErr error) {
	// #nosec G304 -- initialization paths are supplied only by trusted process assembly.
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open source file: %w", err)
	}
	defer func() {
		if closeErr := source.Close(); closeErr != nil && returnErr == nil {
			returnErr = fmt.Errorf("close source file: %w", closeErr)
		}
	}()

	// #nosec G304 -- the target is joined beneath the trusted configuration root.
	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create target file: %w", err)
	}
	transaction.record(targetPath)
	closed := false
	defer func() {
		if !closed {
			if closeErr := target.Close(); closeErr != nil && returnErr == nil {
				returnErr = fmt.Errorf("close target file: %w", closeErr)
			}
		}
	}()

	buffer := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			if _, err := target.Write(buffer[:read]); err != nil {
				return fmt.Errorf("write target file: %w", err)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return fmt.Errorf("read source file: %w", readErr)
		}
	}
	if err := target.Chmod(mode); err != nil {
		return fmt.Errorf("set target file permissions: %w", err)
	}
	if err := target.Sync(); err != nil {
		return fmt.Errorf("sync target file: %w", err)
	}
	if err := target.Close(); err != nil {
		closed = true
		return fmt.Errorf("close target file: %w", err)
	}
	closed = true
	return nil
}
