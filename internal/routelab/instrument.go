/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

package routelab

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/kuroky/nginx-uix/internal/nginxast"
)

// ListenerKey binds one original listener group to a reserved sandbox port.
type ListenerKey struct {
	Address string
	Port    int
	SSL     bool
}

// InstrumentOptions contains only trusted stage facts and validated request semantics.
type InstrumentOptions struct {
	Prefix        string
	RunToken      string
	Request       StaticRequest
	ListenerPorts map[ListenerKey]int
}

// Instrumentation is the complete set of changed candidate files and evidence identities.
type Instrumentation struct {
	Files             map[string]string
	Routes            []RouteDefinition
	TargetPort        int
	ServerVariable    string
	RouteVariable     string
	LogFormat         string
	AccessLogPath     string
	ErrorLogPath      string
	PIDPath           string
	ListenerPortByKey map[ListenerKey]int
}

type listenerDirective struct {
	reference *nginxast.NodeRef
	directive *nginxast.Directive
	fact      ListenerFact
	http2     bool
}

// Instrument rewrites only an immutable candidate project into a sandbox-specific source set.
func Instrument(project *nginxast.Project, options InstrumentOptions) (Instrumentation, error) {
	if project == nil || !project.Complete {
		return Instrumentation{}, ErrProjectIncomplete
	}
	request, err := validateStaticRequest(options.Request)
	if err != nil {
		return Instrumentation{}, err
	}
	if err := validateInstrumentOptions(options); err != nil {
		return Instrumentation{}, err
	}
	servers, complete, err := buildServerModels(project)
	if err != nil {
		return Instrumentation{}, err
	}
	if !complete || len(servers) == 0 {
		return Instrumentation{}, ErrProjectIncomplete
	}
	routes, err := BuildRouteDefinitions(project)
	if err != nil {
		return Instrumentation{}, err
	}
	httpReference, httpBlock, err := uniqueHTTPBlock(project)
	if err != nil {
		return Instrumentation{}, err
	}
	targetKey, err := targetListenerKey(servers, request)
	if err != nil {
		return Instrumentation{}, err
	}
	targetPort, exists := options.ListenerPorts[targetKey]
	if !exists || targetPort <= 0 || targetPort > 65535 {
		return Instrumentation{}, ErrInvalidInstrumentation
	}

	shortToken := options.RunToken[:12]
	serverVariable := "nginx_uix_server_" + shortToken
	routeVariable := "nginx_uix_route_" + shortToken
	logFormat := "nginx_uix_route_" + shortToken
	if projectContainsVariable(project, serverVariable) || projectContainsVariable(project, routeVariable) ||
		projectContainsLogFormat(project, logFormat) {
		return Instrumentation{}, ErrInvalidInstrumentation
	}

	pidPath := filepath.Join(options.Prefix, "nginx.pid")
	errorLogPath := filepath.Join(options.Prefix, "logs", "error.log")
	accessLogPath := filepath.Join(options.Prefix, "logs", "access.log")
	edits := make([]nginxast.SourceEdit, 0, len(routes)*2+32)
	if err := appendGlobalDirectiveDeletes(project, &edits); err != nil {
		return Instrumentation{}, err
	}
	root, exists := project.Documents["nginx.conf"]
	if !exists || root.Document == nil {
		return Instrumentation{}, ErrProjectIncomplete
	}
	rootPosition := mainInsertionPosition(root.Document)
	mainText := strings.Join([]string{
		"daemon off;",
		"master_process on;",
		"pid " + quoteNginx(pidPath) + ";",
		"error_log " + quoteNginx(errorLogPath) + " notice;",
	}, "\n") + "\n"
	edits = append(edits, nginxast.SourceEdit{
		Path: "nginx.conf",
		Edit: nginxast.Edit{
			Span: nginxast.Span{Start: rootPosition, End: rootPosition}, Replacement: mainText,
		},
	})

	logPayload := fmt.Sprintf(
		"'{\"test\":\"$http_x_nginx_uix_test_id\",\"server\":\"$%s\",\"route\":\"$%s\",\"uri\":\"$uri\",\"status\":\"$status\",\"upstream\":\"$upstream_addr\",\"upstream_status\":\"$upstream_status\",\"request_time\":\"$request_time\"}'",
		serverVariable,
		routeVariable,
	)
	httpLines := []string{
		"client_body_temp_path " + quoteNginx(filepath.Join(options.Prefix, "temp", "client")) + ";",
		"proxy_temp_path " + quoteNginx(filepath.Join(options.Prefix, "temp", "proxy")) + ";",
		"fastcgi_temp_path " + quoteNginx(filepath.Join(options.Prefix, "temp", "fastcgi")) + ";",
		"uwsgi_temp_path " + quoteNginx(filepath.Join(options.Prefix, "temp", "uwsgi")) + ";",
		"scgi_temp_path " + quoteNginx(filepath.Join(options.Prefix, "temp", "scgi")) + ";",
		"log_format " + logFormat + " escape=json " + logPayload + ";",
		"access_log " + quoteNginx(accessLogPath) + " " + logFormat + ";",
		"log_subrequest off;",
	}
	httpEdit, err := blockPrefixEdit(project, httpReference.Path, httpBlock, httpLines)
	if err != nil {
		return Instrumentation{}, err
	}
	edits = append(edits, nginxast.SourceEdit{Path: httpReference.Path, Edit: httpEdit})

	for _, server := range servers {
		lines, listenerEdits, err := instrumentServerListeners(project, server, options.ListenerPorts)
		if err != nil {
			return Instrumentation{}, err
		}
		edits = append(edits, listenerEdits...)
		lines = append(lines,
			"set $"+serverVariable+" "+server.routeID+";",
			"set $"+routeVariable+" "+server.routeID+";",
		)
		edit, err := blockPrefixEdit(project, server.reference.Path, server.block, lines)
		if err != nil {
			return Instrumentation{}, err
		}
		edits = append(edits, nginxast.SourceEdit{Path: server.reference.Path, Edit: edit})
		if err := appendLocationMarkerEdits(project, server.locations, routeVariable, &edits); err != nil {
			return Instrumentation{}, err
		}
	}

	rendered, err := project.ApplyEdits(edits)
	if err != nil {
		return Instrumentation{}, fmt.Errorf("instrument route project: %w", err)
	}
	for sourcePath, content := range rendered {
		if _, err := nginxast.Parse(content); err != nil {
			return Instrumentation{}, fmt.Errorf("reparse instrumented route source %s: %w", sourcePath, err)
		}
	}
	return Instrumentation{
		Files: rendered, Routes: routes, TargetPort: targetPort,
		ServerVariable: serverVariable, RouteVariable: routeVariable, LogFormat: logFormat,
		AccessLogPath: accessLogPath, ErrorLogPath: errorLogPath, PIDPath: pidPath,
		ListenerPortByKey: cloneListenerPorts(options.ListenerPorts),
	}, nil
}

func validateInstrumentOptions(options InstrumentOptions) error {
	if options.Prefix == "" || !filepath.IsAbs(options.Prefix) || filepath.Clean(options.Prefix) != options.Prefix ||
		strings.ContainsAny(options.Prefix, "\x00\r\n") {
		return ErrInvalidInstrumentation
	}
	if len(options.RunToken) != 32 {
		return ErrInvalidInstrumentation
	}
	for _, character := range options.RunToken {
		if character >= '0' && character <= '9' || character >= 'a' && character <= 'f' {
			continue
		}
		return ErrInvalidInstrumentation
	}
	for key, port := range options.ListenerPorts {
		if key.Address == "" || key.Port <= 0 || key.Port > 65535 || port <= 0 || port > 65535 {
			return ErrInvalidInstrumentation
		}
	}
	return nil
}

func uniqueHTTPBlock(project *nginxast.Project) (*nginxast.NodeRef, *nginxast.Block, error) {
	var selectedReference *nginxast.NodeRef
	var selectedBlock *nginxast.Block
	for _, reference := range project.Nodes {
		block, ok := reference.Node.(*nginxast.Block)
		if !ok || block.Name.Value != "http" || !hasPlacement(reference, nginxast.ContextMain) {
			continue
		}
		if selectedBlock != nil || reference.Ambiguous || reference.Instances != 1 || len(block.Arguments) != 0 {
			return nil, nil, ErrProjectIncomplete
		}
		selectedReference = reference
		selectedBlock = block
	}
	if selectedBlock == nil {
		return nil, nil, ErrProjectIncomplete
	}
	return selectedReference, selectedBlock, nil
}

func targetListenerKey(servers []*serverModel, request StaticRequest) (ListenerKey, error) {
	addresses := make(map[string]struct{})
	for _, server := range servers {
		for _, listener := range server.listeners {
			if listener.Port != request.Port || listener.SSL != (request.Scheme == SchemeHTTPS) {
				continue
			}
			if !listener.Supported {
				return ListenerKey{}, ErrProjectIncomplete
			}
			addresses[listener.Address] = struct{}{}
		}
	}
	if len(addresses) == 0 {
		return ListenerKey{}, ErrInvalidInstrumentation
	}
	if len(addresses) > 1 {
		return ListenerKey{}, ErrListenerAmbiguous
	}
	address := ""
	for value := range addresses {
		address = value
	}
	return ListenerKey{Address: address, Port: request.Port, SSL: request.Scheme == SchemeHTTPS}, nil
}

func appendGlobalDirectiveDeletes(project *nginxast.Project, edits *[]nginxast.SourceEdit) error {
	for _, reference := range project.Nodes {
		directive, ok := reference.Node.(*nginxast.Directive)
		if !ok {
			continue
		}
		if !sandboxReplacedDirective(directive.Name.Value) {
			continue
		}
		parsed := project.Documents[reference.Path]
		if parsed.Document == nil {
			return ErrProjectIncomplete
		}
		span, err := parsed.Document.StatementDeleteSpan(directive)
		if err != nil {
			return err
		}
		*edits = append(*edits, nginxast.SourceEdit{
			Path: reference.Path, Edit: nginxast.Edit{Span: span, Replacement: ""},
		})
	}
	return nil
}

func mainInsertionPosition(document *nginxast.Document) nginxast.Position {
	for _, node := range document.Statements {
		directive, ok := node.(*nginxast.Directive)
		if ok && sandboxReplacedDirective(directive.Name.Value) {
			continue
		}
		return node.SourceSpan().Start
	}
	if len(document.Tokens) > 0 {
		return document.Tokens[len(document.Tokens)-1].Span.Start
	}
	return nginxast.Position{Offset: 0, Line: 1, Column: 1}
}

func sandboxReplacedDirective(name string) bool {
	switch name {
	case "daemon", "master_process", "pid", "error_log", "access_log",
		"client_body_temp_path", "proxy_temp_path", "fastcgi_temp_path",
		"uwsgi_temp_path", "scgi_temp_path", "log_subrequest":
		return true
	default:
		return false
	}
}

func instrumentServerListeners(
	project *nginxast.Project,
	server *serverModel,
	ports map[ListenerKey]int,
) ([]string, []nginxast.SourceEdit, error) {
	directives := make([]listenerDirective, 0)
	for _, reference := range directChildren(project, server.reference.ID, nginxast.ContextServer) {
		directive, ok := reference.Node.(*nginxast.Directive)
		if !ok || directive.Name.Value != "listen" {
			continue
		}
		fact := parseListener(directive)
		if !fact.Supported {
			return nil, nil, ErrProjectIncomplete
		}
		directives = append(directives, listenerDirective{
			reference: reference, directive: directive, fact: fact, http2: hasArgument(directive, "http2"),
		})
	}
	if len(directives) == 0 {
		key := ListenerKey{Address: "*", Port: 80}
		port, exists := ports[key]
		if !exists {
			return nil, nil, ErrInvalidInstrumentation
		}
		return []string{"listen 127.0.0.1:" + strconv.Itoa(port) + ";"}, nil, nil
	}

	grouped := make(map[ListenerKey][]listenerDirective)
	order := make([]ListenerKey, 0)
	for _, item := range directives {
		key := ListenerKey{Address: item.fact.Address, Port: item.fact.Port, SSL: item.fact.SSL}
		if _, exists := grouped[key]; !exists {
			order = append(order, key)
		}
		grouped[key] = append(grouped[key], item)
	}
	edits := make([]nginxast.SourceEdit, 0, len(directives))
	for _, key := range order {
		port, exists := ports[key]
		if !exists {
			return nil, nil, ErrInvalidInstrumentation
		}
		items := grouped[key]
		defaultServer := false
		http2 := false
		for _, item := range items {
			defaultServer = defaultServer || item.fact.DefaultServer
			http2 = http2 || item.http2
		}
		replacement := "listen 127.0.0.1:" + strconv.Itoa(port)
		if defaultServer {
			replacement += " default_server"
		}
		if key.SSL {
			replacement += " ssl"
		}
		if http2 {
			replacement += " http2"
		}
		replacement += ";"
		edits = append(edits, nginxast.SourceEdit{
			Path: items[0].reference.Path,
			Edit: nginxast.Edit{Span: items[0].directive.Span, Replacement: replacement},
		})
		for _, duplicate := range items[1:] {
			document := project.Documents[duplicate.reference.Path].Document
			span, err := document.StatementDeleteSpan(duplicate.directive)
			if err != nil {
				return nil, nil, err
			}
			edits = append(edits, nginxast.SourceEdit{
				Path: duplicate.reference.Path, Edit: nginxast.Edit{Span: span, Replacement: ""},
			})
		}
	}
	return nil, edits, nil
}

func appendLocationMarkerEdits(
	project *nginxast.Project,
	locations []*locationModel,
	routeVariable string,
	edits *[]nginxast.SourceEdit,
) error {
	for _, location := range locations {
		edit, err := blockPrefixEdit(
			project,
			location.reference.Path,
			location.block,
			[]string{"set $" + routeVariable + " " + location.routeID + ";"},
		)
		if err != nil {
			return err
		}
		*edits = append(*edits, nginxast.SourceEdit{Path: location.reference.Path, Edit: edit})
		if err := appendLocationMarkerEdits(project, location.children, routeVariable, edits); err != nil {
			return err
		}
	}
	return nil
}

func blockPrefixEdit(
	project *nginxast.Project,
	sourcePath string,
	block *nginxast.Block,
	lines []string,
) (nginxast.Edit, error) {
	parsed, exists := project.Documents[sourcePath]
	if !exists || parsed.Document == nil || block == nil || len(lines) == 0 {
		return nginxast.Edit{}, ErrInvalidInstrumentation
	}
	source := parsed.Document.Render()
	lineEnding := "\n"
	if strings.Contains(source, "\r\n") {
		lineEnding = "\r\n"
	}
	indent := lineIndent(source, block.Name.Span.Start.Offset) + "    "
	if len(block.Children) > 0 {
		childIndent := lineIndent(source, block.Children[0].SourceSpan().Start.Offset)
		if childIndent != "" {
			indent = childIndent
		}
	}
	var replacement strings.Builder
	replacement.WriteString(lineEnding)
	for _, line := range lines {
		if line == "" || strings.ContainsAny(line, "\r\n") {
			return nginxast.Edit{}, ErrInvalidInstrumentation
		}
		replacement.WriteString(indent)
		replacement.WriteString(line)
		replacement.WriteString(lineEnding)
	}
	position := block.BodySpan.Start
	return nginxast.Edit{
		Span: nginxast.Span{Start: position, End: position}, Replacement: replacement.String(),
	}, nil
}

func lineIndent(source string, offset int) string {
	if offset < 0 || offset > len(source) {
		return ""
	}
	start := offset
	for start > 0 && source[start-1] != '\n' && source[start-1] != '\r' {
		start--
	}
	value := source[start:offset]
	for _, character := range value {
		if character != ' ' && character != '\t' {
			return ""
		}
	}
	return value
}

func quoteNginx(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return "\"" + value + "\""
}

func projectContainsVariable(project *nginxast.Project, variable string) bool {
	needle := "$" + variable
	for _, parsed := range project.Documents {
		if parsed.Document != nil && strings.Contains(parsed.Document.Render(), needle) {
			return true
		}
	}
	return false
}

func projectContainsLogFormat(project *nginxast.Project, format string) bool {
	for _, reference := range project.Nodes {
		directive, ok := reference.Node.(*nginxast.Directive)
		if !ok || directive.Name.Value != "log_format" || len(directive.Arguments) == 0 {
			continue
		}
		if directive.Arguments[0].Value == format {
			return true
		}
	}
	return false
}

func hasArgument(directive *nginxast.Directive, value string) bool {
	for _, argument := range directive.Arguments {
		if argument.Value == value {
			return true
		}
	}
	return false
}

func cloneListenerPorts(input map[ListenerKey]int) map[ListenerKey]int {
	output := make(map[ListenerKey]int, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

// ListenerKeys returns the stable distinct original listener groups requiring sandbox ports.
func ListenerKeys(project *nginxast.Project) ([]ListenerKey, error) {
	servers, complete, err := buildServerModels(project)
	if err != nil {
		return nil, err
	}
	if !complete {
		return nil, ErrProjectIncomplete
	}
	keys := make([]ListenerKey, 0)
	for _, server := range servers {
		for _, listener := range server.listeners {
			key := ListenerKey{Address: listener.Address, Port: listener.Port, SSL: listener.SSL}
			if !slices.Contains(keys, key) {
				keys = append(keys, key)
			}
		}
	}
	slices.SortFunc(keys, func(left, right ListenerKey) int {
		if compared := strings.Compare(left.Address, right.Address); compared != 0 {
			return compared
		}
		if left.Port != right.Port {
			return left.Port - right.Port
		}
		switch {
		case left.SSL == right.SSL:
			return 0
		case !left.SSL:
			return -1
		default:
			return 1
		}
	})
	return keys, nil
}
