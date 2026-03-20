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
	"github.com/delinoio/oss/cmds/ttlc/internal/ir"
)

type Program struct {
	Module    string         `json:"module"`
	EntryTask string         `json:"entry_task"`
	Args      map[string]any `json:"args"`
	Tasks     []ir.TaskDef   `json:"tasks"`
	Funcs     []ir.FuncDef   `json:"funcs"`
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

	tasks := make([]ir.TaskDef, 0, len(module.Decls))
	entryExists := false
	for _, declaration := range module.Decls {
		taskDeclaration, ok := declaration.(*ast.TaskDecl)
		if !ok {
			continue
		}
		serializedTask, err := ir.TaskFromDecl(taskDeclaration)
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

	funcs := make([]ir.FuncDef, 0)
	for _, declaration := range module.Decls {
		funcDeclaration, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		serializedFunc, err := ir.FuncFromDecl(funcDeclaration)
		if err != nil {
			return Program{}, err
		}
		funcs = append(funcs, serializedFunc)
	}

	sort.Slice(tasks, func(left int, right int) bool {
		return tasks[left].Name < tasks[right].Name
	})
	sort.Slice(funcs, func(left int, right int) bool {
		return funcs[left].Name < funcs[right].Name
	})
	return Program{
		Module:    module.PackageName,
		EntryTask: entryTask,
		Args:      cloneArgs(args),
		Tasks:     tasks,
		Funcs:     funcs,
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
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

const payloadBase64 = %q

type program struct {
	Module    string         `+"`json:\"module\"`"+`
	EntryTask string         `+"`json:\"entry_task\"`"+`
	Args      map[string]any `+"`json:\"args\"`"+`
	Tasks     []taskDef      `+"`json:\"tasks\"`"+`
	Funcs     []taskDef      `+"`json:\"funcs\"`"+`
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
	funcs         map[string]taskDef
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
	decoder := json.NewDecoder(bytes.NewReader(payloadBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&programData); err != nil {
		return program{}, fmt.Errorf("decode program json: %%w", err)
	}
	return programData, nil
}

func newEvaluator(programData program) *evaluator {
	tasks := make(map[string]taskDef, len(programData.Tasks))
	for _, task := range programData.Tasks {
		tasks[task.Name] = task
	}
	funcs := make(map[string]taskDef, len(programData.Funcs))
	for _, fn := range programData.Funcs {
		funcs[fn.Name] = fn
	}
	return &evaluator{
		tasks:         tasks,
		funcs:         funcs,
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

func (e *evaluator) executeFunc(funcID string, args []any) (any, error) {
	fn, exists := e.funcs[funcID]
	if !exists {
		return nil, fmt.Errorf("func not found: %%s", funcID)
	}
	if len(args) != len(fn.Params) {
		return nil, fmt.Errorf("func %%s argument count mismatch: got=%%d want=%%d", funcID, len(args), len(fn.Params))
	}
	for _, activeTask := range e.stack {
		if activeTask == funcID {
			cycle := append(append([]string{}, e.stack...), funcID)
			return nil, fmt.Errorf("cyclic func invocation detected: %%s", strings.Join(cycle, " -> "))
		}
	}

	e.stack = append(e.stack, funcID)
	defer func() {
		e.stack = e.stack[:len(e.stack)-1]
	}()

	scope := make(map[string]any, len(fn.Params))
	for index, parameter := range fn.Params {
		scope[parameter] = args[index]
	}

	for _, statement := range fn.Body {
		returned, value, err := e.evalStatement(scope, statement)
		if err != nil {
			return nil, fmt.Errorf("func %%s: %%w", funcID, err)
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
		numberValue := strings.TrimSpace(expression.Value)
		if numberValue == "" {
			return nil, fmt.Errorf("parse number literal %%q: empty value", expression.Value)
		}
		if !json.Valid([]byte("[" + numberValue + "]")) {
			return nil, fmt.Errorf("parse number literal %%q: invalid numeric literal", expression.Value)
		}
		return json.Number(numberValue), nil
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
	if task, exists := e.tasks[name]; exists {
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
	if fn, exists := e.funcs[name]; exists {
		callArgs := make([]any, 0, len(expression.Args))
		for _, argument := range expression.Args {
			value, err := e.evalExpression(scope, argument)
			if err != nil {
				return nil, err
			}
			callArgs = append(callArgs, value)
		}
		if len(callArgs) != len(fn.Params) {
			return nil, fmt.Errorf("func %%s argument count mismatch: got=%%d want=%%d", name, len(callArgs), len(fn.Params))
		}
		return e.executeFunc(name, callArgs)
	}

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
		payloadBuilder := strings.Builder{}
		for _, argument := range expression.Args {
			value, err := e.evalExpression(scope, argument)
			if err != nil {
				return nil, err
			}
			encodedArgument := stableValueString(value)
			payloadBuilder.WriteString(fmt.Sprintf("%%d:", len(encodedArgument)))
			payloadBuilder.WriteString(encodedArgument)
			payloadBuilder.WriteString(";")
		}
		sum := sha256.Sum256([]byte(payloadBuilder.String()))
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
		_, _ = fmt.Fprintln(os.Stderr, values...)
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported function call: %%s", name)
	}
}

func stableValueString(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		return "str:"+fmt.Sprintf("%%q", typed)
	case bool:
		if typed {
			return "bool:true"
		}
		return "bool:false"
	case json.Number:
		return "num:"+typed.String()
	case int, int8, int16, int32, int64:
		return fmt.Sprintf("num:%%v", typed)
	case uint, uint8, uint16, uint32, uint64, uintptr:
		return fmt.Sprintf("num:%%v", typed)
	case float32, float64:
		return fmt.Sprintf("num:%%v", typed)
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		builder := strings.Builder{}
		builder.WriteString("obj{")
		for _, key := range keys {
			encodedKey := fmt.Sprintf("%%q", key)
			encodedValue := stableValueString(typed[key])
			builder.WriteString(fmt.Sprintf("%%d:", len(encodedKey)))
			builder.WriteString(encodedKey)
			builder.WriteString("=")
			builder.WriteString(fmt.Sprintf("%%d:", len(encodedValue)))
			builder.WriteString(encodedValue)
			builder.WriteString(";")
		}
		builder.WriteString("}")
		return builder.String()
	case []any:
		builder := strings.Builder{}
		builder.WriteString("arr[")
		for _, item := range typed {
			encodedItem := stableValueString(item)
			builder.WriteString(fmt.Sprintf("%%d:", len(encodedItem)))
			builder.WriteString(encodedItem)
			builder.WriteString(";")
		}
		builder.WriteString("]")
		return builder.String()
	default:
		payload, err := json.Marshal(typed)
		if err == nil {
			return "json:"+string(payload)
		}
		return fmt.Sprintf("unknown:%%T:%%v", typed, typed)
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
	if stderrBuffer.Len() > 0 {
		_, _ = os.Stderr.Write(stderrBuffer.Bytes())
	}

	decodedResult := ExecutionResult{}
	decoder := json.NewDecoder(bytes.NewReader(stdoutBuffer.Bytes()))
	decoder.UseNumber()
	if err := decoder.Decode(&decodedResult); err != nil {
		return ExecutionResult{}, fmt.Errorf("decode generated runner output: %w", err)
	}
	if decodedResult.ExecutedTasks == nil {
		decodedResult.ExecutedTasks = make([]string, 0)
	}
	return decodedResult, nil
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
