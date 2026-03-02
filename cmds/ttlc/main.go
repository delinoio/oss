package main

import (
	"os"

	"github.com/delinoio/oss/cmds/ttlc/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:]))
}
