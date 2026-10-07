// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type startupReportFixture struct {
	delidevv1connect.WorkerServiceClient
	observation *pb.ExecutionStartupObservation
}

func (f *startupReportFixture) ReportExecutionStartup(_ context.Context, r *connect.Request[pb.ReportExecutionStartupRequest]) (*connect.Response[pb.ReportExecutionStartupResponse], error) {
	f.observation = r.Msg.Observation
	return connect.NewResponse(&pb.ReportExecutionStartupResponse{Observation: r.Msg.Observation}), nil
}

func TestDirectStartupFailurePreservesFirstErrorAndIndependentCleanup(t *testing.T) {
	for _, scenario := range []string{"before-input", "claimed-input", "acknowledged-input", "cleanup-uncertain"} {
		t.Run(scenario, func(t *testing.T) {
			client := &startupReportFixture{}
			job := domain.NewID()
			config := Config{Root: t.TempDir(), execution: &PublicationConfig{Assignment: &pb.Resource{Id: string(job), Revision: 3}, Client: client, Instance: domain.NewID()}}
			if err := prepareStartupProcessIndex(config.Root, job); err != nil {
				t.Fatal(err)
			}
			a := newExecutionStartupAttempt(config, job, domain.ExecutionJobInput{MachineID: domain.NewID(), Configuration: domain.ExecutionConfiguration{Harness: domain.Codex}})
			original := domain.Fail(domain.PermissionDenied, "Access denied.", "Review permissions.")
			returned := error(original)
			switch scenario {
			case "claimed-input":
				a.claimInput()
			case "acknowledged-input":
				a.claimInput()
				a.acknowledgeInput()
			case "cleanup-uncertain":
				returned = a.cleanupFailure(original, domain.Fail(domain.Unavailable, "Cleanup failed.", "Recover it."))
			}
			if a.finish(returned) == nil || client.observation == nil {
				t.Fatal("lost startup failure")
			}
			o := client.observation
			if o.ProblemCode != string(domain.PermissionDenied) || o.CorrelationId != string(job) {
				t.Fatal("cleanup replaced the first failure")
			}
			if (scenario == "before-input") != (o.State == pb.ExecutionStartupState_EXECUTION_STARTUP_STATE_FAILED) {
				t.Fatal("uncertainty granted retry")
			}
			if (scenario == "cleanup-uncertain") != (o.Cleanup == pb.ExecutionStartupCleanup_EXECUTION_STARTUP_CLEANUP_UNCERTAIN) {
				t.Fatal("delivery and cleanup were collapsed")
			}
		})
	}
}

func TestDirectStartupMissingExecutableReportsNoSendWithoutInspection(t *testing.T) {
	f := newCheckpointFixture(t)
	f.input.Version, f.input.Installation = 4, domain.Installation{}
	f.input.Startup = &domain.ExecutionStartupSelection{Harness: domain.Codex, ExplicitPath: filepath.Join(f.root, "not-installed")}
	raw, err := json.Marshal(f.input)
	if err != nil {
		t.Fatal(err)
	}
	f.job.Input = raw
	client := &startupReportFixture{}
	config := Config{Root: f.root, executionContext: context.Background(), execution: &PublicationConfig{Assignment: &pb.Resource{Id: string(f.jobID), Revision: 2}, Client: client, Instance: domain.NewID()}}
	_, err = executeSession(context.Background(), config, f.jobID, f.job)
	if domain.SafeError(err).Code != domain.NotFound || client.observation == nil || client.observation.State != pb.ExecutionStartupState_EXECUTION_STARTUP_STATE_FAILED || client.observation.InputDelivery != pb.ExecutionStartupInputDelivery_EXECUTION_STARTUP_INPUT_DELIVERY_NOT_SENT || client.observation.Cleanup != pb.ExecutionStartupCleanup_EXECUTION_STARTUP_CLEANUP_CONFIRMED || client.observation.NativeVersion != "" {
		t.Fatalf("missing executable failure was not proven: %v %#v", err, client.observation)
	}
}
