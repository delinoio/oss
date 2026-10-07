// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type desktopReadSecrets struct {
	accountSecrets
	mu      sync.Mutex
	reads   []credentials.Ref
	buffers [][]byte
	read    func(context.Context, credentials.Ref) error
}

func (v *desktopReadSecrets) Get(ctx context.Context, ref credentials.Ref) ([]byte, error) {
	v.mu.Lock()
	v.reads = append(v.reads, ref)
	v.mu.Unlock()
	var err error
	if v.read != nil {
		err = v.read(ctx, ref)
	}
	key := []byte("startup-secret-sentinel")
	v.mu.Lock()
	v.buffers = append(v.buffers, key)
	v.mu.Unlock()
	return key, err
}
func desktopClient(t *testing.T, f *oauthFixture) domain.ID {
	t.Helper()
	id := domain.NewID()
	_, err := f.s.Store.Mutate(f.ctx, domain.NewID(), "fixture-client", id, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.DeviceKind, id, 0, "", "", domain.Device{Name: "Desktop fixture", Type: domain.ClientDevice, PairedAt: time.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func desktopAPI(t *testing.T, f *oauthFixture, auth domain.Authentication, connected bool) *pb.Resource {
	t.Helper()
	p, err := saveAPIFormatConfiguration(f, domain.ProviderKind, domain.Provider{Name: "Startup fixture", Protocol: domain.OpenAIResponses, Endpoint: "http://127.0.0.1:1/v1", Authentication: auth}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err := saveAPIFormatConfiguration(f, domain.AccountKind, domain.Account{Alias: "Startup fixture", Type: domain.APIAccount, ProviderID: domain.ID(p.Id), Health: domain.AccountDisconnected}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !connected {
		return a
	}
	input := &pb.ConnectAccountRequest{Mutation: acctMutation(a, domain.NewID()), Keyless: auth == domain.KeylessAuth}
	if !input.Keyless {
		input.ApiKey = []byte("startup-secret-sentinel")
	}
	r, err := f.s.ConnectAccount(f.ctx, connect.NewRequest(input))
	if err != nil {
		t.Fatal(err)
	}
	return r.Msg.Account
}
func desktopAccess(t *testing.T, f *oauthFixture) (*DesktopCredentialAccess, domain.ID, domain.ID) {
	t.Helper()
	client := desktopClient(t, f)
	c := &DesktopCredentialAccess{platform: "darwin"}
	c.attach(f.ctx, f.s)
	t.Cleanup(c.close)
	return c, client, domain.NewID()
}
func waitDesktopAccess(t *testing.T, c *DesktopCredentialAccess) DesktopCredentialResult {
	t.Helper()
	c.mu.Lock()
	done := c.done
	c.mu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("startup read did not settle")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.result
}

func TestDesktopCredentialsPreserveAccountsAndClearReadBuffers(t *testing.T) {
	f := newOAuthFixture(t)
	a := desktopAPI(t, f, domain.BearerAuth, true)
	desktopAPI(t, f, domain.KeylessAuth, true)
	desktopAPI(t, f, domain.BearerAuth, false)
	spy := &desktopReadSecrets{accountSecrets: f.vault}
	f.s.accountSecrets = spy
	c, client, attempt := desktopAccess(t, f)
	if _, err := c.Handle(f.ctx, DesktopCredentialBegin, attempt, "", client); err != nil {
		t.Fatal(err)
	}
	if got := waitDesktopAccess(t, c); got.State != DesktopCredentialSucceeded {
		t.Fatalf("result: %+v", got)
	}
	for range 5 {
		if _, err := c.Handle(f.ctx, DesktopCredentialBegin, attempt, "", client); err != nil {
			t.Fatal(err)
		}
	}
	if len(spy.reads) != 1 || spy.reads[0].Owner != domain.ID(a.Id) || spy.reads[0].ID != accountBody(t, a).Connection.ID {
		t.Fatal("startup read another credential generation")
	}
	for _, b := range spy.buffers {
		if !bytes.Equal(b, make([]byte, len(b))) {
			t.Fatal("startup retained credential bytes")
		}
	}
	record, err := f.s.Store.Get(f.ctx, domain.AccountKind, domain.ID(a.Id))
	if err != nil || record.Revision != a.Revision || !bytes.Equal(record.Data, a.DocumentJson) {
		t.Fatal("startup changed account status/revision")
	}
	if bytes.Contains(f.logs.Bytes(), []byte("startup-secret-sentinel")) {
		t.Fatal("secret reached logs")
	}
}

func TestDesktopCredentialsDoNotInitializeMissingCredentialScope(t *testing.T) {
	f := newOAuthFixture(t)
	desktopAPI(t, f, domain.BearerAuth, true)
	secretRoot := filepath.Join(f.root, "secrets")
	if err := os.RemoveAll(secretRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(secretRoot); !os.IsNotExist(err) {
		t.Fatalf("fixture unexpectedly has a credential scope: %v", err)
	}
	// The account metadata still points at the fake, server-owned credential,
	// but startup must not create a real scope just to inspect its readiness.
	f.s.accountSecrets = nil
	c, client, attempt := desktopAccess(t, f)
	c.Handle(f.ctx, DesktopCredentialBegin, attempt, "", client)
	if got := waitDesktopAccess(t, c); got.State != DesktopCredentialFailed {
		t.Fatalf("missing scope was treated as ready: %+v", got)
	}
	if _, err := os.Stat(secretRoot); !os.IsNotExist(err) {
		t.Fatalf("startup initialized the missing credential scope: %v", err)
	}
}

func TestDesktopCredentialsFailureRequiresExplicitNewAttempt(t *testing.T) {
	f := newOAuthFixture(t)
	a := desktopAPI(t, f, domain.BearerAuth, true)
	spy := &desktopReadSecrets{accountSecrets: f.vault, read: func(context.Context, credentials.Ref) error {
		return domain.Fail(domain.ConfirmationRequired, "Unsafe native diagnostic sentinel", "")
	}}
	f.s.accountSecrets = spy
	c, client, attempt := desktopAccess(t, f)
	c.Handle(f.ctx, DesktopCredentialBegin, attempt, "", client)
	original := waitDesktopAccess(t, c)
	if original.State != DesktopCredentialFailed || original.Issue != credentialConfirmation {
		t.Fatalf("result: %+v", original)
	}
	c.close()
	c.attach(f.ctx, f.s)
	if got, err := c.Handle(f.ctx, DesktopCredentialStatus, attempt, "", client); err != nil || got != original || len(spy.reads) != 1 {
		t.Fatalf("reattachment restarted the original attempt: %+v %v", got, err)
	}
	c.Handle(f.ctx, DesktopCredentialBegin, attempt, "", client)
	if len(spy.reads) != 1 {
		t.Fatal("observation retried denied authentication")
	}
	if _, err := c.Handle(f.ctx, DesktopCredentialBegin, domain.NewID(), "", client); err == nil {
		t.Fatal("fresh automatic attempt replaced failure")
	}
	spy.read = nil
	if _, err := c.Handle(f.ctx, DesktopCredentialRetry, domain.NewID(), attempt, client); err != nil {
		t.Fatal(err)
	}
	if got := waitDesktopAccess(t, c); got.State != DesktopCredentialSucceeded || len(spy.reads) != 2 {
		t.Fatalf("retry: %+v", got)
	}
	for _, b := range spy.buffers {
		if !bytes.Equal(b, make([]byte, len(b))) {
			t.Fatal("failed read buffer retained")
		}
	}
	row, _ := f.s.Store.Get(f.ctx, domain.AccountKind, domain.ID(a.Id))
	if row.Revision != a.Revision {
		t.Fatal("failure changed account revision")
	}
	if bytes.Contains(f.logs.Bytes(), []byte("Unsafe native diagnostic sentinel")) {
		t.Fatal("raw native diagnostic logged")
	}
}

func TestDesktopCredentialsRebindAfterAuthorizedLocalRegistrationRecovery(t *testing.T) {
	f := newOAuthFixture(t)
	desktopAPI(t, f, domain.BearerAuth, true)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	spy := &desktopReadSecrets{accountSecrets: f.vault, read: func(context.Context, credentials.Ref) error {
		once.Do(func() { close(entered) })
		<-release
		return nil
	}}
	f.s.accountSecrets = spy
	c, originalClient, attempt := desktopAccess(t, f)
	if _, err := c.Handle(f.ctx, DesktopCredentialBegin, attempt, "", originalClient); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("startup read did not reach the protected credential")
	}
	replacementClient := desktopClient(t, f)
	if _, err := c.Handle(f.ctx, DesktopCredentialStatus, attempt, "", replacementClient); err == nil {
		t.Fatal("active original client allowed an unrelated device to rebind the attempt")
	}
	_, err := f.s.Store.Mutate(f.ctx, domain.NewID(), "fixture-revoke-original-client", originalClient, func(tx *store.Tx) (any, error) {
		record, err := tx.Get(domain.DeviceKind, originalClient)
		if err != nil {
			return nil, err
		}
		device, err := store.Decode[domain.Device](record)
		if err != nil {
			return nil, err
		}
		device.Revoked = true
		return tx.Put(domain.DeviceKind, originalClient, record.Revision, "", "", device)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := c.Handle(f.ctx, DesktopCredentialStatus, attempt, "", replacementClient); err != nil || got.State != DesktopCredentialChecking {
		t.Fatalf("authorized replacement could not observe the original in-flight attempt: %+v %v", got, err)
	}
	close(release)
	if got := waitDesktopAccess(t, c); got.State != DesktopCredentialSucceeded {
		t.Fatalf("rebound attempt: %+v", got)
	}
	if len(spy.reads) != 1 {
		t.Fatal("registration recovery opened another Keychain read")
	}
}

func TestDesktopCredentialsSkipStopsFurtherReadsAndCloseJoinsPrompt(t *testing.T) {
	f := newOAuthFixture(t)
	desktopAPI(t, f, domain.BearerAuth, true)
	desktopAPI(t, f, domain.BearerAuth, true)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	spy := &desktopReadSecrets{accountSecrets: f.vault, read: func(context.Context, credentials.Ref) error {
		once.Do(func() { close(entered) })
		<-release
		return nil
	}}
	f.s.accountSecrets = spy
	c, client, attempt := desktopAccess(t, f)
	c.Handle(f.ctx, DesktopCredentialBegin, attempt, "", client)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no native read")
	}
	if got, err := c.Handle(f.ctx, DesktopCredentialSkip, attempt, "", client); err != nil || got.State != DesktopCredentialSkipped {
		t.Fatal("skip did not return immediately", err)
	}
	closed := make(chan struct{})
	go func() { c.close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("close detached native prompt")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("close did not join")
	}
	if len(spy.reads) != 1 {
		t.Fatal("skip accessed another reference")
	}
}

func TestDesktopCredentialsOAuthReadsOriginalTokenWithoutRefresh(t *testing.T) {
	for _, state := range []domain.OAuthRefreshState{domain.OAuthRefreshClaimed, domain.OAuthRefreshRecovery, domain.OAuthRefreshDenied} {
		t.Run(string(state), func(t *testing.T) {
			f := newHuggingFaceFixture(t)
			a := f.connectedHF(t, time.Now().Add(-time.Minute))
			body := accountBody(t, a)
			var metadata domain.AccountOAuthCredential
			if err := f.s.Store.Read(f.ctx, func(tx *store.Tx) error {
				var err error
				metadata, _, err = tx.AccountOAuthCredential(domain.ID(a.Id), body.Connection.ID)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			metadata.RefreshState = state
			metadata.RefreshID = domain.NewID()
			metadata.Revision++
			if _, err := f.s.Store.Mutate(f.ctx, domain.NewID(), "fixture-refresh-state", domain.ID(a.Id), func(tx *store.Tx) (any, error) {
				return nil, tx.PutAccountOAuthCredential(metadata, metadata.Revision-1)
			}); err != nil {
				t.Fatal(err)
			}
			spy := &desktopReadSecrets{accountSecrets: f.vault}
			f.s.accountSecrets = spy
			c, client, attempt := desktopAccess(t, f)
			c.Handle(f.ctx, DesktopCredentialBegin, attempt, "", client)
			if got := waitDesktopAccess(t, c); got.State != DesktopCredentialSucceeded {
				t.Fatalf("result: %+v", got)
			}
			if len(spy.reads) != 1 || spy.reads[0].ID != metadata.TokenID {
				t.Fatal("did not read the original OAuth token")
			}
			var after domain.AccountOAuthCredential
			if err := f.s.Store.Read(f.ctx, func(tx *store.Tx) error {
				var err error
				after, _, err = tx.AccountOAuthCredential(domain.ID(a.Id), body.Connection.ID)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if after.Revision != metadata.Revision || after.TokenID != metadata.TokenID || after.RefreshState != state || after.RefreshID != metadata.RefreshID {
				t.Fatal("startup changed OAuth metadata or refreshed credentials")
			}
		})
	}
}

func TestDesktopCredentialsReadSharedKeyAfterAPIFormatChange(t *testing.T) {
	f := newOAuthFixture(t)
	account, err := saveAPIFormatConfiguration(f, domain.AccountKind, domain.Account{
		Alias: "Startup format fixture", Type: domain.APIAccount, ProviderID: domain.ID(f.provider.Id),
		APIProtocol: domain.OpenAIChat, Enabled: true, Health: domain.AccountDisconnected,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	connected, err := f.s.ConnectAccount(f.ctx, connect.NewRequest(&pb.ConnectAccountRequest{
		Mutation: acctMutation(account, domain.NewID()), ApiKey: []byte("startup-format-key"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	original := accountBody(t, connected.Msg.Account).Connection.CredentialReferenceID()
	changed, err := f.s.ChangeAccountApiFormat(f.ctx, connect.NewRequest(formatRequest(connected.Msg.Account, pb.ApiProtocol_API_PROTOCOL_OPENAI_RESPONSES)))
	if err != nil {
		t.Fatal(err)
	}
	body := accountBody(t, changed.Msg.Account)
	if body.Connection.ID == original || body.Connection.CredentialReferenceID() != original {
		t.Fatal("format change did not preserve the shared credential reference")
	}
	spy := &desktopReadSecrets{accountSecrets: f.vault}
	f.s.accountSecrets = spy
	c, client, attempt := desktopAccess(t, f)
	c.Handle(f.ctx, DesktopCredentialBegin, attempt, "", client)
	if got := waitDesktopAccess(t, c); got.State != DesktopCredentialSucceeded {
		t.Fatalf("result: %+v", got)
	}
	if len(spy.reads) != 1 || spy.reads[0].ID != original {
		t.Fatalf("startup did not read the shared credential reference: %+v", spy.reads)
	}
}

func TestDesktopCredentialsOAuthReadsSharedReferenceAfterAPIFormatChange(t *testing.T) {
	f := newHuggingFaceFixture(t)
	connected := f.connectedHF(t, time.Now().Add(time.Minute))
	connectedBody := accountBody(t, connected)
	original := connectedBody.Connection.CredentialReferenceID()
	protocol := pb.ApiProtocol_API_PROTOCOL_OPENAI_RESPONSES
	if connectedBody.APIProtocol == domain.OpenAIResponses {
		protocol = pb.ApiProtocol_API_PROTOCOL_OPENAI_CHAT
	}
	changed, err := f.s.ChangeAccountApiFormat(f.ctx, connect.NewRequest(formatRequest(connected, protocol)))
	if err != nil {
		t.Fatal(err)
	}
	var metadata domain.AccountOAuthCredential
	var found bool
	if err := f.s.Store.Read(f.ctx, func(tx *store.Tx) error {
		var err error
		metadata, found, err = tx.AccountOAuthCredential(domain.ID(connected.Id), original)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("OAuth metadata was not retained under the shared reference")
	}
	body := accountBody(t, changed.Msg.Account)
	if body.Connection.ID == original || body.Connection.CredentialReferenceID() != original {
		t.Fatal("format change did not preserve the OAuth metadata reference")
	}
	spy := &desktopReadSecrets{accountSecrets: f.vault}
	f.s.accountSecrets = spy
	c, client, attempt := desktopAccess(t, f)
	c.Handle(f.ctx, DesktopCredentialBegin, attempt, "", client)
	if got := waitDesktopAccess(t, c); got.State != DesktopCredentialSucceeded {
		t.Fatalf("result: %+v", got)
	}
	if len(spy.reads) != 1 || spy.reads[0].ID != metadata.TokenID {
		t.Fatalf("startup did not read the original OAuth token reference: got %+v, want %s", spy.reads, metadata.TokenID)
	}
}

func TestDesktopCredentialsEmptyAndRevokedClient(t *testing.T) {
	f := newOAuthFixture(t)
	c, client, attempt := desktopAccess(t, f)
	if _, err := c.Handle(f.ctx, DesktopCredentialBegin, attempt, "", domain.NewID()); err == nil {
		t.Fatal("foreign client admitted")
	}
	c.Handle(f.ctx, DesktopCredentialBegin, attempt, "", client)
	if got := waitDesktopAccess(t, c); got.State != DesktopCredentialSucceeded || f.s.ownedVault != nil {
		t.Fatal("empty accounts required a vault")
	}
	_, err := f.s.Store.Mutate(f.ctx, domain.NewID(), "fixture-revoke", client, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.DeviceKind, client)
		if err != nil {
			return nil, err
		}
		d, err := store.Decode[domain.Device](r)
		if err != nil {
			return nil, err
		}
		d.Revoked = true
		return tx.Put(domain.DeviceKind, client, r.Revision, "", "", d)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Handle(f.ctx, DesktopCredentialStatus, attempt, "", client); err == nil {
		t.Fatal("revoked client observed credentials")
	}
}

func TestDesktopCredentialsPagesAndResolvesSelectedProfile(t *testing.T) {
	f := newOAuthFixture(t)
	p, err := saveAPIFormatConfiguration(f, domain.ProviderKind, domain.Provider{Name: "Mixed formats", Protocol: domain.OpenAIResponses, Endpoint: "http://127.0.0.1:1/v1", Authentication: domain.KeylessAuth, APIFormats: []domain.ProviderAPIFormat{
		{Protocol: domain.OpenAIResponses, Endpoint: "http://127.0.0.1:1/v1", Authentication: domain.KeylessAuth},
		{Protocol: domain.OpenAIChat, Endpoint: "http://127.0.0.1:2/v1", Authentication: domain.BearerAuth},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.s.Store.Mutate(f.ctx, domain.NewID(), "fixture-page", nil, func(tx *store.Tx) (any, error) {
		for range store.MaxPage + 1 {
			_, err := tx.Put(domain.AccountKind, domain.NewID(), 0, "", "", domain.Account{Alias: "Disconnected", Type: domain.APIAccount, ProviderID: domain.ID(p.Id), Health: domain.AccountDisconnected})
			if err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := saveAPIFormatConfiguration(f, domain.AccountKind, domain.Account{Alias: "Selected credential format", Type: domain.APIAccount, ProviderID: domain.ID(p.Id), APIProtocol: domain.OpenAIChat, Health: domain.AccountDisconnected}, nil)
	if err != nil {
		t.Fatal(err)
	}
	connected, err := f.s.ConnectAccount(f.ctx, connect.NewRequest(&pb.ConnectAccountRequest{Mutation: acctMutation(a, domain.NewID()), ApiKey: []byte("startup-secret-sentinel")}))
	if err != nil {
		t.Fatal(err)
	}
	spy := &desktopReadSecrets{accountSecrets: f.vault}
	f.s.accountSecrets = spy
	c, client, attempt := desktopAccess(t, f)
	c.Handle(f.ctx, DesktopCredentialBegin, attempt, "", client)
	if got := waitDesktopAccess(t, c); got.State != DesktopCredentialSucceeded || len(spy.reads) != 1 || spy.reads[0].Owner != domain.ID(connected.Msg.Account.Id) {
		t.Fatalf("selected profile after first page: %+v", got)
	}
}

func TestDesktopCredentialsSkipBeforeUnknownBegin(t *testing.T) {
	f := newOAuthFixture(t)
	desktopAPI(t, f, domain.BearerAuth, true)
	spy := &desktopReadSecrets{accountSecrets: f.vault}
	f.s.accountSecrets = spy
	c, client, attempt := desktopAccess(t, f)
	got, err := c.Handle(f.ctx, DesktopCredentialSkip, attempt, "", client)
	if err != nil || got.State != DesktopCredentialSkipped {
		t.Fatalf("skip: %+v %v", got, err)
	}
	got, err = c.Handle(f.ctx, DesktopCredentialBegin, attempt, "", client)
	if err != nil || got.State != DesktopCredentialSkipped || len(spy.reads) != 0 {
		t.Fatalf("late begin: %+v %v", got, err)
	}
}

func TestDesktopCredentialsSkipBeforeUnknownRetry(t *testing.T) {
	f := newOAuthFixture(t)
	desktopAPI(t, f, domain.BearerAuth, true)
	spy := &desktopReadSecrets{accountSecrets: f.vault, read: func(context.Context, credentials.Ref) error {
		return domain.Fail(domain.ConfirmationRequired, "fixture denied", "")
	}}
	f.s.accountSecrets = spy
	c, client, attempt := desktopAccess(t, f)
	c.Handle(f.ctx, DesktopCredentialBegin, attempt, "", client)
	if got := waitDesktopAccess(t, c); got.State != DesktopCredentialFailed {
		t.Fatal(got)
	}
	retry := domain.NewID()
	got, err := c.Handle(f.ctx, DesktopCredentialSkip, retry, attempt, client)
	if err != nil || got.State != DesktopCredentialSkipped {
		t.Fatalf("skip: %+v %v", got, err)
	}
	got, err = c.Handle(f.ctx, DesktopCredentialRetry, retry, attempt, client)
	if err != nil || got.State != DesktopCredentialSkipped || len(spy.reads) != 1 {
		t.Fatalf("late retry: %+v %v", got, err)
	}
}
