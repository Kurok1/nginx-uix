/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package upstream

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kuroky/nginx-uix/internal/nginxast"
)

var (
	// ErrInvalid indicates invalid structured upstream input or an unsafe resulting invariant.
	ErrInvalid = errors.New("upstream invalid")
	// ErrDuplicate indicates a non-unique upstream name.
	ErrDuplicate = errors.New("upstream duplicate")
	// ErrReferenced indicates that direct proxy_pass references prevent deletion.
	ErrReferenced = errors.New("upstream referenced")
	// ErrReferenceIncomplete indicates that dynamic or unknown references prevent safe rename or deletion.
	ErrReferenceIncomplete = errors.New("upstream reference analysis incomplete")
	// ErrContextAmbiguous indicates that a target has no unique writable semantic context.
	ErrContextAmbiguous = errors.New("upstream context ambiguous")
	// ErrEditConflict indicates that trusted semantic syntax cannot be mapped back to one local source span.
	ErrEditConflict = errors.New("upstream edit conflict")
)

// Plan is a deterministic set of lossless source edits for one upstream operation.
type Plan struct {
	Kind     string
	TargetID string
	Edits    []nginxast.SourceEdit
}

// ServerInput contains the supported editable fields for one upstream server directive.
type ServerInput struct {
	Endpoint    Endpoint
	Weight      *int
	Backup      bool
	Down        bool
	MaxFails    *int
	FailTimeout *string
}

// CreateInput creates an upstream beneath one unique HTTP block.
type CreateInput struct {
	HTTPBlockID string
	Name        string
	Servers     []ServerInput
}

// RenameInput renames one upstream and all statically resolved direct references.
type RenameInput struct {
	UpstreamID string
	NewName    string
}

// DeleteInput deletes one unreferenced upstream after exact name confirmation.
type DeleteInput struct {
	UpstreamID  string
	ConfirmName string
}

// CreateServerInput appends one server directive to an upstream.
type CreateServerInput struct {
	UpstreamID string
	Server     ServerInput
}

// UpdateServerInput replaces the supported fields of one server while preserving unknown parameters.
type UpdateServerInput struct {
	UpstreamID string
	ServerID   string
	Server     ServerInput
}

// DeleteServerInput deletes one server without removing the last usable peer.
type DeleteServerInput struct {
	UpstreamID string
	ServerID   string
}

// PlanCreate validates and locally renders one new upstream block.
func PlanCreate(project *nginxast.Project, catalog Catalog, input CreateInput) (Plan, error) {
	if project == nil || !project.Complete || !validUpstreamName(input.Name) || len(input.Servers) == 0 {
		return Plan{}, ErrInvalid
	}
	if upstreamNameExists(catalog, input.Name, "") {
		return Plan{}, ErrDuplicate
	}
	reference := findReference(project, input.HTTPBlockID)
	block, ok := nodeBlock(reference, "http")
	if !ok || reference.Ambiguous || reference.Instances != 1 || len(block.Arguments) != 0 ||
		!hasPlacement(reference, nginxast.ContextMain, "") {
		return Plan{}, ErrContextAmbiguous
	}
	lines := make([]string, 0, len(input.Servers)+2)
	lines = append(lines, "upstream "+input.Name+" {")
	hasUsablePeer := false
	for _, server := range input.Servers {
		directive, err := renderServerDirective(server, nil)
		if err != nil {
			return Plan{}, err
		}
		hasUsablePeer = hasUsablePeer || !server.Down
		lines = append(lines, "    "+directive)
	}
	if !hasUsablePeer {
		return Plan{}, ErrInvalid
	}
	lines = append(lines, "}")
	document := project.Documents[reference.Path].Document
	edit, err := document.AppendToBlock(block, strings.Join(lines, "\n"))
	if err != nil {
		return Plan{}, fmt.Errorf("%w: append upstream", ErrEditConflict)
	}
	return Plan{
		Kind: "upstream.create", TargetID: input.HTTPBlockID,
		Edits: []nginxast.SourceEdit{{Path: reference.Path, Edit: edit}},
	}, nil
}

// PlanRename validates reference completeness and changes only name-bearing source spans.
func PlanRename(project *nginxast.Project, catalog Catalog, input RenameInput) (Plan, error) {
	if project == nil || !project.Complete || !validUpstreamName(input.NewName) {
		return Plan{}, ErrInvalid
	}
	if !catalog.ReferenceAnalysisComplete {
		return Plan{}, ErrReferenceIncomplete
	}
	group, ok := findUpstream(catalog, input.UpstreamID)
	if !ok || group.Name == input.NewName {
		return Plan{}, ErrInvalid
	}
	if !group.Editable {
		if group.ReadOnlyReason == "duplicate_name" {
			return Plan{}, ErrDuplicate
		}
		return Plan{}, ErrContextAmbiguous
	}
	if upstreamNameExists(catalog, input.NewName, group.ID) {
		return Plan{}, ErrDuplicate
	}
	reference := findReference(project, group.ID)
	block, ok := nodeBlock(reference, "upstream")
	if !ok || len(block.Arguments) != 1 {
		return Plan{}, ErrEditConflict
	}
	edits := []nginxast.SourceEdit{{
		Path: reference.Path,
		Edit: nginxast.Edit{
			Span:        block.Arguments[0].Span,
			Replacement: replaceArgumentValue(block.Arguments[0].Raw, input.NewName),
		},
	}}
	for _, proxyReference := range group.References {
		sourceEdit, err := proxyHostEdit(project, proxyReference, group.Name, input.NewName)
		if err != nil {
			return Plan{}, err
		}
		edits = append(edits, sourceEdit)
	}
	return Plan{Kind: "upstream.rename", TargetID: group.ID, Edits: edits}, nil
}

// PlanDelete validates confirmation and reference safety before removing one block.
func PlanDelete(project *nginxast.Project, catalog Catalog, input DeleteInput) (Plan, error) {
	if project == nil || !project.Complete {
		return Plan{}, ErrInvalid
	}
	if !catalog.ReferenceAnalysisComplete {
		return Plan{}, ErrReferenceIncomplete
	}
	group, ok := findUpstream(catalog, input.UpstreamID)
	if !ok || input.ConfirmName != group.Name {
		return Plan{}, ErrInvalid
	}
	if !group.Editable {
		if group.ReadOnlyReason == "duplicate_name" {
			return Plan{}, ErrDuplicate
		}
		return Plan{}, ErrContextAmbiguous
	}
	if len(group.References) != 0 {
		return Plan{}, ErrReferenced
	}
	reference := findReference(project, group.ID)
	block, ok := nodeBlock(reference, "upstream")
	if !ok {
		return Plan{}, ErrEditConflict
	}
	document := project.Documents[reference.Path].Document
	span, err := document.StatementDeleteSpan(block)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: delete upstream", ErrEditConflict)
	}
	return Plan{
		Kind: "upstream.delete", TargetID: group.ID,
		Edits: []nginxast.SourceEdit{{Path: reference.Path, Edit: nginxast.Edit{Span: span}}},
	}, nil
}

// PlanCreateServer appends one validated server directive.
func PlanCreateServer(project *nginxast.Project, catalog Catalog, input CreateServerInput) (Plan, error) {
	group, ok := findUpstream(catalog, input.UpstreamID)
	if project == nil || !project.Complete || !ok {
		return Plan{}, ErrInvalid
	}
	if !group.Editable {
		return Plan{}, ErrContextAmbiguous
	}
	directive, err := renderServerDirective(input.Server, nil)
	if err != nil {
		return Plan{}, err
	}
	if input.Server.Down && usablePeerCount(group, "") == 0 {
		return Plan{}, ErrInvalid
	}
	reference := findReference(project, group.ID)
	block, ok := nodeBlock(reference, "upstream")
	if !ok {
		return Plan{}, ErrEditConflict
	}
	edit, err := project.Documents[reference.Path].Document.AppendToBlock(block, directive)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: append upstream server", ErrEditConflict)
	}
	return Plan{
		Kind: "upstream_server.create", TargetID: group.ID,
		Edits: []nginxast.SourceEdit{{Path: reference.Path, Edit: edit}},
	}, nil
}

// PlanUpdateServer rewrites one directive while retaining all unrecognized parameters byte-for-byte.
func PlanUpdateServer(project *nginxast.Project, catalog Catalog, input UpdateServerInput) (Plan, error) {
	group, server, ok := findServer(catalog, input.UpstreamID, input.ServerID)
	if project == nil || !project.Complete || !ok {
		return Plan{}, ErrInvalid
	}
	if !group.Editable || !server.Editable {
		return Plan{}, ErrContextAmbiguous
	}
	directive, err := renderServerDirective(input.Server, server.PreservedParameters)
	if err != nil {
		return Plan{}, err
	}
	if input.Server.Down && usablePeerCount(group, server.ID) == 0 {
		return Plan{}, ErrInvalid
	}
	reference := findReference(project, server.ID)
	if reference == nil {
		return Plan{}, ErrEditConflict
	}
	node, ok := reference.Node.(*nginxast.Directive)
	if !ok || node.Name.Value != "server" {
		return Plan{}, ErrEditConflict
	}
	return Plan{
		Kind: "upstream_server.update", TargetID: server.ID,
		Edits: []nginxast.SourceEdit{{
			Path: reference.Path,
			Edit: nginxast.Edit{Span: node.Span, Replacement: directive},
		}},
	}, nil
}

// PlanDeleteServer removes one peer only when another known usable peer remains.
func PlanDeleteServer(project *nginxast.Project, catalog Catalog, input DeleteServerInput) (Plan, error) {
	group, server, ok := findServer(catalog, input.UpstreamID, input.ServerID)
	if project == nil || !project.Complete || !ok {
		return Plan{}, ErrInvalid
	}
	if !group.Editable || !server.Editable {
		return Plan{}, ErrContextAmbiguous
	}
	if usablePeerCount(group, server.ID) == 0 {
		return Plan{}, ErrInvalid
	}
	reference := findReference(project, server.ID)
	if reference == nil {
		return Plan{}, ErrEditConflict
	}
	node, ok := reference.Node.(*nginxast.Directive)
	if !ok || node.Name.Value != "server" {
		return Plan{}, ErrEditConflict
	}
	span, err := project.Documents[reference.Path].Document.StatementDeleteSpan(node)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: delete upstream server", ErrEditConflict)
	}
	return Plan{
		Kind: "upstream_server.delete", TargetID: server.ID,
		Edits: []nginxast.SourceEdit{{Path: reference.Path, Edit: nginxast.Edit{Span: span}}},
	}, nil
}

func usablePeerCount(group Upstream, exceptID string) int {
	count := 0
	for _, candidate := range group.Servers {
		if candidate.ID != exceptID && candidate.Endpoint.Address != "" && !candidate.Down {
			count++
		}
	}
	return count
}

func renderServerDirective(input ServerInput, preserved []PreservedParameter) (string, error) {
	endpoint, err := renderEndpoint(input.Endpoint)
	if err != nil || input.Weight != nil && *input.Weight <= 0 ||
		input.MaxFails != nil && *input.MaxFails < 0 || input.Backup && input.Down ||
		input.FailTimeout != nil && (len(*input.FailTimeout) > 64 || !validTimeLiteral(*input.FailTimeout)) {
		return "", ErrInvalid
	}
	arguments := []string{endpoint}
	if input.Weight != nil {
		arguments = append(arguments, "weight="+strconv.Itoa(*input.Weight))
	}
	if input.MaxFails != nil {
		arguments = append(arguments, "max_fails="+strconv.Itoa(*input.MaxFails))
	}
	if input.FailTimeout != nil {
		arguments = append(arguments, "fail_timeout="+*input.FailTimeout)
	}
	if input.Backup {
		arguments = append(arguments, "backup")
	}
	if input.Down {
		arguments = append(arguments, "down")
	}
	for _, parameter := range preserved {
		if parameter.Raw == "" {
			return "", ErrEditConflict
		}
		arguments = append(arguments, parameter.Raw)
	}
	return "server " + strings.Join(arguments, " ") + ";", nil
}

func renderEndpoint(endpoint Endpoint) (string, error) {
	if endpoint.Address == "" || !utf8.ValidString(endpoint.Address) ||
		endpoint.Port != nil && *endpoint.Port == 0 {
		return "", ErrInvalid
	}
	if endpoint.Unix {
		if endpoint.Port != nil || !strings.HasPrefix(endpoint.Address, "/") ||
			!safeEndpointText(endpoint.Address, 1024) {
			return "", ErrInvalid
		}
		return "unix:" + endpoint.Address, nil
	}
	if !safeEndpointText(endpoint.Address, 255) {
		return "", ErrInvalid
	}
	if parsed := net.ParseIP(endpoint.Address); parsed != nil {
		if strings.Contains(endpoint.Address, ":") {
			if endpoint.Port != nil {
				return "[" + endpoint.Address + "]:" + strconv.FormatUint(uint64(*endpoint.Port), 10), nil
			}
			return "[" + endpoint.Address + "]", nil
		}
		if endpoint.Port != nil {
			return endpoint.Address + ":" + strconv.FormatUint(uint64(*endpoint.Port), 10), nil
		}
		return endpoint.Address, nil
	}
	if !validHostname(endpoint.Address) {
		return "", ErrInvalid
	}
	if endpoint.Port != nil {
		return endpoint.Address + ":" + strconv.FormatUint(uint64(*endpoint.Port), 10), nil
	}
	return endpoint.Address, nil
}

func validUpstreamName(value string) bool {
	if len(value) == 0 || len(value) > 255 || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if !(unicode.IsLetter(character) || unicode.IsDigit(character) ||
			character == '_' || character == '-' || character == '.') {
			return false
		}
	}
	return true
}

func safeEndpointText(value string, maximum int) bool {
	if len(value) == 0 || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) ||
			strings.ContainsRune("\"'$;{}#\\", character) {
			return false
		}
	}
	return true
}

func validHostname(value string) bool {
	if len(value) > 253 || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func upstreamNameExists(catalog Catalog, name string, exceptID string) bool {
	for _, group := range catalog.Upstreams {
		if group.ID != exceptID && group.Name == name {
			return true
		}
	}
	return false
}

func findUpstream(catalog Catalog, id string) (Upstream, bool) {
	for _, group := range catalog.Upstreams {
		if group.ID == id {
			return group, true
		}
	}
	return Upstream{}, false
}

func findServer(catalog Catalog, upstreamID string, serverID string) (Upstream, Server, bool) {
	group, exists := findUpstream(catalog, upstreamID)
	if !exists {
		return Upstream{}, Server{}, false
	}
	for _, server := range group.Servers {
		if server.ID == serverID {
			return group, server, true
		}
	}
	return Upstream{}, Server{}, false
}

func findReference(project *nginxast.Project, id string) *nginxast.NodeRef {
	if project == nil {
		return nil
	}
	for _, reference := range project.Nodes {
		if reference.ID == id {
			return reference
		}
	}
	return nil
}

func nodeBlock(reference *nginxast.NodeRef, name string) (*nginxast.Block, bool) {
	if reference == nil {
		return nil, false
	}
	block, ok := reference.Node.(*nginxast.Block)
	return block, ok && block.Name.Value == name
}

func hasPlacement(reference *nginxast.NodeRef, context nginxast.ContextKind, parentID string) bool {
	if reference == nil || len(reference.Placements) != 1 {
		return false
	}
	return reference.Placements[0].Context == context && reference.Placements[0].ParentID == parentID
}

func replaceArgumentValue(raw string, value string) string {
	if len(raw) >= 2 && (raw[0] == '\'' || raw[0] == '"') && raw[len(raw)-1] == raw[0] {
		return string(raw[0]) + value + string(raw[0])
	}
	return value
}

func proxyHostEdit(
	project *nginxast.Project,
	proxyReference Reference,
	oldName string,
	newName string,
) (nginxast.SourceEdit, error) {
	reference := findReference(project, proxyReference.ID)
	if reference == nil || reference.Ambiguous || reference.Instances != 1 {
		return nginxast.SourceEdit{}, ErrEditConflict
	}
	directive, ok := reference.Node.(*nginxast.Directive)
	if !ok || directive.Name.Value != "proxy_pass" || len(directive.Arguments) != 1 {
		return nginxast.SourceEdit{}, ErrEditConflict
	}
	raw := directive.Arguments[0].Raw
	marker := strings.Index(raw, "://")
	if marker < 0 {
		return nginxast.SourceEdit{}, ErrEditConflict
	}
	start := marker + len("://")
	end := start
	for end < len(raw) && !strings.ContainsRune(":/?#'\"", rune(raw[end])) {
		end++
	}
	if raw[start:end] != oldName {
		return nginxast.SourceEdit{}, ErrEditConflict
	}
	span := nginxast.Span{
		Start: nginxast.Position{Offset: directive.Arguments[0].Span.Start.Offset + start},
		End:   nginxast.Position{Offset: directive.Arguments[0].Span.Start.Offset + end},
	}
	return nginxast.SourceEdit{
		Path: reference.Path,
		Edit: nginxast.Edit{Span: span, Replacement: newName},
	}, nil
}
