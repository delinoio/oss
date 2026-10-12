// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mcpFixture(t *testing.T) (*Manager, ReadRequest) {
	t.Helper()
	machine := domain.NewID()
	return &Manager{Root: t.TempDir()}, ReadRequest{ID: domain.NewID(), Deadline: time.Now().Add(time.Minute), Preparation: PrepareRequest{MachineID: machine}, MCP: &domain.MCPRequest{ServerID: domain.NewID(), ActorID: domain.NewID(), MachineID: machine, WorkerDeviceID: domain.NewID(), WorkerInstanceID: domain.NewID(), RequestID: domain.NewID(), Mutation: domain.MCPMutation{Version: 1, Action: domain.MCPList}}}
}
func TestMCPManagementRetainsOriginalGenerationsWithoutLaunching(t *testing.T) {
	m, q := mcpFixture(t)
	ctx := context.Background()
	id := domain.NewID()
	marker := filepath.Join(m.Root, "must-not-run")
	q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPSave, DefinitionID: id, Definition: &domain.MCPDefinition{Name: "Fixture MCP", Enabled: true, Transport: domain.MCPStdio, Executable: marker, Support: []domain.MCPHarnessSupport{{Harness: domain.Codex, Verified: true, Profile: "forged"}}, CredentialID: domain.NewID(), Authentication: domain.MCPAuthenticated}}
	first, err := m.ReadMCP(ctx, q)
	if err != nil || first.State != domain.MCPSucceeded || len(first.Definitions) != 1 {
		t.Fatal(first, err)
	}
	original := first.Definitions[0]
	if original.Revision != 1 || original.CredentialID != "" || len(original.Support) != 0 {
		t.Fatal("configuration manufactured credential or native authority")
	}
	replay, err := m.ReadMCP(ctx, q)
	if err != nil || replay.RequestID != first.RequestID {
		t.Fatal("same receipt did not recover", err)
	}
	q.MCP.RequestID = domain.NewID()
	q.MCP.Mutation.ExpectedRevision = 1
	q.MCP.Mutation.Definition.Enabled = false
	second, err := m.ReadMCP(ctx, q)
	if err != nil || second.Definitions[0].Revision != 2 || second.Definitions[0].Enabled {
		t.Fatal(second, err)
	}
	q.MCP.RequestID = domain.NewID()
	q.MCP.Mutation.ExpectedRevision = 1
	if _, err := m.ReadMCP(ctx, q); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("stale revision was not rejected", err)
	}
	q.MCP.RequestID = domain.NewID()
	q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPDelete, DefinitionID: id, ExpectedRevision: 2, Confirmed: true}
	deleted, err := m.ReadMCP(ctx, q)
	if err != nil || len(deleted.Definitions) != 0 {
		t.Fatal(deleted, err)
	}
	raw, err := security.ReadPrivate(filepath.Join(m.Root, "mcp", string(q.MCP.ServerID), string(q.MCP.WorkerDeviceID), "catalog.json"), 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	var retained mcpCatalog
	if json.Unmarshal(raw, &retained) != nil || retained.Generations[string(id)+":1"].Revision != 1 || retained.Generations[string(id)+":2"].Revision != 2 {
		t.Fatal("deletion discarded original generations")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("management launched the configured executable")
	}
}
func TestMCPManagementScopesAndAbsentOriginalReceipt(t *testing.T) {
	m, q := mcpFixture(t)
	ctx := context.Background()
	original := q.MCP.WorkerDeviceID
	q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPSave, DefinitionID: domain.NewID(), Definition: &domain.MCPDefinition{Name: "HTTP fixture", Enabled: true, Transport: domain.MCPStreamableHTTP, Endpoint: "https://mcp.example.invalid/rpc"}}
	if _, err := m.ReadMCP(ctx, q); err != nil {
		t.Fatal(err)
	}
	q.MCP.WorkerDeviceID = domain.NewID()
	q.MCP.RequestID = domain.NewID()
	q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPList}
	result, err := m.ReadMCP(ctx, q)
	if err != nil || len(result.Definitions) != 0 {
		t.Fatal("cross-Worker catalog leaked", err)
	}
	q.MCP.WorkerDeviceID = original
	q.MCP.RequestID = domain.NewID()
	q.MCP.Mutation.Action = domain.MCPInspectMutation
	missing, err := m.ReadMCP(ctx, q)
	if err != nil || missing.State != domain.MCPFailed {
		t.Fatal("absent original delivery not fenced", err)
	}
	q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPSave, DefinitionID: domain.NewID(), Definition: &domain.MCPDefinition{Name: "replacement", Transport: domain.MCPStdio, Executable: "not-run"}}
	if _, err := m.ReadMCP(ctx, q); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("absent original identity was replayed", err)
	}
	q.MCP.MachineID = domain.NewID()
	if _, err := m.ReadMCP(ctx, q); err == nil {
		t.Fatal("foreign machine request accepted")
	}
}
func TestMCPManagementPendingReceiptNeverReplays(t *testing.T) {
	m, q := mcpFixture(t)
	root := filepath.Join(m.Root, "mcp", string(q.MCP.ServerID), string(q.MCP.WorkerDeviceID))
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	q.MCP.RequestID = domain.NewID()
	catalog := mcpCatalog{Version: 1, ServerID: q.MCP.ServerID, WorkerDeviceID: q.MCP.WorkerDeviceID, Definitions: map[domain.ID]domain.MCPDefinition{}, Generations: map[string]domain.MCPDefinition{}, Receipts: map[domain.ID]mcpReceipt{q.MCP.RequestID: {ActorID: q.MCP.ActorID, Result: domain.MCPResult{WorkerDeviceID: q.MCP.WorkerDeviceID, RequestID: q.MCP.RequestID, State: domain.MCPPending, Definitions: []domain.MCPDefinition{}}}}}
	raw, _ := json.Marshal(catalog)
	if err := security.WriteAtomic(filepath.Join(root, "catalog.json"), raw); err != nil {
		t.Fatal(err)
	}
	q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPInspectMutation}
	result, err := m.ReadMCP(context.Background(), q)
	if err != nil || result.State != domain.MCPRecoveryRequired {
		t.Fatal("unknown outcome became replay authority", err)
	}
	q.MCP.ActorID = domain.NewID()
	if _, err := m.ReadMCP(context.Background(), q); domain.SafeError(err).Code != domain.PermissionDenied {
		t.Fatal("foreign receipt owner accepted", err)
	}
}

type fixtureMCPVault struct{ secrets map[credentials.Ref][]byte }

func (v *fixtureMCPVault) Put(_ context.Context, r credentials.Ref, raw []byte) (string, error) {
	if v.secrets == nil {
		v.secrets = map[credentials.Ref][]byte{}
	}
	v.secrets[r] = append([]byte(nil), raw...)
	return "fixture", nil
}
func (v *fixtureMCPVault) Get(_ context.Context, r credentials.Ref) ([]byte, error) {
	return append([]byte(nil), v.secrets[r]...), nil
}
func (*fixtureMCPVault) Close() error { return nil }
func TestMCPOAuthOwnershipOnceOnlyExchangeAndSecretProjection(t *testing.T) {
	m, q := mcpFixture(t)
	vault := &fixtureMCPVault{}
	m.mcpOpenVault = func(string, domain.ID) (mcpSecretVault, error) { return vault, nil }
	exchanges := 0
	m.mcpExchange = func(_ context.Context, _ domain.MCPOAuthConfiguration, code, verifier string) (domain.MCPSecrets, error) {
		exchanges++
		if code != "original-code" || verifier == "" {
			t.Fatal("wrong immutable OAuth input")
		}
		return domain.MCPSecrets{AccessToken: "private-token", RefreshToken: "private-refresh"}, nil
	}
	id := domain.NewID()
	q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPSave, DefinitionID: id, Definition: &domain.MCPDefinition{Name: "OAuth fixture", Enabled: true, Transport: domain.MCPStreamableHTTP, Endpoint: "https://mcp.example.invalid/rpc", OAuth: &domain.MCPOAuthConfiguration{AuthorizationURL: "https://auth.example.invalid/authorize", TokenURL: "https://auth.example.invalid/token", ClientID: "public-client", RedirectURI: "http://127.0.0.1/callback"}}}
	if _, err := m.ReadMCP(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	q.MCP.RequestID = domain.NewID()
	q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPOAuthStart, DefinitionID: id, ExpectedRevision: 1}
	start, err := m.ReadMCP(context.Background(), q)
	if err != nil || start.State != domain.MCPPending || exchanges != 0 {
		t.Fatal("opening authentication exchanged tokens", err)
	}
	authURL, _ := url.Parse(start.AuthorizationURL)
	original := q.MCP.RequestID
	q.MCP.RequestID = domain.NewID()
	q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPOAuthCallback, DefinitionID: id, ExpectedRevision: 1, OriginalRequestID: original, CallbackURI: "http://127.0.0.1/wrong?state=" + url.QueryEscape(authURL.Query().Get("state")) + "&code=original-code"}
	wrong, err := m.ReadMCP(context.Background(), q)
	if err != nil || wrong.State != domain.MCPFailed || exchanges != 0 {
		t.Fatal("foreign callback exchanged", err)
	}
	q.MCP.RequestID = domain.NewID()
	q.MCP.Mutation.CallbackURI = strings.Replace(q.MCP.Mutation.CallbackURI, "/wrong?", "/callback?", 1)
	complete, err := m.ReadMCP(context.Background(), q)
	if err != nil || complete.State != domain.MCPSucceeded || exchanges != 1 || complete.Definitions[0].Authentication != domain.MCPAuthenticated {
		t.Fatal(complete, err)
	}
	if _, err := m.ReadMCP(context.Background(), q); err != nil || exchanges != 1 {
		t.Fatal("response loss duplicated exchange", err)
	}
	q.MCP.RequestID = domain.NewID()
	q.MCP.Mutation.ExpectedRevision = 2
	again, err := m.ReadMCP(context.Background(), q)
	if err != nil || again.State != domain.MCPFailed || exchanges != 1 {
		t.Fatal("new request reused original callback", err)
	}
	raw, err := security.ReadPrivate(filepath.Join(m.Root, "mcp", string(q.MCP.ServerID), string(q.MCP.WorkerDeviceID), "catalog.json"), 32<<20)
	if err != nil || bytes.Contains(raw, []byte("private-token")) || bytes.Contains(raw, []byte("private-refresh")) || bytes.Contains(raw, []byte("original-code")) {
		t.Fatal("secret appeared in catalog or receipt", err)
	}
	public, _ := json.Marshal(complete)
	if bytes.Contains(public, []byte("private-token")) || bytes.Contains(public, []byte("private-refresh")) {
		t.Fatal("authentication secret readback")
	}
}

func TestMCPManualSecretsKeepOriginalGenerationAfterDeletion(t *testing.T) {
	m, q := mcpFixture(t)
	vault := &fixtureMCPVault{}
	m.mcpOpenVault = func(string, domain.ID) (mcpSecretVault, error) { return vault, nil }
	id := domain.NewID()
	q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPSave, DefinitionID: id, Definition: &domain.MCPDefinition{Name: "Protected stdio", Enabled: true, Transport: domain.MCPStdio, Executable: "not-run"}}
	first, err := m.ReadMCP(context.Background(), q)
	if err != nil || first.State != domain.MCPSucceeded {
		t.Fatal(first, err)
	}
	if len(vault.secrets) != 0 || first.Definitions[0].Authentication != domain.MCPNoAuthentication {
		t.Fatal("saving authenticated or wrote credentials")
	}
	q.MCP.RequestID = domain.NewID()
	q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPManualAuthentication, DefinitionID: id, ExpectedRevision: 1, Secrets: &domain.MCPSecrets{Environment: map[string]string{"FIXTURE_TOKEN": "original-secret"}}}
	first, err = m.ReadMCP(context.Background(), q)
	if err != nil || first.State != domain.MCPSucceeded {
		t.Fatal(first, err)
	}
	original := first.Definitions[0]
	q.MCP.RequestID = domain.NewID()
	q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPManualAuthentication, DefinitionID: id, ExpectedRevision: 2, Secrets: &domain.MCPSecrets{Environment: map[string]string{"FIXTURE_TOKEN": "replacement-secret"}}}
	second, err := m.ReadMCP(context.Background(), q)
	if err != nil || second.State != domain.MCPSucceeded || second.Definitions[0].CredentialID == original.CredentialID {
		t.Fatal("replacement reused original credentials", err)
	}
	q.MCP.RequestID = domain.NewID()
	q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPDelete, DefinitionID: id, ExpectedRevision: 3, Confirmed: true}
	if _, err := m.ReadMCP(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	secret, err := m.MCPGenerationSecrets(context.Background(), q.MCP.ServerID, q.MCP.WorkerDeviceID, original)
	if err != nil || secret.Environment["FIXTURE_TOKEN"] != "original-secret" {
		t.Fatal("deletion redirected original credentials", err)
	}
	original.Executable = "replacement-command"
	if _, err := m.MCPGenerationSecrets(context.Background(), q.MCP.ServerID, q.MCP.WorkerDeviceID, original); err == nil {
		t.Fatal("altered generation resolved credentials")
	}
	raw, _ := json.Marshal(second)
	if bytes.Contains(raw, []byte("original-secret")) || bytes.Contains(raw, []byte("replacement-secret")) {
		t.Fatal("manual credential readback")
	}
}

func TestMCPStdioLaunchChangesRequireFreshAuthentication(t *testing.T) {
	for _, field := range []string{"executable", "arguments", "working-directory"} {
		t.Run(field, func(t *testing.T) {
			m, q := mcpFixture(t)
			vault := &fixtureMCPVault{}
			m.mcpOpenVault = func(string, domain.ID) (mcpSecretVault, error) { return vault, nil }
			id := domain.NewID()
			definition := domain.MCPDefinition{Name: "Authenticated stdio", Enabled: true, Transport: domain.MCPStdio, Executable: "/original/runner", Arguments: []string{"serve", "--safe"}, WorkingDirectory: "/original/work"}
			q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPSave, DefinitionID: id, Definition: &definition}
			created, err := m.ReadMCP(context.Background(), q)
			if err != nil || created.State != domain.MCPSucceeded {
				t.Fatal(created, err)
			}
			q.MCP.RequestID = domain.NewID()
			q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPManualAuthentication, DefinitionID: id, ExpectedRevision: 1, Secrets: &domain.MCPSecrets{Environment: map[string]string{"TOKEN": "original-secret"}}}
			authenticated, err := m.ReadMCP(context.Background(), q)
			if err != nil || authenticated.State != domain.MCPSucceeded || authenticated.Definitions[0].CredentialID == "" || authenticated.Definitions[0].Authentication != domain.MCPAuthenticated {
				t.Fatal(authenticated, err)
			}
			original := authenticated.Definitions[0]
			definition = original
			switch field {
			case "executable":
				definition.Executable = "/replacement/runner"
			case "arguments":
				definition.Arguments = []string{"serve", "--different"}
			case "working-directory":
				definition.WorkingDirectory = "/replacement/work"
			}
			q.MCP.RequestID = domain.NewID()
			q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPSave, DefinitionID: id, ExpectedRevision: original.Revision, Definition: &definition}
			updated, err := m.ReadMCP(context.Background(), q)
			if err != nil || updated.State != domain.MCPSucceeded || updated.Definitions[0].CredentialID != "" || updated.Definitions[0].Authentication != domain.MCPAuthenticationRequired {
				t.Fatal("changed launch target retained the old authentication", updated, err)
			}
			if _, err := m.MCPGenerationSecrets(context.Background(), q.MCP.ServerID, q.MCP.WorkerDeviceID, original); err != nil {
				t.Fatal("original generation was discarded instead of retired", err)
			}
		})
	}
}
func TestMCPOAuthCancellationAndExpiryNeverExchange(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "expiry"}[expired], func(t *testing.T) {
			m, q := mcpFixture(t)
			vault := &fixtureMCPVault{}
			m.mcpOpenVault = func(string, domain.ID) (mcpSecretVault, error) { return vault, nil }
			exchanges := 0
			m.mcpExchange = func(context.Context, domain.MCPOAuthConfiguration, string, string) (domain.MCPSecrets, error) {
				exchanges++
				return domain.MCPSecrets{}, nil
			}
			id := domain.NewID()
			q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPSave, DefinitionID: id, Definition: &domain.MCPDefinition{Name: "OAuth", Transport: domain.MCPStreamableHTTP, Endpoint: "https://mcp.example.invalid/rpc", OAuth: &domain.MCPOAuthConfiguration{AuthorizationURL: "https://auth.example.invalid/authorize", TokenURL: "https://auth.example.invalid/token", ClientID: "client", RedirectURI: "http://127.0.0.1/callback"}}}
			if _, err := m.ReadMCP(context.Background(), q); err != nil {
				t.Fatal(err)
			}
			q.MCP.RequestID = domain.NewID()
			q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPOAuthStart, DefinitionID: id, ExpectedRevision: 1}
			start, err := m.ReadMCP(context.Background(), q)
			if err != nil || start.State != domain.MCPPending {
				t.Fatal(start, err)
			}
			original := q.MCP.RequestID
			if expired {
				path := filepath.Join(m.Root, "mcp", string(q.MCP.ServerID), string(q.MCP.WorkerDeviceID), "catalog.json")
				raw, err := security.ReadPrivate(path, 32<<20)
				if err != nil {
					t.Fatal(err)
				}
				var catalog mcpCatalog
				if json.Unmarshal(raw, &catalog) != nil {
					t.Fatal("fixture catalog")
				}
				prior := catalog.Receipts[original]
				past := time.Now().Add(-time.Minute)
				prior.Result.ExpiresAt = &past
				catalog.Receipts[original] = prior
				raw, _ = json.Marshal(catalog)
				if err := security.WriteAtomic(path, raw); err != nil {
					t.Fatal(err)
				}
				q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPInspectMutation}
				result, err := m.ReadMCP(context.Background(), q)
				if err != nil || result.State != domain.MCPCanceled {
					t.Fatal("expiry was not visible", err)
				}
			} else {
				q.MCP.RequestID = domain.NewID()
				q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPOAuthCancel, DefinitionID: id, ExpectedRevision: 1, OriginalRequestID: original}
				result, err := m.ReadMCP(context.Background(), q)
				if err != nil || result.State != domain.MCPCanceled {
					t.Fatal(result, err)
				}
			}
			authURL, _ := url.Parse(start.AuthorizationURL)
			q.MCP.RequestID = domain.NewID()
			q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPOAuthCallback, DefinitionID: id, ExpectedRevision: 1, OriginalRequestID: original, CallbackURI: "http://127.0.0.1/callback?code=unused&state=" + url.QueryEscape(authURL.Query().Get("state"))}
			result, err := m.ReadMCP(context.Background(), q)
			if err != nil || result.State != domain.MCPFailed || exchanges != 0 {
				t.Fatal("canceled/expired OAuth exchanged", result, err)
			}
		})
	}
}

func TestMCPOAuthOriginalRevisionAndCancellationAfterDeletion(t *testing.T) {
	for _, deletion := range []bool{false, true} {
		t.Run(fmt.Sprintf("deleted-%t", deletion), func(t *testing.T) {
			m, q := mcpFixture(t)
			vault := &fixtureMCPVault{}
			m.mcpOpenVault = func(string, domain.ID) (mcpSecretVault, error) { return vault, nil }
			exchanges := 0
			m.mcpExchange = func(context.Context, domain.MCPOAuthConfiguration, string, string) (domain.MCPSecrets, error) {
				exchanges++
				return domain.MCPSecrets{AccessToken: "must-not-exchange"}, nil
			}
			id := domain.NewID()
			definition := domain.MCPDefinition{Name: "Original OAuth", Transport: domain.MCPStreamableHTTP, Endpoint: "https://mcp.example.invalid/rpc", OAuth: &domain.MCPOAuthConfiguration{AuthorizationURL: "https://auth.example.invalid/authorize", TokenURL: "https://auth.example.invalid/token", ClientID: "client", RedirectURI: "http://127.0.0.1/callback"}}
			q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPSave, DefinitionID: id, Definition: &definition}
			if _, err := m.ReadMCP(context.Background(), q); err != nil {
				t.Fatal(err)
			}
			q.MCP.RequestID = domain.NewID()
			q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPOAuthStart, DefinitionID: id, ExpectedRevision: 1}
			start, err := m.ReadMCP(context.Background(), q)
			if err != nil || start.State != domain.MCPPending {
				t.Fatal(start, err)
			}
			original := q.MCP.RequestID
			q.MCP.RequestID = domain.NewID()
			if deletion {
				q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPDelete, DefinitionID: id, ExpectedRevision: 1, Confirmed: true}
			} else {
				definition.OAuth.TokenURL = "https://replacement.example.invalid/token"
				q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPSave, DefinitionID: id, ExpectedRevision: 1, Definition: &definition}
			}
			if _, err := m.ReadMCP(context.Background(), q); err != nil {
				t.Fatal(err)
			}
			q.MCP.RequestID = domain.NewID()
			if !deletion {
				parsed, _ := url.Parse(start.AuthorizationURL)
				q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPOAuthCallback, DefinitionID: id, ExpectedRevision: 2, OriginalRequestID: original, CallbackURI: "http://127.0.0.1/callback?state=" + url.QueryEscape(parsed.Query().Get("state")) + "&code=original-code"}
				result, err := m.ReadMCP(context.Background(), q)
				if err != nil || result.State != domain.MCPFailed || exchanges != 0 {
					t.Fatal("a changed revision redirected the original exchange", result, err)
				}
				q.MCP.RequestID = domain.NewID()
			}
			q.MCP.Mutation = domain.MCPMutation{Version: 1, Action: domain.MCPOAuthCancel, DefinitionID: id, ExpectedRevision: 1, OriginalRequestID: original}
			result, err := m.ReadMCP(context.Background(), q)
			if err != nil || result.State != domain.MCPCanceled || exchanges != 0 {
				t.Fatal("catalog changes lost original cancellation ownership", result, err)
			}
		})
	}
}
