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
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
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
			got, err := claudeCleanupInstallation(context.Background(), tc.config, tc.journal)
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

func TestClaudeInstallationAdmissionCoversInspectedAndDirectStartupIdentities(t *testing.T) {
	digest := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name         string
		installation domain.Installation
		want         bool
	}{
		{"inspected baseline", domain.Installation{Harness: domain.ClaudeCode, ResolvedPath: "/claude", Version: domain.ClaudeProtocolVersion}, true},
		{"inspected newer", domain.Installation{Harness: domain.ClaudeCode, ResolvedPath: "/claude", Version: "9.9.9"}, true},
		{"inspected newer with digest", domain.Installation{Harness: domain.ClaudeCode, ResolvedPath: "/claude", Version: "9.9.9", ExecutableSHA256: digest}, true},
		{"inspected below baseline", domain.Installation{Harness: domain.ClaudeCode, ResolvedPath: "/claude", Version: "0.0.1", ExecutableSHA256: digest}, false},
		{"inspected malformed", domain.Installation{Harness: domain.ClaudeCode, ResolvedPath: "/claude", Version: "not-a-version"}, false},
		{"direct startup path and digest", domain.Installation{Harness: domain.ClaudeCode, ResolvedPath: "/claude", ExecutableSHA256: digest}, true},
		{"direct startup without digest", domain.Installation{Harness: domain.ClaudeCode, ResolvedPath: "/claude"}, false},
		{"direct startup malformed digest", domain.Installation{Harness: domain.ClaudeCode, ResolvedPath: "/claude", ExecutableSHA256: "ABC"}, false},
		{"direct startup without path", domain.Installation{Harness: domain.ClaudeCode, ExecutableSHA256: digest}, false},
		{"another harness", domain.Installation{Harness: domain.Codex, ResolvedPath: "/claude", ExecutableSHA256: digest}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeInstallationAdmitted(tc.installation); got != tc.want {
				t.Fatalf("admitted = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestClaudeCleanupInstallationRechecksRetainedExecutableDigest(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "claude")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	digest, err := harness.InspectExecutable(context.Background(), executable)
	if err != nil {
		t.Fatal(err)
	}
	other := strings.Repeat("0", 64)
	retained := func(version, sum string) managedSubscriptionJournal {
		return managedSubscriptionJournal{Installation: &domain.Installation{Harness: domain.ClaudeCode, ResolvedPath: executable, Version: version, ExecutableSHA256: sum}}
	}
	for _, tc := range []struct {
		name    string
		journal managedSubscriptionJournal
		mutate  func()
		changed bool
	}{
		{"matching direct-startup digest", retained("", digest), func() {}, false},
		{"matching inspected digest", retained(domain.ClaudeProtocolVersion, digest), func() {}, false},
		{"inspected without retained digest", retained(domain.ClaudeProtocolVersion, ""), func() { _ = os.WriteFile(executable, []byte("#!/bin/sh\nexit 1\n"), 0o700) }, false},
		{"mismatching retained digest", retained("", other), func() {}, true},
		{"replaced in place after the operation", retained("", digest), func() { _ = os.WriteFile(executable, []byte("#!/bin/sh\nexit 2\n"), 0o700) }, true},
		{"removed after the operation", retained("", digest), func() { _ = os.Remove(executable) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			tc.mutate()
			got, err := claudeCleanupInstallation(context.Background(), Config{}, tc.journal)
			if tc.changed {
				if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
					t.Fatal("changed executable was not rejected before cleanup", err)
				}
				return
			}
			if err != nil || got.ResolvedPath != executable {
				t.Fatal("unchanged executable was rejected", err)
			}
		})
	}
	t.Run("legacy journal inventory digest is rechecked too", func(t *testing.T) {
		if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		inventory := func(sum string) Config {
			raw, err := json.Marshal(domain.Machine{Name: "fixture", OS: "darwin", Architecture: "arm64", Version: "1", Installations: []domain.Installation{{Harness: domain.ClaudeCode, State: domain.InstallationDetected, ResolvedPath: executable, Version: domain.ClaudeProtocolVersion, ProtocolVerified: true, ExecutableSHA256: sum, Protocol: &domain.ProtocolObservation{State: domain.ProtocolVerified}}}})
			if err != nil {
				t.Fatal(err)
			}
			return Config{attachedMachine: &pb.Resource{DocumentJson: raw}}
		}
		if _, err := claudeCleanupInstallation(context.Background(), inventory(digest), managedSubscriptionJournal{}); err != nil {
			t.Fatal("matching inventory digest rejected", err)
		}
		if _, err := claudeCleanupInstallation(context.Background(), inventory(other), managedSubscriptionJournal{}); err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
			t.Fatal("mismatching inventory digest was accepted", err)
		}
	})
}

func TestOpenClaudeProfileRechecksRetainedDigestBeforeProfileAccess(t *testing.T) {
	original := []byte("#!/bin/sh\nexit 0\n")
	replaced := []byte("#!/bin/sh\nexit 7\n")
	for _, tc := range []struct {
		name     string
		inspect  bool
		retain   string // "match", "none" or "other"
		mutate   func(path string)
		wantOpen bool
	}{
		{"direct startup unchanged", false, "match", func(string) {}, true},
		{"direct startup replaced while waiting", false, "match", func(p string) { _ = os.WriteFile(p, replaced, 0o700) }, false},
		{"direct startup removed while waiting", false, "match", func(p string) { _ = os.Remove(p) }, false},
		{"inspected with digest unchanged", true, "match", func(string) {}, true},
		{"inspected with digest replaced", true, "match", func(p string) { _ = os.WriteFile(p, replaced, 0o700) }, false},
		{"inspected with foreign digest", true, "other", func(string) {}, false},
		{"inspected without digest replaced", true, "none", func(p string) { _ = os.WriteFile(p, replaced, 0o700) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			executable := filepath.Join(root, "claude")
			if err := os.WriteFile(executable, original, 0o700); err != nil {
				t.Fatal(err)
			}
			digest, err := harness.InspectExecutable(context.Background(), executable)
			if err != nil {
				t.Fatal(err)
			}
			installation := domain.Installation{Harness: domain.ClaudeCode, ResolvedPath: executable}
			switch tc.retain {
			case "match":
				installation.ExecutableSHA256 = digest
			case "other":
				installation.ExecutableSHA256 = strings.Repeat("0", 64)
			}
			if tc.inspect {
				installation.Version = domain.ClaudeProtocolVersion
			}
			tc.mutate(executable)
			config := Config{Root: filepath.Join(root, "state")}
			credential := Credential{ServerID: domain.NewID(), MachineID: domain.NewID()}
			profile, err := openClaudeProfile(config, credential, domain.NewID(), domain.NewID(), installation, domain.NewID(), true)
			if tc.wantOpen {
				if err != nil {
					t.Fatal("unchanged executable was rejected", err)
				}
				if err := profile.close(); err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("changed executable reached the profile", err)
			}
			if _, statErr := os.Lstat(filepath.Join(config.Root, "native-claude")); !os.IsNotExist(statErr) {
				t.Fatal("profile directories were touched before the digest check", statErr)
			}
		})
	}
}
