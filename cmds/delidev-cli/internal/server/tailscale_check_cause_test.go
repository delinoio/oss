// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/tailscale"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
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

func probeCauseFailure(code domain.Code, cause tailscale.ProbeCause) error {
	return &domain.Error{Code: code, Message: "Probe failed.", Guidance: "Retry.", Cause: string(cause)}
}

// newCauseProbeService returns a Service whose only discovered peer is
// reachable through the supplied fake probe; probes records the withCause flag
// of each call.
func newCauseProbeService(t *testing.T, probe func(origin string, out any, withCause bool) error, withCauseCalls *[]bool) *Service {
	t.Helper()
	owner := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	db, err := store.Open(owner, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	identity := security.Identity{ServerID: domain.NewID(), Token: "temporary-tailnet-test-owner"}
	return &Service{
		Store:     db,
		Identity:  identity,
		logger:    slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)),
		tailscale: tailscale.NewReadyManagerForTest(identity.ServerID, "https://self.tailnet.ts.net:8443"),
		tailnetPeerLookup: func(context.Context, string) (tailscale.Peer, error) {
			return tailscale.Peer{ID: "peer", Name: "peer", Host: "peer.tailnet.ts.net", Online: true}, nil
		},
		tailnetProbe: func(_ context.Context, origin string, out any, withCause bool) error {
			*withCauseCalls = append(*withCauseCalls, withCause)
			return probe(origin, out, withCause)
		},
	}
}

// Check-only causes must cross the wire only from CheckTailscale. Begin
// revalidates the peer for a mutation and must report the same failures as
// generic ones, so the global client failure projection never types them as
// Check outcomes.
func TestBeginTailscaleConnectionNeverCarriesCheckOnlyCauses(t *testing.T) {
	failures := map[string]struct {
		code  domain.Code
		cause tailscale.ProbeCause
	}{
		"refused":     {domain.Unavailable, tailscale.ProbeConnectionRefused},
		"timeout":     {domain.Unavailable, tailscale.ProbeTimeout},
		"certificate": {domain.Unavailable, tailscale.ProbeCertificateInvalid},
		"http":        {domain.Unavailable, tailscale.ProbeUnexpectedHTTP},
		"identity":    {domain.Unsupported, tailscale.ProbeInvalidIdentity},
	}
	for name, failure := range failures {
		t.Run(name, func(t *testing.T) {
			var calls []bool
			probe := func(_ string, _ any, withCause bool) error {
				if withCause {
					return probeCauseFailure(failure.code, failure.cause)
				}
				return &domain.Error{Code: failure.code, Message: "Probe failed.", Guidance: "Retry."}
			}
			s := newCauseProbeService(t, probe, &calls)
			owner := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})

			_, err := s.CheckTailscale(owner, connect.NewRequest(&pb.CheckTailscaleRequest{PeerId: "peer"}))
			if got := rpc.ClientError(err); got.Code != failure.code || got.Cause != string(failure.cause) {
				t.Fatalf("Check lost its cause: %#v", got)
			}
			_, err = s.BeginTailscaleConnection(owner, connect.NewRequest(&pb.BeginTailscaleConnectionRequest{RequestId: string(domain.NewID()), PeerId: "peer", Name: "laptop", Role: pb.DeviceType_DEVICE_TYPE_CLIENT}))
			if err == nil {
				t.Fatal("Begin accepted a failing peer")
			}
			if got := rpc.ClientError(err); got.Code != failure.code || got.Cause != "" {
				t.Fatalf("Begin leaked a Check-only cause: %#v", got)
			}
			if len(calls) != 2 || !calls[0] || calls[1] {
				t.Fatalf("probe causes requested %v, want Check true then Begin false", calls)
			}
		})
	}
}

func TestBeginTailscaleConnectionIncompatibleIdentityIsGeneric(t *testing.T) {
	var calls []bool
	probe := func(origin string, out any, _ bool) error {
		*(out.(*tailnetIdentity)) = tailnetIdentity{Version: 1, ServerID: domain.NewID(), Origin: origin, ProtocolVersion: rpc.ProtocolVersion + 1, Approval: true}
		return nil
	}
	s := newCauseProbeService(t, probe, &calls)
	owner := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	_, err := s.BeginTailscaleConnection(owner, connect.NewRequest(&pb.BeginTailscaleConnectionRequest{RequestId: string(domain.NewID()), PeerId: "peer", Name: "laptop", Role: pb.DeviceType_DEVICE_TYPE_CLIENT}))
	if got := rpc.ClientError(err); got.Code != domain.Unsupported || got.Cause != "" {
		t.Fatalf("%#v", got)
	}
	_, err = s.CheckTailscale(owner, connect.NewRequest(&pb.CheckTailscaleRequest{PeerId: "peer"}))
	if got := rpc.ClientError(err); got.Code != domain.Unsupported || got.Cause != string(tailscale.ProbeInvalidIdentity) {
		t.Fatalf("%#v", got)
	}
}
