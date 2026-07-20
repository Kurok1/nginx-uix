/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

// Package nginxast provides a lossless syntax tree for bounded Nginx configuration text.
package nginxast

import (
	"errors"
	"fmt"
)

// ErrLimitExceeded indicates that a bounded syntax or project construction limit was reached.
var ErrLimitExceeded = errors.New("nginx AST limit exceeded")

// Position is a byte offset and one-based human-readable source position.
type Position struct {
	Offset int
	Line   int
	Column int
}

// Span identifies a half-open byte range in one source document.
type Span struct {
	Start Position
	End   Position
}

// TokenKind identifies a lossless lexical token.
type TokenKind string

const (
	// TokenWhitespace preserves spacing and line endings.
	TokenWhitespace TokenKind = "whitespace"
	// TokenComment preserves a hash comment without consuming its line ending.
	TokenComment TokenKind = "comment"
	// TokenWord is an unquoted argument segment.
	TokenWord TokenKind = "word"
	// TokenQuoted is a single- or double-quoted argument segment.
	TokenQuoted TokenKind = "quoted"
	// TokenSemicolon terminates a directive.
	TokenSemicolon TokenKind = "semicolon"
	// TokenLeftBrace starts a block body.
	TokenLeftBrace TokenKind = "left_brace"
	// TokenRightBrace ends a block body.
	TokenRightBrace TokenKind = "right_brace"
	// TokenEOF terminates the token stream without consuming source.
	TokenEOF TokenKind = "eof"
)

// Token retains both the exact source and minimally decoded semantic value.
type Token struct {
	Kind  TokenKind
	Raw   string
	Value string
	Span  Span
}

// ErrorCode is a stable syntax failure category.
type ErrorCode string

const (
	// ErrorInvalidUTF8 indicates that source cannot be represented safely.
	ErrorInvalidUTF8 ErrorCode = "invalid_utf8"
	// ErrorUnterminatedQuote indicates an opening quote without a closing quote.
	ErrorUnterminatedQuote ErrorCode = "unterminated_quote"
	// ErrorDanglingEscape indicates a final backslash without an escaped byte.
	ErrorDanglingEscape ErrorCode = "dangling_escape"
	// ErrorUnterminatedVariable indicates a braced variable without its closing brace.
	ErrorUnterminatedVariable ErrorCode = "unterminated_variable"
	// ErrorMissingTerminator indicates a statement without a semicolon or block body.
	ErrorMissingTerminator ErrorCode = "missing_terminator"
	// ErrorUnexpectedCloseBrace indicates a close brace at the wrong nesting level.
	ErrorUnexpectedCloseBrace ErrorCode = "unexpected_close_brace"
	// ErrorUnclosedBlock indicates a block without a close brace.
	ErrorUnclosedBlock ErrorCode = "unclosed_block"
	// ErrorEmptyStatement indicates a terminator without a directive or block name.
	ErrorEmptyStatement ErrorCode = "empty_statement"
	// ErrorLimitExceeded indicates a configured parser bound was reached.
	ErrorLimitExceeded ErrorCode = "limit_exceeded"
)

// SyntaxError reports a stable failure and its safe source position.
type SyntaxError struct {
	Code ErrorCode
	Span Span
}

// Error returns a concise lower-case diagnostic.
func (e *SyntaxError) Error() string {
	if e == nil {
		return "nginx syntax error"
	}
	return fmt.Sprintf("nginx syntax %s at %d:%d", e.Code, e.Span.Start.Line, e.Span.Start.Column)
}

// Limits bounds lexical and syntactic work for one document.
type Limits struct {
	MaxTokens     int
	MaxStatements int
	MaxDepth      int
	MaxArguments  int
}

// DefaultLimits returns the v0.3 per-file syntax bounds.
func DefaultLimits() Limits {
	return Limits{
		MaxTokens:     200_000,
		MaxStatements: 50_000,
		MaxDepth:      128,
		MaxArguments:  1_024,
	}
}

func validLimits(limits Limits) bool {
	return limits.MaxTokens > 0 && limits.MaxStatements > 0 && limits.MaxDepth > 0 && limits.MaxArguments > 0
}
