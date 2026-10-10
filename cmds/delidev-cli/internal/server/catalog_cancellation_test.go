package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestCatalogCheckCancellationPreservesOriginalEvidence(t *testing.T) {
	for _, change := range []string{"provider-disabled", "discovery-disabled", "enabled-success"} {
		t.Run(change, func(t *testing.T) {
			started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			var releaseOnce sync.Once
			finishUpstream := func() { releaseOnce.Do(func() { close(release) }) }
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) > 1 {
					fmt.Fprint(w, `{"data":[]}`)
					return
				}
				close(started)
				select {
				case <-r.Context().Done():
					close(canceled)
					<-release
				case <-release:
				}
				// Even a late upstream success cannot publish through the canceled owner.
				fmt.Fprint(w, `{"data":[{"id":"late-model"}]}`)
			}))
			defer upstream.Close()
			defer finishUpstream()
			s, _ := newDoctorFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			pid, aid := domain.NewID(), domain.NewID()
			provider := domain.Provider{Name: "fixture", Endpoint: upstream.URL + "/v1", Protocol: domain.OpenAIChat, Authentication: domain.KeylessAuth, Discovery: true}
			now := time.Now().UTC().Truncate(time.Millisecond)
			account := domain.Account{Alias: "fixture", ProviderID: pid, Type: domain.APIAccount, Enabled: true, Health: domain.AccountUnverified, Connection: &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.KeylessAuth, ConnectedAt: now}}
			account.Catalog = &domain.CatalogObservation{RequestID: domain.NewID(), ConnectionID: account.Connection.ID, ObservedAt: now, State: domain.Observed, Received: 7}
			prior := *account.Catalog
			doctorPut(t, s, domain.ProviderKind, pid, 0, provider)
			doctorPut(t, s, domain.AccountKind, aid, 0, account)
			meta := &pb.Mutation{Id: string(aid), ExpectedRevision: 1, RequestId: string(domain.NewID())}
			var publications atomic.Int32
			done := make(chan error, 1)
			go func() {
				_, err := s.inspectAccount(ctx, meta, catalogInspection, string(domain.NewID()), func(tx *store.Tx, current domain.Account, observation accountInspection) (any, error) {
					publications.Add(1)
					current.Catalog = &domain.CatalogObservation{RequestID: domain.ID(meta.RequestId), ConnectionID: current.Connection.ID, ObservedAt: observation.ObservedAt, State: domain.Observed, Received: uint32(len(observation.Models))}
					return tx.Put(domain.AccountKind, aid, 1, "", "", current)
				})
				done <- err
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("catalog request did not start")
			}
			if change != "enabled-success" {
				if change == "provider-disabled" {
					provider.SetEnabled(false)
				} else {
					provider.Discovery = false
				}
				raw, _ := json.Marshal(provider)
				_, err := s.SaveConfiguration(ctx, connect.NewRequest(&pb.SaveConfigurationRequest{Mutation: &pb.Mutation{Id: string(pid), ExpectedRevision: 1, RequestId: string(domain.NewID())}, Kind: pb.EntityKind_ENTITY_KIND_PROVIDER, SchemaVersion: 1, DocumentJson: raw}))
				if err != nil {
					t.Fatal(err)
				}
				select {
				case <-canceled:
				case <-ctx.Done():
					t.Fatal("provider change did not cancel original upstream request")
				}
				select {
				case err := <-done:
					if err == nil || domain.SafeError(err).Code != domain.Canceled {
						t.Fatalf("cancellation lost: %v", err)
					}
				case <-ctx.Done():
					t.Fatal("canceled catalog owner did not finish")
				}
				record, err := s.Store.Get(ctx, domain.AccountKind, aid)
				if err != nil {
					t.Fatal(err)
				}
				current, err := store.Decode[domain.Account](record)
				if err != nil || record.Revision != 1 || !reflect.DeepEqual(current.Catalog, &prior) || publications.Load() != 0 {
					t.Fatal("canceled request replaced prior evidence")
				}
				// Explicit validation on another account keeps independent ownership,
				// even while this provider is Off or discovery-disabled.
				other := account
				other.Catalog = nil
				other.Connection = &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.KeylessAuth, ConnectedAt: now}
				otherID := domain.NewID()
				doctorPut(t, s, domain.AccountKind, otherID, 0, other)
				_, err = s.validateAccount(ctx, &pb.Mutation{Id: string(otherID), ExpectedRevision: 1, RequestId: string(domain.NewID())}, string(domain.NewID()))
				if err != nil {
					t.Fatalf("explicit validation was coupled to catalog cancellation: %v", err)
				}
				_, replayed, err := s.Store.Replay(ctx, domain.ID(meta.RequestId), string(catalogInspection), disconnectAccountInput{ID: aid, Revision: 1})
				if err != nil || replayed {
					t.Fatal("canceled request acquired a publication receipt")
				}
			} else {
				finishUpstream()
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("enabled discovery did not publish")
				}
				record, err := s.Store.Get(ctx, domain.AccountKind, aid)
				if err != nil {
					t.Fatal(err)
				}
				current, err := store.Decode[domain.Account](record)
				if err != nil || record.Revision != 2 || current.Catalog.RequestID != domain.ID(meta.RequestId) || current.Catalog.Received != 1 || publications.Load() != 1 {
					t.Fatal("ordinary catalog publication changed")
				}

			}
		})
	}
}

func TestProviderDisableKeepsIndependentInspectionOwners(t *testing.T) {
	s, _ := newDoctorFixture(t)
	pid, otherProvider := domain.NewID(), domain.NewID()
	provider := domain.Provider{Name: "fixture", Endpoint: "http://127.0.0.1:8000/v1", Protocol: domain.OpenAIChat, Authentication: domain.KeylessAuth, Discovery: true}
	doctorPut(t, s, domain.ProviderKind, pid, 0, provider)
	// Register independent original owners under the same gate used by real reads.
	checks := []struct {
		operation               inspectionOperation
		provider                domain.ID
		automatic, wantCanceled bool
	}{
		{catalogInspection, pid, false, true},
		{validationInspection, pid, false, false},
		{validationInspection, pid, true, true},
		{catalogInspection, otherProvider, false, false},
	}
	contexts := make([]context.Context, len(checks))
	unlock, err := s.lockAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i, check := range checks {
		ctx := context.Background()
		if check.automatic {
			ctx = context.WithValue(ctx, automaticInspectionKey{}, true)
		}
		child, finish, e := s.startAccountCheck(ctx, domain.NewID(), domain.NewID(), check.provider, check.operation)
		if e != nil {
			unlock()
			t.Fatal(e)
		}
		contexts[i] = child
		defer finish()
	}
	unlock()
	provider.SetEnabled(false)
	raw, _ := json.Marshal(provider)
	_, err = s.SaveConfiguration(context.Background(), connect.NewRequest(&pb.SaveConfigurationRequest{Mutation: &pb.Mutation{Id: string(pid), ExpectedRevision: 1, RequestId: string(domain.NewID())}, Kind: pb.EntityKind_ENTITY_KIND_PROVIDER, SchemaVersion: 1, DocumentJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	for i, check := range checks {
		if (contexts[i].Err() != nil) != check.wantCanceled {
			t.Fatalf("inspection %d lost independent ownership", i)
		}
	}
}
