package ir

import (
	"fmt"

	"github.com/delinoio/oss/cmds/ttlc/internal/ast"
)

type StmtKind string

const (
	StmtKindAssign StmtKind = "assign"
	StmtKindExpr   StmtKind = "expr"
	StmtKindReturn StmtKind = "return"
)

type ExprKind string

const (
	ExprKindIdentifier       ExprKind = "identifier"
	ExprKindStringLiteral    ExprKind = "string_literal"
	ExprKindNumberLiteral    ExprKind = "number_literal"
	ExprKindCall             ExprKind = "call"
	ExprKindSelector         ExprKind = "selector"
	ExprKindCompositeLiteral ExprKind = "composite_literal"
)

type Field struct {
	Name  string `json:"name"`
	Value Expr   `json:"value"`
}

type Expr struct {
	Kind     ExprKind `json:"kind"`
	Name     string   `json:"name,omitempty"`
	Value    string   `json:"value,omitempty"`
	TypeName string   `json:"type_name,omitempty"`
	Callee   *Expr    `json:"callee,omitempty"`
	Target   *Expr    `json:"target,omitempty"`
	Args     []Expr   `json:"args,omitempty"`
	Fields   []Field  `json:"fields,omitempty"`
}

type Stmt struct {
	Kind     StmtKind `json:"kind"`
	Name     string   `json:"name,omitempty"`
	Operator string   `json:"operator,omitempty"`
	Value    *Expr    `json:"value,omitempty"`
}

type TaskDef struct {
	Name   string   `json:"name"`
	Params []string `json:"params"`
	Body   []Stmt   `json:"body"`
}

type FuncDef struct {
	Name   string   `json:"name"`
	Params []string `json:"params"`
	Body   []Stmt   `json:"body"`
}

func TaskFromDecl(declaration *ast.TaskDecl) (TaskDef, error) {
	if declaration == nil {
		return TaskDef{}, fmt.Errorf("task declaration is nil")
	}
	parameters := make([]string, 0, len(declaration.Parameters))
	for _, parameter := range declaration.Parameters {
		parameters = append(parameters, parameter.Name)
	}

	body := make([]Stmt, 0, len(declaration.Body))
	for _, statement := range declaration.Body {
		serializedStatement, err := StmtFromAST(statement)
		if err != nil {
			return TaskDef{}, err
		}
		body = append(body, serializedStatement)
	}

	return TaskDef{Name: declaration.Name, Params: parameters, Body: body}, nil
}

func FuncFromDecl(declaration *ast.FuncDecl) (FuncDef, error) {
	if declaration == nil {
		return FuncDef{}, fmt.Errorf("func declaration is nil")
	}
	parameters := make([]string, 0, len(declaration.Parameters))
	for _, parameter := range declaration.Parameters {
		parameters = append(parameters, parameter.Name)
	}

	body := make([]Stmt, 0, len(declaration.Body))
	for _, statement := range declaration.Body {
		serializedStatement, err := StmtFromAST(statement)
		if err != nil {
			return FuncDef{}, err
		}
		body = append(body, serializedStatement)
	}

	return FuncDef{Name: declaration.Name, Params: parameters, Body: body}, nil
}

func StmtFromAST(statement ast.Stmt) (Stmt, error) {
	switch typed := statement.(type) {
	case *ast.AssignStmt:
		value, err := ExprFromAST(typed.Value)
		if err != nil {
			return Stmt{}, err
		}
		return Stmt{
			Kind:     StmtKindAssign,
			Name:     typed.Name,
			Operator: string(typed.Operator),
			Value:    &value,
		}, nil
	case *ast.ExprStmt:
		value, err := ExprFromAST(typed.Value)
		if err != nil {
			return Stmt{}, err
		}
		return Stmt{
			Kind:  StmtKindExpr,
			Value: &value,
		}, nil
	case *ast.ReturnStmt:
		if typed.Value == nil {
			return Stmt{Kind: StmtKindReturn}, nil
		}
		value, err := ExprFromAST(typed.Value)
		if err != nil {
			return Stmt{}, err
		}
		return Stmt{
			Kind:  StmtKindReturn,
			Value: &value,
		}, nil
	default:
		return Stmt{}, fmt.Errorf("unsupported statement type: %T", statement)
	}
}

func ExprFromAST(expression ast.Expr) (Expr, error) {
	switch typed := expression.(type) {
	case *ast.IdentifierExpr:
		return Expr{Kind: ExprKindIdentifier, Name: typed.Name}, nil
	case *ast.StringLiteralExpr:
		return Expr{Kind: ExprKindStringLiteral, Value: typed.Value}, nil
	case *ast.NumberLiteralExpr:
		return Expr{Kind: ExprKindNumberLiteral, Value: typed.Value}, nil
	case *ast.CallExpr:
		callee, err := ExprFromAST(typed.Callee)
		if err != nil {
			return Expr{}, err
		}
		args := make([]Expr, 0, len(typed.Args))
		for _, argument := range typed.Args {
			convertedArgument, err := ExprFromAST(argument)
			if err != nil {
				return Expr{}, err
			}
			args = append(args, convertedArgument)
		}
		return Expr{
			Kind:   ExprKindCall,
			Callee: &callee,
			Args:   args,
		}, nil
	case *ast.SelectorExpr:
		target, err := ExprFromAST(typed.Target)
		if err != nil {
			return Expr{}, err
		}
		return Expr{
			Kind:   ExprKindSelector,
			Name:   typed.Name,
			Target: &target,
		}, nil
	case *ast.CompositeLiteralExpr:
		typeName := typed.Type.Name
		if typed.Type.Package != "" {
			typeName = typed.Type.Package + "." + typed.Type.Name
		}
		fields := make([]Field, 0, len(typed.Fields))
		for _, field := range typed.Fields {
			value, err := ExprFromAST(field.Value)
			if err != nil {
				return Expr{}, err
			}
			fields = append(fields, Field{Name: field.Name, Value: value})
		}
		return Expr{
			Kind:     ExprKindCompositeLiteral,
			TypeName: typeName,
			Fields:   fields,
		}, nil
	default:
		return Expr{}, fmt.Errorf("unsupported expression type: %T", expression)
	}
}
