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
	// ErrConfigPathOutsideAllowedRoots indicates that an effective configuration file is outside the read-only allowlist.
	ErrConfigPathOutsideAllowedRoots = errors.New("nginx configuration path outside allowed roots")
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

// EffectiveConfigDisplayMode describes whether the snapshot has verified file boundaries.
type EffectiveConfigDisplayMode string

const (
	// EffectiveConfigDisplayModeStructured exposes only byte-verified file occurrences.
	EffectiveConfigDisplayModeStructured EffectiveConfigDisplayMode = "structured"
	// EffectiveConfigDisplayModeRaw exposes the bounded nginx -T stdout without inferred boundaries.
	EffectiveConfigDisplayModeRaw EffectiveConfigDisplayMode = "raw"
)

// EffectiveConfigWarning is a stable reason why a snapshot cannot be displayed structurally.
type EffectiveConfigWarning string

const (
	// EffectiveConfigWarningPathOutsideAllowedRoots indicates that nginx loaded a file outside the read-only allowlist.
	EffectiveConfigWarningPathOutsideAllowedRoots EffectiveConfigWarning = "NGINX_CONFIG_PATH_OUTSIDE_ALLOWED_ROOTS"
	// EffectiveConfigWarningStructureUnverified indicates that file boundaries could not be byte-verified.
	EffectiveConfigWarningStructureUnverified EffectiveConfigWarning = "NGINX_CONFIG_STRUCTURE_UNVERIFIED"
)

// EffectiveConfig contains one response-scoped ordered nginx -T snapshot.
type EffectiveConfig struct {
	DisplayMode EffectiveConfigDisplayMode
	Occurrences []ConfigOccurrence
	RawContent  string
	Warnings    []EffectiveConfigWarning
}

// StartupValidation is the bounded result of nginx -t.
type StartupValidation struct {
	Valid      bool
	CheckedAt  time.Time
	ExitCode   *int
	Diagnostic string
}

// State is the verified Nginx runtime state.
type State string

const (
	StateRunning  State = "running"
	StateDegraded State = "degraded"
	StateStopped  State = "stopped"
	StateUnknown  State = "unknown"
)

// ProcessRole is a verified Nginx process role.
type ProcessRole string

const (
	ProcessRoleMaster ProcessRole = "master"
	ProcessRoleWorker ProcessRole = "worker"
)

// NginxProcess is the public, bounded subset of one verified process.
type NginxProcess struct {
	PID       int         `json:"pid"`
	Role      ProcessRole `json:"role"`
	StartedAt time.Time   `json:"started_at"`
}

// Status is one response-scoped Nginx process snapshot.
type Status struct {
	SampledAt         time.Time          `json:"sampled_at"`
	State             State              `json:"state"`
	Master            *NginxProcess      `json:"master,omitempty"`
	Workers           []NginxProcess     `json:"workers"`
	Build             *BuildInfo         `json:"build,omitempty"`
	StartupValidation *StartupValidation `json:"startup_validation,omitempty"`
	Recovery          *RecoveryState     `json:"recovery,omitempty"`
	Issues            []string           `json:"issues"`
}

// StartupState is the complete bounded state written by the startup supervisor.
type StartupState struct {
	Validation *StartupValidation `json:"validation,omitempty"`
	Recovery   *RecoveryState     `json:"recovery,omitempty"`
}

// RecoveryResult describes the most recent bounded automatic recovery action.
type RecoveryResult string

const (
	RecoveryResultRestarting    RecoveryResult = "restarting"
	RecoveryResultInvalidConfig RecoveryResult = "invalid_config"
	RecoveryResultPermanent     RecoveryResult = "permanent_failure"
)

// RecoveryState records only the current five-minute Nginx death window.
type RecoveryState struct {
	Count      int            `json:"count"`
	LastResult RecoveryResult `json:"last_result,omitempty"`
	Permanent  bool           `json:"permanent"`
	Events     []ExitEvent    `json:"events,omitempty"`
}

// ExitEvent is one bounded Nginx exit observation.
type ExitEvent struct {
	OccurredAt time.Time `json:"occurred_at"`
	ExitCode   int       `json:"exit_code"`
	Signal     int       `json:"signal"`
}
