/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

package routelab

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/kuroky/nginx-uix/internal/nginxast"
)

const (
	maximumAnalyzedServers   = 1_000
	maximumAnalyzedLocations = 5_000
)

type serverModel struct {
	reference *nginxast.NodeRef
	block     *nginxast.Block
	routeID   string
	listeners []ListenerFact
	names     []string
	locations []*locationModel
}

type locationModel struct {
	reference *nginxast.NodeRef
	block     *nginxast.Block
	routeID   string
	parentID  string
	typeName  MatcherType
	matcher   string
	children  []*locationModel
}

type nameMatch struct {
	matched        bool
	indeterminate  bool
	rank           int
	length         int
	order          int
	reason         CandidateReason
	uncertainRank  int
	uncertainOrder int
}

// Analyze produces a conservative static explanation for one immutable project.
func Analyze(project *nginxast.Project, input StaticRequest) (Analysis, error) {
	request, err := validateStaticRequest(input)
	if err != nil {
		return Analysis{}, err
	}
	models, complete, err := buildServerModels(project)
	if err != nil {
		return Analysis{}, err
	}
	analysis := Analysis{Complete: complete, NormalizedURI: request.URI}
	analysis.Servers = make([]ServerCandidate, len(models))
	for index, model := range models {
		analysis.Servers[index] = ServerCandidate{
			RouteID: model.routeID, Source: sourceLocation(model.reference.Path, model.block.Span),
			Listeners: slices.Clone(model.listeners), ServerNames: slices.Clone(model.names),
			Disposition: DispositionExcluded, Reason: ReasonListenerMismatch,
		}
	}

	selected, tlsSelected, selectionComplete, err := selectServer(models, request, analysis.Servers)
	if err != nil {
		return Analysis{}, err
	}
	analysis.Complete = analysis.Complete && selectionComplete
	if tlsSelected != nil {
		analysis.PredictedTLSServerRouteID = tlsSelected.routeID
	}
	if selected == nil {
		return analysis, nil
	}
	analysis.PredictedServerRouteID = selected.routeID
	mergeSlashes, settingComplete := mergeSlashesForServer(project, selected)
	normalizedURI, normalizationComplete := normalizeURIForMatching(request.URI, mergeSlashes)
	analysis.NormalizedURI = normalizedURI
	uriComplete := settingComplete && normalizationComplete
	locations, selectedLocation, locationComplete := analyzeLocations(selected, analysis.NormalizedURI)
	analysis.Locations = locations
	analysis.Complete = analysis.Complete && locationComplete && uriComplete
	switch {
	case !uriComplete:
		markLocationNormalizationIndeterminate(analysis.Locations)
	case selectedLocation != "":
		analysis.PredictedLocationRouteID = selectedLocation
	case locationComplete:
		analysis.PredictedLocationRouteID = selected.routeID
	}
	analysis.RuntimeRedirectPossible = routeMayRedirect(project, selected.reference.ID)
	return analysis, nil
}

func mergeSlashesForServer(project *nginxast.Project, server *serverModel) (bool, bool) {
	if project == nil || server == nil || server.reference == nil {
		return false, false
	}
	httpParent := ""
	for _, placement := range server.reference.Placements {
		if placement.Context != nginxast.ContextHTTP {
			continue
		}
		if httpParent != "" && httpParent != placement.ParentID {
			return false, false
		}
		httpParent = placement.ParentID
	}
	if httpParent == "" {
		return false, false
	}
	enabled := true
	complete := true
	for _, scope := range []struct {
		parent  string
		context nginxast.ContextKind
	}{{parent: httpParent, context: nginxast.ContextHTTP}, {parent: server.reference.ID, context: nginxast.ContextServer}} {
		seen := false
		for _, child := range directChildren(project, scope.parent, scope.context) {
			directive, ok := child.Node.(*nginxast.Directive)
			if !ok || directive.Name.Value != "merge_slashes" {
				continue
			}
			if seen || len(directive.Arguments) != 1 {
				complete = false
				continue
			}
			seen = true
			switch directive.Arguments[0].Value {
			case "on":
				enabled = true
			case "off":
				enabled = false
			default:
				enabled = false
				complete = false
			}
		}
	}
	return enabled, complete
}

func markLocationNormalizationIndeterminate(candidates []LocationCandidate) {
	for index := range candidates {
		if candidates[index].Disposition != DispositionSelected && candidates[index].Disposition != DispositionMatched {
			continue
		}
		candidates[index].Disposition = DispositionIndeterminate
		candidates[index].Reason = ReasonLocationURIIndeterminate
	}
}

// BuildRouteDefinitions returns the stable instrumentable server/location identity map.
func BuildRouteDefinitions(project *nginxast.Project) ([]RouteDefinition, error) {
	models, complete, err := buildServerModels(project)
	if err != nil {
		return nil, err
	}
	if !complete {
		return nil, ErrProjectIncomplete
	}
	definitions := make([]RouteDefinition, 0)
	for _, server := range models {
		definitions = append(definitions, RouteDefinition{
			RouteID: server.routeID, NodeID: server.reference.ID, Kind: RouteServer,
			Source: sourceLocation(server.reference.Path, server.block.Span),
		})
		appendLocationDefinitions(&definitions, server.locations)
	}
	return definitions, nil
}

// ProjectMayContactUpstream conservatively detects directives that can open a non-loopback application connection.
func ProjectMayContactUpstream(project *nginxast.Project) bool {
	if project == nil {
		return false
	}
	for _, reference := range project.Nodes {
		directive, ok := reference.Node.(*nginxast.Directive)
		if !ok {
			continue
		}
		switch directive.Name.Value {
		case "proxy_pass", "fastcgi_pass", "grpc_pass", "memcached_pass", "scgi_pass", "uwsgi_pass":
			return true
		}
	}
	return false
}

func appendLocationDefinitions(target *[]RouteDefinition, locations []*locationModel) {
	for _, location := range locations {
		*target = append(*target, RouteDefinition{
			RouteID: location.routeID, NodeID: location.reference.ID, ParentRouteID: location.parentID,
			Kind: RouteLocation, MatcherType: location.typeName, Matcher: location.matcher,
			Source: sourceLocation(location.reference.Path, location.block.Span),
		})
		appendLocationDefinitions(target, location.children)
	}
}

func buildServerModels(project *nginxast.Project) ([]*serverModel, bool, error) {
	if project == nil {
		return nil, false, ErrProjectIncomplete
	}
	complete := project.Complete
	models := make([]*serverModel, 0)
	locationCount := 0
	for _, reference := range project.Nodes {
		block, ok := reference.Node.(*nginxast.Block)
		if !ok || block.Name.Value != "server" || !hasPlacement(reference, nginxast.ContextHTTP) {
			continue
		}
		if len(models) >= maximumAnalyzedServers {
			return nil, false, ErrLimitExceeded
		}
		model := &serverModel{reference: reference, block: block, routeID: stableRouteID("srv", reference, block)}
		if reference.Ambiguous || reference.Instances != 1 || len(block.Arguments) != 0 {
			complete = false
		}
		for _, child := range directChildren(project, reference.ID, nginxast.ContextServer) {
			directive, ok := child.Node.(*nginxast.Directive)
			if !ok {
				continue
			}
			switch directive.Name.Value {
			case "listen":
				listener := parseListener(directive)
				model.listeners = append(model.listeners, listener)
				complete = complete && listener.Supported
			case "server_name":
				for _, argument := range directive.Arguments {
					model.names = append(model.names, argument.Value)
				}
			}
		}
		if len(model.listeners) == 0 {
			model.listeners = []ListenerFact{{Address: "*", Port: 80, Derived: true, Supported: true}}
		}
		if len(model.names) == 0 {
			model.names = []string{""}
		}
		model.locations = buildLocationModels(project, reference.ID, model.routeID, nginxast.ContextServer, 0, &locationCount, &complete)
		if locationCount > maximumAnalyzedLocations {
			return nil, false, ErrLimitExceeded
		}
		models = append(models, model)
	}
	return models, complete, nil
}

func buildLocationModels(
	project *nginxast.Project,
	parentNodeID string,
	parentRouteID string,
	context nginxast.ContextKind,
	depth int,
	count *int,
	complete *bool,
) []*locationModel {
	models := make([]*locationModel, 0)
	for _, reference := range directChildren(project, parentNodeID, context) {
		block, ok := reference.Node.(*nginxast.Block)
		if !ok || block.Name.Value != "location" {
			continue
		}
		(*count)++
		matcherType, matcher, valid := parseLocationMatcher(block.Arguments)
		model := &locationModel{
			reference: reference, block: block, routeID: stableRouteID("loc", reference, block),
			parentID: parentRouteID, typeName: matcherType, matcher: matcher,
		}
		if !valid || reference.Ambiguous || reference.Instances != 1 || depth > 128 {
			*complete = false
		}
		model.children = buildLocationModels(
			project, reference.ID, model.routeID, nginxast.ContextLocation, depth+1, count, complete,
		)
		models = append(models, model)
	}
	return models
}

func selectServer(
	models []*serverModel,
	request StaticRequest,
	candidates []ServerCandidate,
) (*serverModel, *serverModel, bool, error) {
	addresses := make(map[string]struct{})
	eligible := make([]int, 0)
	for index, model := range models {
		matched := false
		for _, listener := range model.listeners {
			if listener.Port != request.Port || listener.SSL != (request.Scheme == SchemeHTTPS) {
				continue
			}
			if !listener.Supported {
				candidates[index].Disposition = DispositionIndeterminate
				candidates[index].Reason = ReasonListenerUnsupported
				continue
			}
			matched = true
			addresses[listener.Address] = struct{}{}
		}
		if matched {
			eligible = append(eligible, index)
		}
	}
	if len(addresses) > 1 {
		return nil, nil, false, ErrListenerAmbiguous
	}
	if len(eligible) == 0 {
		return nil, nil, true, nil
	}

	selectedIndex, matches, selectionComplete, selectionCertain := selectServerName(models, eligible, request.Host)
	if !selectionCertain {
		selectedIndex = -1
	}
	if selectedIndex < 0 && selectionCertain {
		selectedIndex = defaultServerIndex(models, eligible)
		candidates[selectedIndex].Reason = ReasonListenerDefault
	}
	for _, index := range eligible {
		if index == selectedIndex {
			candidates[index].Disposition = DispositionSelected
			if candidates[index].Reason == ReasonListenerMismatch {
				match := bestServerNameMatch(models[index].names, request.Host, 0)
				candidates[index].Reason = match.reason
			}
			continue
		}
		match := matches[index]
		switch {
		case candidates[index].Disposition == DispositionIndeterminate:
			continue
		case match.indeterminate:
			candidates[index].Disposition = DispositionIndeterminate
			candidates[index].Reason = ReasonServerNameIndeterminate
		case selectedIndex < 0 && match.matched:
			candidates[index].Disposition = DispositionMatched
			candidates[index].Reason = match.reason
		default:
			candidates[index].Disposition = DispositionExcluded
			candidates[index].Reason = ReasonServerNameLowerPriority
		}
	}

	var tlsSelected *serverModel
	if request.Scheme == SchemeHTTPS && request.SNI != "" {
		tlsIndex, _, tlsComplete, tlsCertain := selectServerName(models, eligible, request.SNI)
		selectionComplete = selectionComplete && tlsComplete
		if !tlsCertain {
			tlsIndex = -1
		}
		if tlsIndex < 0 && tlsCertain {
			tlsIndex = defaultServerIndex(models, eligible)
		}
		if tlsIndex >= 0 {
			tlsSelected = models[tlsIndex]
		}
	}
	if selectedIndex < 0 {
		return nil, tlsSelected, selectionComplete, nil
	}
	return models[selectedIndex], tlsSelected, selectionComplete, nil
}

func selectServerName(
	models []*serverModel,
	eligible []int,
	host string,
) (int, map[int]nameMatch, bool, bool) {
	selected := -1
	best := emptyNameMatch()
	complete := true
	regexOrder := 0
	matches := make(map[int]nameMatch, len(eligible))
	for _, index := range eligible {
		match := bestServerNameMatch(models[index].names, host, regexOrder)
		matches[index] = match
		regexOrder += len(models[index].names)
		complete = complete && !match.indeterminate
		mergeNameUncertainty(&best, match)
		if !match.matched {
			continue
		}
		if selected < 0 || betterNameMatch(match, best) {
			uncertainRank, uncertainOrder := best.uncertainRank, best.uncertainOrder
			indeterminate := best.indeterminate
			selected = index
			best = match
			best.indeterminate = best.indeterminate || indeterminate
			if uncertainRank < best.uncertainRank || uncertainRank == best.uncertainRank && uncertainOrder < best.uncertainOrder {
				best.uncertainRank = uncertainRank
				best.uncertainOrder = uncertainOrder
			}
		}
	}
	return selected, matches, complete, !nameUncertaintyCanPreempt(best)
}

func bestServerNameMatch(names []string, host string, orderBase int) nameMatch {
	best := emptyNameMatch()
	for index, raw := range names {
		name := strings.ToLower(strings.TrimSuffix(raw, "."))
		match := emptyNameMatch()
		match.order = orderBase + index
		switch {
		case strings.HasPrefix(name, "~"):
			pattern := strings.TrimSpace(strings.TrimPrefix(raw, "~"))
			expression, err := regexp.Compile(pattern)
			if err != nil {
				match.indeterminate = true
				match.reason = ReasonServerNameIndeterminate
				match.uncertainRank = 4
				match.uncertainOrder = match.order
				break
			}
			match.matched = expression.MatchString(host)
			match.rank = 4
			match.reason = ReasonServerNameRegex
		case strings.HasPrefix(name, "*."):
			base := strings.TrimPrefix(name, "*.")
			match.matched = host != base && strings.HasSuffix(host, "."+base)
			match.rank = 2
			match.length = len(base)
			match.reason = ReasonServerNameLeadingWildcard
		case strings.HasPrefix(name, "."):
			base := strings.TrimPrefix(name, ".")
			match.matched = host == base || strings.HasSuffix(host, "."+base)
			match.rank = 2
			match.length = len(base)
			match.reason = ReasonServerNameLeadingWildcard
		case strings.HasSuffix(name, ".*"):
			base := strings.TrimSuffix(name, "*")
			match.matched = strings.HasPrefix(host, base) && len(host) > len(base)
			match.rank = 3
			match.length = len(base)
			match.reason = ReasonServerNameTrailingWildcard
		case strings.Contains(name, "$"):
			match.indeterminate = true
			match.reason = ReasonServerNameIndeterminate
			match.uncertainRank = 1
			match.uncertainOrder = match.order
		default:
			match.matched = name == host
			match.rank = 1
			match.length = len(name)
			match.reason = ReasonServerNameExact
		}
		mergeNameUncertainty(&best, match)
		if match.matched && (!best.matched || betterNameMatch(match, best)) {
			indeterminate := best.indeterminate
			uncertainRank, uncertainOrder := best.uncertainRank, best.uncertainOrder
			best = match
			best.indeterminate = best.indeterminate || indeterminate
			if uncertainRank < best.uncertainRank || uncertainRank == best.uncertainRank && uncertainOrder < best.uncertainOrder {
				best.uncertainRank = uncertainRank
				best.uncertainOrder = uncertainOrder
			}
		}
	}
	return best
}

func emptyNameMatch() nameMatch {
	return nameMatch{rank: 100, order: 1 << 30, uncertainRank: 100, uncertainOrder: 1 << 30}
}

func mergeNameUncertainty(target *nameMatch, source nameMatch) {
	if target == nil || !source.indeterminate {
		return
	}
	target.indeterminate = true
	if source.uncertainRank < target.uncertainRank ||
		source.uncertainRank == target.uncertainRank && source.uncertainOrder < target.uncertainOrder {
		target.uncertainRank = source.uncertainRank
		target.uncertainOrder = source.uncertainOrder
	}
}

func nameUncertaintyCanPreempt(match nameMatch) bool {
	if !match.indeterminate {
		return false
	}
	if !match.matched || match.uncertainRank < match.rank {
		return true
	}
	if match.uncertainRank > match.rank {
		return false
	}
	if match.rank == 4 {
		return match.uncertainOrder < match.order
	}
	return true
}

func betterNameMatch(left, right nameMatch) bool {
	return left.rank < right.rank || left.rank == right.rank &&
		(left.length > right.length || left.length == right.length && left.order < right.order)
}

func defaultServerIndex(models []*serverModel, eligible []int) int {
	for _, index := range eligible {
		for _, listener := range models[index].listeners {
			if listener.DefaultServer {
				return index
			}
		}
	}
	return eligible[0]
}

func analyzeLocations(server *serverModel, uri string) ([]LocationCandidate, string, bool) {
	candidates := make([]LocationCandidate, 0)
	indexes := make(map[string]int)
	flattenLocations(server.locations, 0, &candidates, indexes)
	selected, complete, _ := selectLocationLevel(server.locations, uri, candidates, indexes)
	if selected != nil {
		candidate := &candidates[indexes[selected.routeID]]
		candidate.Disposition = DispositionSelected
		return candidates, selected.routeID, complete
	}
	return candidates, "", complete
}

func flattenLocations(
	locations []*locationModel,
	depth int,
	target *[]LocationCandidate,
	indexes map[string]int,
) {
	for _, location := range locations {
		indexes[location.routeID] = len(*target)
		*target = append(*target, LocationCandidate{
			RouteID: location.routeID, ParentRouteID: location.parentID,
			Source: sourceLocation(location.reference.Path, location.block.Span), Type: location.typeName,
			Matcher: location.matcher, Depth: depth, Disposition: DispositionExcluded,
			Reason: ReasonLocationParentNotSelected,
		})
		flattenLocations(location.children, depth+1, target, indexes)
	}
}

func selectLocationLevel(
	locations []*locationModel,
	uri string,
	candidates []LocationCandidate,
	indexes map[string]int,
) (*locationModel, bool, bool) {
	complete := true
	var exact *locationModel
	var longest *locationModel
	unknownMatcher := false
	for _, location := range locations {
		candidate := &candidates[indexes[location.routeID]]
		switch location.typeName {
		case MatcherExact:
			if uri == location.matcher {
				exact = location
				candidate.Disposition = DispositionMatched
				candidate.Reason = ReasonLocationExact
			} else {
				candidate.Reason = ReasonLocationPrefixNoMatch
			}
		case MatcherPrefix, MatcherPrefixPriority:
			if strings.HasPrefix(uri, location.matcher) {
				candidate.Disposition = DispositionMatched
				candidate.Reason = ReasonLocationLongestPrefix
				if longest == nil || len(location.matcher) > len(longest.matcher) {
					if longest != nil {
						previous := &candidates[indexes[longest.routeID]]
						previous.Disposition = DispositionExcluded
						previous.Reason = ReasonLocationShorterPrefix
					}
					longest = location
				} else {
					candidate.Disposition = DispositionExcluded
					candidate.Reason = ReasonLocationShorterPrefix
				}
			} else {
				candidate.Reason = ReasonLocationPrefixNoMatch
			}
		case MatcherNamed:
			candidate.Reason = ReasonLocationNamedInitial
		case MatcherUnknown:
			candidate.Disposition = DispositionIndeterminate
			candidate.Reason = ReasonLocationRegexIndeterminate
			complete = false
			unknownMatcher = true
		case MatcherRegex, MatcherRegexInsensitive:
			// Regular expressions are evaluated in source order after the longest literal prefix is known.
		}
	}
	if exact != nil {
		return exact, complete, true
	}
	if unknownMatcher {
		return nil, false, false
	}

	var nestedSelection *locationModel
	nestedTerminal := false
	if longest != nil && len(longest.children) > 0 {
		nested, nestedComplete, terminal := selectLocationLevel(longest.children, uri, candidates, indexes)
		complete = complete && nestedComplete
		if nested != nil {
			parent := &candidates[indexes[longest.routeID]]
			parent.Disposition = DispositionMatched
			parent.Reason = ReasonLocationParentMatched
			nestedSelection = nested
			nestedTerminal = terminal
		} else if !nestedComplete {
			return nil, false, false
		}
	}
	if nestedTerminal {
		return nestedSelection, complete, true
	}
	if longest != nil && longest.typeName == MatcherPrefixPriority {
		candidate := &candidates[indexes[longest.routeID]]
		candidate.Reason = ReasonLocationPrefixPriority
		if nestedSelection != nil {
			return nestedSelection, complete, false
		}
		return longest, complete, false
	}

	var selectedRegex *locationModel
	blockingIndeterminate := false
	for _, location := range locations {
		if location.typeName != MatcherRegex && location.typeName != MatcherRegexInsensitive {
			continue
		}
		candidate := &candidates[indexes[location.routeID]]
		pattern := location.matcher
		if location.typeName == MatcherRegexInsensitive {
			pattern = "(?i:" + pattern + ")"
		}
		expression, err := regexp.Compile(pattern)
		if err != nil {
			candidate.Disposition = DispositionIndeterminate
			candidate.Reason = ReasonLocationRegexIndeterminate
			complete = false
			blockingIndeterminate = blockingIndeterminate || selectedRegex == nil
			continue
		}
		if expression.MatchString(uri) {
			if selectedRegex == nil {
				candidate.Disposition = DispositionMatched
				candidate.Reason = ReasonLocationRegex
				selectedRegex = location
			} else {
				candidate.Disposition = DispositionExcluded
				candidate.Reason = ReasonLocationEarlierRegex
			}
			continue
		}
		candidate.Reason = ReasonLocationRegexNoMatch
	}
	if blockingIndeterminate {
		return nil, false, false
	}
	if selectedRegex != nil {
		if len(selectedRegex.children) > 0 {
			nested, nestedComplete, _ := selectLocationLevel(selectedRegex.children, uri, candidates, indexes)
			complete = complete && nestedComplete
			if nested != nil {
				parent := &candidates[indexes[selectedRegex.routeID]]
				parent.Disposition = DispositionMatched
				parent.Reason = ReasonLocationParentMatched
				return nested, complete, true
			}
			if !nestedComplete {
				return nil, false, false
			}
		}
		return selectedRegex, complete, true
	}
	if nestedSelection != nil {
		return nestedSelection, complete, false
	}
	if longest != nil {
		candidate := &candidates[indexes[longest.routeID]]
		candidate.Reason = ReasonLocationLongestPrefix
		return longest, complete, false
	}
	return nil, complete, false
}

func parseListener(directive *nginxast.Directive) ListenerFact {
	listener := ListenerFact{Address: "*", Supported: true}
	if directive == nil || len(directive.Arguments) == 0 {
		listener.Supported = false
		return listener
	}
	endpoint := directive.Arguments[0].Value
	if port, err := strconv.Atoi(endpoint); err == nil {
		listener.Port = port
	} else {
		host, portText, err := net.SplitHostPort(endpoint)
		if err != nil {
			listener.Supported = false
			return listener
		}
		port, err := strconv.Atoi(portText)
		if err != nil {
			listener.Supported = false
			return listener
		}
		listener.Address = normalizeListenerAddress(host)
		listener.Port = port
	}
	if listener.Port <= 0 || listener.Port > 65535 {
		listener.Supported = false
	}
	for _, argument := range directive.Arguments[1:] {
		value := argument.Value
		switch {
		case value == "default_server" || value == "default":
			listener.DefaultServer = true
		case value == "ssl":
			listener.SSL = true
		case value == "http2", value == "bind", value == "reuseport", value == "deferred",
			strings.HasPrefix(value, "backlog="), strings.HasPrefix(value, "rcvbuf="),
			strings.HasPrefix(value, "sndbuf="), strings.HasPrefix(value, "so_keepalive="):
		case value == "proxy_protocol" || value == "quic" || strings.Contains(value, "$"),
			strings.HasPrefix(value, "setfib="), strings.HasPrefix(value, "accept_filter="),
			strings.HasPrefix(value, "ipv6only="):
			listener.Supported = false
		default:
			listener.Supported = false
		}
	}
	return listener
}

func normalizeListenerAddress(value string) string {
	value = strings.Trim(strings.ToLower(value), "[]")
	if value == "" || value == "*" || value == "0.0.0.0" || value == "::" {
		return "*"
	}
	return value
}

func parseLocationMatcher(arguments []nginxast.Argument) (MatcherType, string, bool) {
	if len(arguments) == 1 {
		value := arguments[0].Value
		if strings.HasPrefix(value, "@") {
			return MatcherNamed, value, len(value) > 1
		}
		return MatcherPrefix, value, strings.HasPrefix(value, "/")
	}
	if len(arguments) != 2 {
		return MatcherUnknown, "", false
	}
	switch arguments[0].Value {
	case "=":
		return MatcherExact, arguments[1].Value, strings.HasPrefix(arguments[1].Value, "/")
	case "^~":
		return MatcherPrefixPriority, arguments[1].Value, strings.HasPrefix(arguments[1].Value, "/")
	case "~":
		return MatcherRegex, arguments[1].Value, arguments[1].Value != ""
	case "~*":
		return MatcherRegexInsensitive, arguments[1].Value, arguments[1].Value != ""
	default:
		return MatcherUnknown, "", false
	}
}

func directChildren(project *nginxast.Project, parentID string, context nginxast.ContextKind) []*nginxast.NodeRef {
	children := make([]*nginxast.NodeRef, 0)
	if project == nil {
		return children
	}
	for _, reference := range project.Nodes {
		for _, placement := range reference.Placements {
			if placement.ParentID == parentID && placement.Context == context {
				children = append(children, reference)
				break
			}
		}
	}
	return children
}

func hasPlacement(reference *nginxast.NodeRef, context nginxast.ContextKind) bool {
	for _, placement := range reference.Placements {
		if placement.Context == context {
			return true
		}
	}
	return false
}

func stableRouteID(prefix string, reference *nginxast.NodeRef, block *nginxast.Block) string {
	hash := sha256.New()
	writeIdentity(hash, "route-v1")
	writeIdentity(hash, prefix)
	writeIdentity(hash, reference.Path)
	writeIdentity(hash, reference.ID)
	for _, argument := range block.Arguments {
		writeIdentity(hash, argument.Raw)
	}
	sum := hash.Sum(nil)
	return fmt.Sprintf("%s_%x", prefix, sum[:16])
}

func writeIdentity(target interface{ Write([]byte) (int, error) }, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = target.Write(size[:])
	_, _ = target.Write([]byte(value))
}

func sourceLocation(sourcePath string, span nginxast.Span) SourceLocation {
	return SourceLocation{
		Path: sourcePath, StartLine: span.Start.Line, StartColumn: span.Start.Column,
		EndLine: span.End.Line, EndColumn: span.End.Column,
	}
}

func normalizeURIForMatching(value string, mergeSlashes bool) (string, bool) {
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return value, false
	}
	if !mergeSlashes {
		if strings.Contains(decoded, "/./") || strings.HasSuffix(decoded, "/.") ||
			strings.Contains(decoded, "/../") || strings.HasSuffix(decoded, "/..") {
			return value, false
		}
		return decoded, true
	}
	trailingSlash := strings.HasSuffix(decoded, "/")
	normalized := path.Clean(decoded)
	if !strings.HasPrefix(normalized, "/") {
		normalized = "/" + normalized
	}
	if trailingSlash && normalized != "/" {
		normalized += "/"
	}
	return normalized, true
}

func routeMayRedirect(project *nginxast.Project, serverNodeID string) bool {
	if project == nil {
		return false
	}
	interesting := map[string]struct{}{"rewrite": {}, "try_files": {}, "error_page": {}, "index": {}}
	for _, reference := range project.Nodes {
		directive, ok := reference.Node.(*nginxast.Directive)
		if !ok {
			continue
		}
		if _, exists := interesting[directive.Name.Value]; !exists {
			continue
		}
		for _, placement := range reference.Placements {
			if placement.ParentID == serverNodeID || descendantOf(project, placement.ParentID, serverNodeID) {
				return true
			}
		}
	}
	return false
}

func descendantOf(project *nginxast.Project, nodeID, ancestorID string) bool {
	visited := make(map[string]bool)
	for nodeID != "" && !visited[nodeID] {
		if nodeID == ancestorID {
			return true
		}
		visited[nodeID] = true
		parent := ""
		for _, reference := range project.Nodes {
			if reference.ID != nodeID || len(reference.Placements) == 0 {
				continue
			}
			parent = reference.Placements[0].ParentID
			break
		}
		nodeID = parent
	}
	return false
}
