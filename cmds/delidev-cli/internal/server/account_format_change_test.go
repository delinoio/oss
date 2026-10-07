// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func formatRequest(account *pb.Resource, protocol pb.ApiProtocol) *pb.ChangeAccountApiFormatRequest {
	body := accountBodyNoTest(account)
	return &pb.ChangeAccountApiFormatRequest{Mutation: acctMutation(account, domain.NewID()), ApiProtocol: protocol, Alias: body.Alias, Enabled: body.Enabled, ExcludeAutomatic: body.ExcludeAutomatic, RecoveryNotifications: body.RecoveryNotifications}
}
func accountBodyNoTest(account *pb.Resource) domain.Account {
	var body domain.Account
	_ = domain.Decode(account.DocumentJson, &body)
	return body
}

func TestAccountApiFormatChangeKeepsKeyAcrossRestartAndCleanup(t *testing.T) {
	f := newOAuthFixture(t)
	account, err := saveAPIFormatConfiguration(f, domain.AccountKind, domain.Account{Alias: "Original", Type: domain.APIAccount, ProviderID: domain.ID(f.provider.Id), APIProtocol: domain.OpenAIChat, Enabled: true, Health: domain.AccountDisconnected}, nil)
	if err != nil {
		t.Fatal(err)
	}
	connected, err := f.s.ConnectAccount(f.ctx, connect.NewRequest(&pb.ConnectAccountRequest{Mutation: acctMutation(account, domain.NewID()), ApiKey: []byte("fixture-retained-key")}))
	if err != nil {
		t.Fatal(err)
	}
	account = connected.Msg.Account
	original := accountBody(t, account).Connection.ID
	puts, deletes, enumerations := f.vault.counts()
	request := formatRequest(account, pb.ApiProtocol_API_PROTOCOL_OPENAI_RESPONSES)
	request.Alias = "Changed"
	changed, err := f.s.ChangeAccountApiFormat(f.ctx, connect.NewRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	body := accountBody(t, changed.Msg.Account)
	if body.APIProtocol != domain.OpenAIResponses || body.Alias != "Changed" || body.Health != domain.AccountUnverified || body.Validation != nil || body.Connection.ID == original || body.Connection.CredentialReferenceID() != original || len(body.RetainedConnections) != 1 {
		t.Fatal("format/generation was not published atomically")
	}
	if p, d, e := f.vault.counts(); p != puts || d != deletes || e != enumerations {
		t.Fatal("format save accessed protected storage")
	}
	if bytes.Contains(changed.Msg.Account.DocumentJson, []byte("fixture-retained-key")) {
		t.Fatal("key escaped")
	}
	stale := formatRequest(account, pb.ApiProtocol_API_PROTOCOL_ANTHROPIC_MESSAGES)
	if _, err = f.s.ChangeAccountApiFormat(f.ctx, connect.NewRequest(stale)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("stale revision changed account", err)
	}
	for _, protocol := range []pb.ApiProtocol{pb.ApiProtocol_API_PROTOCOL_OPENAI_CHAT, pb.ApiProtocol_API_PROTOCOL_ANTHROPIC_MESSAGES} {
		next, err := f.s.ChangeAccountApiFormat(f.ctx, connect.NewRequest(formatRequest(changed.Msg.Account, protocol)))
		if err != nil {
			t.Fatal(err)
		}
		changed = next
	}
	f.restart(t)
	replay, err := f.s.ChangeAccountApiFormat(f.ctx, connect.NewRequest(request))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Account.Revision != changed.Msg.Account.Revision {
		t.Fatal("replay replaced current generation", err)
	}
	body = accountBody(t, replay.Msg.Account)
	for _, id := range []domain.ID{original, body.Connection.ID, body.RetainedConnections[1].Connection.ID} {
		credential, err := f.s.resolveAPICredential(f.ctx, domain.ID(account.Id), id, body.ProviderID)
		if err != nil || string(credential.key) != "fixture-retained-key" {
			t.Fatal("shared original key lost", err)
		}
		clear(credential.key)
	}
	f.vault.deleteError = domain.Fail(domain.Unavailable, "Fixture cleanup failure.", "")
	removal := &pb.DisconnectAccountRequest{Mutation: acctMutation(replay.Msg.Account, domain.NewID())}
	disconnected, err := f.s.DisconnectAccount(f.ctx, connect.NewRequest(removal))
	if err != nil || accountBody(t, disconnected.Msg.Account).Removal == nil {
		t.Fatal("cleanup failure lost original retry", err)
	}
	if _, err = f.s.ChangeAccountApiFormat(f.ctx, connect.NewRequest(formatRequest(disconnected.Msg.Account, pb.ApiProtocol_API_PROTOCOL_OPENAI_CHAT))); err == nil {
		t.Fatal("cleanup allowed new format")
	}
	for _, id := range []domain.ID{original, body.Connection.ID} {
		if _, err = f.s.resolveAPICredential(f.ctx, domain.ID(account.Id), id, body.ProviderID); err == nil {
			t.Fatal("disconnection left a generation authorized")
		}
	}
	f.vault.deleteError = nil
	finished, err := f.s.DisconnectAccount(f.ctx, connect.NewRequest(removal))
	if err != nil || accountBody(t, finished.Msg.Account).Removal != nil || len(f.vault.values) != 0 {
		t.Fatal("shared cleanup failed", err)
	}
}

func TestAccountApiFormatChangePreservesOAuthReceipt(t *testing.T) {
	f := newOAuthFixture(t)
	calls := 0
	f.s.oauthExchange = oauthExchangeFunc(func(context.Context, []byte, []byte) ([]byte, error) {
		calls++
		return []byte("fixture-oauth-retained"), nil
	})
	started := f.start(t)
	requestID := domain.NewID()
	connected, err := f.complete(started.Attempt, requestID, "fixture-code")
	if err != nil {
		t.Fatal(err)
	}
	original := accountBody(t, connected.Msg.Account).Connection.ID
	changed, err := f.s.ChangeAccountApiFormat(f.ctx, connect.NewRequest(formatRequest(connected.Msg.Account, pb.ApiProtocol_API_PROTOCOL_OPENAI_RESPONSES)))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.complete(started.Attempt, requestID, "fixture-code")
	if err != nil || !replay.Msg.Replayed || calls != 1 || replay.Msg.Account.Revision != changed.Msg.Account.Revision {
		t.Fatal("format change broke once-only OAuth receipt", err)
	}
	credential, err := f.s.resolveAPICredential(f.ctx, domain.ID(changed.Msg.Account.Id), accountBody(t, changed.Msg.Account).Connection.ID, domain.ID(f.provider.Id))
	if err != nil || string(credential.key) != "fixture-oauth-retained" || accountBody(t, changed.Msg.Account).Connection.CredentialReferenceID() != original {
		t.Fatal("OAuth key was replaced", err)
	}
	clear(credential.key)
}

func TestAccountApiFormatChangeKeepsOriginalExecutionAuthority(t *testing.T) {
	oldRequests, newRequests := 0, 0
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oldRequests++
		if r.Header.Get("Authorization") != "Bearer temporary-upstream-fixture-key" {
			t.Error("wrong key")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_format","object":"response","status":"completed","output":[]}`)
	}))
	defer old.Close()
	next := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { newRequests++; w.WriteHeader(500) }))
	defer next.Close()
	f := newAuthorityFixture(t, old.URL)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.formats", nil, func(tx *store.Tx) (any, error) {
		record, e := tx.Get(domain.ProviderKind, f.input.Configuration.ProviderID)
		if e != nil {
			return nil, e
		}
		provider, e := store.Decode[domain.Provider](record)
		if e != nil {
			return nil, e
		}
		provider.APIFormats = []domain.ProviderAPIFormat{provider.LegacyAPIFormat(), {Protocol: domain.OpenAIChat, Endpoint: next.URL, Authentication: domain.BearerAuth}}
		if _, e = tx.Put(domain.ProviderKind, record.ID, record.Revision, "", "", provider); e != nil {
			return nil, e
		}
		ar, account, e := accountFromTx(tx, f.input.AccountID, 0)
		if e != nil {
			return nil, e
		}
		account.Validation = &domain.AccountValidation{RequestID: domain.NewID(), ConnectionID: account.Connection.ID, State: domain.Observed, ObservedAt: time.Now().UTC(), Authentication: domain.CredentialAccepted}
		return tx.Put(domain.AccountKind, ar.ID, ar.Revision, "", "", account)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.registerGrant(t)
	record, err := f.service.accountRecord(context.Background(), f.input.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	client := delidevv1connect.NewAccountServiceClient(http.DefaultClient, f.http.URL)
	input := formatRequest(rpc.Resource(record), pb.ApiProtocol_API_PROTOCOL_OPENAI_CHAT)
	worker := connect.NewRequest(input)
	worker.Header().Set("Authorization", "Bearer "+f.workerToken)
	if _, err = client.ChangeAccountApiFormat(context.Background(), worker); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker changed account format", err)
	}
	changed, err := client.ChangeAccountApiFormat(context.Background(), ownerRequest(f.service.Identity, input))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := f.service.executionAuthority.Acquire(context.Background(), f.token)
	if err != nil {
		t.Fatal("original lease lost", err)
	}
	defer lease.Release()
	if lease.Scope.Provider.Protocol != domain.OpenAIResponses || lease.Scope.Provider.Endpoint != old.URL {
		t.Fatal("original execution was converted")
	}
	credential, err := lease.Key(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	clear(credential)
	response := f.request(t, f.token, `{"model":"fixture-model"}`)
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || oldRequests != 1 || newRequests != 0 {
		t.Fatal("execution reached new profile", response.StatusCode, oldRequests, newRequests)
	}
	agentRecord, _ := f.service.Store.Get(context.Background(), domain.AgentKind, f.input.Configuration.AgentID)
	agent, _ := store.Decode[domain.Agent](agentRecord)
	if !agent.ReconfigurationRequired {
		t.Fatal("incompatible Worker was not marked")
	}
	body := accountBody(t, changed.Msg.Account)
	if body.Health != domain.AccountUnverified || body.Validation != nil {
		t.Fatal("new profile inherited readiness")
	}
	projected := body.ForConnection(f.input.ConnectionID)
	if projected.APIProtocol != domain.OpenAIResponses || projected.Validation == nil || projected.Health != domain.AccountReady {
		t.Fatal("continuation lost original validation")
	}
	// Editing the provider's old profile is forbidden even when no current
	// account selects it; original stopped/running execution references remain.
	providerRecord, _ := f.service.Store.Get(context.Background(), domain.ProviderKind, f.input.Configuration.ProviderID)
	provider, _ := store.Decode[domain.Provider](providerRecord)
	provider.APIFormats[0].Endpoint = next.URL
	raw, _ := json.Marshal(provider)
	_, err = f.service.SaveConfiguration(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), connect.NewRequest(&pb.SaveConfigurationRequest{Mutation: acctMutation(rpc.Resource(providerRecord), domain.NewID()), Kind: pb.EntityKind_ENTITY_KIND_PROVIDER, SchemaVersion: 3, DocumentJson: raw}))
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("retained profile became mutable", err)
	}
}

func TestAccountApiFormatChangePreservesSessionContinuation(t *testing.T) {
	f := newContinuationFixture(t, domain.ExecutionSucceeded)
	ctx := context.Background()
	original := f.input
	err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
		r, e := tx.Get(domain.AccountKind, original.AccountID)
		if e == nil {
			f.account = rpc.Resource(r)
		}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.format-profiles", nil, func(tx *store.Tx) (any, error) {
		r, e := tx.Get(domain.ProviderKind, original.Configuration.ProviderID)
		if e != nil {
			return nil, e
		}
		p, e := store.Decode[domain.Provider](r)
		if e != nil {
			return nil, e
		}
		p.APIFormats = []domain.ProviderAPIFormat{p.LegacyAPIFormat(), {Protocol: domain.OpenAIChat, Endpoint: p.Endpoint, Authentication: domain.KeylessAuth}}
		return tx.Put(domain.ProviderKind, r.ID, r.Revision, "", "", p)
	})
	if err != nil {
		t.Fatal(err)
	}
	changed, err := f.accounts.ChangeAccountApiFormat(ctx, ownerRequest(f.identity, formatRequest(f.account, pb.ApiProtocol_API_PROTOCOL_OPENAI_CHAT)))
	if err != nil {
		t.Fatal(err)
	}
	if accountBody(t, changed.Msg.Account).Health != domain.AccountUnverified {
		t.Fatal("new format was validated by save")
	}
	f.enqueue(t, "continue original format", domain.PlanMode)
	if err := f.service.dispatchExecution(ctx, f.refresh(t)); err != nil {
		t.Fatal("format change blocked original continuation", err)
	}
	f.claim(t)
	if f.input.ConnectionID != original.ConnectionID || f.input.ConfigurationDigest != original.ConfigurationDigest {
		t.Fatal("continuation switched generation or configuration")
	}
	f.complete(t, domain.ExecutionSucceeded)
}
