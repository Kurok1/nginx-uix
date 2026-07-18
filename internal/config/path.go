/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"strings"
	"unicode/utf8"
)

const portableNameMax = 255

// ParseRelativePath validates a slash-separated path beneath a scoped root.
func ParseRelativePath(raw string, limits Limits) (RelativePath, error) {
	return parseRelativePath(raw, limits, limits.MaxPathComponentBytes)
}

func parseRelativePath(raw string, limits Limits, componentLimit int) (RelativePath, error) {
	if !utf8.ValidString(raw) {
		return "", invalidPath("path is not valid utf-8")
	}
	if raw == "" {
		return "", invalidPath("path is empty")
	}
	if limits.MaxPathBytes <= 0 || len(raw) > limits.MaxPathBytes {
		return "", invalidPath("path exceeds byte limit")
	}
	if raw[0] == '/' {
		return "", invalidPath("path is absolute")
	}
	if strings.ContainsRune(raw, '\\') {
		return "", invalidPath("path contains backslash")
	}
	if strings.ContainsRune(raw, '\x00') {
		return "", invalidPath("path contains nul")
	}

	components := strings.Split(raw, "/")
	if limits.MaxPathDepth <= 0 || len(components) > limits.MaxPathDepth {
		return "", invalidPath("path exceeds depth limit")
	}
	maxComponentBytes := min(portableNameMax, limits.MaxPathComponentBytes, componentLimit)
	if maxComponentBytes <= 0 {
		return "", invalidPath("component limit unavailable")
	}
	for _, component := range components {
		switch {
		case component == "":
			return "", invalidPath("path contains empty component")
		case component == "." || component == "..":
			return "", invalidPath("path contains traversal component")
		case len(component) > maxComponentBytes:
			return "", invalidPath("component exceeds byte limit")
		}
	}

	return RelativePath(raw), nil
}

func invalidPath(reason string) error {
	return &PathError{Reason: reason}
}
