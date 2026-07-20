/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.3.0
 */

package nginxast

import (
	"strings"
	"unicode/utf8"
)

type tokenizer struct {
	source string
	limits Limits
	offset int
	line   int
	column int
	tokens []Token
}

// Tokenize splits one UTF-8 Nginx source while preserving every source byte.
func Tokenize(source string, limits Limits) ([]Token, error) {
	if !validLimits(limits) {
		return nil, syntaxErrorAt(ErrorLimitExceeded, Position{Line: 1, Column: 1})
	}
	if invalid := firstInvalidUTF8(source); invalid != nil {
		return nil, syntaxErrorAt(ErrorInvalidUTF8, *invalid)
	}
	scanner := &tokenizer{source: source, limits: limits, line: 1, column: 1}
	for scanner.offset < len(scanner.source) {
		var token Token
		var err error
		switch current := scanner.source[scanner.offset]; {
		case isSpaceByte(current):
			token = scanner.scanWhitespace()
		case current == '#':
			token = scanner.scanComment()
		case current == '\'' || current == '"':
			token, err = scanner.scanQuoted(current)
		case current == ';':
			token = scanner.scanPunctuation(TokenSemicolon)
		case current == '{':
			token = scanner.scanPunctuation(TokenLeftBrace)
		case current == '}':
			token = scanner.scanPunctuation(TokenRightBrace)
		default:
			token, err = scanner.scanWord()
		}
		if err != nil {
			return nil, err
		}
		if err := scanner.append(token); err != nil {
			return nil, err
		}
	}
	position := scanner.position()
	scanner.tokens = append(scanner.tokens, Token{Kind: TokenEOF, Span: Span{Start: position, End: position}})
	return scanner.tokens, nil
}

func (s *tokenizer) scanWhitespace() Token {
	start := s.position()
	startOffset := s.offset
	for s.offset < len(s.source) && isSpaceByte(s.source[s.offset]) {
		s.advance()
	}
	raw := s.source[startOffset:s.offset]
	return Token{Kind: TokenWhitespace, Raw: raw, Value: raw, Span: Span{Start: start, End: s.position()}}
}

func (s *tokenizer) scanComment() Token {
	start := s.position()
	startOffset := s.offset
	for s.offset < len(s.source) && s.source[s.offset] != '\n' && s.source[s.offset] != '\r' {
		s.advance()
	}
	raw := s.source[startOffset:s.offset]
	return Token{Kind: TokenComment, Raw: raw, Value: raw, Span: Span{Start: start, End: s.position()}}
}

func (s *tokenizer) scanQuoted(quote byte) (Token, error) {
	start := s.position()
	startOffset := s.offset
	s.advance()
	var value strings.Builder
	for s.offset < len(s.source) {
		current := s.source[s.offset]
		if current == quote {
			s.advance()
			raw := s.source[startOffset:s.offset]
			return Token{Kind: TokenQuoted, Raw: raw, Value: value.String(), Span: Span{Start: start, End: s.position()}}, nil
		}
		if current == '\\' {
			escape := s.position()
			s.advance()
			if s.offset == len(s.source) {
				return Token{}, syntaxErrorAt(ErrorDanglingEscape, escape)
			}
			value.WriteString(s.currentRune())
			s.advance()
			continue
		}
		value.WriteString(s.currentRune())
		s.advance()
	}
	return Token{}, syntaxErrorAt(ErrorUnterminatedQuote, start)
}

func (s *tokenizer) scanWord() (Token, error) {
	start := s.position()
	startOffset := s.offset
	var value strings.Builder
	if strings.HasPrefix(s.source[s.offset:], "${") {
		value.WriteString("${")
		s.advance()
		s.advance()
		for s.offset < len(s.source) && s.source[s.offset] != '}' {
			value.WriteString(s.currentRune())
			s.advance()
		}
		if s.offset == len(s.source) {
			return Token{}, syntaxErrorAt(ErrorUnterminatedVariable, start)
		}
		value.WriteByte('}')
		s.advance()
		raw := s.source[startOffset:s.offset]
		return Token{Kind: TokenWord, Raw: raw, Value: value.String(), Span: Span{Start: start, End: s.position()}}, nil
	}

	for s.offset < len(s.source) {
		current := s.source[s.offset]
		if isSpaceByte(current) || current == '#' || current == ';' || current == '{' || current == '}' ||
			current == '\'' || current == '"' {
			break
		}
		if strings.HasPrefix(s.source[s.offset:], "${") && s.offset > startOffset {
			break
		}
		if current == '\\' {
			escape := s.position()
			s.advance()
			if s.offset == len(s.source) {
				return Token{}, syntaxErrorAt(ErrorDanglingEscape, escape)
			}
			value.WriteString(s.currentRune())
			s.advance()
			continue
		}
		value.WriteString(s.currentRune())
		s.advance()
	}
	raw := s.source[startOffset:s.offset]
	return Token{Kind: TokenWord, Raw: raw, Value: value.String(), Span: Span{Start: start, End: s.position()}}, nil
}

func (s *tokenizer) scanPunctuation(kind TokenKind) Token {
	start := s.position()
	startOffset := s.offset
	s.advance()
	raw := s.source[startOffset:s.offset]
	return Token{Kind: kind, Raw: raw, Value: raw, Span: Span{Start: start, End: s.position()}}
}

func (s *tokenizer) append(token Token) error {
	if len(s.tokens) >= s.limits.MaxTokens {
		return syntaxErrorAt(ErrorLimitExceeded, token.Span.Start)
	}
	s.tokens = append(s.tokens, token)
	return nil
}

func (s *tokenizer) position() Position {
	return Position{Offset: s.offset, Line: s.line, Column: s.column}
}

func (s *tokenizer) currentRune() string {
	_, size := utf8.DecodeRuneInString(s.source[s.offset:])
	return s.source[s.offset : s.offset+size]
}

func (s *tokenizer) advance() {
	switch s.source[s.offset] {
	case '\r':
		s.offset++
		if s.offset < len(s.source) && s.source[s.offset] == '\n' {
			s.offset++
		}
		s.line++
		s.column = 1
	case '\n':
		s.offset++
		s.line++
		s.column = 1
	default:
		_, size := utf8.DecodeRuneInString(s.source[s.offset:])
		s.offset += size
		s.column++
	}
}

func firstInvalidUTF8(source string) *Position {
	line := 1
	column := 1
	for offset := 0; offset < len(source); {
		value, size := utf8.DecodeRuneInString(source[offset:])
		if value == utf8.RuneError && size == 1 {
			return &Position{Offset: offset, Line: line, Column: column}
		}
		switch source[offset] {
		case '\r':
			offset++
			if offset < len(source) && source[offset] == '\n' {
				offset++
			}
			line++
			column = 1
		case '\n':
			offset++
			line++
			column = 1
		default:
			offset += size
			column++
		}
	}
	return nil
}

func isSpaceByte(value byte) bool {
	switch value {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	default:
		return false
	}
}

func syntaxErrorAt(code ErrorCode, start Position) *SyntaxError {
	return &SyntaxError{Code: code, Span: Span{Start: start, End: start}}
}
