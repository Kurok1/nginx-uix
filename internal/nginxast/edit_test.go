/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package nginxast

import (
	"reflect"
	"testing"
)

func TestAppendToBlockUsesLocalLineEndingAndIndentation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		text   string
		want   string
	}{
		{
			name:   "empty LF block",
			source: "http {\n}\n",
			text:   "upstream backend {\n    server 127.0.0.1;\n}",
			want: "http {\n" +
				"    upstream backend {\n" +
				"        server 127.0.0.1;\n" +
				"    }\n" +
				"}\n",
		},
		{
			name:   "existing CRLF indentation",
			source: "http {\r\n\tserver { }\r\n}\r\n",
			text:   "upstream backend {\n    server 127.0.0.1;\n}",
			want: "http {\r\n" +
				"\tserver { }\r\n" +
				"\tupstream backend {\r\n" +
				"\t    server 127.0.0.1;\r\n" +
				"\t}\r\n" +
				"}\r\n",
		},
		{
			name:   "inline empty block",
			source: "http {}\n",
			text:   "upstream backend {\n    server 127.0.0.1;\n}",
			want: "http {\n" +
				"    upstream backend {\n" +
				"        server 127.0.0.1;\n" +
				"    }\n" +
				"}\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document, err := Parse(test.source)
			if err != nil {
				t.Fatal(err)
			}
			block := document.Statements[0].(*Block)
			edit, err := document.AppendToBlock(block, test.text)
			if err != nil {
				t.Fatalf("AppendToBlock() error = %v", err)
			}
			got, err := document.Apply([]Edit{edit})
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("AppendToBlock() = %q, want %q", got, test.want)
			}
			if _, err := Parse(got); err != nil {
				t.Fatalf("Parse(result) error = %v", err)
			}
		})
	}
}

func TestStatementDeleteSpanLeavesIndependentCommentsAndRemovesOwnLine(t *testing.T) {
	t.Parallel()

	source := "http {\n" +
		"    # keep this comment\n" +
		"    upstream old { server 127.0.0.1; }\n" +
		"    server { }\n" +
		"}\n"
	document, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	httpBlock := document.Statements[0].(*Block)
	upstream := httpBlock.Children[0].(*Block)
	span, err := document.StatementDeleteSpan(upstream)
	if err != nil {
		t.Fatalf("StatementDeleteSpan() error = %v", err)
	}
	got, err := document.Apply([]Edit{{Span: span}})
	if err != nil {
		t.Fatal(err)
	}
	want := "http {\n" +
		"    # keep this comment\n" +
		"    server { }\n" +
		"}\n"
	if got != want {
		t.Fatalf("delete result = %q, want %q", got, want)
	}
}

func TestProjectApplyEditsChangesOnlyAddressedDocuments(t *testing.T) {
	t.Parallel()

	project, err := BuildProject(
		[]SourceFile{
			{Path: "a.conf", Source: "upstream old { server 127.0.0.1; }\n"},
			{Path: "nginx.conf", Source: "http { include a.conf; }\n"},
		},
		[]IncludeEdge{{
			Source: "nginx.conf", Line: 1, Column: 8, Target: "a.conf", Status: IncludeResolved,
		}},
		DefaultProjectLimits(),
	)
	if err != nil {
		t.Fatal(err)
	}
	reference := requireProjectBlock(t, project, "a.conf", "upstream")
	block := reference.Node.(*Block)
	rendered, err := project.ApplyEdits([]SourceEdit{{
		Path: "a.conf",
		Edit: Edit{Span: block.Arguments[0].Span, Replacement: "backend"},
	}})
	if err != nil {
		t.Fatalf("ApplyEdits() error = %v", err)
	}
	want := map[string]string{"a.conf": "upstream backend { server 127.0.0.1; }\n"}
	if !reflect.DeepEqual(rendered, want) {
		t.Fatalf("ApplyEdits() = %#v, want %#v", rendered, want)
	}
}
