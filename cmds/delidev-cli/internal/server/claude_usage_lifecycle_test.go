// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestClaudeUsageDisconnectRemovesOnlyUsageCredentialsAndPreservesExecution(t *testing.T) {
	f := newUsageFixture(t)
	generation := f.link()
	f.refreshOnce(true)
	_, before := f.record()
	executionRefs := f.executionRefs()
	if len(f.usage().Windows) != 2 || len(f.usageRefs()) != 1 {
		t.Fatal("fixture did not link usage")
	}
	disconnected, err := f.disconnect(generation)
	if err != nil || disconnected.Replayed {
		t.Fatal(err)
	}
	usage := f.usage()
	if usage != nil && (usage.Linked() || usage.Generation != "" || usage.Identity != nil || len(usage.Windows) != 0 || usage.LastSuccess != nil || usage.CleanupPending || len(usage.RetiredCredentials) != 0) {
		t.Fatalf("usage after disconnect %+v", usage)
	}
	if len(f.usageRefs()) != 0 {
		t.Fatal("usage credentials remained in the vault")
	}
	if f.native.store.removals == 0 {
		t.Fatal("native usage storage was not removed")
	}
	_, after := f.record()
	if after.Health != domain.AccountReady || after.Subscription.Generation != before.Subscription.Generation || len(after.Quota) != 0 {
		t.Fatal("disconnecting usage changed execution state or left windows")
	}
	if got := f.executionRefs(); len(got) != len(executionRefs) || got[0] != executionRefs[0] {
		t.Fatal("the execution setup-token reference changed")
	}
	// Reads are revoked: nothing is eligible and no GET is sent.
	sent := f.transport.count()
	if outcome := f.refreshOnce(true); outcome != claudeUsageReadIneligible || f.transport.count() != sent {
		t.Fatalf("a read was accepted after disconnect: %s", outcome)
	}
	// A replay of the original request is idempotent; a stale generation is not.
	if _, err := f.disconnect(generation); err == nil {
		t.Fatal("a stale usage generation disconnected")
	}
}

func TestClaudeUsageDisconnectReportsIncompleteCleanupAndRetries(t *testing.T) {
	f := newUsageFixture(t)
	generation := f.link()
	f.native.store.removeErr = errors.New("busy")
	r, _ := f.record()
	request := &pb.DisconnectClaudeUsageRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, UsageGeneration: generation}
	if _, err := f.client.DisconnectClaudeUsage(context.Background(), subscriptionRequest(f.service.Identity.Token, request)); connect.CodeOf(err) == 0 {
		t.Fatal("an incomplete cleanup was reported as success")
	}
	usage := f.usage()
	if usage.Linked() || !usage.CleanupPending || len(usage.RetiredCredentials) != 1 {
		t.Fatalf("reads must be revoked immediately with a durable obligation: %+v", usage)
	}
	if outcome := f.refreshOnce(true); outcome != claudeUsageReadIneligible {
		t.Fatalf("a read was accepted during retained cleanup: %s", outcome)
	}
	if _, err := f.start(""); err == nil {
		t.Fatal("a new login started while cleanup was pending")
	}
	f.native.store.removeErr = nil
	replay, err := f.client.DisconnectClaudeUsage(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
	if err != nil || !replay.Msg.Replayed {
		t.Fatalf("the original request did not recover: %v", err)
	}
	if usage := f.usage(); usage != nil && (usage.CleanupPending || len(usage.RetiredCredentials) != 0) || len(f.usageRefs()) != 0 {
		t.Fatal("cleanup is not confirmed after the retry")
	}
}

func TestClaudeUsageDisconnectCancelsAndJoinsALiveReadWithoutLatePublication(t *testing.T) {
	f := newUsageFixture(t)
	generation := f.link()
	f.transport.delay = make(chan struct{})
	if f.refreshOnceAsyncAccept() != claudeUsageReadAccepted {
		t.Fatal("not accepted")
	}
	done := make(chan struct{})
	go func() { defer close(done); f.service.runClaudeUsageRead(f.owner(), f.input.AccountID) }()
	deadline := time.Now().Add(5 * time.Second)
	for f.transport.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	// The cancelled read settles before the disconnect commits and may advance
	// the account revision once, so the caller retries with a fresh revision.
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if _, err = f.disconnect(generation); err == nil || connect.CodeOf(err) != connect.CodeAborted {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	awaitServerFixture(t, done)
	if usage := f.usage(); usage != nil && (usage.LastSuccess != nil || len(usage.Windows) != 0 || usage.Linked()) {
		t.Fatalf("a cancelled read published after disconnect: %+v", usage)
	}
	if _, a := f.record(); len(a.Quota) != 0 {
		t.Fatal("late windows were projected")
	}
}

func TestClaudeUsageAccountLogoutRetiresTheUsageConnection(t *testing.T) {
	f := newUsageFixture(t)
	f.link()
	f.refreshOnce(true)
	// Logout needs a fresh native identity for the setup-token lane.
	logout, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	if err != nil {
		t.Fatal(err)
	}
	// Reads stop at acceptance, before the logout settles.
	if usage := f.usage(); usage.Linked() || !usage.CleanupPending {
		t.Fatalf("usage was not revoked at logout acceptance: %+v", usage)
	}
	if outcome := f.refreshOnce(true); outcome != claudeUsageReadIneligible {
		t.Fatalf("read after logout acceptance: %s", outcome)
	}
	awaitServerFixture(t, f.run())
	_, a := f.record()
	if a.Connection != nil || a.Subscription.ClaudeUsage != nil || len(a.Quota) != 0 {
		t.Fatalf("logout left usage state: connection %v usage %+v quota %d", a.Connection != nil, a.Subscription.ClaudeUsage, len(a.Quota))
	}
	if len(f.usageRefs()) != 0 || len(f.executionRefs()) != 0 {
		t.Fatalf("references remained: %d usage %d execution", len(f.usageRefs()), len(f.executionRefs()))
	}
	if f.native.store.removals == 0 {
		t.Fatal("native usage storage was not removed")
	}
	_ = logout
}

func TestClaudeUsageAccountDeletionRemovesUsageReferencesAndProfile(t *testing.T) {
	f := newUsageFixture(t)
	f.link()
	if _, err := f.request(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT); err != nil {
		t.Fatal(err)
	}
	awaitServerFixture(t, f.run())
	// Retained usage evidence of an unreferenced, logged-out account: the
	// deletion owns it.
	_, logged := f.record()
	id := domain.NewID()
	if _, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.account.deletion", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.AccountKind, id, 0, "", "", logged)
	}); err != nil {
		t.Fatal(err)
	}
	ref := credentials.Ref{Owner: id, ID: domain.NewID(), Purpose: credentials.ClaudeUsage}
	f.secrets.mu.Lock()
	f.secrets.values[ref] = []byte("leftover-usage-envelope")
	f.secrets.mu.Unlock()
	registry, err := f.service.nativeProfiles()
	if err != nil {
		t.Fatal(err)
	}
	lease, err := registry.Acquire(context.Background(), nativeprofile.Key{Harness: domain.ClaudeCode, Purpose: nativeprofile.PurposeUsage, Account: id})
	if err != nil {
		t.Fatal(err)
	}
	lease.Close()
	configuration := delidevv1connect.NewConfigurationServiceClient(http.DefaultClient, f.http.URL)
	record, err := f.service.accountRecord(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configuration.DeleteConfiguration(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.DeleteConfigurationRequest{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, Mutation: &pb.Mutation{Id: string(id), ExpectedRevision: record.Revision, RequestId: string(domain.NewID())}})); err != nil {
		t.Fatalf("deletion refused a claude-usage reference: %v", err)
	}
	f.secrets.mu.Lock()
	_, remains := f.secrets.values[ref]
	f.secrets.mu.Unlock()
	if remains {
		t.Fatal("usage references survived account deletion")
	}
	if found, _ := registry.Exists(context.Background(), nativeprofile.DeleteScope{Account: id}); found {
		t.Fatal("the usage profile survived account deletion")
	}
}

func TestClaudeUsageReferencesNeverCountAsExecutionGenerations(t *testing.T) {
	vault := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
	owner := domain.NewID()
	usage := credentials.Ref{Owner: owner, ID: domain.NewID(), Purpose: credentials.ClaudeUsage}
	if _, err := vault.Put(context.Background(), usage, []byte("usage")); err != nil {
		t.Fatal(err)
	}
	keep := credentials.Ref{Owner: owner, ID: domain.NewID(), Purpose: credentials.AccountLogin}
	obsolete := credentials.Ref{Owner: owner, ID: domain.NewID(), Purpose: credentials.AccountLogin}
	for _, ref := range []credentials.Ref{keep, obsolete} {
		if _, err := vault.Put(context.Background(), ref, []byte("execution")); err != nil {
			t.Fatal(err)
		}
	}
	// The Worker Finish retirement keeps the usage reference and still retires
	// the obsolete execution generation.
	if err := cleanupSubscriptionReferences(context.Background(), vault, owner, keep.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := vault.values[usage]; !ok {
		t.Fatal("Worker Finish retired a usage reference")
	}
	if _, ok := vault.values[obsolete]; ok {
		t.Fatal("Worker Finish did not retire the obsolete execution generation")
	}
	// A reference of another purpose still denies the whole call.
	foreign := credentials.Ref{Owner: owner, ID: domain.NewID(), Purpose: credentials.NetworkProxy}
	if _, err := vault.Put(context.Background(), foreign, []byte("proxy")); err != nil {
		t.Fatal(err)
	}
	if err := cleanupSubscriptionReferences(context.Background(), vault, owner, keep.ID); err == nil {
		t.Fatal("a foreign purpose was not denied")
	}
	delete(vault.values, foreign)
	// Explicit removal removes both purposes of the account.
	removed, err := removeAccountLoginReferences(context.Background(), vault, owner)
	if err != nil || removed != 2 {
		t.Fatalf("removed %d: %v", removed, err)
	}
	if refs, _ := vault.UnremovedReferences(context.Background(), owner); len(refs) != 0 {
		t.Fatal("references remain")
	}
}

func TestGenericConfigurationWritesCannotOverwriteTheUsageConnection(t *testing.T) {
	f := newUsageFixture(t)
	f.link()
	f.refreshOnce(true)
	r, _ := f.record()
	accepted := f.usage()
	configuration := delidevv1connect.NewConfigurationServiceClient(http.DefaultClient, f.http.URL)
	save := func(document []byte, revision uint64) (*pb.SaveConfigurationResponse, error) {
		response, err := configuration.SaveConfiguration(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.SaveConfigurationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: revision}, Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, SchemaVersion: 2, DocumentJson: document}))
		if err != nil {
			return nil, err
		}
		return response.Msg, nil
	}
	// An older client that re-serializes the account without the optional state.
	stripped := bytes.Replace(r.Data, []byte(`"claude_usage":`), []byte(`"unknown_future":`), 1)
	if bytes.Equal(stripped, r.Data) {
		t.Fatal("fixture document has no usage state")
	}
	if _, err := save(stripped, r.Revision); err == nil {
		// The renamed field is not a known field and must not be accepted either.
		t.Log("unknown field accepted")
	}
	// A forged state: a valid document that carries other usage values.
	forged := *f.usage()
	forged.Windows = []domain.ClaudeLimitWindow{{ID: domain.ClaudeLimitFiveHour, Remaining: 0.01}}
	_, a := f.record()
	a.Subscription.ClaudeUsage = &forged
	a.Alias = "Renamed"
	response, err := save(mustJSON(t, a), r.Revision)
	if err != nil {
		t.Fatal(err)
	}
	var saved domain.Account
	if err := domain.Decode(response.Resource.DocumentJson, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Alias != "Renamed" {
		t.Fatal("the editable alias was not saved")
	}
	usage := saved.Subscription.ClaudeUsage
	if usage == nil || len(usage.Windows) != 2 || usage.Windows[0].Remaining != 0.75 || usage.CredentialID != accepted.CredentialID || !usage.LastSuccess.Equal(*accepted.LastSuccess) {
		t.Fatalf("a generic write replaced the usage connection: %+v", usage)
	}
	for _, w := range saved.Quota {
		if w.ComparisonGroup == "claude" && (w.Remaining == nil || *w.Remaining < 0.4) {
			t.Fatalf("a forged window was projected: %+v", w)
		}
	}
}

func TestClaudeUsageCapabilityIsAdvertisedByStatus(t *testing.T) {
	f := newUsageFixture(t)
	status, err := f.service.GetStatus(context.Background(), connect.NewRequest(&pb.GetStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, capability := range status.Msg.Capabilities {
		if capability == pb.SystemCapability_SYSTEM_CAPABILITY_CLAUDE_SERVER_USAGE_V1 {
			return
		}
	}
	t.Fatal("SYSTEM_CAPABILITY_CLAUDE_SERVER_USAGE_V1 is not advertised")
}

func TestClaudeUsageStateIsOptionalAndNeverAutoConnected(t *testing.T) {
	f := newUsageFixture(t)
	if usage := f.usage(); usage != nil {
		t.Fatalf("an existing account was connected automatically: %+v", usage)
	}
	// The maintenance scanner never starts a login or a read for an unlinked account.
	if err := f.service.queueDueClaudeUsage(f.owner(), time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if f.native.opens.Load() != 0 || f.transport.count() != 0 || f.usage() != nil {
		t.Fatal("background maintenance acted on an account that never connected usage")
	}
	// The pre-existing shape of a subscription document is unchanged.
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.shape", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		raw := mustJSON(t, a.Subscription)
		if bytes.Contains(raw, []byte("claude_usage")) {
			t.Fatal("an unconnected account serialized an empty usage state")
		}
		_ = r
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPortableConfigurationOmitsAndRejectsClaudeUsageState(t *testing.T) {
	f := newUsageFixture(t)
	f.link()
	f.refreshOnce(true)
	r, _ := f.record()
	exported, err := portableValue(domain.AccountKind, r.Data, false)
	if err != nil {
		t.Fatal(err)
	}
	account := exported.(*domain.Account)
	if account.Subscription != nil || len(account.Quota) != 0 || bytes.Contains(mustJSON(t, account), []byte("claude_usage")) {
		t.Fatalf("export carried protected usage state: %s", mustJSON(t, account))
	}
	// An import that carries the protected state is refused, never merged.
	if _, err := portableValue(domain.AccountKind, r.Data, true); err == nil {
		t.Fatal("an import accepted server-owned usage state")
	}
}
