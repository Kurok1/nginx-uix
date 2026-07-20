/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package nginxast

import "testing"

func TestLosslessGoldenSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
	}{
		{
			name: "comments and mixed quoting",
			source: "# retained\nhttp {\n\tmap $host $name { default 'api value'; }\n" +
				"\tserver { location ~* \\.php$ { proxy_pass http://backend/$request_uri; } }\n}\n",
		},
		{
			name:   "crlf and unknown directives",
			source: "http {\r\n    upstream backend {\r\n        zone backend 64k;\r\n        server [::1]:8080 resolve;\r\n    }\r\n}\r\n",
		},
		{
			name:   "empty and nested blocks",
			source: "events {}\nhttp { server { location /api { location /api/internal {} } } }\n",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document, err := Parse(test.source)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			got := document.Render()
			if got != test.source {
				t.Fatalf("golden mismatch\nwant %q\n got %q", test.source, got)
			}
			reparsed, err := Parse(got)
			if err != nil {
				t.Fatalf("reparse error = %v", err)
			}
			if len(reparsed.Statements) != len(document.Statements) {
				t.Fatalf("statement count = %d, want %d", len(reparsed.Statements), len(document.Statements))
			}
		})
	}
}
