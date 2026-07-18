/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kuroky/nginx-uix/internal/config"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func operationAuditReplay(
	ctx context.Context,
	connection *sql.Conn,
	operation config.OperationRecord,
	audit config.AuditEvent,
) (bool, error) {
	existingOperation, existingAudit, found, err := readOperationAudit(ctx, connection, operation.ID)
	if err != nil || !found {
		return false, err
	}
	if !sameOperation(existingOperation, operation, formatTime(existingOperation.OccurredAt)) || !sameAudit(existingAudit, audit) {
		return false, config.ErrConflict
	}
	return true, nil
}

func sameAudit(existing, requested config.AuditEvent) bool {
	return existing.OperationID == requested.OperationID &&
		formatTime(existing.OccurredAt) == formatTime(requested.OccurredAt) &&
		existing.ActorUserID == requested.ActorUserID && existing.Action == requested.Action &&
		existing.ObjectType == requested.ObjectType && existing.ObjectID == requested.ObjectID &&
		existing.Result == requested.Result && existing.RequestID == requested.RequestID &&
		existing.DetailsJSON == requested.DetailsJSON
}

type operationAuditQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// OperationAudit returns the exact linked records for one idempotent configuration operation.
func (d *DB) OperationAudit(ctx context.Context, operationID string) (config.OperationRecord, config.AuditEvent, bool, error) {
	if ctx == nil || operationID == "" {
		return config.OperationRecord{}, config.AuditEvent{}, false, fmt.Errorf("read configuration operation: invalid input")
	}
	operation, audit, found, err := readOperationAudit(ctx, d.sql, operationID)
	if err != nil {
		return config.OperationRecord{}, config.AuditEvent{}, false, fmt.Errorf("read configuration operation: %w", err)
	}
	return operation, audit, found, nil
}

func readOperationAudit(
	ctx context.Context,
	queryer operationAuditQueryer,
	operationID string,
) (config.OperationRecord, config.AuditEvent, bool, error) {
	var existingOperation config.OperationRecord
	var beforeDigest, afterDigest []byte
	var occurredAt string
	operationErr := queryer.QueryRowContext(
		ctx,
		`SELECT id, object_type, object_id, action, before_digest, after_digest, result, request_id, occurred_at
		 FROM config_operations WHERE id = ?`,
		operationID,
	).Scan(
		&existingOperation.ID,
		&existingOperation.ObjectType,
		&existingOperation.ObjectID,
		&existingOperation.Action,
		&beforeDigest,
		&afterDigest,
		&existingOperation.Result,
		&existingOperation.RequestID,
		&occurredAt,
	)

	var existingAudit config.AuditEvent
	var actorUserID sql.NullInt64
	var auditOccurredAt string
	auditErr := queryer.QueryRowContext(
		ctx,
		`SELECT operation_id, occurred_at, actor_user_id, action, object_type, object_id, result, request_id, details_json
		 FROM audit_events WHERE operation_id = ?`,
		operationID,
	).Scan(
		&existingAudit.OperationID,
		&auditOccurredAt,
		&actorUserID,
		&existingAudit.Action,
		&existingAudit.ObjectType,
		&existingAudit.ObjectID,
		&existingAudit.Result,
		&existingAudit.RequestID,
		&existingAudit.DetailsJSON,
	)
	if errors.Is(operationErr, sql.ErrNoRows) && errors.Is(auditErr, sql.ErrNoRows) {
		return config.OperationRecord{}, config.AuditEvent{}, false, nil
	}
	if operationErr != nil || auditErr != nil {
		if errors.Is(operationErr, sql.ErrNoRows) || errors.Is(auditErr, sql.ErrNoRows) {
			return config.OperationRecord{}, config.AuditEvent{}, false, config.ErrConflict
		}
		return config.OperationRecord{}, config.AuditEvent{}, false, errors.Join(operationErr, auditErr)
	}
	if !actorUserID.Valid {
		return config.OperationRecord{}, config.AuditEvent{}, false, config.ErrConflict
	}

	existingOperation.BeforeDigest = digestPointer(beforeDigest)
	existingOperation.AfterDigest = digestPointer(afterDigest)
	parsedOccurredAt, err := parseTime("configuration operation time", occurredAt)
	if err != nil || formatTime(parsedOccurredAt) != occurredAt {
		return config.OperationRecord{}, config.AuditEvent{}, false, config.ErrConflict
	}
	parsedAuditOccurredAt, err := parseTime("configuration audit time", auditOccurredAt)
	if err != nil || formatTime(parsedAuditOccurredAt) != auditOccurredAt {
		return config.OperationRecord{}, config.AuditEvent{}, false, config.ErrConflict
	}
	existingOperation.OccurredAt = parsedOccurredAt
	existingAudit.ActorUserID = actorUserID.Int64
	existingAudit.OccurredAt = parsedAuditOccurredAt
	return existingOperation, existingAudit, true, nil
}

func sameOperation(existing, requested config.OperationRecord, existingOccurredAt string) bool {
	return existing.ID == requested.ID && existing.ObjectType == requested.ObjectType &&
		existing.ObjectID == requested.ObjectID && existing.Action == requested.Action &&
		sameDigest(existing.BeforeDigest, requested.BeforeDigest) &&
		sameDigest(existing.AfterDigest, requested.AfterDigest) && existing.Result == requested.Result &&
		existing.RequestID == requested.RequestID && existingOccurredAt == formatTime(requested.OccurredAt)
}

func sameDigest(left, right *config.Digest) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return bytes.Equal(left[:], right[:])
}

func digestPointer(raw []byte) *config.Digest {
	if raw == nil {
		return nil
	}
	var digest config.Digest
	if len(raw) != len(digest) {
		return &digest
	}
	copy(digest[:], raw)
	return &digest
}

func insertOperationAndAudit(
	ctx context.Context,
	connection *sql.Conn,
	operation config.OperationRecord,
	audit config.AuditEvent,
) error {
	if err := validateOperationAudit(operation, audit); err != nil {
		return err
	}

	if _, err := connection.ExecContext(
		ctx,
		`INSERT INTO config_operations(
			id, object_type, object_id, action, before_digest, after_digest, result, request_id, occurred_at
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		operation.ID,
		operation.ObjectType,
		operation.ObjectID,
		operation.Action,
		nullableDigest(operation.BeforeDigest),
		nullableDigest(operation.AfterDigest),
		operation.Result,
		operation.RequestID,
		formatTime(operation.OccurredAt),
	); err != nil {
		return mapConfigConstraint("insert configuration operation", err)
	}
	return insertAudit(ctx, connection, audit)
}

func insertAudit(ctx context.Context, connection *sql.Conn, audit config.AuditEvent) error {
	if _, err := connection.ExecContext(
		ctx,
		`INSERT INTO audit_events(
			occurred_at, actor_user_id, action, object_type, object_id, result, request_id, details_json, operation_id
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		formatTime(audit.OccurredAt),
		audit.ActorUserID,
		audit.Action,
		audit.ObjectType,
		audit.ObjectID,
		audit.Result,
		audit.RequestID,
		audit.DetailsJSON,
		nullableOperationID(audit.OperationID),
	); err != nil {
		return mapConfigConstraint("insert configuration audit", err)
	}
	return nil
}

func nullableOperationID(operationID string) any {
	if operationID == "" {
		return nil
	}
	return operationID
}

func validateOperationAudit(operation config.OperationRecord, audit config.AuditEvent) error {
	if operation.ID == "" || audit.OperationID != operation.ID {
		return fmt.Errorf("validate configuration audit: linked operation id required")
	}
	if audit.Action != operation.Action || audit.ObjectType != operation.ObjectType ||
		audit.ObjectID != operation.ObjectID || audit.Result != operation.Result ||
		audit.RequestID != operation.RequestID {
		return fmt.Errorf("validate configuration audit: operation metadata mismatch")
	}

	var details map[string]json.RawMessage
	if err := json.Unmarshal([]byte(audit.DetailsJSON), &details); err != nil || details == nil {
		return fmt.Errorf("validate configuration audit details: JSON object required")
	}
	return nil
}

func nullableDigest(digest *config.Digest) any {
	if digest == nil {
		return nil
	}
	return digest[:]
}

func mapConfigConstraint(action string, err error) error {
	var sqliteError *sqlite.Error
	if errors.As(err, &sqliteError) && sqliteError.Code()&0xff == sqlite3.SQLITE_CONSTRAINT {
		return fmt.Errorf("%s: %w", action, config.ErrConflict)
	}
	return fmt.Errorf("%s: %w", action, err)
}
