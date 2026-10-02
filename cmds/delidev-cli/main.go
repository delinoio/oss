package main

import (
	"context"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if (filepath.Base(os.Args[0]) == "git" || strings.EqualFold(filepath.Base(os.Args[0]), "git.exe")) && os.Getenv("DELIDEV_PR_GIT_SCOPE") != "" {
		if err := workspace.RunPRGit(ctx, os.Getenv("DELIDEV_PR_GIT_SCOPE"), os.Args[1:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, domain.SafeError(err).Message)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) >= 4 && os.Args[1] == "internal-pr-git" && os.Args[3] == "--" {
		if err := workspace.RunPRGit(ctx, os.Args[2], os.Args[4:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, domain.SafeError(err).Message)
			os.Exit(1)
		}
		return
	}
	os.Exit(cli.Run(ctx, os.Args[1:], cli.IO{}))
}
