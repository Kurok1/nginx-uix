/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

package routelab

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

var (
	// ErrBusy indicates that the bounded active-run limit was reached.
	ErrBusy = errors.New("route lab busy")
	// ErrAlreadyTerminal indicates that a terminal route run cannot be cancelled or transitioned.
	ErrAlreadyTerminal = errors.New("route run already terminal")
)

// RunID is one opaque lowercase hexadecimal route-test identity.
type RunID string

// RunState is the durable route-test lifecycle state.
type RunState string

const (
	RunStateQueued    RunState = "queued"
	RunStateRunning   RunState = "running"
	RunStateSucceeded RunState = "succeeded"
	RunStateFailed    RunState = "failed"
	RunStateCancelled RunState = "cancelled"
	RunStateTimedOut  RunState = "timed_out"
)

// RunStageName is one immutable progress stage.
type RunStageName string

const (
	RunStageQueued     RunStageName = "queued"
	RunStagePreparing  RunStageName = "preparing"
	RunStageValidating RunStageName = "validating"
	RunStageStarting   RunStageName = "starting"
	RunStageRequesting RunStageName = "requesting"
	RunStageCollecting RunStageName = "collecting"
	RunStageCompleted  RunStageName = "completed"
	RunStageFailed     RunStageName = "failed"
	RunStageCancelled  RunStageName = "cancelled"
	RunStageTimedOut   RunStageName = "timed_out"
)

// StageResult classifies one immutable stage event.
type StageResult string

const (
	StageResultPending StageResult = "pending"
	StageResultRunning StageResult = "running"
	StageResultSuccess StageResult = "success"
	StageResultFailed  StageResult = "failed"
	StageResultWarning StageResult = "warning"
)

// Run is one durable Route Lab execution record. JSON fields are already bounded public projections.
type Run struct {
	ID                       RunID
	WorkspaceID              config.WorkspaceID
	WorkspaceRevision        uint64
	WorkspaceETag            string
	ProductionDigest         config.Digest
	DraftDigest              config.Digest
	CandidateDigest          config.Digest
	State                    RunState
	Stage                    RunStageName
	SafeRequestJSON          string
	StaticAnalysisJSON       string
	TerminalResultJSON       string
	Replayable               bool
	SideEffecting            bool
	BodyBytes                int64
	BodyDigest               config.Digest
	SensitiveHeaderNamesJSON string
	LastErrorCode            string
	CreatedBy                int64
	RequestID                string
	CancelRequestedAt        time.Time
	CreatedAt                time.Time
	UpdatedAt                time.Time
	StartedAt                time.Time
	FinishedAt               time.Time
}

// RunStage is one monotonic immutable lifecycle event.
type RunStage struct {
	RunID             RunID
	Sequence          uint64
	Stage             RunStageName
	Result            StageResult
	Code              string
	PublicDetailsJSON string
	OccurredAt        time.Time
}

// HistoryQuery is one bounded stable newest-first keyset query.
type HistoryQuery struct {
	WorkspaceID     config.WorkspaceID
	State           RunState
	BeforeCreatedAt time.Time
	BeforeID        RunID
	Limit           int
}

// SafeHeader is one replayable non-sensitive request header.
type SafeHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SafeRequest is the only request projection that may be persisted.
type SafeRequest struct {
	Scheme               Scheme       `json:"scheme"`
	Host                 string       `json:"host"`
	Port                 int          `json:"port"`
	SNI                  string       `json:"sni"`
	Method               string       `json:"method"`
	URI                  string       `json:"uri"`
	Query                string       `json:"query"`
	Headers              []SafeHeader `json:"headers"`
	SensitiveHeaderNames []string     `json:"sensitive_header_names"`
	BodyBytes            int64        `json:"body_bytes"`
	BodyDigest           string       `json:"body_digest"`
	TimeoutMS            int64        `json:"timeout_ms"`
	Assertions           Assertions   `json:"assertions"`
	SideEffecting        bool         `json:"side_effecting"`
	Replayable           bool         `json:"replayable"`
}

// AssertionKind identifies one persisted assertion result without retaining assertion text.
type AssertionKind string

const (
	AssertionStatusCode    AssertionKind = "status_code"
	AssertionContainsText  AssertionKind = "contains_text"
	AssertionForbiddenText AssertionKind = "forbidden_text"
)

// AssertionResult is one safe assertion outcome.
type AssertionResult struct {
	Kind     AssertionKind `json:"kind"`
	Passed   bool          `json:"passed"`
	Complete bool          `json:"complete"`
}

// AssertionOutcome separates a failed assertion from an indeterminate truncated-body check.
type AssertionOutcome struct {
	Passed   bool              `json:"passed"`
	Complete bool              `json:"complete"`
	Results  []AssertionResult `json:"results"`
}

// TerminalResult is the bounded persisted successful execution evidence.
type TerminalResult struct {
	AgentResult AgentResult `json:"agent_result"`
}

// ParseRunID validates one opaque lowercase hexadecimal run ID.
func ParseRunID(raw string) (RunID, error) {
	if len(raw) != 32 {
		return "", ErrInvalidRequest
	}
	for _, character := range raw {
		if character >= '0' && character <= '9' || character >= 'a' && character <= 'f' {
			continue
		}
		return "", ErrInvalidRequest
	}
	return RunID(raw), nil
}

// Terminal reports whether a run state cannot transition again.
func (state RunState) Terminal() bool {
	switch state {
	case RunStateSucceeded, RunStateFailed, RunStateCancelled, RunStateTimedOut:
		return true
	case RunStateQueued, RunStateRunning:
		return false
	default:
		return false
	}
}

// ValidRunState reports whether a value is part of the durable schema.
func ValidRunState(state RunState) bool {
	switch state {
	case RunStateQueued, RunStateRunning, RunStateSucceeded, RunStateFailed, RunStateCancelled, RunStateTimedOut:
		return true
	default:
		return false
	}
}

// ValidRunStage reports whether a value is part of the durable schema.
func ValidRunStage(stage RunStageName) bool {
	switch stage {
	case RunStageQueued, RunStagePreparing, RunStageValidating, RunStageStarting, RunStageRequesting,
		RunStageCollecting, RunStageCompleted, RunStageFailed, RunStageCancelled, RunStageTimedOut:
		return true
	default:
		return false
	}
}

// ValidStageResult reports whether a value is part of the durable schema.
func ValidStageResult(result StageResult) bool {
	switch result {
	case StageResultPending, StageResultRunning, StageResultSuccess, StageResultFailed, StageResultWarning:
		return true
	default:
		return false
	}
}

// NewSafeRequest removes request secrets while retaining replay metadata and body identity.
func NewSafeRequest(request ValidatedRequest) SafeRequest {
	sensitive := make(map[string]struct{}, len(request.SensitiveHeaderNames))
	for _, name := range request.SensitiveHeaderNames {
		sensitive[strings.ToLower(name)] = struct{}{}
	}
	headers := make([]SafeHeader, 0, len(request.Headers))
	for _, header := range request.Headers {
		if _, secret := sensitive[strings.ToLower(header.Name)]; secret {
			continue
		}
		headers = append(headers, SafeHeader(header))
	}
	digest := sha256.Sum256(request.Body)
	return SafeRequest{
		Scheme: request.Scheme, Host: request.Host, Port: request.Port, SNI: request.SNI,
		Method: request.Method, URI: request.URI, Query: request.Query, Headers: headers,
		SensitiveHeaderNames: slices.Clone(request.SensitiveHeaderNames),
		BodyBytes:            int64(len(request.Body)), BodyDigest: fmt.Sprintf("%x", digest[:]),
		TimeoutMS: request.Timeout.Milliseconds(), Assertions: request.Assertions,
		SideEffecting: request.SideEffecting, Replayable: request.Replayable,
	}
}

// EvaluateAssertions evaluates only captured response bytes and marks truncation-dependent results incomplete.
func EvaluateAssertions(assertions Assertions, status int, body []byte, truncated bool) AssertionOutcome {
	result := AssertionOutcome{Passed: true, Complete: true, Results: make([]AssertionResult, 0, 3)}
	appendResult := func(item AssertionResult) {
		result.Results = append(result.Results, item)
		result.Passed = result.Passed && item.Passed
		result.Complete = result.Complete && item.Complete
	}
	if assertions.StatusCode != 0 {
		appendResult(AssertionResult{
			Kind: AssertionStatusCode, Passed: status == assertions.StatusCode, Complete: true,
		})
	}
	if assertions.ContainsText != "" {
		found := bytes.Contains(body, []byte(assertions.ContainsText))
		appendResult(AssertionResult{
			Kind: AssertionContainsText, Passed: found, Complete: found || !truncated,
		})
	}
	if assertions.ForbiddenText != "" {
		found := bytes.Contains(body, []byte(assertions.ForbiddenText))
		appendResult(AssertionResult{
			Kind: AssertionForbiddenText, Passed: !found, Complete: found || !truncated,
		})
	}
	if !result.Complete {
		result.Passed = false
	}
	return result
}
