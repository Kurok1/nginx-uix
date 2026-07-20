/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"time"
)

const (
	// JournalSchemaVersion is the only durable mutation-journal schema understood by this release.
	JournalSchemaVersion uint16 = 1
	journalPayloadLimit         = int64(16 << 10)
)

var journalPath = RelativePath("control/journal.json")

// JournalPhase identifies the last durable mutation boundary.
type JournalPhase string

const (
	// JournalPrepared means recovery material is durable and draft bytes are unchanged.
	JournalPrepared JournalPhase = "prepared"
	// JournalFilesApplied means the draft and next manifest are durable.
	JournalFilesApplied JournalPhase = "files_applied"
	// JournalSQLCommitted means workspace metadata and audit are durable.
	JournalSQLCommitted JournalPhase = "sql_committed"
	// JournalControlCommitted means the public control records are durable.
	JournalControlCommitted JournalPhase = "control_committed"
)

// Journal is the content-free durable state machine for one workspace mutation.
type Journal struct {
	SchemaVersion    uint16         `json:"schema_version"`
	OperationID      string         `json:"operation_id"`
	Kind             string         `json:"kind"`
	Phase            JournalPhase   `json:"phase"`
	WorkspaceID      WorkspaceID    `json:"workspace_id"`
	ExpectedRevision uint64         `json:"expected_revision"`
	NextRevision     uint64         `json:"next_revision"`
	BeforeDigest     Digest         `json:"before_digest"`
	AfterDigest      Digest         `json:"after_digest"`
	Actor            Actor          `json:"actor"`
	Paths            []RelativePath `json:"paths"`
	StartedAt        time.Time      `json:"started_at"`
}

type journalRecord struct {
	SchemaVersion    uint16         `json:"schema_version"`
	OperationID      string         `json:"operation_id"`
	Kind             string         `json:"kind"`
	Phase            JournalPhase   `json:"phase"`
	WorkspaceID      WorkspaceID    `json:"workspace_id"`
	ExpectedRevision uint64         `json:"expected_revision"`
	NextRevision     uint64         `json:"next_revision"`
	BeforeDigest     string         `json:"before_digest"`
	AfterDigest      string         `json:"after_digest"`
	Actor            Actor          `json:"actor"`
	Paths            []RelativePath `json:"paths"`
	StartedAt        string         `json:"started_at"`
}

// ReadJournal reads one strict owner-only durable mutation journal.
func ReadJournal(ctx context.Context, root *ScopedRoot) (Journal, error) {
	if ctx == nil || root == nil {
		return Journal{}, fmt.Errorf("read workspace journal: %w", ErrPathInvalid)
	}
	payload, info, err := root.ReadRegular(ctx, journalPath, journalPayloadLimit)
	if err != nil {
		return Journal{}, fmt.Errorf("read workspace journal: %w", err)
	}
	if info.Mode().Perm() != controlFileMode {
		return Journal{}, fmt.Errorf("read workspace journal: %w", ErrPathInvalid)
	}
	if err := rejectDuplicateJSONFields(payload); err != nil {
		return Journal{}, fmt.Errorf("decode workspace journal: invalid JSON")
	}
	var record journalRecord
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return Journal{}, fmt.Errorf("decode workspace journal: invalid JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Journal{}, fmt.Errorf("decode workspace journal: trailing data")
	}
	before, err := ParseDigest(record.BeforeDigest)
	if err != nil {
		return Journal{}, fmt.Errorf("decode workspace journal: invalid before digest")
	}
	after, err := ParseDigest(record.AfterDigest)
	if err != nil {
		return Journal{}, fmt.Errorf("decode workspace journal: invalid after digest")
	}
	startedAt, err := time.Parse(time.RFC3339Nano, record.StartedAt)
	if err != nil || record.StartedAt != startedAt.UTC().Format(time.RFC3339Nano) {
		return Journal{}, fmt.Errorf("decode workspace journal: invalid started_at")
	}
	journal := Journal{
		SchemaVersion: record.SchemaVersion, OperationID: record.OperationID, Kind: record.Kind,
		Phase: record.Phase, WorkspaceID: record.WorkspaceID, ExpectedRevision: record.ExpectedRevision,
		NextRevision: record.NextRevision, BeforeDigest: before, AfterDigest: after, Actor: record.Actor,
		Paths: slices.Clone(record.Paths), StartedAt: startedAt.UTC(),
	}
	if err := validateJournal(journal); err != nil {
		return Journal{}, err
	}
	return journal, nil
}

// WriteJournal atomically publishes one canonical owner-only mutation journal.
func WriteJournal(ctx context.Context, root *ScopedRoot, journal Journal) error {
	if ctx == nil || root == nil {
		return fmt.Errorf("write workspace journal: %w", ErrPathInvalid)
	}
	journal.StartedAt = journal.StartedAt.UTC()
	if err := validateJournal(journal); err != nil {
		return err
	}
	record := journalRecord{
		SchemaVersion: journal.SchemaVersion, OperationID: journal.OperationID, Kind: journal.Kind,
		Phase: journal.Phase, WorkspaceID: journal.WorkspaceID, ExpectedRevision: journal.ExpectedRevision,
		NextRevision: journal.NextRevision, BeforeDigest: journal.BeforeDigest.String(),
		AfterDigest: journal.AfterDigest.String(), Actor: journal.Actor, Paths: slices.Clone(journal.Paths),
		StartedAt: journal.StartedAt.Format(time.RFC3339Nano),
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode workspace journal: %w", err)
	}
	payload = append(payload, '\n')
	if int64(len(payload)) > journalPayloadLimit {
		return fmt.Errorf("encode workspace journal: %w", ErrLimitExceeded)
	}
	if err := root.AtomicReplace(ctx, journalPath, payload, controlFileMode); err != nil {
		return fmt.Errorf("write workspace journal: %w", err)
	}
	return nil
}

// RemoveJournal durably removes the journal and succeeds when it is already absent.
func RemoveJournal(ctx context.Context, root *ScopedRoot) error {
	if ctx == nil || root == nil {
		return fmt.Errorf("remove workspace journal: %w", ErrPathInvalid)
	}
	if err := root.RemoveRegular(ctx, journalPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove workspace journal: %w", err)
	}
	return nil
}

func validateJournal(journal Journal) error {
	if journal.SchemaVersion != JournalSchemaVersion || !validOpaqueID(journal.OperationID) {
		return fmt.Errorf("validate workspace journal: invalid metadata")
	}
	if _, err := ParseWorkspaceID(string(journal.WorkspaceID)); err != nil {
		return fmt.Errorf("validate workspace journal: %w", err)
	}
	switch journal.Phase {
	case JournalPrepared, JournalFilesApplied, JournalSQLCommitted, JournalControlCommitted:
	default:
		return fmt.Errorf("validate workspace journal: invalid phase")
	}
	expectedPaths := 0
	switch journal.Kind {
	case "create", "replace", "delete":
		expectedPaths = 1
	case "copy", "rename":
		expectedPaths = 2
	case "workspace_create":
	case "workspace_delete":
		if journal.AfterDigest != journal.BeforeDigest {
			return fmt.Errorf("validate workspace journal: delete digest changed")
		}
	default:
		return fmt.Errorf("validate workspace journal: invalid kind")
	}
	if journal.ExpectedRevision == 0 || journal.NextRevision != journal.ExpectedRevision+1 ||
		journal.BeforeDigest == (Digest{}) || journal.AfterDigest == (Digest{}) ||
		journal.StartedAt.IsZero() || journal.StartedAt != journal.StartedAt.UTC() ||
		len(journal.Paths) != expectedPaths {
		return fmt.Errorf("validate workspace journal: invalid metadata")
	}
	if err := validateActor(journal.Actor); err != nil {
		return fmt.Errorf("validate workspace journal: %w", err)
	}
	seen := make(map[RelativePath]struct{}, len(journal.Paths))
	for _, raw := range journal.Paths {
		path, err := ParseRelativePath(string(raw), DefaultLimits())
		if err != nil || path != raw {
			return fmt.Errorf("validate workspace journal: %w", ErrPathInvalid)
		}
		if _, duplicate := seen[path]; duplicate {
			return fmt.Errorf("validate workspace journal: duplicate path")
		}
		seen[path] = struct{}{}
	}
	return nil
}
