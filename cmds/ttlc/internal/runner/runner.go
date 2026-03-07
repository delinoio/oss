package runner

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"

	"github.com/delinoio/oss/cmds/ttlc/internal/ast"
)

type Program struct {
	Module    string         `json:"module"`
	EntryTask string         `json:"entry_task"`
	Args      map[string]any `json:"args"`
	Tasks     []Task         `json:"tasks"`
}

type Task struct {
	Name   string   `json:"name"`
	Params []string `json:"params"`
	Body   []Stmt   `json:"body"`
}

type StmtKind string

const (
	StmtKindAssign StmtKind = "assign"
	StmtKindExpr   StmtKind = "expr"
	StmtKindReturn StmtKind = "return"
)

type Stmt struct {
	Kind     StmtKind `json:"kind"`
	Name     string   `json:"name,omitempty"`
	Operator string   `json:"operator,omitempty"`
	Value    *Expr    `json:"value,omitempty"`
}

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

type ExecutionResult struct {
	Result        any      `json:"result"`
	ExecutedTasks []string `json:"executed_tasks"`
}

func BuildProgram(module *ast.Module, entryTask string, args map[string]any) (Program, error) {
	if module == nil {
		return Program{}, fmt.Errorf("module is required")
	}
	if strings.TrimSpace(module.PackageName) == "" {
		return Program{}, fmt.Errorf("module package name is required")
	}
	if strings.TrimSpace(entryTask) == "" {
		return Program{}, fmt.Errorf("entry task is required")
	}

	tasks := make([]Task, 0, len(module.Decls))
	entryExists := false
	for _, declaration := range module.Decls {
		taskDeclaration, ok := declaration.(*ast.TaskDecl)
		if !ok {
			continue
		}
		serializedTask, err := taskFromDecl(taskDeclaration)
		if err != nil {
			return Program{}, err
		}
		if serializedTask.Name == entryTask {
			entryExists = true
		}
		tasks = append(tasks, serializedTask)
	}

	if !entryExists {
		return Program{}, fmt.Errorf("entry task not found: %s", entryTask)
	}

	sort.Slice(tasks, func(left int, right int) bool {
		return tasks[left].Name < tasks[right].Name
	})
	return Program{
		Module:    module.PackageName,
		EntryTask: entryTask,
		Args:      cloneArgs(args),
		Tasks:     tasks,
	}, nil
}

func GenerateGoSource(program Program) ([]byte, error) {
	if strings.TrimSpace(program.EntryTask) == "" {
		return nil, fmt.Errorf("program entry task is required")
	}
	if len(program.Tasks) == 0 {
		return nil, fmt.Errorf("program requires at least one task")
	}

	payload, err := json.Marshal(program)
	if err != nil {
		return nil, fmt.Errorf("marshal program payload: %w", err)
	}
	encodedPayload := base64.StdEncoding.EncodeToString(payload)

	source := fmt.Sprintf(`package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

const payloadBase64 = %q

type program struct {
	Module    string         `+"`json:\"module\"`"+`
	EntryTask string         `+"`json:\"entry_task\"`"+`
	Args      map[string]any `+"`json:\"args\"`"+`
	Tasks     []taskDef      `+"`json:\"tasks\"`"+`
}

type taskDef struct {
	Name   string      `+"`json:\"name\"`"+`
	Params []string    `+"`json:\"params\"`"+`
	Body   []statement `+"`json:\"body\"`"+`
}

type statement struct {
	Kind     string      `+"`json:\"kind\"`"+`
	Name     string      `+"`json:\"name,omitempty\"`"+`
	Operator string      `+"`json:\"operator,omitempty\"`"+`
	Value    *expression `+"`json:\"value,omitempty\"`"+`
}

type expression struct {
	Kind     string       `+"`json:\"kind\"`"+`
	Name     string       `+"`json:\"name,omitempty\"`"+`
	Value    string       `+"`json:\"value,omitempty\"`"+`
	TypeName string       `+"`json:\"type_name,omitempty\"`"+`
	Callee   *expression  `+"`json:\"callee,omitempty\"`"+`
	Target   *expression  `+"`json:\"target,omitempty\"`"+`
	Args     []expression `+"`json:\"args,omitempty\"`"+`
	Fields   []fieldValue `+"`json:\"fields,omitempty\"`"+`
}

type fieldValue struct {
	Name  string     `+"`json:\"name\"`"+`
	Value expression `+"`json:\"value\"`"+`
}

type evaluator struct {
	tasks         map[string]taskDef
	executedTasks []string
	stack         []string
}

func main() {
	programData, err := loadProgram()
	if err != nil {
		emitError(err)
		return
	}

	e := newEvaluator(programData)
	result, err := e.run(programData.EntryTask, programData.Args)
	if err != nil {
		emitError(err)
		return
	}

	response := map[string]any{
		"result":         result,
		"executed_tasks": e.executedTasks,
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(response); err != nil {
		emitError(err)
	}
}

func loadProgram() (program, error) {
	payloadBytes, err := base64.StdEncoding.DecodeString(payloadBase64)
	if err != nil {
		return program{}, fmt.Errorf("decode program payload: %%w", err)
	}
	programData := program{}
	if err := json.Unmarshal(payloadBytes, &programData); err != nil {
		return program{}, fmt.Errorf("decode program json: %%w", err)
	}
	return programData, nil
}

func newEvaluator(programData program) *evaluator {
	tasks := make(map[string]taskDef, len(programData.Tasks))
	for _, task := range programData.Tasks {
		tasks[task.Name] = task
	}
	return &evaluator{
		tasks:         tasks,
		executedTasks: make([]string, 0, len(programData.Tasks)),
		stack:         make([]string, 0, len(programData.Tasks)),
	}
}

func (e *evaluator) run(entryTask string, args map[string]any) (any, error) {
	task, exists := e.tasks[entryTask]
	if !exists {
		return nil, fmt.Errorf("entry task not found: %%s", entryTask)
	}
	orderedArgs := make([]any, 0, len(task.Params))
	for _, parameter := range task.Params {
		value, ok := args[parameter]
		if !ok {
			return nil, fmt.Errorf("missing argument: %%s", parameter)
		}
		orderedArgs = append(orderedArgs, value)
	}
	return e.executeTask(entryTask, orderedArgs)
}

func (e *evaluator) executeTask(taskID string, args []any) (any, error) {
	task, exists := e.tasks[taskID]
	if !exists {
		return nil, fmt.Errorf("task not found: %%s", taskID)
	}
	if len(args) != len(task.Params) {
		return nil, fmt.Errorf("task %%s argument count mismatch: got=%%d want=%%d", taskID, len(args), len(task.Params))
	}
	for _, activeTask := range e.stack {
		if activeTask == taskID {
			cycle := append(append([]string{}, e.stack...), taskID)
			return nil, fmt.Errorf("cyclic task invocation detected: %%s", strings.Join(cycle, " -> "))
		}
	}

	e.stack = append(e.stack, taskID)
	defer func() {
		e.stack = e.stack[:len(e.stack)-1]
	}()
	e.executedTasks = append(e.executedTasks, taskID)

	scope := make(map[string]any, len(task.Params))
	for index, parameter := range task.Params {
		scope[parameter] = args[index]
	}

	for _, statement := range task.Body {
		returned, value, err := e.evalStatement(scope, statement)
		if err != nil {
			return nil, fmt.Errorf("task %%s: %%w", taskID, err)
		}
		if returned {
			return value, nil
		}
	}
	return nil, nil
}

func (e *evaluator) evalStatement(scope map[string]any, statement statement) (bool, any, error) {
	switch statement.Kind {
	case "assign":
		if statement.Value == nil {
			return false, nil, fmt.Errorf("assignment requires a value expression")
		}
		value, err := e.evalExpression(scope, *statement.Value)
		if err != nil {
			return false, nil, err
		}
		scope[statement.Name] = value
		return false, nil, nil
	case "expr":
		if statement.Value == nil {
			return false, nil, nil
		}
		_, err := e.evalExpression(scope, *statement.Value)
		if err != nil {
			return false, nil, err
		}
		return false, nil, nil
	case "return":
		if statement.Value == nil {
			return true, nil, nil
		}
		value, err := e.evalExpression(scope, *statement.Value)
		if err != nil {
			return false, nil, err
		}
		return true, value, nil
	default:
		return false, nil, fmt.Errorf("unsupported statement kind: %%s", statement.Kind)
	}
}

func (e *evaluator) evalExpression(scope map[string]any, expression expression) (any, error) {
	switch expression.Kind {
	case "identifier":
		value, ok := scope[expression.Name]
		if !ok {
			return nil, fmt.Errorf("unknown identifier: %%s", expression.Name)
		}
		return value, nil
	case "string_literal":
		return expression.Value, nil
	case "number_literal":
		numberValue, err := strconv.ParseFloat(expression.Value, 64)
		if err != nil {
			return nil, fmt.Errorf("parse number literal %%q: %%w", expression.Value, err)
		}
		return numberValue, nil
	case "call":
		return e.evalCall(scope, expression)
	case "selector":
		if expression.Target == nil {
			return nil, fmt.Errorf("selector expression requires a target")
		}
		targetValue, err := e.evalExpression(scope, *expression.Target)
		if err != nil {
			return nil, err
		}
		targetObject, ok := targetValue.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("selector target is not an object for field %%s", expression.Name)
		}
		fieldValue, found := targetObject[expression.Name]
		if !found {
			return nil, fmt.Errorf("selector field not found: %%s", expression.Name)
		}
		return fieldValue, nil
	case "composite_literal":
		object := make(map[string]any, len(expression.Fields))
		for _, field := range expression.Fields {
			fieldValue, err := e.evalExpression(scope, field.Value)
			if err != nil {
				return nil, err
			}
			object[field.Name] = fieldValue
		}
		return object, nil
	default:
		return nil, fmt.Errorf("unsupported expression kind: %%s", expression.Kind)
	}
}

func (e *evaluator) evalCall(scope map[string]any, expression expression) (any, error) {
	if expression.Callee == nil {
		return nil, fmt.Errorf("call expression requires callee")
	}
	if expression.Callee.Kind != "identifier" {
		return nil, fmt.Errorf("unsupported call target kind: %%s", expression.Callee.Kind)
	}
	name := expression.Callee.Name
	switch name {
	case "vc":
		if len(expression.Args) != 1 {
			return nil, fmt.Errorf("vc(...) requires exactly one argument")
		}
		return e.evalExpression(scope, expression.Args[0])
	case "read":
		if len(expression.Args) != 1 {
			return nil, fmt.Errorf("read(...) requires exactly one argument")
		}
		return e.evalExpression(scope, expression.Args[0])
	case "hash":
		parts := make([]string, 0, len(expression.Args))
		for _, argument := range expression.Args {
			value, err := e.evalExpression(scope, argument)
			if err != nil {
				return nil, err
			}
			parts = append(parts, stableValueString(value))
		}
		payload := strings.Join(parts, "|")
		sum := sha256.Sum256([]byte(payload))
		return hex.EncodeToString(sum[:]), nil
	case "print":
		values := make([]any, 0, len(expression.Args))
		for _, argument := range expression.Args {
			value, err := e.evalExpression(scope, argument)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		fmt.Println(values...)
		return nil, nil
	default:
		task, exists := e.tasks[name]
		if !exists {
			return nil, fmt.Errorf("unsupported function call: %%s", name)
		}
		callArgs := make([]any, 0, len(expression.Args))
		for _, argument := range expression.Args {
			value, err := e.evalExpression(scope, argument)
			if err != nil {
				return nil, err
			}
			callArgs = append(callArgs, value)
		}
		if len(callArgs) != len(task.Params) {
			return nil, fmt.Errorf("task %%s argument count mismatch: got=%%d want=%%d", name, len(callArgs), len(task.Params))
		}
		return e.executeTask(name, callArgs)
	}
}

func stableValueString(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, key+"="+stableValueString(typed[key]))
		}
		return "{"+strings.Join(parts, ",")+"}"
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, stableValueString(item))
		}
		return "["+strings.Join(parts, ",")+"]"
	default:
		return fmt.Sprintf("%%v", typed)
	}
}

func emitError(err error) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(map[string]any{
		"error": err.Error(),
	})
	os.Exit(1)
}
`, encodedPayload)

	formatted, err := format.Source([]byte(source))
	if err != nil {
		return nil, fmt.Errorf("format generated runner: %w", err)
	}
	return formatted, nil
}

func Execute(ctx context.Context, outDir string, source []byte) (ExecutionResult, error) {
	if strings.TrimSpace(outDir) == "" {
		return ExecutionResult{}, fmt.Errorf("runner out-dir is required")
	}
	if len(source) == 0 {
		return ExecutionResult{}, fmt.Errorf("runner source is empty")
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return ExecutionResult{}, fmt.Errorf("create runner out-dir: %w", err)
	}
	if err := chmodDirectory(outDir); err != nil {
		return ExecutionResult{}, err
	}

	file, err := os.CreateTemp(outDir, "ttlc_run_*.go")
	if err != nil {
		return ExecutionResult{}, fmt.Errorf("create runner file: %w", err)
	}
	filePath := file.Name()
	if _, err := file.Write(source); err != nil {
		_ = file.Close()
		return ExecutionResult{}, fmt.Errorf("write runner file: %w", err)
	}
	if err := file.Close(); err != nil {
		return ExecutionResult{}, fmt.Errorf("close runner file: %w", err)
	}
	if err := chmodFile(filePath); err != nil {
		return ExecutionResult{}, err
	}
	defer func() {
		_ = os.Remove(filePath)
	}()

	command := exec.CommandContext(ctx, "go", "run", filePath)
	command.Dir = outDir
	command.Env = os.Environ()
	stdoutBuffer := &bytes.Buffer{}
	stderrBuffer := &bytes.Buffer{}
	command.Stdout = stdoutBuffer
	command.Stderr = stderrBuffer
	if err := command.Run(); err != nil {
		errorMessage := strings.TrimSpace(stderrBuffer.String())
		if runnerErrorMessage := decodeRunnerError(stdoutBuffer.Bytes()); runnerErrorMessage != "" {
			errorMessage = runnerErrorMessage
		}
		if errorMessage == "" {
			errorMessage = strings.TrimSpace(stdoutBuffer.String())
		}
		if errorMessage != "" {
			return ExecutionResult{}, fmt.Errorf("execute generated runner: %w: %s", err, errorMessage)
		}
		return ExecutionResult{}, fmt.Errorf("execute generated runner: %w", err)
	}

	decodedResult := ExecutionResult{}
	if err := json.Unmarshal(stdoutBuffer.Bytes(), &decodedResult); err != nil {
		return ExecutionResult{}, fmt.Errorf("decode generated runner output: %w", err)
	}
	if decodedResult.ExecutedTasks == nil {
		decodedResult.ExecutedTasks = make([]string, 0)
	}
	return decodedResult, nil
}

func taskFromDecl(declaration *ast.TaskDecl) (Task, error) {
	if declaration == nil {
		return Task{}, fmt.Errorf("task declaration is nil")
	}
	parameters := make([]string, 0, len(declaration.Parameters))
	for _, parameter := range declaration.Parameters {
		parameters = append(parameters, parameter.Name)
	}

	body := make([]Stmt, 0, len(declaration.Body))
	for _, statement := range declaration.Body {
		serializedStatement, err := stmtFromAST(statement)
		if err != nil {
			return Task{}, err
		}
		body = append(body, serializedStatement)
	}

	return Task{Name: declaration.Name, Params: parameters, Body: body}, nil
}

func stmtFromAST(statement ast.Stmt) (Stmt, error) {
	switch typed := statement.(type) {
	case *ast.AssignStmt:
		value, err := exprFromAST(typed.Value)
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
		value, err := exprFromAST(typed.Value)
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
		value, err := exprFromAST(typed.Value)
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

func exprFromAST(expression ast.Expr) (Expr, error) {
	switch typed := expression.(type) {
	case *ast.IdentifierExpr:
		return Expr{Kind: ExprKindIdentifier, Name: typed.Name}, nil
	case *ast.StringLiteralExpr:
		return Expr{Kind: ExprKindStringLiteral, Value: typed.Value}, nil
	case *ast.NumberLiteralExpr:
		return Expr{Kind: ExprKindNumberLiteral, Value: typed.Value}, nil
	case *ast.CallExpr:
		callee, err := exprFromAST(typed.Callee)
		if err != nil {
			return Expr{}, err
		}
		args := make([]Expr, 0, len(typed.Args))
		for _, argument := range typed.Args {
			convertedArgument, err := exprFromAST(argument)
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
		target, err := exprFromAST(typed.Target)
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
			value, err := exprFromAST(field.Value)
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

func decodeRunnerError(payload []byte) string {
	response := struct {
		Error string `json:"error"`
	}{}
	if err := json.Unmarshal(payload, &response); err != nil {
		return ""
	}
	return strings.TrimSpace(response.Error)
}

func cloneArgs(args map[string]any) map[string]any {
	if args == nil {
		return map[string]any{}
	}
	clone := make(map[string]any, len(args))
	for key, value := range args {
		clone[key] = value
	}
	return clone
}

func chmodDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("chmod runner out-dir: %w", err)
	}
	return nil
}

func chmodFile(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod runner file: %w", err)
	}
	return nil
}
