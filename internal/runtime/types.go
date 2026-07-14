/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import (
	"errors"
	"time"
)

var (
	// ErrCommandTimeout indicates that a fixed Nginx inspection deadline elapsed.
	ErrCommandTimeout = errors.New("nginx command timed out")
	// ErrOutputTooLarge indicates that combined stdout and stderr exceeded its fixed limit.
	ErrOutputTooLarge = errors.New("nginx command output too large")
	// ErrConfigInvalid indicates that Nginx rejected the fixed entry configuration.
	ErrConfigInvalid = errors.New("nginx configuration invalid")
)

// BuildInfo is the parsed output of the fixed nginx -V operation.
type BuildInfo struct {
	Version            string
	ConfigureArguments []string
	PIDPath            string
	SbinPath           string
}

// ConfigOccurrence is one ordered marker and body emitted by nginx -T.
type ConfigOccurrence struct {
	ID        string
	LoadOrder int
	Path      string
	Content   string
}

// EffectiveConfig contains one response-scoped ordered nginx -T snapshot.
type EffectiveConfig struct {
	Occurrences []ConfigOccurrence
}

// StartupValidation is the bounded result of nginx -t.
type StartupValidation struct {
	Valid      bool
	CheckedAt  time.Time
	Diagnostic string
}
