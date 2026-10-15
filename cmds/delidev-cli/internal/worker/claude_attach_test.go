// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

const (
	nativeClaudeCapability  = pb.WorkerCapability_WORKER_CAPABILITY_NATIVE_CLAUDE_SUBSCRIPTIONS_V1
	managedClaudeCapability = pb.WorkerCapability_WORKER_CAPABILITY_MANAGED_CLAUDE_EXECUTION_V1
)

// claudeAttachFixture answers each attachment with a scripted set of supported
// capabilities and one fixed Machine inventory. It never rejects a request, so
// the test observes exactly what the Worker advertises.
type claudeAttachFixture struct {
	delidevv1connect.UnimplementedWorkerServiceHandler
	sync.Mutex
	serverID domain.ID
	cancel   context.CancelFunc
	support  [][]pb.WorkerCapability
	machine  *pb.Resource
	requests []*pb.AttachWorkerRequest
}

func (f *claudeAttachFixture) AttachWorker(_ context.Context, req *connect.Request[pb.AttachWorkerRequest]) (*connect.Response[pb.AttachWorkerResponse], error) {
	f.Lock()
	defer f.Unlock()
	index := len(f.requests)
	if req.Header().Get("Authorization") != "Bearer isolated-claude-attachment" || index >= len(f.support) {
		f.cancel()
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}
	f.requests = append(f.requests, req.Msg)
	if index+1 == len(f.support) {
		f.cancel()
	}
	return connect.NewResponse(&pb.AttachWorkerResponse{ServerId: string(f.serverID), SupportedWorkerCapabilities: f.support[index], Machine: f.machine}), nil
}

func (f *claudeAttachFixture) WatchWork(context.Context, *connect.Request[pb.WatchWorkRequest], *connect.ServerStream[pb.WatchWorkResponse]) error {
	return connect.NewError(connect.CodeUnavailable, nil)
}

// claudeInventory builds a Machine resource whose only Claude installation points
// at a marker script. Any native launch would create the returned marker path.
func claudeInventory(t *testing.T, installations func(executable string) []domain.Installation) (*pb.Resource, string) {
	t.Helper()
	directory := t.TempDir()
	marker := filepath.Join(directory, "launched")
	executable := filepath.Join(directory, "claude")
	script := "#!/bin/sh\necho launched > '" + marker + "'\nexit 1\n"
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(domain.Machine{Name: "fixture", OS: "darwin", Architecture: "arm64", Version: "1", Installations: installations(executable), LastSeen: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	return &pb.Resource{DocumentJson: raw}, marker
}

func TestClaudeAttachAdvertisesSupportedAdaptersWithoutInstallationReadiness(t *testing.T) {
	now := time.Now().UTC()
	inventories := map[string]func(string) []domain.Installation{
		"empty": func(string) []domain.Installation { return nil },
		"detected-unverified": func(executable string) []domain.Installation {
			return []domain.Installation{{Harness: domain.ClaudeCode, State: domain.InstallationDetected, ResolvedPath: executable, Version: "2.1.287", ProtocolVerified: false, ObservedAt: &now}}
		},
		"stale-version": func(executable string) []domain.Installation {
			return []domain.Installation{{Harness: domain.ClaudeCode, State: domain.InstallationDetected, ResolvedPath: executable, Version: "0.0.1", ProtocolVerified: true, ObservedAt: &now}}
		},
		"ambiguous-verified": func(executable string) []domain.Installation {
			verified := domain.Installation{Harness: domain.ClaudeCode, State: domain.InstallationDetected, ResolvedPath: executable, Version: domain.ClaudeProtocolVersion, ProtocolVerified: true, Protocol: &domain.ProtocolObservation{State: domain.ProtocolVerified}}
			return []domain.Installation{verified, verified}
		},
	}
	for inventoryName, inventory := range inventories {
		for _, tc := range []struct {
			name      string
			supported []pb.WorkerCapability
		}{
			{"device-only", []pb.WorkerCapability{nativeClaudeCapability}},
			{"managed-only", []pb.WorkerCapability{managedClaudeCapability}},
			{"both", []pb.WorkerCapability{nativeClaudeCapability, managedClaudeCapability}},
			{"neither", nil},
		} {
			t.Run(inventoryName+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				machine, marker := claudeInventory(t, inventory)
				fixture := &claudeAttachFixture{serverID: domain.NewID(), cancel: cancel, support: [][]pb.WorkerCapability{tc.supported, tc.supported}, machine: machine}
				_, handler := delidevv1connect.NewWorkerServiceHandler(fixture)
				server := httptest.NewServer(handler)
				defer server.Close()
				credential := Credential{Endpoint: server.URL, ServerID: fixture.serverID, MachineID: domain.NewID(), Token: "isolated-claude-attachment"}
				root := t.TempDir()
				_ = runConnected(ctx, Config{Root: root, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, credential)
				fixture.Lock()
				defer fixture.Unlock()
				if len(fixture.requests) != 2 && len(tc.supported) != 0 {
					t.Fatal("peer support did not trigger the negotiated attachment")
				}
				if len(fixture.requests) == 0 {
					t.Fatal("Worker never attached")
				}
				final := fixture.requests[len(fixture.requests)-1]
				for _, capability := range []pb.WorkerCapability{nativeClaudeCapability, managedClaudeCapability} {
					if slices.Contains(final.Capabilities, capability) != slices.Contains(tc.supported, capability) {
						t.Fatalf("%s advertisement did not follow peer support alone", capability)
					}
				}
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatal("attachment launched the Claude executable")
				}
				if _, err := os.Stat(filepath.Join(root, "processes")); !os.IsNotExist(err) {
					t.Fatal("attachment created native process ownership state")
				}
			})
		}
	}
}

func TestClaudeAttachRotatesIdentityWhenEitherCapabilitySupportChanges(t *testing.T) {
	for _, tc := range []struct {
		name         string
		first, again []pb.WorkerCapability
		sameIdentity bool
	}{
		{"unchanged-both", []pb.WorkerCapability{nativeClaudeCapability, managedClaudeCapability}, []pb.WorkerCapability{nativeClaudeCapability, managedClaudeCapability}, true},
		{"managed-added", []pb.WorkerCapability{nativeClaudeCapability}, []pb.WorkerCapability{nativeClaudeCapability, managedClaudeCapability}, false},
		{"managed-removed", []pb.WorkerCapability{nativeClaudeCapability, managedClaudeCapability}, []pb.WorkerCapability{nativeClaudeCapability}, false},
		{"device-added", []pb.WorkerCapability{managedClaudeCapability}, []pb.WorkerCapability{nativeClaudeCapability, managedClaudeCapability}, false},
		{"device-removed", []pb.WorkerCapability{nativeClaudeCapability, managedClaudeCapability}, []pb.WorkerCapability{managedClaudeCapability}, false},
		{"all-removed", []pb.WorkerCapability{nativeClaudeCapability, managedClaudeCapability}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			machine, marker := claudeInventory(t, func(string) []domain.Installation { return nil })
			// Requests: compatibility, negotiated, compatibility, negotiated.
			fixture := &claudeAttachFixture{serverID: domain.NewID(), cancel: cancel, support: [][]pb.WorkerCapability{tc.first, tc.first, tc.again, tc.again}, machine: machine}
			_, handler := delidevv1connect.NewWorkerServiceHandler(fixture)
			server := httptest.NewServer(handler)
			defer server.Close()
			credential := Credential{Endpoint: server.URL, ServerID: fixture.serverID, MachineID: domain.NewID(), Token: "isolated-claude-attachment"}
			_ = runConnected(ctx, Config{Root: t.TempDir(), Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, credential)
			fixture.Lock()
			defer fixture.Unlock()
			if len(fixture.requests) != 4 {
				t.Fatalf("got %d attach requests, want 4", len(fixture.requests))
			}
			negotiated, renegotiated := fixture.requests[1], fixture.requests[3]
			if (negotiated.RequestId == renegotiated.RequestId) != tc.sameIdentity {
				t.Fatalf("request identity reuse = %t, want %t", negotiated.RequestId == renegotiated.RequestId, tc.sameIdentity)
			}
			for capability, supported := range map[pb.WorkerCapability][2]bool{
				nativeClaudeCapability:  {slices.Contains(tc.first, nativeClaudeCapability), slices.Contains(tc.again, nativeClaudeCapability)},
				managedClaudeCapability: {slices.Contains(tc.first, managedClaudeCapability), slices.Contains(tc.again, managedClaudeCapability)},
			} {
				if slices.Contains(negotiated.Capabilities, capability) != supported[0] || slices.Contains(renegotiated.Capabilities, capability) != supported[1] {
					t.Fatalf("%s advertisement did not follow each attach's peer support", capability)
				}
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("attachment launched the Claude executable")
			}
		})
	}
}

func TestClaudeCleanupInstallationPrefersRetainedJournalEvidence(t *testing.T) {
	verified := func(path string) domain.Installation {
		return domain.Installation{Harness: domain.ClaudeCode, State: domain.InstallationDetected, ResolvedPath: path, Version: domain.ClaudeProtocolVersion, ProtocolVerified: true, Protocol: &domain.ProtocolObservation{State: domain.ProtocolVerified}}
	}
	resource := func(installations ...domain.Installation) *pb.Resource {
		raw, err := json.Marshal(domain.Machine{Name: "fixture", OS: "darwin", Architecture: "arm64", Version: "1", Installations: installations})
		if err != nil {
			t.Fatal(err)
		}
		return &pb.Resource{DocumentJson: raw}
	}
	original, current := verified("/original/claude"), verified("/current/claude")
	for _, tc := range []struct {
		name    string
		config  Config
		journal managedSubscriptionJournal
		want    string
		invalid bool
	}{
		{"journal wins over current inventory", Config{attachedMachine: resource(current)}, managedSubscriptionJournal{Installation: &original}, "/original/claude", false},
		{"journal works without any inventory", Config{}, managedSubscriptionJournal{Installation: &original}, "/original/claude", false},
		{"legacy journal uses the single verified entry", Config{attachedMachine: resource(current)}, managedSubscriptionJournal{}, "/current/claude", false},
		{"legacy journal without inventory", Config{}, managedSubscriptionJournal{}, "", true},
		{"legacy journal with ambiguous inventory", Config{attachedMachine: resource(current, original)}, managedSubscriptionJournal{}, "", true},
		{"legacy journal with unverified inventory", Config{attachedMachine: resource(domain.Installation{Harness: domain.ClaudeCode, State: domain.InstallationDetected, ResolvedPath: "/current/claude", Version: domain.ClaudeProtocolVersion})}, managedSubscriptionJournal{}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := claudeCleanupInstallation(tc.config, tc.journal)
			if tc.invalid {
				if err == nil || domain.SafeError(err).Code != domain.SafeError(subscription.Invalid()).Code {
					t.Fatal("missing or ambiguous original evidence did not fail closed", err)
				}
				return
			}
			if err != nil || got.ResolvedPath != tc.want {
				t.Fatal("cleanup resolved the wrong installation", got.ResolvedPath, err)
			}
		})
	}
}
