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

	configuration, err := parseEffectiveConfig(result.stdout)
	if err != nil {
		return EffectiveConfig{}, err
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

func parseEffectiveConfig(output []byte) (EffectiveConfig, error) {
	normalized := bytes.ReplaceAll(output, []byte("\r\n"), []byte("\n"))
	configuration := EffectiveConfig{Occurrences: make([]ConfigOccurrence, 0, 8)}
	bodyStart := -1

	for lineStart := 0; lineStart < len(normalized); {
		lineEnd := bytes.IndexByte(normalized[lineStart:], '\n')
		nextLine := len(normalized)
		if lineEnd >= 0 {
			lineEnd += lineStart
			nextLine = lineEnd + 1
		} else {
			lineEnd = len(normalized)
		}
		line := string(normalized[lineStart:lineEnd])

		if strings.HasPrefix(line, configurationMarkerPrefix) {
			pathWithColon := strings.TrimPrefix(line, configurationMarkerPrefix)
			if !strings.HasSuffix(pathWithColon, ":") {
				return EffectiveConfig{}, fmt.Errorf("parse effective nginx configuration: malformed marker")
			}
			path := strings.TrimSuffix(pathWithColon, ":")
			if path == "" || strings.IndexFunc(path, unicode.IsControl) >= 0 {
				return EffectiveConfig{}, fmt.Errorf("parse effective nginx configuration: malformed marker path")
			}

			if bodyStart >= 0 {
				last := len(configuration.Occurrences) - 1
				configuration.Occurrences[last].Content = string(normalized[bodyStart:lineStart])
			}
			loadOrder := len(configuration.Occurrences) + 1
			configuration.Occurrences = append(configuration.Occurrences, ConfigOccurrence{
				ID:        fmt.Sprintf("occurrence-%06d", loadOrder),
				LoadOrder: loadOrder,
				Path:      path,
			})
			bodyStart = nextLine
		}
		lineStart = nextLine
	}

	if bodyStart < 0 {
		return EffectiveConfig{}, fmt.Errorf("parse effective nginx configuration: marker is required")
	}
	last := len(configuration.Occurrences) - 1
	configuration.Occurrences[last].Content = string(normalized[bodyStart:])
	return configuration, nil
}

func cloneEffectiveConfig(configuration EffectiveConfig) EffectiveConfig {
	configuration.Occurrences = slices.Clone(configuration.Occurrences)
	return configuration
}
