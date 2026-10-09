// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestNativeImageGenerationPreservesOriginalNegotiatedMachineCapabilities(t *testing.T) {
	for _, test := range []struct {
		name         string
		capabilities []domain.WorkerCapability
		accepted     bool
	}{
		{"mixed known capabilities", []domain.WorkerCapability{domain.RemoteWorkspaceCloneV1, domain.BranchPrefixInstructionsV1, domain.NativeImageGenerationV1}, true},
		{"unknown alongside known", []domain.WorkerCapability{domain.RemoteWorkspaceCloneV1, domain.NativeImageGenerationV1, domain.WorkerCapability("unknown-fixture-capability")}, false},
		{"duplicate image capability", []domain.WorkerCapability{domain.RemoteWorkspaceCloneV1, domain.NativeImageGenerationV1, domain.NativeImageGenerationV1}, false},
		{"duplicate original capability", []domain.WorkerCapability{domain.RemoteWorkspaceCloneV1, domain.NativeImageGenerationV1, domain.RemoteWorkspaceCloneV1}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			machine := domain.Machine{Name: "Original Worker", OS: "linux", Architecture: "amd64", WorkerCapabilities: test.capabilities}
			raw, err := json.Marshal(machine)
			if err != nil {
				t.Fatal(err)
			}
			resource := &pb.Resource{Kind: pb.EntityKind_ENTITY_KIND_MACHINE, SchemaVersion: 1, DocumentJson: raw}
			// A supported additive capability cannot hide an independently
			// negotiated capability. Unknown or duplicate documents remain closed.
			for _, capability := range []domain.WorkerCapability{domain.RemoteWorkspaceCloneV1, domain.NativeImageGenerationV1, domain.BranchPrefixInstructionsV1} {
				if got := machineCapability(resource, capability); got != test.accepted {
					t.Fatalf("capability %s accepted=%t, expected valid document=%t", capability, got, test.accepted)
				}
			}
		})
	}
}
