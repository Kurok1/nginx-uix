/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestGroupCollectionETagUsesCanonicalGroupAndMemberOrder(t *testing.T) {
	createdAt := time.Date(2026, time.July, 16, 3, 0, 0, 0, time.UTC)
	alpha := Group{
		ID: "00000000000000000000000000000001", Name: "Alpha", NormalizedName: "alpha", SortOrder: 10,
		Members: []RelativePath{"z.conf", "a.conf"}, CreatedBy: 7, CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	beta := Group{
		ID: "00000000000000000000000000000002", Name: "Beta", NormalizedName: "beta", SortOrder: 5,
		Members: []RelativePath{"conf.d/site.conf"}, CreatedBy: 7, CreatedAt: createdAt, UpdatedAt: createdAt,
	}

	left := GroupCollection{Revision: 3, Groups: []Group{alpha, beta}}
	alpha.Members = []RelativePath{"a.conf", "z.conf"}
	right := GroupCollection{Revision: 3, Groups: []Group{beta, alpha}}

	if left.ETag() != right.ETag() {
		t.Fatalf("ETag depends on insertion order: %q != %q", left.ETag(), right.ETag())
	}
	if _, err := ParseStrongETag(left.ETag(), groupETagPrefix); err != nil {
		t.Fatalf("ETag is not a strong group tag: %v", err)
	}
}

func TestGroupCollectionETagChangesWithRevisionGroupOrMember(t *testing.T) {
	baseGroup := Group{
		ID: "00000000000000000000000000000001", Name: "Alpha", NormalizedName: "alpha", SortOrder: 10,
		Members: []RelativePath{"a.conf"}, CreatedBy: 7,
		CreatedAt: time.Date(2026, time.July, 16, 3, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, time.July, 16, 3, 0, 0, 0, time.UTC),
	}
	base := GroupCollection{Revision: 3, Groups: []Group{baseGroup}}
	tests := []struct {
		name       string
		collection GroupCollection
	}{
		{name: "revision", collection: GroupCollection{Revision: 4, Groups: []Group{baseGroup}}},
		{name: "name", collection: GroupCollection{Revision: 3, Groups: []Group{withGroup(baseGroup, func(group *Group) { group.Name = "Alpha renamed" })}}},
		{name: "sort order", collection: GroupCollection{Revision: 3, Groups: []Group{withGroup(baseGroup, func(group *Group) { group.SortOrder = 11 })}}},
		{name: "member", collection: GroupCollection{Revision: 3, Groups: []Group{withGroup(baseGroup, func(group *Group) { group.Members = []RelativePath{"b.conf"} })}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.collection.ETag() == base.ETag() {
				t.Fatalf("ETag did not change for %s", test.name)
			}
		})
	}
}

func TestGroupListUsesStableOrderAndMarksMissingForSelectedWorkspace(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	alpha := configGroup(1, "Alpha", 10, "z.conf", "nginx.conf", "conf.d/site.conf")
	beta := configGroup(2, "Beta", 5, "conf.d/missing.conf")
	seedGroupCollection(fixture.repository, 4, []Group{alpha, beta})

	view, err := fixture.service.ListGroups(context.Background(), &workspace.ID)
	if err != nil {
		t.Fatalf("ListGroups() error = %v", err)
	}
	if view.ETag != (GroupCollection{Revision: 4, Groups: []Group{alpha, beta}}).ETag() {
		t.Fatalf("ListGroups() ETag = %q", view.ETag)
	}
	if len(view.Groups) != 2 || view.Groups[0].Group.ID != beta.ID || view.Groups[1].Group.ID != alpha.ID {
		t.Fatalf("ListGroups() order = %#v", view.Groups)
	}
	if got, want := view.Groups[1].Group.Members, []RelativePath{"conf.d/site.conf", "nginx.conf", "z.conf"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("alpha members = %v, want %v", got, want)
	}
	if got, want := view.Groups[0].Missing, []RelativePath{"conf.d/missing.conf"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("beta missing = %v, want %v", got, want)
	}
	if got, want := view.Groups[1].Missing, []RelativePath{"z.conf"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("alpha missing = %v, want %v", got, want)
	}

	global, err := fixture.service.ListGroups(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListGroups(global) error = %v", err)
	}
	for _, group := range global.Groups {
		if len(group.Missing) != 0 {
			t.Fatalf("global ListGroups() marked missing members: %#v", global.Groups)
		}
	}
	stored, err := fixture.repository.GroupCollection(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(stored.Groups[0].Members, RelativePath("conf.d/missing.conf")) {
		t.Fatalf("missing member was removed from storage: %#v", stored)
	}
}

func TestGroupMutationDoesNotChangeDraftETagOrWorkspaceBytes(t *testing.T) {
	fixture := newServiceFixture(t)
	workspace := fixture.mustCreate(t)
	beforeFiles := snapshotWorkspaceFiles(t, fixture.path(workspace.ID, ""))
	collection, err := fixture.service.ListGroups(context.Background(), &workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	digestCalls := fixture.production.digestCalls
	snapshotCalls := fixture.production.snapshotCalls

	created, err := fixture.service.CreateGroup(
		context.Background(),
		Actor{UserID: 7, RequestID: "req-group"},
		CreateGroupInput{
			Name: "edge routes", SortOrder: 10, Members: []RelativePath{"conf.d/missing.conf"},
			IfMatch: collection.ETag,
		},
	)
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	if created.ETag == collection.ETag || fixture.production.digestCalls != digestCalls ||
		fixture.production.snapshotCalls != snapshotCalls {
		t.Fatalf("CreateGroup() used workspace/production identity: %#v", created)
	}
	after, err := fixture.service.Get(context.Background(), workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ETag() != workspace.ETag() || after.Revision != workspace.Revision {
		t.Fatalf("draft identity changed: %#v -> %#v", workspace, after)
	}
	if afterFiles := snapshotWorkspaceFiles(t, fixture.path(workspace.ID, "")); !reflect.DeepEqual(afterFiles, beforeFiles) {
		t.Fatalf("workspace files changed: before=%v after=%v", beforeFiles, afterFiles)
	}
	assertGroupAudit(t, fixture.repository, "config.groups.create", "req-group")
}

func TestGroupMutationsCreateReplaceDeleteWithCollectionCASAndAudit(t *testing.T) {
	fixture := newServiceFixture(t)
	initial, err := fixture.service.ListGroups(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	created, err := fixture.service.CreateGroup(context.Background(), Actor{UserID: 7, RequestID: "req-group-create"}, CreateGroupInput{
		Name: " Edge Routes ", SortOrder: 10, Members: []RelativePath{"z.conf", "a.conf"}, IfMatch: initial.ETag,
	})
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	if len(created.Groups) != 1 || created.Groups[0].Group.Name != "Edge Routes" ||
		created.Groups[0].Group.NormalizedName != "edge routes" ||
		!reflect.DeepEqual(created.Groups[0].Group.Members, []RelativePath{"a.conf", "z.conf"}) ||
		created.ETag == initial.ETag {
		t.Fatalf("CreateGroup() = %#v", created)
	}
	group := created.Groups[0].Group
	fixture.clock.now = fixture.clock.now.Add(time.Minute)
	replaced, err := fixture.service.ReplaceGroup(
		context.Background(), Actor{UserID: 7, RequestID: "req-group-replace"}, group.ID,
		ReplaceGroupInput{Name: "Core Routes", SortOrder: -5, Members: []RelativePath{"nginx.conf"}, IfMatch: created.ETag},
	)
	if err != nil {
		t.Fatalf("ReplaceGroup() error = %v", err)
	}
	if len(replaced.Groups) != 1 || replaced.Groups[0].Group.Name != "Core Routes" ||
		replaced.Groups[0].Group.CreatedAt != group.CreatedAt || replaced.Groups[0].Group.CreatedBy != group.CreatedBy ||
		replaced.Groups[0].Group.UpdatedAt != fixture.clock.now || replaced.ETag == created.ETag {
		t.Fatalf("ReplaceGroup() = %#v", replaced)
	}

	_, err = fixture.service.DeleteGroup(
		context.Background(), Actor{UserID: 7, RequestID: "req-group-delete-wrong"}, group.ID,
		DeleteGroupInput{ConfirmName: "core routes", IfMatch: replaced.ETag},
	)
	if !errors.Is(err, ErrConflict) || groupChangeCalls(fixture.repository) != 2 {
		t.Fatalf("DeleteGroup(wrong confirm) error = %v, calls = %d", err, groupChangeCalls(fixture.repository))
	}
	fixture.clock.now = fixture.clock.now.Add(time.Minute)
	deleted, err := fixture.service.DeleteGroup(
		context.Background(), Actor{UserID: 7, RequestID: "req-group-delete"}, group.ID,
		DeleteGroupInput{ConfirmName: "Core Routes", IfMatch: replaced.ETag},
	)
	if err != nil {
		t.Fatalf("DeleteGroup() error = %v", err)
	}
	if len(deleted.Groups) != 0 || deleted.ETag == replaced.ETag {
		t.Fatalf("DeleteGroup() = %#v", deleted)
	}
	assertGroupAudit(t, fixture.repository, "config.groups.create", "req-group-create")
	assertGroupAudit(t, fixture.repository, "config.groups.replace", "req-group-replace")
	assertGroupAudit(t, fixture.repository, "config.groups.delete", "req-group-delete")
}

func TestGroupMutationRequiresExactCurrentStrongCollectionETag(t *testing.T) {
	wrong := GroupETag(Digest{1})
	tests := []struct {
		name    string
		ifMatch func(string) string
	}{
		{name: "missing", ifMatch: func(string) string { return "" }},
		{name: "weak", ifMatch: func(current string) string { return "W/" + current }},
		{name: "multiple", ifMatch: func(current string) string { return current + ", " + current }},
		{name: "wrong", ifMatch: func(string) string { return wrong }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			current, err := fixture.service.ListGroups(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = fixture.service.CreateGroup(context.Background(), Actor{UserID: 7, RequestID: "req-group-etag"}, CreateGroupInput{
				Name: "Edge", Members: []RelativePath{"nginx.conf"}, IfMatch: test.ifMatch(current.ETag),
			})
			var conflict *ConflictError
			if !errors.As(err, &conflict) || conflict.CurrentETag != current.ETag {
				t.Fatalf("CreateGroup() error = %#v, want current %q", err, current.ETag)
			}
			if groupChangeCalls(fixture.repository) != 0 {
				t.Fatalf("repository writes = %d, want 0", groupChangeCalls(fixture.repository))
			}
		})
	}
}

func TestGroupMutationRejectsDuplicateRawMembersBeforeRepositoryWrite(t *testing.T) {
	fixture := newServiceFixture(t)
	current, err := fixture.service.ListGroups(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.CreateGroup(context.Background(), Actor{UserID: 7, RequestID: "req-group-duplicate"}, CreateGroupInput{
		Name: "Edge", Members: []RelativePath{"conf.d/site.conf", "conf.d/site.conf"}, IfMatch: current.ETag,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateGroup() error = %v, want ErrConflict", err)
	}
	if groupChangeCalls(fixture.repository) != 0 {
		t.Fatalf("repository writes = %d, want 0", groupChangeCalls(fixture.repository))
	}
}

func TestGroupMutationEnforcesExactCollectionAndMemberLimits(t *testing.T) {
	t.Run("groups", func(t *testing.T) {
		fixture := newServiceFixture(t)
		groups := make([]Group, 127)
		for index := range groups {
			groups[index] = configGroup(index+1, fmt.Sprintf("Group %d", index+1), index)
		}
		seedGroupCollection(fixture.repository, 1, groups)
		current, err := fixture.service.ListGroups(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		atLimit, err := fixture.service.CreateGroup(context.Background(), Actor{UserID: 7, RequestID: "req-group-128"}, CreateGroupInput{
			Name: "Group 128", IfMatch: current.ETag,
		})
		if err != nil || len(atLimit.Groups) != 128 {
			t.Fatalf("CreateGroup(128) = %#v, %v", atLimit, err)
		}
		_, err = fixture.service.CreateGroup(context.Background(), Actor{UserID: 7, RequestID: "req-group-129"}, CreateGroupInput{
			Name: "Group 129", IfMatch: atLimit.ETag,
		})
		if !errors.Is(err, ErrLimitExceeded) || groupChangeCalls(fixture.repository) != 1 {
			t.Fatalf("CreateGroup(129) error = %v, calls = %d", err, groupChangeCalls(fixture.repository))
		}
	})

	t.Run("members per group", func(t *testing.T) {
		fixture := newServiceFixture(t)
		current, err := fixture.service.ListGroups(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		atLimit, err := fixture.service.CreateGroup(context.Background(), Actor{UserID: 7, RequestID: "req-members-1024"}, CreateGroupInput{
			Name: "At limit", Members: groupMemberPaths("at", 1024), IfMatch: current.ETag,
		})
		if err != nil || len(atLimit.Groups[0].Group.Members) != 1024 {
			t.Fatalf("CreateGroup(1024) error = %v", err)
		}

		other := newServiceFixture(t)
		otherCurrent, err := other.service.ListGroups(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = other.service.CreateGroup(context.Background(), Actor{UserID: 7, RequestID: "req-members-1025"}, CreateGroupInput{
			Name: "Over limit", Members: groupMemberPaths("over", 1025), IfMatch: otherCurrent.ETag,
		})
		if !errors.Is(err, ErrLimitExceeded) || groupChangeCalls(other.repository) != 0 {
			t.Fatalf("CreateGroup(1025) error = %v, calls = %d", err, groupChangeCalls(other.repository))
		}
	})

	t.Run("total members", func(t *testing.T) {
		fixture := newServiceFixture(t)
		groups := make([]Group, 3)
		for index := range groups {
			groups[index] = configGroup(index+1, fmt.Sprintf("Seed %d", index), index)
			groups[index].Members = groupMemberPaths(fmt.Sprintf("seed-%d", index), 1024)
		}
		seedGroupCollection(fixture.repository, 1, groups)
		current, err := fixture.service.ListGroups(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		atLimit, err := fixture.service.CreateGroup(context.Background(), Actor{UserID: 7, RequestID: "req-total-4096"}, CreateGroupInput{
			Name: "Fourth", Members: groupMemberPaths("fourth", 1024), IfMatch: current.ETag,
		})
		if err != nil {
			t.Fatalf("CreateGroup(total 4096) error = %v", err)
		}
		_, err = fixture.service.CreateGroup(context.Background(), Actor{UserID: 7, RequestID: "req-total-4097"}, CreateGroupInput{
			Name: "Fifth", Members: []RelativePath{"fifth/0000.conf"}, IfMatch: atLimit.ETag,
		})
		if !errors.Is(err, ErrLimitExceeded) || groupChangeCalls(fixture.repository) != 1 {
			t.Fatalf("CreateGroup(total 4097) error = %v, calls = %d", err, groupChangeCalls(fixture.repository))
		}
	})
}

func TestGroupMutationValidatesDisplayNamesPathsAndNormalizedUniqueness(t *testing.T) {
	tests := []struct {
		name    string
		group   CreateGroupInput
		wantErr error
	}{
		{name: "empty name", group: CreateGroupInput{Name: " \t "}, wantErr: ErrDisplayNameInvalid},
		{name: "control name", group: CreateGroupInput{Name: "edge\nroute"}, wantErr: ErrDisplayNameInvalid},
		{name: "name over 128 runes", group: CreateGroupInput{Name: strings.Repeat("界", 129)}, wantErr: ErrDisplayNameInvalid},
		{name: "unsafe member", group: CreateGroupInput{Name: "Edge", Members: []RelativePath{"../nginx.conf"}}, wantErr: ErrPathInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			current, err := fixture.service.ListGroups(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			test.group.IfMatch = current.ETag
			_, err = fixture.service.CreateGroup(context.Background(), Actor{UserID: 7, RequestID: "req-group-invalid"}, test.group)
			if !errors.Is(err, test.wantErr) || groupChangeCalls(fixture.repository) != 0 {
				t.Fatalf("CreateGroup() error = %v, calls = %d", err, groupChangeCalls(fixture.repository))
			}
		})
	}

	fixture := newServiceFixture(t)
	current, err := fixture.service.ListGroups(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	created, err := fixture.service.CreateGroup(context.Background(), Actor{UserID: 7, RequestID: "req-group-name-boundary"}, CreateGroupInput{
		Name: strings.Repeat("界", 128), IfMatch: current.ETag,
	})
	if err != nil {
		t.Fatalf("CreateGroup(128-rune name) error = %v", err)
	}
	_, err = fixture.service.CreateGroup(context.Background(), Actor{UserID: 7, RequestID: "req-group-case"}, CreateGroupInput{
		Name: strings.ToLower(created.Groups[0].Group.Name), IfMatch: created.ETag,
	})
	if !errors.Is(err, ErrConflict) || groupChangeCalls(fixture.repository) != 1 {
		t.Fatalf("CreateGroup(case duplicate) error = %v, calls = %d", err, groupChangeCalls(fixture.repository))
	}
}

func TestGroupReplaceRejectsAnotherNormalizedNameAndUnknownID(t *testing.T) {
	fixture := newServiceFixture(t)
	alpha := configGroup(1, "Alpha", 0)
	beta := configGroup(2, "Beta", 1)
	seedGroupCollection(fixture.repository, 3, []Group{alpha, beta})
	current, err := fixture.service.ListGroups(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.ReplaceGroup(context.Background(), Actor{UserID: 7, RequestID: "req-group-duplicate-name"}, beta.ID, ReplaceGroupInput{
		Name: "ALPHA", IfMatch: current.ETag,
	})
	if !errors.Is(err, ErrConflict) || groupChangeCalls(fixture.repository) != 0 {
		t.Fatalf("ReplaceGroup(duplicate name) error = %v", err)
	}
	_, err = fixture.service.ReplaceGroup(context.Background(), Actor{UserID: 7, RequestID: "req-group-missing"}, GroupID("ffffffffffffffffffffffffffffffff"), ReplaceGroupInput{
		Name: "Missing", IfMatch: current.ETag,
	})
	if !errors.Is(err, fs.ErrNotExist) || groupChangeCalls(fixture.repository) != 0 {
		t.Fatalf("ReplaceGroup(missing) error = %v", err)
	}
}

func withGroup(group Group, modify func(*Group)) Group {
	modify(&group)
	return group
}

func configGroup(number int, name string, sortOrder int, members ...RelativePath) Group {
	display, normalized, err := NormalizeGroupName(name)
	if err != nil {
		panic(err)
	}
	createdAt := time.Date(2026, time.July, 16, 1, 0, 0, 0, time.UTC)
	return Group{
		ID: GroupID(fmt.Sprintf("%032x", number)), Name: display, NormalizedName: normalized, SortOrder: sortOrder,
		Members: slices.Clone(members), CreatedBy: 7, CreatedAt: createdAt, UpdatedAt: createdAt,
	}
}

func seedGroupCollection(repository *memoryWorkspaceRepository, revision uint64, groups []Group) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.groups = canonicalGroupCollection(GroupCollection{Revision: revision, Groups: groups})
}

func groupChangeCalls(repository *memoryWorkspaceRepository) int {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.groupChangeCalls
}

func groupMemberPaths(prefix string, count int) []RelativePath {
	members := make([]RelativePath, count)
	for index := range members {
		members[index] = RelativePath(fmt.Sprintf("%s/%04d.conf", prefix, index))
	}
	return members
}

func snapshotWorkspaceFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = content
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot workspace files: %v", err)
	}
	return files
}

func assertGroupAudit(t *testing.T, repository *memoryWorkspaceRepository, action, requestID string) {
	t.Helper()
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for _, operation := range repository.operations {
		if operation.Action != action || operation.RequestID != requestID {
			continue
		}
		if operation.ObjectType != "group_collection" || operation.ObjectID != "global" ||
			operation.BeforeDigest == nil || operation.AfterDigest == nil || *operation.BeforeDigest == *operation.AfterDigest {
			t.Fatalf("group operation = %#v", operation)
		}
		for _, audit := range repository.audits {
			if audit.OperationID != operation.ID {
				continue
			}
			var details map[string]any
			if audit.Action != action || audit.RequestID != requestID || audit.ActorUserID != 7 ||
				json.Unmarshal([]byte(audit.DetailsJSON), &details) != nil || details["name"] == nil {
				t.Fatalf("group audit = %#v", audit)
			}
			return
		}
		t.Fatalf("audit for operation %q not found", operation.ID)
	}
	t.Fatalf("group operation %q/%q not found", action, requestID)
}
