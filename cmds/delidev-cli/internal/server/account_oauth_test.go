// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type oauthExchangeFunc func(context.Context, []byte, []byte) ([]byte, error)

func (f oauthExchangeFunc) Exchange(ctx context.Context, c, v []byte) ([]byte, error) {
	return f(ctx, c, v)
}

type oauthFixture struct {
	s        *Service
	ctx      context.Context
	root     string
	provider *pb.Resource
	vault    *accountTestSecrets
	logs     bytes.Buffer
}

func newOAuthFixture(t *testing.T) *oauthFixture {
	t.Helper()
	f := &oauthFixture{root: filepath.Join(t.TempDir(), "state"), ctx: domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), vault: &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}}
	db, err := store.Open(f.ctx, f.root)
	if err != nil {
		t.Fatal(err)
	}
	f.s = &Service{Store: db, Identity: security.Identity{ServerID: domain.NewID(), Token: "temporary-oauth-test-owner"}, logger: slog.New(slog.NewJSONHandler(&f.logs, nil)), accountSecrets: f.vault}
	r, err := f.s.ListProviderInventory(f.ctx, connect.NewRequest(&pb.ListProviderInventoryRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range r.Msg.Entries {
		if e.PresetId == pb.ProviderPresetId_PROVIDER_PRESET_ID_OPENROUTER {
			f.provider = e.Provider
		}
	}
	if f.provider == nil {
		t.Fatal("managed OpenRouter was not seeded")
	}
	t.Cleanup(func() { f.s.closeAccountSecrets(); f.s.Store.Close() })
	return f
}
func (f *oauthFixture) start(t *testing.T) *pb.StartAccountOAuthResponse {
	t.Helper()
	r, err := f.s.StartAccountOAuth(f.ctx, connect.NewRequest(&pb.StartAccountOAuthRequest{Provider: acctMutation(f.provider, domain.NewID())}))
	if err != nil {
		t.Fatal(err)
	}
	if r.Msg.AuthorizationUrl == "" || r.Msg.Attempt.State != pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_AWAITING_AUTHORIZATION {
		t.Fatal("no live headless authorization")
	}
	return r.Msg
}
func oauthMutation(a *pb.AccountOAuthAttempt, request domain.ID) *pb.Mutation {
	return &pb.Mutation{Id: a.Id, ExpectedRevision: a.Revision, RequestId: string(request)}
}
func (f *oauthFixture) complete(a *pb.AccountOAuthAttempt, id domain.ID, code string) (*connect.Response[pb.CompleteAccountOAuthResponse], error) {
	return f.s.CompleteAccountOAuth(f.ctx, connect.NewRequest(&pb.CompleteAccountOAuthRequest{Mutation: oauthMutation(a, id), AuthorizationCode: []byte(code)}))
}
func (f *oauthFixture) restart(t *testing.T) {
	t.Helper()
	identity := f.s.Identity
	exchange := f.s.oauthExchange
	f.s.closeAccountSecrets()
	if err := f.s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(f.ctx, f.root)
	if err != nil {
		t.Fatal(err)
	}
	f.s = &Service{Store: db, Identity: identity, logger: slog.New(slog.NewJSONHandler(&f.logs, nil)), accountSecrets: f.vault, oauthExchange: exchange}
	if err := f.s.initializeOAuth(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAccountOAuthPKCEOnceOnlyReplayAndPrivateMetadata(t *testing.T) {
	f := newOAuthFixture(t)
	a := f.start(t)
	id := domain.NewID()
	const code = "oauth-code-sentinel-9f7a"
	const key = "oauth-key-sentinel-0b2e"
	u, _ := url.Parse(a.AuthorizationUrl)
	var calls atomic.Int32
	f.s.oauthExchange = oauthExchangeFunc(func(ctx context.Context, c, v []byte) ([]byte, error) {
		calls.Add(1)
		h := sha256.Sum256(v)
		if string(c) != code || base64.RawURLEncoding.EncodeToString(h[:]) != u.Query().Get("code_challenge") || u.Query().Get("code_challenge_method") != "S256" || u.Query().Has("callback_url") {
			t.Error("wrong headless PKCE authority")
		}
		return []byte(key), nil
	})
	r, err := f.complete(a.Attempt, id, code)
	if err != nil {
		t.Fatal(err)
	}
	if r.Msg.Attempt.State != pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_CONNECTED || r.Msg.Account == nil {
		t.Fatalf("not connected: %v", r.Msg.Attempt)
	}
	account := accountBody(t, r.Msg.Account)
	if account.Alias != "OpenRouter" || account.Type != domain.APIAccount || !account.Enabled || account.ExcludeAutomatic || !account.RecoveryNotifications || account.Health != domain.AccountUnverified || account.Validation != nil || account.Catalog != nil || r.Msg.Account.Revision != 2 {
		t.Fatal("default connection semantics changed")
	}
	for _, retryCode := range []string{code, ""} {
		rr, e := f.complete(a.Attempt, id, retryCode)
		if e != nil || !rr.Msg.Replayed || rr.Msg.Account.Id != r.Msg.Account.Id {
			t.Fatalf("original replay: %v", e)
		}
	}
	_, err = f.complete(a.Attempt, id, code+"changed")
	wantAccountCode(t, err, domain.Conflict)
	f.restart(t)
	rr, err := f.complete(a.Attempt, id, "")
	if err != nil || !rr.Msg.Replayed || rr.Msg.Attempt.State != pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_CONNECTED {
		t.Fatalf("restart replay: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatal("exchange repeated")
	}
	puts, _, active := f.vault.counts()
	if puts != 1 || active != 1 {
		t.Fatal("vault repeated")
	}
	status, err := f.s.GetAccountOAuthStatus(f.ctx, connect.NewRequest(&pb.GetAccountOAuthStatusRequest{AttemptId: a.Attempt.Id}))
	if err != nil || status.Msg.Account == nil {
		t.Fatal(err)
	}
	for _, sentinel := range []string{code, key, a.AuthorizationUrl, u.Query().Get("code_challenge")} {
		if strings.Contains(f.logs.String(), sentinel) {
			t.Fatal("OAuth content reached logs")
		}
		err := filepath.WalkDir(f.root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err == nil && bytes.Contains(raw, []byte(sentinel)) {
				t.Error("OAuth content reached persisted state")
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Cancel after connection reports the committed result without deleting it.
	canceled, err := f.s.CancelAccountOAuth(f.ctx, connect.NewRequest(&pb.CancelAccountOAuthRequest{Mutation: oauthMutation(rr.Msg.Attempt, domain.NewID())}))
	if err != nil || canceled.Msg.Attempt.State != pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_CONNECTED {
		t.Fatal(err)
	}
	_, deletes, _ := f.vault.counts()
	if deletes != 0 {
		t.Fatal("cancel disconnected committed account")
	}
}

func TestAccountOAuthLostVaultAcknowledgmentRecoversOnlyOriginalReference(t *testing.T) {
	f := newOAuthFixture(t)
	a := f.start(t)
	id := domain.NewID()
	var calls atomic.Int32
	f.s.oauthExchange = oauthExchangeFunc(func(context.Context, []byte, []byte) ([]byte, error) {
		calls.Add(1)
		return []byte("protected-original-recovery-key"), nil
	})
	f.vault.putError = domain.Fail(domain.RecoveryRequired, "Lost protected save acknowledgment.", "")
	r, err := f.complete(a.Attempt, id, "original-code")
	if err != nil || r.Msg.Attempt.State != pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_RECOVERY_REQUIRED || r.Msg.Account != nil {
		t.Fatalf("lost acknowledgment: %v", err)
	}
	f.vault.putError = nil
	f.restart(t)
	_, err = f.complete(a.Attempt, domain.NewID(), "")
	wantAccountCode(t, err, domain.Conflict)
	r, err = f.complete(a.Attempt, id, "")
	if err != nil || r.Msg.Attempt.State != pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_CONNECTED {
		t.Fatalf("protected local recovery: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatal("recovery repeated exchange")
	}
	puts, _, _ := f.vault.counts()
	if puts != 1 {
		t.Fatal("recovery replaced protected reference")
	}
}

func TestAccountOAuthUncertainExchangeNeverResends(t *testing.T) {
	for _, badKey := range []bool{false, true} {
		t.Run(map[bool]string{false: "lost-response", true: "malformed-key"}[badKey], func(t *testing.T) {
			f := newOAuthFixture(t)
			a := f.start(t)
			id := domain.NewID()
			var calls atomic.Int32
			f.s.oauthExchange = oauthExchangeFunc(func(context.Context, []byte, []byte) ([]byte, error) {
				calls.Add(1)
				if badKey {
					return []byte("bad\nkey"), nil
				}
				return nil, io.ErrUnexpectedEOF
			})
			r, err := f.complete(a.Attempt, id, "code")
			if err != nil || r.Msg.Attempt.State != pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_RECOVERY_REQUIRED {
				t.Fatal(err)
			}
			f.restart(t)
			for _, code := range []string{"code", ""} {
				r, err = f.complete(a.Attempt, id, code)
				if err != nil || r.Msg.Attempt.State != pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_RECOVERY_REQUIRED {
					t.Fatal(err)
				}
			}
			if calls.Load() != 1 {
				t.Fatal("uncertain exchange repeated")
			}
			puts, _, active := f.vault.counts()
			if puts != 0 || active != 0 {
				t.Fatal("uncertain key staged")
			}
		})
	}
}

func TestAccountOAuthCancelDuringExchangeAndCleanupRetry(t *testing.T) {
	f := newOAuthFixture(t)
	a := f.start(t)
	id := domain.NewID()
	entered := make(chan struct{})
	release := make(chan struct{})
	f.s.oauthExchange = oauthExchangeFunc(func(ctx context.Context, _, _ []byte) ([]byte, error) {
		close(entered)
		<-release
		return []byte("returned-after-cancellation"), nil
	})
	done := make(chan error, 1)
	go func() {
		r, err := f.complete(a.Attempt, id, "code")
		if err == nil && r.Msg.Attempt.State != pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_CANCELED {
			err = io.ErrUnexpectedEOF
		}
		done <- err
	}()
	<-entered
	status, err := f.s.GetAccountOAuthStatus(f.ctx, connect.NewRequest(&pb.GetAccountOAuthStatusRequest{AttemptId: a.Attempt.Id}))
	if err != nil {
		t.Fatal(err)
	}
	cancelID := domain.NewID()
	r, err := f.s.CancelAccountOAuth(f.ctx, connect.NewRequest(&pb.CancelAccountOAuthRequest{Mutation: oauthMutation(status.Msg.Attempt, cancelID)}))
	if err != nil || r.Msg.Attempt.State != pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_CANCELED {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	puts, _, active := f.vault.counts()
	if puts != 0 || active != 0 {
		t.Fatal("cancel-first published a late credential")
	}

	// A separate orphan stage remains fenced until the original cancellation
	// confirms native cleanup, even when the first cleanup response fails.
	b := f.start(t)
	original := domain.NewID()
	f.s.oauthExchange = oauthExchangeFunc(func(context.Context, []byte, []byte) ([]byte, error) { return []byte("orphaned-sealed-key"), nil })
	f.vault.putError = io.ErrUnexpectedEOF
	pending, err := f.complete(b.Attempt, original, "other-code")
	if err != nil {
		t.Fatal(err)
	}
	f.vault.deleteError = io.ErrUnexpectedEOF
	mutation := oauthMutation(pending.Msg.Attempt, domain.NewID())
	_, err = f.s.CancelAccountOAuth(f.ctx, connect.NewRequest(&pb.CancelAccountOAuthRequest{Mutation: mutation}))
	wantAccountCode(t, err, domain.RecoveryRequired)
	f.vault.deleteError = nil
	f.vault.putError = nil
	f.restart(t)
	r, err = f.s.CancelAccountOAuth(f.ctx, connect.NewRequest(&pb.CancelAccountOAuthRequest{Mutation: mutation}))
	if err != nil || !r.Msg.Replayed {
		t.Fatalf("cleanup retry: %v", err)
	}
	_, _, active = f.vault.counts()
	if active != 0 {
		t.Fatal("original cleanup remained")
	}
	_, err = f.complete(b.Attempt, original, "")
	if err != nil {
		t.Fatal(err)
	}
	puts, _, _ = f.vault.counts()
	if puts != 1 {
		t.Fatal("canceled original was restaged")
	}
}

func TestAccountOAuthRestartInterruptsAwaitingAndWorkerCannotStart(t *testing.T) {
	f := newOAuthFixture(t)
	a := f.start(t)
	f.restart(t)
	r, err := f.s.StartAccountOAuth(f.ctx, connect.NewRequest(&pb.StartAccountOAuthRequest{Provider: &pb.Mutation{Id: f.provider.Id, ExpectedRevision: f.provider.Revision, RequestId: a.RequestId}}))
	if err != nil || r.Msg.AuthorizationUrl != "" || r.Msg.Attempt.State != pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_INTERRUPTED {
		t.Fatalf("old start authority recovered: %v", err)
	}
	worker := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: domain.NewID(), MachineID: domain.NewID()})
	_, err = f.s.StartAccountOAuth(worker, connect.NewRequest(&pb.StartAccountOAuthRequest{Provider: acctMutation(f.provider, domain.NewID())}))
	wantAccountCode(t, err, domain.PermissionDenied)
	_, err = f.s.GetAccountOAuthStatus(worker, connect.NewRequest(&pb.GetAccountOAuthStatusRequest{AttemptId: a.Attempt.Id}))
	wantAccountCode(t, err, domain.PermissionDenied)
}

func TestAccountOAuthUnownedDurableDispatchCannotRemainLiveOrResend(t *testing.T) {
	f := newOAuthFixture(t)
	start := f.start(t)
	private, err := f.s.oauthRead(f.ctx, domain.ID(start.Attempt.Id))
	if err != nil {
		t.Fatal(err)
	}
	original := domain.NewID()
	actor := domain.Principal{Type: domain.OwnerDevice}
	commitment := f.s.oauthCommitment("code", original, []byte("unknown-dispatch-code"))
	_, err = f.s.Store.Mutate(f.ctx, original, "oauth.complete", oauthCompleteInput{private.ID, private.Revision, actor, commitment}, func(tx *store.Tx) (any, error) {
		current := private
		current.Revision++
		current.State = domain.OAuthExchanging
		current.CompletionRequestID = original
		current.CompletionRevision = private.Revision
		current.CodeCommitment = commitment
		current.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
		return oauthReceipt{private.ID}, tx.PutAccountOAuth(current, private.Revision)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.s.oauthExchange = oauthExchangeFunc(func(context.Context, []byte, []byte) ([]byte, error) {
		t.Fatal("unowned dispatch was retried")
		return nil, nil
	})
	r, err := f.complete(start.Attempt, original, "")
	if err != nil || r.Msg.Attempt.State != pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_RECOVERY_REQUIRED || !r.Msg.Replayed {
		t.Fatalf("unowned dispatch outcome: %v", err)
	}
	if f.s.oauthLive[private.ID] != nil {
		t.Fatal("unowned verifier retained")
	}
	puts, _, _ := f.vault.counts()
	if puts != 0 {
		t.Fatal("unowned dispatch staged a key")
	}
}
