/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

// GroupAPI is the logical configuration group behavior consumed by HTTP.
type GroupAPI interface {
	ListGroups(context.Context, *config.WorkspaceID) (config.GroupCollectionView, error)
	CreateGroup(context.Context, config.Actor, config.CreateGroupInput) (config.GroupCollectionView, error)
	ReplaceGroup(context.Context, config.Actor, config.GroupID, config.ReplaceGroupInput) (config.GroupCollectionView, error)
	DeleteGroup(context.Context, config.Actor, config.GroupID, config.DeleteGroupInput) (config.GroupCollectionView, error)
}

type groupMutationRequest struct {
	Name      string   `json:"name"`
	SortOrder int      `json:"sort_order"`
	Members   []string `json:"members"`
}

type deleteGroupRequest struct {
	ConfirmName string `json:"confirm_name"`
}

type groupCollectionResponse struct {
	Groups     []configGroupResponse `json:"groups"`
	GroupsETag string                `json:"groups_etag"`
}

type configGroupResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	SortOrder int       `json:"sort_order"`
	Members   []string  `json:"members"`
	Missing   []string  `json:"missing"`
	CreatedBy int64     `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (h *configHandler) groupsCollection(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		if !authorizeBusinessGET(writer, request, h.sessions) {
			return
		}
		if h.groups == nil {
			writeConfigUnavailable(writer, request)
			return
		}
		values, ok := parseExactQuery(writer, request, "workspace_id", false)
		if !ok {
			return
		}
		var workspaceID *config.WorkspaceID
		if len(values) == 1 {
			parsed, err := config.ParseWorkspaceID(values[0])
			if err != nil {
				writeInvalidConfigRequest(writer, request, "workspace_id")
				return
			}
			workspaceID = &parsed
		}
		view, err := h.groups.ListGroups(request.Context(), workspaceID)
		if err != nil {
			writeConfigAPIError(writer, request, err, nil)
			return
		}
		writeETagJSON(writer, http.StatusOK, view.ETag, newGroupCollectionResponse(view))
	case http.MethodPost:
		actor, ok := authorizeBusinessMutation(writer, request, h.sessions, h.publicURL)
		if !ok {
			return
		}
		if h.groups == nil {
			writeConfigUnavailable(writer, request)
			return
		}
		if !requireNoQuery(writer, request) {
			return
		}
		ifMatch, ok := h.requireGroupIfMatch(writer, request)
		if !ok {
			return
		}
		input, err := decodeStrictJSON[groupMutationRequest](request, configMutationBodyLimit)
		if err != nil {
			writeConfigRequestError(writer, request, err, configMutationBodyLimit)
			return
		}
		members, ok := parseGroupMembers(writer, request, input.Members)
		if !ok || input.Name == "" {
			if ok {
				writeInvalidConfigRequest(writer, request, "name")
			}
			return
		}
		view, err := h.groups.CreateGroup(request.Context(), actor, config.CreateGroupInput{
			Name: input.Name, SortOrder: input.SortOrder, Members: members, IfMatch: ifMatch,
		})
		if err != nil {
			writeConfigAPIError(writer, request, err, nil)
			return
		}
		writeETagJSON(writer, http.StatusCreated, view.ETag, newGroupCollectionResponse(view))
	}
}

func (h *configHandler) group(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, h.sessions, h.publicURL)
	if !ok {
		return
	}
	if h.groups == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	if !requireNoQuery(writer, request) {
		return
	}
	id, err := config.ParseGroupID(request.PathValue("group_id"))
	if err != nil {
		writeInvalidConfigRequest(writer, request, "group_id")
		return
	}
	ifMatch, ok := h.requireGroupIfMatch(writer, request)
	if !ok {
		return
	}
	switch request.Method {
	case http.MethodPut:
		input, err := decodeStrictJSON[groupMutationRequest](request, configMutationBodyLimit)
		if err != nil {
			writeConfigRequestError(writer, request, err, configMutationBodyLimit)
			return
		}
		members, valid := parseGroupMembers(writer, request, input.Members)
		if !valid || input.Name == "" {
			if valid {
				writeInvalidConfigRequest(writer, request, "name")
			}
			return
		}
		view, err := h.groups.ReplaceGroup(request.Context(), actor, id, config.ReplaceGroupInput{
			Name: input.Name, SortOrder: input.SortOrder, Members: members, IfMatch: ifMatch,
		})
		if err != nil {
			writeConfigAPIError(writer, request, err, nil)
			return
		}
		writeETagJSON(writer, http.StatusOK, view.ETag, newGroupCollectionResponse(view))
	case http.MethodDelete:
		input, err := decodeStrictJSON[deleteGroupRequest](request, configMutationBodyLimit)
		if err != nil {
			writeConfigRequestError(writer, request, err, configMutationBodyLimit)
			return
		}
		if input.ConfirmName == "" {
			writeInvalidConfigRequest(writer, request, "confirm_name")
			return
		}
		view, err := h.groups.DeleteGroup(request.Context(), actor, id, config.DeleteGroupInput{ConfirmName: input.ConfirmName, IfMatch: ifMatch})
		if err != nil {
			writeConfigAPIError(writer, request, err, nil)
			return
		}
		writeETagJSON(writer, http.StatusOK, view.ETag, newGroupCollectionResponse(view))
	}
}

func (h *configHandler) requireGroupIfMatch(writer http.ResponseWriter, request *http.Request) (string, bool) {
	raw, valid := oneStrongIfMatch(request, "groups-v1:")
	current, err := h.groups.ListGroups(request.Context(), nil)
	if err != nil {
		writeConfigAPIError(writer, request, err, nil)
		return "", false
	}
	if !valid || raw != current.ETag {
		writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusConflict, "CONFIG_WORKSPACE_CONFLICT", "逻辑组集合已变化", map[string]any{"current_etag": current.ETag})
		return "", false
	}
	return raw, true
}

func parseGroupMembers(writer http.ResponseWriter, request *http.Request, raw []string) ([]config.RelativePath, bool) {
	if raw == nil {
		writeInvalidConfigRequest(writer, request, "members")
		return nil, false
	}
	members := make([]config.RelativePath, len(raw))
	seen := make(map[config.RelativePath]struct{}, len(raw))
	for index, value := range raw {
		member, err := config.ParseRelativePath(value, config.DefaultLimits())
		if err != nil {
			writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusUnprocessableEntity, "CONFIG_PATH_INVALID", "配置路径无效", map[string]any{"path": value, "field": "members"})
			return nil, false
		}
		if _, duplicate := seen[member]; duplicate {
			writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusConflict, "CONFIG_WORKSPACE_CONFLICT", "逻辑组成员重复", map[string]any{"path": value, "field": "members"})
			return nil, false
		}
		seen[member] = struct{}{}
		members[index] = member
	}
	return members, true
}

func newGroupCollectionResponse(view config.GroupCollectionView) groupCollectionResponse {
	response := groupCollectionResponse{Groups: make([]configGroupResponse, len(view.Groups)), GroupsETag: view.ETag}
	for index, item := range view.Groups {
		members := make([]string, len(item.Group.Members))
		for memberIndex, member := range item.Group.Members {
			members[memberIndex] = string(member)
		}
		missing := make([]string, len(item.Missing))
		for memberIndex, member := range item.Missing {
			missing[memberIndex] = string(member)
		}
		response.Groups[index] = configGroupResponse{
			ID: string(item.Group.ID), Name: item.Group.Name, SortOrder: item.Group.SortOrder,
			Members: members, Missing: missing, CreatedBy: item.Group.CreatedBy,
			CreatedAt: item.Group.CreatedAt.UTC(), UpdatedAt: item.Group.UpdatedAt.UTC(),
		}
	}
	return response
}
