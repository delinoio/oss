// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestAttachRequestIdentityFollowsExactAdvertisedCapabilities(t *testing.T) {
	const (
		a = pb.WorkerCapability_WORKER_CAPABILITY_INLINE_MODEL_EXECUTION_V1
		b = pb.WorkerCapability_WORKER_CAPABILITY_OPENCODE_IMAGE_INPUTS_V1
		c = pb.WorkerCapability_WORKER_CAPABILITY_REPOSITORY_BRANCH_DISCOVERY_V1
	)
	request := func(capabilities ...pb.WorkerCapability) *pb.AttachWorkerRequest {
		return &pb.AttachWorkerRequest{MachineId: "machine", InstanceId: "instance", ProtocolVersion: 2, Version: "1", Capabilities: capabilities}
	}
	for _, tc := range []struct {
		name         string
		before, next *pb.AttachWorkerRequest
		nextProfile  string
		same         bool
	}{
		{"identical", request(a, b), request(a, b), "", true},
		{"add", request(a), request(a, b), "", false},
		{"remove", request(a, b), request(a), "", false},
		{"swap", request(a, b), request(a, c), "", false},
		{"reorder", request(a, b), request(b, a), "", false},
		{"duplicate", request(a, b), request(a, b, b), "", false},
		{"remove duplicate", request(a, b, b), request(a, b), "", false},
		{"empty to one", request(), request(a), "", false},
		{"network generation", request(a), func() *pb.AttachWorkerRequest {
			r := request(a)
			r.NetworkGeneration = 2
			return r
		}(), "", false},
		{"network recipient", request(a), func() *pb.AttachWorkerRequest {
			r := request(a)
			r.NetworkKeyId, r.NetworkRecipient = "key", "recipient"
			return r
		}(), "", false},
		{"local profile", request(a), request(a), "profile-change", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var identity attachRequestIdentity
			identity.assign("", tc.before)
			first := tc.before.RequestId
			identity.assign(tc.nextProfile, tc.next)
			if first == "" || tc.next.RequestId == "" {
				t.Fatal("attach request identity was not assigned")
			}
			if (first == tc.next.RequestId) != tc.same {
				t.Fatalf("request identity reuse = %t, want %t", first == tc.next.RequestId, tc.same)
			}
		})
	}
}

func TestAttachRequestIdentityIsStableAcrossRepeatedAssignments(t *testing.T) {
	var identity attachRequestIdentity
	first := &pb.AttachWorkerRequest{MachineId: "machine", Capabilities: []pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_NATIVE_SKILLS_V1}}
	identity.assign("profile", first)
	for range 3 {
		again := &pb.AttachWorkerRequest{MachineId: "machine", Capabilities: []pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_NATIVE_SKILLS_V1}}
		identity.assign("profile", again)
		if again.RequestId != first.RequestId {
			t.Fatal("identical retry rotated its request identity")
		}
	}
	other := &pb.AttachWorkerRequest{MachineId: "other", Capabilities: first.Capabilities}
	identity.assign("profile", other)
	if other.RequestId == first.RequestId {
		t.Fatal("different Worker machine reused the request identity")
	}
}

func TestOpenCodeImageInputsAttachmentRenegotiatesProfileWhenSupportChanges(t *testing.T) {
	assertCapabilityAttachmentRenegotiatesProfile(t, pb.WorkerCapability_WORKER_CAPABILITY_OPENCODE_IMAGE_INPUTS_V1)
}

func TestBranchPrefixInstructionsAttachmentRenegotiatesProfileWhenSupportChanges(t *testing.T) {
	assertCapabilityAttachmentRenegotiatesProfile(t, pb.WorkerCapability_WORKER_CAPABILITY_BRANCH_PREFIX_INSTRUCTIONS_V1)
}

func TestRepositoryBranchDiscoveryAttachmentRenegotiatesProfileWhenSupportChanges(t *testing.T) {
	assertCapabilityAttachmentRenegotiatesProfile(t, pb.WorkerCapability_WORKER_CAPABILITY_REPOSITORY_BRANCH_DISCOVERY_V1)
}

// Every capability the Worker can advertise after server negotiation must
// rotate the reconnect identity when the server adds or removes its support.
func TestEveryNegotiatedCapabilityRotatesAttachIdentityOnSupportChange(t *testing.T) {
	advertised := 0
	for value, name := range pb.WorkerCapability_name {
		capability := pb.WorkerCapability(value)
		if capability == pb.WorkerCapability_WORKER_CAPABILITY_UNSPECIFIED {
			continue
		}
		for _, tc := range []struct {
			name    string
			support []bool
		}{
			{"added", []bool{false, false, true, true}},
			{"removed", []bool{true, true, false, false}},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				fixture := &imageGenerationAttachFixture{serverID: domain.NewID(), cancel: cancel, support: tc.support, capability: capability}
				_, handler := delidevv1connect.NewWorkerServiceHandler(fixture)
				server := httptest.NewServer(handler)
				defer server.Close()
				credential := Credential{Endpoint: server.URL, ServerID: fixture.serverID, MachineID: domain.NewID(), Token: "isolated-image-attachment"}
				_ = runConnected(ctx, Config{Root: t.TempDir(), Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, credential)
				fixture.Lock()
				defer fixture.Unlock()
				if len(fixture.requests) != 4 {
					t.Skip("capability is baseline or gated by other negotiated state")
				}
				negotiated, renegotiated := fixture.requests[1], fixture.requests[3]
				before, after := slices.Contains(negotiated.Capabilities, capability), slices.Contains(renegotiated.Capabilities, capability)
				if before == after {
					t.Skip("capability is not advertised from this support change alone")
				}
				advertised++
				if negotiated.RequestId == renegotiated.RequestId {
					t.Fatalf("changing %s advertisement reused the attach request identity", name)
				}
			})
		}
	}
	if advertised == 0 {
		t.Fatal("no negotiated capability was exercised")
	}
}
