// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// TestCodexOwnerFinishRetiresTheFoundingOwnerMarker pins that an owner-scope
// Finish sent through a joined member's lease clears the founding owner's
// in-flight marker even when the Finish outcome stays unconfirmed. Recovery is
// keyed by the founding owner lease, so a stale marker would hold a closed
// owner active until the process reconnected.
func TestCodexOwnerFinishRetiresTheFoundingOwnerMarker(t *testing.T) {
	_, handler := delidevv1connect.NewSubscriptionServiceHandler(delidevv1connect.UnimplementedSubscriptionServiceHandler{})
	server := httptest.NewServer(handler)
	defer server.Close()
	client := delidevv1connect.NewSubscriptionServiceClient(server.Client(), server.URL)
	build := func(t *testing.T, owner, member domain.ID, joined bool) *managedSubscriptionLease {
		response := &pb.TakeSubscriptionResponse{LeaseId: string(owner), GenerationId: string(domain.NewID())}
		if joined {
			response.LeaseId, response.OwnerLeaseId = string(member), string(owner)
		}
		return &managedSubscriptionLease{client: client, credential: Credential{MachineID: domain.NewID(), Token: "token"}, instance: domain.NewID(), account: domain.NewID(), response: response, finishID: domain.NewID(), journalPath: filepath.Join(t.TempDir(), "lease.json")}
	}
	for _, joined := range []bool{true, false} {
		owner, member := domain.NewID(), domain.NewID()
		codexLeasesInFlight.Store(string(owner), struct{}{})
		if joined {
			codexLeasesInFlight.Store(string(member), struct{}{})
		}
		lease := build(t, owner, member, joined)
		// A member-scope Finish of the joined member keeps the owner marker.
		if joined {
			_ = lease.finishCodexScoped(nil, true, true, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, 1)
			if !codexLeaseInFlight(owner) || codexLeaseInFlight(member) {
				t.Fatal("a joined member Finish must retire only its own marker")
			}
		}
		// The owner Finish is unconfirmed (the server rejects it), yet the
		// execution ended locally.
		if err := lease.finishCodexScoped(nil, true, true, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER, 1); err == nil {
			t.Fatal("the fixture server unexpectedly accepted the Finish")
		}
		if codexLeaseInFlight(owner) || joined && codexLeaseInFlight(member) {
			t.Fatalf("an owner Finish left a lease marker behind (joined=%v)", joined)
		}
	}
}
