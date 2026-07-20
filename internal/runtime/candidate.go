/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.2
 */
package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

const (
	candidateValidationTimeout = 60 * time.Second
	candidateDiagnosticLimit   = 1 << 20
	candidateValidatorVersion  = uint16(1)
	candidateFileLimit         = int64(64 << 20)
)

type candidateOptions struct {
	NginxRoot     string
	WorkspaceRoot string
	StageRoot     string
	Entry         config.RelativePath
	Limits        config.Limits
	Executor      commandExecutor
}

func defaultCandidateOptions() candidateOptions {
	return candidateOptions{
		NginxRoot: defaultConfigNginxRoot, WorkspaceRoot: defaultConfigWorkspaceRoot,
		StageRoot: "/var/lib/nginx-uix/releases", Entry: "nginx.conf",
		Limits: config.DefaultLimits(), Executor: executeCommand,
	}
}

func newCandidateService(options candidateOptions) (*Service, error) {
	if err := validateCandidateOptions(options); err != nil {
		return nil, err
	}
	service := newServiceWithExecutor(options.Executor)
	service.candidate = options
	service.configSnapshot.NginxRoot = options.NginxRoot
	service.configSnapshot.WorkspaceRoot = options.WorkspaceRoot
	service.configSnapshot.Entry = options.Entry
	service.configSnapshot.Limits = options.Limits
	return service, nil
}

func validateCandidateOptions(options candidateOptions) error {
	if options.Executor == nil {
		return errors.New("configure candidate validator: executor is required")
	}
	for _, root := range []string{options.NginxRoot, options.WorkspaceRoot, options.StageRoot} {
		if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
			return fmt.Errorf("configure candidate validator root: %w", config.ErrPathInvalid)
		}
		information, err := os.Lstat(root)
		if err != nil || information.Mode()&fs.ModeSymlink != 0 || !information.IsDir() {
			return errors.Join(fmt.Errorf("configure candidate validator root: %w", config.ErrPathInvalid), err)
		}
	}
	if options.NginxRoot == options.WorkspaceRoot || options.NginxRoot == options.StageRoot || options.WorkspaceRoot == options.StageRoot {
		return fmt.Errorf("configure candidate validator roots: %w", config.ErrPathInvalid)
	}
	if _, err := config.ParseRelativePath(string(options.Entry), options.Limits); err != nil {
		return fmt.Errorf("configure candidate entry: %w", err)
	}
	return nil
}

// ValidateCandidate materializes, validates, and removes one complete root-only candidate tree.
func (s *Service) ValidateCandidate(ctx context.Context, request config.CandidateValidationRequest) (_ config.CandidateValidation, returnErr error) {
	if ctx == nil || s == nil {
		return config.CandidateValidation{}, errors.New("validate candidate: service is unavailable")
	}
	if _, err := config.ParseWorkspaceID(string(request.WorkspaceID)); err != nil || request.ProductionDigest == (config.Digest{}) || request.DraftDigest == (config.Digest{}) {
		return config.CandidateValidation{}, errors.Join(fmt.Errorf("validate candidate request: %w", config.ErrDigestInvalid), err)
	}
	select {
	case s.candidateLock <- struct{}{}:
		defer func() { <-s.candidateLock }()
	case <-ctx.Done():
		return config.CandidateValidation{}, fmt.Errorf("wait for candidate validation slot: %w", ctx.Err())
	}
	options := s.candidate
	if options.Executor == nil {
		options = defaultCandidateOptions()
	}
	if err := validateCandidateOptions(options); err != nil {
		return config.CandidateValidation{}, err
	}
	operationCtx, cancel := context.WithTimeout(ctx, candidateValidationTimeout)
	defer cancel()

	productionRoot, err := config.OpenScopedRoot(options.NginxRoot)
	if err != nil {
		return config.CandidateValidation{}, fmt.Errorf("open candidate production root: %w", err)
	}
	productionInventory, inventoryErr := config.BuildInventory(operationCtx, productionRoot, config.SnapshotOptions{
		Entry: options.Entry, Limits: options.Limits, Policy: config.NewPolicy(), FileMode: 0o400, DirectoryMode: 0o700,
	})
	if inventoryErr != nil || productionInventory.Digest != request.ProductionDigest {
		return config.CandidateValidation{}, errors.Join(fmt.Errorf("verify candidate production digest: %w", config.ErrSnapshotChanged), inventoryErr, productionRoot.Close())
	}

	workspacePath := filepath.Join(options.WorkspaceRoot, string(request.WorkspaceID))
	workspaceRoot, err := config.OpenScopedRoot(workspacePath)
	if err != nil {
		return config.CandidateValidation{}, errors.Join(fmt.Errorf("open candidate workspace: %w", err), productionRoot.Close())
	}
	state, stateErr := config.ReadControlState(operationCtx, workspaceRoot)
	manifest, manifestErr := config.ReadControlManifest(operationCtx, workspaceRoot, options.Limits)
	workspaceCloseErr := workspaceRoot.Close()
	if stateErr != nil || manifestErr != nil || workspaceCloseErr != nil {
		return config.CandidateValidation{}, errors.Join(fmt.Errorf("read candidate workspace control: %w", config.ErrConflict), stateErr, manifestErr, workspaceCloseErr, productionRoot.Close())
	}
	if state.WorkspaceID != request.WorkspaceID || state.State != config.StateReady || manifest.Digest() != request.DraftDigest {
		return config.CandidateValidation{}, errors.Join(fmt.Errorf("verify candidate workspace binding: %w", config.ErrConflict), productionRoot.Close())
	}
	if err := verifyCandidateDraft(operationCtx, workspacePath, manifest, request.DraftDigest, options.Limits); err != nil {
		return config.CandidateValidation{}, errors.Join(fmt.Errorf("verify candidate draft: %w", err), productionRoot.Close())
	}

	stagePath, err := os.MkdirTemp(options.StageRoot, ".candidate-")
	if err != nil {
		return config.CandidateValidation{}, errors.Join(fmt.Errorf("create candidate stage: %w", err), productionRoot.Close())
	}
	// #nosec G302 -- the candidate is an owner-only directory and requires execute permission for traversal.
	if err := os.Chmod(stagePath, 0o700); err != nil {
		return config.CandidateValidation{}, errors.Join(fmt.Errorf("protect candidate stage: %w", err), productionRoot.Close(), os.RemoveAll(stagePath))
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapCandidateCleanup(os.RemoveAll(stagePath)))
	}()

	if err := copyCandidateProduction(operationCtx, productionRoot, stagePath, options.Limits); err != nil {
		return config.CandidateValidation{}, errors.Join(fmt.Errorf("copy complete candidate: %w", err), productionRoot.Close())
	}
	if err := productionRoot.Close(); err != nil {
		return config.CandidateValidation{}, fmt.Errorf("close candidate production root: %w", err)
	}
	if err := overlayCandidateDraft(operationCtx, stagePath, workspacePath, productionInventory.Manifest, manifest, options.Limits); err != nil {
		return config.CandidateValidation{}, fmt.Errorf("overlay candidate draft: %w", err)
	}
	candidateDigest, err := digestCandidateTree(operationCtx, stagePath, options.Limits)
	if err != nil {
		return config.CandidateValidation{}, fmt.Errorf("digest candidate tree: %w", err)
	}
	if err := isolateCandidateIncludes(operationCtx, stagePath, options.NginxRoot, manifest, options.Limits); err != nil {
		return config.CandidateValidation{}, fmt.Errorf("isolate candidate includes: %w", err)
	}
	build, err := s.BuildInfo(operationCtx)
	if err != nil {
		return config.CandidateValidation{}, fmt.Errorf("bind candidate validator build: %w", err)
	}
	result := config.CandidateValidation{
		CandidateDigest: candidateDigest, ValidatorVersion: candidateValidatorVersion,
		ValidatorBuildID: validatorBuildIdentity(build), CheckedAt: s.now().UTC(),
	}
	commandResult, commandErr := options.Executor(operationCtx, commandSpec{
		executable: nginxExecutable,
		arguments:  []string{"-t", "-p", stagePath + string(filepath.Separator), "-c", filepath.Join(stagePath, filepath.FromSlash(string(options.Entry)))},
		timeout:    candidateValidationTimeout, maxOutputBytes: candidateDiagnosticLimit,
		allowedExitCodes: map[int]struct{}{0: {}},
	})
	if commandErr != nil {
		var exitErr *commandExitError
		if errors.As(commandErr, &exitErr) {
			result.Diagnostics = parseCandidateDiagnostics(commandResult.stderr, stagePath, options.Limits)
			if len(result.Diagnostics) == 0 {
				result.Diagnostics = []config.CandidateDiagnostic{{Code: "nginx_config_invalid", Summary: "Nginx rejected the candidate configuration"}}
			}
			return result, fmt.Errorf("validate candidate nginx configuration: %w: %w", ErrConfigInvalid, commandErr)
		}
		return config.CandidateValidation{}, fmt.Errorf("validate candidate nginx configuration: %w", commandErr)
	}
	result.Valid = true
	return result, nil
}

func verifyCandidateDraft(ctx context.Context, workspacePath string, manifest config.Manifest, want config.Digest, limits config.Limits) (returnErr error) {
	if manifest.Digest() != want {
		return config.ErrConflict
	}
	root, err := config.OpenScopedRoot(filepath.Join(workspacePath, "draft"))
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, root.Close())
	}()
	entries, err := root.Walk(ctx, limits.MaxEntries)
	if err != nil {
		return err
	}
	expected := make(map[config.RelativePath]config.Entry)
	for _, entry := range manifest.Entries {
		if entry.Type == config.EntryDirectory || entry.Class == config.EntryManagedText {
			expected[entry.Path] = entry
		}
	}
	if len(entries) != len(expected) {
		return config.ErrConflict
	}
	for _, raw := range entries {
		entry, ok := expected[raw.Path]
		if !ok || raw.Type != entry.Type {
			return config.ErrConflict
		}
		if raw.Type == config.EntryDirectory {
			if raw.Mode.Perm() != 0o700 {
				return config.ErrPathInvalid
			}
			continue
		}
		contents, information, err := root.ReadRegular(ctx, raw.Path, limits.MaxFileBytes)
		if err != nil || information.Mode().Perm() != 0o600 || int64(len(contents)) != entry.Size || config.Digest(sha256.Sum256(contents)) != entry.ContentDigest {
			return errors.Join(config.ErrConflict, err)
		}
	}
	return nil
}

func copyCandidateProduction(ctx context.Context, source *config.ScopedRoot, target string, limits config.Limits) error {
	entries, err := source.Walk(ctx, limits.MaxEntries)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		destination := filepath.Join(target, filepath.FromSlash(string(entry.Path)))
		switch entry.Type {
		case config.EntryDirectory:
			if err := os.Mkdir(destination, entry.Mode.Perm()); err != nil {
				return err
			}
		case config.EntryRegular:
			limit := min(candidateFileLimit, limits.MaxWorkspaceBytes)
			contents, information, err := source.ReadRegular(ctx, entry.Path, limit)
			if err != nil || information.Mode().Perm() != entry.Mode.Perm() || int64(len(contents)) != entry.Size {
				return errors.Join(config.ErrSnapshotChanged, err)
			}
			if err := os.WriteFile(destination, contents, 0o600); err != nil {
				return err
			}
			if err := os.Chmod(destination, entry.Mode.Perm()); err != nil {
				return err
			}
		case config.EntrySymlink:
			if entry.LinkClass != config.EntrySymlinkInternal || entry.SafeLinkTarget == "" {
				return config.ErrPathInvalid
			}
			relative, err := filepath.Rel(filepath.Dir(destination), filepath.Join(target, filepath.FromSlash(string(entry.SafeLinkTarget))))
			if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return config.ErrPathInvalid
			}
			if err := os.Symlink(relative, destination); err != nil {
				return err
			}
		case config.EntrySpecial:
			return config.ErrPathInvalid
		default:
			return config.ErrPathInvalid
		}
	}
	return nil
}

func overlayCandidateDraft(ctx context.Context, candidate, workspace string, production, draft config.Manifest, limits config.Limits) (returnErr error) {
	draftManaged := make(map[config.RelativePath]config.Entry)
	for _, entry := range draft.Entries {
		if entry.Type == config.EntryDirectory {
			path := filepath.Join(candidate, filepath.FromSlash(string(entry.Path)))
			if err := os.MkdirAll(path, 0o700); err != nil {
				return err
			}
		}
		if entry.Class == config.EntryManagedText {
			draftManaged[entry.Path] = entry
		}
	}
	for _, entry := range production.Entries {
		if entry.Class != config.EntryManagedText {
			continue
		}
		if _, retained := draftManaged[entry.Path]; !retained {
			if err := os.Remove(filepath.Join(candidate, filepath.FromSlash(string(entry.Path)))); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	draftRoot, err := config.OpenScopedRoot(filepath.Join(workspace, "draft"))
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, draftRoot.Close())
	}()
	paths := make([]config.RelativePath, 0, len(draftManaged))
	for path := range draftManaged {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		entry := draftManaged[path]
		contents, _, err := draftRoot.ReadRegular(ctx, path, limits.MaxFileBytes)
		if err != nil || int64(len(contents)) != entry.Size || config.Digest(sha256.Sum256(contents)) != entry.ContentDigest {
			return errors.Join(config.ErrConflict, err)
		}
		destination := filepath.Join(candidate, filepath.FromSlash(string(path)))
		mode := fs.FileMode(0o640)
		if information, statErr := os.Lstat(destination); statErr == nil {
			if !information.Mode().IsRegular() {
				return config.ErrPathInvalid
			}
			mode = information.Mode().Perm()
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return statErr
		}
		if err := os.WriteFile(destination, contents, mode); err != nil {
			return err
		}
		if err := os.Chmod(destination, mode); err != nil {
			return err
		}
	}
	return nil
}

func isolateCandidateIncludes(ctx context.Context, candidate, production string, manifest config.Manifest, limits config.Limits) (returnErr error) {
	root, err := config.OpenScopedRoot(candidate)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, root.Close())
	}()
	for _, entry := range manifest.Entries {
		if entry.Class != config.EntryManagedText {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		contents, information, err := root.ReadRegular(ctx, entry.Path, limits.MaxFileBytes)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		directives, err := config.ScanDirectives(contents, limits)
		if err != nil {
			return err
		}
		rewritten := string(contents)
		for _, directive := range directives {
			if directive.Name != "include" || len(directive.Arguments) != 1 {
				continue
			}
			argument := directive.Arguments[0]
			if strings.Contains(argument, "$") {
				return config.ErrPathInvalid
			}
			if !filepath.IsAbs(argument) {
				continue
			}
			clean := filepath.Clean(argument)
			if clean != production && !strings.HasPrefix(clean, production+string(filepath.Separator)) {
				return config.ErrPathInvalid
			}
			relative, err := filepath.Rel(production, clean)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return config.ErrPathInvalid
			}
			replacement := filepath.Join(candidate, relative)
			rewritten = strings.ReplaceAll(rewritten, argument, replacement)
		}
		if rewritten != string(contents) {
			if err := root.AtomicReplace(ctx, entry.Path, []byte(rewritten), information.Mode().Perm()); err != nil {
				return err
			}
		}
	}
	return nil
}

func digestCandidateTree(ctx context.Context, rootPath string, limits config.Limits) (digest config.Digest, returnErr error) {
	root, err := config.OpenScopedRoot(rootPath)
	if err != nil {
		return config.Digest{}, err
	}
	defer func() {
		returnErr = errors.Join(returnErr, root.Close())
	}()
	entries, err := root.Walk(ctx, limits.MaxEntries)
	if err != nil {
		return config.Digest{}, err
	}
	hash := sha256.New()
	var length [8]byte
	for _, entry := range entries {
		if entry.Type == config.EntrySpecial || entry.Type == config.EntrySymlink && entry.LinkClass != config.EntrySymlinkInternal {
			return config.Digest{}, config.ErrPathInvalid
		}
		fields := []string{string(entry.Path), string(entry.Type), strconv.FormatUint(uint64(entry.Mode.Perm()), 8), string(entry.SafeLinkTarget)}
		for _, field := range fields {
			binary.BigEndian.PutUint64(length[:], uint64(len(field)))
			_, _ = hash.Write(length[:])
			_, _ = hash.Write([]byte(field))
		}
		if entry.Type == config.EntryRegular {
			contents, _, err := root.ReadRegular(ctx, entry.Path, min(candidateFileLimit, limits.MaxWorkspaceBytes))
			if err != nil {
				return config.Digest{}, err
			}
			binary.BigEndian.PutUint64(length[:], uint64(len(contents)))
			_, _ = hash.Write(length[:])
			_, _ = hash.Write(contents)
		}
	}
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

func validatorBuildIdentity(build BuildInfo) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(build.Version))
	for _, argument := range build.ConfigureArguments {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(argument))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func parseCandidateDiagnostics(stderr []byte, candidate string, limits config.Limits) []config.CandidateDiagnostic {
	text := sanitizeDiagnostic(stderr)
	lines := strings.Split(text, "\n")
	diagnostics := make([]config.CandidateDiagnostic, 0, 1)
	for _, line := range lines {
		marker := " in " + candidate + string(filepath.Separator)
		index := strings.Index(line, marker)
		if index < 0 {
			continue
		}
		location := line[index+len(marker):]
		pathRaw, lineRaw, found := strings.Cut(location, ":")
		if !found {
			continue
		}
		lineNumber, err := strconv.Atoi(strings.Fields(lineRaw)[0])
		path, pathErr := config.ParseRelativePath(filepath.ToSlash(pathRaw), limits)
		if err != nil || lineNumber <= 0 || pathErr != nil {
			continue
		}
		summary := strings.TrimSpace(line[:index])
		if len(summary) > 512 {
			summary = summary[:512]
		}
		diagnostics = append(diagnostics, config.CandidateDiagnostic{Code: "nginx_config_invalid", Path: path, Line: lineNumber, Summary: summary})
		break
	}
	return diagnostics
}

func wrapCandidateCleanup(err error) error {
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("clean candidate stage: %w", err)
}
