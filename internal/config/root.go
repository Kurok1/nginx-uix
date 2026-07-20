/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	temporaryRandomBytes = 8
	temporaryAttempts    = 8
)

// ScopedRoot provides descriptor-relative, no-symlink filesystem operations.
type ScopedRoot struct {
	root      *os.Root
	directory *os.File
	path      string
	nameMax   int
	fsync     func(int) error
}

// OpenScopedRoot opens and verifies a trusted directory without following a root symlink.
func OpenScopedRoot(path string) (*ScopedRoot, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect scoped root: %w", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return nil, invalidPath("scoped root is not a directory")
	}

	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, classifyPathOperation("open scoped root", err)
	}
	directory, err := fileFromDescriptor(descriptor, "scoped root")
	if err != nil {
		return nil, errors.Join(err, closeDescriptor("close scoped root descriptor", descriptor))
	}

	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open scoped root handle: %w", err), directory.Close())
	}
	directoryInfo, directoryErr := directory.Stat()
	rootInfo, rootErr := root.Stat(".")
	if directoryErr != nil || rootErr != nil || !os.SameFile(directoryInfo, rootInfo) {
		verifyErr := errors.Join(directoryErr, rootErr)
		if verifyErr == nil {
			verifyErr = invalidPath("scoped root handles differ")
		}
		return nil, errors.Join(verifyErr, root.Close(), directory.Close())
	}

	nameMax, err := filesystemNameMax(descriptor)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("read scoped root name limit: %w", err), root.Close(), directory.Close())
	}
	nameMax = min(portableNameMax, nameMax)
	if nameMax <= 0 {
		return nil, errors.Join(invalidPath("scoped root name limit unavailable"), root.Close(), directory.Close())
	}

	return &ScopedRoot{
		root:      root,
		directory: directory,
		path:      path,
		nameMax:   nameMax,
		fsync:     unix.Fsync,
	}, nil
}

// Close releases both verified handles for the scoped root.
func (r *ScopedRoot) Close() error {
	if r == nil {
		return nil
	}
	var rootErr, directoryErr error
	if r.root != nil {
		rootErr = r.root.Close()
	}
	if r.directory != nil {
		directoryErr = r.directory.Close()
	}
	return errors.Join(rootErr, directoryErr)
}

// Lstat returns metadata for a regular file or directory without following symlinks.
func (r *ScopedRoot) Lstat(ctx context.Context, path RelativePath) (fs.FileInfo, error) {
	file, parent, info, err := r.openEntry(ctx, path)
	if err != nil {
		return nil, err
	}
	closeErr := errors.Join(file.Close(), unix.Close(parent))
	if !info.Mode().IsRegular() && !info.IsDir() {
		return nil, errors.Join(invalidPath("entry is not a regular file or directory"), closeErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close scoped entry: %w", closeErr)
	}
	return info, nil
}

// ReadRegular reads a regular file while enforcing an exact byte limit.
func (r *ScopedRoot) ReadRegular(ctx context.Context, path RelativePath, limit int64) ([]byte, fs.FileInfo, error) {
	if limit < 0 {
		return nil, nil, fmt.Errorf("read regular file: %w", ErrLimitExceeded)
	}
	file, parent, info, err := r.openEntry(ctx, path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		closeErr := errors.Join(file.Close(), unix.Close(parent))
		return nil, nil, errors.Join(invalidPath("entry is not a regular file"), closeErr)
	}
	if info.Size() > limit {
		closeErr := errors.Join(file.Close(), unix.Close(parent))
		return nil, nil, errors.Join(fmt.Errorf("read regular file: %w", ErrLimitExceeded), closeErr)
	}

	content, readErr := readBounded(ctx, file, limit)
	closeErr := errors.Join(file.Close(), unix.Close(parent))
	if readErr != nil || closeErr != nil {
		return nil, nil, errors.Join(readErr, closeErr)
	}
	return content, info, nil
}

// CreateRegular exclusively creates and durably writes a regular file.
func (r *ScopedRoot) CreateRegular(ctx context.Context, path RelativePath, content []byte, mode fs.FileMode) error {
	return r.createRegularOwned(ctx, path, content, mode, nil)
}

func (r *ScopedRoot) createRegularOwned(ctx context.Context, path RelativePath, content []byte, mode fs.FileMode, owner *Owner) (returnErr error) {
	parent, basename, err := r.openParent(ctx, path)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, closeDescriptor("close created file parent", parent))
	}()

	if err := ctx.Err(); err != nil {
		return err
	}
	descriptor, err := unix.Openat(parent, basename, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(mode.Perm()))
	if err != nil {
		return classifyPathOperation("create regular file", err)
	}
	file, err := fileFromDescriptor(descriptor, "scoped regular file")
	if err != nil {
		return errors.Join(err, closeDescriptor("close scoped regular descriptor", descriptor), unlinkTemporary(parent, basename))
	}
	if err := verifyRegular(file); err != nil {
		return errors.Join(err, file.Close(), unlinkTemporary(parent, basename))
	}
	if err := unix.Fchmod(descriptor, uint32(mode.Perm())); err != nil {
		return errors.Join(fmt.Errorf("change regular file mode: %w", err), file.Close(), unlinkTemporary(parent, basename))
	}
	if owner != nil {
		if err := unix.Fchown(descriptor, owner.UID, owner.GID); err != nil {
			return errors.Join(fmt.Errorf("change regular file owner: %w", err), file.Close(), unlinkTemporary(parent, basename))
		}
	}
	writeErr := r.writeAndSync(ctx, file, content)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		primaryErr := errors.Join(writeErr, closeErr)
		cleanupErr := unlinkTemporary(parent, basename)
		return errors.Join(primaryErr, cleanupErr)
	}
	if err := r.fsync(parent); err != nil {
		return fmt.Errorf("sync regular file parent: %w", err)
	}
	return nil
}

// AtomicReplace durably publishes content using a same-directory atomic rename.
func (r *ScopedRoot) AtomicReplace(ctx context.Context, path RelativePath, content []byte, mode fs.FileMode) (returnErr error) {
	parent, basename, err := r.openParent(ctx, path)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, closeDescriptor("close replaced file parent", parent))
	}()

	temporaryName, file, err := r.createTemporary(ctx, parent, basename, mode)
	if err != nil {
		return err
	}
	writeErr := r.writeAndSync(ctx, file, content)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		primaryErr := errors.Join(writeErr, closeErr)
		return errors.Join(primaryErr, unlinkTemporary(parent, temporaryName))
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(err, unlinkTemporary(parent, temporaryName))
	}
	if err := unix.Renameat(parent, temporaryName, parent, basename); err != nil {
		return errors.Join(fmt.Errorf("publish regular file: %w", err), unlinkTemporary(parent, temporaryName))
	}
	if err := r.fsync(parent); err != nil {
		return fmt.Errorf("sync published file parent: %w", err)
	}
	return nil
}

// RemoveRegular removes a verified regular file and syncs its held parent directory.
func (r *ScopedRoot) RemoveRegular(ctx context.Context, path RelativePath) (returnErr error) {
	file, parent, info, err := r.openEntry(ctx, path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		closeErr := errors.Join(file.Close(), unix.Close(parent))
		return errors.Join(invalidPath("entry is not a regular file"), closeErr)
	}
	if err := file.Close(); err != nil {
		return errors.Join(fmt.Errorf("close regular file: %w", err), closeDescriptor("close removed file parent", parent))
	}
	defer func() {
		returnErr = errors.Join(returnErr, closeDescriptor("close removed file parent", parent))
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	components := strings.Split(string(path), "/")
	if err := unix.Unlinkat(parent, components[len(components)-1], 0); err != nil {
		return fmt.Errorf("remove regular file: %w", err)
	}
	if err := r.fsync(parent); err != nil {
		return fmt.Errorf("sync removed file parent: %w", err)
	}
	return nil
}

// EnsureDirectory creates every missing directory component without following symlinks.
func (r *ScopedRoot) EnsureDirectory(ctx context.Context, path RelativePath, mode fs.FileMode) (returnErr error) {
	return r.ensureDirectoryOwned(ctx, path, mode, nil)
}

func (r *ScopedRoot) ensureDirectoryOwned(ctx context.Context, path RelativePath, mode fs.FileMode, owner *Owner) (returnErr error) {
	components, err := r.validatedComponents(path)
	if err != nil {
		return err
	}
	current, err := r.duplicateRoot()
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, closeDescriptor("close ensured directory", current))
	}()

	for _, component := range components {
		if err := ctx.Err(); err != nil {
			return err
		}
		next, openErr := unix.Openat(current, component, directoryOpenFlags(), 0)
		created := false
		if errors.Is(openErr, unix.ENOENT) {
			mkdirErr := unix.Mkdirat(current, component, uint32(mode.Perm()))
			if mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				return classifyPathOperation("create directory", mkdirErr)
			}
			created = mkdirErr == nil
			if created {
				if chmodErr := unix.Fchmodat(current, component, uint32(mode.Perm()), unix.AT_SYMLINK_NOFOLLOW); chmodErr != nil {
					return classifyPathOperation("change directory mode", chmodErr)
				}
			}
			if syncErr := r.fsync(current); syncErr != nil {
				return fmt.Errorf("sync created directory parent: %w", syncErr)
			}
			next, openErr = unix.Openat(current, component, directoryOpenFlags(), 0)
		}
		if openErr != nil {
			return classifyPathOperation("open directory", openErr)
		}
		if created && owner != nil {
			if err := unix.Fchown(next, owner.UID, owner.GID); err != nil {
				return errors.Join(fmt.Errorf("change directory owner: %w", err), closeDescriptor("close owned directory", next))
			}
		}
		if err := closeDescriptor("close directory descriptor", current); err != nil {
			return errors.Join(err, closeDescriptor("close next directory descriptor", next))
		}
		current = next
	}
	if err := r.fsync(current); err != nil {
		return fmt.Errorf("sync ensured directory: %w", err)
	}
	return nil
}

func (r *ScopedRoot) clear(ctx context.Context, limit int) error {
	if limit <= 0 {
		return fmt.Errorf("clear scoped root: %w", ErrLimitExceeded)
	}
	descriptor, err := r.duplicateRoot()
	if err != nil {
		return err
	}
	count := 0
	clearErr := r.clearDirectory(ctx, descriptor, limit, &count)
	closeErr := unix.Close(descriptor)
	return errors.Join(clearErr, closeErr)
}

func (r *ScopedRoot) clearDirectory(ctx context.Context, descriptor, limit int, count *int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := readDirectoryEntries(descriptor, limit-*count)
	if err != nil {
		return fmt.Errorf("read cleanup directory: %w", err)
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].Name() < entries[right].Name()
	})
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		(*count)++
		if *count > limit {
			return fmt.Errorf("clear scoped root: %w", ErrLimitExceeded)
		}
		var stat unix.Stat_t
		if err := unix.Fstatat(descriptor, entry.Name(), &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return classifyPathOperation("inspect cleanup entry", err)
		}
		entryType, _ := entryTypeAndMode(uint32(stat.Mode))
		if entryType == EntryDirectory {
			child, err := unix.Openat(descriptor, entry.Name(), directoryOpenFlags(), 0)
			if err != nil {
				return classifyPathOperation("open cleanup child", err)
			}
			childErr := r.clearDirectory(ctx, child, limit, count)
			childCloseErr := unix.Close(child)
			if childErr != nil || childCloseErr != nil {
				return errors.Join(childErr, childCloseErr)
			}
			if err := unix.Unlinkat(descriptor, entry.Name(), unix.AT_REMOVEDIR); err != nil {
				return fmt.Errorf("remove cleanup directory: %w", err)
			}
		} else if err := unix.Unlinkat(descriptor, entry.Name(), 0); err != nil {
			return fmt.Errorf("remove cleanup entry: %w", err)
		}
	}
	if err := r.fsync(descriptor); err != nil {
		return fmt.Errorf("sync cleanup directory: %w", err)
	}
	return nil
}

// Walk returns a deterministic descriptor-relative traversal bounded by entry count.
func (r *ScopedRoot) Walk(ctx context.Context, limit int) (entries []RawEntry, returnErr error) {
	if limit <= 0 {
		return nil, fmt.Errorf("walk scoped root: %w", ErrLimitExceeded)
	}
	descriptor, err := r.duplicateRoot()
	if err != nil {
		return nil, err
	}
	defer func() {
		returnErr = errors.Join(returnErr, closeDescriptor("close walked root", descriptor))
	}()
	entries = make([]RawEntry, 0, min(limit, 64))
	if err := r.walkDirectory(ctx, descriptor, "", limit, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (r *ScopedRoot) openEntry(ctx context.Context, path RelativePath) (*os.File, int, fs.FileInfo, error) {
	parent, basename, err := r.openParent(ctx, path)
	if err != nil {
		return nil, -1, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, -1, nil, errors.Join(err, closeDescriptor("close scoped entry parent", parent))
	}
	descriptor, err := unix.Openat(parent, basename, entryOpenFlags(), 0)
	if err != nil {
		return nil, -1, nil, errors.Join(classifyPathOperation("open scoped entry", err), closeDescriptor("close scoped entry parent", parent))
	}
	file, err := fileFromDescriptor(descriptor, "scoped entry")
	if err != nil {
		return nil, -1, nil, errors.Join(err, closeDescriptor("close scoped entry descriptor", descriptor), closeDescriptor("close scoped entry parent", parent))
	}
	info, err := file.Stat()
	if err != nil {
		return nil, -1, nil, errors.Join(fmt.Errorf("inspect scoped entry: %w", err), file.Close(), unix.Close(parent))
	}
	return file, parent, info, nil
}

func (r *ScopedRoot) openParent(ctx context.Context, path RelativePath) (int, string, error) {
	components, err := r.validatedComponents(path)
	if err != nil {
		return -1, "", err
	}
	current, err := r.duplicateRoot()
	if err != nil {
		return -1, "", err
	}
	for _, component := range components[:len(components)-1] {
		if err := ctx.Err(); err != nil {
			return -1, "", errors.Join(err, closeDescriptor("close canceled path component", current))
		}
		next, err := unix.Openat(current, component, directoryOpenFlags(), 0)
		if err != nil {
			return -1, "", errors.Join(classifyPathOperation("open path component", err), closeDescriptor("close failed path component", current))
		}
		if err := closeDescriptor("close path component", current); err != nil {
			return -1, "", errors.Join(err, closeDescriptor("close next path component", next))
		}
		current = next
	}
	return current, components[len(components)-1], nil
}

func (r *ScopedRoot) validatedComponents(path RelativePath) ([]string, error) {
	if r == nil || r.directory == nil || r.nameMax <= 0 {
		return nil, invalidPath("scoped root is unavailable")
	}
	validated, err := parseRelativePath(string(path), DefaultLimits(), r.nameMax)
	if err != nil {
		return nil, err
	}
	return strings.Split(string(validated), "/"), nil
}

func (r *ScopedRoot) duplicateRoot() (int, error) {
	if r == nil || r.directory == nil {
		return -1, fs.ErrClosed
	}
	rootDescriptor, err := descriptorFromFile(r.directory)
	if err != nil {
		return -1, err
	}
	descriptor, err := unix.Dup(rootDescriptor)
	if err != nil {
		return -1, fmt.Errorf("duplicate scoped root: %w", err)
	}
	unix.CloseOnExec(descriptor)
	return descriptor, nil
}

func (r *ScopedRoot) writeAndSync(ctx context.Context, file *os.File, content []byte) error {
	for written := 0; written < len(content); {
		if err := ctx.Err(); err != nil {
			return err
		}
		count, err := file.Write(content[written:])
		if err != nil {
			return fmt.Errorf("write regular file: %w", err)
		}
		if count == 0 {
			return fmt.Errorf("write regular file: %w", io.ErrShortWrite)
		}
		written += count
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	descriptor, err := descriptorFromFile(file)
	if err != nil {
		return err
	}
	if err := r.fsync(descriptor); err != nil {
		return fmt.Errorf("sync regular file: %w", err)
	}
	return nil
}

func (r *ScopedRoot) createTemporary(ctx context.Context, parent int, basename string, mode fs.FileMode) (string, *os.File, error) {
	for range temporaryAttempts {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		random := make([]byte, temporaryRandomBytes)
		if _, err := io.ReadFull(rand.Reader, random); err != nil {
			return "", nil, fmt.Errorf("generate temporary file name: %w", err)
		}
		name := "." + basename + ".nginx-uix-" + hex.EncodeToString(random)
		if len(name) > r.nameMax {
			return "", nil, invalidPath("temporary file name exceeds component limit")
		}
		descriptor, err := unix.Openat(parent, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(mode.Perm()))
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return "", nil, classifyPathOperation("create temporary file", err)
		}
		file, err := fileFromDescriptor(descriptor, "scoped temporary file")
		if err != nil {
			return "", nil, errors.Join(err, closeDescriptor("close scoped temporary descriptor", descriptor), unlinkTemporary(parent, name))
		}
		if err := verifyRegular(file); err != nil {
			return "", nil, errors.Join(err, file.Close(), unlinkTemporary(parent, name))
		}
		return name, file, nil
	}
	return "", nil, fmt.Errorf("create temporary file: %w", fs.ErrExist)
}

func (r *ScopedRoot) walkDirectory(ctx context.Context, descriptor int, prefix string, limit int, entries *[]RawEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	directoryEntries, err := readDirectoryEntries(descriptor, limit-len(*entries))
	if err != nil {
		return fmt.Errorf("read walk directory: %w", err)
	}
	sort.Slice(directoryEntries, func(left, right int) bool {
		return directoryEntries[left].Name() < directoryEntries[right].Name()
	})

	for _, directoryEntry := range directoryEntries {
		if err := ctx.Err(); err != nil {
			return err
		}
		rawPath := directoryEntry.Name()
		if prefix != "" {
			rawPath = prefix + "/" + rawPath
		}
		path, err := parseRelativePath(rawPath, DefaultLimits(), r.nameMax)
		if err != nil {
			return err
		}
		var stat unix.Stat_t
		if err := unix.Fstatat(descriptor, directoryEntry.Name(), &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return classifyPathOperation("inspect walked entry", err)
		}
		entryType, mode := entryTypeAndMode(uint32(stat.Mode))
		raw := RawEntry{Path: path, Type: entryType, Mode: mode, Size: stat.Size}
		switch entryType {
		case EntryRegular:
		case EntryDirectory:
			raw.LinkClass = EntryDirectoryReadOnly
		case EntrySymlink:
			raw.Size = 0
			raw.SafeLinkTarget, raw.LinkClass = r.classifySymlink(ctx, path, descriptor, directoryEntry.Name())
		case EntrySpecial:
			raw.Size = 0
			raw.LinkClass = EntrySpecialReadOnly
		}
		*entries = append(*entries, raw)
		if len(*entries) > limit {
			return fmt.Errorf("walk scoped root: %w", ErrLimitExceeded)
		}
		if entryType == EntryDirectory {
			child, err := unix.Openat(descriptor, directoryEntry.Name(), directoryOpenFlags(), 0)
			if err != nil {
				return classifyPathOperation("open walked directory", err)
			}
			if err := r.walkDirectory(ctx, child, rawPath, limit, entries); err != nil {
				return errors.Join(err, closeDescriptor("close failed walked directory", child))
			}
			if err := closeDescriptor("close walked directory", child); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *ScopedRoot) classifySymlink(ctx context.Context, linkPath RelativePath, parent int, basename string) (RelativePath, EntryClass) {
	target, err := readLinkAt(parent, basename, DefaultLimits().MaxPathBytes)
	if err != nil {
		return "", EntrySymlinkUnavailable
	}
	candidate, external, err := r.normalizeLinkTarget(linkPath, target)
	if err != nil {
		return "", EntrySymlinkUnavailable
	}
	if external {
		return "", EntrySymlinkExternal
	}
	resolved, class := r.resolveLinkTarget(ctx, candidate)
	return resolved, class
}

func (r *ScopedRoot) resolveLinkTarget(ctx context.Context, candidate RelativePath) (RelativePath, EntryClass) {
	seen := make(map[RelativePath]struct{})
	for hop := 0; hop < DefaultLimits().MaxIncludeDepth; hop++ {
		if err := ctx.Err(); err != nil {
			return "", EntrySymlinkUnavailable
		}
		components := strings.Split(string(candidate), "/")
		current, err := r.duplicateRoot()
		if err != nil {
			return "", EntrySymlinkUnavailable
		}
		resolved := make([]string, 0, len(components))
		restart := false
		for index, component := range components {
			var stat unix.Stat_t
			if err := unix.Fstatat(current, component, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				if closeDescriptor("close unresolved link target", current) != nil {
					return "", EntrySymlinkUnavailable
				}
				return "", EntrySymlinkUnavailable
			}
			entryType, _ := entryTypeAndMode(uint32(stat.Mode))
			if entryType == EntrySymlink {
				physical := RelativePath(strings.Join(append(resolved, component), "/"))
				if _, duplicate := seen[physical]; duplicate {
					if closeDescriptor("close cyclic link target", current) != nil {
						return "", EntrySymlinkUnavailable
					}
					return "", EntrySymlinkUnavailable
				}
				seen[physical] = struct{}{}
				target, err := readLinkAt(current, component, DefaultLimits().MaxPathBytes)
				closeErr := closeDescriptor("close resolved link parent", current)
				if err != nil || closeErr != nil {
					return "", EntrySymlinkUnavailable
				}
				next, external, err := r.normalizeLinkTarget(physical, target)
				if err != nil {
					return "", EntrySymlinkUnavailable
				}
				if external {
					return "", EntrySymlinkExternal
				}
				if index+1 < len(components) {
					joined, err := ParseRelativePath(path.Join(string(next), strings.Join(components[index+1:], "/")), DefaultLimits())
					if err != nil {
						return "", EntrySymlinkUnavailable
					}
					next = joined
				}
				candidate = next
				restart = true
				break
			}
			resolved = append(resolved, component)
			if index+1 < len(components) {
				if entryType != EntryDirectory {
					if closeDescriptor("close non-directory link target", current) != nil {
						return "", EntrySymlinkUnavailable
					}
					return "", EntrySymlinkUnavailable
				}
				next, err := unix.Openat(current, component, directoryOpenFlags(), 0)
				if err != nil {
					if closeDescriptor("close failed link component", current) != nil {
						return "", EntrySymlinkUnavailable
					}
					return "", EntrySymlinkUnavailable
				}
				if err := closeDescriptor("close link component", current); err != nil {
					if closeDescriptor("close next link component", next) != nil {
						return "", EntrySymlinkUnavailable
					}
					return "", EntrySymlinkUnavailable
				}
				current = next
			}
		}
		if restart {
			continue
		}
		if err := closeDescriptor("close resolved link target", current); err != nil {
			return "", EntrySymlinkUnavailable
		}
		return RelativePath(strings.Join(resolved, "/")), EntrySymlinkInternal
	}
	return "", EntrySymlinkUnavailable
}

func (r *ScopedRoot) normalizeLinkTarget(linkPath RelativePath, target string) (RelativePath, bool, error) {
	if !utf8.ValidString(target) || target == "" || strings.ContainsRune(target, '\x00') || strings.ContainsRune(target, '\\') {
		return "", false, invalidPath("symlink target is invalid")
	}
	if path.IsAbs(target) {
		rootPath, err := filepath.Abs(r.path)
		if err != nil {
			return "", false, err
		}
		relative, err := filepath.Rel(filepath.Clean(rootPath), filepath.Clean(target))
		if err != nil {
			return "", false, fmt.Errorf("resolve absolute symlink target: %w", err)
		}
		if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return "", true, nil
		}
		parsed, err := ParseRelativePath(filepath.ToSlash(relative), DefaultLimits())
		return parsed, false, err
	}
	joined := path.Join(path.Dir(string(linkPath)), target)
	if joined == "." || joined == ".." || strings.HasPrefix(joined, "../") {
		return "", true, nil
	}
	parsed, err := ParseRelativePath(joined, DefaultLimits())
	return parsed, false, err
}

func readLinkAt(parent int, basename string, limit int) (string, error) {
	if limit <= 0 {
		return "", ErrLimitExceeded
	}
	buffer := make([]byte, limit+1)
	count, err := unix.Readlinkat(parent, basename, buffer)
	if err != nil {
		return "", err
	}
	if count > limit {
		return "", ErrLimitExceeded
	}
	return string(buffer[:count]), nil
}

func entryTypeAndMode(rawMode uint32) (EntryType, fs.FileMode) {
	mode := fs.FileMode(rawMode & 0o777)
	switch rawMode & unix.S_IFMT {
	case unix.S_IFREG:
		return EntryRegular, mode
	case unix.S_IFDIR:
		return EntryDirectory, mode | fs.ModeDir
	case unix.S_IFLNK:
		return EntrySymlink, mode | fs.ModeSymlink
	case unix.S_IFIFO:
		return EntrySpecial, mode | fs.ModeNamedPipe
	case unix.S_IFSOCK:
		return EntrySpecial, mode | fs.ModeSocket
	case unix.S_IFCHR:
		return EntrySpecial, mode | fs.ModeDevice | fs.ModeCharDevice
	case unix.S_IFBLK:
		return EntrySpecial, mode | fs.ModeDevice
	default:
		return EntrySpecial, mode | fs.ModeIrregular
	}
}

func readDirectoryEntries(descriptor, remaining int) ([]os.DirEntry, error) {
	if remaining < 0 {
		return nil, ErrLimitExceeded
	}
	readDescriptor, err := unix.Openat(descriptor, ".", directoryOpenFlags(), 0)
	if err != nil {
		return nil, classifyPathOperation("open bounded directory", err)
	}
	directory, err := fileFromDescriptor(readDescriptor, "scoped bounded directory")
	if err != nil {
		return nil, errors.Join(err, closeDescriptor("close bounded directory descriptor", readDescriptor))
	}
	entries, readErr := directory.ReadDir(remaining + 1)
	if errors.Is(readErr, io.EOF) {
		readErr = nil
	}
	closeErr := directory.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	if len(entries) > remaining {
		return nil, ErrLimitExceeded
	}
	return entries, nil
}

func readBounded(ctx context.Context, file *os.File, limit int64) ([]byte, error) {
	content := make([]byte, 0, minInt64(limit, 32<<10))
	buffer := make([]byte, 32<<10)
	remaining := limit
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		readBuffer := buffer
		if remaining < int64(len(readBuffer)) {
			readBuffer = readBuffer[:remaining+1]
		}
		count, err := file.Read(readBuffer)
		if int64(count) > remaining {
			return nil, fmt.Errorf("read regular file: %w", ErrLimitExceeded)
		}
		content = append(content, readBuffer[:count]...)
		remaining -= int64(count)
		if errors.Is(err, io.EOF) {
			return content, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read regular file: %w", err)
		}
	}
}

func unlinkTemporary(parent int, name string) error {
	if err := unix.Unlinkat(parent, name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("clean temporary file: %w", err)
	}
	return nil
}

func verifyRegular(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect created regular file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return invalidPath("created entry is not a regular file")
	}
	return nil
}

func classifyPathOperation(action string, err error) error {
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ENXIO) || errors.Is(err, unix.EOPNOTSUPP) {
		return fmt.Errorf("%s: %w: %w", action, ErrPathInvalid, err)
	}
	return fmt.Errorf("%s: %w", action, err)
}

func fileFromDescriptor(descriptor int, name string) (*os.File, error) {
	if descriptor < 0 {
		return nil, invalidPath("file descriptor is invalid")
	}
	file := os.NewFile(uintptr(descriptor), name)
	if file == nil {
		return nil, errors.New("open file descriptor")
	}
	return file, nil
}

func descriptorFromFile(file *os.File) (int, error) {
	if file == nil {
		return -1, fs.ErrClosed
	}
	descriptor := file.Fd()
	if descriptor > ^uintptr(0)>>1 {
		return -1, invalidPath("file descriptor exceeds platform range")
	}
	return int(descriptor), nil
}

func closeDescriptor(action string, descriptor int) error {
	if descriptor < 0 {
		return fmt.Errorf("%s: invalid file descriptor", action)
	}
	if err := unix.Close(descriptor); err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	return nil
}

func directoryOpenFlags() int {
	return unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW
}

func entryOpenFlags() int {
	return unix.O_RDONLY | unix.O_NONBLOCK | unix.O_CLOEXEC | unix.O_NOFOLLOW
}

func minInt64(left int64, right int) int {
	if left < int64(right) {
		return int(left)
	}
	return right
}
