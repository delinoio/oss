package main

import (
	"os"

	"github.com/delinoio/oss/cmds/thenv/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:]))
}
