/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"

	"github.com/kuroky/nginx-uix/internal/config"
	"github.com/kuroky/nginx-uix/internal/location"
	"github.com/kuroky/nginx-uix/internal/nginxast"
	"github.com/kuroky/nginx-uix/internal/structuredconfig"
	"github.com/kuroky/nginx-uix/internal/upstream"
)

const structuredRequestBodyLimit int64 = 256 << 10

// StructuredAPI is the ETag-bound structured configuration behavior consumed by HTTP.
type StructuredAPI interface {
	Catalog(context.Context, config.WorkspaceID) (structuredconfig.Projection, error)
	Preview(
		context.Context,
		config.WorkspaceID,
		structuredconfig.Operation,
	) (structuredconfig.Preview, error)
	Apply(
		context.Context,
		config.Actor,
		config.WorkspaceID,
		structuredconfig.Operation,
		string,
		string,
	) (config.ReplaceFilesResult, error)
}

type structuredOperationRequest struct {
	Kind  structuredconfig.OperationKind `json:"kind"`
	Input json.RawMessage                `json:"input"`
}

type structuredApplyRequest struct {
	PreviewID string                         `json:"preview_id"`
	Kind      structuredconfig.OperationKind `json:"kind"`
	Input     json.RawMessage                `json:"input"`
}

type upstreamServerInputRequest struct {
	Address     string  `json:"address"`
	Port        *uint16 `json:"port"`
	Unix        bool    `json:"unix"`
	Weight      *int    `json:"weight"`
	Backup      bool    `json:"backup"`
	Down        bool    `json:"down"`
	MaxFails    *int    `json:"max_fails"`
	FailTimeout *string `json:"fail_timeout"`
}

type upstreamCreateRequest struct {
	HTTPBlockID string                       `json:"http_block_id"`
	Name        string                       `json:"name"`
	Servers     []upstreamServerInputRequest `json:"servers"`
}

type upstreamRenameRequest struct {
	UpstreamID string `json:"upstream_id"`
	NewName    string `json:"new_name"`
}

type upstreamDeleteRequest struct {
	UpstreamID  string `json:"upstream_id"`
	ConfirmName string `json:"confirm_name"`
}

type upstreamServerCreateRequest struct {
	UpstreamID string                     `json:"upstream_id"`
	Server     upstreamServerInputRequest `json:"server"`
}

type upstreamServerUpdateRequest struct {
	UpstreamID string                     `json:"upstream_id"`
	ServerID   string                     `json:"server_id"`
	Server     upstreamServerInputRequest `json:"server"`
}

type upstreamServerDeleteRequest struct {
	UpstreamID string `json:"upstream_id"`
	ServerID   string `json:"server_id"`
}

type proxyPassInputRequest struct {
	UpstreamID string  `json:"upstream_id"`
	Scheme     string  `json:"scheme"`
	Port       *uint16 `json:"port"`
	URI        string  `json:"uri"`
}

type locationCreateRequest struct {
	ParentID  string                 `json:"parent_id"`
	Type      location.MatcherType   `json:"type"`
	Matcher   string                 `json:"matcher"`
	ProxyPass *proxyPassInputRequest `json:"proxy_pass"`
}

type locationUpdateRequest struct {
	LocationID string                 `json:"location_id"`
	Type       location.MatcherType   `json:"type"`
	Matcher    string                 `json:"matcher"`
	ProxyMode  location.ProxyMode     `json:"proxy_mode"`
	ProxyPass  *proxyPassInputRequest `json:"proxy_pass"`
}

type locationDeleteRequest struct {
	LocationID     string `json:"location_id"`
	ConfirmMatcher string `json:"confirm_matcher"`
}

type structuredChangedFileResponse struct {
	Path         string `json:"path"`
	BeforeDigest string `json:"before_digest"`
	AfterDigest  string `json:"after_digest"`
	AddedLines   int    `json:"added_lines"`
	RemovedLines int    `json:"removed_lines"`
	Patch        string `json:"patch"`
}

type structuredPreviewResponse struct {
	PreviewID     string                          `json:"preview_id"`
	WorkspaceID   string                          `json:"workspace_id"`
	DraftETag     string                          `json:"draft_etag"`
	OperationKind structuredconfig.OperationKind  `json:"operation_kind"`
	TargetID      string                          `json:"target_id"`
	ChangedFiles  []structuredChangedFileResponse `json:"changed_files"`
	Complete      bool                            `json:"complete"`
}

type structuredApplyResponse struct {
	Workspace    workspaceResponse `json:"workspace"`
	DraftETag    string            `json:"draft_etag"`
	ChangedPaths []string          `json:"changed_paths"`
}

func (h *configHandler) structuredCatalog(writer http.ResponseWriter, request *http.Request) {
	if !authorizeBusinessGET(writer, request, h.sessions) {
		return
	}
	if h.structured == nil || h.workspaces == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	id, ok := parseWorkspaceRouteID(writer, request)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	if _, ok := h.requirePublicWorkspace(writer, request, id); !ok {
		return
	}
	projection, err := h.structured.Catalog(request.Context(), id)
	if err != nil {
		writeStructuredAPIError(writer, request, err)
		return
	}
	writeETagJSON(writer, http.StatusOK, projection.DraftETag, newStructuredCatalogResponse(projection))
}

func (h *configHandler) structuredPreview(writer http.ResponseWriter, request *http.Request) {
	if _, ok := authorizeBusinessMutation(writer, request, h.sessions, h.publicURL); !ok {
		return
	}
	if h.structured == nil || h.workspaces == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	id, ok := parseWorkspaceRouteID(writer, request)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	if _, ok := h.requirePublicWorkspace(writer, request, id); !ok {
		return
	}
	input, err := decodeStrictJSON[structuredOperationRequest](request, structuredRequestBodyLimit)
	if err != nil {
		writeConfigRequestError(writer, request, err, structuredRequestBodyLimit)
		return
	}
	operation, err := decodeStructuredOperation(input.Kind, input.Input)
	if err != nil {
		writeInvalidConfigRequest(writer, request, "input")
		return
	}
	preview, err := h.structured.Preview(request.Context(), id, operation)
	if err != nil {
		writeStructuredAPIError(writer, request, err)
		return
	}
	writeETagJSON(writer, http.StatusOK, preview.DraftETag, newStructuredPreviewResponse(preview))
}

func (h *configHandler) structuredApply(writer http.ResponseWriter, request *http.Request) {
	actor, ok := authorizeBusinessMutation(writer, request, h.sessions, h.publicURL)
	if !ok {
		return
	}
	if h.structured == nil || h.workspaces == nil {
		writeConfigUnavailable(writer, request)
		return
	}
	id, ok := parseWorkspaceRouteID(writer, request)
	if !ok || !requireNoQuery(writer, request) {
		return
	}
	ifMatch, ok := h.requireWorkspaceIfMatch(writer, request, id, true)
	if !ok {
		return
	}
	input, err := decodeStrictJSON[structuredApplyRequest](request, structuredRequestBodyLimit)
	if err != nil {
		writeConfigRequestError(writer, request, err, structuredRequestBodyLimit)
		return
	}
	if _, err := config.ParseDigest(input.PreviewID); err != nil {
		writeInvalidConfigRequest(writer, request, "preview_id")
		return
	}
	operation, err := decodeStructuredOperation(input.Kind, input.Input)
	if err != nil {
		writeInvalidConfigRequest(writer, request, "input")
		return
	}
	result, err := h.structured.Apply(
		request.Context(), actor, id, operation, input.PreviewID, ifMatch,
	)
	if err != nil {
		writeStructuredAPIError(writer, request, err)
		return
	}
	paths := make([]string, len(result.ChangedPaths))
	for index, path := range result.ChangedPaths {
		paths[index] = string(path)
	}
	response := structuredApplyResponse{
		Workspace: newWorkspaceResponse(result.Workspace),
		DraftETag: result.Workspace.ETag(), ChangedPaths: paths,
	}
	writeETagJSON(writer, http.StatusOK, response.DraftETag, response)
}

func decodeStructuredOperation(
	kind structuredconfig.OperationKind,
	raw json.RawMessage,
) (structuredconfig.Operation, error) {
	operation := structuredconfig.Operation{Kind: kind}
	switch kind {
	case structuredconfig.OperationUpstreamCreate:
		input, err := decodeStructuredInput[upstreamCreateRequest](raw)
		if err != nil {
			return structuredconfig.Operation{}, err
		}
		servers := make([]upstream.ServerInput, len(input.Servers))
		for index, server := range input.Servers {
			servers[index] = server.domain()
		}
		operation.UpstreamCreate = &upstream.CreateInput{
			HTTPBlockID: input.HTTPBlockID, Name: input.Name, Servers: servers,
		}
	case structuredconfig.OperationUpstreamRename:
		input, err := decodeStructuredInput[upstreamRenameRequest](raw)
		if err != nil {
			return structuredconfig.Operation{}, err
		}
		operation.UpstreamRename = &upstream.RenameInput{
			UpstreamID: input.UpstreamID, NewName: input.NewName,
		}
	case structuredconfig.OperationUpstreamDelete:
		input, err := decodeStructuredInput[upstreamDeleteRequest](raw)
		if err != nil {
			return structuredconfig.Operation{}, err
		}
		operation.UpstreamDelete = &upstream.DeleteInput{
			UpstreamID: input.UpstreamID, ConfirmName: input.ConfirmName,
		}
	case structuredconfig.OperationUpstreamServerCreate:
		input, err := decodeStructuredInput[upstreamServerCreateRequest](raw)
		if err != nil {
			return structuredconfig.Operation{}, err
		}
		operation.UpstreamServerCreate = &upstream.CreateServerInput{
			UpstreamID: input.UpstreamID, Server: input.Server.domain(),
		}
	case structuredconfig.OperationUpstreamServerUpdate:
		input, err := decodeStructuredInput[upstreamServerUpdateRequest](raw)
		if err != nil {
			return structuredconfig.Operation{}, err
		}
		operation.UpstreamServerUpdate = &upstream.UpdateServerInput{
			UpstreamID: input.UpstreamID, ServerID: input.ServerID, Server: input.Server.domain(),
		}
	case structuredconfig.OperationUpstreamServerDelete:
		input, err := decodeStructuredInput[upstreamServerDeleteRequest](raw)
		if err != nil {
			return structuredconfig.Operation{}, err
		}
		operation.UpstreamServerDelete = &upstream.DeleteServerInput{
			UpstreamID: input.UpstreamID, ServerID: input.ServerID,
		}
	case structuredconfig.OperationLocationCreate:
		input, err := decodeStructuredInput[locationCreateRequest](raw)
		if err != nil {
			return structuredconfig.Operation{}, err
		}
		operation.LocationCreate = &location.CreateInput{
			ParentID: input.ParentID, Type: input.Type, Matcher: input.Matcher,
			ProxyPass: input.ProxyPass.domain(),
		}
	case structuredconfig.OperationLocationUpdate:
		input, err := decodeStructuredInput[locationUpdateRequest](raw)
		if err != nil {
			return structuredconfig.Operation{}, err
		}
		operation.LocationUpdate = &location.UpdateInput{
			LocationID: input.LocationID, Type: input.Type, Matcher: input.Matcher,
			ProxyMode: input.ProxyMode, ProxyPass: input.ProxyPass.domain(),
		}
	case structuredconfig.OperationLocationDelete:
		input, err := decodeStructuredInput[locationDeleteRequest](raw)
		if err != nil {
			return structuredconfig.Operation{}, err
		}
		operation.LocationDelete = &location.DeleteInput{
			LocationID: input.LocationID, ConfirmMatcher: input.ConfirmMatcher,
		}
	default:
		return structuredconfig.Operation{}, structuredconfig.ErrInvalidOperation
	}
	return operation, nil
}

func decodeStructuredInput[T any](raw json.RawMessage) (T, error) {
	var zero T
	if len(raw) == 0 {
		return zero, structuredconfig.ErrInvalidOperation
	}
	if err := validateUniqueJSONObject(raw, exactJSONFields(reflect.TypeFor[T]())); err != nil {
		return zero, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var result T
	if err := decoder.Decode(&result); err != nil {
		return zero, err
	}
	if token, err := decoder.Token(); err != io.EOF || token != nil {
		return zero, structuredconfig.ErrInvalidOperation
	}
	return result, nil
}

func (request upstreamServerInputRequest) domain() upstream.ServerInput {
	return upstream.ServerInput{
		Endpoint: upstream.Endpoint{Address: request.Address, Port: request.Port, Unix: request.Unix},
		Weight:   request.Weight, Backup: request.Backup, Down: request.Down,
		MaxFails: request.MaxFails, FailTimeout: request.FailTimeout,
	}
}

func (request *proxyPassInputRequest) domain() *location.ProxyPassInput {
	if request == nil {
		return nil
	}
	return &location.ProxyPassInput{
		UpstreamID: request.UpstreamID, Scheme: request.Scheme, Port: request.Port, URI: request.URI,
	}
}

func newStructuredPreviewResponse(preview structuredconfig.Preview) structuredPreviewResponse {
	response := structuredPreviewResponse{
		PreviewID: preview.PreviewID, WorkspaceID: string(preview.WorkspaceID),
		DraftETag: preview.DraftETag, OperationKind: preview.OperationKind,
		TargetID: preview.TargetID, Complete: preview.Complete,
		ChangedFiles: make([]structuredChangedFileResponse, len(preview.ChangedFiles)),
	}
	for index, file := range preview.ChangedFiles {
		response.ChangedFiles[index] = structuredChangedFileResponse{
			Path: string(file.Path), BeforeDigest: file.BeforeDigest, AfterDigest: file.AfterDigest,
			AddedLines: file.AddedLines, RemovedLines: file.RemovedLines, Patch: file.Patch,
		}
	}
	return response
}

func writeStructuredAPIError(writer http.ResponseWriter, request *http.Request, err error) {
	requestID := requestIDFromContext(request.Context())
	switch {
	case errors.Is(err, structuredconfig.ErrInvalidOperation):
		writeAPIError(writer, requestID, http.StatusBadRequest, "invalid_request", "请求格式无效", map[string]any{"field": "kind"})
	case errors.Is(err, structuredconfig.ErrPreviewStale):
		writeAPIError(writer, requestID, http.StatusConflict, "STRUCTURED_PREVIEW_STALE", "结构化预览已失效", nil)
	case errors.Is(err, structuredconfig.ErrParseFailed):
		writeAPIError(writer, requestID, http.StatusUnprocessableEntity, "STRUCTURED_PARSE_FAILED", "结构化配置无法安全解析", nil)
	case errors.Is(err, structuredconfig.ErrPreviewIncomplete),
		errors.Is(err, structuredconfig.ErrLimitExceeded),
		errors.Is(err, nginxast.ErrLimitExceeded):
		writeAPIError(writer, requestID, http.StatusRequestEntityTooLarge, "STRUCTURED_LIMIT_EXCEEDED", "结构化预览超过安全限制", nil)
	case errors.Is(err, structuredconfig.ErrPostcondition),
		errors.Is(err, upstream.ErrEditConflict),
		errors.Is(err, location.ErrEditConflict),
		errors.Is(err, nginxast.ErrEditOverlap):
		writeAPIError(writer, requestID, http.StatusConflict, "STRUCTURED_EDIT_CONFLICT", "结构化变更与当前配置冲突", nil)
	case errors.Is(err, upstream.ErrContextAmbiguous), errors.Is(err, location.ErrContextAmbiguous):
		writeAPIError(writer, requestID, http.StatusConflict, "STRUCTURED_CONTEXT_AMBIGUOUS", "配置上下文不唯一", nil)
	case errors.Is(err, upstream.ErrInvalid):
		writeAPIError(writer, requestID, http.StatusUnprocessableEntity, "UPSTREAM_INVALID", "Upstream 配置无效", nil)
	case errors.Is(err, upstream.ErrDuplicate):
		writeAPIError(writer, requestID, http.StatusConflict, "UPSTREAM_DUPLICATE", "Upstream 名称不唯一", nil)
	case errors.Is(err, upstream.ErrReferenced):
		writeAPIError(writer, requestID, http.StatusConflict, "UPSTREAM_REFERENCED", "Upstream 仍被引用", nil)
	case errors.Is(err, upstream.ErrReferenceIncomplete):
		writeAPIError(writer, requestID, http.StatusConflict, "UPSTREAM_REFERENCE_INCOMPLETE", "Upstream 引用分析不完整", nil)
	case errors.Is(err, location.ErrInvalid):
		writeAPIError(writer, requestID, http.StatusUnprocessableEntity, "LOCATION_INVALID", "Location 配置无效", nil)
	case errors.Is(err, location.ErrDuplicate):
		writeAPIError(writer, requestID, http.StatusConflict, "LOCATION_DUPLICATE", "Location 规则重复", nil)
	case errors.Is(err, location.ErrProxyPassInvalid):
		writeAPIError(writer, requestID, http.StatusUnprocessableEntity, "PROXY_PASS_INVALID", "proxy_pass 选择无效", nil)
	default:
		writeConfigAPIError(writer, request, err, nil)
	}
}
