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

func TestManagedMCPSecretPayloadMustFitTheVaultBound(t *testing.T) {
	values := func(count, size int) map[string]string {
		result := map[string]string{}
		for i := 0; i < count; i++ {
			result[strings.Repeat("K", 8)+string(rune('A'+i))] = strings.Repeat("v", size)
		}
		return result
	}
	if err := (MCPSecretInput{Environment: values(4, 8192)}).Validate(MCPStdio); err != nil {
		t.Fatalf("a payload within the bound was rejected: %v", err)
	}
	// Every value is individually legal, but the serialized object exceeds the vault bound.
	if err := (MCPSecretInput{Environment: values(9, 8192)}).Validate(MCPStdio); SafeError(err).Code != InvalidArgument {
		t.Fatalf("an oversized aggregate was accepted: %v", err)
	}
}
