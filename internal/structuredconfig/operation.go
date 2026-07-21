/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package structuredconfig

import (
	"bytes"
	"encoding/binary"

	"github.com/kuroky/nginx-uix/internal/location"
	"github.com/kuroky/nginx-uix/internal/nginxast"
	"github.com/kuroky/nginx-uix/internal/upstream"
)

type plannedChange struct {
	kind     OperationKind
	targetID string
	edits    []nginxast.SourceEdit
}

func planOperation(
	project *nginxast.Project,
	projection Projection,
	operation Operation,
) (plannedChange, error) {
	if operationPayloadCount(operation) != 1 {
		return plannedChange{}, ErrInvalidOperation
	}
	switch operation.Kind {
	case OperationUpstreamCreate:
		if operation.UpstreamCreate == nil {
			return plannedChange{}, ErrInvalidOperation
		}
		plan, err := upstream.PlanCreate(project, projection.Upstreams, *operation.UpstreamCreate)
		return upstreamPlan(operation.Kind, plan, err)
	case OperationUpstreamRename:
		if operation.UpstreamRename == nil {
			return plannedChange{}, ErrInvalidOperation
		}
		plan, err := upstream.PlanRename(project, projection.Upstreams, *operation.UpstreamRename)
		return upstreamPlan(operation.Kind, plan, err)
	case OperationUpstreamDelete:
		if operation.UpstreamDelete == nil {
			return plannedChange{}, ErrInvalidOperation
		}
		plan, err := upstream.PlanDelete(project, projection.Upstreams, *operation.UpstreamDelete)
		return upstreamPlan(operation.Kind, plan, err)
	case OperationUpstreamServerCreate:
		if operation.UpstreamServerCreate == nil {
			return plannedChange{}, ErrInvalidOperation
		}
		plan, err := upstream.PlanCreateServer(project, projection.Upstreams, *operation.UpstreamServerCreate)
		return upstreamPlan(operation.Kind, plan, err)
	case OperationUpstreamServerUpdate:
		if operation.UpstreamServerUpdate == nil {
			return plannedChange{}, ErrInvalidOperation
		}
		plan, err := upstream.PlanUpdateServer(project, projection.Upstreams, *operation.UpstreamServerUpdate)
		return upstreamPlan(operation.Kind, plan, err)
	case OperationUpstreamServerDelete:
		if operation.UpstreamServerDelete == nil {
			return plannedChange{}, ErrInvalidOperation
		}
		plan, err := upstream.PlanDeleteServer(project, projection.Upstreams, *operation.UpstreamServerDelete)
		return upstreamPlan(operation.Kind, plan, err)
	case OperationLocationCreate:
		if operation.LocationCreate == nil {
			return plannedChange{}, ErrInvalidOperation
		}
		plan, err := location.PlanCreate(
			project, projection.Locations, projection.Upstreams, *operation.LocationCreate,
		)
		return locationPlan(operation.Kind, plan, err)
	case OperationLocationUpdate:
		if operation.LocationUpdate == nil {
			return plannedChange{}, ErrInvalidOperation
		}
		plan, err := location.PlanUpdate(
			project, projection.Locations, projection.Upstreams, *operation.LocationUpdate,
		)
		return locationPlan(operation.Kind, plan, err)
	case OperationLocationDelete:
		if operation.LocationDelete == nil {
			return plannedChange{}, ErrInvalidOperation
		}
		plan, err := location.PlanDelete(project, projection.Locations, *operation.LocationDelete)
		return locationPlan(operation.Kind, plan, err)
	default:
		return plannedChange{}, ErrInvalidOperation
	}
}

func upstreamPlan(kind OperationKind, plan upstream.Plan, err error) (plannedChange, error) {
	if err != nil {
		return plannedChange{}, err
	}
	if OperationKind(plan.Kind) != kind || plan.TargetID == "" || len(plan.Edits) == 0 {
		return plannedChange{}, ErrPostcondition
	}
	return plannedChange{
		kind: kind, targetID: plan.TargetID, edits: append([]nginxast.SourceEdit(nil), plan.Edits...),
	}, nil
}

func locationPlan(kind OperationKind, plan location.Plan, err error) (plannedChange, error) {
	if err != nil {
		return plannedChange{}, err
	}
	if OperationKind(plan.Kind) != kind || plan.TargetID == "" || len(plan.Edits) == 0 {
		return plannedChange{}, ErrPostcondition
	}
	return plannedChange{
		kind: kind, targetID: plan.TargetID, edits: append([]nginxast.SourceEdit(nil), plan.Edits...),
	}, nil
}

func operationPayloadCount(operation Operation) int {
	count := 0
	for _, present := range []bool{
		operation.UpstreamCreate != nil,
		operation.UpstreamRename != nil,
		operation.UpstreamDelete != nil,
		operation.UpstreamServerCreate != nil,
		operation.UpstreamServerUpdate != nil,
		operation.UpstreamServerDelete != nil,
		operation.LocationCreate != nil,
		operation.LocationUpdate != nil,
		operation.LocationDelete != nil,
	} {
		if present {
			count++
		}
	}
	return count
}

func canonicalOperation(operation Operation) ([]byte, error) {
	if operationPayloadCount(operation) != 1 {
		return nil, ErrInvalidOperation
	}
	var writer canonicalWriter
	writer.string(string(operation.Kind))
	switch operation.Kind {
	case OperationUpstreamCreate:
		if operation.UpstreamCreate == nil {
			return nil, ErrInvalidOperation
		}
		value := operation.UpstreamCreate
		writer.string(value.HTTPBlockID)
		writer.string(value.Name)
		writer.uint64(uint64(len(value.Servers)))
		for _, server := range value.Servers {
			writer.server(server)
		}
	case OperationUpstreamRename:
		if operation.UpstreamRename == nil {
			return nil, ErrInvalidOperation
		}
		writer.string(operation.UpstreamRename.UpstreamID)
		writer.string(operation.UpstreamRename.NewName)
	case OperationUpstreamDelete:
		if operation.UpstreamDelete == nil {
			return nil, ErrInvalidOperation
		}
		writer.string(operation.UpstreamDelete.UpstreamID)
		writer.string(operation.UpstreamDelete.ConfirmName)
	case OperationUpstreamServerCreate:
		if operation.UpstreamServerCreate == nil {
			return nil, ErrInvalidOperation
		}
		writer.string(operation.UpstreamServerCreate.UpstreamID)
		writer.server(operation.UpstreamServerCreate.Server)
	case OperationUpstreamServerUpdate:
		if operation.UpstreamServerUpdate == nil {
			return nil, ErrInvalidOperation
		}
		writer.string(operation.UpstreamServerUpdate.UpstreamID)
		writer.string(operation.UpstreamServerUpdate.ServerID)
		writer.server(operation.UpstreamServerUpdate.Server)
	case OperationUpstreamServerDelete:
		if operation.UpstreamServerDelete == nil {
			return nil, ErrInvalidOperation
		}
		writer.string(operation.UpstreamServerDelete.UpstreamID)
		writer.string(operation.UpstreamServerDelete.ServerID)
	case OperationLocationCreate:
		if operation.LocationCreate == nil {
			return nil, ErrInvalidOperation
		}
		writer.string(operation.LocationCreate.ParentID)
		writer.string(string(operation.LocationCreate.Type))
		writer.string(operation.LocationCreate.Matcher)
		writer.proxy(operation.LocationCreate.ProxyPass)
	case OperationLocationUpdate:
		if operation.LocationUpdate == nil {
			return nil, ErrInvalidOperation
		}
		writer.string(operation.LocationUpdate.LocationID)
		writer.string(string(operation.LocationUpdate.Type))
		writer.string(operation.LocationUpdate.Matcher)
		writer.string(string(operation.LocationUpdate.ProxyMode))
		writer.proxy(operation.LocationUpdate.ProxyPass)
	case OperationLocationDelete:
		if operation.LocationDelete == nil {
			return nil, ErrInvalidOperation
		}
		writer.string(operation.LocationDelete.LocationID)
		writer.string(operation.LocationDelete.ConfirmMatcher)
	default:
		return nil, ErrInvalidOperation
	}
	return writer.Bytes(), nil
}

type canonicalWriter struct {
	bytes.Buffer
}

func (w *canonicalWriter) uint64(value uint64) {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], value)
	_, _ = w.Write(raw[:])
}

func (w *canonicalWriter) string(value string) {
	w.uint64(uint64(len(value)))
	_, _ = w.WriteString(value)
}

func (w *canonicalWriter) boolean(value bool) {
	if value {
		_ = w.WriteByte(1)
		return
	}
	_ = w.WriteByte(0)
}

func (w *canonicalWriter) optionalUint16(value *uint16) {
	w.boolean(value != nil)
	if value != nil {
		w.uint64(uint64(*value))
	}
}

func (w *canonicalWriter) optionalInt(value *int) {
	w.boolean(value != nil)
	if value != nil {
		w.uint64(uint64(int64(*value))) // #nosec G115 -- canonical encoding intentionally preserves the signed two's-complement bit pattern.
	}
}

func (w *canonicalWriter) optionalString(value *string) {
	w.boolean(value != nil)
	if value != nil {
		w.string(*value)
	}
}

func (w *canonicalWriter) server(value upstream.ServerInput) {
	w.string(value.Endpoint.Address)
	w.optionalUint16(value.Endpoint.Port)
	w.boolean(value.Endpoint.Unix)
	w.optionalInt(value.Weight)
	w.boolean(value.Backup)
	w.boolean(value.Down)
	w.optionalInt(value.MaxFails)
	w.optionalString(value.FailTimeout)
}

func (w *canonicalWriter) proxy(value *location.ProxyPassInput) {
	w.boolean(value != nil)
	if value == nil {
		return
	}
	w.string(value.UpstreamID)
	w.string(value.Scheme)
	w.optionalUint16(value.Port)
	w.string(value.URI)
}
