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
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
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
func identityProbe[I, O any](mux *http.ServeMux, procedure string, reply *O, calls chan string, outcome identityOutcome) {
	mux.Handle(procedure, connect.NewUnaryHandler(procedure, func(ctx context.Context, req *connect.Request[I]) (*connect.Response[O], error) {
		id := wireMutationIdentity(req.Msg)
		calls <- id
		if err := domain.ID(id).Validate(); err != nil {
			return nil, rpc.Error(err, "")
		}
		if outcome == identityRejected {
			return nil, rpc.Error(domain.Fail(domain.Conflict, "Fixture revision changed.", "Retain the original request."), "")
		}
		return connect.NewResponse(reply), nil
	}))
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
					identityProbe[pb.CheckUpdateRequest](mux, delidevv1connect.InstallationServiceCheckUpdateProcedure, &pb.CheckUpdateResponse{}, calls, outcome)
					identityProbe[pb.RequestWorkerUpdateRequest](mux, delidevv1connect.InstallationServiceRequestWorkerUpdateProcedure, &pb.RequestWorkerUpdateResponse{}, calls, outcome)
					identityProbe[pb.CancelUpdateRequest](mux, delidevv1connect.InstallationServiceCancelUpdateProcedure, &pb.CancelUpdateResponse{}, calls, outcome)
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
