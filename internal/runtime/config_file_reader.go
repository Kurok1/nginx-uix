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
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type effectiveConfigRoot struct {
	declared string
	resolved string
}

func normalizeEffectiveConfigRoots(roots []string) ([]effectiveConfigRoot, error) {
	normalized := make([]effectiveConfigRoot, 0, len(roots))
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		if !filepath.IsAbs(root) {
			return nil, fmt.Errorf("normalize effective configuration root %q: absolute path required", root)
		}
		root = filepath.Clean(root)
		if root == string(filepath.Separator) {
			return nil, fmt.Errorf("normalize effective configuration root %q: filesystem root is forbidden", root)
		}
		declaredRoot := root
		resolvedRoot, err := filepath.EvalSymlinks(declaredRoot)
		switch {
		case err == nil:
			info, statErr := os.Stat(resolvedRoot)
			if statErr != nil {
				return nil, fmt.Errorf("inspect effective configuration root %q: %w", root, statErr)
			}
			if !info.IsDir() {
				return nil, fmt.Errorf("normalize effective configuration root %q: directory required", root)
			}
		case errors.Is(err, os.ErrNotExist):
			// A built-in root may be created later during container initialization.
			resolvedRoot = declaredRoot
		default:
			return nil, fmt.Errorf("resolve effective configuration root %q: %w", root, err)
		}
		key := declaredRoot + "\x00" + resolvedRoot
		if _, found := seen[key]; found {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, effectiveConfigRoot{declared: declaredRoot, resolved: resolvedRoot})
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("normalize effective configuration roots: at least one root is required")
	}
	sort.Slice(normalized, func(left, right int) bool {
		return normalized[left].declared < normalized[right].declared
	})
	return normalized, nil
}

func newNginxConfigFileReader(allowedRoots []effectiveConfigRoot) configFileReader {
	allowedRoots = append([]effectiveConfigRoot(nil), allowedRoots...)
	return func(ctx context.Context, configPath string) (contents []byte, returnErr error) {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("read nginx configuration file: %w", err)
		}
		configPath = filepath.Clean(configPath)
		if !filepath.IsAbs(configPath) || !isInsideAllowedRootAlias(allowedRoots, configPath) {
			return nil, fmt.Errorf("validate nginx configuration path %q: %w", configPath, ErrConfigPathOutsideAllowedRoots)
		}

		resolvedPath, err := filepath.EvalSymlinks(configPath)
		if err != nil {
			return nil, fmt.Errorf("resolve nginx configuration path %q: %w", configPath, err)
		}
		resolvedRoot := findResolvedRoot(allowedRoots, resolvedPath)
		if resolvedRoot == "" {
			return nil, fmt.Errorf("validate resolved nginx configuration path %q: %w", configPath, ErrConfigPathOutsideAllowedRoots)
		}
		relativePath, err := filepath.Rel(resolvedRoot, resolvedPath)
		if err != nil {
			return nil, fmt.Errorf("resolve nginx configuration path under allowed root: %w", err)
		}

		root, err := os.OpenRoot(resolvedRoot)
		if err != nil {
			return nil, fmt.Errorf("open nginx configuration root: %w", err)
		}
		defer func() {
			if err := root.Close(); err != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("close nginx configuration root: %w", err))
			}
		}()

		file, err := root.Open(filepath.ToSlash(relativePath))
		if err != nil {
			return nil, fmt.Errorf("open nginx configuration file: %w", err)
		}
		defer func() {
			if err := file.Close(); err != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("close nginx configuration file: %w", err))
			}
		}()

		info, err := file.Stat()
		if err != nil {
			return nil, fmt.Errorf("inspect nginx configuration file: %w", err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("validate nginx configuration file: regular file required")
		}
		if info.Size() > effectiveConfigOutputLimit {
			return nil, fmt.Errorf("read nginx configuration file: %w", ErrOutputTooLarge)
		}

		contents, err = io.ReadAll(io.LimitReader(contextCheckedReader{ctx: ctx, reader: file}, effectiveConfigOutputLimit+1))
		if err != nil {
			return nil, fmt.Errorf("read nginx configuration file: %w", err)
		}
		if len(contents) > effectiveConfigOutputLimit {
			return nil, fmt.Errorf("read nginx configuration file: %w", ErrOutputTooLarge)
		}
		return contents, nil
	}
}

func isInsideAllowedRootAlias(allowedRoots []effectiveConfigRoot, path string) bool {
	for _, root := range allowedRoots {
		if isPathInsideRoot(root.declared, path) || isPathInsideRoot(root.resolved, path) {
			return true
		}
	}
	return false
}

func findResolvedRoot(allowedRoots []effectiveConfigRoot, path string) string {
	var best string
	for _, root := range allowedRoots {
		if !isPathInsideRoot(root.resolved, path) {
			continue
		}
		if len(root.resolved) > len(best) {
			best = root.resolved
		}
	}
	return best
}

func isPathInsideRoot(root string, path string) bool {
	relativePath, err := filepath.Rel(root, path)
	return err == nil && relativePath != "." && relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(filepath.Separator))
}

type contextCheckedReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextCheckedReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}
