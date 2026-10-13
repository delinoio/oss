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

func TestManagedMCPRedirectQueryCannotUseAuthorizationResponseNames(t *testing.T) {
	profile := func(redirect string) ManagedMCPDefinition {
		return ManagedMCPDefinition{ID: NewID(), Revision: 1, MachineID: NewID(), WorkerDeviceID: NewID(), Name: "Fixture", Transport: MCPStreamableHTTP, Endpoint: "https://fixture.test/mcp", Authentication: MCPOAuthAuthentication, OAuth: &MCPOAuthProfile{ClientID: "public", AuthorizationURL: "https://fixture.test/auth", TokenURL: "https://fixture.test/token", RedirectURI: redirect}}
	}
	if err := profile("http://localhost/callback?tenant=a").Validate(); err != nil {
		t.Fatalf("a fixed redirect query was rejected: %v", err)
	}
	for _, redirect := range []string{"http://localhost/callback?state=x", "http://localhost/callback?tenant=a&code=x"} {
		if err := profile(redirect).Validate(); SafeError(err).Code != InvalidArgument {
			t.Fatalf("%s accepted a reserved query name: %v", redirect, err)
		}
	}
}

func TestManagedMCPSecretValuesMustBeLongEnoughForTheFrameGuard(t *testing.T) {
	if err := (MCPSecretInput{Environment: map[string]string{"TOKEN": "long-enough-value"}}).Validate(MCPStdio); err != nil {
		t.Fatalf("a suitable credential was rejected: %v", err)
	}
	for name, input := range map[string]struct {
		secrets   MCPSecretInput
		transport MCPTransport
	}{
		"environment": {MCPSecretInput{Environment: map[string]string{"TOKEN": "a"}}, MCPStdio},
		"header":      {MCPSecretInput{Headers: map[string]string{"X-Api-Key": "short"}}, MCPStreamableHTTP},
	} {
		if err := input.secrets.Validate(input.transport); SafeError(err).Code != InvalidArgument {
			t.Fatalf("%s: a value the guard cannot protect was accepted: %v", name, err)
		}
	}
}
