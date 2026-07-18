/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"slices"
	"strconv"
	"time"
)

const (
	groupCollectionSchema     = "groups-v1"
	groupCollectionObjectType = "group_collection"
	groupCollectionObjectID   = "global"
)

// GroupView is one logical group with optional selected-workspace missing members.
type GroupView struct {
	Group   Group
	Missing []RelativePath
}

// GroupCollectionView is the complete logical group collection and its independent ETag.
type GroupCollectionView struct {
	Groups []GroupView
	ETag   string
}

// CreateGroupInput describes one new logical group guarded by the collection ETag.
type CreateGroupInput struct {
	Name      string
	SortOrder int
	Members   []RelativePath
	IfMatch   string
}

// ReplaceGroupInput describes a complete logical group replacement.
type ReplaceGroupInput struct {
	Name      string
	SortOrder int
	Members   []RelativePath
	IfMatch   string
}

// DeleteGroupInput describes a named logical group deletion.
type DeleteGroupInput struct {
	ConfirmName string
	IfMatch     string
}

// ETag returns the strong identity of the complete revisioned group collection.
func (c GroupCollection) ETag() string {
	return GroupETag(c.digest())
}

// ListGroups returns the canonical global collection with optional missing-member markers.
func (s *Service) ListGroups(ctx context.Context, workspaceID *WorkspaceID) (GroupCollectionView, error) {
	if ctx == nil || s == nil {
		return GroupCollectionView{}, fmt.Errorf("list configuration groups: service is unavailable")
	}
	collection, err := s.groups.GroupCollection(ctx)
	if err != nil {
		return GroupCollectionView{}, fmt.Errorf("list configuration groups: %w", err)
	}
	collection = canonicalGroupCollection(collection)

	var present map[RelativePath]struct{}
	if workspaceID != nil {
		tree, err := s.Tree(ctx, *workspaceID)
		if err != nil {
			return GroupCollectionView{}, fmt.Errorf("list configuration groups: read selected workspace: %w", err)
		}
		present = make(map[RelativePath]struct{}, len(tree.Entries))
		for _, entry := range tree.Entries {
			present[entry.Path] = struct{}{}
		}
	}
	return groupCollectionView(collection, present), nil
}

// CreateGroup adds one logical group without changing any workspace state or bytes.
func (s *Service) CreateGroup(
	ctx context.Context,
	actor Actor,
	input CreateGroupInput,
) (GroupCollectionView, error) {
	if ctx == nil || s == nil {
		return GroupCollectionView{}, fmt.Errorf("create configuration group: service is unavailable")
	}
	if err := validateActor(actor); err != nil {
		return GroupCollectionView{}, fmt.Errorf("create configuration group: %w", err)
	}
	name, normalized, err := NormalizeGroupName(input.Name)
	if err != nil {
		return GroupCollectionView{}, fmt.Errorf("create configuration group: %w", err)
	}
	members, err := s.validateGroupMembers(input.Members)
	if err != nil {
		return GroupCollectionView{}, fmt.Errorf("create configuration group: %w", err)
	}
	current, err := s.currentGroupCollection(ctx, "create")
	if err != nil {
		return GroupCollectionView{}, err
	}
	if err := requireGroupETag(input.IfMatch, current); err != nil {
		return GroupCollectionView{}, err
	}
	if normalizedGroupNameExists(current.Groups, normalized, "") {
		return GroupCollectionView{}, fmt.Errorf("create configuration group: name: %w", ErrConflict)
	}
	id, err := NewGroupID(s.random)
	if err != nil {
		return GroupCollectionView{}, fmt.Errorf("create configuration group: %w", err)
	}
	if groupIndex(current.Groups, id) >= 0 {
		return GroupCollectionView{}, fmt.Errorf("create configuration group: id: %w", ErrConflict)
	}
	now := s.clock.Now().UTC()
	group := Group{
		ID: id, Name: name, NormalizedName: normalized, SortOrder: input.SortOrder,
		Members: members, CreatedBy: actor.UserID, CreatedAt: now, UpdatedAt: now,
	}
	groups := append(slices.Clone(current.Groups), group)
	if err := validateGroupCollectionLimits(groups, s.limits); err != nil {
		return GroupCollectionView{}, fmt.Errorf("create configuration group: %w", err)
	}
	return s.changeGroupCollection(ctx, actor, current, groups, group, "create", now)
}

// ReplaceGroup replaces the display metadata and member set of one exact logical group.
func (s *Service) ReplaceGroup(
	ctx context.Context,
	actor Actor,
	id GroupID,
	input ReplaceGroupInput,
) (GroupCollectionView, error) {
	if ctx == nil || s == nil {
		return GroupCollectionView{}, fmt.Errorf("replace configuration group: service is unavailable")
	}
	parsedID, err := ParseGroupID(string(id))
	if err != nil || parsedID != id {
		return GroupCollectionView{}, fmt.Errorf("replace configuration group: %w", ErrIdentifierInvalid)
	}
	if err := validateActor(actor); err != nil {
		return GroupCollectionView{}, fmt.Errorf("replace configuration group: %w", err)
	}
	name, normalized, err := NormalizeGroupName(input.Name)
	if err != nil {
		return GroupCollectionView{}, fmt.Errorf("replace configuration group: %w", err)
	}
	members, err := s.validateGroupMembers(input.Members)
	if err != nil {
		return GroupCollectionView{}, fmt.Errorf("replace configuration group: %w", err)
	}
	current, err := s.currentGroupCollection(ctx, "replace")
	if err != nil {
		return GroupCollectionView{}, err
	}
	if err := requireGroupETag(input.IfMatch, current); err != nil {
		return GroupCollectionView{}, err
	}
	index := groupIndex(current.Groups, id)
	if index < 0 {
		return GroupCollectionView{}, fmt.Errorf("replace configuration group: %w", fs.ErrNotExist)
	}
	if normalizedGroupNameExists(current.Groups, normalized, id) {
		return GroupCollectionView{}, fmt.Errorf("replace configuration group: name: %w", ErrConflict)
	}
	now := s.clock.Now().UTC()
	group := current.Groups[index]
	group.Name = name
	group.NormalizedName = normalized
	group.SortOrder = input.SortOrder
	group.Members = members
	group.UpdatedAt = now
	groups := slices.Clone(current.Groups)
	groups[index] = group
	if err := validateGroupCollectionLimits(groups, s.limits); err != nil {
		return GroupCollectionView{}, fmt.Errorf("replace configuration group: %w", err)
	}
	return s.changeGroupCollection(ctx, actor, current, groups, group, "replace", now)
}

// DeleteGroup removes only logical metadata after an exact display-name confirmation.
func (s *Service) DeleteGroup(
	ctx context.Context,
	actor Actor,
	id GroupID,
	input DeleteGroupInput,
) (GroupCollectionView, error) {
	if ctx == nil || s == nil {
		return GroupCollectionView{}, fmt.Errorf("delete configuration group: service is unavailable")
	}
	parsedID, err := ParseGroupID(string(id))
	if err != nil || parsedID != id {
		return GroupCollectionView{}, fmt.Errorf("delete configuration group: %w", ErrIdentifierInvalid)
	}
	if err := validateActor(actor); err != nil {
		return GroupCollectionView{}, fmt.Errorf("delete configuration group: %w", err)
	}
	current, err := s.currentGroupCollection(ctx, "delete")
	if err != nil {
		return GroupCollectionView{}, err
	}
	if err := requireGroupETag(input.IfMatch, current); err != nil {
		return GroupCollectionView{}, err
	}
	index := groupIndex(current.Groups, id)
	if index < 0 {
		return GroupCollectionView{}, fmt.Errorf("delete configuration group: %w", fs.ErrNotExist)
	}
	group := current.Groups[index]
	if input.ConfirmName != group.Name {
		return GroupCollectionView{}, fmt.Errorf("delete configuration group: confirm name: %w", ErrConflict)
	}
	groups := slices.Clone(current.Groups)
	groups = append(groups[:index], groups[index+1:]...)
	return s.changeGroupCollection(ctx, actor, current, groups, group, "delete", s.clock.Now().UTC())
}

func (c GroupCollection) digest() Digest {
	collection := canonicalGroupCollection(c)
	var payload bytes.Buffer
	writeGroupField(&payload, []byte(groupCollectionSchema))
	writeGroupUint64Field(&payload, collection.Revision)
	writeGroupUint64Field(&payload, uint64(len(collection.Groups)))
	for _, group := range collection.Groups {
		var record bytes.Buffer
		writeGroupField(&record, []byte(group.ID))
		writeGroupField(&record, []byte(group.Name))
		writeGroupField(&record, []byte(group.NormalizedName))
		writeGroupField(&record, []byte(strconv.Itoa(group.SortOrder)))
		writeGroupField(&record, []byte(strconv.FormatInt(group.CreatedBy, 10)))
		writeGroupField(&record, []byte(group.CreatedAt.UTC().Format(time.RFC3339Nano)))
		writeGroupField(&record, []byte(group.UpdatedAt.UTC().Format(time.RFC3339Nano)))
		writeGroupUint64Field(&record, uint64(len(group.Members)))
		for _, member := range group.Members {
			writeGroupField(&record, []byte(member))
		}
		writeGroupField(&payload, record.Bytes())
	}
	return Digest(sha256.Sum256(payload.Bytes()))
}

func (s *Service) currentGroupCollection(ctx context.Context, action string) (GroupCollection, error) {
	collection, err := s.groups.GroupCollection(ctx)
	if err != nil {
		return GroupCollection{}, fmt.Errorf("%s configuration group: read collection: %w", action, err)
	}
	if collection.Revision == 0 || collection.Revision == math.MaxUint64 {
		return GroupCollection{}, fmt.Errorf("%s configuration group: collection revision: %w", action, ErrConflict)
	}
	return canonicalGroupCollection(collection), nil
}

func (s *Service) changeGroupCollection(
	ctx context.Context,
	actor Actor,
	current GroupCollection,
	groups []Group,
	target Group,
	action string,
	occurredAt time.Time,
) (GroupCollectionView, error) {
	operationID, err := s.newOperationID()
	if err != nil {
		return GroupCollectionView{}, fmt.Errorf("%s configuration group: %w", action, err)
	}
	next := canonicalGroupCollection(GroupCollection{Revision: current.Revision + 1, Groups: groups})
	beforeDigest := current.digest()
	afterDigest := next.digest()
	operation := OperationRecord{
		ID: operationID, ObjectType: groupCollectionObjectType, ObjectID: groupCollectionObjectID,
		Action: "config.groups." + action, BeforeDigest: &beforeDigest, AfterDigest: &afterDigest,
		Result: operationResultSuccess, RequestID: actor.RequestID, OccurredAt: occurredAt,
	}
	details, err := json.Marshal(struct {
		GroupID     GroupID `json:"group_id"`
		Name        string  `json:"name"`
		MemberCount int     `json:"member_count"`
	}{GroupID: target.ID, Name: target.Name, MemberCount: len(target.Members)})
	if err != nil {
		return GroupCollectionView{}, fmt.Errorf("%s configuration group: build audit: %w", action, err)
	}
	changed, err := s.groups.ChangeGroupCollection(ctx, GroupChange{
		ExpectedRevision: current.Revision,
		Groups:           next.Groups,
		Operation:        operation,
		Audit: AuditEvent{
			OperationID: operation.ID, OccurredAt: operation.OccurredAt, ActorUserID: actor.UserID,
			Action: operation.Action, ObjectType: operation.ObjectType, ObjectID: operation.ObjectID,
			Result: operation.Result, RequestID: operation.RequestID, DetailsJSON: string(details),
		},
	})
	if errors.Is(err, ErrConflict) {
		latest, readErr := s.groups.GroupCollection(ctx)
		if readErr == nil {
			return GroupCollectionView{}, &ConflictError{CurrentETag: latest.ETag()}
		}
		return GroupCollectionView{}, errors.Join(
			fmt.Errorf("%s configuration group: persist collection: %w", action, err),
			fmt.Errorf("reread group collection after conflict: %w", readErr),
		)
	}
	if err != nil {
		return GroupCollectionView{}, fmt.Errorf("%s configuration group: persist collection: %w", action, err)
	}
	return groupCollectionView(canonicalGroupCollection(changed), nil), nil
}

func (s *Service) validateGroupMembers(input []RelativePath) ([]RelativePath, error) {
	if len(input) > s.limits.MaxGroupMembers {
		return nil, ErrLimitExceeded
	}
	members := make([]RelativePath, len(input))
	seen := make(map[RelativePath]struct{}, len(input))
	for index, raw := range input {
		if _, duplicate := seen[raw]; duplicate {
			return nil, ErrConflict
		}
		seen[raw] = struct{}{}
		member, err := ParseRelativePath(string(raw), s.limits)
		if err != nil || member != raw {
			return nil, errors.Join(ErrPathInvalid, err)
		}
		members[index] = member
	}
	slices.Sort(members)
	return members, nil
}

func validateGroupCollectionLimits(groups []Group, limits Limits) error {
	if len(groups) > limits.MaxGroups {
		return ErrLimitExceeded
	}
	totalMembers := 0
	for _, group := range groups {
		if len(group.Members) > limits.MaxGroupMembers || len(group.Members) > limits.MaxTotalGroupMembers-totalMembers {
			return ErrLimitExceeded
		}
		totalMembers += len(group.Members)
	}
	return nil
}

func requireGroupETag(ifMatch string, collection GroupCollection) error {
	current := collection.ETag()
	digest, err := ParseStrongETag(ifMatch, groupETagPrefix)
	want := collection.digest()
	if err != nil || subtle.ConstantTimeCompare(digest[:], want[:]) != 1 ||
		subtle.ConstantTimeCompare([]byte(ifMatch), []byte(current)) != 1 {
		return &ConflictError{CurrentETag: current}
	}
	return nil
}

func groupCollectionView(collection GroupCollection, present map[RelativePath]struct{}) GroupCollectionView {
	view := GroupCollectionView{Groups: make([]GroupView, len(collection.Groups)), ETag: collection.ETag()}
	for index, group := range collection.Groups {
		group.Members = slices.Clone(group.Members)
		view.Groups[index].Group = group
		if present == nil {
			continue
		}
		for _, member := range group.Members {
			if _, exists := present[member]; !exists {
				view.Groups[index].Missing = append(view.Groups[index].Missing, member)
			}
		}
	}
	return view
}

func normalizedGroupNameExists(groups []Group, normalized string, except GroupID) bool {
	for _, group := range groups {
		if group.ID != except && group.NormalizedName == normalized {
			return true
		}
	}
	return false
}

func groupIndex(groups []Group, id GroupID) int {
	for index, group := range groups {
		if group.ID == id {
			return index
		}
	}
	return -1
}

func canonicalGroupCollection(collection GroupCollection) GroupCollection {
	canonical := GroupCollection{Revision: collection.Revision, Groups: make([]Group, len(collection.Groups))}
	for index, group := range collection.Groups {
		group.Members = slices.Clone(group.Members)
		slices.Sort(group.Members)
		canonical.Groups[index] = group
	}
	slices.SortFunc(canonical.Groups, compareGroups)
	return canonical
}

func compareGroups(left, right Group) int {
	if left.SortOrder < right.SortOrder {
		return -1
	}
	if left.SortOrder > right.SortOrder {
		return 1
	}
	if left.ID < right.ID {
		return -1
	}
	if left.ID > right.ID {
		return 1
	}
	return 0
}

func writeGroupField(payload *bytes.Buffer, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	payload.Write(length[:])
	payload.Write(value)
}

func writeGroupUint64Field(payload *bytes.Buffer, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	writeGroupField(payload, encoded[:])
}
