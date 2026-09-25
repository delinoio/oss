package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func connectValidationAccount(t *testing.T, f *accountFixture, endpoint string, auth domain.Authentication) *pb.Resource {
	t.Helper()
	p := f.save(pb.EntityKind_ENTITY_KIND_PROVIDER, domain.Provider{Name: "Validation fixture", Endpoint: endpoint + "/v1", Protocol: domain.OpenAIChat, Authentication: auth})
	a := f.save(pb.EntityKind_ENTITY_KIND_ACCOUNT, domain.Account{Alias: "Validation", ProviderID: domain.ID(p.Id), Type: domain.APIAccount, Enabled: true, Health: domain.AccountDisconnected})
	req := &pb.ConnectAccountRequest{Mutation: &pb.Mutation{Id: a.Id, ExpectedRevision: a.Revision, RequestId: string(domain.NewID())}, Keyless: auth == domain.KeylessAuth}
	if !req.Keyless {
		req.ApiKey = []byte("validation-fixture-key")
	}
	response, err := f.accounts.ConnectAccount(context.Background(), ownerRequest(f.identity, req))
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg.Account
}

func TestAccountValidationReplayAndDisconnect(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"data":[{"id":"model-fixture"}]}`)
	}))
	defer upstream.Close()
	f := newAccountFixture(t)
	a := connectValidationAccount(t, f, upstream.URL, domain.KeylessAuth)
	input := &pb.ValidateAccountRequest{Mutation: &pb.Mutation{Id: a.Id, ExpectedRevision: a.Revision, RequestId: string(domain.NewID())}}
	validated, err := f.accounts.ValidateAccount(context.Background(), ownerRequest(f.identity, input))
	if err != nil {
		t.Fatal(err)
	}
	body := accountBody(t, validated.Msg.Account)
	if body.Health != domain.AccountReady || body.Validation == nil || body.Validation.Authentication != domain.KeylessEndpoint || body.Validation.ModelCount != 1 || body.ConfirmedExhausted || len(body.Quota) != 0 {
		t.Fatalf("validation state: %+v", body)
	}
	f.shutdown()
	f.start()
	replayed, err := f.accounts.ValidateAccount(context.Background(), ownerRequest(f.identity, input))
	if err != nil || !replayed.Msg.Replayed || calls.Load() != 1 {
		t.Fatalf("validation replay repeated network: %v", err)
	}
	disconnected, err := f.accounts.DisconnectAccount(context.Background(), ownerRequest(f.identity, &pb.DisconnectAccountRequest{Mutation: &pb.Mutation{Id: a.Id, ExpectedRevision: validated.Msg.Account.Revision, RequestId: string(domain.NewID())}}))
	if err != nil || accountBody(t, disconnected.Msg.Account).Validation != nil {
		t.Fatalf("disconnect retained readiness: %v", err)
	}
	replayed, err = f.accounts.ValidateAccount(context.Background(), ownerRequest(f.identity, input))
	if err != nil || !replayed.Msg.Replayed || accountBody(t, replayed.Msg.Account).Health != domain.AccountDisconnected || calls.Load() != 1 {
		t.Fatalf("old validation resurrected readiness: %v", err)
	}
}

func TestAccountValidationDistinguishesPublicCatalogAndFailures(t *testing.T) {
	for _, tc := range []struct {
		status int
		health domain.AccountHealth
		code   domain.Code
	}{{200, domain.AccountUnverified, domain.Unsupported}, {401, domain.AccountFailed, domain.Unauthenticated}, {403, domain.AccountFailed, domain.PermissionDenied}, {429, domain.AccountFailed, domain.ResourceExhausted}, {500, domain.AccountFailed, domain.Unavailable}} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != "Bearer validation-fixture-key" {
					t.Error("selected account credential missing")
				}
				w.Header().Set("Retry-After", "12")
				w.WriteHeader(tc.status)
				if tc.status == 200 {
					fmt.Fprint(w, `{"data":[]}`)
				} else {
					fmt.Fprint(w, `{"error":"validation-fixture-key"}`)
				}
			}))
			defer upstream.Close()
			f := newAccountFixture(t)
			a := connectValidationAccount(t, f, upstream.URL, domain.BearerAuth)
			input := &pb.ValidateAccountRequest{Mutation: &pb.Mutation{Id: a.Id, ExpectedRevision: a.Revision, RequestId: string(domain.NewID())}}
			response, err := f.accounts.ValidateAccount(context.Background(), ownerRequest(f.identity, input))
			if err != nil {
				t.Fatal(err)
			}
			body := accountBody(t, response.Msg.Account)
			if body.Health != tc.health || body.Validation == nil || body.Validation.Problem == nil || body.Validation.Problem.Code != tc.code || body.ConfirmedExhausted {
				t.Fatalf("failure state: %+v", body)
			}
			wantState := domain.ObservationFailed
			if tc.code == domain.Unsupported {
				wantState = domain.ObservationUnsupported
			}
			if body.Validation.State != wantState {
				t.Fatalf("validation state = %s, want %s", body.Validation.State, wantState)
			}
			replay, err := f.accounts.ValidateAccount(context.Background(), ownerRequest(f.identity, input))
			if err != nil || !replay.Msg.Replayed || calls.Load() != 1 {
				t.Fatalf("failed accepted check retried network: %v", err)
			}
		})
	}
}

func TestDisconnectCancelsBlockedValidationWithoutBlockingOtherAccounts(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(canceled) }))
	defer upstream.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"data":[]}`) }))
	defer fast.Close()
	f := newAccountFixture(t)
	a := connectValidationAccount(t, f, upstream.URL, domain.KeylessAuth)
	b := connectValidationAccount(t, f, fast.URL, domain.KeylessAuth)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := f.accounts.ValidateAccount(ctx, ownerRequest(f.identity, &pb.ValidateAccountRequest{Mutation: &pb.Mutation{Id: a.Id, ExpectedRevision: a.Revision, RequestId: string(domain.NewID())}}))
		done <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("validation did not reach upstream")
	}
	_, err := f.accounts.ValidateAccount(ctx, ownerRequest(f.identity, &pb.ValidateAccountRequest{Mutation: &pb.Mutation{Id: b.Id, ExpectedRevision: b.Revision, RequestId: string(domain.NewID())}}))
	if err != nil {
		t.Fatal("unrelated validation blocked", err)
	}
	response, err := f.accounts.DisconnectAccount(ctx, ownerRequest(f.identity, &pb.DisconnectAccountRequest{Mutation: &pb.Mutation{Id: a.Id, ExpectedRevision: a.Revision, RequestId: string(domain.NewID())}}))
	if err != nil {
		t.Fatal("disconnect blocked on provider", err)
	}
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("disconnect did not cancel upstream")
	}
	if err := <-done; err == nil || rpc.ClientError(err).Code != domain.Canceled {
		t.Fatalf("validation after disconnect: %v", err)
	}
	if body := accountBody(t, response.Msg.Account); body.Health != domain.AccountDisconnected || body.Validation != nil {
		t.Fatal("stale validation published")
	}
}
