// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/userservice"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type identityOutcome uint8

const (
	identitySuccess identityOutcome = iota
	identityRejected
	identityLost
)

func wireMutationIdentity(message any) string {
	switch value := message.(type) {
	case interface{ GetMutation() *pb.Mutation }:
		return value.GetMutation().GetRequestId()
	case interface{ GetProvider() *pb.Mutation }:
		return value.GetProvider().GetRequestId()
	case interface{ GetRequestId() string }:
		return value.GetRequestId()
	default:
		return ""
	}
}

// The probe captures the real serialized CLI request. The loss wrapper discards
// its response only after that request has reached the authenticated fixture.
// An optional echo builds the success reply from the received request so
// fixtures can acknowledge the exact dispatched request ID.
func identityProbe[I, O any](mux *http.ServeMux, procedure string, reply *O, calls chan string, outcome identityOutcome, echo ...func(*I) *O) {
	mux.Handle(procedure, connect.NewUnaryHandler(procedure, func(ctx context.Context, req *connect.Request[I]) (*connect.Response[O], error) {
		id := wireMutationIdentity(req.Msg)
		calls <- id
		if err := domain.ID(id).Validate(); err != nil {
			return nil, rpc.Error(err, "")
		}
		if outcome == identityRejected {
			return nil, rpc.Error(domain.Fail(domain.Conflict, "Fixture revision changed.", "Retain the original request."), "")
		}
		if len(echo) > 0 {
			return connect.NewResponse(echo[0](req.Msg)), nil
		}
		return connect.NewResponse(reply), nil
	}))
}

func echoCheckUpdate(r *pb.CheckUpdateRequest) *pb.CheckUpdateResponse {
	return &pb.CheckUpdateResponse{RequestId: r.RequestId}
}

func echoWorkerUpdate(r *pb.RequestWorkerUpdateRequest) *pb.RequestWorkerUpdateResponse {
	return &pb.RequestWorkerUpdateResponse{RequestId: r.GetMutation().GetRequestId()}
}

func echoCancelUpdate(r *pb.CancelUpdateRequest) *pb.CancelUpdateResponse {
	return &pb.CancelUpdateResponse{RequestId: r.GetMutation().GetRequestId()}
}

func identityClientScope(t *testing.T, endpoint string, kind domain.DeviceType) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "client")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	token, err := worker.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	credential := worker.Credential{Version: 1, Type: kind, Endpoint: endpoint, ServerID: domain.NewID(), DeviceID: domain.NewID(), PairingID: domain.NewID(), Token: token}
	if kind == domain.WorkerDevice {
		credential.MachineID = domain.NewID()
	}
	raw, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(filepath.Join(root, "device.json"), raw); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCLIMutationDispatchRetainsGeneratedAndExplicitIdentities(t *testing.T) {
	resource, machine, account := string(domain.NewID()), string(domain.NewID()), string(domain.NewID())
	selection := domain.PRFixRequest{SetID: domain.NewID(), SetRevision: 1, RepositoryID: domain.NewID(), ProjectID: domain.NewID(), Problems: []domain.PRFixProblem{{ID: domain.NewID(), Revision: 1, ContentVersion: strings.Repeat("a", 64)}}}
	raw, _ := json.Marshal(selection)
	path := filepath.Join(t.TempDir(), "selection.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	commands := []struct {
		name    string
		args    []string
		input   string
		success bool
	}{
		{"oauth-start", []string{"account", "oauth", "start", "--provider-id", resource, "--revision", "1"}, "", true},
		{"oauth-complete", []string{"account", "oauth", "complete", "--attempt-id", resource, "--revision", "1", "--code-stdin"}, "fixture-code", true},
		{"oauth-cancel", []string{"account", "oauth", "cancel", "--attempt-id", resource, "--revision", "1"}, "", true},
		{"pr-refresh", []string{"github", "pr", "problems", "refresh", "--repository-id", resource, "--number", "17"}, "", false},
		{"pr-dismiss", []string{"github", "pr", "problems", "dismiss", "--id", resource, "--revision", "1", "--content-version", strings.Repeat("a", 64)}, "", false},
		{"pr-resume", []string{"github", "pr", "remediation", "resume", "--id", resource, "--revision", "1"}, "", false},
		{"pr-fix", []string{"github", "pr", "remediation", "fix", "--input", path}, "", false},
		{"native-discover", []string{"model", "native-discover", "--machine-id", machine, "--revision", "1", "--account-id", account, "--account-revision", "1"}, "", true},
		{"native-cancel", []string{"model", "native-cancel", "--id", resource, "--revision", "1"}, "", true},
		{"update-check", []string{"update", "check", "--component", "desktop", "--target", "darwin-arm64", "--current-version", "0.0.1"}, "", true},
		{"update-worker", []string{"update", "worker-request", "--id", resource, "--revision", "1"}, "", true},
		{"update-cancel", []string{"update", "cancel", "--id", resource, "--revision", "1"}, "", true},
	}
	for _, command := range commands {
		for _, explicit := range []bool{false, true} {
			for _, outcome := range []identityOutcome{identitySuccess, identityRejected, identityLost} {
				t.Run(command.name+"/"+map[bool]string{true: "explicit", false: "generated"}[explicit]+"/"+[]string{"response", "rejection", "lost"}[outcome], func(t *testing.T) {
					calls := make(chan string, 3)
					mux := http.NewServeMux()
					mux.Handle(delidevv1connect.SystemServiceGetStatusProcedure, connect.NewUnaryHandler(delidevv1connect.SystemServiceGetStatusProcedure, func(context.Context, *connect.Request[pb.GetStatusRequest]) (*connect.Response[pb.GetStatusResponse], error) {
						return connect.NewResponse(&pb.GetStatusResponse{Capabilities: []pb.SystemCapability{pb.SystemCapability_SYSTEM_CAPABILITY_SIGNED_UPDATES_V1}}), nil
					}))
					identityProbe[pb.StartAccountOAuthRequest](mux, delidevv1connect.AccountServiceStartAccountOAuthProcedure, &pb.StartAccountOAuthResponse{}, calls, outcome)
					identityProbe[pb.CompleteAccountOAuthRequest](mux, delidevv1connect.AccountServiceCompleteAccountOAuthProcedure, &pb.CompleteAccountOAuthResponse{}, calls, outcome)
					identityProbe[pb.CancelAccountOAuthRequest](mux, delidevv1connect.AccountServiceCancelAccountOAuthProcedure, &pb.CancelAccountOAuthResponse{}, calls, outcome)
					identityProbe[pb.RefreshPullRequestProblemsRequest](mux, delidevv1connect.IntegrationServiceRefreshPullRequestProblemsProcedure, &pb.RefreshPullRequestProblemsResponse{}, calls, outcome)
					identityProbe[pb.DismissPullRequestProblemRequest](mux, delidevv1connect.IntegrationServiceDismissPullRequestProblemProcedure, &pb.DismissPullRequestProblemResponse{}, calls, outcome)
					identityProbe[pb.ResumePullRequestRemediationRequest](mux, delidevv1connect.IntegrationServiceResumePullRequestRemediationProcedure, &pb.ResumePullRequestRemediationResponse{}, calls, outcome)
					identityProbe[pb.RequestPullRequestFixRequest](mux, delidevv1connect.PullRequestFixServiceRequestPullRequestFixProcedure, &pb.RequestPullRequestFixResponse{}, calls, outcome)
					identityProbe[pb.DiscoverNativeModelsRequest](mux, delidevv1connect.NativeModelServiceDiscoverNativeModelsProcedure, &pb.DiscoverNativeModelsResponse{}, calls, outcome)
					identityProbe[pb.CancelNativeModelDiscoveryRequest](mux, delidevv1connect.NativeModelServiceCancelNativeModelDiscoveryProcedure, &pb.CancelNativeModelDiscoveryResponse{}, calls, outcome)
					identityProbe[pb.CheckUpdateRequest](mux, delidevv1connect.InstallationServiceCheckUpdateProcedure, &pb.CheckUpdateResponse{}, calls, outcome, echoCheckUpdate)
					identityProbe[pb.RequestWorkerUpdateRequest](mux, delidevv1connect.InstallationServiceRequestWorkerUpdateProcedure, &pb.RequestWorkerUpdateResponse{}, calls, outcome, echoWorkerUpdate)
					identityProbe[pb.CancelUpdateRequest](mux, delidevv1connect.InstallationServiceCancelUpdateProcedure, &pb.CancelUpdateResponse{}, calls, outcome, echoCancelUpdate)
					peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if outcome != identityLost || r.URL.Path == delidevv1connect.SystemServiceGetStatusProcedure {
							mux.ServeHTTP(w, r)
							return
						}
						mux.ServeHTTP(httptest.NewRecorder(), r)
						connection, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = connection.Close()
					}))
					defer peer.Close()
					root := identityClientScope(t, peer.URL, domain.ClientDevice)
					args := append([]string(nil), command.args...)
					supplied := string(domain.NewID())
					if explicit {
						args = append(args, "--request-id", supplied)
					}
					code, envelope := cliRun(t, root, args, command.input)
					var id string
					select {
					case id = <-calls:
					default:
						t.Fatalf("mutation not dispatched: %d %v", code, envelope)
					}
					if err := domain.ID(id).Validate(); err != nil {
						t.Fatal("missing UUID-v7", id, err)
					}
					if envelope["request_id"] != id || explicit && id != supplied {
						t.Fatalf("lost original identity: RPC=%s envelope=%v", id, envelope)
					}
					if len(calls) != 0 {
						t.Fatal("mutation automatically repeated")
					}
					if outcome == identitySuccess && command.success && code != 0 {
						t.Fatalf("successful reply rejected: %d %v", code, envelope)
					}
					if outcome != identitySuccess && code == 0 {
						t.Fatal("failed/uncertain mutation reported success")
					}
				})
			}
		}
	}
}

func TestCLIOAuthRecoveryRequiresOriginalExplicitIdentityBeforeDispatch(t *testing.T) {
	calls := make(chan string, 1)
	mux := http.NewServeMux()
	identityProbe[pb.CompleteAccountOAuthRequest](mux, delidevv1connect.AccountServiceCompleteAccountOAuthProcedure, &pb.CompleteAccountOAuthResponse{}, calls, identitySuccess)
	peer := httptest.NewServer(mux)
	defer peer.Close()
	root := identityClientScope(t, peer.URL, domain.ClientDevice)
	code, envelope := cliRun(t, root, []string{"account", "oauth", "complete", "--attempt-id", string(domain.NewID()), "--revision", "1", "--recover"}, "")
	if code != 2 || envelope["error"].(map[string]any)["code"] != "missing_input" || envelope["request_id"] != nil || len(calls) != 0 {
		t.Fatalf("recovery invented request authority: %d %v calls=%d", code, envelope, len(calls))
	}
}

func TestCLILocalServiceMutationRetainsOuterAndNestedIdentity(t *testing.T) {
	for _, kind := range []string{"server", "worker"} {
		for _, explicit := range []bool{false, true} {
			t.Run(kind+map[bool]string{true: "/explicit", false: "/generated"}[explicit], func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "state")
				if err := security.PrivateDir(root); err != nil {
					t.Fatal(err)
				}
				args := []string{kind, "service", "stop", "--revision", "99"}
				if kind == "server" {
					if _, err := security.CreateIdentity(root); err != nil {
						t.Fatal(err)
					}
				} else {
					scope := identityClientScope(t, "http://127.0.0.1:12345", domain.WorkerDevice)
					args = append(args, "--worker-dir", scope)
				}
				supplied := string(domain.NewID())
				if explicit {
					args = append(args, "--request-id", supplied)
				}
				code, envelope := cliRun(t, root, args, "")
				if code != 5 || envelope["error"].(map[string]any)["code"] != "conflict" {
					t.Fatalf("fixture reached native service work: %d %v", code, envelope)
				}
				id, _ := envelope["request_id"].(string)
				if domain.ID(id).Validate() != nil || explicit && id != supplied || envelope["result"].(map[string]any)["request_id"] != id {
					t.Fatalf("service lost identity: %v", envelope)
				}
			})
		}
	}
}

// Read-only failures must not acquire mutation authority merely by entering a
// helper that also handles writes.
func TestCLIReadDispatchDoesNotGenerateMutationIdentity(t *testing.T) {
	id := string(domain.NewID())
	commands := [][]string{
		{"account", "oauth", "status", "--attempt-id", id},
		{"github", "pr", "problems", "list", "--remote-repository-id", "37", "--pull-request-id", "53"},
		{"github", "pr", "remediation", "list", "--remote-repository-id", "37", "--pull-request-id", "53"},
		{"github", "pr", "remediation", "capabilities"},
		{"model", "native-observation", "--id", id},
		{"model", "native-list", "--id", id},
		{"update", "get", "--id", id},
	}
	for _, command := range commands {
		t.Run(strings.Join(command[:2], "-")+"-"+command[2], func(t *testing.T) {
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "Fixture unavailable", http.StatusServiceUnavailable)
			}))
			defer peer.Close()
			root := identityClientScope(t, peer.URL, domain.ClientDevice)
			var output, diagnostic strings.Builder
			code := Run(context.Background(), append([]string{"--data-dir", root}, command...), IO{In: strings.NewReader(""), Out: &output, Err: &diagnostic})
			var envelope map[string]any
			if json.Unmarshal([]byte(output.String()), &envelope) != nil || code == 0 || envelope["request_id"] != nil {
				t.Fatal("read-only failure acquired mutation identity", code, output.String(), diagnostic.String())
			}
		})
	}
}

func TestCLIUpdateMutationRejectsUnacknowledgedReceiptIDs(t *testing.T) {
	resource := string(domain.NewID())
	foreign := string(domain.NewID())
	commands := []struct {
		name string
		args []string
		// register installs the update probe with a receipt chosen by the vector.
		register func(mux *http.ServeMux, calls chan string, receipt func(string) string)
	}{
		{"update-check", []string{"update", "check", "--component", "desktop", "--target", "darwin-arm64", "--current-version", "0.0.1"}, func(mux *http.ServeMux, calls chan string, receipt func(string) string) {
			identityProbe[pb.CheckUpdateRequest](mux, delidevv1connect.InstallationServiceCheckUpdateProcedure, &pb.CheckUpdateResponse{}, calls, identitySuccess, func(r *pb.CheckUpdateRequest) *pb.CheckUpdateResponse {
				return &pb.CheckUpdateResponse{RequestId: receipt(r.RequestId)}
			})
		}},
		{"update-worker", []string{"update", "worker-request", "--id", resource, "--revision", "1"}, func(mux *http.ServeMux, calls chan string, receipt func(string) string) {
			identityProbe[pb.RequestWorkerUpdateRequest](mux, delidevv1connect.InstallationServiceRequestWorkerUpdateProcedure, &pb.RequestWorkerUpdateResponse{}, calls, identitySuccess, func(r *pb.RequestWorkerUpdateRequest) *pb.RequestWorkerUpdateResponse {
				return &pb.RequestWorkerUpdateResponse{RequestId: receipt(r.GetMutation().GetRequestId())}
			})
		}},
		{"update-cancel", []string{"update", "cancel", "--id", resource, "--revision", "1"}, func(mux *http.ServeMux, calls chan string, receipt func(string) string) {
			identityProbe[pb.CancelUpdateRequest](mux, delidevv1connect.InstallationServiceCancelUpdateProcedure, &pb.CancelUpdateResponse{}, calls, identitySuccess, func(r *pb.CancelUpdateRequest) *pb.CancelUpdateResponse {
				return &pb.CancelUpdateResponse{RequestId: receipt(r.GetMutation().GetRequestId())}
			})
		}},
	}
	vectors := []struct {
		name    string
		receipt func(sent string) string
		accept  bool
	}{
		{"matching", func(sent string) string { return sent }, true},
		{"missing", func(string) string { return "" }, false},
		{"malformed", func(string) string { return "not-a-request-id" }, false},
		{"foreign", func(string) string { return foreign }, false},
		{"uppercase-variant", func(sent string) string { return strings.ToUpper(sent) }, false},
	}
	for _, command := range commands {
		for _, explicit := range []bool{false, true} {
			for _, vector := range vectors {
				t.Run(command.name+"/"+map[bool]string{true: "explicit", false: "generated"}[explicit]+"/"+vector.name, func(t *testing.T) {
					calls := make(chan string, 3)
					mux := http.NewServeMux()
					mux.Handle(delidevv1connect.SystemServiceGetStatusProcedure, connect.NewUnaryHandler(delidevv1connect.SystemServiceGetStatusProcedure, func(context.Context, *connect.Request[pb.GetStatusRequest]) (*connect.Response[pb.GetStatusResponse], error) {
						return connect.NewResponse(&pb.GetStatusResponse{Capabilities: []pb.SystemCapability{pb.SystemCapability_SYSTEM_CAPABILITY_SIGNED_UPDATES_V1}}), nil
					}))
					command.register(mux, calls, vector.receipt)
					peer := httptest.NewServer(mux)
					defer peer.Close()
					root := identityClientScope(t, peer.URL, domain.ClientDevice)
					args := append([]string(nil), command.args...)
					supplied := string(domain.NewID())
					if explicit {
						args = append(args, "--request-id", supplied)
					}
					code, envelope := cliRun(t, root, args, "")
					var sent string
					select {
					case sent = <-calls:
					default:
						t.Fatalf("mutation not dispatched: %d %v", code, envelope)
					}
					if explicit && sent != supplied {
						t.Fatalf("explicit identity changed: RPC=%s supplied=%s", sent, supplied)
					}
					if len(calls) != 0 {
						t.Fatal("mutation automatically repeated")
					}
					if envelope["request_id"] != sent {
						t.Fatalf("envelope lost the sent identity: RPC=%s envelope=%v", sent, envelope)
					}
					if vector.accept {
						if code != 0 || envelope["result"] == nil {
							t.Fatalf("matching receipt rejected: %d %v", code, envelope)
						}
						return
					}
					failure, _ := envelope["error"].(map[string]any)
					if code == 0 || envelope["result"] != nil || failure == nil || failure["code"] != string(domain.RecoveryRequired) {
						t.Fatalf("unacknowledged receipt not closed as recovery-required: %d %v", code, envelope)
					}
				})
			}
		}
	}
}

// invalidInputPeer records every RPC path so tests can prove that locally
// rejected commands never reach the server beyond the capability read.
func invalidInputPeer(t *testing.T, capabilities []pb.SystemCapability, statusFails bool) (*httptest.Server, func() []string) {
	t.Helper()
	var mutex sync.Mutex
	var paths []string
	mux := http.NewServeMux()
	mux.Handle(delidevv1connect.SystemServiceGetStatusProcedure, connect.NewUnaryHandler(delidevv1connect.SystemServiceGetStatusProcedure, func(context.Context, *connect.Request[pb.GetStatusRequest]) (*connect.Response[pb.GetStatusResponse], error) {
		if statusFails {
			return nil, rpc.Error(domain.Fail(domain.Unsupported, "Fixture status unavailable.", "Retry."), "")
		}
		return connect.NewResponse(&pb.GetStatusResponse{Capabilities: capabilities}), nil
	}))
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != delidevv1connect.SystemServiceGetStatusProcedure {
			mutex.Lock()
			paths = append(paths, r.URL.Path)
			mutex.Unlock()
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(peer.Close)
	return peer, func() []string {
		mutex.Lock()
		defer mutex.Unlock()
		return append([]string(nil), paths...)
	}
}

func TestCLIUpdateLocalRejectionsAllocateNoMutationIdentity(t *testing.T) {
	resource := string(domain.NewID())
	signed := []pb.SystemCapability{pb.SystemCapability_SYSTEM_CAPABILITY_SIGNED_UPDATES_V1}
	validCheck := []string{"update", "check", "--component", "desktop", "--target", "darwin-arm64", "--current-version", "0.0.1"}
	cases := []struct {
		name         string
		args         []string
		capabilities []pb.SystemCapability
		statusFails  bool
	}{
		{"check/unknown-flag", append(append([]string(nil), validCheck...), "--bogus"), signed, false},
		{"check/bad-component", []string{"update", "check", "--component", "mobile", "--target", "darwin-arm64"}, signed, false},
		{"check/forbidden-id", append(append([]string(nil), validCheck...), "--id", resource), signed, false},
		{"worker-request/unknown-flag", []string{"update", "worker-request", "--id", resource, "--revision", "1", "--bogus"}, signed, false},
		{"cancel/unknown-flag", []string{"update", "cancel", "--id", resource, "--revision", "1", "--bogus"}, signed, false},
		{"bad-subcommand", []string{"update", "bogus"}, signed, false},
		{"check/status-error", validCheck, nil, true},
		{"worker-request/status-error", []string{"update", "worker-request", "--id", resource, "--revision", "1"}, nil, true},
		{"cancel/status-error", []string{"update", "cancel", "--id", resource, "--revision", "1"}, nil, true},
		{"check/missing-capability", validCheck, nil, false},
		{"worker-request/missing-capability", []string{"update", "worker-request", "--id", resource, "--revision", "1"}, nil, false},
		{"cancel/missing-capability", []string{"update", "cancel", "--id", resource, "--revision", "1"}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			peer, observed := invalidInputPeer(t, tc.capabilities, tc.statusFails)
			root := identityClientScope(t, peer.URL, domain.ClientDevice)
			code, envelope := cliRun(t, root, tc.args, "")
			if code == 0 || envelope["error"] == nil || envelope["request_id"] != nil {
				t.Fatalf("local rejection acquired mutation identity: %d %v", code, envelope)
			}
			if paths := observed(); len(paths) != 0 {
				t.Fatalf("rejected update reached mutation RPCs: %v", paths)
			}
		})
	}
}

func TestCLINativeModelLocalRejectionsAllocateNoMutationIdentity(t *testing.T) {
	resource := string(domain.NewID())
	for _, args := range [][]string{
		{"model", "native-discover", "--machine-id", resource, "--revision", "1", "--bogus"},
		{"model", "native-cancel", "--id", resource, "--revision", "1", "--bogus"},
		{"model", "native-discover", "--machine-id", resource, "extra"},
		{"model", "native-cancel", "--id", resource, "extra"},
	} {
		t.Run(strings.Join(args[1:3], "-")+"-"+args[len(args)-1], func(t *testing.T) {
			peer, observed := invalidInputPeer(t, nil, false)
			root := identityClientScope(t, peer.URL, domain.ClientDevice)
			code, envelope := cliRun(t, root, args, "")
			if code != 2 || envelope["error"] == nil || envelope["request_id"] != nil {
				t.Fatalf("local rejection acquired mutation identity: %d %v", code, envelope)
			}
			if paths := observed(); len(paths) != 0 {
				t.Fatalf("rejected native model command reached mutation RPCs: %v", paths)
			}
		})
	}
}

func TestCLILocalServiceInvalidActionAllocatesNoMutationIdentity(t *testing.T) {
	for _, kind := range []string{"server", "worker"} {
		for _, explicit := range []bool{false, true} {
			t.Run(kind+map[bool]string{true: "/explicit", false: "/generated"}[explicit], func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "state")
				if err := security.PrivateDir(root); err != nil {
					t.Fatal(err)
				}
				args := []string{kind, "service", "bogus"}
				if kind == "server" {
					if _, err := security.CreateIdentity(root); err != nil {
						t.Fatal(err)
					}
				} else {
					args = append(args, "--worker-dir", identityClientScope(t, "http://127.0.0.1:12345", domain.WorkerDevice))
				}
				supplied := string(domain.NewID())
				if explicit {
					args = append(args, "--request-id", supplied)
				}
				code, envelope := cliRun(t, root, args, "")
				failure, _ := envelope["error"].(map[string]any)
				if code == 0 || failure == nil || failure["code"] != string(domain.InvalidArgument) || envelope["result"] != nil {
					t.Fatalf("invalid service action not rejected as invalid argument: %d %v", code, envelope)
				}
				if explicit && envelope["request_id"] != supplied {
					t.Fatalf("explicit identity changed: %v", envelope)
				}
				if !explicit && envelope["request_id"] != nil {
					t.Fatalf("invalid service action generated an identity: %v", envelope)
				}
				entries, err := os.ReadDir(root)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if strings.Contains(entry.Name(), "service") {
						t.Fatalf("invalid service action produced native manager state: %s", entry.Name())
					}
				}
			})
		}
	}
}

// Command admission already rejects unknown service words, but serviceCommand is
// the allocation authority and must validate the action before it allocates an
// identity or reaches the native manager.
func TestServiceCommandRejectsInvalidActionBeforeAllocatingIdentity(t *testing.T) {
	for _, kind := range []userservice.Kind{userservice.Server, userservice.Worker} {
		for _, explicit := range []bool{false, true} {
			t.Run(string(kind)+map[bool]string{true: "/explicit", false: "/generated"}[explicit], func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "state")
				if err := security.PrivateDir(root); err != nil {
					t.Fatal(err)
				}
				o := options{dataDir: root}
				args := []string{"bogus"}
				if kind == userservice.Server {
					if _, err := security.CreateIdentity(root); err != nil {
						t.Fatal(err)
					}
				} else {
					args = append(args, "--worker-dir", identityClientScope(t, "http://127.0.0.1:12345", domain.WorkerDevice))
				}
				supplied := domain.NewID()
				if explicit {
					o.requestID = supplied
				}
				var diagnostics strings.Builder
				value, err := serviceCommand(context.Background(), &o, kind, args, IO{Err: &diagnostics})
				failure := domain.SafeError(err)
				if value != nil || err == nil || failure.Code != domain.InvalidArgument {
					t.Fatalf("invalid action not rejected as invalid argument: %v %v", value, err)
				}
				if explicit && o.requestID != supplied || !explicit && o.requestID != "" {
					t.Fatalf("invalid action changed request identity: %q", o.requestID)
				}
				entries, err := os.ReadDir(root)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if strings.Contains(entry.Name(), "service") {
						t.Fatalf("invalid action produced native manager state: %s", entry.Name())
					}
				}
			})
		}
	}
}
