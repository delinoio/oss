package main

import (
	"os"

	"github.com/delinoio/oss/cmds/runmoor/internal/runmoor"
)

func main() { os.Exit(runmoor.Execute(os.Args[1:], os.Stdout, os.Stderr)) }
