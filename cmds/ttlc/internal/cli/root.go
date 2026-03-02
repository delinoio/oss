package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/delinoio/oss/cmds/ttlc/internal/compiler"
	"github.com/delinoio/oss/cmds/ttlc/internal/contracts"
	"github.com/delinoio/oss/cmds/ttlc/internal/diagnostic"
	"github.com/delinoio/oss/cmds/ttlc/internal/logging"
)

const (
	defaultEntryPath = "./main.ttl"
	defaultOutDir    = ".ttl/gen"
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
		return 1
	}

	service := newCompilerService(logger)
	result, err := service.Check(context.Background(), compiler.CheckOptions{Entry: entry})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "check failed: %v\n", err)
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
		return 1
	}

	service := newCompilerService(logger)
	result, err := service.Build(context.Background(), compiler.BuildOptions{Entry: entry, OutDir: outDir})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "build failed: %v\n", err)
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
		return 1
	}

	service := newCompilerService(logger)
	result, err := service.Explain(context.Background(), compiler.ExplainOptions{Entry: entry, Task: task})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "explain failed: %v\n", err)
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

func normalizeCacheAnalysis(values []compiler.CacheAnalysis) []compiler.CacheAnalysis {
	if values == nil {
		return make([]compiler.CacheAnalysis, 0)
	}
	return values
}

func printUsage(stderr io.Writer) {
	_, _ = fmt.Fprintln(stderr, "usage:")
	_, _ = fmt.Fprintln(stderr, "  ttlc build [--entry <file.ttl>] [--out-dir <dir>] [--no-color]")
	_, _ = fmt.Fprintln(stderr, "  ttlc check [--entry <file.ttl>] [--no-color]")
	_, _ = fmt.Fprintln(stderr, "  ttlc explain [--entry <file.ttl>] [--task <task-name>] [--no-color]")
}
