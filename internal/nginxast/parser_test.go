/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package nginxast

import (
	"errors"
	"strings"
	"testing"
)

func TestTokenizePreservesRawSourceAndPositions(t *testing.T) {
	t.Parallel()

	source := "http {\r\n\t# keep this\r\n\tset $target http://${backend}:8080;\r\n\treturn 200 'hello world';\r\n}\r\n"
	tokens, err := Tokenize(source, DefaultLimits())
	if err != nil {
		t.Fatalf("Tokenize() error = %v", err)
	}

	var rebuilt strings.Builder
	for _, token := range tokens {
		if token.Kind != TokenEOF {
			rebuilt.WriteString(token.Raw)
		}
	}
	if got := rebuilt.String(); got != source {
		t.Fatalf("token raw source mismatch:\n got: %q\nwant: %q", got, source)
	}

	backend := tokenWithRaw(t, tokens, "${backend}")
	if backend.Kind != TokenWord || backend.Value != "${backend}" {
		t.Fatalf("backend token = %#v", backend)
	}
	if backend.Span.Start.Line != 3 || backend.Span.Start.Column != 21 {
		t.Fatalf("backend start = %#v, want line 3 column 21", backend.Span.Start)
	}

	quoted := tokenWithRaw(t, tokens, "'hello world'")
	if quoted.Kind != TokenQuoted || quoted.Value != "hello world" {
		t.Fatalf("quoted token = %#v", quoted)
	}
	if quoted.Span.Start.Line != 4 || quoted.Span.Start.Column != 13 {
		t.Fatalf("quoted start = %#v, want line 4 column 13", quoted.Span.Start)
	}
}

func TestParseBuildsGenericLosslessAST(t *testing.T) {
	t.Parallel()

	source := "# leading comment\n" +
		"http {\n" +
		"    upstream backend {\n" +
		"        zone backend 64k;\n" +
		"        server 127.0.0.1:8080 weight=2 custom=kept;\n" +
		"    }\n\n" +
		"    server {\n" +
		"        location ^~ /assets/ {\n" +
		"            proxy_pass http://backend/static/;\n" +
		"        }\n" +
		"    }\n" +
		"}\n"
	document, err := Parse(source)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got := document.Render(); got != source {
		t.Fatalf("Render() changed source:\n%s", got)
	}
	if len(document.Statements) != 1 {
		t.Fatalf("top-level statements = %d, want 1", len(document.Statements))
	}

	httpBlock := requireBlock(t, document.Statements[0], "http")
	if got := document.Text(httpBlock.Span); got != strings.TrimSuffix(strings.TrimPrefix(source, "# leading comment\n"), "\n") {
		t.Fatalf("http source span = %q", got)
	}
	if len(httpBlock.Children) != 2 {
		t.Fatalf("http children = %d, want 2", len(httpBlock.Children))
	}

	upstream := requireBlock(t, httpBlock.Children[0], "upstream")
	if len(upstream.Arguments) != 1 || upstream.Arguments[0].Value != "backend" {
		t.Fatalf("upstream arguments = %#v", upstream.Arguments)
	}
	if len(upstream.Children) != 2 {
		t.Fatalf("upstream children = %d, want 2", len(upstream.Children))
	}
	serverDirective := requireDirective(t, upstream.Children[1], "server")
	wantArguments := []string{"127.0.0.1:8080", "weight=2", "custom=kept"}
	if got := argumentValues(serverDirective.Arguments); !equalStrings(got, wantArguments) {
		t.Fatalf("server arguments = %#v, want %#v", got, wantArguments)
	}

	serverBlock := requireBlock(t, httpBlock.Children[1], "server")
	location := requireBlock(t, serverBlock.Children[0], "location")
	if got := argumentValues(location.Arguments); !equalStrings(got, []string{"^~", "/assets/"}) {
		t.Fatalf("location arguments = %#v", got)
	}
	proxyPass := requireDirective(t, location.Children[0], "proxy_pass")
	if got := argumentValues(proxyPass.Arguments); !equalStrings(got, []string{"http://backend/static/"}) {
		t.Fatalf("proxy_pass arguments = %#v", got)
	}
}

func TestParseConcatenatesAdjacentQuotedSegmentsIntoOneArgument(t *testing.T) {
	t.Parallel()

	document, err := Parse("set $value pre' middle 'post\\ value;\n")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	directive := requireDirective(t, document.Statements[0], "set")
	if got := argumentValues(directive.Arguments); !equalStrings(got, []string{"$value", "pre middle post value"}) {
		t.Fatalf("arguments = %#v", got)
	}
	if got := document.Text(directive.Arguments[1].Span); got != "pre' middle 'post\\ value" {
		t.Fatalf("raw concatenated argument = %q", got)
	}
}

func TestParseReportsStableSyntaxErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		code   ErrorCode
		line   int
		column int
	}{
		{name: "invalid utf8", source: string([]byte{'x', 0xff, ';'}), code: ErrorInvalidUTF8, line: 1, column: 2},
		{name: "unterminated quote", source: "set $x \"value;", code: ErrorUnterminatedQuote, line: 1, column: 8},
		{name: "dangling escape", source: "set $x value\\", code: ErrorDanglingEscape, line: 1, column: 13},
		{name: "missing terminator", source: "worker_processes auto", code: ErrorMissingTerminator, line: 1, column: 22},
		{name: "unexpected close", source: "}\n", code: ErrorUnexpectedCloseBrace, line: 1, column: 1},
		{name: "unclosed block", source: "events { worker_connections 1;", code: ErrorUnclosedBlock, line: 1, column: 8},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse(test.source)
			var syntaxError *SyntaxError
			if !errors.As(err, &syntaxError) {
				t.Fatalf("Parse() error = %v, want *SyntaxError", err)
			}
			if syntaxError.Code != test.code {
				t.Fatalf("error code = %q, want %q", syntaxError.Code, test.code)
			}
			if syntaxError.Span.Start.Line != test.line || syntaxError.Span.Start.Column != test.column {
				t.Fatalf("error position = %#v, want %d:%d", syntaxError.Span.Start, test.line, test.column)
			}
		})
	}
}

func TestParseEnforcesLimits(t *testing.T) {
	t.Parallel()

	limits := DefaultLimits()
	limits.MaxStatements = 1
	_, err := ParseWithLimits("a 1;\nb 2;\n", limits)
	var syntaxError *SyntaxError
	if !errors.As(err, &syntaxError) || syntaxError.Code != ErrorLimitExceeded {
		t.Fatalf("ParseWithLimits() error = %v, want %q", err, ErrorLimitExceeded)
	}
}

func TestDocumentApplyUsesNonOverlappingSourceSpans(t *testing.T) {
	t.Parallel()

	source := "http {\n    upstream old {\n        server 127.0.0.1;\n    }\n}\n"
	document, err := Parse(source)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	httpBlock := requireBlock(t, document.Statements[0], "http")
	upstream := requireBlock(t, httpBlock.Children[0], "upstream")

	rendered, err := document.Apply([]Edit{{Span: upstream.Arguments[0].Span, Replacement: "backend"}})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	want := strings.Replace(source, "upstream old", "upstream backend", 1)
	if rendered != want {
		t.Fatalf("Apply() = %q, want %q", rendered, want)
	}
	if _, err := Parse(rendered); err != nil {
		t.Fatalf("Parse(rendered) error = %v", err)
	}

	_, err = document.Apply([]Edit{
		{Span: upstream.Span, Replacement: ""},
		{Span: upstream.Arguments[0].Span, Replacement: "backend"},
	})
	if !errors.Is(err, ErrEditOverlap) {
		t.Fatalf("overlapping Apply() error = %v, want ErrEditOverlap", err)
	}
}

func tokenWithRaw(t *testing.T, tokens []Token, raw string) Token {
	t.Helper()
	for _, token := range tokens {
		if token.Raw == raw {
			return token
		}
	}
	t.Fatalf("token %q not found in %#v", raw, tokens)
	return Token{}
}

func requireBlock(t *testing.T, node Node, name string) *Block {
	t.Helper()
	block, ok := node.(*Block)
	if !ok || block.Name.Value != name {
		t.Fatalf("node = %#v, want block %q", node, name)
	}
	return block
}

func requireDirective(t *testing.T, node Node, name string) *Directive {
	t.Helper()
	directive, ok := node.(*Directive)
	if !ok || directive.Name.Value != name {
		t.Fatalf("node = %#v, want directive %q", node, name)
	}
	return directive
}

func argumentValues(arguments []Argument) []string {
	values := make([]string, len(arguments))
	for index, argument := range arguments {
		values[index] = argument.Value
	}
	return values
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
