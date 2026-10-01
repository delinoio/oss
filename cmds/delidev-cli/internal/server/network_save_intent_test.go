// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outboundtest"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func networkIntentRequest(id domain.ID) *connect.Request[pb.SaveNetworkProfileRequest] {
	raw, _ := json.Marshal(domain.ProxyDefinition{Name: "Fixture", Mode: domain.ProxyHTTP, Host: "127.0.0.1", Port: 3128})
	return connect.NewRequest(&pb.SaveNetworkProfileRequest{Mutation: &pb.Mutation{RequestId: string(id)}, SchemaVersion: 1, DocumentJson: raw, CredentialJson: []byte(outboundtest.Credential)})
}

func networkIntentContext() context.Context {
	return domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
}

func TestNetworkSaveIntentCanceledPublicationRetainsExactRetry(t *testing.T) {
	f := newIntegrationFixture(t)
	vault := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
	f.service.accountSecrets = vault
	id := domain.NewID()
	ctx, cancel := context.WithCancel(networkIntentContext())
	defer cancel()
	vault.afterPut = cancel
	if _, err := f.service.SaveNetworkProfile(ctx, networkIntentRequest(id)); err == nil {
		t.Fatal("canceled publication succeeded")
	}
	if _, err := f.service.Store.Get(networkIntentContext(), domain.NetworkProfileKind, id); domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("canceled publication created a profile", err)
	}
	raw, err := os.ReadFile(f.service.networkSaveIntentPath())
	if err != nil || bytes.Contains(raw, []byte(outboundtest.Username)) || bytes.Contains(raw, []byte(outboundtest.Password)) {
		t.Fatal("missing or secret-bearing publication intent", err)
	}
	vault.afterPut = nil
	altered := networkIntentRequest(id)
	altered.Msg.DocumentJson = bytes.Replace(altered.Msg.DocumentJson, []byte("Fixture"), []byte("Changed"), 1)
	if _, err := f.service.SaveNetworkProfile(networkIntentContext(), altered); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("failed request's public input could change", err)
	}
	altered = networkIntentRequest(id)
	altered.Msg.CredentialJson = []byte(`{"username":"changed","password":"changed"}`)
	if _, err := f.service.SaveNetworkProfile(networkIntentContext(), altered); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("failed request's secret input could change", err)
	}
	// Recreate the coordinator to prove the retry is driven by durable intent,
	// not an in-memory callback or a live request context.
	restarted := &Service{Store: f.service.Store, Identity: f.service.Identity, logger: f.service.logger, accountSecrets: vault}
	result, err := restarted.SaveNetworkProfile(networkIntentContext(), networkIntentRequest(id))
	if err != nil || result.Msg.Resource.Id != string(id) || len(vault.values) != 1 {
		t.Fatal("exact restart retry lost the original generation", err)
	}
	if _, err := os.Stat(restarted.networkSaveIntentPath()); !os.IsNotExist(err) {
		t.Fatal("accepted publication retained pending intent", err)
	}
}

func TestNetworkSaveIntentSQLFailureBlocksReplacementUntilCleanup(t *testing.T) {
	f := newIntegrationFixture(t)
	vault := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
	f.service.accountSecrets = vault
	db, err := sql.Open("sqlite", filepath.Join(f.root, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER fixture_network_publication_fail BEFORE INSERT ON entities WHEN NEW.kind='network_profile' BEGIN SELECT RAISE(ABORT,'fixture publication failure'); END`); err != nil {
		t.Fatal(err)
	}
	id := domain.NewID()
	if _, err := f.service.SaveNetworkProfile(networkIntentContext(), networkIntentRequest(id)); err == nil || len(vault.values) != 1 {
		t.Fatal("SQL failure did not retain bounded recovery", err)
	}
	vault.deleteError = domain.Fail(domain.Unavailable, "Fixture native cleanup unavailable.", "")
	for i := 0; i < 4; i++ {
		if _, err := f.service.SaveNetworkProfile(networkIntentContext(), networkIntentRequest(domain.NewID())); connect.CodeOf(err) != connect.CodeUnavailable || vault.puts != 1 || len(vault.values) != 1 {
			t.Fatal("failed cleanup admitted another native generation", err)
		}
	}
	intent, exists, err := f.service.readNetworkSaveIntent()
	if err != nil || !exists || intent.RequestID != id || intent.State != networkSaveCleanup {
		t.Fatal("original cleanup intent was replaced", err)
	}
	vault.deleteError = nil
	// Repeated distinct failed creates must clean their predecessor instead of
	// retaining one credential under every new owner UUID.
	for i := 0; i < 3; i++ {
		if _, err := f.service.SaveNetworkProfile(networkIntentContext(), networkIntentRequest(domain.NewID())); err == nil || len(vault.values) != 1 {
			t.Fatal("failed creates accumulated orphan credentials", err)
		}
	}
	if _, err := db.Exec(`DROP TRIGGER fixture_network_publication_fail`); err != nil {
		t.Fatal(err)
	}
	final := domain.NewID()
	if _, err := f.service.SaveNetworkProfile(networkIntentContext(), networkIntentRequest(final)); err != nil || len(vault.values) != 1 || vault.values[proxyRef(final, final)] == nil {
		t.Fatal("replacement did not finish original cleanup", err)
	}
}

func TestNetworkSaveIntentCorruptionBlocksNativeReplacement(t *testing.T) {
	f := newIntegrationFixture(t)
	vault := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
	f.service.accountSecrets = vault
	ctx, cancel := context.WithCancel(networkIntentContext())
	defer cancel()
	vault.afterPut = cancel
	if _, err := f.service.SaveNetworkProfile(ctx, networkIntentRequest(domain.NewID())); err == nil {
		t.Fatal("canceled publication succeeded")
	}
	vault.afterPut = nil
	raw, err := os.ReadFile(f.service.networkSaveIntentPath())
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(raw, []byte("Fixture"), []byte("Changed"), 1)
	if err := os.WriteFile(f.service.networkSaveIntentPath(), changed, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.SaveNetworkProfile(networkIntentContext(), networkIntentRequest(domain.NewID())); connect.CodeOf(err) != connect.CodeFailedPrecondition || vault.puts != 1 || vault.deletes != 0 {
		t.Fatal("unverified intent allowed credential replacement", err)
	}
	retained, err := os.ReadFile(f.service.networkSaveIntentPath())
	if err != nil || !bytes.Equal(retained, changed) {
		t.Fatal("corrupt recovery evidence was replaced", err)
	}
}

func TestNetworkSaveIntentAcceptedReceiptPreservesPublishedCredential(t *testing.T) {
	f := newIntegrationFixture(t)
	vault := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
	f.service.accountSecrets = vault
	id := domain.NewID()
	request := networkIntentRequest(id)
	if _, err := f.service.SaveNetworkProfile(networkIntentContext(), request); err != nil {
		t.Fatal(err)
	}
	var definition domain.ProxyDefinition
	if err := domain.Decode(request.Msg.DocumentJson, &definition); err != nil {
		t.Fatal(err)
	}
	// Model a crash after SQL commit but before clearing its private intent.
	intent := networkSaveIntent{Version: 1, ServerID: f.service.Identity.ServerID, RequestID: id, State: networkSavePending, Input: networkSaveInput{Actor: domain.Principal{Type: domain.OwnerDevice}, ID: id, Definition: definition, Commitment: f.service.networkCommitment(id, []byte(outboundtest.Credential))}}
	if err := f.service.writeNetworkSaveIntent(intent); err != nil {
		t.Fatal(err)
	}
	puts := vault.puts
	result, err := f.service.SaveNetworkProfile(networkIntentContext(), networkIntentRequest(id))
	if err != nil || !result.Msg.Replayed || vault.puts != puts || vault.deletes != 0 || len(vault.values) != 1 {
		t.Fatal("receipt reconciliation removed or rewrote an accepted generation", err)
	}
	// A later failed edit retains the selected original generation while a
	// replacement request cleans only its unpublished credential.
	_, err = f.service.Store.Mutate(networkIntentContext(), domain.NewID(), "fixture.route", id, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.NetworkProfileKind, id)
		if err != nil {
			return nil, err
		}
		p, err := store.Decode[domain.NetworkProfile](r)
		if err != nil {
			return nil, err
		}
		return tx.Put(domain.NetworkRouteKind, domain.NewID(), 0, "", "", domain.NetworkRoute{ProfileID: id, ProfileRevision: r.Revision, Profile: p})
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(networkIntentContext())
	defer cancel()
	vault.afterPut = cancel
	edit := networkIntentRequest(domain.NewID())
	edit.Msg.Mutation.Id, edit.Msg.Mutation.ExpectedRevision = string(id), 1
	if _, err := f.service.SaveNetworkProfile(ctx, edit); err == nil {
		t.Fatal("canceled edit succeeded")
	}
	vault.afterPut = nil
	replacement := networkIntentRequest(domain.NewID())
	replacement.Msg.Mutation.Id, replacement.Msg.Mutation.ExpectedRevision = string(id), 1
	if _, err := f.service.SaveNetworkProfile(networkIntentContext(), replacement); err != nil {
		t.Fatal(err)
	}
	_, secret, err := f.service.outboundResolver()(networkIntentContext())
	defer clear(secret)
	if err != nil || string(secret) != outboundtest.Credential || vault.values[proxyRef(id, id)] == nil || len(vault.values) != 2 {
		t.Fatal("orphan cleanup damaged the independently pinned generation", err)
	}
}
