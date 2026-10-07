// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestAutomaticValidationAndDiscoveryRemainIndependent(t *testing.T) {
	for _, test := range []struct {
		name      string
		auth      domain.Authentication
		discovery bool
		status    int
		state     domain.ObservationState
		calls     int32
	}{
		{"keyless-both", domain.KeylessAuth, true, 200, domain.Observed, 2},
		{"keyless-validation-only", domain.KeylessAuth, false, 200, domain.Observed, 1},
		{"custom-public", domain.BearerAuth, true, 200, domain.ObservationUnsupported, 2},
		{"rejected-credentials", domain.KeylessAuth, true, 401, domain.ObservationFailed, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "GET" || r.URL.Path != "/v1/models" {
					t.Errorf("inference/unexpected request: %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(test.status)
				fmt.Fprint(w, `{"data":[{"id":"fixture"}]}`)
			}))
			defer upstream.Close()
			s, secrets := newDoctorFixture(t)
			id := domain.NewID()
			body := domain.Account{Alias: "fixture", ProviderID: domain.NewID(), Type: domain.APIAccount, Enabled: true, Health: domain.AccountUnverified, Connection: &domain.AccountConnection{ID: domain.NewID(), Authentication: test.auth, ConnectedAt: time.Now().UTC()}}
			doctorPut(t, s, domain.ProviderKind, body.ProviderID, 0, domain.Provider{Name: "fixture", Endpoint: upstream.URL + "/v1", Protocol: domain.OpenAIChat, Authentication: test.auth, Discovery: test.discovery})
			doctorPut(t, s, domain.AccountKind, id, 0, body)
			secrets.values[credentials.Ref{Owner: id, ID: body.Connection.ID, Purpose: credentials.AccountAPI}] = []byte("fixture-key")
			p, err := s.Store.Get(context.Background(), domain.ProviderKind, body.ProviderID)
			if err != nil {
				t.Fatal(err)
			}
			provider, err := store.Decode[domain.Provider](p)
			if err != nil {
				t.Fatal(err)
			}
			provider.Discovery = test.discovery
			doctorPut(t, s, domain.ProviderKind, p.ID, p.Revision, provider)
			record, err := s.Store.Get(context.Background(), domain.AccountKind, id)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), automaticInspectionKey{}, true)
			s.maintainAPIAccount(ctx, record)
			current, err := s.Store.Get(context.Background(), domain.AccountKind, record.ID)
			if err != nil {
				t.Fatal(err)
			}
			account, err := store.Decode[domain.Account](current)
			if err != nil {
				t.Fatal(err)
			}
			if account.Validation == nil || account.Validation.State != test.state || calls.Load() != test.calls || (account.Catalog != nil) != test.discovery {
				t.Fatalf("independent observations: %+v calls=%d", account, calls.Load())
			}
			if account.Catalog != nil && account.Catalog.RequestID == account.Validation.RequestID {
				t.Fatal("operations shared request identity")
			}
			s.maintainAPIAccount(ctx, current)
			if calls.Load() != test.calls {
				t.Fatal("fresh persisted observations repeated")
			}
		})
	}
}

func TestAutomaticInspectionDueCompletionAndRetryAfter(t *testing.T) {
	id := domain.NewID()
	now := time.Now().UTC()
	delay := uint32(3600)
	for _, test := range []struct {
		completed time.Time
		retry     *uint32
		at        time.Time
		due       bool
	}{{now, nil, now.Add(14 * time.Minute), false}, {now, nil, now.Add(15 * time.Minute), true}, {now, &delay, now.Add(20 * time.Minute), false}, {now, &delay, now.Add(time.Hour), true}, {time.Time{}, nil, now, true}} {
		if got := accountObservationDue(id, id, test.completed, test.retry, test.at); got != test.due {
			t.Fatalf("completion-based due time got %v want %v", got, test.due)
		}
	}
	if !accountObservationDue(id, domain.NewID(), now, &delay, now) {
		t.Fatal("new connection did not become immediately due")
	}
}

func TestAutomaticValidationAdmissionDoesNotChangeExplicitSemantics(t *testing.T) {
	s, _ := newDoctorFixture(t)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, `{"data":[]}`) }))
	defer upstream.Close()
	id := domain.NewID()
	body := domain.Account{Alias: "fixture", ProviderID: domain.NewID(), Type: domain.APIAccount, Enabled: true, Health: domain.AccountUnverified, Connection: &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.KeylessAuth, ConnectedAt: time.Now().UTC()}}
	doctorPut(t, s, domain.ProviderKind, body.ProviderID, 0, domain.Provider{Name: "fixture", Endpoint: upstream.URL + "/v1", Protocol: domain.OpenAIChat, Authentication: domain.KeylessAuth})
	doctorPut(t, s, domain.AccountKind, id, 0, body)
	record, err := s.Store.Get(context.Background(), domain.AccountKind, id)
	if err != nil {
		t.Fatal(err)
	}
	body.Enabled = false
	doctorPut(t, s, domain.AccountKind, record.ID, record.Revision, body)
	record, err = s.Store.Get(context.Background(), domain.AccountKind, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	meta := &pb.Mutation{Id: string(record.ID), ExpectedRevision: record.Revision, RequestId: string(domain.NewID())}
	ctx := context.WithValue(context.Background(), automaticInspectionKey{}, true)
	if _, err := s.validateAccount(ctx, meta, meta.RequestId); err == nil || calls.Load() != 0 {
		t.Fatal("disabled automatic account inspected")
	}
	meta.RequestId = string(domain.NewID())
	if _, err := s.validateAccount(context.Background(), meta, meta.RequestId); err != nil || calls.Load() != 1 {
		t.Fatalf("explicit disabled-account behavior changed: %v", err)
	}
}

func TestAutomaticValidationFencesDisablementAndNewConnection(t *testing.T) {
	for _, change := range []string{"provider-disabled", "account-disabled", "connection", "discovery-disabled", "shutdown"} {
		t.Run(change, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				select {
				case <-release:
					fmt.Fprint(w, `{"data":[]}`)
				case <-r.Context().Done():
				}
			}))
			defer upstream.Close()
			s, _ := newDoctorFixture(t)
			id, pid := domain.NewID(), domain.NewID()
			a := domain.Account{Alias: "fixture", ProviderID: pid, Type: domain.APIAccount, Enabled: true, Health: domain.AccountUnverified, Connection: &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.KeylessAuth, ConnectedAt: time.Now().UTC()}}
			provider := domain.Provider{Name: "fixture", Endpoint: upstream.URL + "/v1", Protocol: domain.OpenAIChat, Authentication: domain.KeylessAuth, Discovery: true}
			doctorPut(t, s, domain.ProviderKind, pid, 0, provider)
			doctorPut(t, s, domain.AccountKind, id, 0, a)
			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), automaticInspectionKey{}, true))
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := s.validateAccount(ctx, &pb.Mutation{Id: string(id), ExpectedRevision: 1, RequestId: string(domain.NewID())}, string(domain.NewID()))
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("automatic validation did not start")
			}
			switch change {
			case "connection":
				a.Connection.ID = domain.NewID()
				doctorPut(t, s, domain.AccountKind, id, 1, a)
			case "shutdown":
				cancel()
			default:
				kind, target, body := pb.EntityKind_ENTITY_KIND_PROVIDER, pid, any(provider)
				if change == "provider-disabled" {
					off := false
					provider.Enabled = &off
					body = provider
				}
				if change == "discovery-disabled" {
					provider.Discovery = false
					body = provider
				}
				if change == "account-disabled" {
					a.Enabled = false
					kind, target, body = pb.EntityKind_ENTITY_KIND_ACCOUNT, id, a
				}
				raw, _ := json.Marshal(body)
				_, err := s.SaveConfiguration(context.Background(), connect.NewRequest(&pb.SaveConfigurationRequest{Mutation: &pb.Mutation{Id: string(target), ExpectedRevision: 1, RequestId: string(domain.NewID())}, Kind: kind, SchemaVersion: 1, DocumentJson: raw}))
				if err != nil {
					close(release)
					t.Fatal(err)
				}
			}
			close(release)
			select {
			case err := <-done:
				if (err == nil) != (change == "discovery-disabled") {
					t.Fatalf("automatic stale/disabled validation publication: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("automatic cancellation not joined")
			}
			record, err := s.Store.Get(context.Background(), domain.AccountKind, id)
			if err != nil {
				t.Fatal(err)
			}
			current, err := store.Decode[domain.Account](record)
			if err != nil {
				t.Fatal(err)
			}
			if (current.Validation != nil) != (change == "discovery-disabled") {
				t.Fatal("automatic authority fence lost")
			}
		})
	}
}
