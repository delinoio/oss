// SPDX-License-Identifier: Apache-2.0
package managedmcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type fixtureSecrets struct {
	values     map[credentials.Ref][]byte
	failDelete bool
}

func (v *fixtureSecrets) Put(_ context.Context, r credentials.Ref, b []byte) (string, error) {
	v.values[r] = append([]byte(nil), b...)
	return "fixture-binding", nil
}
func (v *fixtureSecrets) Get(_ context.Context, r credentials.Ref) ([]byte, error) {
	b, ok := v.values[r]
	if !ok {
		return nil, errors.New("missing fixture secret")
	}
	return append([]byte(nil), b...), nil
}
func (v *fixtureSecrets) Delete(_ context.Context, r credentials.Ref) error {
	if v.failDelete {
		return errors.New("fixture cleanup failed")
	}
	delete(v.values, r)
	return nil
}
func fixture(t *testing.T) (Manager, domain.ManagedMCPRequest, domain.ManagedMCPDefinition) {
	t.Helper()
	m := Manager{Root: t.TempDir(), ServerID: domain.NewID(), MachineID: domain.NewID(), WorkerDeviceID: domain.NewID(), Secrets: &fixtureSecrets{values: map[credentials.Ref][]byte{}}}
	q := domain.ManagedMCPRequest{ServerID: m.ServerID, MachineID: m.MachineID, WorkerDeviceID: m.WorkerDeviceID, WorkerInstanceID: domain.NewID(), ActorID: domain.NewID(), RequestID: domain.NewID(), Action: domain.MCPSave}
	d := domain.ManagedMCPDefinition{ID: domain.NewID(), Revision: 1, MachineID: m.MachineID, WorkerDeviceID: m.WorkerDeviceID, Name: "Fixture", Transport: domain.MCPStdio, Command: filepath.Join(t.TempDir(), "never-launched"), Cwd: t.TempDir(), Enabled: true, Authentication: domain.MCPManualAuthentication, SupportedHarnesses: []domain.Harness{}}
	q.Definition = &d
	return m, q, d
}
func must(t *testing.T, m Manager, q domain.ManagedMCPRequest) domain.ManagedMCPResult {
	t.Helper()
	r, e := m.Execute(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestManagedMCPCatalogIsolationRevisionsAndPrivateCredentials(t *testing.T) {
	m, q, d := fixture(t)
	original := must(t, m, q)
	if original.Definitions[0].Revision != 1 {
		t.Fatal("missing original generation")
	}
	replay := q
	replay.WorkerInstanceID = domain.NewID()
	if !must(t, m, replay).Replayed {
		t.Fatal("lost acknowledgment was not recovered across attachment")
	}
	q.RequestID = domain.NewID()
	q.Action = domain.MCPList
	q.Definition = nil
	list := must(t, m, q)
	if len(list.Definitions) != 1 || len(list.Definitions[0].SupportedHarnesses) != 0 {
		t.Fatal("configuration fabricated native support")
	}
	other := q
	other.WorkerDeviceID = domain.NewID()
	if _, e := m.Execute(context.Background(), other); domain.SafeError(e).Code != domain.PermissionDenied {
		t.Fatal("foreign Worker accepted")
	}
	auth := q
	auth.RequestID = domain.NewID()
	auth.Action = domain.MCPAuthenticate
	auth.DefinitionID = d.ID
	auth.ExpectedRevision = 1
	auth.Secrets = &domain.MCPSecretInput{Environment: map[string]string{"TOKEN": "private-fixture-token"}}
	r := must(t, m, auth)
	if r.Definitions[0].Revision != 2 || r.Definitions[0].CredentialID != auth.RequestID {
		t.Fatal("credential generation not retained")
	}
	b, e := os.ReadFile(filepath.Join(m.Root, "managed-mcp", string(m.ServerID), string(m.WorkerDeviceID), "catalog.json"))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "private-fixture-token") {
		t.Fatal("plaintext credential entered metadata")
	}
	var c catalog
	if json.Unmarshal(b, &c) != nil || c.Generations[generationKey(d)].Revision != 1 {
		t.Fatal("history overwritten")
	}
	edit := q
	edit.RequestID = domain.NewID()
	edit.Action = domain.MCPSave
	edit.ExpectedRevision = 1
	d.Revision = 2
	edit.Definition = &d
	if _, e = m.Execute(context.Background(), edit); domain.SafeError(e).Code != domain.Conflict {
		t.Fatal("stale save accepted")
	}
	remove := q
	remove.RequestID = domain.NewID()
	remove.Action = domain.MCPDelete
	remove.DefinitionID = d.ID
	remove.ExpectedRevision = 2
	remove.Confirmed = true
	remove.Referenced = true
	if _, e = m.Execute(context.Background(), remove); domain.SafeError(e).Code != domain.Conflict {
		t.Fatal("referenced definition deleted")
	}
	remove.Referenced = false
	must(t, m, remove)
	if len(must(t, m, q).Definitions) != 0 {
		t.Fatal("deleted row remained current")
	}
	if len(m.Secrets.(*fixtureSecrets).values) != 1 {
		t.Fatal("historical credentials erased before cleanup")
	}
	if _, e = os.Stat(d.Command); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("catalog launched configured executable")
	}
}
func TestManagedMCPOAuthOriginalScopeAndNoExchangeReplay(t *testing.T) {
	m, q, d := fixture(t)
	var endpoint string
	exchanges := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			json.NewEncoder(w).Encode(map[string]any{"resource": endpoint + "/mcp", "authorization_servers": []string{endpoint}})
		case "/.well-known/oauth-authorization-server":
			json.NewEncoder(w).Encode(map[string]any{"issuer": endpoint, "authorization_endpoint": endpoint + "/authorize", "token_endpoint": endpoint + "/token", "code_challenge_methods_supported": []string{"S256"}})
		case "/token":
			exchanges++
			if r.ParseForm() != nil || r.Form.Get("code_verifier") == "" || r.Form.Get("resource") != endpoint+"/mcp" {
				t.Error("missing original PKCE/resource")
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "private-oauth-token", "token_type": "Bearer", "expires_in": 3600})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	endpoint = server.URL
	d.Transport = domain.MCPStreamableHTTP
	d.Command = ""
	d.Cwd = ""
	d.Endpoint = endpoint + "/mcp"
	d.Authentication = domain.MCPOAuthAuthentication
	d.OAuth = &domain.MCPOAuthProfile{ClientID: "fixture", AuthorizationURL: endpoint + "/authorize", TokenURL: endpoint + "/token", RedirectURI: "http://localhost/callback"}
	q.Definition = &d
	must(t, m, q)
	begin := q
	begin.Definition = nil
	begin.DefinitionID = d.ID
	begin.ExpectedRevision = 1
	begin.RequestID = domain.NewID()
	begin.Action = domain.MCPOAuthBegin
	r := must(t, m, begin)
	list := begin
	list.Action = domain.MCPList
	observed := must(t, m, list)
	if len(observed.Operations) != 1 || observed.Operations[0].ID != begin.RequestID {
		t.Fatal("original awaiting capture not discoverable")
	}
	list.ActorID = domain.NewID()
	if len(must(t, m, list).Operations) != 0 {
		t.Fatal("foreign client received original OAuth capture")
	}
	editing := q
	editing.RequestID = domain.NewID()
	editing.ExpectedRevision = 1
	edited := d
	edited.Revision = 2
	editing.Definition = &edited
	if _, e := m.Execute(context.Background(), editing); domain.SafeError(e).Code != domain.Conflict {
		t.Fatal("edit stranded original OAuth generation")
	}
	u, e := url.Parse(r.Operation.AuthorizationURL)
	if e != nil || u.Query().Get("resource") != d.Endpoint || u.Query().Get("code_challenge_method") != "S256" {
		t.Fatal("invalid original authorization")
	}
	complete := begin
	complete.RequestID = domain.NewID()
	complete.Action = domain.MCPOAuthComplete
	complete.AttemptID = begin.RequestID
	complete.CallbackURL = "http://localhost/callback?" + url.Values{"state": {u.Query().Get("state")}, "code": {"fixture-code"}}.Encode()
	foreign := complete
	foreign.ActorID = domain.NewID()
	if _, e = m.Execute(context.Background(), foreign); domain.SafeError(e).Code != domain.PermissionDenied {
		t.Fatal("foreign callback accepted")
	}
	done := must(t, m, complete)
	if done.Operation.State != domain.MCPOperationCompleted || exchanges != 1 {
		t.Fatal("OAuth did not finish once")
	}
	inspect := begin
	inspect.Action = domain.MCPOperationRead
	inspect.AttemptID = begin.RequestID
	inspect.RequestID = domain.NewID()
	if observed := must(t, m, inspect); observed.Operation.State != domain.MCPOperationCompleted || observed.Operation.AuthorizationURL != "" {
		t.Fatal("original attempt did not retain current settled state")
	}
	if !must(t, m, complete).Replayed || exchanges != 1 {
		t.Fatal("exchange replayed")
	}
	b, _ := os.ReadFile(filepath.Join(m.Root, "managed-mcp", string(m.ServerID), string(m.WorkerDeviceID), "catalog.json"))
	if strings.Contains(string(b), "private-oauth-token") || strings.Contains(string(b), "fixture-code") {
		t.Fatal("OAuth plaintext retained in metadata")
	}
}

func TestManagedMCPOAuthExpiryCancellationAndLostExchange(t *testing.T) {
	for _, mode := range []string{"expired", "cancel", "cleanup-failed", "lost-exchange"} {
		t.Run(mode, func(t *testing.T) {
			m, q, d := fixture(t)
			d.Transport = domain.MCPStreamableHTTP
			d.Command = ""
			d.Cwd = ""
			d.Endpoint = "https://fixture.test/mcp"
			d.Authentication = domain.MCPOAuthAuthentication
			d.OAuth = &domain.MCPOAuthProfile{ClientID: "public", AuthorizationURL: "https://fixture.test/auth", TokenURL: "https://fixture.test/token", RedirectURI: "http://localhost/callback"}
			q.Definition = &d
			must(t, m, q)
			attempt := domain.NewID()
			now := time.Now().UTC()
			m.Now = func() time.Time { return now }
			state := sha256.Sum256([]byte("original-state"))
			expires := now.Add(time.Minute)
			if mode == "expired" {
				expires = now.Add(-time.Minute)
			}
			path := filepath.Join(m.Root, "managed-mcp", string(m.ServerID), string(m.WorkerDeviceID), "catalog.json")
			raw, _ := os.ReadFile(path)
			var c catalog
			if json.Unmarshal(raw, &c) != nil {
				t.Fatal("bad fixture catalog")
			}
			c.Attempts[attempt] = oauthAttempt{ActorID: q.ActorID, DefinitionID: d.ID, Revision: 1, State: domain.MCPOperationAwaiting, ExpiresAt: expires, StateDigest: hex.EncodeToString(state[:])}
			raw, _ = json.Marshal(c)
			if os.WriteFile(path, raw, 0600) != nil {
				t.Fatal("fixture write")
			}
			secret, _ := json.Marshal(oauthPrivate{Verifier: strings.Repeat("x", 43)})
			m.Secrets.Put(context.Background(), credentials.Ref{Owner: d.ID, ID: attempt, Purpose: credentials.ManagedMCP}, secret)
			q.Definition = nil
			q.DefinitionID = d.ID
			q.RequestID = domain.NewID()
			q.ExpectedRevision = 1
			q.AttemptID = attempt
			q.Action = domain.MCPOAuthComplete
			q.CallbackURL = "http://localhost/callback?state=original-state&code=private-code"
			exchanges := 0
			m.HTTP = &http.Client{Transport: fixtureTransport(func(*http.Request) (*http.Response, error) {
				exchanges++
				return nil, errors.New("lost exchange response")
			})}
			if mode == "cancel" || mode == "cleanup-failed" {
				q.Action = domain.MCPOAuthCancel
				q.Confirmed = true
				m.Secrets.(*fixtureSecrets).failDelete = mode == "cleanup-failed"
			}
			result, err := m.Execute(context.Background(), q)
			if mode == "cancel" {
				if err != nil || result.Operation.State != domain.MCPOperationCanceled || len(m.Secrets.(*fixtureSecrets).values) != 0 {
					t.Fatal("cancellation not confirmed")
				}
			} else if err == nil {
				t.Fatal("failed operation claimed acceptance")
			}
			if mode == "lost-exchange" {
				if exchanges != 1 {
					t.Fatal("exchange missing")
				}
				if _, err = m.Execute(context.Background(), q); err == nil || exchanges != 1 {
					t.Fatal("uncertain exchange replayed")
				}
			}
			if mode == "expired" && exchanges != 0 {
				t.Fatal("expired authorization exchanged")
			}
			if mode == "cleanup-failed" && len(m.Secrets.(*fixtureSecrets).values) != 1 {
				t.Fatal("failed cleanup erased ownership")
			}
		})
	}
}

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestManagedMCPHTTPManualCredentialsBindEndpointGeneration(t *testing.T) {
	m, q, d := fixture(t)
	d.Transport = domain.MCPStreamableHTTP
	d.Command = ""
	d.Cwd = ""
	d.Endpoint = "https://fixture.test/mcp"
	q.Definition = &d
	must(t, m, q)
	auth := q
	auth.Definition = nil
	auth.DefinitionID = d.ID
	auth.Action = domain.MCPAuthenticate
	auth.ExpectedRevision = 1
	auth.RequestID = domain.NewID()
	auth.Secrets = &domain.MCPSecretInput{Headers: map[string]string{"Authorization": "Bearer private-header"}}
	authenticated := must(t, m, auth).Definitions[0]
	if authenticated.CredentialID == "" {
		t.Fatal("manual header not sealed")
	}
	edit := q
	edit.RequestID = domain.NewID()
	edit.ExpectedRevision = 2
	d.Revision = 3
	d.Name = "Renamed"
	edit.Definition = &d
	renamed := must(t, m, edit).Definitions[0]
	if renamed.CredentialID != authenticated.CredentialID {
		t.Fatal("name edit erased original credential binding")
	}
	edit.RequestID = domain.NewID()
	edit.ExpectedRevision = 3
	d.Revision = 4
	d.Endpoint = "https://other.test/mcp"
	changed := must(t, m, edit).Definitions[0]
	if changed.CredentialID != "" {
		t.Fatal("changed resource adopted original authentication")
	}
	raw, _ := os.ReadFile(filepath.Join(m.Root, "managed-mcp", string(m.ServerID), string(m.WorkerDeviceID), "catalog.json"))
	if strings.Contains(string(raw), "private-header") {
		t.Fatal("manual HTTP secret persisted in metadata")
	}
	if len(m.Secrets.(*fixtureSecrets).values) != 1 {
		t.Fatal("original credential generation lost")
	}
}
