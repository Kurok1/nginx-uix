/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/kuroky/nginx-uix/internal/config"
)

var _ config.GroupRepository = (*DB)(nil)

// GroupCollection returns the revisioned global logical group collection.
func (d *DB) GroupCollection(ctx context.Context) (config.GroupCollection, error) {
	collection, err := readGroupCollection(ctx, d.sql)
	if err != nil {
		return config.GroupCollection{}, fmt.Errorf("read group collection: %w", err)
	}
	return collection, nil
}

// ChangeGroupCollection atomically replaces the global logical group collection.
func (d *DB) ChangeGroupCollection(ctx context.Context, change config.GroupChange) (config.GroupCollection, error) {
	groups, err := validateGroupChange(change)
	if err != nil {
		return config.GroupCollection{}, err
	}

	var changed config.GroupCollection
	err = d.withImmediateTransaction(ctx, func(connection *sql.Conn) error {
		result, err := connection.ExecContext(
			ctx,
			`UPDATE config_group_collection
			 SET revision = revision + 1, updated_at = ?
			 WHERE singleton = 1 AND revision = ?`,
			formatTime(change.Operation.OccurredAt),
			change.ExpectedRevision,
		)
		if err != nil {
			return mapConfigConstraint("advance group collection revision", err)
		}
		matched, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read group collection revision count: %w", err)
		}
		if matched == 0 {
			var currentRevision int64
			err := connection.QueryRowContext(
				ctx,
				"SELECT revision FROM config_group_collection WHERE singleton = 1",
			).Scan(&currentRevision)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("read group collection revision: %w", sql.ErrNoRows)
			}
			if err != nil {
				return fmt.Errorf("read group collection revision: %w", err)
			}
			return config.ErrConflict
		}

		if _, err := connection.ExecContext(ctx, "DELETE FROM config_groups"); err != nil {
			return mapConfigConstraint("clear group collection", err)
		}
		for _, group := range groups {
			if _, err := connection.ExecContext(
				ctx,
				`INSERT INTO config_groups(
					id, name, normalized_name, sort_order, created_by, created_at, updated_at
				 ) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				group.ID,
				group.Name,
				group.NormalizedName,
				group.SortOrder,
				group.CreatedBy,
				formatTime(group.CreatedAt),
				formatTime(group.UpdatedAt),
			); err != nil {
				return mapConfigConstraint("insert configuration group", err)
			}
			for ordinal, member := range group.Members {
				if _, err := connection.ExecContext(
					ctx,
					"INSERT INTO config_group_members(group_id, ordinal, path) VALUES (?, ?, ?)",
					group.ID,
					ordinal,
					member,
				); err != nil {
					return mapConfigConstraint("insert configuration group member", err)
				}
			}
		}
		if err := insertOperationAndAudit(ctx, connection, change.Operation, change.Audit); err != nil {
			return err
		}
		changed, err = readGroupCollection(ctx, connection)
		if err != nil {
			return fmt.Errorf("read changed group collection: %w", err)
		}
		return nil
	})
	if err != nil {
		return config.GroupCollection{}, fmt.Errorf("change group collection: %w", err)
	}
	return changed, nil
}

type groupRowsQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readGroupCollection(
	ctx context.Context,
	queryer groupRowsQueryer,
) (collection config.GroupCollection, returnErr error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT
			collection.revision,
			groups.id, groups.name, groups.normalized_name, groups.sort_order,
			groups.created_by, groups.created_at, groups.updated_at,
			members.path
		FROM config_group_collection AS collection
		LEFT JOIN config_groups AS groups ON 1 = 1
		LEFT JOIN config_group_members AS members ON members.group_id = groups.id
		WHERE collection.singleton = 1
		ORDER BY groups.sort_order ASC, groups.id ASC, members.ordinal ASC
	`)
	if err != nil {
		return config.GroupCollection{}, err
	}
	defer func() {
		if err := rows.Close(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("close group collection rows: %w", err)
		}
	}()

	var revisionSet bool
	for rows.Next() {
		var revision int64
		var id, name, normalizedName, createdAt, updatedAt, member sql.NullString
		var sortOrder, createdBy sql.NullInt64
		if err := rows.Scan(
			&revision,
			&id,
			&name,
			&normalizedName,
			&sortOrder,
			&createdBy,
			&createdAt,
			&updatedAt,
			&member,
		); err != nil {
			return config.GroupCollection{}, err
		}
		if revision < 1 {
			return config.GroupCollection{}, fmt.Errorf("invalid group collection revision")
		}
		if !revisionSet {
			collection.Revision = uint64(revision)
			revisionSet = true
		} else if collection.Revision != uint64(revision) {
			return config.GroupCollection{}, fmt.Errorf("inconsistent group collection revision")
		}
		if !id.Valid {
			continue
		}

		if len(collection.Groups) == 0 || string(collection.Groups[len(collection.Groups)-1].ID) != id.String {
			if !name.Valid || !normalizedName.Valid || !sortOrder.Valid || !createdBy.Valid ||
				!createdAt.Valid || !updatedAt.Valid {
				return config.GroupCollection{}, fmt.Errorf("incomplete configuration group row")
			}
			parsedCreatedAt, err := parseTime("configuration group creation", createdAt.String)
			if err != nil {
				return config.GroupCollection{}, err
			}
			parsedUpdatedAt, err := parseTime("configuration group update", updatedAt.String)
			if err != nil {
				return config.GroupCollection{}, err
			}
			collection.Groups = append(collection.Groups, config.Group{
				ID:             config.GroupID(id.String),
				Name:           name.String,
				NormalizedName: normalizedName.String,
				SortOrder:      int(sortOrder.Int64),
				CreatedBy:      createdBy.Int64,
				CreatedAt:      parsedCreatedAt,
				UpdatedAt:      parsedUpdatedAt,
			})
		}
		if member.Valid {
			last := len(collection.Groups) - 1
			collection.Groups[last].Members = append(collection.Groups[last].Members, config.RelativePath(member.String))
		}
	}
	if err := rows.Err(); err != nil {
		return config.GroupCollection{}, err
	}
	if !revisionSet {
		return config.GroupCollection{}, sql.ErrNoRows
	}
	return collection, nil
}

func validateGroupChange(change config.GroupChange) ([]config.Group, error) {
	if !validRevision(change.ExpectedRevision) || change.ExpectedRevision == math.MaxInt64 {
		return nil, fmt.Errorf("change group collection: %w", config.ErrConflict)
	}
	if err := validateOperationAudit(change.Operation, change.Audit); err != nil {
		return nil, fmt.Errorf("change group collection: %w", err)
	}

	limits := config.DefaultLimits()
	if len(change.Groups) > limits.MaxGroups {
		return nil, fmt.Errorf("change group collection: %w", config.ErrLimitExceeded)
	}
	groups := make([]config.Group, len(change.Groups))
	groupIDs := make(map[config.GroupID]struct{}, len(change.Groups))
	normalizedNames := make(map[string]struct{}, len(change.Groups))
	totalMembers := 0
	for index, input := range change.Groups {
		if _, err := config.ParseGroupID(string(input.ID)); err != nil {
			return nil, fmt.Errorf("change group collection: %w", err)
		}
		if _, exists := groupIDs[input.ID]; exists {
			return nil, fmt.Errorf("change group collection: %w", config.ErrConflict)
		}
		groupIDs[input.ID] = struct{}{}

		display, normalized, err := config.NormalizeGroupName(input.Name)
		if err != nil || display != input.Name {
			return nil, fmt.Errorf("change group collection: %w", config.ErrDisplayNameInvalid)
		}
		if _, exists := normalizedNames[normalized]; exists {
			return nil, fmt.Errorf("change group collection: %w", config.ErrConflict)
		}
		normalizedNames[normalized] = struct{}{}
		if input.CreatedBy <= 0 {
			return nil, fmt.Errorf("change group collection: invalid group creator")
		}
		if len(input.Members) > limits.MaxGroupMembers {
			return nil, fmt.Errorf("change group collection: %w", config.ErrLimitExceeded)
		}
		totalMembers += len(input.Members)
		if totalMembers > limits.MaxTotalGroupMembers {
			return nil, fmt.Errorf("change group collection: %w", config.ErrLimitExceeded)
		}

		group := input
		group.Name = display
		group.NormalizedName = normalized
		group.Members = make([]config.RelativePath, len(input.Members))
		memberSet := make(map[config.RelativePath]struct{}, len(input.Members))
		for memberIndex, inputMember := range input.Members {
			member, err := config.ParseRelativePath(string(inputMember), limits)
			if err != nil {
				return nil, fmt.Errorf("change group collection: %w", err)
			}
			if _, exists := memberSet[member]; exists {
				return nil, fmt.Errorf("change group collection: %w", config.ErrConflict)
			}
			memberSet[member] = struct{}{}
			group.Members[memberIndex] = member
		}
		groups[index] = group
	}
	return groups, nil
}
