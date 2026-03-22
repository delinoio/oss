package lexer

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/ttlc/internal/contracts"
	"github.com/delinoio/oss/cmds/ttlc/internal/diagnostic"
	"github.com/delinoio/oss/cmds/ttlc/internal/messages"
)

type Lexer struct {
	source      string
	offset      int
	line        int
	column      int
	diagnostics []diagnostic.Diagnostic
}

func New(source string) *Lexer {
	return &Lexer{source: source, line: 1, column: 1}
}

func Lex(source string) ([]Token, []diagnostic.Diagnostic) {
	lexer := New(source)
	return lexer.Lex()
}

func (l *Lexer) Lex() ([]Token, []diagnostic.Diagnostic) {
	tokens := make([]Token, 0, len(l.source)/2)
	for {
		token := l.nextToken()
		tokens = append(tokens, token)
		if token.Kind == TokenEOF {
			break
		}
	}
	return tokens, l.diagnostics
}

func (l *Lexer) nextToken() Token {
	l.skipWhitespaceAndComments()
	start := l.position()

	if l.offset >= len(l.source) {
		return Token{Kind: TokenEOF, Span: Span{Start: start, End: start}}
	}

	r, size := utf8.DecodeRuneInString(l.source[l.offset:])
	if r == utf8.RuneError && size == 1 {
		l.advance(size)
		return l.illegalToken(start, messages.FormatDiagnostic(messages.DiagnosticInvalidUTF8Rune))
	}

	if isIdentifierStart(r) {
		return l.readIdentifier(start)
	}
	if unicode.IsDigit(r) {
		return l.readNumber(start)
	}

	switch r {
	case '"':
		return l.readString(start)
	case '(':
		l.advance(size)
		return Token{Kind: TokenLParen, Lexeme: "(", Span: Span{Start: start, End: l.position()}}
	case ')':
		l.advance(size)
		return Token{Kind: TokenRParen, Lexeme: ")", Span: Span{Start: start, End: l.position()}}
	case '{':
		l.advance(size)
		return Token{Kind: TokenLBrace, Lexeme: "{", Span: Span{Start: start, End: l.position()}}
	case '}':
		l.advance(size)
		return Token{Kind: TokenRBrace, Lexeme: "}", Span: Span{Start: start, End: l.position()}}
	case '[':
		l.advance(size)
		return Token{Kind: TokenLBracket, Lexeme: "[", Span: Span{Start: start, End: l.position()}}
	case ']':
		l.advance(size)
		return Token{Kind: TokenRBracket, Lexeme: "]", Span: Span{Start: start, End: l.position()}}
	case ',':
		l.advance(size)
		return Token{Kind: TokenComma, Lexeme: ",", Span: Span{Start: start, End: l.position()}}
	case '.':
		l.advance(size)
		return Token{Kind: TokenDot, Lexeme: ".", Span: Span{Start: start, End: l.position()}}
	case ';':
		l.advance(size)
		return Token{Kind: TokenSemicolon, Lexeme: ";", Span: Span{Start: start, End: l.position()}}
	case ':':
		l.advance(size)
		if l.peekRune() == '=' {
			l.advance(1)
			return Token{Kind: TokenDefine, Lexeme: ":=", Span: Span{Start: start, End: l.position()}}
		}
		return Token{Kind: TokenColon, Lexeme: ":", Span: Span{Start: start, End: l.position()}}
	case '=':
		l.advance(size)
		return Token{Kind: TokenAssign, Lexeme: "=", Span: Span{Start: start, End: l.position()}}
	case '+':
		l.advance(size)
		return Token{Kind: TokenPlus, Lexeme: "+", Span: Span{Start: start, End: l.position()}}
	case '-':
		l.advance(size)
		return Token{Kind: TokenMinus, Lexeme: "-", Span: Span{Start: start, End: l.position()}}
	case '*':
		l.advance(size)
		return Token{Kind: TokenStar, Lexeme: "*", Span: Span{Start: start, End: l.position()}}
	case '/':
		l.advance(size)
		return Token{Kind: TokenSlash, Lexeme: "/", Span: Span{Start: start, End: l.position()}}
	default:
		l.advance(size)
		return l.illegalToken(start, messages.FormatDiagnostic(messages.DiagnosticUnsupportedToken, string(r)))
	}
}

func (l *Lexer) readIdentifier(start Position) Token {
	begin := l.offset
	for l.offset < len(l.source) {
		r, size := utf8.DecodeRuneInString(l.source[l.offset:])
		if !isIdentifierPart(r) {
			break
		}
		l.advance(size)
	}
	lexeme := l.source[begin:l.offset]
	kind := TokenIdentifier
	if keywordKind, ok := keywords[lexeme]; ok {
		kind = keywordKind
	}
	return Token{Kind: kind, Lexeme: lexeme, Span: Span{Start: start, End: l.position()}}
}

func (l *Lexer) readNumber(start Position) Token {
	begin := l.offset
	for l.offset < len(l.source) {
		r, size := utf8.DecodeRuneInString(l.source[l.offset:])
		if !unicode.IsDigit(r) {
			break
		}
		l.advance(size)
	}
	return Token{Kind: TokenNumber, Lexeme: l.source[begin:l.offset], Span: Span{Start: start, End: l.position()}}
}

func (l *Lexer) readString(start Position) Token {
	l.advance(1)
	builder := strings.Builder{}
	for l.offset < len(l.source) {
		r, size := utf8.DecodeRuneInString(l.source[l.offset:])
		if r == '"' {
			l.advance(size)
			return Token{Kind: TokenString, Lexeme: builder.String(), Span: Span{Start: start, End: l.position()}}
		}
		if r == '\\' {
			l.advance(size)
			if l.offset >= len(l.source) {
				break
			}
			escaped, escapedSize := utf8.DecodeRuneInString(l.source[l.offset:])
			switch escaped {
			case 'n':
				builder.WriteRune('\n')
			case 't':
				builder.WriteRune('\t')
			case 'r':
				builder.WriteRune('\r')
			case '"':
				builder.WriteRune('"')
			case '\\':
				builder.WriteRune('\\')
			default:
				builder.WriteRune(escaped)
			}
			l.advance(escapedSize)
			continue
		}
		if r == '\n' {
			break
		}
		builder.WriteRune(r)
		l.advance(size)
	}
	l.diagnostics = append(l.diagnostics, diagnostic.Diagnostic{
		Kind:    contracts.DiagnosticKindSyntaxError,
		Message: messages.FormatDiagnostic(messages.DiagnosticUnterminatedStringLiteral),
		Line:    start.Line,
		Column:  start.Column,
	})
	return Token{Kind: TokenIllegal, Lexeme: "", Span: Span{Start: start, End: l.position()}}
}

func (l *Lexer) illegalToken(start Position, message string) Token {
	l.diagnostics = append(l.diagnostics, diagnostic.Diagnostic{
		Kind:    contracts.DiagnosticKindSyntaxError,
		Message: message,
		Line:    start.Line,
		Column:  start.Column,
	})
	return Token{Kind: TokenIllegal, Lexeme: "", Span: Span{Start: start, End: l.position()}}
}

func (l *Lexer) skipWhitespaceAndComments() {
	for l.offset < len(l.source) {
		r, size := utf8.DecodeRuneInString(l.source[l.offset:])
		if unicode.IsSpace(r) {
			l.advance(size)
			continue
		}
		if r == '/' {
			next := l.peekNthRune(1)
			if next == '/' {
				l.advance(2)
				for l.offset < len(l.source) {
					lineRune, lineSize := utf8.DecodeRuneInString(l.source[l.offset:])
					l.advance(lineSize)
					if lineRune == '\n' {
						break
					}
				}
				continue
			}
			if next == '*' {
				commentStart := l.position()
				l.advance(2)
				terminated := false
				for l.offset < len(l.source) {
					current := l.peekRune()
					if current == '*' && l.peekNthRune(1) == '/' {
						l.advance(2)
						terminated = true
						break
					}
					_, lineSize := utf8.DecodeRuneInString(l.source[l.offset:])
					l.advance(lineSize)
				}
				if !terminated {
					l.diagnostics = append(l.diagnostics, diagnostic.Diagnostic{
						Kind:    contracts.DiagnosticKindSyntaxError,
						Message: messages.FormatDiagnostic(messages.DiagnosticUnterminatedBlockComment),
						Line:    commentStart.Line,
						Column:  commentStart.Column,
					})
					return
				}
				continue
			}
		}
		break
	}
}

func (l *Lexer) position() Position {
	return Position{Offset: l.offset, Line: l.line, Column: l.column}
}

func (l *Lexer) peekRune() rune {
	if l.offset >= len(l.source) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(l.source[l.offset:])
	return r
}

func (l *Lexer) peekNthRune(n int) rune {
	pos := l.offset
	for i := 0; i < n; i++ {
		if pos >= len(l.source) {
			return 0
		}
		_, size := utf8.DecodeRuneInString(l.source[pos:])
		pos += size
	}
	if pos >= len(l.source) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(l.source[pos:])
	return r
}

func (l *Lexer) advance(size int) {
	consumed := 0
	for consumed < size && l.offset+consumed < len(l.source) {
		r, runeSize := utf8.DecodeRuneInString(l.source[l.offset+consumed:])
		if r == '\n' {
			l.line++
			l.column = 1
		} else {
			l.column++
		}
		consumed += runeSize
	}
	l.offset += size
}

func isIdentifierStart(r rune) bool {
	return unicode.IsLetter(r) || r == '_'
}

func isIdentifierPart(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}
