package sema

import (
	"fmt"
	"sort"

	"github.com/delinoio/oss/cmds/ttlc/internal/ast"
	"github.com/delinoio/oss/cmds/ttlc/internal/contracts"
	"github.com/delinoio/oss/cmds/ttlc/internal/diagnostic"
	"github.com/delinoio/oss/cmds/ttlc/internal/messages"
)

type TaskParam struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type TypeField struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type TypeDecl struct {
	Name   string      `json:"name"`
	Fields []TypeField `json:"fields"`
}

type Task struct {
	ID         string      `json:"id"`
	Params     []TaskParam `json:"params"`
	ReturnType string      `json:"return_type"`
	Deps       []string    `json:"deps"`
}

type FuncInfo struct {
	ID         string      `json:"id"`
	Params     []TaskParam `json:"params"`
	ReturnType string      `json:"return_type"`
}

type Result struct {
	Tasks        []Task
	Funcs        []FuncInfo
	Types        []TypeDecl
	Diagnostics  []diagnostic.Diagnostic
	ModuleName   string
	HasTaskFuncs bool
}

func Check(module *ast.Module) Result {
	result := Result{ModuleName: module.PackageName}

	for _, importDecl := range module.Imports {
		result.Diagnostics = append(result.Diagnostics, diagnostic.Diagnostic{
			Kind:    contracts.DiagnosticKindUnsupportedImport,
			Message: messages.FormatDiagnostic(messages.DiagnosticUnsupportedImportInSema, importDecl.Path),
			Line:    importDecl.Span.Start.Line,
			Column:  importDecl.Span.Start.Column,
		})
	}

	typeNames := make(map[string]struct{})
	for _, declaration := range module.Decls {
		typeDeclaration, ok := declaration.(*ast.TypeDecl)
		if !ok {
			continue
		}
		if _, exists := typeNames[typeDeclaration.Name]; exists {
			result.Diagnostics = append(result.Diagnostics, diagnostic.Diagnostic{
				Kind:    contracts.DiagnosticKindTypeError,
				Message: messages.FormatDiagnostic(messages.DiagnosticDuplicateTypeDeclaration, typeDeclaration.Name),
				Line:    typeDeclaration.Span.Start.Line,
				Column:  typeDeclaration.Span.Start.Column,
			})
			continue
		}
		typeNames[typeDeclaration.Name] = struct{}{}

		fields := make([]TypeField, 0, len(typeDeclaration.Fields))
		fieldNames := make(map[string]struct{}, len(typeDeclaration.Fields))
		for _, field := range typeDeclaration.Fields {
			if _, exists := fieldNames[field.Name]; exists {
				result.Diagnostics = append(result.Diagnostics, diagnostic.Diagnostic{
					Kind:    contracts.DiagnosticKindTypeError,
					Message: messages.FormatDiagnostic(messages.DiagnosticDuplicateStructFieldDeclaration, typeDeclaration.Name, field.Name),
					Line:    field.Span.Start.Line,
					Column:  field.Span.Start.Column,
				})
				continue
			}
			fieldNames[field.Name] = struct{}{}
			fields = append(fields, TypeField{
				Name: field.Name,
				Type: typeExprString(field.Type),
			})
		}
		result.Types = append(result.Types, TypeDecl{
			Name:   typeDeclaration.Name,
			Fields: fields,
		})
	}

	taskNames := make(map[string]struct{})
	duplicateTaskNames := make(map[string]struct{})
	for _, declaration := range module.Decls {
		taskDeclaration, ok := declaration.(*ast.TaskDecl)
		if !ok {
			continue
		}
		result.HasTaskFuncs = true
		if _, exists := taskNames[taskDeclaration.Name]; exists {
			duplicateTaskNames[taskDeclaration.Name] = struct{}{}
			result.Diagnostics = append(result.Diagnostics, diagnostic.Diagnostic{
				Kind:    contracts.DiagnosticKindTypeError,
				Message: messages.FormatDiagnostic(messages.DiagnosticDuplicateTaskDeclaration, taskDeclaration.Name),
				Line:    taskDeclaration.Span.Start.Line,
				Column:  taskDeclaration.Span.Start.Column,
			})
			continue
		}
		taskNames[taskDeclaration.Name] = struct{}{}
	}

	emittedTaskNames := make(map[string]struct{})
	for _, declaration := range module.Decls {
		taskDeclaration, ok := declaration.(*ast.TaskDecl)
		if !ok {
			continue
		}
		if _, isDuplicate := duplicateTaskNames[taskDeclaration.Name]; isDuplicate {
			if _, alreadyEmitted := emittedTaskNames[taskDeclaration.Name]; alreadyEmitted {
				continue
			}
		}
		if _, alreadyEmitted := emittedTaskNames[taskDeclaration.Name]; alreadyEmitted {
			continue
		}
		emittedTaskNames[taskDeclaration.Name] = struct{}{}

		parameterNames := make(map[string]struct{}, len(taskDeclaration.Parameters))
		parameterDiagnostics := make([]diagnostic.Diagnostic, 0, len(taskDeclaration.Parameters))
		uniqueParameters := make([]ast.Parameter, 0, len(taskDeclaration.Parameters))
		for _, parameter := range taskDeclaration.Parameters {
			if _, exists := parameterNames[parameter.Name]; exists {
				parameterDiagnostics = append(parameterDiagnostics, diagnostic.Diagnostic{
					Kind:    contracts.DiagnosticKindTypeError,
					Message: messages.FormatDiagnostic(messages.DiagnosticDuplicateTaskParameterName, taskDeclaration.Name, parameter.Name),
					Line:    parameter.Span.Start.Line,
					Column:  parameter.Span.Start.Column,
				})
				continue
			}
			parameterNames[parameter.Name] = struct{}{}
			uniqueParameters = append(uniqueParameters, parameter)
		}
		result.Diagnostics = append(result.Diagnostics, parameterDiagnostics...)

		if !isVcReturnType(taskDeclaration.ReturnType) {
			result.Diagnostics = append(result.Diagnostics, diagnostic.Diagnostic{
				Kind:    contracts.DiagnosticKindTypeError,
				Message: messages.FormatDiagnostic(messages.DiagnosticTaskMustReturnVC, taskDeclaration.Name),
				Line:    taskDeclaration.Span.Start.Line,
				Column:  taskDeclaration.Span.Start.Column,
			})
		}

		deps := make(map[string]struct{})
		for _, statement := range taskDeclaration.Body {
			walkStmt(statement, func(expression ast.Expr) {
				callExpression, ok := expression.(*ast.CallExpr)
				if !ok {
					return
				}
				calleeIdentifier, ok := callExpression.Callee.(*ast.IdentifierExpr)
				if !ok || calleeIdentifier.Name != "read" {
					return
				}
				if len(callExpression.Args) != 1 {
					result.Diagnostics = append(result.Diagnostics, diagnostic.Diagnostic{
						Kind:    contracts.DiagnosticKindTypeError,
						Message: messages.FormatDiagnostic(messages.DiagnosticReadRequiresOneArgument, len(callExpression.Args)),
						Line:    callExpression.Span.Start.Line,
						Column:  callExpression.Span.Start.Column,
					})
					return
				}
				dependencyCall, ok := callExpression.Args[0].(*ast.CallExpr)
				if !ok {
					return
				}
				dependencyName := callableTaskName(dependencyCall.Callee)
				if dependencyName == "" {
					return
				}
				if _, exists := taskNames[dependencyName]; exists {
					deps[dependencyName] = struct{}{}
				}
			})
		}

		result.Tasks = append(result.Tasks, Task{
			ID:         taskDeclaration.Name,
			Params:     parametersToTaskParams(uniqueParameters),
			ReturnType: typeExprString(taskDeclaration.ReturnType),
			Deps:       orderedKeys(deps),
		})
	}

	funcNames := make(map[string]struct{})
	duplicateFuncNames := make(map[string]struct{})
	for _, declaration := range module.Decls {
		funcDeclaration, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if _, exists := taskNames[funcDeclaration.Name]; exists {
			result.Diagnostics = append(result.Diagnostics, diagnostic.Diagnostic{
				Kind:    contracts.DiagnosticKindTypeError,
				Message: messages.FormatDiagnostic(messages.DiagnosticFunctionTaskNameCollision, funcDeclaration.Name, funcDeclaration.Name),
				Line:    funcDeclaration.Span.Start.Line,
				Column:  funcDeclaration.Span.Start.Column,
			})
			continue
		}
		if _, exists := funcNames[funcDeclaration.Name]; exists {
			duplicateFuncNames[funcDeclaration.Name] = struct{}{}
			result.Diagnostics = append(result.Diagnostics, diagnostic.Diagnostic{
				Kind:    contracts.DiagnosticKindTypeError,
				Message: messages.FormatDiagnostic(messages.DiagnosticDuplicateFunctionDeclaration, funcDeclaration.Name),
				Line:    funcDeclaration.Span.Start.Line,
				Column:  funcDeclaration.Span.Start.Column,
			})
			continue
		}
		funcNames[funcDeclaration.Name] = struct{}{}
	}

	emittedFuncNames := make(map[string]struct{})
	for _, declaration := range module.Decls {
		funcDeclaration, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if _, isDuplicate := duplicateFuncNames[funcDeclaration.Name]; isDuplicate {
			if _, alreadyEmitted := emittedFuncNames[funcDeclaration.Name]; alreadyEmitted {
				continue
			}
		}
		if _, alreadyEmitted := emittedFuncNames[funcDeclaration.Name]; alreadyEmitted {
			continue
		}
		if _, collidesWithTask := taskNames[funcDeclaration.Name]; collidesWithTask {
			continue
		}
		emittedFuncNames[funcDeclaration.Name] = struct{}{}

		parameterNames := make(map[string]struct{}, len(funcDeclaration.Parameters))
		for _, parameter := range funcDeclaration.Parameters {
			if _, exists := parameterNames[parameter.Name]; exists {
				result.Diagnostics = append(result.Diagnostics, diagnostic.Diagnostic{
					Kind:    contracts.DiagnosticKindTypeError,
					Message: messages.FormatDiagnostic(messages.DiagnosticDuplicateFunctionParameterName, funcDeclaration.Name, parameter.Name),
					Line:    parameter.Span.Start.Line,
					Column:  parameter.Span.Start.Column,
				})
				continue
			}
			parameterNames[parameter.Name] = struct{}{}
		}

		returnType := ""
		if funcDeclaration.ReturnType != nil {
			returnType = typeExprString(funcDeclaration.ReturnType)
		}

		result.Funcs = append(result.Funcs, FuncInfo{
			ID:         funcDeclaration.Name,
			Params:     parametersToTaskParams(funcDeclaration.Parameters),
			ReturnType: returnType,
		})
	}

	sort.Slice(result.Tasks, func(left int, right int) bool {
		return result.Tasks[left].ID < result.Tasks[right].ID
	})
	sort.Slice(result.Funcs, func(left int, right int) bool {
		return result.Funcs[left].ID < result.Funcs[right].ID
	})
	sort.Slice(result.Types, func(left int, right int) bool {
		return result.Types[left].Name < result.Types[right].Name
	})

	return result
}

func parametersToTaskParams(parameters []ast.Parameter) []TaskParam {
	results := make([]TaskParam, 0, len(parameters))
	for _, parameter := range parameters {
		results = append(results, TaskParam{Name: parameter.Name, Type: typeExprString(parameter.Type)})
	}
	return results
}

func isVcReturnType(typeExpr *ast.TypeExpr) bool {
	if typeExpr == nil {
		return false
	}
	if typeExpr.Package != "" {
		return false
	}
	if typeExpr.Name != "Vc" {
		return false
	}
	return len(typeExpr.TypeArgs) == 1
}

func callableTaskName(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.IdentifierExpr:
		return typed.Name
	default:
		return ""
	}
}

func orderedKeys(set map[string]struct{}) []string {
	results := make([]string, 0, len(set))
	for key := range set {
		results = append(results, key)
	}
	sort.Strings(results)
	return results
}

func typeExprString(typeExpr *ast.TypeExpr) string {
	if typeExpr == nil {
		return ""
	}
	name := typeExpr.Name
	if typeExpr.Package != "" {
		name = typeExpr.Package + "." + typeExpr.Name
	}
	if len(typeExpr.TypeArgs) == 0 {
		return name
	}
	parts := make([]string, 0, len(typeExpr.TypeArgs))
	for _, typeArg := range typeExpr.TypeArgs {
		parts = append(parts, typeExprString(typeArg))
	}
	return fmt.Sprintf("%s[%s]", name, join(parts, ", "))
}

func join(values []string, separator string) string {
	if len(values) == 0 {
		return ""
	}
	result := values[0]
	for index := 1; index < len(values); index++ {
		result += separator + values[index]
	}
	return result
}

func walkStmt(statement ast.Stmt, visit func(ast.Expr)) {
	switch typed := statement.(type) {
	case *ast.ReturnStmt:
		walkExpr(typed.Value, visit)
	case *ast.AssignStmt:
		walkExpr(typed.Value, visit)
	case *ast.ExprStmt:
		walkExpr(typed.Value, visit)
	}
}

func walkExpr(expression ast.Expr, visit func(ast.Expr)) {
	if expression == nil {
		return
	}
	visit(expression)
	switch typed := expression.(type) {
	case *ast.CallExpr:
		walkExpr(typed.Callee, visit)
		for _, argument := range typed.Args {
			walkExpr(argument, visit)
		}
	case *ast.SelectorExpr:
		walkExpr(typed.Target, visit)
	case *ast.CompositeLiteralExpr:
		for _, field := range typed.Fields {
			walkExpr(field.Value, visit)
		}
	}
}
