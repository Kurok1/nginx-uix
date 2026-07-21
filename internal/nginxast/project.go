/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package nginxast

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"unicode/utf8"
)

// IncludeStatus is the sanitized resolution state supplied by the workspace manifest.
type IncludeStatus string

const (
	// IncludeResolved identifies a managed regular target.
	IncludeResolved IncludeStatus = "resolved"
	// IncludeMissing identifies an absent internal target.
	IncludeMissing IncludeStatus = "missing"
	// IncludeExternal identifies a target outside the managed root.
	IncludeExternal IncludeStatus = "external"
	// IncludeUnresolved identifies an expression that cannot be resolved statically.
	IncludeUnresolved IncludeStatus = "unresolved"
	// IncludeSymlink identifies a target that the workspace deliberately does not follow.
	IncludeSymlink IncludeStatus = "symlink"
	// IncludeSpecial identifies a non-regular target.
	IncludeSpecial IncludeStatus = "special"
	// IncludeCycle identifies an edge back into the active include stack.
	IncludeCycle IncludeStatus = "cycle"
)

// SourceFile is one immutable managed source supplied by a caller.
type SourceFile struct {
	Path   string
	Source string
}

// IncludeEdge binds one include directive location to a sanitized target.
type IncludeEdge struct {
	Source string
	Line   int
	Column int
	Target string
	Status IncludeStatus
}

// ContextKind is the semantic context containing a physical syntax node.
type ContextKind string

const (
	// ContextMain is the root Nginx configuration context.
	ContextMain ContextKind = "main"
	// ContextHTTP is the body of an HTTP block.
	ContextHTTP ContextKind = "http"
	// ContextServer is the body of an HTTP server block.
	ContextServer ContextKind = "server"
	// ContextUpstream is the body of an HTTP upstream block.
	ContextUpstream ContextKind = "upstream"
	// ContextLocation is the body of an HTTP location block.
	ContextLocation ContextKind = "location"
	// ContextOther is a block whose module semantics are intentionally unknown.
	ContextOther ContextKind = "other"
)

// DiagnosticCode is a stable project construction result.
type DiagnosticCode string

const (
	// DiagnosticParseFailed indicates a managed file that is not syntactically parseable.
	DiagnosticParseFailed DiagnosticCode = "parse_failed"
	// DiagnosticIncludeUnbound indicates an include without a matching manifest edge.
	DiagnosticIncludeUnbound DiagnosticCode = "include_unbound"
	// DiagnosticIncludeMissing indicates a missing internal include target.
	DiagnosticIncludeMissing DiagnosticCode = "include_missing"
	// DiagnosticIncludeExternal indicates an external include target.
	DiagnosticIncludeExternal DiagnosticCode = "include_external"
	// DiagnosticIncludeUnresolved indicates a dynamic include expression.
	DiagnosticIncludeUnresolved DiagnosticCode = "include_unresolved"
	// DiagnosticIncludeSymlink indicates a read-only symlink include target.
	DiagnosticIncludeSymlink DiagnosticCode = "include_symlink"
	// DiagnosticIncludeSpecial indicates a special-file include target.
	DiagnosticIncludeSpecial DiagnosticCode = "include_special"
	// DiagnosticIncludeCycle indicates an include cycle.
	DiagnosticIncludeCycle DiagnosticCode = "include_cycle"
	// DiagnosticIncludeTargetUnavailable indicates a resolved edge without a parseable source.
	DiagnosticIncludeTargetUnavailable DiagnosticCode = "include_target_unavailable"
	// DiagnosticAmbiguousContext indicates one physical node with multiple semantic placements.
	DiagnosticAmbiguousContext DiagnosticCode = "ambiguous_context"
	// DiagnosticRootUnavailable indicates that nginx.conf is absent or unparseable.
	DiagnosticRootUnavailable DiagnosticCode = "root_unavailable"
)

// ProjectDiagnostic contains only safe relative source facts.
type ProjectDiagnostic struct {
	Code        DiagnosticCode
	Path        string
	Line        int
	Column      int
	RelatedPath string
}

// ProjectLimits bounds cross-file expansion.
type ProjectLimits struct {
	Syntax             Limits
	MaxIncludeDepth    int
	MaxContextsPerFile int
	MaxNodes           int
	MaxDiagnostics     int
}

// DefaultProjectLimits returns the v0.3 bounded project limits.
func DefaultProjectLimits() ProjectLimits {
	return ProjectLimits{
		Syntax: DefaultLimits(), MaxIncludeDepth: 64, MaxContextsPerFile: 64,
		MaxNodes: 10_000, MaxDiagnostics: 20_000,
	}
}

// ParsedSource retains either a document or its safe syntax error.
type ParsedSource struct {
	Document *Document
	Error    *SyntaxError
}

// Placement is one semantic occurrence of a physical syntax node.
type Placement struct {
	Context  ContextKind
	ParentID string
}

// NodeRef binds one physical syntax node to its semantic placements.
type NodeRef struct {
	ID         string
	Path       string
	Node       Node
	Placements []Placement
	Instances  int
	Ambiguous  bool
}

// Project is the bounded, include-expanded syntax projection rooted at nginx.conf.
type Project struct {
	Documents   map[string]ParsedSource
	Nodes       []*NodeRef
	Diagnostics []ProjectDiagnostic
	Complete    bool
}

// BuildProject parses managed sources and expands only manifest-proven include edges.
func BuildProject(files []SourceFile, edges []IncludeEdge, limits ProjectLimits) (*Project, error) {
	if !validProjectLimits(limits) {
		return nil, fmt.Errorf("build nginx AST project: invalid limits")
	}
	orderedFiles := slices.Clone(files)
	slices.SortFunc(orderedFiles, func(left, right SourceFile) int { return strings.Compare(left.Path, right.Path) })
	builder := &projectBuilder{
		limits:  limits,
		project: &Project{Documents: make(map[string]ParsedSource, len(files)), Complete: true},
		files:   make(map[string]SourceFile, len(files)),
		edges:   make(map[string][]IncludeEdge),
		refs:    make(map[string]*NodeRef),
		visits:  make(map[string]int),
	}
	for _, file := range orderedFiles {
		if !validSourcePath(file.Path) {
			return nil, fmt.Errorf("build nginx AST project: invalid source path")
		}
		if _, duplicate := builder.files[file.Path]; duplicate {
			return nil, fmt.Errorf("build nginx AST project: duplicate source path")
		}
		builder.files[file.Path] = file
		document, err := ParseWithLimits(file.Source, limits.Syntax)
		if err != nil {
			var syntaxError *SyntaxError
			if !errors.As(err, &syntaxError) {
				return nil, fmt.Errorf("build nginx AST project: parse source: %w", err)
			}
			builder.project.Documents[file.Path] = ParsedSource{Error: syntaxError}
			if err := builder.diagnostic(ProjectDiagnostic{
				Code: DiagnosticParseFailed, Path: file.Path,
				Line: syntaxError.Span.Start.Line, Column: syntaxError.Span.Start.Column,
			}); err != nil {
				return nil, err
			}
			continue
		}
		builder.project.Documents[file.Path] = ParsedSource{Document: document}
	}

	for _, edge := range edges {
		if !validSourcePath(edge.Source) || edge.Line <= 0 || edge.Column <= 0 ||
			(edge.Target != "" && !validSourcePath(edge.Target)) || !validIncludeStatus(edge.Status) {
			return nil, fmt.Errorf("build nginx AST project: invalid include edge")
		}
		key := includeEdgeKey(edge.Source, edge.Line, edge.Column)
		builder.edges[key] = append(builder.edges[key], edge)
	}
	for key := range builder.edges {
		slices.SortFunc(builder.edges[key], compareIncludeEdges)
	}

	root, exists := builder.project.Documents["nginx.conf"]
	if !exists || root.Document == nil {
		if err := builder.diagnostic(ProjectDiagnostic{Code: DiagnosticRootUnavailable, Path: "nginx.conf", Line: 1, Column: 1}); err != nil {
			return nil, err
		}
		return builder.project, nil
	}
	if err := builder.walkFile("nginx.conf", ContextMain, "", 0, map[string]bool{"nginx.conf": true}); err != nil {
		return nil, err
	}
	if err := builder.finishAmbiguity(); err != nil {
		return nil, err
	}
	return builder.project, nil
}

// NodeID returns the deterministic physical identity for a node under one relative path.
func NodeID(sourcePath string, node Node) string {
	if node == nil {
		return ""
	}
	digest := sha256.New()
	writeIdentityString(digest, "nginx-node-v1")
	writeIdentityString(digest, sourcePath)
	span := node.SourceSpan()
	var offsets [16]byte
	binary.BigEndian.PutUint64(offsets[:8], uint64(span.Start.Offset)) // #nosec G115 -- parser-produced source offsets are non-negative.
	binary.BigEndian.PutUint64(offsets[8:], uint64(span.End.Offset))   // #nosec G115 -- parser-produced source offsets are non-negative.
	_, _ = digest.Write(offsets[:])
	switch value := node.(type) {
	case *Directive:
		writeIdentityString(digest, "directive")
		writeIdentityString(digest, value.Name.Raw)
	case *Block:
		writeIdentityString(digest, "block")
		writeIdentityString(digest, value.Name.Raw)
	default:
		return ""
	}
	sum := digest.Sum(nil)
	return fmt.Sprintf("%x", sum[:16])
}

type projectBuilder struct {
	limits  ProjectLimits
	project *Project
	files   map[string]SourceFile
	edges   map[string][]IncludeEdge
	refs    map[string]*NodeRef
	visits  map[string]int
}

func (b *projectBuilder) walkFile(
	sourcePath string,
	context ContextKind,
	parentID string,
	depth int,
	active map[string]bool,
) error {
	if depth > b.limits.MaxIncludeDepth {
		return b.diagnostic(ProjectDiagnostic{Code: DiagnosticIncludeTargetUnavailable, Path: sourcePath, Line: 1, Column: 1})
	}
	b.visits[sourcePath]++
	if b.visits[sourcePath] > b.limits.MaxContextsPerFile {
		return b.diagnostic(ProjectDiagnostic{Code: DiagnosticIncludeTargetUnavailable, Path: sourcePath, Line: 1, Column: 1})
	}
	parsed, exists := b.project.Documents[sourcePath]
	if !exists || parsed.Document == nil {
		return b.diagnostic(ProjectDiagnostic{Code: DiagnosticIncludeTargetUnavailable, Path: sourcePath, Line: 1, Column: 1})
	}
	return b.walkNodes(sourcePath, parsed.Document.Statements, context, parentID, depth, active)
}

func (b *projectBuilder) walkNodes(
	sourcePath string,
	nodes []Node,
	context ContextKind,
	parentID string,
	depth int,
	active map[string]bool,
) error {
	for _, node := range nodes {
		reference, err := b.addNode(sourcePath, node, Placement{Context: context, ParentID: parentID})
		if err != nil {
			return err
		}
		switch value := node.(type) {
		case *Directive:
			if value.Name.Value == "include" {
				if err := b.expandInclude(sourcePath, value, context, parentID, depth, active); err != nil {
					return err
				}
			}
		case *Block:
			if err := b.walkNodes(sourcePath, value.Children, childContext(context, value), reference.ID, depth, active); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *projectBuilder) expandInclude(
	sourcePath string,
	directive *Directive,
	context ContextKind,
	parentID string,
	depth int,
	active map[string]bool,
) error {
	key := includeEdgeKey(sourcePath, directive.Name.Span.Start.Line, directive.Name.Span.Start.Column)
	edges := b.edges[key]
	if len(edges) == 0 {
		return b.diagnostic(ProjectDiagnostic{
			Code: DiagnosticIncludeUnbound, Path: sourcePath,
			Line: directive.Name.Span.Start.Line, Column: directive.Name.Span.Start.Column,
		})
	}
	for _, edge := range edges {
		if edge.Status != IncludeResolved {
			if err := b.diagnostic(ProjectDiagnostic{
				Code: diagnosticForIncludeStatus(edge.Status), Path: sourcePath,
				Line: edge.Line, Column: edge.Column, RelatedPath: edge.Target,
			}); err != nil {
				return err
			}
			continue
		}
		if active[edge.Target] {
			if err := b.diagnostic(ProjectDiagnostic{
				Code: DiagnosticIncludeCycle, Path: sourcePath,
				Line: edge.Line, Column: edge.Column, RelatedPath: edge.Target,
			}); err != nil {
				return err
			}
			continue
		}
		parsed, exists := b.project.Documents[edge.Target]
		if !exists || parsed.Document == nil {
			if err := b.diagnostic(ProjectDiagnostic{
				Code: DiagnosticIncludeTargetUnavailable, Path: sourcePath,
				Line: edge.Line, Column: edge.Column, RelatedPath: edge.Target,
			}); err != nil {
				return err
			}
			continue
		}
		nextActive := mapsClone(active)
		nextActive[edge.Target] = true
		if err := b.walkFile(edge.Target, context, parentID, depth+1, nextActive); err != nil {
			return err
		}
	}
	return nil
}

func (b *projectBuilder) addNode(sourcePath string, node Node, placement Placement) (*NodeRef, error) {
	id := NodeID(sourcePath, node)
	key := physicalNodeKey(sourcePath, node)
	reference, exists := b.refs[key]
	if !exists {
		if len(b.project.Nodes) >= b.limits.MaxNodes {
			return nil, fmt.Errorf("build nginx AST project: node limit exceeded: %w", ErrLimitExceeded)
		}
		reference = &NodeRef{ID: id, Path: sourcePath, Node: node}
		b.refs[key] = reference
		b.project.Nodes = append(b.project.Nodes, reference)
	}
	reference.Instances++
	if !slices.Contains(reference.Placements, placement) {
		reference.Placements = append(reference.Placements, placement)
	}
	return reference, nil
}

func (b *projectBuilder) finishAmbiguity() error {
	for _, reference := range b.project.Nodes {
		if len(reference.Placements) <= 1 {
			continue
		}
		reference.Ambiguous = true
		span := reference.Node.SourceSpan()
		if err := b.diagnostic(ProjectDiagnostic{
			Code: DiagnosticAmbiguousContext, Path: reference.Path,
			Line: span.Start.Line, Column: span.Start.Column,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (b *projectBuilder) diagnostic(diagnostic ProjectDiagnostic) error {
	if len(b.project.Diagnostics) >= b.limits.MaxDiagnostics {
		return fmt.Errorf("build nginx AST project: diagnostic limit exceeded: %w", ErrLimitExceeded)
	}
	b.project.Diagnostics = append(b.project.Diagnostics, diagnostic)
	b.project.Complete = false
	return nil
}

type nodeIdentityProjection struct {
	ID         string
	Path       string
	Placements []Placement
	Instances  int
	Ambiguous  bool
}

func (p *Project) safeIdentityProjection() []nodeIdentityProjection {
	if p == nil {
		return nil
	}
	projection := make([]nodeIdentityProjection, len(p.Nodes))
	for index, reference := range p.Nodes {
		projection[index] = nodeIdentityProjection{
			ID: reference.ID, Path: reference.Path, Placements: slices.Clone(reference.Placements),
			Instances: reference.Instances, Ambiguous: reference.Ambiguous,
		}
	}
	return projection
}

func childContext(parent ContextKind, block *Block) ContextKind {
	switch {
	case parent == ContextMain && block.Name.Value == "http":
		return ContextHTTP
	case parent == ContextHTTP && block.Name.Value == "server":
		return ContextServer
	case parent == ContextHTTP && block.Name.Value == "upstream":
		return ContextUpstream
	case (parent == ContextServer || parent == ContextLocation) && block.Name.Value == "location":
		return ContextLocation
	default:
		return ContextOther
	}
}

func diagnosticForIncludeStatus(status IncludeStatus) DiagnosticCode {
	switch status {
	case IncludeResolved:
		return DiagnosticIncludeTargetUnavailable
	case IncludeMissing:
		return DiagnosticIncludeMissing
	case IncludeExternal:
		return DiagnosticIncludeExternal
	case IncludeUnresolved:
		return DiagnosticIncludeUnresolved
	case IncludeSymlink:
		return DiagnosticIncludeSymlink
	case IncludeSpecial:
		return DiagnosticIncludeSpecial
	case IncludeCycle:
		return DiagnosticIncludeCycle
	default:
		return DiagnosticIncludeTargetUnavailable
	}
}

func validProjectLimits(limits ProjectLimits) bool {
	return validLimits(limits.Syntax) && limits.MaxIncludeDepth > 0 && limits.MaxContextsPerFile > 0 &&
		limits.MaxNodes > 0 && limits.MaxDiagnostics > 0
}

func validSourcePath(value string) bool {
	return value != "" && utf8.ValidString(value) && !strings.HasPrefix(value, "/") &&
		!strings.Contains(value, "\\") && path.Clean(value) == value && value != "."
}

func validIncludeStatus(status IncludeStatus) bool {
	switch status {
	case IncludeResolved, IncludeMissing, IncludeExternal, IncludeUnresolved, IncludeSymlink, IncludeSpecial, IncludeCycle:
		return true
	default:
		return false
	}
}

func includeEdgeKey(source string, line, column int) string {
	return fmt.Sprintf("%s\x00%d\x00%d", source, line, column)
}

func compareIncludeEdges(left, right IncludeEdge) int {
	if compared := strings.Compare(left.Target, right.Target); compared != 0 {
		return compared
	}
	return strings.Compare(string(left.Status), string(right.Status))
}

func physicalNodeKey(sourcePath string, node Node) string {
	span := node.SourceSpan()
	return fmt.Sprintf("%s\x00%d\x00%d\x00%T", sourcePath, span.Start.Offset, span.End.Offset, node)
}

func writeIdentityString(target interface{ Write([]byte) (int, error) }, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = target.Write(length[:])
	_, _ = target.Write([]byte(value))
}

func mapsClone(source map[string]bool) map[string]bool {
	cloned := make(map[string]bool, len(source)+1)
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
