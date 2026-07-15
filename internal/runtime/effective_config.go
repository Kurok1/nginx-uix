/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
)

const (
	effectiveConfigTimeout     = 10 * time.Second
	effectiveConfigOutputLimit = 16 * 1024 * 1024
	startupValidationTimeout   = 10 * time.Second
	startupDiagnosticLimit     = 256 * 1024
	configurationMarkerPrefix  = "# configuration file "
)

// EffectiveConfig returns one response-scoped snapshot from the fixed nginx -T command.
func (s *Service) EffectiveConfig(ctx context.Context) (EffectiveConfig, error) {
	if err := ctx.Err(); err != nil {
		return EffectiveConfig{}, fmt.Errorf("wait for effective nginx configuration: %w", err)
	}

	resultChannel := s.effectiveConfigGroup.DoChan("effective-config", s.inspectEffectiveConfig)

	select {
	case <-ctx.Done():
		return EffectiveConfig{}, fmt.Errorf("wait for effective nginx configuration: %w", ctx.Err())
	case result := <-resultChannel:
		if result.Err != nil {
			return EffectiveConfig{}, result.Err
		}
		configuration, ok := result.Val.(EffectiveConfig)
		if !ok {
			return EffectiveConfig{}, fmt.Errorf("inspect effective nginx configuration: unexpected shared result")
		}
		return cloneEffectiveConfig(configuration), nil
	}
}

func (s *Service) inspectEffectiveConfig() (any, error) {
	internalContext, cancel := context.WithTimeout(context.Background(), effectiveConfigTimeout)
	defer cancel()

	result, err := s.executor(internalContext, commandSpec{
		executable:       nginxExecutable,
		arguments:        []string{"-T", "-c", nginxConfigPath},
		timeout:          effectiveConfigTimeout,
		maxOutputBytes:   effectiveConfigOutputLimit,
		allowedExitCodes: map[int]struct{}{0: {}},
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return EffectiveConfig{}, fmt.Errorf("inspect effective nginx configuration: %w", ErrCommandTimeout)
		}
		var exitError *commandExitError
		if errors.As(err, &exitError) {
			return EffectiveConfig{}, fmt.Errorf("inspect effective nginx configuration: %w: %w", ErrConfigInvalid, err)
		}
		return EffectiveConfig{}, fmt.Errorf("inspect effective nginx configuration: %w", err)
	}

	configuration, err := parseEffectiveConfig(internalContext, result.stdout, s.readConfigFile)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled),
			errors.Is(err, context.DeadlineExceeded),
			errors.Is(err, ErrOutputTooLarge):
			return EffectiveConfig{}, err
		case errors.Is(err, ErrConfigPathOutsideAllowedRoots):
			return rawEffectiveConfig(result.stdout, EffectiveConfigWarningPathOutsideAllowedRoots), nil
		default:
			return rawEffectiveConfig(result.stdout, EffectiveConfigWarningStructureUnverified), nil
		}
	}
	return configuration, nil
}

// ValidateStartup runs the fixed bounded nginx -t command.
func (s *Service) ValidateStartup(ctx context.Context) (StartupValidation, error) {
	result, err := s.executor(ctx, commandSpec{
		executable:       nginxExecutable,
		arguments:        []string{"-t", "-c", nginxConfigPath},
		timeout:          startupValidationTimeout,
		maxOutputBytes:   startupDiagnosticLimit,
		allowedExitCodes: map[int]struct{}{0: {}},
	})
	if err != nil {
		var exitError *commandExitError
		if errors.As(err, &exitError) {
			return StartupValidation{
				Valid:      false,
				CheckedAt:  time.Now().UTC(),
				Diagnostic: sanitizeDiagnostic(result.stderr),
			}, fmt.Errorf("validate startup nginx configuration: %w: %w", ErrConfigInvalid, err)
		}
		return StartupValidation{}, fmt.Errorf("validate startup nginx configuration: %w", err)
	}

	return StartupValidation{
		Valid:      true,
		CheckedAt:  time.Now().UTC(),
		Diagnostic: sanitizeDiagnostic(result.stderr),
	}, nil
}

func parseEffectiveConfig(
	ctx context.Context,
	output []byte,
	readConfigFile configFileReader,
) (EffectiveConfig, error) {
	if readConfigFile == nil {
		return EffectiveConfig{}, fmt.Errorf("parse effective nginx configuration: file reader is required")
	}
	normalized := bytes.ReplaceAll(output, []byte("\r\n"), []byte("\n"))
	configuration := EffectiveConfig{
		DisplayMode: EffectiveConfigDisplayModeStructured,
		Occurrences: make([]ConfigOccurrence, 0, 8),
		Warnings:    make([]EffectiveConfigWarning, 0),
	}
	markerStart, err := findEntryConfigurationMarker(normalized)
	if err != nil {
		return EffectiveConfig{}, err
	}

	contentsByPath := make(map[string][]byte)
	for {
		if err := ctx.Err(); err != nil {
			return EffectiveConfig{}, fmt.Errorf("parse effective nginx configuration: %w", err)
		}
		markerPath, bodyStart, err := parseConfigurationMarker(normalized, markerStart)
		if err != nil {
			return EffectiveConfig{}, err
		}

		contents, found := contentsByPath[markerPath]
		if !found {
			contents, err = readConfigFile(ctx, markerPath)
			if err != nil {
				return EffectiveConfig{}, fmt.Errorf(
					"parse effective nginx configuration: read occurrence %q: %w",
					markerPath,
					err,
				)
			}
			contents = bytes.ReplaceAll(contents, []byte("\r\n"), []byte("\n"))
			contentsByPath[markerPath] = contents
		}

		if len(contents) > len(normalized)-bodyStart {
			return EffectiveConfig{}, fmt.Errorf("parse effective nginx configuration: occurrence %q is truncated", markerPath)
		}
		bodyEnd := bodyStart + len(contents)
		if !bytes.Equal(normalized[bodyStart:bodyEnd], contents) {
			return EffectiveConfig{}, fmt.Errorf("parse effective nginx configuration: occurrence %q differs from its file", markerPath)
		}
		if bodyEnd >= len(normalized) || normalized[bodyEnd] != '\n' {
			return EffectiveConfig{}, fmt.Errorf("parse effective nginx configuration: occurrence %q has no dump separator", markerPath)
		}

		loadOrder := len(configuration.Occurrences) + 1
		configuration.Occurrences = append(configuration.Occurrences, ConfigOccurrence{
			ID:        fmt.Sprintf("occurrence-%06d", loadOrder),
			LoadOrder: loadOrder,
			Path:      markerPath,
			Content:   string(contents),
		})

		markerStart = bodyEnd + 1
		if markerStart == len(normalized) {
			return configuration, nil
		}
		if !bytes.HasPrefix(normalized[markerStart:], []byte(configurationMarkerPrefix)) {
			return EffectiveConfig{}, fmt.Errorf("parse effective nginx configuration: unexpected data after occurrence %q", markerPath)
		}
	}
}

func findEntryConfigurationMarker(output []byte) (int, error) {
	want := configurationMarkerPrefix + nginxConfigPath + ":"
	for lineStart := 0; lineStart < len(output); {
		lineEnd := bytes.IndexByte(output[lineStart:], '\n')
		nextLine := len(output)
		if lineEnd >= 0 {
			lineEnd += lineStart
			nextLine = lineEnd + 1
		} else {
			lineEnd = len(output)
		}
		line := string(output[lineStart:lineEnd])
		if strings.HasPrefix(line, configurationMarkerPrefix) {
			if line != want {
				return 0, fmt.Errorf("parse effective nginx configuration: fixed entry marker is required first")
			}
			return lineStart, nil
		}
		lineStart = nextLine
	}
	return 0, fmt.Errorf("parse effective nginx configuration: fixed entry marker is required")
}

func parseConfigurationMarker(output []byte, markerStart int) (string, int, error) {
	if markerStart < 0 || markerStart >= len(output) {
		return "", 0, fmt.Errorf("parse effective nginx configuration: marker position is invalid")
	}
	lineEnd := bytes.IndexByte(output[markerStart:], '\n')
	if lineEnd < 0 {
		return "", 0, fmt.Errorf("parse effective nginx configuration: marker has no line ending")
	}
	lineEnd += markerStart
	line := string(output[markerStart:lineEnd])
	if !strings.HasPrefix(line, configurationMarkerPrefix) || !strings.HasSuffix(line, ":") {
		return "", 0, fmt.Errorf("parse effective nginx configuration: malformed marker")
	}
	markerPath := strings.TrimSuffix(strings.TrimPrefix(line, configurationMarkerPrefix), ":")
	if markerPath == "" || strings.IndexFunc(markerPath, unicode.IsControl) >= 0 {
		return "", 0, fmt.Errorf("parse effective nginx configuration: malformed marker path")
	}
	if !filepath.IsAbs(markerPath) {
		return "", 0, fmt.Errorf("parse effective nginx configuration: marker path must be absolute")
	}
	return filepath.Clean(markerPath), lineEnd + 1, nil
}

func cloneEffectiveConfig(configuration EffectiveConfig) EffectiveConfig {
	configuration.Occurrences = slices.Clone(configuration.Occurrences)
	configuration.Warnings = slices.Clone(configuration.Warnings)
	return configuration
}

func rawEffectiveConfig(output []byte, warning EffectiveConfigWarning) EffectiveConfig {
	return EffectiveConfig{
		DisplayMode: EffectiveConfigDisplayModeRaw,
		Occurrences: make([]ConfigOccurrence, 0),
		RawContent:  string(output),
		Warnings:    []EffectiveConfigWarning{warning},
	}
}
