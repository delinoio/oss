// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestSaveConfigurationCancelsOnlyAffectedCatalogInspections(t *testing.T) {
	for _, test := range []struct {
		name                         string
		enabled, discovery, canceled bool
	}{
		{"provider-disabled", false, true, true},
		{"discovery-disabled", true, false, true},
		{"enabled-discovery", true, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 5*time.Second)
			defer cancel()
			started := make(chan struct{}, 3)
			canceled := make(chan struct{}, 3)
			release := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				started <- struct{}{}
				select {
				case <-r.Context().Done():
					canceled <- struct{}{}
				case <-release:
					fmt.Fprint(w, `{"data":[{"id":"fixture"}]}`)
				}
			}))
			defer upstream.Close()
			defer upstream.CloseClientConnections()
			s, _ := newDoctorFixture(t)
			providerID, otherProviderID := domain.NewID(), domain.NewID()
			provider := domain.Provider{Name: "Catalog fixture", Endpoint: upstream.URL + "/v1", Protocol: domain.OpenAIChat, Authentication: domain.KeylessAuth, Discovery: true}
			provider.SetEnabled(true)
			doctorPut(t, s, domain.ProviderKind, providerID, 0, provider)
			otherProvider := provider
			otherProvider.Name = "Independent fixture"
			doctorPut(t, s, domain.ProviderKind, otherProviderID, 0, otherProvider)
			type outcome struct {
				err       error
				published bool
			}
			run := func(id domain.ID, operation inspectionOperation) (store.Record, <-chan outcome) {
				t.Helper()
				accountID := domain.NewID()
				account := domain.Account{Alias: "Inspection fixture", ProviderID: id, Type: domain.APIAccount, Enabled: true, Health: domain.AccountUnverified, Connection: &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.KeylessAuth, ConnectedAt: time.Now().UTC()}}
				doctorPut(t, s, domain.AccountKind, accountID, 0, account)
				record, err := s.Store.Get(ctx, domain.AccountKind, accountID)
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan outcome, 1)
				go func() {
					published := false
					// Persistent Model RPCs are retired. Exercise the shared inspection
					// lifecycle directly without reactivating that public API.
					_, err := s.inspectAccount(ctx, &pb.Mutation{Id: string(accountID), ExpectedRevision: record.Revision, RequestId: string(domain.NewID())}, operation, "", func(_ *store.Tx, current domain.Account, observation accountInspection) (any, error) {
						published = true
						if observation.Problem != nil || len(observation.Models) != 1 {
							return nil, fmt.Errorf("unexpected inspection observation: %v", observation.Problem)
						}
						return current, nil
					})
					done <- outcome{err, published}
				}()
				select {
				case <-started:
				case <-ctx.Done():
					t.Fatal("inspection did not reach upstream")
				}
				return record, done
			}
			original, catalogDone := run(providerID, catalogInspection)
			_, validationDone := run(providerID, validationInspection)
			_, otherDone := run(otherProviderID, catalogInspection)
			record, err := s.Store.Get(ctx, domain.ProviderKind, providerID)
			if err != nil {
				t.Fatal(err)
			}
			provider.SetEnabled(test.enabled)
			provider.Discovery = test.discovery
			raw, err := json.Marshal(provider)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.SaveConfiguration(ctx, connect.NewRequest(&pb.SaveConfigurationRequest{Mutation: &pb.Mutation{Id: string(providerID), ExpectedRevision: record.Revision, RequestId: string(domain.NewID())}, Kind: pb.EntityKind_ENTITY_KIND_PROVIDER, SchemaVersion: rpc.ResourceSchemaVersion(domain.ProviderKind, raw), DocumentJson: raw}))
			if err != nil {
				t.Fatal(err)
			}
			await := func(done <-chan outcome) outcome {
				t.Helper()
				select {
				case result := <-done:
					return result
				case <-ctx.Done():
					t.Fatal("inspection did not finish")
					return outcome{}
				}
			}
			if test.canceled {
				select {
				case <-canceled:
				case <-ctx.Done():
					t.Fatal("provider save did not cancel upstream catalog request")
				}
				result := await(catalogDone)
				if result.err == nil || domain.SafeError(result.err).Code != domain.Canceled || result.published {
					t.Fatalf("canceled catalog published: %+v", result)
				}
				current, err := s.Store.Get(ctx, domain.AccountKind, original.ID)
				if err != nil || current.Revision != original.Revision || string(current.Data) != string(original.Data) {
					t.Fatalf("canceled inspection changed prior account evidence: %v", err)
				}
			}
			close(release)
			if !test.canceled {
				if result := await(catalogDone); result.err != nil || !result.published {
					t.Fatalf("enabled catalog failed: %+v", result)
				}
			}
			for _, done := range []<-chan outcome{validationDone, otherDone} {
				if result := await(done); result.err != nil || !result.published {
					t.Fatalf("independent inspection affected: %+v", result)
				}
			}
			select {
			case <-canceled:
				t.Fatal("an independent inspection was canceled")
			default:
			}
		})
	}
}
