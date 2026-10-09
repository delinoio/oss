// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSourceChangeCancelsOnlyOriginalEndpointReads(t *testing.T) {
	provider, account, other := domain.NewID(), domain.NewID(), domain.NewID()
	endpoint, stop := context.WithCancel(context.Background())
	defer stop()
	validation, stopValidation := context.WithCancel(context.Background())
	defer stopValidation()
	unrelated, stopOther := context.WithCancel(context.Background())
	defer stopOther()
	s := &Service{accountChecks: map[domain.ID]map[domain.ID]accountCheck{account: {domain.NewID(): {cancel: stop, providerID: provider, operation: endpointListing}, domain.NewID(): {cancel: stopValidation, providerID: provider, operation: validationInspection}}, other: {domain.NewID(): {cancel: stopOther, providerID: other, operation: endpointListing}}}}
	s.cancelEndpointChecks(provider, true)
	if endpoint.Err() == nil {
		t.Fatal("original read survived source change")
	}
	if validation.Err() != nil || unrelated.Err() != nil {
		t.Fatal("independent inspection cancelled")
	}
}

func TestEndpointSuggestionsAreOriginalAccountReadOnly(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/models" || r.Header.Get("Authorization") != "" {
			t.Error("private probe or credential exposure", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"exact/native","name":"Optional hint"}]}`))
	}))
	defer upstream.Close()
	f := newAccountFixture(t)
	p := f.save(pb.EntityKind_ENTITY_KIND_PROVIDER, domain.Provider{Name: "Hints", Endpoint: upstream.URL, Protocol: domain.OpenAIChat, Authentication: domain.KeylessAuth, Discovery: false})
	a := wizardAccount(f, p, "Connected")
	connected, err := connectAccount(f, a, domain.NewID(), "", true)
	if err != nil {
		t.Fatal(err)
	}
	original := connected.Msg.Account
	response, err := catalogClient(f).ListEndpointModels(context.Background(), ownerRequest(f.identity, &pb.ListEndpointModelsRequest{AccountId: original.Id, ExpectedAccountRevision: original.Revision, ExpectedProviderRevision: p.Revision}))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(response.Msg.Models) != 1 || response.Msg.Models[0].NativeId != "exact/native" || response.Msg.AccountRevision != original.Revision {
		t.Fatal("non-exact or repeated suggestion read", response)
	}
	current := currentCatalogResource(t, f, original)
	if current.Revision != original.Revision || string(current.DocumentJson) != string(original.DocumentJson) {
		t.Fatal("read changed account")
	}
	_, err = catalogClient(f).SearchModels(context.Background(), ownerRequest(f.identity, &pb.SearchModelsRequest{}))
	wantAccountCode(t, err, domain.Unsupported)
	_, err = f.resources.ListResources(context.Background(), ownerRequest(f.identity, &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_MODEL}}))
	wantAccountCode(t, err, domain.InvalidArgument)
}
