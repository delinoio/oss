// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestManagedMCPRecordsAreNotAGenericResourceKind(t *testing.T) {
	if _, err := Kind(pb.EntityKind_ENTITY_KIND_MANAGED_MCP); domain.SafeError(err).Code != domain.InvalidArgument {
		t.Fatalf("generic resource APIs accepted the private managed MCP record kind: %v", err)
	}
	if kind, err := Kind(pb.EntityKind_ENTITY_KIND_AGENT); err != nil || kind != domain.AgentKind {
		t.Fatalf("ordinary kinds changed: %v %v", kind, err)
	}
}
