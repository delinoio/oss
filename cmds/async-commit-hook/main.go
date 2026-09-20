package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/delinoio/oss/cmds/async-commit-hook/internal/cli"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	cancel()
	os.Exit(code)
}
