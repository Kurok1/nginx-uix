/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package location

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kuroky/nginx-uix/internal/nginxast"
	"github.com/kuroky/nginx-uix/internal/upstream"
)

var (
	// ErrInvalid indicates invalid matcher input, nesting, confirmation, or resulting invariants.
	ErrInvalid = errors.New("location invalid")
	// ErrDuplicate indicates an identical sibling matcher.
	ErrDuplicate = errors.New("location duplicate")
	// ErrContextAmbiguous indicates that a target has no unique writable semantic context.
	ErrContextAmbiguous = errors.New("location context ambiguous")
	// ErrProxyPassInvalid indicates an unsafe or unresolvable structured proxy_pass selection.
	ErrProxyPassInvalid = errors.New("proxy_pass invalid")
	// ErrEditConflict indicates that trusted semantic syntax cannot be mapped to one local source span.
	ErrEditConflict = errors.New("location edit conflict")
)

// ProxyMode identifies how an update treats the direct proxy_pass directive.
type ProxyMode string

const (
	// ProxyPreserve leaves the current direct proxy_pass syntax untouched.
	ProxyPreserve ProxyMode = "preserve"
	// ProxySet creates or replaces one direct proxy_pass.
	ProxySet ProxyMode = "set"
	// ProxyRemove deletes one statically understood direct proxy_pass.
	ProxyRemove ProxyMode = "remove"
)

// ProxyPassInput selects a static upstream without accepting an arbitrary target URL.
type ProxyPassInput struct {
	UpstreamID string
	Scheme     string
	Port       *uint16
	URI        string
}

// CreateInput creates one location below a server or legal parent location.
type CreateInput struct {
	ParentID  string
	Type      MatcherType
	Matcher   string
	ProxyPass *ProxyPassInput
}

// UpdateInput changes a matcher and explicitly preserves, sets, or removes direct proxy_pass.
type UpdateInput struct {
	LocationID string
	Type       MatcherType
	Matcher    string
	ProxyMode  ProxyMode
	ProxyPass  *ProxyPassInput
}

// DeleteInput deletes one location after exact matcher confirmation.
type DeleteInput struct {
	LocationID     string
	ConfirmMatcher string
}

// Plan is a deterministic set of lossless source edits for one location operation.
type Plan struct {
	Kind     string
	TargetID string
	Edits    []nginxast.SourceEdit
}

// PlanCreate validates parent relationships and appends one location block.
func PlanCreate(
	project *nginxast.Project,
	catalog Catalog,
	upstreams upstream.Catalog,
	input CreateInput,
) (Plan, error) {
	if project == nil || !project.Complete || !validMatcher(input.Type, input.Matcher) {
		return Plan{}, ErrInvalid
	}
	parent, ok := findParent(catalog, input.ParentID)
	if !ok || !parent.Editable {
		return Plan{}, ErrContextAmbiguous
	}
	if err := validateRelationship(input.Type, input.Matcher, parent.Location, nil); err != nil {
		return Plan{}, err
	}
	if siblingDuplicate(parent.Children, input.Type, input.Matcher, "") {
		return Plan{}, ErrDuplicate
	}
	header, err := renderLocationHeader(input.Type, input.Matcher)
	if err != nil {
		return Plan{}, err
	}
	lines := []string{header + " {"}
	if input.ProxyPass != nil {
		proxyPass, err := renderProxyPass(upstreams, *input.ProxyPass, input.Type)
		if err != nil {
			return Plan{}, err
		}
		lines = append(lines, "    "+proxyPass)
	}
	lines = append(lines, "}")

	reference := findReference(project, parent.ID)
	block, ok := referenceBlock(reference)
	if !ok || (block.Name.Value != "server" && block.Name.Value != "location") {
		return Plan{}, ErrEditConflict
	}
	edit, err := project.Documents[reference.Path].Document.AppendToBlock(block, strings.Join(lines, "\n"))
	if err != nil {
		return Plan{}, fmt.Errorf("%w: append location", ErrEditConflict)
	}
	return Plan{
		Kind: "location.create", TargetID: parent.ID,
		Edits: []nginxast.SourceEdit{{Path: reference.Path, Edit: edit}},
	}, nil
}

// PlanUpdate validates the resulting tree and makes only explicit matcher and proxy_pass edits.
func PlanUpdate(
	project *nginxast.Project,
	catalog Catalog,
	upstreams upstream.Catalog,
	input UpdateInput,
) (Plan, error) {
	if project == nil || !project.Complete || !validMatcher(input.Type, input.Matcher) {
		return Plan{}, ErrInvalid
	}
	position, ok := findLocation(catalog, input.LocationID)
	if !ok || !position.Location.Editable {
		return Plan{}, ErrContextAmbiguous
	}
	if err := validateRelationship(
		input.Type, input.Matcher, position.Parent, position.Location.Children,
	); err != nil {
		return Plan{}, err
	}
	if siblingDuplicate(position.Siblings, input.Type, input.Matcher, position.Location.ID) {
		return Plan{}, ErrDuplicate
	}
	reference := findReference(project, position.Location.ID)
	block, ok := referenceBlock(reference)
	if !ok || block.Name.Value != "location" {
		return Plan{}, ErrEditConflict
	}

	edits := make([]nginxast.SourceEdit, 0, 2)
	if input.Type != position.Location.Type || input.Matcher != position.Location.Matcher {
		header, err := renderLocationHeader(input.Type, input.Matcher)
		if err != nil {
			return Plan{}, err
		}
		edits = append(edits, nginxast.SourceEdit{
			Path: reference.Path,
			Edit: nginxast.Edit{Span: block.HeaderSpan, Replacement: header},
		})
	}

	switch input.ProxyMode {
	case ProxyPreserve:
		if input.ProxyPass != nil {
			return Plan{}, ErrProxyPassInvalid
		}
		if proxyURIForbidden(input.Type) {
			for _, proxyPass := range position.Location.ProxyPasses {
				if proxyPass.URI != "" {
					return Plan{}, ErrProxyPassInvalid
				}
			}
		}
	case ProxySet:
		if input.ProxyPass == nil {
			return Plan{}, ErrProxyPassInvalid
		}
		directive, err := renderProxyPass(upstreams, *input.ProxyPass, input.Type)
		if err != nil {
			return Plan{}, err
		}
		switch len(position.Location.ProxyPasses) {
		case 0:
			edit, err := project.Documents[reference.Path].Document.AppendToBlock(block, directive)
			if err != nil {
				return Plan{}, fmt.Errorf("%w: append proxy_pass", ErrEditConflict)
			}
			edits = append(edits, nginxast.SourceEdit{Path: reference.Path, Edit: edit})
		case 1:
			if !position.Location.ProxyPassEditable {
				return Plan{}, ErrProxyPassInvalid
			}
			proxyReference := findReference(project, position.Location.ProxyPasses[0].ID)
			proxyDirective, ok := referenceDirective(proxyReference, "proxy_pass")
			if !ok {
				return Plan{}, ErrEditConflict
			}
			edits = append(edits, nginxast.SourceEdit{
				Path: proxyReference.Path,
				Edit: nginxast.Edit{Span: proxyDirective.Span, Replacement: directive},
			})
		default:
			return Plan{}, ErrProxyPassInvalid
		}
	case ProxyRemove:
		if input.ProxyPass != nil || len(position.Location.ProxyPasses) != 1 ||
			!position.Location.ProxyPassEditable {
			return Plan{}, ErrProxyPassInvalid
		}
		proxyReference := findReference(project, position.Location.ProxyPasses[0].ID)
		proxyDirective, ok := referenceDirective(proxyReference, "proxy_pass")
		if !ok {
			return Plan{}, ErrEditConflict
		}
		span, err := project.Documents[proxyReference.Path].Document.StatementDeleteSpan(proxyDirective)
		if err != nil {
			return Plan{}, fmt.Errorf("%w: delete proxy_pass", ErrEditConflict)
		}
		edits = append(edits, nginxast.SourceEdit{
			Path: proxyReference.Path, Edit: nginxast.Edit{Span: span},
		})
	default:
		return Plan{}, ErrInvalid
	}
	if len(edits) == 0 {
		return Plan{}, ErrInvalid
	}
	return Plan{Kind: "location.update", TargetID: position.Location.ID, Edits: edits}, nil
}

// PlanDelete validates exact confirmation and removes one complete location block.
func PlanDelete(project *nginxast.Project, catalog Catalog, input DeleteInput) (Plan, error) {
	position, ok := findLocation(catalog, input.LocationID)
	if project == nil || !project.Complete || !ok || input.ConfirmMatcher != position.Location.Matcher {
		return Plan{}, ErrInvalid
	}
	if !position.Location.Editable {
		return Plan{}, ErrContextAmbiguous
	}
	reference := findReference(project, position.Location.ID)
	block, ok := referenceBlock(reference)
	if !ok || block.Name.Value != "location" {
		return Plan{}, ErrEditConflict
	}
	span, err := project.Documents[reference.Path].Document.StatementDeleteSpan(block)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: delete location", ErrEditConflict)
	}
	return Plan{
		Kind: "location.delete", TargetID: position.Location.ID,
		Edits: []nginxast.SourceEdit{{Path: reference.Path, Edit: nginxast.Edit{Span: span}}},
	}, nil
}

type parentPosition struct {
	ID       string
	Editable bool
	Location *Location
	Children []Location
}

type locationPosition struct {
	Location Location
	Parent   *Location
	Siblings []Location
}

func findParent(catalog Catalog, id string) (parentPosition, bool) {
	for _, server := range catalog.Servers {
		if server.ID == id {
			return parentPosition{
				ID: server.ID, Editable: server.Editable, Children: server.Locations,
			}, true
		}
		if location, ok := findLocationTree(server.Locations, id, nil); ok {
			copy := location.Location
			return parentPosition{
				ID: copy.ID, Editable: copy.Editable, Location: &copy, Children: copy.Children,
			}, true
		}
	}
	return parentPosition{}, false
}

func findLocation(catalog Catalog, id string) (locationPosition, bool) {
	for _, server := range catalog.Servers {
		if position, ok := findLocationTree(server.Locations, id, nil); ok {
			return position, true
		}
	}
	return locationPosition{}, false
}

func findLocationTree(
	locations []Location,
	id string,
	parent *Location,
) (locationPosition, bool) {
	for index := range locations {
		location := locations[index]
		if location.ID == id {
			return locationPosition{Location: location, Parent: parent, Siblings: locations}, true
		}
		parentCopy := location
		if result, ok := findLocationTree(location.Children, id, &parentCopy); ok {
			return result, true
		}
	}
	return locationPosition{}, false
}

func validateRelationship(
	matcherType MatcherType,
	matcher string,
	parent *Location,
	children []Location,
) error {
	if (matcherType == MatcherExact || matcherType == MatcherNamed) && len(children) != 0 {
		return ErrInvalid
	}
	for _, child := range children {
		if child.Type == MatcherNamed {
			return ErrInvalid
		}
		if !isLiteral(child.Type) {
			continue
		}
		switch matcherType {
		case MatcherPrefix, MatcherPrefixPriority:
			if !strings.HasPrefix(child.Matcher, matcher) {
				return ErrInvalid
			}
		case MatcherRegex, MatcherRegexInsensitive, MatcherUnknown:
			return ErrInvalid
		}
	}
	if parent == nil {
		return nil
	}
	if parent.Type == MatcherExact || parent.Type == MatcherNamed || matcherType == MatcherNamed {
		return ErrInvalid
	}
	if !isLiteral(matcherType) {
		return nil
	}
	switch parent.Type {
	case MatcherPrefix, MatcherPrefixPriority:
		if !strings.HasPrefix(matcher, parent.Matcher) {
			return ErrInvalid
		}
	case MatcherRegex, MatcherRegexInsensitive, MatcherUnknown:
		return ErrInvalid
	}
	return nil
}

func siblingDuplicate(
	siblings []Location,
	matcherType MatcherType,
	matcher string,
	exceptID string,
) bool {
	for _, sibling := range siblings {
		if sibling.ID != exceptID && sibling.Type == matcherType && sibling.Matcher == matcher {
			return true
		}
	}
	return false
}

func validMatcher(matcherType MatcherType, matcher string) bool {
	switch matcherType {
	case MatcherExact, MatcherPrefix, MatcherPrefixPriority:
		return validLiteralMatcher(matcher)
	case MatcherRegex, MatcherRegexInsensitive:
		return validRegexMatcher(matcher)
	case MatcherNamed:
		return validNamedMatcher(matcher)
	default:
		return false
	}
}

func renderLocationHeader(matcherType MatcherType, matcher string) (string, error) {
	if !validMatcher(matcherType, matcher) {
		return "", ErrInvalid
	}
	argument := encodeNginxArgument(matcher)
	switch matcherType {
	case MatcherExact:
		return "location = " + argument, nil
	case MatcherPrefix:
		return "location " + argument, nil
	case MatcherPrefixPriority:
		return "location ^~ " + argument, nil
	case MatcherRegex:
		return "location ~ " + argument, nil
	case MatcherRegexInsensitive:
		return "location ~* " + argument, nil
	case MatcherNamed:
		return "location " + argument, nil
	default:
		return "", ErrInvalid
	}
}

func renderProxyPass(
	catalog upstream.Catalog,
	input ProxyPassInput,
	matcherType MatcherType,
) (string, error) {
	group, ok := selectedUpstream(catalog, input.UpstreamID)
	if !ok || !group.Editable || !safeUpstreamAuthority(group.Name) ||
		(input.Scheme != "http" && input.Scheme != "https") ||
		input.Port != nil && *input.Port == 0 {
		return "", ErrProxyPassInvalid
	}
	uri, err := canonicalProxyURI(input.URI)
	if err != nil || uri != "" && proxyURIForbidden(matcherType) {
		return "", ErrProxyPassInvalid
	}
	value := input.Scheme + "://" + group.Name
	if input.Port != nil {
		value += ":" + strconv.FormatUint(uint64(*input.Port), 10)
	}
	value += uri
	return "proxy_pass " + encodeNginxArgument(value) + ";", nil
}

func canonicalProxyURI(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if !utf8.ValidString(value) || !strings.HasPrefix(value, "/") ||
		strings.ContainsAny(value, "$\x00\r\n") {
		return "", ErrProxyPassInvalid
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return "", ErrProxyPassInvalid
		}
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Fragment != "" {
		return "", ErrProxyPassInvalid
	}
	result := parsed.EscapedPath()
	if parsed.RawQuery != "" {
		result += "?" + parsed.RawQuery
	}
	return result, nil
}

func proxyURIForbidden(matcherType MatcherType) bool {
	return matcherType == MatcherRegex || matcherType == MatcherRegexInsensitive || matcherType == MatcherNamed
}

func selectedUpstream(catalog upstream.Catalog, id string) (upstream.Upstream, bool) {
	for _, group := range catalog.Upstreams {
		if group.ID == id {
			return group, true
		}
	}
	return upstream.Upstream{}, false
}

func safeUpstreamAuthority(value string) bool {
	if len(value) == 0 || len(value) > 255 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' && character != '-' &&
			character != '.' {
			return false
		}
	}
	return true
}

func encodeNginxArgument(value string) string {
	safe := value != ""
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) ||
			strings.ContainsRune(";{}#\"'\\", character) {
			safe = false
			break
		}
	}
	if safe {
		return value
	}
	escaped := strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(value)
	return "\"" + escaped + "\""
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

func referenceBlock(reference *nginxast.NodeRef) (*nginxast.Block, bool) {
	if reference == nil || reference.Ambiguous || reference.Instances != 1 {
		return nil, false
	}
	block, ok := reference.Node.(*nginxast.Block)
	return block, ok
}

func referenceDirective(reference *nginxast.NodeRef, name string) (*nginxast.Directive, bool) {
	if reference == nil || reference.Ambiguous || reference.Instances != 1 {
		return nil, false
	}
	directive, ok := reference.Node.(*nginxast.Directive)
	return directive, ok && directive.Name.Value == name
}
