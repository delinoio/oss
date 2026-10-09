// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type deadlineCommand uint8

const (
	deadlineOAuth deadlineCommand = iota
	deadlineUpdate
	deadlineValidation
	deadlineDiscovery
)

type deadlineCall struct {
	id        string
	remaining time.Duration
}

func (command deadlineCommand) arguments() ([]string, string) {
	id := string(domain.NewID())
	switch command {
	case deadlineOAuth:
		return []string{"account", "oauth", "complete", "--attempt-id", id, "--revision", "1", "--code-stdin"}, "fixture-code"
	case deadlineUpdate:
		return []string{"update", "check", "--component", "desktop", "--target", "darwin-arm64", "--current-version", "0.0.1"}, ""
	case deadlineValidation:
		return []string{"account", "validate", "--id", id, "--revision", "1"}, ""
	default:
		return []string{"provider", "discover", "--account-id", id, "--revision", "1"}, ""
	}
}
func (command deadlineCommand) budget() time.Duration {
	if command == deadlineOAuth || command == deadlineUpdate {
		return 35 * time.Second
	}
	return 50 * time.Second
}

func deadlineProbe[I, O any](mux *http.ServeMux, procedure string, delay time.Duration, problem *domain.Error, calls chan deadlineCall, reply func(*I, string) *O) {
	mux.Handle(procedure, connect.NewUnaryHandler(procedure, func(ctx context.Context, req *connect.Request[I]) (*connect.Response[O], error) {
		id := wireMutationIdentity(req.Msg)
		deadline, _ := ctx.Deadline()
		calls <- deadlineCall{id: id, remaining: time.Until(deadline)}
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
		if problem != nil {
			return nil, rpc.Error(problem, "")
		}
		return connect.NewResponse(reply(req.Msg, id)), nil
	}))
}
func deadlineHTTPFixture(t *testing.T, delay time.Duration, problem *domain.Error) (string, chan deadlineCall) {
	t.Helper()
	mux := http.NewServeMux()
	calls := make(chan deadlineCall, 4)
	mux.Handle(delidevv1connect.SystemServiceGetStatusProcedure, connect.NewUnaryHandler(delidevv1connect.SystemServiceGetStatusProcedure, func(context.Context, *connect.Request[pb.GetStatusRequest]) (*connect.Response[pb.GetStatusResponse], error) {
		return connect.NewResponse(&pb.GetStatusResponse{Capabilities: []pb.SystemCapability{pb.SystemCapability_SYSTEM_CAPABILITY_SIGNED_UPDATES_V1}}), nil
	}))
	deadlineProbe(mux, delidevv1connect.AccountServiceCompleteAccountOAuthProcedure, delay, problem, calls, func(r *pb.CompleteAccountOAuthRequest, id string) *pb.CompleteAccountOAuthResponse {
		return &pb.CompleteAccountOAuthResponse{RequestId: id}
	})
	deadlineProbe(mux, delidevv1connect.InstallationServiceCheckUpdateProcedure, delay, problem, calls, func(r *pb.CheckUpdateRequest, id string) *pb.CheckUpdateResponse { return &pb.CheckUpdateResponse{} })
	deadlineProbe(mux, delidevv1connect.AccountServiceValidateAccountProcedure, delay, problem, calls, func(r *pb.ValidateAccountRequest, id string) *pb.ValidateAccountResponse {
		return &pb.ValidateAccountResponse{ValidationJson: []byte(`{}`)}
	})
	deadlineProbe(mux, delidevv1connect.ProviderServiceDiscoverModelsProcedure, delay, problem, calls, func(r *pb.DiscoverModelsRequest, id string) *pb.DiscoverModelsResponse {
		return &pb.DiscoverModelsResponse{ObservationJson: []byte(`{}`)}
	})
	deadlineProbe(mux, delidevv1connect.InstallationServiceGetUpdateProcedure, delay, problem, calls, func(r *pb.GetUpdateRequest, id string) *pb.GetUpdateResponse { return &pb.GetUpdateResponse{} })
	deadlineProbe(mux, delidevv1connect.ProviderServiceListProviderInventoryProcedure, delay, problem, calls, func(r *pb.ListProviderInventoryRequest, id string) *pb.ListProviderInventoryResponse {
		return &pb.ListProviderInventoryResponse{}
	})
	peer := httptest.NewServer(mux)
	t.Cleanup(peer.Close)
	return identityClientScope(t, peer.URL, domain.ClientDevice), calls
}
func runDeadlineCommand(t *testing.T, ctx context.Context, root string, args []string, input string) (int, map[string]any) {
	t.Helper()
	var out, stderr bytes.Buffer
	code := Run(ctx, append([]string{"--data-dir", root}, args...), IO{In: strings.NewReader(input), Out: &out, Err: &stderr})
	var envelope map[string]any
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal("invalid envelope", err)
	}
	return code, envelope
}
func assertDeadlineIdentity(t *testing.T, calls chan deadlineCall, envelope map[string]any, explicit string, budget time.Duration) {
	t.Helper()
	var call deadlineCall
	select {
	case call = <-calls:
	default:
		t.Fatal("original mutation not dispatched")
	}
	if domain.ID(call.id).Validate() != nil || envelope["request_id"] != call.id || explicit != "" && call.id != explicit {
		t.Fatal("original mutation identity changed", call.id, envelope)
	}
	if call.remaining < budget-2*time.Second || call.remaining > budget {
		t.Fatalf("outer server deadline %s does not match %s", call.remaining, budget)
	}
	select {
	case <-calls:
		t.Fatal("mutation automatically repeated")
	default:
	}
}

func TestCLIUnaryDeadlineDelayedHTTPResponseRetainsOriginal(t *testing.T) {
	for _, command := range []deadlineCommand{deadlineOAuth, deadlineUpdate, deadlineValidation, deadlineDiscovery} {
		for _, explicit := range []bool{false, true} {
			t.Run(string(rune('A'+command))+map[bool]string{false: "/generated", true: "/explicit"}[explicit], func(t *testing.T) {
				t.Parallel()
				root, calls := deadlineHTTPFixture(t, 16*time.Second, nil)
				args, input := command.arguments()
				var supplied string
				if explicit {
					supplied = string(domain.NewID())
					args = append(args, "--request-id", supplied)
				}
				code, envelope := runDeadlineCommand(t, context.Background(), root, args, input)
				if code != 0 {
					t.Fatal("valid server response abandoned", code, envelope)
				}
				assertDeadlineIdentity(t, calls, envelope, supplied, command.budget())
			})
		}
	}
}

func TestCLIUnaryDeadlineComposedInspectionAndTypedSettlement(t *testing.T) {
	for _, command := range []deadlineCommand{deadlineValidation, deadlineDiscovery, deadlineOAuth, deadlineUpdate} {
		t.Run(string(rune('A'+command)), func(t *testing.T) {
			t.Parallel()
			delay := 32 * time.Second
			if command == deadlineOAuth {
				delay = 24 * time.Second
			}
			if command == deadlineUpdate {
				delay = 29 * time.Second
			}
			problem := domain.Fail(domain.RecoveryRequired, "Original fixture work needs reconciliation.", "Retain only the original request.")
			root, calls := deadlineHTTPFixture(t, delay, problem)
			args, input := command.arguments()
			code, envelope := runDeadlineCommand(t, context.Background(), root, args, input)
			failure, _ := envelope["error"].(map[string]any)
			if code == 0 || failure["code"] != string(domain.RecoveryRequired) {
				t.Fatal("typed bounded outcome lost", code, envelope)
			}
			assertDeadlineIdentity(t, calls, envelope, "", command.budget())
		})
	}
}

func TestCLIUnaryDeadlineEarlierCallerWinsWithoutRetry(t *testing.T) {
	for _, command := range []deadlineCommand{deadlineOAuth, deadlineUpdate, deadlineValidation, deadlineDiscovery} {
		t.Run(string(rune('A'+command)), func(t *testing.T) {
			root, calls := deadlineHTTPFixture(t, time.Minute, nil)
			args, input := command.arguments()
			supplied := string(domain.NewID())
			args = append(args, "--request-id", supplied)
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()
			started := time.Now()
			code, envelope := runDeadlineCommand(t, ctx, root, args, input)
			if code == 0 || time.Since(started) > 2*time.Second {
				t.Fatal("shorter caller did not win", code, envelope)
			}
			var call deadlineCall
			select {
			case call = <-calls:
			default:
				t.Fatal("original operation was not captured")
			}
			if call.id != supplied || envelope["request_id"] != supplied || call.remaining > 250*time.Millisecond {
				t.Fatal("caller cancellation replaced original claim", call, envelope)
			}
			select {
			case <-calls:
				t.Fatal("canceled mutation retried")
			default:
			}
		})
	}
}

func TestCLIUnaryDeadlineUpdateRemainsBounded(t *testing.T) {
	t.Parallel()
	root, calls := deadlineHTTPFixture(t, 40*time.Second, nil)
	args, input := deadlineUpdate.arguments()
	started := time.Now()
	code, envelope := runDeadlineCommand(t, context.Background(), root, args, input)
	elapsed := time.Since(started)
	if code == 0 || elapsed < 34*time.Second || elapsed > 39*time.Second {
		t.Fatal("check lost its35second bound", elapsed, code, envelope)
	}
	assertDeadlineIdentity(t, calls, envelope, "", 35*time.Second)
}

func TestCLIUnaryDeadlineOrdinaryReadKeepsHeaderAndOuterLimits(t *testing.T) {
	for _, args := range [][]string{{"update", "get", "--id", string(domain.NewID())}, {"provider", "inventory"}} {
		t.Run(strings.Join(args[:2], "-"), func(t *testing.T) {
			t.Parallel()
			root, calls := deadlineHTTPFixture(t, 16*time.Second, nil)
			started := time.Now()
			code, envelope := runDeadlineCommand(t, context.Background(), root, args, "")
			elapsed := time.Since(started)
			if code == 0 || envelope["request_id"] != nil || elapsed < 14*time.Second || elapsed > 19*time.Second {
				t.Fatal("ordinary15second header wait changed", elapsed, code, envelope)
			}
			var call deadlineCall
			select {
			case call = <-calls:
			default:
				t.Fatal("read request not captured")
			}
			if call.id != "" || call.remaining < 28*time.Second || call.remaining > 30*time.Second {
				t.Fatal("ordinary30second outer budget/identity changed", call)
			}
			select {
			case <-calls:
				t.Fatal("read retried")
			default:
			}
		})
	}
}
