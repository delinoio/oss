// SPDX-License-Identifier: Apache-2.0
package main

import (
	"github.com/delinoio/oss/cmds/cachecow/internal/cli"
	"github.com/delinoio/oss/cmds/cachecow/internal/process"
	"github.com/delinoio/oss/cmds/cachecow/internal/session"
	"os"
)

var version = "0.1.0"

func main() {
	if code, handled := process.SupervisorMain(session.Hooks(cli.Validate, cli.Records())); handled {
		os.Exit(code)
	}
	os.Exit(cli.Main(version))
}
