// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedMCPStdioWorkingDirectoryIsBoundedText(t *testing.T) {
	base := t.TempDir()
	definition := func(cwd string) ManagedMCPDefinition {
		return ManagedMCPDefinition{ID: NewID(), Revision: 1, MachineID: NewID(), WorkerDeviceID: NewID(), Name: "Fixture", Transport: MCPStdio, Command: filepath.Join(base, "server"), Cwd: cwd, Authentication: MCPNoAuthentication}
	}
	if e := definition(base).Validate(); e != nil {
		t.Fatalf("valid working directory rejected: %v", e)
	}
	for name, cwd := range map[string]string{
		"nul":         base + "/bad\x00dir",
		"too long":    base + "/" + strings.Repeat("d", 4096),
		"invalid utf": base + "/\xff",
		"relative":    "relative/dir",
		"empty":       "",
	} {
		t.Run(name, func(t *testing.T) {
			if e := definition(cwd).Validate(); SafeError(e).Code != InvalidArgument {
				t.Fatalf("accepted invalid working directory: %v", e)
			}
		})
	}
}
