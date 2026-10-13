// SPDX-License-Identifier: Apache-2.0
package managedmcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// savedGeneration saves, optionally authenticates, and returns the Worker-owned
// generation with the Codex support stamped as the server would.
func savedGeneration(t *testing.T, m Manager, q domain.ManagedMCPRequest, d domain.ManagedMCPDefinition, secrets *domain.MCPSecretInput) domain.ManagedMCPDefinition {
	t.Helper()
	q.Definition = &d
	saved := must(t, m, q).Definitions[0]
	if secrets != nil {
		auth := q
		auth.RequestID = domain.NewID()
		auth.Action = domain.MCPAuthenticate
		auth.Definition = nil
		auth.DefinitionID = d.ID
		auth.ExpectedRevision = saved.Revision
		auth.Secrets = secrets
		saved = must(t, m, auth).Definitions[0]
	}
	saved.SupportedHarnesses = []domain.Harness{domain.Codex}
	return saved
}

func TestRuntimeResolvesOnlyTheFrozenWorkerGenerationAndPrivateCredential(t *testing.T) {
	m, q, d := fixture(t)
	generation := savedGeneration(t, m, q, d, &domain.MCPSecretInput{Environment: map[string]string{"TOKEN": "private-runtime-secret"}})
	resolved, err := m.Resolve(context.Background(), []domain.ManagedMCPDefinition{generation}, domain.Codex)
	if err != nil || len(resolved) != 1 {
		t.Fatalf("frozen generation did not resolve: %v", err)
	}
	if resolved[0].Environment["TOKEN"] != "private-runtime-secret" || resolved[0].Name != generation.NativeServerName() || resolved[0].Identity().Revision != generation.Revision || resolved[0].Identity().DefinitionID != d.ID {
		t.Fatalf("runtime lost identity or credential: %+v", resolved[0].Identity())
	}
	if values := resolved[0].ProtectedValues(); len(values) != 1 || values[0] != "private-runtime-secret" {
		t.Fatalf("protected values for redaction: %v", values)
	}
	raw, _ := json.Marshal(resolved[0].Definition)
	if strings.Contains(string(raw), "private-runtime-secret") {
		t.Fatal("definition serialization carried the secret")
	}
	resolved[0].Clear()
	if resolved[0].Environment != nil || len(resolved[0].ProtectedValues()) != 0 {
		t.Fatal("clear kept protected values")
	}
}

func TestRuntimeKeepsOriginalGenerationAfterEditsAndRejectsForgedOrForeignDefinitions(t *testing.T) {
	m, q, d := fixture(t)
	original := savedGeneration(t, m, q, d, &domain.MCPSecretInput{Environment: map[string]string{"TOKEN": "first"}})
	// A later authentication creates a newer generation; the frozen one remains valid.
	later := q
	later.RequestID = domain.NewID()
	later.Action = domain.MCPAuthenticate
	later.DefinitionID = d.ID
	later.ExpectedRevision = original.Revision
	later.Secrets = &domain.MCPSecretInput{Environment: map[string]string{"TOKEN": "second"}}
	must(t, m, later)
	resolved, err := m.Resolve(context.Background(), []domain.ManagedMCPDefinition{original}, domain.Codex)
	if err != nil || resolved[0].Environment["TOKEN"] != "first" {
		t.Fatalf("the execution did not keep its original credential generation: %v", err)
	}
	for name, mutate := range map[string]func(*domain.ManagedMCPDefinition){
		"changed-command":  func(g *domain.ManagedMCPDefinition) { g.Command = "/usr/bin/other" },
		"changed-revision": func(g *domain.ManagedMCPDefinition) { g.Revision += 10 },
		"changed-args":     func(g *domain.ManagedMCPDefinition) { g.Arguments = []string{"--evil"} },
		"foreign-worker":   func(g *domain.ManagedMCPDefinition) { g.WorkerDeviceID = domain.NewID() },
		"foreign-machine":  func(g *domain.ManagedMCPDefinition) { g.MachineID = domain.NewID() },
		"unsupported":      func(g *domain.ManagedMCPDefinition) { g.SupportedHarnesses = nil },
		"disabled":         func(g *domain.ManagedMCPDefinition) { g.Enabled = false },
		"other-credential": func(g *domain.ManagedMCPDefinition) { g.CredentialID = domain.NewID() },
	} {
		t.Run(name, func(t *testing.T) {
			forged := original
			mutate(&forged)
			if _, err := m.Resolve(context.Background(), []domain.ManagedMCPDefinition{forged}, domain.Codex); err == nil {
				t.Fatal("forged generation resolved")
			}
		})
	}
	other, _, _ := fixture(t)
	other.Root = m.Root
	if _, err := other.Resolve(context.Background(), []domain.ManagedMCPDefinition{original}, domain.Codex); err == nil {
		t.Fatal("another Worker adopted this Worker's definition")
	}
	if _, err := m.Resolve(context.Background(), []domain.ManagedMCPDefinition{original, original}, domain.Codex); err == nil {
		t.Fatal("duplicate selection resolved")
	}
	if _, err := m.Resolve(context.Background(), []domain.ManagedMCPDefinition{original}, domain.ClaudeCode); err == nil {
		t.Fatal("generation resolved for an unsupported harness")
	}
}

func TestRuntimeOAuthUsesBearerAndExpiredOrMissingCredentialsFailClosed(t *testing.T) {
	m, q, d := fixture(t)
	d.Transport, d.Command, d.Cwd, d.Endpoint = domain.MCPStreamableHTTP, "", "", "https://mcp.example.test/stream"
	d.Authentication = domain.MCPOAuthAuthentication
	d.OAuth = &domain.MCPOAuthProfile{ClientID: "client", AuthorizationURL: "https://mcp.example.test/authorize", TokenURL: "https://mcp.example.test/token", RedirectURI: "http://127.0.0.1:1234/callback"}
	saved := savedGeneration(t, m, q, d, nil)
	secrets := m.Secrets.(*fixtureSecrets)
	token, _ := json.Marshal(map[string]any{"token": map[string]any{"access_token": "oauth-access-token", "token_type": "Bearer", "refresh_token": "refresh", "expires_in": 3600, "scope": "x"}, "accepted_at": time.Now()})
	credential := domain.NewID()
	secrets.values[credentials.Ref{Owner: d.ID, ID: credential, Purpose: credentials.ManagedMCP}] = token
	// The vault entry is bound to the Worker's own immutable generation. Install
	// the authenticated generation exactly as OAuth completion would.
	c := authenticatedOAuthGeneration(t, m, saved, credential, time.Now().Add(time.Hour), saved.Revision+1)
	resolved, err := m.Resolve(context.Background(), []domain.ManagedMCPDefinition{c}, domain.Codex)
	if err != nil || resolved[0].BearerToken != "oauth-access-token" || len(resolved[0].Headers) != 0 || len(resolved[0].Environment) != 0 {
		t.Fatalf("oauth bearer was not resolved: %v", err)
	}
	expired := authenticatedOAuthGeneration(t, m, saved, credential, time.Now().Add(-time.Minute), saved.Revision+2)
	if _, err := m.Resolve(context.Background(), []domain.ManagedMCPDefinition{expired}, domain.Codex); err == nil {
		t.Fatal("expired oauth credential resolved")
	}
	secrets.values = map[credentials.Ref][]byte{}
	if _, err := m.Resolve(context.Background(), []domain.ManagedMCPDefinition{c}, domain.Codex); err == nil {
		t.Fatal("missing vault entry resolved")
	}
	m.Secrets = nil
	if _, err := m.Resolve(context.Background(), []domain.ManagedMCPDefinition{c}, domain.Codex); domain.SafeError(err).Code != domain.Unavailable {
		t.Fatalf("locked vault must be unavailable: %v", err)
	}
}

// authenticatedOAuthGeneration persists a Worker-owned authenticated revision
// the way a completed OAuth exchange does, then returns its stamped copy.
func authenticatedOAuthGeneration(t *testing.T, m Manager, base domain.ManagedMCPDefinition, credential domain.ID, expires time.Time, revision uint64) domain.ManagedMCPDefinition {
	t.Helper()
	generation := base
	generation.SupportedHarnesses = nil
	generation.CredentialID = credential
	generation.CredentialExpiresAt = expires
	generation.Revision = revision
	installGeneration(t, m, generation)
	generation.SupportedHarnesses = []domain.Harness{domain.Codex}
	return generation
}

func TestRuntimeLeavesNoCatalogChangeAndRequiresNoVaultForUnauthenticatedDefinitions(t *testing.T) {
	m, q, d := fixture(t)
	d.Authentication = domain.MCPNoAuthentication
	m.Secrets = nil
	generation := savedGeneration(t, m, q, d, nil)
	before := catalogBytes(t, m)
	resolved, err := m.Resolve(context.Background(), []domain.ManagedMCPDefinition{generation}, domain.Codex)
	if err != nil || len(resolved) != 1 || len(resolved[0].Environment) != 0 || resolved[0].BearerToken != "" {
		t.Fatalf("unauthenticated definition: %v", err)
	}
	if string(before) != string(catalogBytes(t, m)) {
		t.Fatal("execution resolution mutated the Worker catalog")
	}
	if none, err := m.Resolve(context.Background(), nil, domain.Codex); err != nil || none != nil {
		t.Fatal("empty selection must be a no-op")
	}
}

func catalogBytes(t *testing.T, m Manager) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(m.Root, "managed-mcp", string(m.ServerID), string(m.WorkerDeviceID), "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// installGeneration records an immutable generation exactly as the catalog owns
// it. Tests use it for states that need a live OAuth exchange to reach.
func installGeneration(t *testing.T, m Manager, generation domain.ManagedMCPDefinition) {
	t.Helper()
	var c catalog
	if err := json.Unmarshal(catalogBytes(t, m), &c); err != nil {
		t.Fatal(err)
	}
	c.Generations[generationKey(generation)] = generation
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomicOwned(filepath.Join(m.Root, "managed-mcp", string(m.ServerID), string(m.WorkerDeviceID), "catalog.json"), raw); err != nil {
		t.Fatal(err)
	}
}
