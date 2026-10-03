// SPDX-License-Identifier: Apache-2.0
// Command guidance generates the fixed native provider-help allowlist.
package main

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"os"
)

func main() {
	raw, err := providers.GuidanceJSON()
	if err != nil {
		os.Exit(1)
	}
	if _, err = os.Stdout.Write(raw); err != nil {
		os.Exit(1)
	}
}
