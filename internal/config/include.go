/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
)

const nginxPrefix = "/etc/nginx"

const (
	// DependencyResolved indicates a regular manifest target.
	DependencyResolved DependencyStatus = "resolved"
	// DependencyMissing indicates a safe internal target absent from the manifest.
	DependencyMissing DependencyStatus = "missing"
	// DependencyExternal indicates a target outside the fixed Nginx prefix.
	DependencyExternal DependencyStatus = "external"
	// DependencyUnresolved indicates an expression that cannot be resolved lexically.
	DependencyUnresolved DependencyStatus = "unresolved"
	// DependencySymlink indicates a manifest symlink that is never followed.
	DependencySymlink DependencyStatus = "symlink"
	// DependencySpecial indicates a non-regular, non-symlink manifest target.
	DependencySpecial DependencyStatus = "special"
	// DependencyCycle indicates an edge back into the active include stack.
	DependencyCycle DependencyStatus = "cycle"
)

// Directive is one lexically scanned Nginx directive.
type Directive struct {
	Name      string
	Arguments []string
	Line      int
	Column    int
}

// IncludeGraph is the bounded dependency graph rooted at nginx.conf.
type IncludeGraph struct {
	Edges         []Dependency
	Cycles        [][]RelativePath
	MissingCount  int
	ExternalCount int
}

// Dependency describes one sanitized include dependency edge.
type Dependency struct {
	Source       RelativePath
	Line         int
	Column       int
	DisplayValue string
	Target       RelativePath
	Status       DependencyStatus
	Cycle        bool
}

// ScanDirectives lexically scans Nginx directives without interpreting them.
func ScanDirectives(content []byte, limits Limits) ([]Directive, error) {
	if limits.MaxIncludeTokenBytes <= 0 || limits.MaxIncludeDirectiveBytes <= 0 {
		return nil, fmt.Errorf("scan directives: %w", ErrLimitExceeded)
	}
	var (
		directives     []Directive
		words          []string
		word           []byte
		wordStarted    bool
		inComment      bool
		quote          byte
		escaped        bool
		tokenBytes     int
		directiveBytes int
		directiveOpen  bool
		directiveLine  int
		directiveCol   int
		line           = 1
		column         = 1
	)

	startWord := func() {
		if wordStarted {
			return
		}
		wordStarted = true
		tokenBytes = 0
		if len(words) == 0 {
			directiveLine = line
			directiveCol = column
			directiveBytes = 0
			directiveOpen = true
		}
	}
	finishWord := func() {
		if !wordStarted {
			return
		}
		words = append(words, string(word))
		word = word[:0]
		wordStarted = false
		tokenBytes = 0
	}
	finishDirective := func() {
		finishWord()
		if len(words) == 0 {
			return
		}
		directives = append(directives, Directive{
			Name:      words[0],
			Arguments: append([]string(nil), words[1:]...),
			Line:      directiveLine,
			Column:    directiveCol,
		})
		words = words[:0]
		directiveBytes = 0
		directiveOpen = false
	}
	advancePosition := func(current byte) {
		if current == '\n' {
			line++
			column = 1
			return
		}
		column++
	}
	countTokenByte := func() error {
		tokenBytes++
		if tokenBytes > limits.MaxIncludeTokenBytes {
			return fmt.Errorf("scan directives: token: %w", ErrLimitExceeded)
		}
		return nil
	}
	countDirectiveByte := func() error {
		directiveBytes++
		if directiveBytes > limits.MaxIncludeDirectiveBytes {
			return fmt.Errorf("scan directives: directive: %w", ErrLimitExceeded)
		}
		return nil
	}

	for _, current := range content {
		if inComment {
			if current == '\n' {
				inComment = false
			}
			advancePosition(current)
			continue
		}
		if directiveOpen {
			if err := countDirectiveByte(); err != nil {
				return nil, err
			}
		}
		if escaped {
			if err := countTokenByte(); err != nil {
				return nil, err
			}
			word = append(word, current)
			escaped = false
			advancePosition(current)
			continue
		}
		if quote != 0 {
			if err := countTokenByte(); err != nil {
				return nil, err
			}
			switch current {
			case '\\':
				escaped = true
			case quote:
				quote = 0
			default:
				word = append(word, current)
			}
			advancePosition(current)
			continue
		}

		switch current {
		case '#':
			finishWord()
			inComment = true
		case '\'', '"':
			startWord()
			if err := countTokenByte(); err != nil {
				return nil, err
			}
			if directiveBytes == 0 {
				if err := countDirectiveByte(); err != nil {
					return nil, err
				}
			}
			quote = current
		case '\\':
			startWord()
			if err := countTokenByte(); err != nil {
				return nil, err
			}
			if directiveBytes == 0 {
				if err := countDirectiveByte(); err != nil {
					return nil, err
				}
			}
			escaped = true
		case ';':
			finishDirective()
		case '{':
			finishDirective()
		case '}':
		case ' ', '\t', '\r':
			finishWord()
		case '\n':
			finishWord()
		default:
			startWord()
			if err := countTokenByte(); err != nil {
				return nil, err
			}
			if directiveBytes == 0 {
				if err := countDirectiveByte(); err != nil {
					return nil, err
				}
			}
			word = append(word, current)
		}
		advancePosition(current)
	}
	if escaped {
		return nil, errors.New("scan directives: dangling escape")
	}
	if quote != 0 {
		return nil, errors.New("scan directives: unterminated quote")
	}
	if wordStarted || len(words) != 0 {
		return nil, errors.New("scan directives: missing terminator")
	}

	return directives, nil
}

// ExpandIncludeGraph expands deterministic includes only against the supplied manifest.
func ExpandIncludeGraph(
	ctx context.Context,
	entry RelativePath,
	entries []RawEntry,
	read func(context.Context, RelativePath) ([]byte, error),
	limits Limits,
) (IncludeGraph, map[RelativePath]struct{}, map[RelativePath]struct{}, error) {
	if limits.MaxIncludeEdges <= 0 || limits.MaxIncludeDepth <= 0 {
		return IncludeGraph{}, nil, nil, fmt.Errorf("expand include graph: %w", ErrLimitExceeded)
	}
	manifest := make(map[RelativePath]RawEntry, len(entries))
	manifestPaths := make([]RelativePath, 0, len(entries))
	for _, raw := range entries {
		if _, exists := manifest[raw.Path]; !exists {
			manifestPaths = append(manifestPaths, raw.Path)
		}
		manifest[raw.Path] = raw
	}
	sortRelativePaths(manifestPaths)
	root, ok := manifest[entry]
	if !ok || root.Type != EntryType("regular") {
		return IncludeGraph{}, nil, nil, fmt.Errorf("expand include graph: %w", ErrEntryNotManaged)
	}
	if read == nil {
		return IncludeGraph{}, nil, nil, errors.New("expand include graph: read callback is nil")
	}

	graph := IncludeGraph{}
	included := make(map[RelativePath]struct{})
	sensitive := make(map[RelativePath]struct{})
	visited := make(map[RelativePath]struct{})
	type edgeKey struct {
		source     RelativePath
		target     RelativePath
		status     DependencyStatus
		expression string
	}
	edges := make(map[edgeKey]struct{})
	addEdge := func(dependency Dependency, expression string) (bool, error) {
		key := edgeKey{
			source:     dependency.Source,
			target:     dependency.Target,
			status:     dependency.Status,
			expression: expression,
		}
		if _, exists := edges[key]; exists {
			return false, nil
		}
		if len(graph.Edges) >= limits.MaxIncludeEdges {
			return false, fmt.Errorf("expand include graph: edges: %w", ErrLimitExceeded)
		}
		edges[key] = struct{}{}
		graph.Edges = append(graph.Edges, dependency)
		switch dependency.Status {
		case DependencyMissing:
			graph.MissingCount++
		case DependencyExternal:
			graph.ExternalCount++
		case DependencyResolved, DependencyUnresolved, DependencySymlink, DependencySpecial, DependencyCycle:
		}
		return true, nil
	}
	active := make(map[RelativePath]int)
	stack := make([]RelativePath, 0, limits.MaxIncludeDepth)
	cycleKeys := make(map[string]struct{})
	var visit func(RelativePath, int) error
	visit = func(source RelativePath, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > limits.MaxIncludeDepth {
			return fmt.Errorf("expand include graph: depth: %w", ErrLimitExceeded)
		}
		if _, ok := visited[source]; ok {
			return nil
		}
		visited[source] = struct{}{}
		active[source] = len(stack)
		stack = append(stack, source)
		defer func() {
			stack = stack[:len(stack)-1]
			delete(active, source)
		}()
		content, err := read(ctx, source)
		if err != nil {
			return fmt.Errorf("read include source %q: %w", source, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		directives, err := ScanDirectives(content, limits)
		if err != nil {
			return fmt.Errorf("scan include source %q: %w", source, err)
		}
		for _, directive := range directives {
			if err := ctx.Err(); err != nil {
				return err
			}
			if isSensitiveDirectiveName(directive.Name) {
				if len(directive.Arguments) == 1 {
					resolved := resolveExpression(directive.Arguments[0], limits)
					if resolved.status == DependencyResolved {
						for _, target := range matchingTargets(resolved, manifestPaths) {
							sensitive[target] = struct{}{}
						}
					}
				}
				continue
			}
			if directive.Name != "include" {
				continue
			}
			if len(directive.Arguments) != 1 {
				if _, err := addEdge(Dependency{
					Source:       source,
					Line:         directive.Line,
					Column:       directive.Column,
					DisplayValue: "[unresolved]",
					Status:       DependencyUnresolved,
				}, strings.Join(directive.Arguments, "\x00")); err != nil {
					return err
				}
				continue
			}
			expression := directive.Arguments[0]
			resolved := resolveExpression(expression, limits)
			if resolved.status != DependencyResolved {
				if _, err := addEdge(Dependency{
					Source:       source,
					Line:         directive.Line,
					Column:       directive.Column,
					DisplayValue: resolved.display,
					Status:       resolved.status,
				}, expression); err != nil {
					return err
				}
				continue
			}
			targets := matchingTargets(resolved, manifestPaths)
			if len(targets) == 0 && resolved.hasGlob {
				if _, err := addEdge(Dependency{
					Source:       source,
					Line:         directive.Line,
					Column:       directive.Column,
					DisplayValue: resolved.display,
					Status:       DependencyMissing,
				}, expression); err != nil {
					return err
				}
				continue
			}
			for _, target := range targets {
				raw, exists := manifest[target]
				status := dependencyStatus(raw, exists)
				dependency := Dependency{
					Source:       source,
					Line:         directive.Line,
					Column:       directive.Column,
					DisplayValue: resolved.display,
					Target:       target,
					Status:       status,
				}
				if status == DependencyResolved {
					if cycleStart, exists := active[target]; exists {
						dependency.Status = DependencyCycle
						dependency.Cycle = true
						added, err := addEdge(dependency, expression)
						if err != nil {
							return err
						}
						included[target] = struct{}{}
						if added {
							cycle := append([]RelativePath(nil), stack[cycleStart:]...)
							cycle = append(cycle, target)
							key := cycleKey(cycle)
							if _, duplicate := cycleKeys[key]; !duplicate {
								cycleKeys[key] = struct{}{}
								graph.Cycles = append(graph.Cycles, cycle)
							}
						}
						continue
					}
				}
				added, err := addEdge(dependency, expression)
				if err != nil {
					return err
				}
				if !added || status != DependencyResolved {
					continue
				}
				included[target] = struct{}{}
				if err := visit(target, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(entry, 1); err != nil {
		return IncludeGraph{}, nil, nil, err
	}
	return graph, included, sensitive, nil
}

func cycleKey(cycle []RelativePath) string {
	var builder strings.Builder
	for _, path := range cycle {
		builder.WriteString(string(path))
		builder.WriteByte(0)
	}
	return builder.String()
}

type resolvedExpression struct {
	pattern RelativePath
	display string
	status  DependencyStatus
	hasGlob bool
}

func resolveExpression(expression string, limits Limits) resolvedExpression {
	if expression == "" || strings.Contains(expression, "$") || strings.ContainsRune(expression, '\x00') {
		return resolvedExpression{display: "[unresolved]", status: DependencyUnresolved}
	}
	cleaned := path.Clean(expression)
	if path.IsAbs(expression) {
		if cleaned == nginxPrefix || !strings.HasPrefix(cleaned, nginxPrefix+"/") {
			return resolvedExpression{display: "[external]", status: DependencyExternal}
		}
		cleaned = strings.TrimPrefix(cleaned, nginxPrefix+"/")
	} else if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return resolvedExpression{display: "[external]", status: DependencyExternal}
	}
	pattern, err := ParseRelativePath(cleaned, limits)
	if err != nil {
		return resolvedExpression{display: "[unresolved]", status: DependencyUnresolved}
	}
	hasGlob := strings.ContainsAny(cleaned, "*?[")
	if hasGlob {
		if _, err := path.Match(cleaned, cleaned); err != nil {
			return resolvedExpression{display: "[unresolved]", status: DependencyUnresolved}
		}
	}
	return resolvedExpression{
		pattern: pattern,
		display: cleaned,
		status:  DependencyResolved,
		hasGlob: hasGlob,
	}
}

func matchingTargets(expression resolvedExpression, manifestPaths []RelativePath) []RelativePath {
	if !expression.hasGlob {
		return []RelativePath{expression.pattern}
	}
	targets := make([]RelativePath, 0)
	for _, candidate := range manifestPaths {
		matched, err := path.Match(string(expression.pattern), string(candidate))
		if err == nil && matched {
			targets = append(targets, candidate)
		}
	}
	return targets
}

func dependencyStatus(raw RawEntry, exists bool) DependencyStatus {
	if !exists {
		return DependencyMissing
	}
	switch raw.Type {
	case EntryRegular:
		return DependencyResolved
	case EntrySymlink:
		return DependencySymlink
	case EntryDirectory, EntrySpecial:
		return DependencySpecial
	}
	return DependencySpecial
}

func sortRelativePaths(paths []RelativePath) {
	slices.SortFunc(paths, func(left, right RelativePath) int {
		return strings.Compare(string(left), string(right))
	})
}

func isSensitiveDirectiveName(name string) bool {
	return name == "ssl_password_file" ||
		name == "auth_basic_user_file" ||
		strings.HasSuffix(name, "_certificate") ||
		strings.HasSuffix(name, "_certificate_key") ||
		strings.HasSuffix(name, "_trusted_certificate") ||
		strings.HasSuffix(name, "_crl")
}
