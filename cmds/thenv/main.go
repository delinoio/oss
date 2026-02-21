package main

import (
	"fmt"
	"os"

	"github.com/delinoio/oss/cmds/thenv/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "thenv: %v\n", err)
		os.Exit(1)
	}
}
