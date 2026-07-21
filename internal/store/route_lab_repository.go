/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/kuroky/nginx-uix/internal/config"
	"github.com/kuroky/nginx-uix/internal/routelab"
)

const (
	routeRunActiveLimit = 8
	routeRunStageLimit  = 512
)

const routeRunSelect = `SELECT id, workspace_id, workspace_revision, workspace_etag,
	production_digest, draft_digest, candidate_digest, state, stage, safe_request_json,
	static_analysis_json, terminal_result_json, replayable, side_effecting, body_bytes,
	body_digest, sensitive_header_names_json, last_error_code, created_by, request_id,
	cancel_requested_at, created_at, updated_at, started_at, finished_at FROM route_lab_runs`

// CreateRouteRun atomically persists one queued route run, initial stage and safe audit record.
func (database *DB) CreateRouteRun(ctx context.Context, run routelab.Run, stage routelab.RunStage) error {
	if err := validateRouteRun(run); err != nil || run.State != routelab.RunStateQueued ||
		run.Stage != routelab.RunStageQueued || stage.RunID != run.ID || stage.Sequence != 1 ||
		stage.Stage != run.Stage || stage.Result != routelab.StageResultPending || validateRouteRunStage(stage) != nil {
		return fmt.Errorf("create route run: invalid input")
	}
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		var active int
		if err := connection.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM route_lab_runs WHERE state IN ('queued', 'running')`,
		).Scan(&active); err != nil {
			return err
		}
		if active >= routeRunActiveLimit {
			return routelab.ErrBusy
		}
		_, err := connection.ExecContext(ctx, `INSERT INTO route_lab_runs(
			id, workspace_id, workspace_revision, workspace_etag, production_digest, draft_digest,
			candidate_digest, state, stage, safe_request_json, static_analysis_json,
			terminal_result_json, replayable, side_effecting, body_bytes, body_digest,
			sensitive_header_names_json, last_error_code, created_by, request_id,
			cancel_requested_at, created_at, updated_at, started_at, finished_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			run.ID, run.WorkspaceID, run.WorkspaceRevision, run.WorkspaceETag,
			run.ProductionDigest[:], run.DraftDigest[:], nullableRouteDigest(run.CandidateDigest),
			run.State, run.Stage, run.SafeRequestJSON, run.StaticAnalysisJSON, run.TerminalResultJSON,
			boolInt(run.Replayable), boolInt(run.SideEffecting), run.BodyBytes, run.BodyDigest[:],
			run.SensitiveHeaderNamesJSON, run.LastErrorCode, run.CreatedBy, run.RequestID,
			nullableTime(run.CancelRequestedAt), formatTime(run.CreatedAt), formatTime(run.UpdatedAt),
			nullableTime(run.StartedAt), nullableTime(run.FinishedAt),
		)
		if err != nil {
			return mapConfigConstraint("insert route run", err)
		}
		if err := insertRouteRunStage(ctx, connection, stage); err != nil {
			return err
		}
		return insertRouteRunAudit(ctx, connection, run, "create", run.CreatedAt)
	})
	if err != nil {
		return fmt.Errorf("create route run: %w", err)
	}
	return nil
}

// TransitionRouteRun applies one exact state/stage CAS and immutable next stage.
func (database *DB) TransitionRouteRun(
	ctx context.Context,
	expectedState routelab.RunState,
	expectedStage routelab.RunStageName,
	next routelab.Run,
	stage routelab.RunStage,
) error {
	if !routelab.ValidRunState(expectedState) || !routelab.ValidRunStage(expectedStage) ||
		validateRouteRun(next) != nil || validateRouteRunStage(stage) != nil ||
		stage.RunID != next.ID || stage.Stage != next.Stage {
		return fmt.Errorf("transition route run: invalid input")
	}
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		var currentState routelab.RunState
		var currentStage routelab.RunStageName
		var lastSequence int64
		if err := connection.QueryRowContext(ctx, `SELECT state, stage,
			COALESCE((SELECT MAX(sequence) FROM route_lab_stages WHERE run_id = route_lab_runs.id), 0)
			FROM route_lab_runs WHERE id = ?`, next.ID).Scan(&currentState, &currentStage, &lastSequence); err != nil {
			return err
		}
		if currentState != expectedState || currentStage != expectedStage || currentState.Terminal() ||
			lastSequence < 0 || uint64(lastSequence)+1 != stage.Sequence {
			return config.ErrConflict
		}
		result, err := connection.ExecContext(ctx, `UPDATE route_lab_runs SET
			candidate_digest = ?, state = ?, stage = ?, terminal_result_json = ?,
			last_error_code = ?, cancel_requested_at = COALESCE(cancel_requested_at, ?), updated_at = ?, started_at = ?, finished_at = ?
			WHERE id = ? AND state = ? AND stage = ?`,
			nullableRouteDigest(next.CandidateDigest), next.State, next.Stage, next.TerminalResultJSON,
			next.LastErrorCode, nullableTime(next.CancelRequestedAt), formatTime(next.UpdatedAt),
			nullableTime(next.StartedAt), nullableTime(next.FinishedAt), next.ID, expectedState, expectedStage,
		)
		if err != nil {
			return mapConfigConstraint("update route run", err)
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		return insertRouteRunStage(ctx, connection, stage)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("transition route run: %w", fs.ErrNotExist)
	}
	if err != nil {
		return fmt.Errorf("transition route run: %w", err)
	}
	return nil
}

// RouteRun returns one exact durable route run.
func (database *DB) RouteRun(ctx context.Context, id routelab.RunID) (routelab.Run, error) {
	if _, err := routelab.ParseRunID(string(id)); err != nil {
		return routelab.Run{}, fmt.Errorf("read route run: invalid id")
	}
	run, err := scanRouteRun(database.sql.QueryRowContext(ctx, routeRunSelect+" WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return routelab.Run{}, fmt.Errorf("read route run: %w", fs.ErrNotExist)
	}
	if err != nil {
		return routelab.Run{}, fmt.Errorf("read route run: %w", err)
	}
	return run, nil
}

// RouteRunStages returns immutable events after one sequence.
func (database *DB) RouteRunStages(
	ctx context.Context,
	id routelab.RunID,
	after uint64,
	limit int,
) (stages []routelab.RunStage, returnErr error) {
	if _, err := routelab.ParseRunID(string(id)); err != nil || after > math.MaxInt64 ||
		limit <= 0 || limit > routeRunStageLimit {
		return nil, fmt.Errorf("list route stages: invalid input")
	}
	rows, err := database.sql.QueryContext(ctx, `SELECT run_id, sequence, stage, result, code,
		public_details_json, occurred_at FROM route_lab_stages
		WHERE run_id = ? AND sequence > ? ORDER BY sequence ASC LIMIT ?`, id, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list route stages: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	for rows.Next() {
		stage, err := scanRouteRunStage(rows)
		if err != nil {
			return nil, fmt.Errorf("list route stages: %w", err)
		}
		stages = append(stages, stage)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list route stages: %w", err)
	}
	return stages, nil
}

// ListRouteRuns returns one stable newest-first keyset page.
func (database *DB) ListRouteRuns(
	ctx context.Context,
	query routelab.HistoryQuery,
) (_ []routelab.Run, returnErr error) {
	if !validRouteHistoryQuery(query) {
		return nil, fmt.Errorf("list route runs: invalid query")
	}
	statement := routeRunSelect + " WHERE 1 = 1"
	arguments := make([]any, 0, 8)
	if query.WorkspaceID != "" {
		statement += " AND workspace_id = ?"
		arguments = append(arguments, query.WorkspaceID)
	}
	if query.State != "" {
		statement += " AND state = ?"
		arguments = append(arguments, query.State)
	}
	if query.BeforeID != "" {
		statement += " AND (created_at < ? OR (created_at = ? AND id < ?))"
		formatted := formatTime(query.BeforeCreatedAt)
		arguments = append(arguments, formatted, formatted, query.BeforeID)
	}
	statement += " ORDER BY created_at DESC, id DESC LIMIT ?"
	arguments = append(arguments, query.Limit)
	rows, err := database.sql.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list route runs: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	runs := make([]routelab.Run, 0, query.Limit)
	for rows.Next() {
		run, err := scanRouteRun(rows)
		if err != nil {
			return nil, fmt.Errorf("list route runs: %w", err)
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list route runs: %w", err)
	}
	return runs, nil
}

// ActiveRouteRunCount returns the bounded active-run usage.
func (database *DB) ActiveRouteRunCount(ctx context.Context) (int, error) {
	var count int
	if err := database.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM route_lab_runs WHERE state IN ('queued', 'running')`,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active route runs: %w", err)
	}
	return count, nil
}

// RequestRouteRunCancellation durably requests cancellation or terminalizes a queued run.
func (database *DB) RequestRouteRunCancellation(
	ctx context.Context,
	id routelab.RunID,
	at time.Time,
) (routelab.Run, error) {
	if _, err := routelab.ParseRunID(string(id)); err != nil || at.IsZero() {
		return routelab.Run{}, fmt.Errorf("cancel route run: invalid input")
	}
	var updated routelab.Run
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		current, err := scanRouteRun(connection.QueryRowContext(ctx, routeRunSelect+" WHERE id = ?", id))
		if err != nil {
			return err
		}
		if current.State.Terminal() {
			return routelab.ErrAlreadyTerminal
		}
		if !current.CancelRequestedAt.IsZero() {
			updated = current
			return nil
		}
		var lastSequence uint64
		if err := connection.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(sequence), 0) FROM route_lab_stages WHERE run_id = ?`, id,
		).Scan(&lastSequence); err != nil {
			return err
		}
		current.CancelRequestedAt = at.UTC()
		current.UpdatedAt = at.UTC()
		stage := routelab.RunStage{
			RunID: id, Sequence: lastSequence + 1, Stage: current.Stage,
			Result: routelab.StageResultWarning, Code: "cancel_requested",
			PublicDetailsJSON: `{}`, OccurredAt: at,
		}
		if current.State == routelab.RunStateQueued {
			current.State = routelab.RunStateCancelled
			current.Stage = routelab.RunStageCancelled
			current.LastErrorCode = "cancelled_by_user"
			current.FinishedAt = at.UTC()
			stage.Stage = current.Stage
			stage.Result = routelab.StageResultFailed
			stage.Code = current.LastErrorCode
		}
		result, err := connection.ExecContext(ctx, `UPDATE route_lab_runs SET state = ?, stage = ?,
			last_error_code = ?, cancel_requested_at = ?, updated_at = ?, finished_at = ?
			WHERE id = ? AND state = ? AND cancel_requested_at IS NULL`,
			current.State, current.Stage, current.LastErrorCode, formatTime(current.CancelRequestedAt),
			formatTime(current.UpdatedAt), nullableTime(current.FinishedAt), id,
			map[bool]routelab.RunState{true: routelab.RunStateQueued, false: routelab.RunStateRunning}[current.State == routelab.RunStateCancelled],
		)
		if err != nil {
			return mapConfigConstraint("cancel route run", err)
		}
		matched, err := result.RowsAffected()
		if err != nil || matched != 1 {
			return errors.Join(config.ErrConflict, err)
		}
		if err := insertRouteRunStage(ctx, connection, stage); err != nil {
			return err
		}
		if err := insertRouteRunAudit(ctx, connection, current, "cancel", at); err != nil {
			return err
		}
		updated = current
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return routelab.Run{}, fmt.Errorf("cancel route run: %w", fs.ErrNotExist)
	}
	if err != nil {
		return routelab.Run{}, fmt.Errorf("cancel route run: %w", err)
	}
	return updated, nil
}

// FailInterruptedRouteRuns terminates all pre-restart active tasks without replaying requests.
func (database *DB) FailInterruptedRouteRuns(ctx context.Context, at time.Time, code string) (int, error) {
	if at.IsZero() || !validRouteCode(code) {
		return 0, fmt.Errorf("fail interrupted route runs: invalid input")
	}
	count := 0
	err := database.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		rows, err := connection.QueryContext(ctx, `SELECT r.id,
			COALESCE((SELECT MAX(sequence) FROM route_lab_stages WHERE run_id = r.id), 0)
			FROM route_lab_runs AS r WHERE r.state IN ('queued', 'running') ORDER BY r.created_at, r.id`)
		if err != nil {
			return err
		}
		type interrupted struct {
			id       routelab.RunID
			sequence uint64
		}
		items := make([]interrupted, 0)
		for rows.Next() {
			var item interrupted
			if err := rows.Scan(&item.id, &item.sequence); err != nil {
				_ = rows.Close()
				return err
			}
			items = append(items, item)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, item := range items {
			result, err := connection.ExecContext(ctx, `UPDATE route_lab_runs SET state = 'failed',
				stage = 'failed', last_error_code = ?, updated_at = ?, finished_at = ?
				WHERE id = ? AND state IN ('queued', 'running')`, code, formatTime(at), formatTime(at), item.id)
			if err != nil {
				return mapConfigConstraint("fail interrupted route run", err)
			}
			matched, err := result.RowsAffected()
			if err != nil || matched != 1 {
				return errors.Join(config.ErrConflict, err)
			}
			if err := insertRouteRunStage(ctx, connection, routelab.RunStage{
				RunID: item.id, Sequence: item.sequence + 1, Stage: routelab.RunStageFailed,
				Result: routelab.StageResultFailed, Code: code, PublicDetailsJSON: `{}`, OccurredAt: at,
			}); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("fail interrupted route runs: %w", err)
	}
	return count, nil
}

func insertRouteRunStage(ctx context.Context, connection *sql.Conn, stage routelab.RunStage) error {
	if validateRouteRunStage(stage) != nil {
		return fmt.Errorf("insert route stage: invalid input")
	}
	_, err := connection.ExecContext(ctx, `INSERT INTO route_lab_stages(
		run_id, sequence, stage, result, code, public_details_json, occurred_at
	) VALUES (?, ?, ?, ?, ?, ?, ?)`, stage.RunID, stage.Sequence, stage.Stage,
		stage.Result, stage.Code, stage.PublicDetailsJSON, formatTime(stage.OccurredAt))
	if err != nil {
		return mapConfigConstraint("insert route stage", err)
	}
	return nil
}

func insertRouteRunAudit(
	ctx context.Context,
	connection *sql.Conn,
	run routelab.Run,
	action string,
	at time.Time,
) error {
	var safe struct {
		Scheme string `json:"scheme"`
		Method string `json:"method"`
	}
	if err := json.Unmarshal([]byte(run.SafeRequestJSON), &safe); err != nil {
		return err
	}
	details, err := json.Marshal(map[string]any{
		"workspace_id": string(run.WorkspaceID), "method": safe.Method,
		"scheme": safe.Scheme, "side_effecting": run.SideEffecting,
	})
	if err != nil {
		return err
	}
	operation := config.OperationRecord{
		ID: string(run.ID) + ":" + action, ObjectType: "route_test", ObjectID: string(run.ID),
		Action: "route_lab." + action, Result: "success", RequestID: run.RequestID, OccurredAt: at,
	}
	audit := config.AuditEvent{
		OperationID: operation.ID, OccurredAt: at, ActorUserID: run.CreatedBy,
		Action: operation.Action, ObjectType: operation.ObjectType, ObjectID: operation.ObjectID,
		Result: operation.Result, RequestID: operation.RequestID, DetailsJSON: string(details),
	}
	return insertOperationAndAudit(ctx, connection, operation, audit)
}

type routeScanner interface {
	Scan(...any) error
}

func scanRouteRun(scanner routeScanner) (routelab.Run, error) {
	var run routelab.Run
	var productionDigest, draftDigest, candidateDigest, bodyDigest []byte
	var replayable, sideEffecting int
	var cancelRequestedAt, startedAt, finishedAt sql.NullString
	var createdAt, updatedAt string
	if err := scanner.Scan(
		&run.ID, &run.WorkspaceID, &run.WorkspaceRevision, &run.WorkspaceETag,
		&productionDigest, &draftDigest, &candidateDigest, &run.State, &run.Stage,
		&run.SafeRequestJSON, &run.StaticAnalysisJSON, &run.TerminalResultJSON,
		&replayable, &sideEffecting, &run.BodyBytes, &bodyDigest,
		&run.SensitiveHeaderNamesJSON, &run.LastErrorCode, &run.CreatedBy, &run.RequestID,
		&cancelRequestedAt, &createdAt, &updatedAt, &startedAt, &finishedAt,
	); err != nil {
		return routelab.Run{}, err
	}
	if !copyDigest(&run.ProductionDigest, productionDigest) || !copyDigest(&run.DraftDigest, draftDigest) ||
		!copyDigest(&run.BodyDigest, bodyDigest) ||
		(len(candidateDigest) != 0 && !copyDigest(&run.CandidateDigest, candidateDigest)) ||
		(replayable != 0 && replayable != 1) || (sideEffecting != 0 && sideEffecting != 1) {
		return routelab.Run{}, config.ErrConflict
	}
	run.Replayable = replayable == 1
	run.SideEffecting = sideEffecting == 1
	var err error
	if run.CreatedAt, err = parseTime("route run creation", createdAt); err != nil {
		return routelab.Run{}, err
	}
	if run.UpdatedAt, err = parseTime("route run update", updatedAt); err != nil {
		return routelab.Run{}, err
	}
	optionalValues := []sql.NullString{cancelRequestedAt, startedAt, finishedAt}
	optionalTargets := []*time.Time{&run.CancelRequestedAt, &run.StartedAt, &run.FinishedAt}
	for index, value := range optionalValues {
		if !value.Valid {
			continue
		}
		parsed, err := parseTime("route run optional time", value.String)
		if err != nil {
			return routelab.Run{}, err
		}
		*optionalTargets[index] = parsed
	}
	if err := validateRouteRun(run); err != nil {
		return routelab.Run{}, config.ErrConflict
	}
	return run, nil
}

func scanRouteRunStage(scanner routeScanner) (routelab.RunStage, error) {
	var stage routelab.RunStage
	var occurredAt string
	if err := scanner.Scan(&stage.RunID, &stage.Sequence, &stage.Stage, &stage.Result,
		&stage.Code, &stage.PublicDetailsJSON, &occurredAt); err != nil {
		return routelab.RunStage{}, err
	}
	parsed, err := parseTime("route stage occurrence", occurredAt)
	if err != nil {
		return routelab.RunStage{}, err
	}
	stage.OccurredAt = parsed
	if err := validateRouteRunStage(stage); err != nil {
		return routelab.RunStage{}, config.ErrConflict
	}
	return stage, nil
}

func validateRouteRun(run routelab.Run) error {
	if _, err := routelab.ParseRunID(string(run.ID)); err != nil {
		return err
	}
	if _, err := config.ParseWorkspaceID(string(run.WorkspaceID)); err != nil {
		return err
	}
	if run.WorkspaceRevision == 0 || run.WorkspaceETag == "" || len(run.WorkspaceETag) > 128 ||
		run.ProductionDigest == (config.Digest{}) || run.DraftDigest == (config.Digest{}) ||
		!routelab.ValidRunState(run.State) || !routelab.ValidRunStage(run.Stage) ||
		!validRouteJSON(run.SafeRequestJSON, 131072) || !validRouteJSON(run.StaticAnalysisJSON, 2097152) ||
		(run.TerminalResultJSON != "" && !validRouteJSON(run.TerminalResultJSON, 2097152)) ||
		run.BodyBytes < 0 || run.BodyBytes > 65536 || run.BodyDigest == (config.Digest{}) ||
		!validRouteJSON(run.SensitiveHeaderNamesJSON, 8192) || !validRouteCodeOrEmpty(run.LastErrorCode) ||
		run.CreatedBy <= 0 || !validRouteRequestID(run.RequestID) || run.CreatedAt.IsZero() || run.UpdatedAt.IsZero() ||
		run.UpdatedAt.Before(run.CreatedAt) {
		return fmt.Errorf("route run invalid")
	}
	switch run.State {
	case routelab.RunStateQueued:
		if run.Stage != routelab.RunStageQueued || !run.StartedAt.IsZero() || !run.FinishedAt.IsZero() ||
			run.TerminalResultJSON != "" || run.LastErrorCode != "" {
			return fmt.Errorf("queued route run invalid")
		}
	case routelab.RunStateRunning:
		if run.Stage == routelab.RunStageQueued || run.Stage == routelab.RunStageCompleted ||
			run.Stage == routelab.RunStageFailed || run.Stage == routelab.RunStageCancelled ||
			run.Stage == routelab.RunStageTimedOut || run.StartedAt.IsZero() || !run.FinishedAt.IsZero() ||
			run.TerminalResultJSON != "" || run.LastErrorCode != "" {
			return fmt.Errorf("running route run invalid")
		}
	case routelab.RunStateSucceeded:
		if run.Stage != routelab.RunStageCompleted || run.CandidateDigest == (config.Digest{}) ||
			run.TerminalResultJSON == "" || run.LastErrorCode != "" || run.StartedAt.IsZero() || run.FinishedAt.IsZero() {
			return fmt.Errorf("succeeded route run invalid")
		}
	case routelab.RunStateFailed, routelab.RunStateCancelled, routelab.RunStateTimedOut:
		expectedStage := map[routelab.RunState]routelab.RunStageName{
			routelab.RunStateFailed:    routelab.RunStageFailed,
			routelab.RunStateCancelled: routelab.RunStageCancelled,
			routelab.RunStateTimedOut:  routelab.RunStageTimedOut,
		}[run.State]
		if run.Stage != expectedStage || run.LastErrorCode == "" || run.FinishedAt.IsZero() {
			return fmt.Errorf("terminal route run invalid")
		}
	}
	return nil
}

func validateRouteRunStage(stage routelab.RunStage) error {
	if _, err := routelab.ParseRunID(string(stage.RunID)); err != nil || stage.Sequence == 0 ||
		!routelab.ValidRunStage(stage.Stage) || !routelab.ValidStageResult(stage.Result) ||
		!validRouteCodeOrEmpty(stage.Code) || !validRouteJSON(stage.PublicDetailsJSON, 65536) ||
		stage.OccurredAt.IsZero() {
		return fmt.Errorf("route stage invalid")
	}
	return nil
}

func validRouteHistoryQuery(query routelab.HistoryQuery) bool {
	if query.Limit <= 0 || query.Limit > 100 {
		return false
	}
	if query.WorkspaceID != "" {
		if _, err := config.ParseWorkspaceID(string(query.WorkspaceID)); err != nil {
			return false
		}
	}
	if query.State != "" && !routelab.ValidRunState(query.State) {
		return false
	}
	if (query.BeforeID == "") != query.BeforeCreatedAt.IsZero() {
		return false
	}
	if query.BeforeID != "" {
		if _, err := routelab.ParseRunID(string(query.BeforeID)); err != nil {
			return false
		}
	}
	return true
}

func validRouteJSON(value string, maximum int) bool {
	return len(value) >= 2 && len(value) <= maximum && json.Valid([]byte(value))
}

func validRouteCode(value string) bool {
	return value != "" && validRouteCodeOrEmpty(value)
}

func validRouteCodeOrEmpty(value string) bool {
	if len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validRouteRequestID(value string) bool {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func nullableRouteDigest(digest config.Digest) any {
	if digest == (config.Digest{}) {
		return nil
	}
	return digest[:]
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
