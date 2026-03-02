package lexer

type TokenKind string

const (
	TokenIllegal    TokenKind = "illegal"
	TokenEOF        TokenKind = "eof"
	TokenIdentifier TokenKind = "identifier"
	TokenString     TokenKind = "string"
	TokenNumber     TokenKind = "number"

	TokenKeywordPackage TokenKind = "package"
	TokenKeywordImport  TokenKind = "import"
	TokenKeywordType    TokenKind = "type"
	TokenKeywordStruct  TokenKind = "struct"
	TokenKeywordTask    TokenKind = "task"
	TokenKeywordFunc    TokenKind = "func"
	TokenKeywordReturn  TokenKind = "return"

	TokenLParen    TokenKind = "("
	TokenRParen    TokenKind = ")"
	TokenLBrace    TokenKind = "{"
	TokenRBrace    TokenKind = "}"
	TokenLBracket  TokenKind = "["
	TokenRBracket  TokenKind = "]"
	TokenComma     TokenKind = ","
	TokenDot       TokenKind = "."
	TokenColon     TokenKind = ":"
	TokenSemicolon TokenKind = ";"
	TokenAssign    TokenKind = "="
	TokenDefine    TokenKind = ":="
	TokenPlus      TokenKind = "+"
	TokenMinus     TokenKind = "-"
	TokenStar      TokenKind = "*"
	TokenSlash     TokenKind = "/"
)

type Position struct {
	Offset int
	Line   int
	Column int
}

type Span struct {
	Start Position
	End   Position
}

type Token struct {
	Kind   TokenKind
	Lexeme string
	Span   Span
}

var keywords = map[string]TokenKind{
	"package": TokenKeywordPackage,
	"import":  TokenKeywordImport,
	"type":    TokenKeywordType,
	"struct":  TokenKeywordStruct,
	"task":    TokenKeywordTask,
	"func":    TokenKeywordFunc,
	"return":  TokenKeywordReturn,
}
