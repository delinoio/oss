// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type oauthTokenFixture struct {
	exchange func(context.Context, oauthProfile, []byte, []byte, string) (oauthTokenResult, error)
	refresh  func(context.Context, oauthProfile, []byte) (oauthTokenResult, error)
}

func (f oauthTokenFixture) Exchange(c context.Context, p oauthProfile, code, v []byte, cb string) (oauthTokenResult, error) {
	return f.exchange(c, p, code, v, cb)
}
func (f oauthTokenFixture) Refresh(c context.Context, p oauthProfile, v []byte) (oauthTokenResult, error) {
	return f.refresh(c, p, v)
}
func newHuggingFaceFixture(t *testing.T) *oauthFixture {
	f := newOAuthFixture(t)
	f.s.oauthRegistrations = map[domain.ProviderPresetID]providers.OAuthRegistration{domain.PresetHuggingFace: {ClientID: "delidev-fixture-public-client", RedirectURI: "http://localhost/oauth/hugging-face/callback", Registration: providers.OAuthRegistered, Compatibility: providers.OAuthAPIAccepted}}
	r, e := f.s.ListProviderInventory(f.ctx, connect.NewRequest(&pb.ListProviderInventoryRequest{}))
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range r.Msg.Entries {
		if p.PresetId == pb.ProviderPresetId_PROVIDER_PRESET_ID_HUGGING_FACE {
			f.provider = p.Provider
		}
	}
	if f.provider == nil {
		t.Fatal("missing managed provider")
	}
	return f
}
func (f *oauthFixture) startHF(t *testing.T) (*pb.StartAccountOAuthResponse, string) {
	t.Helper()
	r, e := f.s.StartAccountOAuth(f.ctx, connect.NewRequest(&pb.StartAccountOAuthRequest{Provider: acctMutation(f.provider, domain.NewID()), CallbackUrl: "http://localhost:55451/oauth/hugging-face/callback"}))
	if e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(r.Msg.AuthorizationUrl)
	if e != nil || u.Host != "huggingface.co" || len(u.Query().Get("state")) != 43 || u.Query().Get("scope") != "inference-api" || r.Msg.Flow != pb.AccountOAuthFlow_ACCOUNT_OAUTH_FLOW_PKCE || r.Msg.UserCode != "" {
		t.Fatal("wrong PKCE profile")
	}
	return r.Msg, u.Query().Get("state")
}
func (f *oauthFixture) completeHF(a *pb.AccountOAuthAttempt, id domain.ID, code, state string) (*connect.Response[pb.CompleteAccountOAuthResponse], error) {
	return f.s.CompleteAccountOAuth(f.ctx, connect.NewRequest(&pb.CompleteAccountOAuthRequest{Mutation: oauthMutation(a, id), AuthorizationCode: []byte(code), AuthorizationState: []byte(state)}))
}
func tokenResult(access, refresh string, expiry time.Time) oauthTokenResult {
	return oauthTokenResult{tokens: oauthTokens{Access: []byte(access), Refresh: []byte(refresh)}, expires: expiry}
}
func (f *oauthFixture) connectedHF(t *testing.T, expiry time.Time) *pb.Resource {
	t.Helper()
	f.s.oauthTokenClient = oauthTokenFixture{exchange: func(context.Context, oauthProfile, []byte, []byte, string) (oauthTokenResult, error) {
		return tokenResult("hf-access-sentinel", "hf-refresh-sentinel", expiry), nil
	}, refresh: func(context.Context, oauthProfile, []byte) (oauthTokenResult, error) {
		t.Fatal("unexpected refresh")
		return oauthTokenResult{}, nil
	}}
	a, state := f.startHF(t)
	r, e := f.completeHF(a.Attempt, domain.NewID(), "hf-code-sentinel", state)
	if e != nil || r.Msg.Account == nil {
		t.Fatalf("connect: %v / %v", e, r)
	}
	return r.Msg.Account
}
func TestHuggingFaceStateDenialAndRegistrationGate(t *testing.T) {
	f := newHuggingFaceFixture(t)
	var calls atomic.Int32
	f.s.oauthTokenClient = oauthTokenFixture{exchange: func(context.Context, oauthProfile, []byte, []byte, string) (oauthTokenResult, error) {
		calls.Add(1)
		return tokenResult("access", "refresh", time.Now().Add(time.Hour)), nil
	}}
	a, state := f.startHF(t)
	_, e := f.completeHF(a.Attempt, domain.NewID(), "code", state+"x")
	wantAccountCode(t, e, domain.PermissionDenied)
	id := domain.NewID()
	r, e := f.completeHF(a.Attempt, id, "", state)
	if e != nil || r.Msg.Attempt.State != pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_FAILED {
		t.Fatalf("denial: %v / %v", e, r)
	}
	r, e = f.completeHF(a.Attempt, id, "", "")
	if e != nil || !r.Msg.Replayed {
		t.Fatalf("denial replay: %v", e)
	}
	if calls.Load() != 0 {
		t.Fatal("denial sent exchange")
	}
	f.s.oauthRegistrations = nil
	inventory, e := f.s.ListProviderInventory(f.ctx, connect.NewRequest(&pb.ListProviderInventoryRequest{}))
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range inventory.Msg.Capabilities {
		if c == pb.ProviderInventoryCapability_PROVIDER_INVENTORY_CAPABILITY_ACCOUNT_OAUTH_V1 {
			t.Fatal("pending app advertised capability")
		}
	}
	for _, p := range inventory.Msg.Entries {
		if p.PresetId == pb.ProviderPresetId_PROVIDER_PRESET_ID_HUGGING_FACE && p.ConnectionMethod != pb.ProviderConnectionMethod_PROVIDER_CONNECTION_METHOD_API_KEY {
			t.Fatal("pending app advertised OAuth")
		}
	}
	_, e = f.s.StartAccountOAuth(f.ctx, connect.NewRequest(&pb.StartAccountOAuthRequest{Provider: acctMutation(f.provider, domain.NewID()), CallbackUrl: "http://localhost:55451/oauth/hugging-face/callback"}))
	wantAccountCode(t, e, domain.Unsupported)
}
func TestHuggingFaceRefreshSerializedRotatesWithoutChangingConnection(t *testing.T) {
	f := newHuggingFaceFixture(t)
	account := f.connectedHF(t, time.Now().Add(30*time.Second))
	body := accountBody(t, account)
	began, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	f.s.oauthTokenClient = oauthTokenFixture{refresh: func(ctx context.Context, p oauthProfile, refresh []byte) (oauthTokenResult, error) {
		if calls.Add(1) != 1 {
			t.Error("refresh duplicated")
		}
		if p.preset != domain.PresetHuggingFace || string(refresh) != "hf-refresh-sentinel" {
			t.Error("wrong refresh scope")
		}
		close(began)
		<-release
		return tokenResult("rotated-access-sentinel", "rotated-refresh-sentinel", time.Now().Add(time.Hour)), nil
	}}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := f.s.resolveAPICredential(f.ctx, domain.ID(account.Id), body.Connection.ID, body.ProviderID)
			if e == nil && string(r.key) != "rotated-access-sentinel" {
				e = io.ErrUnexpectedEOF
			}
			clear(r.key)
			errs <- e
		}()
	}
	<-began
	close(release)
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("multiple refreshes")
	}
	var metadata domain.AccountOAuthCredential
	e := f.s.Store.Read(f.ctx, func(tx *store.Tx) error {
		var err error
		metadata, _, err = tx.AccountOAuthCredential(domain.ID(account.Id), body.Connection.ID)
		row, e := tx.Get(domain.AccountKind, domain.ID(account.Id))
		if e != nil {
			return e
		}
		saved, e := store.Decode[domain.Account](row)
		if e != nil {
			return e
		}
		if row.Revision != account.Revision || saved.Connection.ID != body.Connection.ID {
			t.Error("refresh replaced public connection")
		}
		return err
	})
	if e != nil {
		t.Fatal(e)
	}
	if metadata.TokenID == body.Connection.ID || metadata.RefreshState != domain.OAuthRefreshIdle || len(metadata.Cleanup) != 0 {
		t.Fatal("refresh publication was not atomic")
	}
	assertOAuthPrivate(t, f, "hf-access-sentinel", "hf-refresh-sentinel", "rotated-access-sentinel", "rotated-refresh-sentinel")
}
func TestHuggingFaceRefreshUncertaintyNeverResendsAcrossRestart(t *testing.T) {
	for _, scenario := range []string{"lost-response", "lost-vault-ack", "denied"} {
		t.Run(scenario, func(t *testing.T) {
			f := newHuggingFaceFixture(t)
			account := f.connectedHF(t, time.Now().Add(time.Second))
			body := accountBody(t, account)
			var calls atomic.Int32
			f.s.oauthTokenClient = oauthTokenFixture{refresh: func(context.Context, oauthProfile, []byte) (oauthTokenResult, error) {
				calls.Add(1)
				switch scenario {
				case "lost-response":
					return oauthTokenResult{}, io.ErrUnexpectedEOF
				case "denied":
					return oauthTokenResult{}, domain.Fail(domain.PermissionDenied, "denied", "")
				}
				return tokenResult("new-protected-access", "new-protected-refresh", time.Now().Add(time.Hour)), nil
			}}
			if scenario == "lost-vault-ack" {
				f.vault.putError = io.ErrUnexpectedEOF
			}
			_, e := f.s.resolveAPICredential(f.ctx, domain.ID(account.Id), body.Connection.ID, body.ProviderID)
			if e == nil {
				t.Fatal("uncertainty returned token")
			}
			registrations, client := f.s.oauthRegistrations, f.s.oauthTokenClient
			f.restart(t)
			f.s.oauthRegistrations = registrations
			f.s.oauthTokenClient = client
			f.vault.putError = nil
			_, e = f.s.resolveAPICredential(f.ctx, domain.ID(account.Id), body.Connection.ID, body.ProviderID)
			if e == nil || calls.Load() != 1 {
				t.Fatal("uncertain refresh was resent")
			}
		})
	}
}
func TestHuggingFaceVaultRecoveryAndCallbackIsolation(t *testing.T) {
	f := newHuggingFaceFixture(t)
	for _, callback := range []string{"", "http://127.0.0.1:55451/oauth/hugging-face/callback", "http://localhost:55451/oauth/hugging-face/callback?foreign=1", "http://localhost:55451/oauth/openrouter/callback"} {
		_, e := f.s.StartAccountOAuth(f.ctx, connect.NewRequest(&pb.StartAccountOAuthRequest{Provider: acctMutation(f.provider, domain.NewID()), CallbackUrl: callback}))
		wantAccountCode(t, e, domain.InvalidArgument)
	}
	var calls atomic.Int32
	f.s.oauthTokenClient = oauthTokenFixture{exchange: func(context.Context, oauthProfile, []byte, []byte, string) (oauthTokenResult, error) {
		calls.Add(1)
		return tokenResult("recovered-access-sentinel", "recovered-refresh-sentinel", time.Now().Add(time.Hour)), nil
	}}
	a, state := f.startHF(t)
	id := domain.NewID()
	f.vault.putError = io.ErrUnexpectedEOF
	r, e := f.completeHF(a.Attempt, id, "original-code", state)
	if e != nil || r.Msg.Attempt.State != pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_RECOVERY_REQUIRED {
		t.Fatalf("vault: %v", e)
	}
	f.vault.putError = nil
	r, e = f.completeHF(a.Attempt, id, "", "")
	if e != nil || r.Msg.Account == nil || calls.Load() != 1 {
		t.Fatalf("local recovery: %v", e)
	}
	body := accountBody(t, r.Msg.Account)
	key, e := f.s.resolveAPICredential(f.ctx, domain.ID(r.Msg.Account.Id), body.Connection.ID, body.ProviderID)
	if e != nil || string(key.key) != "recovered-access-sentinel" {
		t.Fatal("protected envelope incompatible")
	}
	clear(key.key)
	_, e = f.s.resolveAPICredential(f.ctx, domain.ID(r.Msg.Account.Id), body.Connection.ID, domain.NewID())
	if e == nil {
		t.Fatal("foreign provider read credential")
	}
	assertOAuthPrivate(t, f, "original-code", state, "recovered-access-sentinel", "recovered-refresh-sentinel")
}
func assertOAuthPrivate(t *testing.T, f *oauthFixture, values ...string) {
	t.Helper()
	for _, v := range values {
		if strings.Contains(f.logs.String(), v) {
			t.Fatal("secret reached logs")
		}
	}
	e := filepath.WalkDir(f.root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if info.Size() == 0 {
			return nil
		}
		raw, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		for _, v := range values {
			if bytes.Contains(raw, []byte(v)) {
				t.Fatal("secret reached SQLite/state")
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
func TestOAuthTokenHTTPOnceOnlyClosedResponse(t *testing.T) {
	for _, scenario := range []string{"ok", "redirect", "duplicate", "lost-response", "denied", "reduced-scope", "bad-expiry", "oversized"} {
		t.Run(scenario, func(t *testing.T) {
			var posts atomic.Int32
			client := ownedOAuthTokenClient{transport: oauthHTTPTransport(func(r *http.Request) (*http.Response, error) {
				posts.Add(1)
				raw, e := io.ReadAll(r.Body)
				if e != nil {
					t.Fatal(e)
				}
				q, e := url.ParseQuery(string(raw))
				if e != nil || q.Get("code") != "code+private" || q.Get("client_id") != "fixture" || q.Get("code_verifier") != "verifier" || r.URL.String() != "https://huggingface.co/oauth/token" || r.GetBody != nil || r.Header.Get("Authorization") != "" {
					t.Fatal("wrong request authority")
				}
				status := 200
				body := `{"token_type":"Bearer","access_token":"protected-access","refresh_token":"protected-refresh","expires_in":3600,"scope":"inference-api"}`
				switch scenario {
				case "redirect":
					status = 307
					body = `{}`
				case "duplicate":
					body = `{"access_token":"one","access_token":"two"}`
				case "lost-response":
					return nil, io.ErrUnexpectedEOF
				case "denied":
					status = 400
					body = `{"error":"invalid_grant"}`
				case "reduced-scope":
					body = strings.Replace(body, "inference-api", "openid", 1)
				case "bad-expiry":
					body = strings.Replace(body, "3600", "0", 1)
				case "oversized":
					body = strings.Repeat("x", 65537)
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Location": []string{"https://foreign.test"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			result, e := client.Exchange(context.Background(), oauthProfile{token: "https://huggingface.co/oauth/token", scope: "inference-api", registration: providers.OAuthRegistration{ClientID: "fixture"}}, []byte("code+private"), []byte("verifier"), "http://localhost:1/oauth/hugging-face/callback")
			defer result.tokens.clear()
			if posts.Load() != 1 || (scenario == "ok") != (e == nil) {
				t.Fatalf("request/response: %d %v", posts.Load(), e)
			}
		})
	}
}

func TestHuggingFaceRefreshCannotPublishAfterDisconnect(t *testing.T) {
	f := newHuggingFaceFixture(t)
	account := f.connectedHF(t, time.Now().Add(time.Second))
	body := accountBody(t, account)
	began, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	f.s.oauthTokenClient = oauthTokenFixture{refresh: func(context.Context, oauthProfile, []byte) (oauthTokenResult, error) {
		close(began)
		<-release
		return tokenResult("late-access", "late-refresh", time.Now().Add(time.Hour)), nil
	}}
	go func() {
		r, e := f.s.resolveAPICredential(f.ctx, domain.ID(account.Id), body.Connection.ID, body.ProviderID)
		clear(r.key)
		finished <- e
	}()
	<-began
	_, e := f.s.DisconnectAccount(f.ctx, connect.NewRequest(&pb.DisconnectAccountRequest{Mutation: acctMutation(account, domain.NewID())}))
	if e != nil {
		t.Fatal(e)
	}
	close(release)
	if e := <-finished; e == nil {
		t.Fatal("disconnected refresh returned token")
	}
	_, _, active := f.vault.counts()
	if active != 0 {
		t.Fatal("late token escaped account cleanup")
	}
}

func TestOAuthRecoveryRetainsOriginalAdapterAndClientDigest(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy-%t", legacy), func(t *testing.T) {
			f := newHuggingFaceFixture(t)
			inventory, e := f.s.ListProviderInventory(f.ctx, connect.NewRequest(&pb.ListProviderInventoryRequest{}))
			if e != nil {
				t.Fatal(e)
			}
			var router *pb.Resource
			for _, entry := range inventory.Msg.Entries {
				if entry.PresetId == pb.ProviderPresetId_PROVIDER_PRESET_ID_OPENROUTER {
					router = entry.Provider
				}
			}
			var start *pb.StartAccountOAuthResponse
			var state string
			foreign := router
			if legacy {
				foreign = f.provider
				f.provider = router
				start = f.start(t)
			} else {
				start, state = f.startHF(t)
			}
			_, e = f.s.Store.Mutate(f.ctx, domain.NewID(), "fixture.invalid-oauth-profile", nil, func(tx *store.Tx) (any, error) {
				a, e := tx.AccountOAuth(domain.ID(start.Attempt.Id))
				if e != nil {
					return nil, e
				}
				rev := a.Revision
				a.ProviderID = domain.ID(foreign.Id)
				a.ProviderRevision = foreign.Revision
				a.Revision++
				return nil, tx.PutAccountOAuth(a, rev)
			})
			if e != nil {
				t.Fatal(e)
			}
			start.Attempt.ProviderId = foreign.Id
			start.Attempt.Revision++
			var calls atomic.Int32
			f.s.oauthExchange = oauthExchangeFunc(func(context.Context, []byte, []byte) ([]byte, error) {
				calls.Add(1)
				return []byte("unexpected-key"), nil
			})
			f.s.oauthTokenClient = oauthTokenFixture{exchange: func(context.Context, oauthProfile, []byte, []byte, string) (oauthTokenResult, error) {
				calls.Add(1)
				return tokenResult("unexpected-access", "unexpected-refresh", time.Now().Add(time.Hour)), nil
			}}
			_, e = f.completeHF(start.Attempt, domain.NewID(), "original-code", state)
			wantAccountCode(t, e, domain.Unsupported)
			if calls.Load() != 0 {
				t.Fatal("another adapter gained exchange authority")
			}
		})
	}
	f := newHuggingFaceFixture(t)
	f.vault.putError = io.ErrUnexpectedEOF
	f.s.oauthTokenClient = oauthTokenFixture{exchange: func(context.Context, oauthProfile, []byte, []byte, string) (oauthTokenResult, error) {
		return tokenResult("original-access", "original-refresh", time.Now().Add(time.Hour)), nil
	}}
	start, state := f.startHF(t)
	id := domain.NewID()
	r, e := f.completeHF(start.Attempt, id, "original-code", state)
	if e != nil || r.Msg.Attempt.State != pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_RECOVERY_REQUIRED {
		t.Fatal("missing original staged result")
	}
	registration := f.s.oauthRegistrations[domain.PresetHuggingFace]
	registration.ClientID = "different-public-client"
	f.s.oauthRegistrations[domain.PresetHuggingFace] = registration
	f.vault.putError = nil
	_, e = f.completeHF(start.Attempt, id, "", "")
	wantAccountCode(t, e, domain.RecoveryRequired)
	original, e := f.s.oauthRead(f.ctx, domain.ID(start.Attempt.Id))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.accountRecord(f.ctx, original.AccountID); domain.SafeError(e).Code != domain.NotFound {
		t.Fatal("changed app published an account")
	}
}
