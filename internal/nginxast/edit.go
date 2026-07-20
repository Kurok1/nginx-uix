/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package nginxast

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// ErrEditOverlap indicates that two source edits do not have a deterministic order.
var ErrEditOverlap = errors.New("nginx AST edits overlap")

// Edit replaces one source span with generated text.
type Edit struct {
	Span        Span
	Replacement string
}

// SourceEdit binds one local edit to a managed project source path.
type SourceEdit struct {
	Path string
	Edit Edit
}

// Apply validates and applies non-overlapping edits without formatting any other source.
func (d *Document) Apply(edits []Edit) (string, error) {
	if d == nil {
		return "", fmt.Errorf("apply nginx AST edits: document is unavailable")
	}
	if len(edits) == 0 {
		return d.source, nil
	}
	ordered := make([]Edit, len(edits))
	copy(ordered, edits)
	for _, edit := range ordered {
		if !validSpan(edit.Span, len(d.source)) {
			return "", fmt.Errorf("apply nginx AST edits: invalid span")
		}
	}
	sort.SliceStable(ordered, func(left, right int) bool {
		if ordered[left].Span.Start.Offset == ordered[right].Span.Start.Offset {
			return ordered[left].Span.End.Offset < ordered[right].Span.End.Offset
		}
		return ordered[left].Span.Start.Offset < ordered[right].Span.Start.Offset
	})
	for index := 1; index < len(ordered); index++ {
		previous := ordered[index-1].Span
		current := ordered[index].Span
		if previous.End.Offset > current.Start.Offset ||
			(previous.Start.Offset == previous.End.Offset && previous.Start.Offset == current.Start.Offset) {
			return "", fmt.Errorf("apply nginx AST edits: %w", ErrEditOverlap)
		}
	}

	var result strings.Builder
	last := 0
	for _, edit := range ordered {
		result.WriteString(d.source[last:edit.Span.Start.Offset])
		result.WriteString(edit.Replacement)
		last = edit.Span.End.Offset
	}
	result.WriteString(d.source[last:])
	return result.String(), nil
}

// AppendToBlock creates one local insertion immediately before a block close brace.
func (d *Document) AppendToBlock(block *Block, text string) (Edit, error) {
	if d == nil || block == nil || !validSpan(block.Span, len(d.source)) ||
		d.Text(block.Name.Span) != block.Name.Raw || text == "" || strings.ContainsRune(text, '\r') ||
		strings.HasSuffix(text, "\n") {
		return Edit{}, fmt.Errorf("append nginx block: invalid input")
	}
	fragment, err := Parse(text)
	if err != nil || len(fragment.Statements) == 0 {
		return Edit{}, fmt.Errorf("append nginx block: invalid statement: %w", err)
	}

	lineEnding := detectedLineEnding(d.source)
	parentIndent := lineIndentAt(d.source, block.Name.Span.Start.Offset)
	childIndent := parentIndent + "    "
	if len(block.Children) > 0 {
		candidate := lineIndentAt(d.source, block.Children[0].SourceSpan().Start.Offset)
		if candidate != "" || block.Children[0].SourceSpan().Start.Offset == lineStart(d.source, block.Children[0].SourceSpan().Start.Offset) {
			childIndent = candidate
		}
	}
	rendered := indentFragment(text, childIndent, lineEnding)
	closeOffset := block.CloseBraceSpan.Start.Offset
	closeLineStart := lineStart(d.source, closeOffset)
	closePrefix := d.source[closeLineStart:closeOffset]
	if onlyHorizontalWhitespace(closePrefix) && closeLineStart > block.OpenBraceSpan.End.Offset {
		position := positionAt(d.source, closeLineStart)
		return Edit{
			Span:        Span{Start: position, End: position},
			Replacement: rendered + lineEnding,
		}, nil
	}
	position := positionAt(d.source, closeOffset)
	return Edit{
		Span:        Span{Start: position, End: position},
		Replacement: lineEnding + rendered + lineEnding + parentIndent,
	}, nil
}

// StatementDeleteSpan returns the smallest whole-line deletion that preserves independent leading comments.
func (d *Document) StatementDeleteSpan(node Node) (Span, error) {
	if d == nil || node == nil || !validSpan(node.SourceSpan(), len(d.source)) {
		return Span{}, fmt.Errorf("delete nginx statement: invalid input")
	}
	span := node.SourceSpan()
	start := span.Start.Offset
	lineStartOffset := lineStart(d.source, start)
	if onlyHorizontalWhitespace(d.source[lineStartOffset:start]) {
		start = lineStartOffset
	}
	end := span.End.Offset
	for end < len(d.source) && (d.source[end] == ' ' || d.source[end] == '\t') {
		end++
	}
	if end < len(d.source) {
		switch d.source[end] {
		case '\r':
			end++
			if end < len(d.source) && d.source[end] == '\n' {
				end++
			}
		case '\n':
			end++
		}
	}
	return Span{Start: positionAt(d.source, start), End: positionAt(d.source, end)}, nil
}

// ApplyEdits applies source-local edits and returns only changed project documents.
func (p *Project) ApplyEdits(edits []SourceEdit) (map[string]string, error) {
	if p == nil {
		return nil, fmt.Errorf("apply nginx project edits: project is unavailable")
	}
	grouped := make(map[string][]Edit)
	for _, sourceEdit := range edits {
		parsed, exists := p.Documents[sourceEdit.Path]
		if !exists || parsed.Document == nil {
			return nil, fmt.Errorf("apply nginx project edits: source is unavailable")
		}
		grouped[sourceEdit.Path] = append(grouped[sourceEdit.Path], sourceEdit.Edit)
	}
	rendered := make(map[string]string, len(grouped))
	for sourcePath, sourceEdits := range grouped {
		content, err := p.Documents[sourcePath].Document.Apply(sourceEdits)
		if err != nil {
			return nil, fmt.Errorf("apply nginx project edits: %w", err)
		}
		rendered[sourcePath] = content
	}
	return rendered, nil
}

func detectedLineEnding(source string) string {
	for index := 0; index < len(source); index++ {
		switch source[index] {
		case '\r':
			if index+1 < len(source) && source[index+1] == '\n' {
				return "\r\n"
			}
		case '\n':
			return "\n"
		}
	}
	return "\n"
}

func lineStart(source string, offset int) int {
	if offset < 0 || offset > len(source) {
		return 0
	}
	for offset > 0 {
		if source[offset-1] == '\n' || source[offset-1] == '\r' {
			break
		}
		offset--
	}
	return offset
}

func lineIndentAt(source string, offset int) string {
	start := lineStart(source, offset)
	prefix := source[start:offset]
	if !onlyHorizontalWhitespace(prefix) {
		return ""
	}
	return prefix
}

func onlyHorizontalWhitespace(value string) bool {
	for _, character := range value {
		if character != ' ' && character != '\t' {
			return false
		}
	}
	return true
}

func indentFragment(fragment string, indent string, lineEnding string) string {
	lines := strings.Split(fragment, "\n")
	for index := range lines {
		lines[index] = indent + lines[index]
	}
	return strings.Join(lines, lineEnding)
}

func positionAt(source string, target int) Position {
	position := Position{Line: 1, Column: 1}
	for position.Offset < target {
		switch source[position.Offset] {
		case '\r':
			position.Offset++
			if position.Offset < target && source[position.Offset] == '\n' {
				position.Offset++
			}
			position.Line++
			position.Column = 1
		case '\n':
			position.Offset++
			position.Line++
			position.Column = 1
		default:
			_, size := utf8.DecodeRuneInString(source[position.Offset:])
			position.Offset += size
			position.Column++
		}
	}
	return position
}
