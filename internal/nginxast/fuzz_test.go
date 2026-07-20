/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package nginxast

import (
	"strings"
	"testing"
)

func FuzzTokenizePreservesEveryAcceptedByte(f *testing.F) {
	for _, seed := range []string{
		"events {}\nhttp { server { location / { return 204; } } }\n",
		"upstream backend {\r\n  server 127.0.0.1:8080 weight=2; # peer\r\n}\r\n",
		"location ~* \\.php$ { proxy_pass http://backend/api/; }\n",
		"set $value \"prefix \\${host}\";\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source string) {
		limits := DefaultLimits()
		limits.MaxTokens = 20_000
		tokens, err := Tokenize(source, limits)
		if err != nil {
			return
		}
		var rendered strings.Builder
		for _, token := range tokens {
			if token.Kind != TokenEOF {
				rendered.WriteString(token.Raw)
			}
			if token.Span.Start.Offset < 0 ||
				token.Span.End.Offset < token.Span.Start.Offset ||
				token.Span.End.Offset > len(source) {
				t.Fatalf("token span %#v outside source length %d", token.Span, len(source))
			}
		}
		if rendered.String() != source {
			t.Fatalf("token raw join changed source\nwant %q\n got %q", source, rendered.String())
		}
	})
}

func FuzzParseNoOpRenderIsLossless(f *testing.F) {
	for _, seed := range []string{
		"http { upstream backend { server unix:/run/backend.sock; } }\n",
		"server { location = /health { return 204; } }\n",
		"server { location ^~ /assets/ {} location ~ \\.php$ {} }\n",
		"map $host $target { default backend; }\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source string) {
		document, err := Parse(source)
		if err != nil {
			return
		}
		rendered := document.Render()
		if rendered != source {
			t.Fatalf("no-op render changed source\nwant %q\n got %q", source, rendered)
		}
		for _, node := range document.Statements {
			span := node.SourceSpan()
			if span.Start.Offset < 0 || span.End.Offset < span.Start.Offset ||
				span.End.Offset > len(source) {
				t.Fatalf("node span %#v outside source length %d", span, len(source))
			}
		}
	})
}
