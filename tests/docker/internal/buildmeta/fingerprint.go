/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */
package buildmeta

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	SourceSchema   = "nginx-uix-source-v1"
	IdentitySchema = "nginx-uix-build-v1"
)

type Digest [sha256.Size]byte

func (digest Digest) String() string {
	return hex.EncodeToString(digest[:])
}

func ParseDigest(value string) (Digest, error) {
	var digest Digest
	if len(value) != hex.EncodedLen(len(digest)) || value != strings.ToLower(value) {
		return digest, errors.New("digest must be 64 lowercase hexadecimal characters")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return digest, errors.New("digest must be 64 lowercase hexadecimal characters")
	}
	copy(digest[:], decoded)
	return digest, nil
}

type SourceOptions struct {
	Root       string
	IgnoreFile string
}

type IdentityInput struct {
	SourceFingerprint Digest
	Platform          string
	BaseDigests       map[string]string
	BuildArgs         map[string]string
}

type sourceEntry struct {
	path       string
	kind       byte
	executable bool
	payload    []byte
}

func SourceFingerprint(ctx context.Context, options SourceOptions) (Digest, error) {
	entries, _, err := collectSourceEntries(ctx, options)
	if err != nil {
		return Digest{}, err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(SourceSchema))
	for _, entry := range entries {
		writeLengthPrefixed(hash, []byte(entry.path))
		_, _ = hash.Write([]byte{entry.kind})
		if entry.kind == 'f' {
			if entry.executable {
				_, _ = hash.Write([]byte{1})
			} else {
				_, _ = hash.Write([]byte{0})
			}
		}
		writeLengthPrefixed(hash, entry.payload)
	}
	var digest Digest
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

func BuildIdentity(input IdentityInput) (Digest, error) {
	if input.SourceFingerprint == (Digest{}) {
		return Digest{}, errors.New("source fingerprint is required")
	}
	if input.Platform != "linux/amd64" && input.Platform != "linux/arm64" {
		return Digest{}, errors.New("platform must be linux/amd64 or linux/arm64")
	}

	requiredBases := map[string]bool{"go": false, "node": false, "nginx": false}
	for name, value := range input.BaseDigests {
		if _, required := requiredBases[name]; required {
			requiredBases[name] = true
		} else if name != "playwright" {
			return Digest{}, fmt.Errorf("unsupported base digest %q", name)
		}
		if _, err := ParseDigest(value); err != nil {
			return Digest{}, fmt.Errorf("base digest %q: %w", name, err)
		}
	}
	for name, present := range requiredBases {
		if !present {
			return Digest{}, fmt.Errorf("base digest %q is required", name)
		}
	}

	for _, name := range []string{"VERSION", "COMMIT", "BUILD_TIME", "SOURCE_DATE_EPOCH"} {
		if input.BuildArgs[name] == "" {
			return Digest{}, fmt.Errorf("build argument %q is required", name)
		}
	}

	hash := sha256.New()
	_, _ = hash.Write([]byte(IdentitySchema))
	writeLengthPrefixed(hash, input.SourceFingerprint[:])
	writeLengthPrefixed(hash, []byte(input.Platform))
	writeCanonicalMap(hash, input.BaseDigests, nil)
	writeCanonicalMap(hash, input.BuildArgs, excludedBuildArgument)
	var digest Digest
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

func VerifyDockerfileCoverage(ctx context.Context, options SourceOptions, dockerfile string) error {
	_, included, err := collectSourceEntries(ctx, options)
	if err != nil {
		return err
	}
	cleanDockerfile, err := cleanRelativePath(dockerfile)
	if err != nil {
		return fmt.Errorf("dockerfile path: %w", err)
	}
	if _, ok := included[cleanDockerfile]; !ok {
		return fmt.Errorf("dockerfile %q is absent from the source fingerprint", cleanDockerfile)
	}
	content, err := os.ReadFile(filepath.Join(options.Root, filepath.FromSlash(cleanDockerfile)))
	if err != nil {
		return fmt.Errorf("read dockerfile %q: operation failed", cleanDockerfile)
	}
	instructions, err := dockerfileInstructions(content)
	if err != nil {
		return fmt.Errorf("parse dockerfile %q: %w", cleanDockerfile, err)
	}
	paths := make([]string, 0, len(included))
	for name := range included {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	for _, instruction := range instructions {
		if err := verifyCopyInstruction(instruction, included, paths); err != nil {
			return fmt.Errorf("dockerfile %q: %w", cleanDockerfile, err)
		}
	}
	return nil
}

func collectSourceEntries(ctx context.Context, options SourceOptions) ([]sourceEntry, map[string]struct{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("fingerprint source: %w", err)
	}
	if options.Root == "" {
		return nil, nil, errors.New("source root is required")
	}
	ignoreFile, err := cleanRelativePath(options.IgnoreFile)
	if err != nil {
		return nil, nil, fmt.Errorf("ignore file path: %w", err)
	}
	ignoreBytes, err := os.ReadFile(filepath.Join(options.Root, filepath.FromSlash(ignoreFile)))
	if err != nil {
		return nil, nil, fmt.Errorf("read ignore file %q: operation failed", ignoreFile)
	}
	ignoreRules, err := parseIgnoreRules(ignoreBytes)
	if err != nil {
		return nil, nil, err
	}

	rootInfo, err := os.Lstat(options.Root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, nil, errors.New("source root is not a regular directory")
	}
	entries := make([]sourceEntry, 0, 256)
	included := make(map[string]struct{}, 256)
	var walk func(string) error
	walk = func(relativeDir string) error {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("fingerprint source: %w", err)
		}
		absoluteDir := options.Root
		if relativeDir != "" {
			absoluteDir = filepath.Join(options.Root, filepath.FromSlash(relativeDir))
		}
		dirEntries, err := os.ReadDir(absoluteDir)
		if err != nil {
			return fmt.Errorf("read source directory %q: operation failed", relativeDir)
		}
		for _, dirEntry := range dirEntries {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("fingerprint source: %w", err)
			}
			relative := dirEntry.Name()
			if relativeDir != "" {
				relative = relativeDir + "/" + relative
			}
			if err := validateSourceRelativePath(relative); err != nil {
				return err
			}
			if relative != ignoreFile && ignoredLiteral(relative, ignoreRules) {
				continue
			}
			info, err := os.Lstat(filepath.Join(options.Root, filepath.FromSlash(relative)))
			if err != nil {
				return fmt.Errorf("inspect source entry %q: operation failed", relative)
			}
			entry := sourceEntry{path: relative}
			switch {
			case info.Mode().IsRegular():
				entry.kind = 'f'
				entry.executable = info.Mode().Perm()&0o111 != 0
				entry.payload, err = os.ReadFile(filepath.Join(options.Root, filepath.FromSlash(relative)))
				if err != nil {
					return fmt.Errorf("read source entry %q: operation failed", relative)
				}
			case info.IsDir():
				entry.kind = 'd'
			case info.Mode()&os.ModeSymlink != 0:
				entry.kind = 'l'
				target, readErr := os.Readlink(filepath.Join(options.Root, filepath.FromSlash(relative)))
				if readErr != nil {
					return fmt.Errorf("read source link %q: operation failed", relative)
				}
				entry.payload = []byte(target)
			default:
				return fmt.Errorf("unsupported source entry type at %q", relative)
			}
			entries = append(entries, entry)
			included[relative] = struct{}{}
			if entry.kind == 'd' {
				if err := walk(relative); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(""); err != nil {
		return nil, nil, err
	}
	sort.Slice(entries, func(left, right int) bool {
		return bytes.Compare([]byte(entries[left].path), []byte(entries[right].path)) < 0
	})
	return entries, included, nil
}

func validateSourceRelativePath(relative string) error {
	if !utf8.ValidString(relative) {
		return errors.New("invalid UTF-8 path in source context")
	}
	return nil
}

func parseIgnoreRules(content []byte) ([]string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	rules := make([]string, 0, 16)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		rule := strings.TrimSpace(scanner.Text())
		if rule == "" || strings.HasPrefix(rule, "#") {
			continue
		}
		if strings.HasPrefix(rule, "!") || strings.HasPrefix(rule, "/") ||
			strings.ContainsAny(rule, "*?[\\") {
			return nil, fmt.Errorf("unsupported ignore rule on line %d", lineNumber)
		}
		rule = strings.TrimSuffix(rule, "/")
		clean, err := cleanRelativePath(rule)
		if err != nil || clean != rule {
			return nil, fmt.Errorf("unsupported ignore rule on line %d", lineNumber)
		}
		rules = append(rules, clean)
	}
	if err := scanner.Err(); err != nil {
		return nil, errors.New("read ignore rules: operation failed")
	}
	return rules, nil
}

func cleanRelativePath(value string) (string, error) {
	if value == "" || !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return "", errors.New("path must be non-empty UTF-8")
	}
	if filepath.IsAbs(value) || strings.Contains(value, "\\") {
		return "", errors.New("path must be a slash-normalized relative path")
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("path escapes the source root")
	}
	return clean, nil
}

func ignoredLiteral(relative string, rules []string) bool {
	for _, rule := range rules {
		if relative == rule || strings.HasPrefix(relative, rule+"/") {
			return true
		}
	}
	return false
}

func writeLengthPrefixed(hash interface{ Write([]byte) (int, error) }, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = hash.Write(length[:])
	_, _ = hash.Write(value)
}

func writeCanonicalMap(hash interface{ Write([]byte) (int, error) }, values map[string]string, excluded func(string) bool) {
	keys := make([]string, 0, len(values))
	for key := range values {
		if excluded == nil || !excluded(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		writeLengthPrefixed(hash, []byte(key))
		writeLengthPrefixed(hash, []byte(values[key]))
	}
}

func excludedBuildArgument(name string) bool {
	upper := strings.ToUpper(name)
	if strings.HasSuffix(upper, "_PROXY") {
		return true
	}
	switch upper {
	case "BUILDKIT_PROGRESS", "BUILDX_BUILDER", "DOCKER_CONFIG":
		return true
	default:
		return false
	}
}

func dockerfileInstructions(content []byte) ([]string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	instructions := make([]string, 0, 32)
	current := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if current == "" && (line == "" || strings.HasPrefix(line, "#")) {
			continue
		}
		continued := strings.HasSuffix(line, "\\")
		if continued {
			line = strings.TrimSpace(strings.TrimSuffix(line, "\\"))
		}
		if current != "" {
			current += " "
		}
		current += line
		if !continued {
			instructions = append(instructions, current)
			current = ""
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, errors.New("read instructions: operation failed")
	}
	if current != "" {
		return nil, errors.New("unterminated line continuation")
	}
	return instructions, nil
}

func verifyCopyInstruction(instruction string, included map[string]struct{}, includedPaths []string) error {
	fields := strings.Fields(instruction)
	if len(fields) == 0 {
		return nil
	}
	verb := strings.ToUpper(fields[0])
	if verb != "COPY" && verb != "ADD" {
		return nil
	}
	if strings.Contains(instruction, "[") || len(fields) < 3 {
		return fmt.Errorf("unrecognized local %s form", verb)
	}
	arguments := fields[1:]
	fromStage := false
	for len(arguments) > 0 && strings.HasPrefix(arguments[0], "--") {
		option := arguments[0]
		arguments = arguments[1:]
		switch {
		case strings.HasPrefix(option, "--from="):
			fromStage = true
		case strings.HasPrefix(option, "--chown="), strings.HasPrefix(option, "--chmod="),
			strings.HasPrefix(option, "--link="), strings.HasPrefix(option, "--checksum="):
		default:
			return fmt.Errorf("unrecognized local %s option", verb)
		}
	}
	if fromStage {
		return nil
	}
	if len(arguments) < 2 {
		return fmt.Errorf("unrecognized local %s form", verb)
	}
	sources := arguments[:len(arguments)-1]
	if verb == "ADD" {
		remote := true
		for _, source := range sources {
			if !strings.HasPrefix(source, "https://") && !strings.HasPrefix(source, "http://") {
				remote = false
			}
		}
		if remote {
			return nil
		}
	}
	for _, source := range sources {
		if strings.ContainsAny(source, "$\"'") {
			return fmt.Errorf("unrecognized local %s source %q", verb, source)
		}
		source = strings.TrimSuffix(source, "/")
		if strings.ContainsAny(source, "*?[") {
			matched := false
			for _, candidate := range includedPaths {
				match, err := path.Match(source, candidate)
				if err != nil {
					return fmt.Errorf("invalid local %s pattern %q", verb, source)
				}
				if match {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("local %s source %q is absent from the source fingerprint", verb, source)
			}
			continue
		}
		clean, err := cleanRelativePath(source)
		if err != nil {
			return fmt.Errorf("unrecognized local %s source %q", verb, source)
		}
		if _, ok := included[clean]; !ok {
			return fmt.Errorf("local %s source %q is absent from the source fingerprint", verb, clean)
		}
	}
	return nil
}
