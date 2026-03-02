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
)

const (
	defaultEntryPath = "./main.ttl"
	defaultOutDir    = ".ttl/gen"
)

var newCompilerService = compiler.New

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
	fs.StringVar(&entry, "entry", defaultEntryPath, "entry .ttl file path")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	service := newCompilerService()
	result, err := service.Check(context.Background(), compiler.CheckOptions{Entry: entry})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "check failed: %v\n", err)
		return 1
	}

	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"entry":       result.Entry,
		"module":      result.Module,
		"diagnostics": result.Diagnostics,
	})
	return 0
}

func executeBuild(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet(string(contracts.TtlCommandBuild), flag.ContinueOnError)
	fs.SetOutput(stderr)

	entry := defaultEntryPath
	outDir := defaultOutDir
	fs.StringVar(&entry, "entry", defaultEntryPath, "entry .ttl file path")
	fs.StringVar(&outDir, "out-dir", defaultOutDir, "generated go output directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	service := newCompilerService()
	result, err := service.Build(context.Background(), compiler.BuildOptions{Entry: entry, OutDir: outDir})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "build failed: %v\n", err)
		return 1
	}

	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"entry":       result.Entry,
		"module":      result.Module,
		"outDir":      outDir,
		"diagnostics": result.Diagnostics,
	})
	return 0
}

func executeExplain(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet(string(contracts.TtlCommandExplain), flag.ContinueOnError)
	fs.SetOutput(stderr)

	entry := defaultEntryPath
	task := ""
	fs.StringVar(&entry, "entry", defaultEntryPath, "entry .ttl file path")
	fs.StringVar(&task, "task", "", "task name to explain")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	service := newCompilerService()
	result, err := service.Explain(context.Background(), compiler.ExplainOptions{Entry: entry, Task: task})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "explain failed: %v\n", err)
		return 1
	}

	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"entry":       result.Entry,
		"module":      result.Module,
		"diagnostics": result.Diagnostics,
	})
	return 0
}

func printUsage(stderr io.Writer) {
	_, _ = fmt.Fprintln(stderr, "usage:")
	_, _ = fmt.Fprintln(stderr, "  ttlc build [--entry <file.ttl>] [--out-dir <dir>]")
	_, _ = fmt.Fprintln(stderr, "  ttlc check [--entry <file.ttl>]")
	_, _ = fmt.Fprintln(stderr, "  ttlc explain [--entry <file.ttl>] [--task <task-name>]")
}
