/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestGroupCollectionUsesStableOrderAndReplaceAllMembers(t *testing.T) {
	database := openRepositoryDatabase(t)
	initial, err := database.GroupCollection(context.Background())
	if err != nil {
		t.Fatalf("GroupCollection() error = %v", err)
	}
	if initial.Revision != 1 || len(initial.Groups) != 0 {
		t.Fatalf("initial GroupCollection() = %#v, want revision 1 and no groups", initial)
	}

	groups := []config.Group{
		testGroup(2, "Beta", 1, "beta/b.conf", "beta/a.conf"),
		testGroup(1, "Alpha", 1, "alpha/site.conf"),
	}
	change := testGroupChange(1, groups, "config.groups.replace", testTime(3))
	created, err := database.ChangeGroupCollection(context.Background(), change)
	if err != nil {
		t.Fatalf("ChangeGroupCollection() error = %v", err)
	}
	if created.Revision != 2 || len(created.Groups) != 2 {
		t.Fatalf("changed GroupCollection() = %#v", created)
	}
	if created.Groups[0].ID != groups[1].ID || created.Groups[1].ID != groups[0].ID {
		t.Fatalf("group order = %v, want sort order then ID", []config.GroupID{created.Groups[0].ID, created.Groups[1].ID})
	}
	if !reflect.DeepEqual(created.Groups[1].Members, groups[0].Members) {
		t.Fatalf("member order = %v, want %v", created.Groups[1].Members, groups[0].Members)
	}
	var collectionUpdatedAt string
	if err := database.sql.QueryRowContext(
		context.Background(),
		"SELECT updated_at FROM config_group_collection WHERE singleton = 1",
	).Scan(&collectionUpdatedAt); err != nil {
		t.Fatalf("read group collection timestamp: %v", err)
	}
	if collectionUpdatedAt != formatTime(change.Operation.OccurredAt) {
		t.Fatalf("group collection updated_at = %q, want %q", collectionUpdatedAt, formatTime(change.Operation.OccurredAt))
	}

	replacement := testGroup(2, "Beta renamed", 0, "beta/new.conf")
	change = testGroupChange(2, []config.Group{replacement}, "config.groups.replace-again", testTime(4))
	replaced, err := database.ChangeGroupCollection(context.Background(), change)
	if err != nil {
		t.Fatalf("second ChangeGroupCollection() error = %v", err)
	}
	if replaced.Revision != 3 || len(replaced.Groups) != 1 || replaced.Groups[0].ID != replacement.ID ||
		!reflect.DeepEqual(replaced.Groups[0].Members, replacement.Members) {
		t.Fatalf("replaced GroupCollection() = %#v", replaced)
	}
	var removedCount int
	if err := database.sql.QueryRowContext(
		context.Background(),
		"SELECT COUNT(*) FROM config_groups WHERE id = ?",
		groups[1].ID,
	).Scan(&removedCount); err != nil {
		t.Fatalf("count removed group: %v", err)
	}
	if removedCount != 0 {
		t.Fatalf("removed group count = %d, want 0", removedCount)
	}
	assertMutationCounts(t, database, 0, 2, 2)
}

func TestGroupCollectionRevisionConflictRollsBack(t *testing.T) {
	database := openRepositoryDatabase(t)
	original := []config.Group{testGroup(1, "Alpha", 0, "alpha/site.conf")}
	if _, err := database.ChangeGroupCollection(
		context.Background(),
		testGroupChange(1, original, "config.groups.create", testTime(2)),
	); err != nil {
		t.Fatalf("ChangeGroupCollection() error = %v", err)
	}

	err := func() error {
		_, err := database.ChangeGroupCollection(
			context.Background(),
			testGroupChange(1, []config.Group{testGroup(2, "Beta", 0, "beta/site.conf")}, "config.groups.stale", testTime(3)),
		)
		return err
	}()
	if !errors.Is(err, config.ErrConflict) {
		t.Fatalf("stale ChangeGroupCollection() error = %v, want ErrConflict", err)
	}
	got, readErr := database.GroupCollection(context.Background())
	if readErr != nil {
		t.Fatalf("GroupCollection() error = %v", readErr)
	}
	if got.Revision != 2 || len(got.Groups) != 1 || got.Groups[0].ID != original[0].ID {
		t.Fatalf("group collection changed after conflict: %#v", got)
	}
	assertMutationCounts(t, database, 0, 1, 1)
}

func TestGroupCollectionRejectsDuplicateNamesAndMembers(t *testing.T) {
	tests := []struct {
		name   string
		groups []config.Group
	}{
		{
			name: "normalized name",
			groups: []config.Group{
				testGroup(1, "Production Sites", 0, "sites/a.conf"),
				testGroup(2, "production sites", 1, "sites/b.conf"),
			},
		},
		{
			name: "member",
			groups: []config.Group{
				testGroup(1, "Sites", 0, "sites/a.conf", "sites/a.conf"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database := openRepositoryDatabase(t)
			_, err := database.ChangeGroupCollection(
				context.Background(),
				testGroupChange(1, test.groups, "config.groups.duplicate-"+test.name, testTime(2)),
			)
			if !errors.Is(err, config.ErrConflict) {
				t.Fatalf("ChangeGroupCollection() error = %v, want ErrConflict", err)
			}
			got, readErr := database.GroupCollection(context.Background())
			if readErr != nil {
				t.Fatalf("GroupCollection() error = %v", readErr)
			}
			if got.Revision != 1 || len(got.Groups) != 0 {
				t.Fatalf("group collection changed after rejection: %#v", got)
			}
			assertMutationCounts(t, database, 0, 0, 0)
		})
	}
}

func TestGroupCollectionEnforcesFixedLimits(t *testing.T) {
	limits := config.DefaultLimits()
	tests := []struct {
		name   string
		groups func() []config.Group
	}{
		{
			name: "groups",
			groups: func() []config.Group {
				groups := make([]config.Group, limits.MaxGroups+1)
				for index := range groups {
					groups[index] = testGroup(index+1, fmt.Sprintf("Group %d", index), index)
				}
				return groups
			},
		},
		{
			name: "members per group",
			groups: func() []config.Group {
				members := make([]string, limits.MaxGroupMembers+1)
				for index := range members {
					members[index] = fmt.Sprintf("sites/%04d.conf", index)
				}
				return []config.Group{testGroup(1, "Sites", 0, members...)}
			},
		},
		{
			name: "total members",
			groups: func() []config.Group {
				groups := make([]config.Group, 5)
				remaining := limits.MaxTotalGroupMembers + 1
				for groupIndex := range groups {
					count := min(remaining, limits.MaxGroupMembers)
					members := make([]string, count)
					for memberIndex := range members {
						members[memberIndex] = fmt.Sprintf("group-%d/%04d.conf", groupIndex, memberIndex)
					}
					groups[groupIndex] = testGroup(groupIndex+1, fmt.Sprintf("Group %d", groupIndex), groupIndex, members...)
					remaining -= count
				}
				return groups
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database := openRepositoryDatabase(t)
			_, err := database.ChangeGroupCollection(
				context.Background(),
				testGroupChange(1, test.groups(), "config.groups.limit-"+test.name, testTime(2)),
			)
			if !errors.Is(err, config.ErrLimitExceeded) {
				t.Fatalf("ChangeGroupCollection() error = %v, want ErrLimitExceeded", err)
			}
			assertMutationCounts(t, database, 0, 0, 0)
		})
	}
}

func TestGroupCollectionRejectsUnsafeMemberPath(t *testing.T) {
	database := openRepositoryDatabase(t)
	group := testGroup(1, "Sites", 0, "/etc/nginx/nginx.conf")
	_, err := database.ChangeGroupCollection(
		context.Background(),
		testGroupChange(1, []config.Group{group}, "config.groups.unsafe-path", testTime(2)),
	)
	if !errors.Is(err, config.ErrPathInvalid) {
		t.Fatalf("ChangeGroupCollection() error = %v, want ErrPathInvalid", err)
	}
	assertMutationCounts(t, database, 0, 0, 0)
}

func TestGroupCollectionAndAuditAreAtomic(t *testing.T) {
	database := openRepositoryDatabase(t)
	original := []config.Group{testGroup(1, "Alpha", 0, "alpha/site.conf")}
	if _, err := database.ChangeGroupCollection(
		context.Background(),
		testGroupChange(1, original, "config.groups.create", testTime(2)),
	); err != nil {
		t.Fatalf("ChangeGroupCollection() error = %v", err)
	}
	breakAuditInsert(t, database)

	replacement := []config.Group{testGroup(2, "Beta", 0, "beta/site.conf")}
	_, err := database.ChangeGroupCollection(
		context.Background(),
		testGroupChange(2, replacement, "config.groups.replace", testTime(3)),
	)
	if err == nil {
		t.Fatal("ChangeGroupCollection() error = nil")
	}
	got, readErr := database.GroupCollection(context.Background())
	if readErr != nil {
		t.Fatalf("GroupCollection() error = %v", readErr)
	}
	if got.Revision != 2 || len(got.Groups) != 1 || got.Groups[0].ID != original[0].ID ||
		!reflect.DeepEqual(got.Groups[0].Members, original[0].Members) {
		t.Fatalf("group collection changed after audit failure: %#v", got)
	}
	assertMutationCounts(t, database, 0, 1, 1)
}

func testGroup(number int, name string, sortOrder int, members ...string) config.Group {
	display, normalized, err := config.NormalizeGroupName(name)
	if err != nil {
		panic(err)
	}
	paths := make([]config.RelativePath, len(members))
	for index, member := range members {
		paths[index] = config.RelativePath(member)
	}
	return config.Group{
		ID:             config.GroupID(fmt.Sprintf("%032x", number)),
		Name:           display,
		NormalizedName: normalized,
		SortOrder:      sortOrder,
		Members:        paths,
		CreatedBy:      1,
		CreatedAt:      testTime(1),
		UpdatedAt:      testTime(2),
	}
}

func testGroupChange(expectedRevision uint64, groups []config.Group, action string, occurredAt time.Time) config.GroupChange {
	operation := testOperation(action, "group_collection", "global", occurredAt)
	return config.GroupChange{
		ExpectedRevision: expectedRevision,
		Groups:           groups,
		Operation:        operation,
		Audit:            testAudit(operation, `{"group_count":1}`),
	}
}
