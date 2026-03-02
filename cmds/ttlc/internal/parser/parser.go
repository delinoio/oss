package parser

import (
	"fmt"

	"github.com/delinoio/oss/cmds/ttlc/internal/ast"
	"github.com/delinoio/oss/cmds/ttlc/internal/contracts"
	"github.com/delinoio/oss/cmds/ttlc/internal/diagnostic"
	"github.com/delinoio/oss/cmds/ttlc/internal/lexer"
)

type Parser struct {
	tokens      []lexer.Token
	index       int
	diagnostics []diagnostic.Diagnostic
}

func Parse(tokens []lexer.Token) (*ast.Module, []diagnostic.Diagnostic) {
	parser := &Parser{tokens: tokens}
	module := parser.parseModule()
	return module, parser.diagnostics
}

func (p *Parser) parseModule() *ast.Module {
	module := &ast.Module{}
	if !p.match(lexer.TokenKeywordPackage) {
		p.addCurrentSyntaxError("module must start with package declaration")
		return module
	}
	packageName, ok := p.expectIdentifier("expected package name")
	if ok {
		module.PackageName = packageName
	}

	for !p.check(lexer.TokenEOF) {
		switch p.current().Kind {
		case lexer.TokenSemicolon:
			p.advance()
		case lexer.TokenKeywordImport:
			module.Imports = append(module.Imports, p.parseImportDecls()...)
		case lexer.TokenKeywordType:
			declaration := p.parseTypeDecl()
			if declaration != nil {
				module.Decls = append(module.Decls, declaration)
			}
		case lexer.TokenKeywordTask:
			declaration := p.parseTaskDecl()
			if declaration != nil {
				module.Decls = append(module.Decls, declaration)
			}
		case lexer.TokenKeywordFunc:
			declaration := p.parseFuncDecl()
			if declaration != nil {
				module.Decls = append(module.Decls, declaration)
			}
		default:
			p.addCurrentSyntaxError("unsupported top-level declaration")
			p.synchronizeTopLevel()
		}
	}

	if len(p.tokens) > 0 {
		module.Span = spanFromTokenRange(p.tokens[0], p.tokens[len(p.tokens)-1])
	}
	return module
}

func (p *Parser) parseImportDecls() []ast.ImportDecl {
	start := p.advance()
	imports := make([]ast.ImportDecl, 0, 2)

	if p.match(lexer.TokenLParen) {
		for !p.check(lexer.TokenRParen) && !p.check(lexer.TokenEOF) {
			if p.match(lexer.TokenSemicolon) {
				continue
			}
			token := p.current()
			if !p.match(lexer.TokenString) {
				p.addSyntaxError(token, "expected import path string")
				p.advance()
				continue
			}
			imports = append(imports, ast.ImportDecl{Path: token.Lexeme, Span: spanFromTokenRange(start, token)})
			_ = p.match(lexer.TokenSemicolon)
		}
		p.expect(lexer.TokenRParen, "expected ')' after import group")
		return imports
	}

	token := p.current()
	if !p.match(lexer.TokenString) {
		p.addSyntaxError(token, "expected import path string")
		return imports
	}
	imports = append(imports, ast.ImportDecl{Path: token.Lexeme, Span: spanFromTokenRange(start, token)})
	_ = p.match(lexer.TokenSemicolon)
	return imports
}

func (p *Parser) parseTypeDecl() ast.Decl {
	start := p.advance()
	name, ok := p.expectIdentifier("expected type name")
	if !ok {
		p.synchronizeTopLevel()
		return nil
	}
	if !p.expect(lexer.TokenKeywordStruct, "only struct type declarations are supported") {
		p.synchronizeTopLevel()
		return nil
	}
	if !p.expect(lexer.TokenLBrace, "expected '{' after struct") {
		p.synchronizeTopLevel()
		return nil
	}

	fields := make([]ast.StructField, 0, 4)
	for !p.check(lexer.TokenRBrace) && !p.check(lexer.TokenEOF) {
		if p.match(lexer.TokenSemicolon) {
			continue
		}
		fieldStart := p.current()
		fieldName, ok := p.expectIdentifier("expected struct field name")
		if !ok {
			p.synchronizeBlock()
			continue
		}
		fieldType := p.parseTypeExpr()
		if fieldType == nil {
			p.synchronizeBlock()
			continue
		}
		fields = append(fields, ast.StructField{
			Name: fieldName,
			Type: fieldType,
			Span: spanFromTokenRange(fieldStart, p.previous()),
		})
		_ = p.match(lexer.TokenSemicolon)
	}
	end := p.current()
	p.expect(lexer.TokenRBrace, "expected '}' after struct fields")
	_ = p.match(lexer.TokenSemicolon)

	return &ast.TypeDecl{Name: name, Fields: fields, Span: spanFromTokenRange(start, end)}
}

func (p *Parser) parseTaskDecl() ast.Decl {
	start := p.advance()
	if !p.expect(lexer.TokenKeywordFunc, "expected 'func' after 'task'") {
		p.synchronizeTopLevel()
		return nil
	}
	name, ok := p.expectIdentifier("expected task name")
	if !ok {
		p.synchronizeTopLevel()
		return nil
	}
	parameters, ok := p.parseParameters()
	if !ok {
		p.synchronizeTopLevel()
		return nil
	}
	returnType := p.parseTypeExpr()
	if returnType == nil {
		p.addCurrentSyntaxError("task functions must declare a return type")
		p.synchronizeTopLevel()
		return nil
	}
	body, bodyEnd, ok := p.parseBlock()
	if !ok {
		p.synchronizeTopLevel()
		return nil
	}
	return &ast.TaskDecl{
		Name:       name,
		Parameters: parameters,
		ReturnType: returnType,
		Body:       body,
		Span:       spanFromTokenRange(start, bodyEnd),
	}
}

func (p *Parser) parseFuncDecl() ast.Decl {
	start := p.advance()
	name, ok := p.expectIdentifier("expected function name")
	if !ok {
		p.synchronizeTopLevel()
		return nil
	}
	parameters, ok := p.parseParameters()
	if !ok {
		p.synchronizeTopLevel()
		return nil
	}

	var returnType *ast.TypeExpr
	if !p.check(lexer.TokenLBrace) {
		returnType = p.parseTypeExpr()
		if returnType == nil {
			p.synchronizeTopLevel()
			return nil
		}
	}

	body, bodyEnd, ok := p.parseBlock()
	if !ok {
		p.synchronizeTopLevel()
		return nil
	}
	return &ast.FuncDecl{
		Name:       name,
		Parameters: parameters,
		ReturnType: returnType,
		Body:       body,
		Span:       spanFromTokenRange(start, bodyEnd),
	}
}

func (p *Parser) parseParameters() ([]ast.Parameter, bool) {
	if !p.expect(lexer.TokenLParen, "expected '(' for parameters") {
		return nil, false
	}
	parameters := make([]ast.Parameter, 0, 4)
	for !p.check(lexer.TokenRParen) && !p.check(lexer.TokenEOF) {
		paramStart := p.current()
		name, ok := p.expectIdentifier("expected parameter name")
		if !ok {
			return nil, false
		}
		paramType := p.parseTypeExpr()
		if paramType == nil {
			return nil, false
		}
		parameters = append(parameters, ast.Parameter{
			Name: name,
			Type: paramType,
			Span: spanFromTokenRange(paramStart, p.previous()),
		})
		if !p.match(lexer.TokenComma) {
			break
		}
	}
	if !p.expect(lexer.TokenRParen, "expected ')' after parameters") {
		return nil, false
	}
	return parameters, true
}

func (p *Parser) parseTypeExpr() *ast.TypeExpr {
	start := p.current()
	name, ok := p.expectIdentifier("expected type name")
	if !ok {
		return nil
	}

	typeExpr := &ast.TypeExpr{Name: name, Span: spanFromTokenRange(start, p.previous())}
	if p.match(lexer.TokenDot) {
		qualifiedName, ok := p.expectIdentifier("expected type name after '.'")
		if !ok {
			return nil
		}
		typeExpr.Package = typeExpr.Name
		typeExpr.Name = qualifiedName
		typeExpr.Span = spanFromTokenRange(start, p.previous())
	}

	if p.match(lexer.TokenLBracket) {
		typeArgs := make([]*ast.TypeExpr, 0, 2)
		for !p.check(lexer.TokenRBracket) && !p.check(lexer.TokenEOF) {
			typeArg := p.parseTypeExpr()
			if typeArg == nil {
				return nil
			}
			typeArgs = append(typeArgs, typeArg)
			if !p.match(lexer.TokenComma) {
				break
			}
		}
		if !p.expect(lexer.TokenRBracket, "expected ']' after generic type arguments") {
			return nil
		}
		typeExpr.TypeArgs = typeArgs
		typeExpr.Span = spanFromTokenRange(start, p.previous())
	}

	return typeExpr
}

func (p *Parser) parseBlock() ([]ast.Stmt, lexer.Token, bool) {
	if !p.expect(lexer.TokenLBrace, "expected '{' to start block") {
		return nil, p.current(), false
	}

	statements := make([]ast.Stmt, 0, 8)
	for !p.check(lexer.TokenRBrace) && !p.check(lexer.TokenEOF) {
		if p.match(lexer.TokenSemicolon) {
			continue
		}
		statement := p.parseStmt()
		if statement == nil {
			p.synchronizeBlock()
			continue
		}
		statements = append(statements, statement)
		_ = p.match(lexer.TokenSemicolon)
	}
	end := p.current()
	if !p.expect(lexer.TokenRBrace, "expected '}' to close block") {
		return nil, end, false
	}
	_ = p.match(lexer.TokenSemicolon)
	return statements, end, true
}

func (p *Parser) parseStmt() ast.Stmt {
	if p.match(lexer.TokenKeywordReturn) {
		start := p.previous()
		if p.check(lexer.TokenRBrace) {
			return &ast.ReturnStmt{Span: spanFromTokenRange(start, start)}
		}
		value := p.parseExpr()
		if value == nil {
			return nil
		}
		return &ast.ReturnStmt{Value: value, Span: spanFromTokenRange(start, tokenFromExpr(value))}
	}

	if p.check(lexer.TokenIdentifier) && (p.peek().Kind == lexer.TokenDefine || p.peek().Kind == lexer.TokenAssign) {
		start := p.current()
		nameToken := p.advance()
		operatorToken := p.advance()
		value := p.parseExpr()
		if value == nil {
			return nil
		}
		operator := ast.AssignOperatorSet
		if operatorToken.Kind == lexer.TokenDefine {
			operator = ast.AssignOperatorDefine
		}
		return &ast.AssignStmt{
			Name:     nameToken.Lexeme,
			Operator: operator,
			Value:    value,
			Span:     spanFromTokenRange(start, tokenFromExpr(value)),
		}
	}

	value := p.parseExpr()
	if value == nil {
		return nil
	}
	return &ast.ExprStmt{Value: value, Span: spanFromTokenRange(tokenFromExpr(value), tokenFromExpr(value))}
}

func (p *Parser) parseExpr() ast.Expr {
	primary := p.parsePrimary()
	if primary == nil {
		return nil
	}
	return p.parseExprSuffix(primary)
}

func (p *Parser) parsePrimary() ast.Expr {
	token := p.current()
	switch token.Kind {
	case lexer.TokenIdentifier:
		p.advance()
		return &ast.IdentifierExpr{Name: token.Lexeme, Span: spanFromTokenRange(token, token)}
	case lexer.TokenString:
		p.advance()
		return &ast.StringLiteralExpr{Value: token.Lexeme, Span: spanFromTokenRange(token, token)}
	case lexer.TokenNumber:
		p.advance()
		return &ast.NumberLiteralExpr{Value: token.Lexeme, Span: spanFromTokenRange(token, token)}
	case lexer.TokenLParen:
		p.advance()
		value := p.parseExpr()
		if !p.expect(lexer.TokenRParen, "expected ')' after expression") {
			return nil
		}
		return value
	default:
		p.addSyntaxError(token, "expected expression")
		return nil
	}
}

func (p *Parser) parseExprSuffix(expression ast.Expr) ast.Expr {
	for {
		switch {
		case p.match(lexer.TokenDot):
			name, ok := p.expectIdentifier("expected selector name")
			if !ok {
				return expression
			}
			expression = &ast.SelectorExpr{
				Target: expression,
				Name:   name,
				Span:   mergeSpan(expression.ExprSpan(), ast.Span{Start: toASTPosition(p.previous().Span.Start), End: toASTPosition(p.previous().Span.End)}),
			}
		case p.match(lexer.TokenLParen):
			args := make([]ast.Expr, 0, 4)
			for !p.check(lexer.TokenRParen) && !p.check(lexer.TokenEOF) {
				arg := p.parseExpr()
				if arg == nil {
					return expression
				}
				args = append(args, arg)
				if !p.match(lexer.TokenComma) {
					break
				}
			}
			if !p.expect(lexer.TokenRParen, "expected ')' after call arguments") {
				return expression
			}
			expression = &ast.CallExpr{
				Callee: expression,
				Args:   args,
				Span:   mergeSpan(expression.ExprSpan(), ast.Span{Start: toASTPosition(p.previous().Span.Start), End: toASTPosition(p.previous().Span.End)}),
			}
		case p.check(lexer.TokenLBrace):
			typeExpr := typeExprFromExpr(expression)
			if typeExpr == nil {
				return expression
			}
			start := p.current()
			p.advance()
			fields := make([]ast.CompositeField, 0, 4)
			for !p.check(lexer.TokenRBrace) && !p.check(lexer.TokenEOF) {
				if p.match(lexer.TokenComma) {
					continue
				}
				fieldStart := p.current()
				fieldName, ok := p.expectIdentifier("expected composite field name")
				if !ok {
					return expression
				}
				if !p.expect(lexer.TokenColon, "expected ':' in composite field") {
					return expression
				}
				value := p.parseExpr()
				if value == nil {
					return expression
				}
				fields = append(fields, ast.CompositeField{Name: fieldName, Value: value, Span: spanFromTokenRange(fieldStart, tokenFromExpr(value))})
				if !p.match(lexer.TokenComma) {
					break
				}
			}
			if !p.expect(lexer.TokenRBrace, "expected '}' after composite literal") {
				return expression
			}
			expression = &ast.CompositeLiteralExpr{
				Type:   typeExpr,
				Fields: fields,
				Span:   mergeSpan(expression.ExprSpan(), spanFromTokenRange(start, p.previous())),
			}
		default:
			return expression
		}
	}
}

func typeExprFromExpr(expression ast.Expr) *ast.TypeExpr {
	switch typed := expression.(type) {
	case *ast.IdentifierExpr:
		return &ast.TypeExpr{Name: typed.Name, Span: typed.Span}
	case *ast.SelectorExpr:
		target, ok := typed.Target.(*ast.IdentifierExpr)
		if !ok {
			return nil
		}
		return &ast.TypeExpr{Package: target.Name, Name: typed.Name, Span: typed.Span}
	default:
		return nil
	}
}

func tokenFromExpr(expression ast.Expr) lexer.Token {
	span := expression.ExprSpan()
	return lexer.Token{
		Span: lexer.Span{
			Start: lexer.Position{Offset: span.Start.Offset, Line: span.Start.Line, Column: span.Start.Column},
			End:   lexer.Position{Offset: span.End.Offset, Line: span.End.Line, Column: span.End.Column},
		},
	}
}

func mergeSpan(left ast.Span, right ast.Span) ast.Span {
	return ast.Span{Start: left.Start, End: right.End}
}

func (p *Parser) synchronizeTopLevel() {
	for !p.check(lexer.TokenEOF) {
		switch p.current().Kind {
		case lexer.TokenKeywordImport, lexer.TokenKeywordType, lexer.TokenKeywordTask, lexer.TokenKeywordFunc:
			return
		default:
			p.advance()
		}
	}
}

func (p *Parser) synchronizeBlock() {
	for !p.check(lexer.TokenEOF) {
		if p.check(lexer.TokenSemicolon) || p.check(lexer.TokenRBrace) {
			return
		}
		p.advance()
	}
}

func (p *Parser) expect(kind lexer.TokenKind, message string) bool {
	if p.match(kind) {
		return true
	}
	p.addCurrentSyntaxError(message)
	return false
}

func (p *Parser) expectIdentifier(message string) (string, bool) {
	if !p.check(lexer.TokenIdentifier) {
		p.addCurrentSyntaxError(message)
		return "", false
	}
	token := p.advance()
	return token.Lexeme, true
}

func (p *Parser) addCurrentSyntaxError(message string) {
	token := p.current()
	p.addSyntaxError(token, message)
}

func (p *Parser) addSyntaxError(token lexer.Token, message string) {
	p.diagnostics = append(p.diagnostics, diagnostic.Diagnostic{
		Kind:    contracts.DiagnosticKindSyntaxError,
		Message: message,
		Line:    token.Span.Start.Line,
		Column:  token.Span.Start.Column,
	})
}

func (p *Parser) check(kind lexer.TokenKind) bool {
	return p.current().Kind == kind
}

func (p *Parser) match(kind lexer.TokenKind) bool {
	if !p.check(kind) {
		return false
	}
	p.advance()
	return true
}

func (p *Parser) advance() lexer.Token {
	if p.index >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1]
	}
	token := p.tokens[p.index]
	if p.index < len(p.tokens)-1 {
		p.index++
	}
	return token
}

func (p *Parser) current() lexer.Token {
	if len(p.tokens) == 0 {
		return lexer.Token{Kind: lexer.TokenEOF}
	}
	if p.index >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1]
	}
	return p.tokens[p.index]
}

func (p *Parser) previous() lexer.Token {
	if len(p.tokens) == 0 {
		return lexer.Token{Kind: lexer.TokenEOF}
	}
	if p.index == 0 {
		return p.tokens[0]
	}
	return p.tokens[p.index-1]
}

func (p *Parser) peek() lexer.Token {
	if len(p.tokens) == 0 {
		return lexer.Token{Kind: lexer.TokenEOF}
	}
	if p.index+1 >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1]
	}
	return p.tokens[p.index+1]
}

func spanFromTokenRange(start lexer.Token, end lexer.Token) ast.Span {
	return ast.Span{Start: toASTPosition(start.Span.Start), End: toASTPosition(end.Span.End)}
}

func toASTPosition(position lexer.Position) ast.Position {
	return ast.Position{Offset: position.Offset, Line: position.Line, Column: position.Column}
}

func (p *Parser) debugCurrent() string {
	token := p.current()
	return fmt.Sprintf("%s(%s)", token.Kind, token.Lexeme)
}
