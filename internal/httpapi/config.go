/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

const (
	configSmallBodyLimit    int64 = 4 << 10
	configMutationBodyLimit int64 = 256 << 10
	configContentBodyLimit  int64 = (2 << 20) + (64 << 10)
)

// WorkspaceAPI is the configuration workspace behavior consumed by HTTP.
type WorkspaceAPI interface {
	Create(context.Context, config.Actor, string) (config.Workspace, error)
	List(context.Context) ([]config.Workspace, error)
	Get(context.Context, config.WorkspaceID) (config.Workspace, error)
	Delete(context.Context, config.Actor, config.WorkspaceID, string, string) error
	Tree(context.Context, config.WorkspaceID) (config.TreeView, error)
	ReadFile(context.Context, config.WorkspaceID, config.RelativePath) (config.FileView, error)
	CreateFile(context.Context, config.Actor, config.WorkspaceID, config.CreateFileInput) (config.MutationResult, error)
	ReplaceFile(context.Context, config.Actor, config.WorkspaceID, config.ReplaceFileInput) (config.MutationResult, error)
	CopyFile(context.Context, config.Actor, config.WorkspaceID, config.CopyFileInput) (config.MutationResult, error)
	RenameFile(context.Context, config.Actor, config.WorkspaceID, config.RenameFileInput) (config.MutationResult, error)
	DeleteFile(context.Context, config.Actor, config.WorkspaceID, config.DeleteFileInput) (config.MutationResult, error)
	Diff(context.Context, config.WorkspaceID, *config.RelativePath) (config.DiffResult, error)
	Search(context.Context, config.WorkspaceID, string) (config.SearchResult, error)
}

type configHandler struct {
	workspaces WorkspaceAPI
	structured StructuredAPI
	groups     GroupAPI
	sessions   SessionService
	publicURL  *url.URL
}

type workspaceResponse struct {
	ID               string                `json:"id"`
	Name             string                `json:"name"`
	State            config.WorkspaceState `json:"state"`
	StateReasonCode  string                `json:"state_reason_code,omitempty"`
	ProductionDigest string                `json:"production_digest"`
	BaseDigest       string                `json:"base_digest"`
	DraftETag        string                `json:"draft_etag"`
	EntryCount       int                   `json:"entry_count"`
	ManagedBytes     int64                 `json:"managed_bytes"`
	WorkspaceBytes   int64                 `json:"workspace_bytes"`
	CreatedBy        int64                 `json:"created_by"`
	CreatedAt        time.Time             `json:"created_at"`
	UpdatedAt        time.Time             `json:"updated_at"`
	LastReleaseID    string                `json:"last_release_id,omitempty"`
}

type workspaceListResponse struct {
	Workspaces []workspaceResponse `json:"workspaces"`
}

type configTreeResponse struct {
	Entries      []configTreeNodeResponse   `json:"entries"`
	Dependencies []configDependencyResponse `json:"dependencies"`
	DraftETag    string                     `json:"draft_etag"`
}

type configTreeNodeResponse struct {
	Path                  string           `json:"path"`
	Name                  string           `json:"name"`
	EntryType             config.EntryType `json:"entry_type"`
	Managed               bool             `json:"managed"`
	ReadOnly              bool             `json:"read_only"`
	StatusReasonCode      string           `json:"status_reason_code"`
	SizeBytes             *int64           `json:"size_bytes,omitempty"`
	ContentDigest         string           `json:"content_digest,omitempty"`
	DiffStatus            string           `json:"diff_status,omitempty"`
	DependencyStatus      string           `json:"dependency_status,omitempty"`
	DependencyTargetCount int              `json:"dependency_target_count,omitempty"`
	DependencyCycle       bool             `json:"dependency_cycle,omitempty"`
}

type configDependencyResponse struct {
	Source       string                  `json:"source"`
	Line         int                     `json:"line"`
	Column       int                     `json:"column"`
	DisplayValue string                  `json:"display_value"`
	Target       string                  `json:"target,omitempty"`
	Status       config.DependencyStatus `json:"status"`
	Cycle        bool                    `json:"cycle"`
}

type configFileResponse struct {
	Path          string `json:"path"`
	Content       string `json:"content"`
	SizeBytes     int64  `json:"size_bytes"`
	ContentDigest string `json:"content_digest"`
	LineEnding    string `json:"line_ending"`
	DraftETag     string `json:"draft_etag"`
}

type fileMutationResponse struct {
	Workspace workspaceResponse       `json:"workspace"`
	Entry     *configTreeNodeResponse `json:"entry,omitempty"`
	DraftETag string                  `json:"draft_etag"`
}

type createWorkspaceRequest struct {
	Name string `json:"name"`
}

type deleteWorkspaceRequest struct {
	ConfirmName string `json:"confirm_name"`
}

type createFileRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type replaceFileRequest struct {
	Content string `json:"content"`
}

type renameFileRequest struct {
	DestinationPath string `json:"destination_path"`
}

type copyFileRequest struct {
	SourcePath      string `json:"source_path"`
	DestinationPath string `json:"destination_path"`
}

type deleteFileRequest struct {
	ConfirmPath string `json:"confirm_path"`
}

func (h *configHandler) workspacesCollection(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		if !authorizeBusinessGET(writer, request, h.sessions) {
			return
		}
		if h.workspaces == nil {
			writeConfigUnavailable(writer, request)
			return
		}
		if !requireNoQuery(writer, request) {
			return
		}
		workspaces, err := h.workspaces.List(request.Context())
		if err != nil {
			writeConfigAPIError(writer, request, err, nil)
			return
		}
		slices.SortFunc(workspaces, func(left, right config.Workspace) int {
			if compared := right.UpdatedAt.Compare(left.UpdatedAt); compared != 0 {
				return compared
			}
			return strings.Compare(string(left.ID), string(right.ID))
		})
		response := workspaceListResponse{Workspaces: make([]workspaceResponse, len(workspaces))}
		for index, workspace := range workspaces {
			response.Workspaces[index] = newWorkspaceResponse(workspace)
		}
		writeJSON(writer, http.StatusOK, response)
	case http.MethodPost:
		actor, ok := authorizeBusinessMutation(writer, request, h.sessions, h.publicURL)
		if !ok {
			return
		}
		if h.workspaces == nil {
			writeConfigUnavailable(writer, request)
			return
		}
		input, err := decodeStrictJSON[createWorkspaceRequest](request, configSmallBodyLimit)
		if err != nil {
			writeConfigRequestError(writer, request, err, configSmallBodyLimit)
			return
		}
		if input.Name == "" {
			writeInvalidConfigRequest(writer, request, "name")
			return
		}
		workspace, err := h.workspaces.Create(request.Context(), actor, input.Name)
		if err != nil {
			writeConfigAPIError(writer, request, err, nil)
			return
		}
		writeETagJSON(writer, http.StatusCreated, workspace.ETag(), newWorkspaceResponse(workspace))
	}
}

func (h *configHandler) workspace(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		if !authorizeBusinessGET(writer, request, h.sessions) {
			return
		}
		if h.workspaces == nil {
			writeConfigUnavailable(writer, request)
			return
		}
		id, ok := parseWorkspaceRouteID(writer, request)
		if !ok || !requireNoQuery(writer, request) {
			return
		}
		workspace, err := h.workspaces.Get(request.Context(), id)
		if err != nil {
			writeConfigAPIError(writer, request, err, nil)
			return
		}
		writeETagJSON(writer, http.StatusOK, workspace.ETag(), newWorkspaceResponse(workspace))
	case http.MethodDelete:
		actor, authorized := authorizeBusinessMutation(writer, request, h.sessions, h.publicURL)
		if !authorized {
			return
		}
		if h.workspaces == nil {
			writeConfigUnavailable(writer, request)
			return
		}
		id, ok := parseWorkspaceRouteID(writer, request)
		if !ok || !requireNoQuery(writer, request) {
			return
		}
		ifMatch, ok := h.requireWorkspaceIfMatch(writer, request, id, false)
		if !ok {
			return
		}
		input, err := decodeStrictJSON[deleteWorkspaceRequest](request, configMutationBodyLimit)
		if err != nil {
			writeConfigRequestError(writer, request, err, configMutationBodyLimit)
			return
		}
		if input.ConfirmName == "" {
			writeInvalidConfigRequest(writer, request, "confirm_name")
			return
		}
		if err := h.workspaces.Delete(request.Context(), actor, id, ifMatch, input.ConfirmName); err != nil {
			writeConfigAPIError(writer, request, err, nil)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}
}

func (h *configHandler) files(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodGet {
		if !authorizeBusinessGET(writer, request, h.sessions) {
			return
		}
		if h.workspaces == nil {
			writeConfigUnavailable(writer, request)
			return
		}
		id, ok := parseWorkspaceRouteID(writer, request)
		if !ok {
			return
		}
		h.readFiles(writer, request, id)
		return
	}
	actor, authorized := authorizeBusinessMutation(writer, request, h.sessions, h.publicURL)
	if !authorized {
		return
	}
	if h.workspaces == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	id, ok := parseWorkspaceRouteID(writer, request)
	if !ok {
		return
	}
	ifMatch, ok := h.requireWorkspaceIfMatch(writer, request, id, true)
	if !ok {
		return
	}
	switch request.Method {
	case http.MethodPost:
		if !requireNoQuery(writer, request) {
			return
		}
		input, err := decodeStrictJSON[createFileRequest](request, configContentBodyLimit)
		if err != nil {
			writeConfigRequestError(writer, request, err, configContentBodyLimit)
			return
		}
		filePath, ok := parseRequestPath(writer, request, "path", input.Path)
		if !ok {
			return
		}
		result, err := h.workspaces.CreateFile(request.Context(), actor, id, config.CreateFileInput{Path: filePath, Content: []byte(input.Content), IfMatch: ifMatch})
		writeMutationResult(writer, request, result, err, http.StatusCreated)
	case http.MethodPut:
		filePath, ok := requirePathQuery(writer, request)
		if !ok {
			return
		}
		input, err := decodeStrictJSON[replaceFileRequest](request, configContentBodyLimit)
		if err != nil {
			writeConfigRequestError(writer, request, err, configContentBodyLimit)
			return
		}
		result, err := h.workspaces.ReplaceFile(request.Context(), actor, id, config.ReplaceFileInput{Path: filePath, Content: []byte(input.Content), IfMatch: ifMatch})
		writeMutationResult(writer, request, result, err, http.StatusOK)
	case http.MethodPatch:
		source, ok := requirePathQuery(writer, request)
		if !ok {
			return
		}
		input, err := decodeStrictJSON[renameFileRequest](request, configMutationBodyLimit)
		if err != nil {
			writeConfigRequestError(writer, request, err, configMutationBodyLimit)
			return
		}
		destination, ok := parseRequestPath(writer, request, "destination_path", input.DestinationPath)
		if !ok {
			return
		}
		result, err := h.workspaces.RenameFile(request.Context(), actor, id, config.RenameFileInput{SourcePath: source, DestinationPath: destination, IfMatch: ifMatch})
		writeMutationResult(writer, request, result, err, http.StatusOK)
	case http.MethodDelete:
		filePath, ok := requirePathQuery(writer, request)
		if !ok {
			return
		}
		input, err := decodeStrictJSON[deleteFileRequest](request, configMutationBodyLimit)
		if err != nil {
			writeConfigRequestError(writer, request, err, configMutationBodyLimit)
			return
		}
		if input.ConfirmPath == "" {
			writeInvalidConfigRequest(writer, request, "confirm_path")
			return
		}
		result, err := h.workspaces.DeleteFile(request.Context(), actor, id, config.DeleteFileInput{Path: filePath, ConfirmPath: input.ConfirmPath, IfMatch: ifMatch})
		writeMutationResult(writer, request, result, err, http.StatusOK)
	}
}

func (h *configHandler) readFiles(writer http.ResponseWriter, request *http.Request, id config.WorkspaceID) {
	values, ok := parseExactQuery(writer, request, "path", false)
	if !ok {
		return
	}
	if len(values) == 0 {
		tree, err := h.workspaces.Tree(request.Context(), id)
		if err != nil {
			writeConfigAPIError(writer, request, err, nil)
			return
		}
		writeETagJSON(writer, http.StatusOK, tree.WorkspaceETag, newConfigTreeResponse(tree))
		return
	}
	filePath, ok := parseRequestPath(writer, request, "path", values[0])
	if !ok {
		return
	}
	file, err := h.workspaces.ReadFile(request.Context(), id, filePath)
	if err != nil {
		writeConfigAPIError(writer, request, err, map[string]any{"path": string(filePath)})
		return
	}
	writeETagJSON(writer, http.StatusOK, file.WorkspaceETag, configFileResponse{
		Path: string(file.Entry.Path), Content: file.Content, SizeBytes: file.Entry.Size,
		ContentDigest: file.Entry.ContentDigest.String(), LineEnding: file.LineEnding, DraftETag: file.WorkspaceETag,
	})
}

func (h *configHandler) copies(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, h.sessions, h.publicURL)
	if !ok {
		return
	}
	if h.workspaces == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	if !requireNoQuery(writer, request) {
		return
	}
	id, ok := parseWorkspaceRouteID(writer, request)
	if !ok {
		return
	}
	ifMatch, ok := h.requireWorkspaceIfMatch(writer, request, id, true)
	if !ok {
		return
	}
	input, err := decodeStrictJSON[copyFileRequest](request, configMutationBodyLimit)
	if err != nil {
		writeConfigRequestError(writer, request, err, configMutationBodyLimit)
		return
	}
	source, sourceOK := parseRequestPath(writer, request, "source_path", input.SourcePath)
	if !sourceOK {
		return
	}
	destination, destinationOK := parseRequestPath(writer, request, "destination_path", input.DestinationPath)
	if !destinationOK {
		return
	}
	result, err := h.workspaces.CopyFile(request.Context(), actor, id, config.CopyFileInput{SourcePath: source, DestinationPath: destination, IfMatch: ifMatch})
	writeMutationResult(writer, request, result, err, http.StatusCreated)
}

func (h *configHandler) search(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, h.sessions) {
		return
	}
	if h.workspaces == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	id, ok := parseWorkspaceRouteID(writer, request)
	if !ok {
		return
	}
	values, ok := parseExactQuery(writer, request, "query", true)
	if !ok {
		return
	}
	result, err := h.workspaces.Search(request.Context(), id, values[0])
	if err != nil {
		writeConfigAPIError(writer, request, err, map[string]any{"field": "query"})
		return
	}
	if result.Matches == nil {
		result.Matches = make([]config.SearchMatch, 0)
	}
	writeJSON(writer, http.StatusOK, result)
}

func (h *configHandler) diff(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, h.sessions) {
		return
	}
	if h.workspaces == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	id, ok := parseWorkspaceRouteID(writer, request)
	if !ok {
		return
	}
	values, ok := parseExactQuery(writer, request, "path", false)
	if !ok {
		return
	}
	var selected *config.RelativePath
	if len(values) == 1 {
		parsed, ok := parseRequestPath(writer, request, "path", values[0])
		if !ok {
			return
		}
		selected = &parsed
	}
	result, err := h.workspaces.Diff(request.Context(), id, selected)
	if err != nil {
		writeConfigAPIError(writer, request, err, nil)
		return
	}
	if result.Files == nil {
		result.Files = make([]config.FileDiffSummary, 0)
	}
	writeJSON(writer, http.StatusOK, result)
}

func (h *configHandler) requireWorkspaceIfMatch(
	writer http.ResponseWriter,
	request *http.Request,
	id config.WorkspaceID,
	requireWritable bool,
) (string, bool) {
	raw, valid := oneStrongIfMatch(request, "draft-v1:")
	workspace, err := h.workspaces.Get(request.Context(), id)
	if err != nil {
		writeConfigAPIError(writer, request, err, nil)
		return "", false
	}
	if requireWritable && workspace.State == config.StateStale {
		writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusConflict, "CONFIG_WORKSPACE_STALE", "生产配置已变化，工作区只读", nil)
		return "", false
	}
	if requireWritable && workspace.State == config.StateNeedsAttention {
		writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusConflict, "CONFIG_WORKSPACE_NEEDS_ATTENTION", "工作区需要人工处理", nil)
		return "", false
	}
	if !valid || raw != workspace.ETag() {
		writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusConflict, "CONFIG_WORKSPACE_CONFLICT", "工作区已变化", map[string]any{"current_etag": workspace.ETag()})
		return "", false
	}
	return raw, true
}

func oneStrongIfMatch(request *http.Request, prefix string) (string, bool) {
	values := request.Header.Values("If-Match")
	if len(values) != 1 || strings.Contains(values[0], ",") {
		return "", false
	}
	if _, err := config.ParseStrongETag(values[0], prefix); err != nil {
		return "", false
	}
	return values[0], true
}

func parseWorkspaceRouteID(writer http.ResponseWriter, request *http.Request) (config.WorkspaceID, bool) {
	id, err := config.ParseWorkspaceID(request.PathValue("workspace_id"))
	if err != nil {
		writeInvalidConfigRequest(writer, request, "workspace_id")
		return "", false
	}
	return id, true
}

func parseRequestPath(writer http.ResponseWriter, request *http.Request, field, raw string) (config.RelativePath, bool) {
	parsed, err := config.ParseRelativePath(raw, config.DefaultLimits())
	if err != nil {
		writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusUnprocessableEntity, "CONFIG_PATH_INVALID", "配置路径无效", map[string]any{"path": raw, "field": field})
		return "", false
	}
	return parsed, true
}

func requirePathQuery(writer http.ResponseWriter, request *http.Request) (config.RelativePath, bool) {
	values, ok := parseExactQuery(writer, request, "path", true)
	if !ok {
		return "", false
	}
	return parseRequestPath(writer, request, "path", values[0])
}

func parseExactQuery(writer http.ResponseWriter, request *http.Request, name string, required bool) ([]string, bool) {
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil || len(query) > 1 {
		writeInvalidConfigRequest(writer, request, "query")
		return nil, false
	}
	values, exists := query[name]
	if len(query) == 1 && !exists {
		writeInvalidConfigRequest(writer, request, "query")
		return nil, false
	}
	if !exists {
		if required {
			writeInvalidConfigRequest(writer, request, name)
			return nil, false
		}
		return nil, true
	}
	if len(values) != 1 || values[0] == "" {
		writeInvalidConfigRequest(writer, request, name)
		return nil, false
	}
	return values, true
}

func requireNoQuery(writer http.ResponseWriter, request *http.Request) bool {
	if request.URL.RawQuery == "" {
		return true
	}
	writeInvalidConfigRequest(writer, request, "query")
	return false
}

func newWorkspaceResponse(workspace config.Workspace) workspaceResponse {
	return workspaceResponse{
		ID: string(workspace.ID), Name: workspace.Name, State: workspace.State, StateReasonCode: workspace.StateReasonCode,
		ProductionDigest: workspace.ProductionDigest.String(), BaseDigest: workspace.BaseDigest.String(), DraftETag: workspace.ETag(),
		EntryCount: workspace.EntryCount, ManagedBytes: workspace.ManagedBytes, WorkspaceBytes: workspace.WorkspaceBytes,
		CreatedBy: workspace.CreatedBy, CreatedAt: workspace.CreatedAt.UTC(), UpdatedAt: workspace.UpdatedAt.UTC(),
		LastReleaseID: string(workspace.LastReleaseID),
	}
}

func newConfigTreeResponse(tree config.TreeView) configTreeResponse {
	response := configTreeResponse{
		Entries:      make([]configTreeNodeResponse, len(tree.Entries)),
		Dependencies: make([]configDependencyResponse, len(tree.Dependencies)), DraftETag: tree.WorkspaceETag,
	}
	summaries := make(map[config.RelativePath]dependencySummary)
	for index, dependency := range tree.Dependencies {
		summary := summaries[dependency.Source]
		summary.targetCount++
		summary.cycle = summary.cycle || dependency.Cycle || dependency.Status == config.DependencyCycle
		if dependencyStatusPriority(dependency.Status) > dependencyStatusPriority(summary.status) {
			summary.status = dependency.Status
		}
		summaries[dependency.Source] = summary
		response.Dependencies[index] = configDependencyResponse{
			Source: string(dependency.Source), Line: dependency.Line, Column: dependency.Column,
			DisplayValue: dependency.DisplayValue, Target: string(dependency.Target), Status: dependency.Status, Cycle: dependency.Cycle,
		}
	}
	for index, entry := range tree.Entries {
		response.Entries[index] = newConfigTreeNodeResponse(entry)
		if status, exists := tree.DiffStatuses[entry.Path]; exists && response.Entries[index].Managed {
			response.Entries[index].DiffStatus = status
		}
		if summary, exists := summaries[entry.Path]; exists {
			response.Entries[index].DependencyStatus = string(summary.status)
			response.Entries[index].DependencyTargetCount = summary.targetCount
			response.Entries[index].DependencyCycle = summary.cycle
		}
	}
	return response
}

type dependencySummary struct {
	status      config.DependencyStatus
	targetCount int
	cycle       bool
}

func dependencyStatusPriority(status config.DependencyStatus) int {
	switch status {
	case config.DependencyCycle:
		return 7
	case config.DependencyExternal:
		return 6
	case config.DependencyUnresolved:
		return 5
	case config.DependencyMissing:
		return 4
	case config.DependencySymlink:
		return 3
	case config.DependencySpecial:
		return 2
	case config.DependencyResolved:
		return 1
	default:
		return 0
	}
}

func newConfigTreeNodeResponse(entry config.Entry) configTreeNodeResponse {
	managed := entry.Type == config.EntryRegular && entry.Class == config.EntryManagedText
	response := configTreeNodeResponse{
		Path: string(entry.Path), Name: path.Base(string(entry.Path)), EntryType: entry.Type,
		Managed: managed, ReadOnly: !managed, StatusReasonCode: string(entry.Class),
	}
	if entry.Type == config.EntryRegular {
		size := entry.Size
		response.SizeBytes = &size
	}
	if managed {
		response.ContentDigest = entry.ContentDigest.String()
	}
	return response
}

func writeMutationResult(writer http.ResponseWriter, request *http.Request, result config.MutationResult, err error, status int) {
	if err != nil {
		writeConfigAPIError(writer, request, err, nil)
		return
	}
	response := fileMutationResponse{Workspace: newWorkspaceResponse(result.Workspace), DraftETag: result.Workspace.ETag()}
	if result.Entry != nil {
		entry := newConfigTreeNodeResponse(*result.Entry)
		response.Entry = &entry
	}
	writeETagJSON(writer, status, response.DraftETag, response)
}

func writeETagJSON(writer http.ResponseWriter, status int, etag string, value any) {
	writer.Header().Set("ETag", etag)
	writeJSON(writer, status, value)
}

func writeConfigRequestError(writer http.ResponseWriter, request *http.Request, err error, limit int64) {
	switch {
	case errors.Is(err, errUnsupportedJSONMediaType):
		writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusUnsupportedMediaType, "unsupported_media_type", "仅接受 application/json", nil)
	case errors.Is(err, errRequestBodyTooLarge):
		writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusRequestEntityTooLarge, "CONFIG_LIMIT_EXCEEDED", "请求体超过安全限制", map[string]any{"limit_name": "request_body_bytes", "limit_value": limit})
	default:
		writeInvalidConfigRequest(writer, request, "body")
	}
}

func writeInvalidConfigRequest(writer http.ResponseWriter, request *http.Request, field string) {
	writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusBadRequest, "invalid_request", "请求格式无效", map[string]any{"field": field})
}

func writeConfigUnavailable(writer http.ResponseWriter, request *http.Request) {
	writeAPIError(writer, requestIDFromContext(request.Context()), http.StatusServiceUnavailable, "service_unavailable", "服务暂时不可用", nil)
}

func configNoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/api/v1/config/") || request.URL.Path == "/api/v1/config" {
			writer.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(writer, request)
	})
}
