/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package nginxast

import "strings"

// Argument is one possibly concatenated Nginx argument and its exact source span.
type Argument struct {
	Raw   string
	Value string
	Span  Span
}

// Node is a generic directive or block with a source span.
type Node interface {
	node()
	SourceSpan() Span
}

// Directive is a semicolon-terminated generic statement.
type Directive struct {
	Name           Argument
	Arguments      []Argument
	Span           Span
	TerminatorSpan Span
}

func (*Directive) node() {}

// SourceSpan returns the complete directive range including its semicolon.
func (d *Directive) SourceSpan() Span {
	if d == nil {
		return Span{}
	}
	return d.Span
}

// Block is a generic named block with recursively parsed children.
type Block struct {
	Name           Argument
	Arguments      []Argument
	Children       []Node
	Span           Span
	HeaderSpan     Span
	OpenBraceSpan  Span
	CloseBraceSpan Span
	BodySpan       Span
}

func (*Block) node() {}

// SourceSpan returns the complete block range including both braces.
func (b *Block) SourceSpan() Span {
	if b == nil {
		return Span{}
	}
	return b.Span
}

// Document owns the immutable source, lossless tokens, and parsed statements.
type Document struct {
	source     string
	Tokens     []Token
	Statements []Node
}

// Parse parses one source with the default bounded limits.
func Parse(source string) (*Document, error) {
	return ParseWithLimits(source, DefaultLimits())
}

// ParseWithLimits parses one source with explicit positive limits.
func ParseWithLimits(source string, limits Limits) (*Document, error) {
	tokens, err := Tokenize(source, limits)
	if err != nil {
		return nil, err
	}
	parser := &parser{source: source, tokens: tokens, limits: limits}
	statements, _, err := parser.parseStatements(0, nil)
	if err != nil {
		return nil, err
	}
	return &Document{source: source, Tokens: tokens, Statements: statements}, nil
}

// Render returns the original source when no explicit edit has been requested.
func (d *Document) Render() string {
	if d == nil {
		return ""
	}
	return d.source
}

// Text returns the exact source in span, or an empty string for an invalid span.
func (d *Document) Text(span Span) string {
	if d == nil || !validSpan(span, len(d.source)) {
		return ""
	}
	return d.source[span.Start.Offset:span.End.Offset]
}

// Walk visits nodes in source preorder until visit returns false for a subtree.
func Walk(nodes []Node, visit func(Node) bool) {
	if visit == nil {
		return
	}
	for _, node := range nodes {
		if !visit(node) {
			continue
		}
		if block, ok := node.(*Block); ok {
			Walk(block.Children, visit)
		}
	}
}

type parser struct {
	source         string
	tokens         []Token
	limits         Limits
	index          int
	statementCount int
}

func (p *parser) parseStatements(depth int, opening *Span) ([]Node, Token, error) {
	nodes := make([]Node, 0)
	for {
		p.skipTrivia()
		current := p.current()
		switch current.Kind {
		case TokenEOF:
			if opening != nil {
				return nil, Token{}, &SyntaxError{Code: ErrorUnclosedBlock, Span: *opening}
			}
			return nodes, current, nil
		case TokenRightBrace:
			if opening == nil {
				return nil, Token{}, &SyntaxError{Code: ErrorUnexpectedCloseBrace, Span: current.Span}
			}
			p.index++
			return nodes, current, nil
		case TokenWord, TokenQuoted, TokenSemicolon, TokenLeftBrace:
			if p.statementCount >= p.limits.MaxStatements {
				return nil, Token{}, &SyntaxError{Code: ErrorLimitExceeded, Span: current.Span}
			}
			node, err := p.parseStatement(depth)
			if err != nil {
				return nil, Token{}, err
			}
			p.statementCount++
			nodes = append(nodes, node)
		case TokenWhitespace, TokenComment:
			return nil, Token{}, &SyntaxError{Code: ErrorMissingTerminator, Span: current.Span}
		}
	}
}

func (p *parser) parseStatement(depth int) (Node, error) {
	header := make([]Argument, 0, 4)
	currentParts := make([]Token, 0, 2)
	flush := func() {
		if len(currentParts) == 0 {
			return
		}
		header = append(header, newArgument(p.source, currentParts))
		currentParts = currentParts[:0]
	}

	for {
		current := p.current()
		switch current.Kind {
		case TokenWhitespace, TokenComment:
			flush()
			p.index++
		case TokenWord, TokenQuoted:
			if len(currentParts) > 0 && currentParts[len(currentParts)-1].Span.End.Offset != current.Span.Start.Offset {
				flush()
			}
			currentParts = append(currentParts, current)
			p.index++
		case TokenSemicolon:
			flush()
			if len(header) == 0 {
				return nil, &SyntaxError{Code: ErrorEmptyStatement, Span: current.Span}
			}
			p.index++
			return &Directive{
				Name: header[0], Arguments: cloneArguments(header[1:]),
				Span: Span{Start: header[0].Span.Start, End: current.Span.End}, TerminatorSpan: current.Span,
			}, nil
		case TokenLeftBrace:
			flush()
			if len(header) == 0 {
				return nil, &SyntaxError{Code: ErrorEmptyStatement, Span: current.Span}
			}
			if depth+1 > p.limits.MaxDepth {
				return nil, &SyntaxError{Code: ErrorLimitExceeded, Span: current.Span}
			}
			opening := current.Span
			p.index++
			children, closing, err := p.parseStatements(depth+1, &opening)
			if err != nil {
				return nil, err
			}
			return &Block{
				Name: header[0], Arguments: cloneArguments(header[1:]), Children: children,
				Span:          Span{Start: header[0].Span.Start, End: closing.Span.End},
				HeaderSpan:    Span{Start: header[0].Span.Start, End: header[len(header)-1].Span.End},
				OpenBraceSpan: opening, CloseBraceSpan: closing.Span,
				BodySpan: Span{Start: opening.End, End: closing.Span.Start},
			}, nil
		case TokenRightBrace, TokenEOF:
			flush()
			return nil, &SyntaxError{Code: ErrorMissingTerminator, Span: current.Span}
		default:
			return nil, &SyntaxError{Code: ErrorMissingTerminator, Span: current.Span}
		}
		if len(header)+boolToInt(len(currentParts) > 0) > p.limits.MaxArguments+1 {
			return nil, &SyntaxError{Code: ErrorLimitExceeded, Span: current.Span}
		}
	}
}

func (p *parser) skipTrivia() {
	for {
		switch p.current().Kind {
		case TokenWhitespace, TokenComment:
			p.index++
		case TokenWord, TokenQuoted, TokenSemicolon, TokenLeftBrace, TokenRightBrace, TokenEOF:
			return
		}
	}
}

func (p *parser) current() Token {
	return p.tokens[p.index]
}

func newArgument(source string, parts []Token) Argument {
	start := parts[0].Span.Start
	end := parts[len(parts)-1].Span.End
	var value strings.Builder
	for _, part := range parts {
		value.WriteString(part.Value)
	}
	return Argument{Raw: source[start.Offset:end.Offset], Value: value.String(), Span: Span{Start: start, End: end}}
}

func cloneArguments(arguments []Argument) []Argument {
	if len(arguments) == 0 {
		return nil
	}
	cloned := make([]Argument, len(arguments))
	copy(cloned, arguments)
	return cloned
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func validSpan(span Span, sourceLength int) bool {
	return span.Start.Offset >= 0 && span.End.Offset >= span.Start.Offset && span.End.Offset <= sourceLength
}
