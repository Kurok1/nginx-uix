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
	"time"
)

var errControlSchemaUnsupported = errors.New("control schema unsupported")

const (
	// ControlSchemaVersion is the only workspace control-record schema understood by this release.
	ControlSchemaVersion        uint16 = 1
	controlStatePath                   = RelativePath("control/state.json")
	controlManifestPath                = RelativePath("control/manifest.bin")
	controlPreparedManifestPath        = RelativePath("control/manifest.prepared.bin")
	controlFileMode                    = fs.FileMode(0o600)
	controlStateLimit                  = int64(4 << 10)
)

// ControlState is the durable filesystem-side workspace lifecycle record.
type ControlState struct {
	SchemaVersion   uint16         `json:"schema_version"`
	WorkspaceID     WorkspaceID    `json:"workspace_id"`
	State           WorkspaceState `json:"state"`
	StateReasonCode string         `json:"state_reason_code"`
	Revision        uint64         `json:"revision"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type controlStateRecord struct {
	SchemaVersion   uint16         `json:"schema_version"`
	WorkspaceID     WorkspaceID    `json:"workspace_id"`
	State           WorkspaceState `json:"state"`
	StateReasonCode string         `json:"state_reason_code"`
	Revision        uint64         `json:"revision"`
	UpdatedAt       string         `json:"updated_at"`
}

// ReadControlState reads and strictly validates the canonical state record.
func ReadControlState(ctx context.Context, root *ScopedRoot) (ControlState, error) {
	payload, info, err := root.ReadRegular(ctx, controlStatePath, controlStateLimit)
	if err != nil {
		return ControlState{}, fmt.Errorf("read workspace control state: %w", err)
	}
	if info.Mode().Perm() != controlFileMode {
		return ControlState{}, fmt.Errorf("read workspace control state: %w", ErrPathInvalid)
	}

	var record controlStateRecord
	if err := rejectDuplicateJSONFields(payload); err != nil {
		return ControlState{}, fmt.Errorf("decode workspace control state: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return ControlState{}, fmt.Errorf("decode workspace control state: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ControlState{}, fmt.Errorf("decode workspace control state: trailing data")
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, record.UpdatedAt)
	if err != nil || record.UpdatedAt != updatedAt.UTC().Format(time.RFC3339Nano) {
		return ControlState{}, fmt.Errorf("decode workspace control state: invalid updated_at")
	}
	state := ControlState{
		SchemaVersion:   record.SchemaVersion,
		WorkspaceID:     record.WorkspaceID,
		State:           record.State,
		StateReasonCode: record.StateReasonCode,
		Revision:        record.Revision,
		UpdatedAt:       updatedAt.UTC(),
	}
	if err := validateControlState(state); err != nil {
		return ControlState{}, err
	}
	return state, nil
}

func rejectDuplicateJSONFields(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := consumeStrictJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("invalid trailing JSON")
	}
	return nil
}

func consumeStrictJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			rawKey, err := decoder.Token()
			key, ok := rawKey.(string)
			if err != nil || !ok {
				return fmt.Errorf("invalid JSON object key")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON field")
			}
			seen[key] = struct{}{}
			if err := consumeStrictJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return fmt.Errorf("invalid JSON object")
		}
	case '[':
		for decoder.More() {
			if err := consumeStrictJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return fmt.Errorf("invalid JSON array")
		}
	default:
		return fmt.Errorf("invalid JSON delimiter")
	}
	return nil
}

// WriteControlState atomically publishes one canonical UTC state record.
func WriteControlState(ctx context.Context, root *ScopedRoot, state ControlState) error {
	state.UpdatedAt = state.UpdatedAt.UTC()
	payload, err := marshalControlState(state)
	if err != nil {
		return err
	}
	if err := root.AtomicReplace(ctx, controlStatePath, payload, controlFileMode); err != nil {
		return fmt.Errorf("write workspace control state: %w", err)
	}
	return nil
}

func marshalControlState(state ControlState) ([]byte, error) {
	state.UpdatedAt = state.UpdatedAt.UTC()
	if err := validateControlState(state); err != nil {
		return nil, err
	}
	record := controlStateRecord{
		SchemaVersion:   state.SchemaVersion,
		WorkspaceID:     state.WorkspaceID,
		State:           state.State,
		StateReasonCode: state.StateReasonCode,
		Revision:        state.Revision,
		UpdatedAt:       state.UpdatedAt.Format(time.RFC3339Nano),
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("encode workspace control state: %w", err)
	}
	payload = append(payload, '\n')
	if int64(len(payload)) > controlStateLimit {
		return nil, fmt.Errorf("encode workspace control state: %w", ErrLimitExceeded)
	}
	return payload, nil
}

// ReadControlManifest reads one bounded canonical binary manifest.
func ReadControlManifest(ctx context.Context, root *ScopedRoot, limits Limits) (Manifest, error) {
	maximum := maximumManifestPayload(limits)
	if maximum <= 0 {
		return Manifest{}, fmt.Errorf("read workspace control manifest: %w", ErrLimitExceeded)
	}
	payload, info, err := root.ReadRegular(ctx, controlManifestPath, int64(maximum))
	if err != nil {
		return Manifest{}, fmt.Errorf("read workspace control manifest: %w", err)
	}
	if info.Mode().Perm() != controlFileMode {
		return Manifest{}, fmt.Errorf("read workspace control manifest: %w", ErrPathInvalid)
	}
	manifest, err := ParseManifest(payload, limits)
	if err != nil {
		return Manifest{}, fmt.Errorf("read workspace control manifest: %w", err)
	}
	return manifest, nil
}

// WriteControlManifest atomically publishes one canonical binary manifest.
func WriteControlManifest(ctx context.Context, root *ScopedRoot, manifest Manifest) error {
	payload, err := manifest.MarshalBinary()
	if err != nil {
		return fmt.Errorf("encode workspace control manifest: %w", err)
	}
	if err := root.AtomicReplace(ctx, controlManifestPath, payload, controlFileMode); err != nil {
		return fmt.Errorf("write workspace control manifest: %w", err)
	}
	return nil
}

func readPreparedControlManifest(ctx context.Context, root *ScopedRoot, limits Limits) (Manifest, error) {
	return readManifestAt(ctx, root, controlPreparedManifestPath, limits)
}

func writePreparedControlManifest(ctx context.Context, root *ScopedRoot, manifest Manifest) error {
	payload, err := manifest.MarshalBinary()
	if err != nil {
		return fmt.Errorf("encode prepared workspace manifest: %w", err)
	}
	if err := root.AtomicReplace(ctx, controlPreparedManifestPath, payload, controlFileMode); err != nil {
		return fmt.Errorf("write prepared workspace manifest: %w", err)
	}
	return nil
}

func readManifestAt(ctx context.Context, root *ScopedRoot, path RelativePath, limits Limits) (Manifest, error) {
	maximum := maximumManifestPayload(limits)
	if maximum <= 0 {
		return Manifest{}, fmt.Errorf("read workspace manifest: %w", ErrLimitExceeded)
	}
	payload, info, err := root.ReadRegular(ctx, path, int64(maximum))
	if err != nil {
		return Manifest{}, fmt.Errorf("read workspace manifest: %w", err)
	}
	if info.Mode().Perm() != controlFileMode {
		return Manifest{}, fmt.Errorf("read workspace manifest: %w", ErrPathInvalid)
	}
	manifest, err := ParseManifest(payload, limits)
	if err != nil {
		return Manifest{}, fmt.Errorf("read workspace manifest: %w", err)
	}
	return manifest, nil
}

func validateControlState(state ControlState) error {
	if state.SchemaVersion != ControlSchemaVersion {
		return fmt.Errorf("validate workspace control state: %w", errControlSchemaUnsupported)
	}
	if _, err := ParseWorkspaceID(string(state.WorkspaceID)); err != nil {
		return fmt.Errorf("validate workspace control state: %w", err)
	}
	switch state.State {
	case StatePreparing, StateReady, StateStale, StatePublished, StateNeedsAttention:
	default:
		return fmt.Errorf("validate workspace control state: invalid state")
	}
	if state.Revision == 0 || state.UpdatedAt.IsZero() {
		return fmt.Errorf("validate workspace control state: invalid revision or updated_at")
	}
	if len(state.StateReasonCode) > 128 {
		return fmt.Errorf("validate workspace control state: invalid reason code")
	}
	return nil
}
