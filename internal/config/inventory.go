/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"slices"
)

// Inventory is one complete bounded root manifest and its canonical digest.
type Inventory struct {
	Manifest Manifest
	Digest   Digest

	managedContent map[RelativePath][]byte
}

// SnapshotOptions fixes the entry, resource policy, modes, and optional owner.
type SnapshotOptions struct {
	Entry         RelativePath
	Limits        Limits
	Policy        Policy
	FileMode      fs.FileMode
	DirectoryMode fs.FileMode
	Owner         *Owner
}

// Owner is the numeric ownership applied to newly created snapshot entries.
type Owner struct {
	UID int
	GID int
}

// Snapshot describes one stable, independently verified immutable base tree.
type Snapshot struct {
	Manifest         Manifest
	ProductionDigest Digest
	BaseDigest       Digest
}

// BuildInventory performs one bounded traversal and finalizes the complete include-aware manifest.
func BuildInventory(ctx context.Context, root *ScopedRoot, options SnapshotOptions) (Inventory, error) {
	if err := validateSnapshotOptions(options); err != nil {
		return Inventory{}, err
	}
	if err := ctx.Err(); err != nil {
		return Inventory{}, err
	}
	rawEntries, err := root.Walk(ctx, options.Limits.MaxEntries)
	if err != nil {
		return Inventory{}, fmt.Errorf("build inventory: %w", err)
	}
	rawByPath := make(map[RelativePath]RawEntry, len(rawEntries))
	for _, raw := range rawEntries {
		rawByPath[raw.Path] = raw
	}
	entryRaw, ok := rawByPath[options.Entry]
	if !ok || entryRaw.Type != EntryRegular {
		return Inventory{}, fmt.Errorf("build inventory entry: %w", ErrEntryNotManaged)
	}

	contents := make(map[RelativePath][]byte)
	loaded := make(map[RelativePath]struct{})
	readCandidate := func(readCtx context.Context, candidate RelativePath) ([]byte, error) {
		if _, ok := loaded[candidate]; ok {
			return contents[candidate], nil
		}
		raw, ok := rawByPath[candidate]
		if !ok || raw.Type != EntryRegular {
			return nil, fmt.Errorf("read inventory candidate: %w", ErrEntryNotManaged)
		}
		loaded[candidate] = struct{}{}
		if hasSensitiveSuffix(candidate) || raw.Size > options.Limits.MaxFileBytes {
			return nil, nil
		}
		content, info, err := root.ReadRegular(readCtx, candidate, options.Limits.MaxFileBytes)
		if err != nil {
			if errors.Is(err, ErrLimitExceeded) {
				return nil, errors.Join(ErrSnapshotChanged, err)
			}
			return nil, err
		}
		if info.Size() != raw.Size || info.Mode().Perm() != raw.Mode.Perm() || int64(len(content)) != raw.Size {
			return nil, ErrSnapshotChanged
		}
		contents[candidate] = content
		return content, nil
	}

	for _, raw := range rawEntries {
		if raw.Type != EntryRegular || !options.Policy.IsPositiveCandidate(raw.Path, false) {
			continue
		}
		if _, err := readCandidate(ctx, raw.Path); err != nil {
			return Inventory{}, fmt.Errorf("read initial inventory candidate: %w", err)
		}
	}
	readGraphCandidate := func(readCtx context.Context, candidate RelativePath) ([]byte, error) {
		content, err := readCandidate(readCtx, candidate)
		if err != nil {
			return nil, err
		}
		if options.Policy.Classify(candidate, content, false, true) != EntryManagedText {
			return nil, nil
		}
		return content, nil
	}
	graph, included, sensitive, err := ExpandIncludeGraph(ctx, options.Entry, rawEntries, readGraphCandidate, options.Limits)
	if err != nil {
		return Inventory{}, fmt.Errorf("build inventory include graph: %w", err)
	}

	entries := make([]Entry, 0, len(rawEntries))
	managedContent := make(map[RelativePath][]byte)
	for _, raw := range rawEntries {
		if err := ctx.Err(); err != nil {
			return Inventory{}, err
		}
		entry := Entry{Path: raw.Path, Type: raw.Type}
		switch raw.Type {
		case EntryRegular:
			entry.Mode = raw.Mode.Perm()
			entry.Size = raw.Size
			_, referencedSensitive := sensitive[raw.Path]
			_, isIncluded := included[raw.Path]
			switch {
			case referencedSensitive || hasSensitiveSuffix(raw.Path):
				entry.Class = EntrySensitiveMaterial
			case raw.Size > options.Limits.MaxFileBytes:
				entry.Class = EntryFileLimit
			case options.Policy.IsPositiveCandidate(raw.Path, isIncluded):
				content, err := readCandidate(ctx, raw.Path)
				if err != nil {
					return Inventory{}, fmt.Errorf("read managed inventory candidate: %w", err)
				}
				entry.Class = options.Policy.Classify(raw.Path, content, false, isIncluded)
				if entry.Class == EntryManagedText {
					entry.ContentDigest = Digest(sha256.Sum256(content))
					managedContent[raw.Path] = content
				}
			default:
				entry.Class = EntryNotCandidate
			}
		case EntryDirectory:
			entry.Class = EntryDirectoryReadOnly
		case EntrySymlink:
			entry.Class = raw.LinkClass
			entry.SafeLinkTarget = raw.SafeLinkTarget
		case EntrySpecial:
			entry.Class = EntrySpecialReadOnly
		default:
			return Inventory{}, errors.New("build inventory: unknown raw entry type")
		}
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, compareEntries)
	dependencies := slices.Clone(graph.Edges)
	for index, dependency := range dependencies {
		canonical, err := canonicalDependency(dependency, options.Limits)
		if err != nil {
			return Inventory{}, fmt.Errorf("build inventory dependency: %w", err)
		}
		dependencies[index] = canonical
	}
	slices.SortFunc(dependencies, compareDependencies)
	var managedBytes int64
	for _, entry := range entries {
		if entry.Class == EntryManagedText {
			if entry.Size > math.MaxInt64-managedBytes {
				return Inventory{}, fmt.Errorf("build inventory managed bytes: %w", ErrLimitExceeded)
			}
			managedBytes += entry.Size
		}
	}
	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion,
		PolicyVersion: options.Policy.Version(),
		Entries:       entries,
		Dependencies:  dependencies,
		EntryCount:    len(entries),
		ManagedBytes:  managedBytes,
	}
	if err := manifest.Validate(options.Limits); err != nil {
		return Inventory{}, err
	}
	digest := manifest.Digest()
	if digest == (Digest{}) {
		return Inventory{}, errors.New("build inventory: canonical digest unavailable")
	}
	return Inventory{Manifest: manifest, Digest: digest, managedContent: managedContent}, nil
}

// DigestRoot returns one production summary from one complete inventory.
func DigestRoot(ctx context.Context, root *ScopedRoot, options SnapshotOptions) (ProductionState, error) {
	inventory, err := BuildInventory(ctx, root, options)
	if err != nil {
		return ProductionState{}, err
	}
	return ProductionState{
		Digest:          inventory.Digest,
		ManifestVersion: inventory.Manifest.SchemaVersion,
		EntryCount:      inventory.Manifest.EntryCount,
		ManagedBytes:    inventory.Manifest.ManagedBytes,
	}, nil
}

// SnapshotTo copies only managed text into an empty target and verifies both roots independently.
func SnapshotTo(ctx context.Context, source, target *ScopedRoot, options SnapshotOptions) (_ Snapshot, returnErr error) {
	if err := validateSnapshotOptions(options); err != nil {
		return Snapshot{}, err
	}
	first, err := BuildInventory(ctx, source, options)
	if err != nil {
		return Snapshot{}, err
	}
	targetEntries, err := target.Walk(ctx, options.Limits.MaxEntries)
	if err != nil {
		return Snapshot{}, fmt.Errorf("inspect snapshot target: %w", err)
	}
	if len(targetEntries) != 0 {
		return Snapshot{}, fmt.Errorf("snapshot target is not empty: %w", fs.ErrExist)
	}

	stageOwned := false
	complete := false
	defer func() {
		if !stageOwned || complete {
			return
		}
		cleanupErr := target.clear(context.WithoutCancel(ctx), options.Limits.MaxEntries)
		returnErr = errors.Join(returnErr, cleanupErr)
	}()
	for _, entry := range first.Manifest.Entries {
		if entry.Type != EntryDirectory {
			continue
		}
		stageOwned = true
		if err := target.ensureDirectoryOwned(ctx, entry.Path, options.DirectoryMode, options.Owner); err != nil {
			return Snapshot{}, fmt.Errorf("create snapshot directory: %w", err)
		}
	}
	for _, entry := range first.Manifest.Entries {
		if entry.Class != EntryManagedText {
			continue
		}
		content, ok := first.managedContent[entry.Path]
		if !ok || Digest(sha256.Sum256(content)) != entry.ContentDigest {
			return Snapshot{}, ErrSnapshotChanged
		}
		stageOwned = true
		if err := target.createRegularOwned(ctx, entry.Path, content, options.FileMode, options.Owner); err != nil {
			return Snapshot{}, fmt.Errorf("copy snapshot file: %w", err)
		}
	}

	second, err := BuildInventory(ctx, source, options)
	if err != nil {
		if ctx.Err() != nil {
			return Snapshot{}, ctx.Err()
		}
		return Snapshot{}, errors.Join(ErrSnapshotChanged, err)
	}
	if first.Digest != second.Digest {
		return Snapshot{}, ErrSnapshotChanged
	}
	baseDigest, err := digestSnapshotTarget(ctx, target, first.Manifest, options)
	if err != nil {
		if ctx.Err() != nil {
			return Snapshot{}, ctx.Err()
		}
		return Snapshot{}, errors.Join(ErrSnapshotChanged, err)
	}
	if baseDigest != first.Digest {
		return Snapshot{}, ErrSnapshotChanged
	}
	complete = true
	return Snapshot{Manifest: first.Manifest, ProductionDigest: first.Digest, BaseDigest: baseDigest}, nil
}

func digestSnapshotTarget(ctx context.Context, target *ScopedRoot, manifest Manifest, options SnapshotOptions) (Digest, error) {
	rawEntries, err := target.Walk(ctx, options.Limits.MaxEntries)
	if err != nil {
		return Digest{}, err
	}
	expected := make(map[RelativePath]EntryType)
	for _, entry := range manifest.Entries {
		if entry.Type == EntryDirectory || entry.Class == EntryManagedText {
			expected[entry.Path] = entry.Type
		}
	}
	if len(rawEntries) != len(expected) {
		return Digest{}, errors.New("digest snapshot target: unexpected entry count")
	}
	for _, raw := range rawEntries {
		if expected[raw.Path] != raw.Type {
			return Digest{}, errors.New("digest snapshot target: unexpected entry")
		}
		if raw.Type == EntryDirectory && raw.Mode.Perm() != options.DirectoryMode.Perm() {
			return Digest{}, errors.New("digest snapshot target: directory mode changed")
		}
	}
	baseManifest := manifest
	baseManifest.Entries = slices.Clone(manifest.Entries)
	baseManifest.Dependencies = slices.Clone(manifest.Dependencies)
	for index, entry := range baseManifest.Entries {
		if entry.Class != EntryManagedText {
			continue
		}
		content, info, err := target.ReadRegular(ctx, entry.Path, options.Limits.MaxFileBytes)
		if err != nil {
			return Digest{}, err
		}
		if info.Mode().Perm() != options.FileMode.Perm() || int64(len(content)) != entry.Size {
			return Digest{}, errors.New("digest snapshot target: file metadata changed")
		}
		baseManifest.Entries[index].ContentDigest = Digest(sha256.Sum256(content))
	}
	if err := baseManifest.Validate(options.Limits); err != nil {
		return Digest{}, err
	}
	return baseManifest.Digest(), nil
}

func validateSnapshotOptions(options SnapshotOptions) error {
	if _, err := ParseRelativePath(string(options.Entry), options.Limits); err != nil {
		return fmt.Errorf("validate snapshot entry: %w", err)
	}
	if options.Policy.Version() != policyVersion {
		return errors.New("validate snapshot policy: unsupported version")
	}
	if options.FileMode&^fs.ModePerm != 0 || options.DirectoryMode&^fs.ModePerm != 0 {
		return invalidPath("snapshot mode is invalid")
	}
	if options.Owner != nil && (options.Owner.UID < 0 || options.Owner.GID < 0) {
		return invalidPath("snapshot owner is invalid")
	}
	if options.Limits.MaxFileBytes <= 0 || options.Limits.MaxEntries <= 0 || options.Limits.MaxManagedBytes < 0 {
		return fmt.Errorf("validate snapshot limits: %w", ErrLimitExceeded)
	}
	return nil
}
