/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"golang.org/x/sync/singleflight"
)

const (
	nginxExecutable  = "/usr/sbin/nginx"
	nginxConfigPath  = "/etc/nginx/nginx.conf"
	buildOutputLimit = 256 * 1024
)

type commandExecutor func(context.Context, commandSpec) (commandResult, error)

// Service performs the fixed read-only Nginx inspection operations.
type Service struct {
	executor             commandExecutor
	buildLock            chan struct{}
	cachedBuild          *BuildInfo
	effectiveConfigGroup singleflight.Group
}

// NewService creates the production fixed-command service.
func NewService() *Service {
	return newServiceWithExecutor(executeCommand)
}

func newServiceWithExecutor(executor commandExecutor) *Service {
	return &Service{executor: executor, buildLock: make(chan struct{}, 1)}
}

// BuildInfo executes and caches only a successfully parsed nginx -V result.
func (s *Service) BuildInfo(ctx context.Context) (BuildInfo, error) {
	select {
	case s.buildLock <- struct{}{}:
		defer func() { <-s.buildLock }()
	case <-ctx.Done():
		return BuildInfo{}, fmt.Errorf("wait for nginx build information: %w", ctx.Err())
	}
	if s.cachedBuild != nil {
		return cloneBuildInfo(*s.cachedBuild), nil
	}
	result, err := s.executor(ctx, commandSpec{
		executable: nginxExecutable, arguments: []string{"-V"}, timeout: buildInfoTimeout,
		maxOutputBytes: buildOutputLimit, allowedExitCodes: map[int]struct{}{0: {}},
	})
	if err != nil {
		return BuildInfo{}, fmt.Errorf("inspect nginx build information: %w", err)
	}
	combined := bytes.Join([][]byte{result.stdout, result.stderr}, []byte{'\n'})
	info, err := parseBuildInfo(combined)
	if err != nil {
		return BuildInfo{}, err
	}
	s.cachedBuild = &info
	return cloneBuildInfo(info), nil
}

const buildInfoTimeout = 3 * time.Second

func parseBuildInfo(output []byte) (BuildInfo, error) {
	var version string
	var rawArguments string
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSuffix(line, "\r")
		switch {
		case strings.HasPrefix(line, "nginx version: nginx/"):
			if version != "" {
				return BuildInfo{}, fmt.Errorf("parse nginx build information: duplicate version")
			}
			version = strings.TrimPrefix(line, "nginx version: nginx/")
		case strings.HasPrefix(line, "configure arguments:"):
			if rawArguments != "" {
				return BuildInfo{}, fmt.Errorf("parse nginx build information: duplicate configure arguments")
			}
			rawArguments = strings.TrimSpace(strings.TrimPrefix(line, "configure arguments:"))
		}
	}
	if version == "" || strings.IndexFunc(version, unicode.IsSpace) >= 0 {
		return BuildInfo{}, fmt.Errorf("parse nginx build information: valid version is required")
	}
	if rawArguments == "" {
		return BuildInfo{}, fmt.Errorf("parse nginx build information: configure arguments are required")
	}
	arguments, err := splitPOSIXArguments(rawArguments)
	if err != nil {
		return BuildInfo{}, fmt.Errorf("parse nginx configure arguments: %w", err)
	}
	info := BuildInfo{Version: version, ConfigureArguments: arguments}
	for _, argument := range arguments {
		if value, found := strings.CutPrefix(argument, "--pid-path="); found {
			info.PIDPath = value
		}
		if value, found := strings.CutPrefix(argument, "--sbin-path="); found {
			info.SbinPath = value
		}
	}
	if info.PIDPath == "" || info.SbinPath == "" {
		return BuildInfo{}, fmt.Errorf("parse nginx build information: pid and sbin paths are required")
	}
	return info, nil
}

func splitPOSIXArguments(input string) ([]string, error) {
	const (
		unquoted = iota
		singleQuoted
		doubleQuoted
	)
	state := unquoted
	escaped := false
	started := false
	var current strings.Builder
	arguments := make([]string, 0, 16)
	flush := func() {
		arguments = append(arguments, current.String())
		current.Reset()
		started = false
	}
	for _, character := range input {
		if escaped {
			if character == '\n' {
				escaped = false
				continue
			}
			if state == doubleQuoted {
				switch character {
				case '$', '`', '"', '\\':
				default:
					current.WriteRune('\\')
				}
			}
			current.WriteRune(character)
			escaped = false
			started = true
			continue
		}
		switch state {
		case unquoted:
			switch {
			case character == '\\':
				escaped = true
				started = true
			case character == '\'':
				state = singleQuoted
				started = true
			case character == '"':
				state = doubleQuoted
				started = true
			case unicode.IsSpace(character):
				if started {
					flush()
				}
			default:
				current.WriteRune(character)
				started = true
			}
		case singleQuoted:
			if character == '\'' {
				state = unquoted
			} else {
				current.WriteRune(character)
			}
		case doubleQuoted:
			switch character {
			case '"':
				state = unquoted
			case '\\':
				escaped = true
			default:
				current.WriteRune(character)
			}
		}
	}
	if escaped || state != unquoted {
		return nil, fmt.Errorf("unterminated quote or escape")
	}
	if started {
		flush()
	}
	if len(arguments) == 0 {
		return nil, fmt.Errorf("no arguments")
	}
	return arguments, nil
}

func cloneBuildInfo(info BuildInfo) BuildInfo {
	info.ConfigureArguments = slices.Clone(info.ConfigureArguments)
	return info
}
