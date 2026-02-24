package main

import (
	"os"

	"github.com/delinoio/oss/cmds/commit-tracker/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:]))
}
