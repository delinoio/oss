// SPDX-License-Identifier: Apache-2.0
package server

import (
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/tailscale"
)

func TestTailnetIdentityCompatibilityIsExact(t *testing.T) {
	const origin = "https://peer.tail1234.ts.net:8443"
	good := tailnetIdentity{Version: 1, ServerID: domain.NewID(), Origin: origin, ProtocolVersion: rpc.ProtocolVersion, Approval: true}
	if !good.compatible(origin) {
		t.Fatal("exact identity rejected")
	}
	for name, mutate := range map[string]func(*tailnetIdentity){
		"version":  func(v *tailnetIdentity) { v.Version = 2 },
		"server":   func(v *tailnetIdentity) { v.ServerID = "" },
		"origin":   func(v *tailnetIdentity) { v.Origin = "https://other.tail1234.ts.net:8443" },
		"protocol": func(v *tailnetIdentity) { v.ProtocolVersion++ },
		"approval": func(v *tailnetIdentity) { v.Approval = false },
	} {
		bad := good
		mutate(&bad)
		if bad.compatible(origin) {
			t.Fatalf("%s mismatch accepted", name)
		}
	}
}

func TestTailnetIdentityRejectionCarriesOnlyTheClosedCause(t *testing.T) {
	client := rpc.ClientError(rpc.Error(tailnetIdentityRejection(), ""))
	if client.Code != domain.Unsupported || client.Cause != string(tailscale.ProbeInvalidIdentity) {
		t.Fatalf("%#v", client)
	}
	generic := tailnetFailure(domain.Unsupported)
	if client.Message != domain.SafeError(generic).Message || client.Guidance != domain.SafeError(generic).Guidance {
		t.Fatalf("safe text changed: %#v", client)
	}
}
