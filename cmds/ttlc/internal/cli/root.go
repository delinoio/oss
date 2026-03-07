package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/delinoio/oss/cmds/ttlc/internal/compiler"
	"github.com/delinoio/oss/cmds/ttlc/internal/contracts"
	"github.com/delinoio/oss/cmds/ttlc/internal/diagnostic"
	"github.com/delinoio/oss/cmds/ttlc/internal/logging"
)

const (
	defaultEntryPath = "./main.ttl"
	defaultOutDir    = ".ttl/gen"
	defaultRunArgs   = "{}"
)

var newCompilerService = compiler.NewWithLogger

type responseEnvelope struct {
	SchemaVersion contracts.TtlSchemaVersion  `json:"schema_version"`
	Command       contracts.TtlCommand        `json:"command"`
	Status        contracts.TtlResponseStatus `json:"status"`
	Diagnostics   []diagnostic.Diagnostic     `json:"diagnostics"`
	Data          any                         `json:"data"`
}

func Execute(args []string) int {
	return execute(args, os.Stdout, os.Stderr)
}

func execute(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch contracts.TtlCommand(args[0]) {
	case contracts.TtlCommandBuild:
		return executeBuild(args[1:], stdout, stderr)
	case contracts.TtlCommandCheck:
		return executeCheck(args[1:], stdout, stderr)
	case contracts.TtlCommandExplain:
		return executeExplain(args[1:], stdout, stderr)
	case contracts.TtlCommandRun:
		return executeRun(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func executeCheck(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet(string(contracts.TtlCommandCheck), flag.ContinueOnError)
	fs.SetOutput(stderr)

	entry := defaultEntryPath
	noColor := false
	fs.StringVar(&entry, "entry", defaultEntryPath, "entry .ttl file path")
	fs.BoolVar(&noColor, "no-color", false, "disable ANSI color output for logs")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	logger, err := logging.NewWithWriter(stderr, logging.Options{Level: "info", NoColor: noColor})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "init logger: %v\n", err)
		if envelopeErr := writeCommandFailureEnvelope(stdout, contracts.TtlCommandCheck, map[string]any{
			"entry":          entry,
			"cache_analysis": make([]compiler.CacheAnalysis, 0),
		}, err); envelopeErr != nil {
			_, _ = fmt.Fprintf(stderr, "encode output: %v\n", envelopeErr)
		}
		return 1
	}

	service := newCompilerService(logger)
	result, err := service.Check(context.Background(), compiler.CheckOptions{Entry: entry})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "check failed: %v\n", err)
		if envelopeErr := writeCommandFailureEnvelope(stdout, contracts.TtlCommandCheck, map[string]any{
			"entry":          entry,
			"cache_analysis": make([]compiler.CacheAnalysis, 0),
		}, err); envelopeErr != nil {
			_, _ = fmt.Fprintf(stderr, "encode output: %v\n", envelopeErr)
		}
		return 1
	}

	payload := map[string]any{
		"entry":                  result.Entry,
		"module":                 result.Module,
		"tasks":                  result.Tasks,
		"fingerprint_components": result.FingerprintComponents,
		"cache_analysis":         normalizeCacheAnalysis(result.CacheAnalysis),
	}
	status := statusFromDiagnostics(result.Diagnostics)
	if err := writeEnvelope(stdout, contracts.TtlCommandCheck, status, result.Diagnostics, payload); err != nil {
		_, _ = fmt.Fprintf(stderr, "encode output: %v\n", err)
		return 1
	}
	if status == contracts.TtlResponseStatusFailed {
		return 1
	}
	return 0
}

func executeBuild(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet(string(contracts.TtlCommandBuild), flag.ContinueOnError)
	fs.SetOutput(stderr)

	entry := defaultEntryPath
	outDir := defaultOutDir
	noColor := false
	fs.StringVar(&entry, "entry", defaultEntryPath, "entry .ttl file path")
	fs.StringVar(&outDir, "out-dir", defaultOutDir, "generated go output directory")
	fs.BoolVar(&noColor, "no-color", false, "disable ANSI color output for logs")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	logger, err := logging.NewWithWriter(stderr, logging.Options{Level: "info", NoColor: noColor})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "init logger: %v\n", err)
		if envelopeErr := writeCommandFailureEnvelope(stdout, contracts.TtlCommandBuild, map[string]any{
			"entry":          entry,
			"out_dir":        outDir,
			"cache_analysis": make([]compiler.CacheAnalysis, 0),
		}, err); envelopeErr != nil {
			_, _ = fmt.Fprintf(stderr, "encode output: %v\n", envelopeErr)
		}
		return 1
	}

	service := newCompilerService(logger)
	result, err := service.Build(context.Background(), compiler.BuildOptions{Entry: entry, OutDir: outDir})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "build failed: %v\n", err)
		if envelopeErr := writeCommandFailureEnvelope(stdout, contracts.TtlCommandBuild, map[string]any{
			"entry":          entry,
			"out_dir":        outDir,
			"cache_analysis": make([]compiler.CacheAnalysis, 0),
		}, err); envelopeErr != nil {
			_, _ = fmt.Fprintf(stderr, "encode output: %v\n", envelopeErr)
		}
		return 1
	}

	payload := map[string]any{
		"entry":                  result.Entry,
		"module":                 result.Module,
		"tasks":                  result.Tasks,
		"fingerprint_components": result.FingerprintComponents,
		"generated_files":        result.GeneratedFiles,
		"cache_db_path":          result.CacheDBPath,
		"cache_analysis":         normalizeCacheAnalysis(result.CacheAnalysis),
	}
	status := statusFromDiagnostics(result.Diagnostics)
	if err := writeEnvelope(stdout, contracts.TtlCommandBuild, status, result.Diagnostics, payload); err != nil {
		_, _ = fmt.Fprintf(stderr, "encode output: %v\n", err)
		return 1
	}
	if status == contracts.TtlResponseStatusFailed {
		return 1
	}
	return 0
}

func executeExplain(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet(string(contracts.TtlCommandExplain), flag.ContinueOnError)
	fs.SetOutput(stderr)

	entry := defaultEntryPath
	task := ""
	noColor := false
	fs.StringVar(&entry, "entry", defaultEntryPath, "entry .ttl file path")
	fs.StringVar(&task, "task", "", "task name to explain")
	fs.BoolVar(&noColor, "no-color", false, "disable ANSI color output for logs")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	logger, err := logging.NewWithWriter(stderr, logging.Options{Level: "info", NoColor: noColor})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "init logger: %v\n", err)
		if envelopeErr := writeCommandFailureEnvelope(stdout, contracts.TtlCommandExplain, map[string]any{
			"entry":          entry,
			"task":           task,
			"cache_analysis": make([]compiler.CacheAnalysis, 0),
		}, err); envelopeErr != nil {
			_, _ = fmt.Fprintf(stderr, "encode output: %v\n", envelopeErr)
		}
		return 1
	}

	service := newCompilerService(logger)
	result, err := service.Explain(context.Background(), compiler.ExplainOptions{Entry: entry, Task: task})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "explain failed: %v\n", err)
		if envelopeErr := writeCommandFailureEnvelope(stdout, contracts.TtlCommandExplain, map[string]any{
			"entry":          entry,
			"task":           task,
			"cache_analysis": make([]compiler.CacheAnalysis, 0),
		}, err); envelopeErr != nil {
			_, _ = fmt.Fprintf(stderr, "encode output: %v\n", envelopeErr)
		}
		return 1
	}

	payload := map[string]any{
		"entry":                  result.Entry,
		"module":                 result.Module,
		"tasks":                  result.Tasks,
		"fingerprint_components": result.FingerprintComponents,
		"cache_analysis":         normalizeCacheAnalysis(result.CacheAnalysis),
	}
	status := statusFromDiagnostics(result.Diagnostics)
	if err := writeEnvelope(stdout, contracts.TtlCommandExplain, status, result.Diagnostics, payload); err != nil {
		_, _ = fmt.Fprintf(stderr, "encode output: %v\n", err)
		return 1
	}
	if status == contracts.TtlResponseStatusFailed {
		return 1
	}
	return 0
}

func executeRun(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet(string(contracts.TtlCommandRun), flag.ContinueOnError)
	fs.SetOutput(stderr)

	entry := defaultEntryPath
	task := ""
	rawArgs := defaultRunArgs
	noColor := false
	fs.StringVar(&entry, "entry", defaultEntryPath, "entry .ttl file path")
	fs.StringVar(&task, "task", "", "task name to run")
	fs.StringVar(&rawArgs, "args", defaultRunArgs, "json object arguments for the selected task")
	fs.BoolVar(&noColor, "no-color", false, "disable ANSI color output for logs")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	parsedArgs, parseErr := parseRunArgsJSON(rawArgs)
	if parseErr != nil {
		diagnostics := []diagnostic.Diagnostic{
			{
				Kind:    contracts.DiagnosticKindTypeError,
				Message: parseErr.Error(),
				Line:    1,
				Column:  1,
			},
		}
		payload := map[string]any{
			"entry":          entry,
			"task":           task,
			"args":           map[string]any{},
			"result":         nil,
			"run_trace":      make([]string, 0),
			"cache_analysis": make([]compiler.CacheAnalysis, 0),
		}
		if err := writeEnvelope(stdout, contracts.TtlCommandRun, contracts.TtlResponseStatusFailed, diagnostics, payload); err != nil {
			_, _ = fmt.Fprintf(stderr, "encode output: %v\n", err)
		}
		return 1
	}

	if strings.TrimSpace(task) == "" {
		diagnostics := []diagnostic.Diagnostic{
			{
				Kind:    contracts.DiagnosticKindTypeError,
				Message: "--task is required for run command",
				Line:    1,
				Column:  1,
			},
		}
		payload := map[string]any{
			"entry":          entry,
			"task":           task,
			"args":           parsedArgs,
			"result":         nil,
			"run_trace":      make([]string, 0),
			"cache_analysis": make([]compiler.CacheAnalysis, 0),
		}
		if err := writeEnvelope(stdout, contracts.TtlCommandRun, contracts.TtlResponseStatusFailed, diagnostics, payload); err != nil {
			_, _ = fmt.Fprintf(stderr, "encode output: %v\n", err)
		}
		return 1
	}

	logger, err := logging.NewWithWriter(stderr, logging.Options{Level: "info", NoColor: noColor})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "init logger: %v\n", err)
		if envelopeErr := writeCommandFailureEnvelope(stdout, contracts.TtlCommandRun, map[string]any{
			"entry":          entry,
			"task":           task,
			"args":           parsedArgs,
			"result":         nil,
			"run_trace":      make([]string, 0),
			"cache_analysis": make([]compiler.CacheAnalysis, 0),
		}, err); envelopeErr != nil {
			_, _ = fmt.Fprintf(stderr, "encode output: %v\n", envelopeErr)
		}
		return 1
	}

	service := newCompilerService(logger)
	result, err := service.Run(context.Background(), compiler.RunOptions{
		Entry: entry,
		Task:  task,
		Args:  parsedArgs,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "run failed: %v\n", err)
		if envelopeErr := writeCommandFailureEnvelope(stdout, contracts.TtlCommandRun, map[string]any{
			"entry":          entry,
			"task":           task,
			"args":           parsedArgs,
			"result":         nil,
			"run_trace":      make([]string, 0),
			"cache_analysis": make([]compiler.CacheAnalysis, 0),
		}, err); envelopeErr != nil {
			_, _ = fmt.Fprintf(stderr, "encode output: %v\n", envelopeErr)
		}
		return 1
	}

	payload := map[string]any{
		"entry":          result.Entry,
		"module":         result.Module,
		"task":           result.Task,
		"args":           result.Args,
		"result":         result.RunResult,
		"run_trace":      normalizeRunTrace(result.RunTrace),
		"cache_analysis": normalizeCacheAnalysis(result.CacheAnalysis),
	}
	status := statusFromDiagnostics(result.Diagnostics)
	if err := writeEnvelope(stdout, contracts.TtlCommandRun, status, result.Diagnostics, payload); err != nil {
		_, _ = fmt.Fprintf(stderr, "encode output: %v\n", err)
		return 1
	}
	if status == contracts.TtlResponseStatusFailed {
		return 1
	}
	return 0
}

func statusFromDiagnostics(diagnostics []diagnostic.Diagnostic) contracts.TtlResponseStatus {
	if len(diagnostics) > 0 {
		return contracts.TtlResponseStatusFailed
	}
	return contracts.TtlResponseStatusOK
}

func writeEnvelope(stdout io.Writer, command contracts.TtlCommand, status contracts.TtlResponseStatus, diagnostics []diagnostic.Diagnostic, data any) error {
	responseDiagnostics := diagnostics
	if responseDiagnostics == nil {
		responseDiagnostics = make([]diagnostic.Diagnostic, 0)
	}
	response := responseEnvelope{
		SchemaVersion: contracts.TtlSchemaVersionV1Alpha1,
		Command:       command,
		Status:        status,
		Diagnostics:   responseDiagnostics,
		Data:          data,
	}
	return json.NewEncoder(stdout).Encode(response)
}

func writeCommandFailureEnvelope(stdout io.Writer, command contracts.TtlCommand, data any, commandErr error) error {
	return writeEnvelope(stdout, command, contracts.TtlResponseStatusFailed, []diagnostic.Diagnostic{
		{
			Kind:    contracts.DiagnosticKindIOError,
			Message: commandErr.Error(),
			Line:    1,
			Column:  1,
		},
	}, data)
}

func normalizeCacheAnalysis(values []compiler.CacheAnalysis) []compiler.CacheAnalysis {
	if values == nil {
		return make([]compiler.CacheAnalysis, 0)
	}
	return values
}

func normalizeRunTrace(values []string) []string {
	if values == nil {
		return make([]string, 0)
	}
	return values
}

func parseRunArgsJSON(raw string) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()

	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("parse --args JSON object: %w", err)
	}
	parsed, ok := decoded.(map[string]any)
	if !ok || parsed == nil {
		return nil, fmt.Errorf("parse --args JSON object: expected JSON object")
	}
	trailing := struct{}{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("parse --args JSON object: unexpected trailing tokens")
	}
	return parsed, nil
}

func printUsage(stderr io.Writer) {
	_, _ = fmt.Fprintln(stderr, "usage:")
	_, _ = fmt.Fprintln(stderr, "  ttlc build [--entry <file.ttl>] [--out-dir <dir>] [--no-color]")
	_, _ = fmt.Fprintln(stderr, "  ttlc check [--entry <file.ttl>] [--no-color]")
	_, _ = fmt.Fprintln(stderr, "  ttlc explain [--entry <file.ttl>] [--task <task-name>] [--no-color]")
	_, _ = fmt.Fprintln(stderr, "  ttlc run [--entry <file.ttl>] --task <task-name> [--args <json>] [--no-color]")
}
