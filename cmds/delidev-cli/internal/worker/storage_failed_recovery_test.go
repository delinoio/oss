// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type failedStorageRecoveryReport struct {
	delidevv1connect.UnimplementedWorkerServiceHandler
	ack    *pb.Resource
	result journal
}

func (s *failedStorageRecoveryReport) ReportWork(_ context.Context, req *connect.Request[pb.ReportWorkRequest]) (*connect.Response[pb.ReportWorkResponse], error) {
	problemMatches := req.Msg.Problem == nil && s.result.Problem == nil || req.Msg.Problem != nil && s.result.Problem != nil && req.Msg.Problem.Code == string(s.result.Problem.Code)
	if req.Msg.Mutation.RequestId != string(s.result.ReportID) || req.Msg.Mutation.Id != string(s.result.JobID) || req.Msg.Mutation.ExpectedRevision != s.result.Revision || !problemMatches {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("original failed report identity changed"))
	}
	return connect.NewResponse(&pb.ReportWorkResponse{Job: s.ack}), nil
}

func TestUnsuccessfulStorageRecoveryDiscardsOnlyReportReceipt(t *testing.T) {
	for _, state := range []domain.JobState{domain.JobFailed, domain.JobCanceled, domain.JobUncertain} {
		for _, replay := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/replay=%t", state, replay), func(t *testing.T) {
				root := t.TempDir()
				config := Config{Root: root, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
				machine, instance, session, original, snapshot, recovery := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
				input := workspace.StorageRequest{Version: 1, OperationID: recovery, Action: workspace.StorageRecover, Recovery: &workspace.StorageRecovery{Original: workspace.StorageRequest{OperationID: original, Action: workspace.StorageCleanup, SnapshotID: snapshot, Preparation: workspace.PrepareRequest{SessionID: session, MachineID: machine}}}}
				raw, _ := json.Marshal(input)
				job := domain.Job{Type: domain.WorkspaceStorageJob, State: domain.JobClaimed, MachineID: machine, InstanceID: instance, Input: raw, AcceptedAt: time.Now().UTC()}
				document, _ := json.Marshal(job)
				assigned := &pb.Resource{Id: string(recovery), SessionId: string(session), Kind: pb.EntityKind_ENTITY_KIND_JOB, SchemaVersion: 1, Revision: 2, DocumentJson: document}
				code := domain.ResourceExhausted
				if state == domain.JobCanceled {
					code = domain.Canceled
				}
				result := journal{Version: 1, JobID: recovery, InstanceID: instance, Revision: 2, Digest: strings.Repeat("a", 64), State: journalFinished, ReportID: domain.NewID(), Problem: domain.Fail(code, "The recovery did not settle the original operation.", "Retry the original uncertain operation.")}
				if state == domain.JobUncertain {
					// A locally clean report was accepted as uncertain before its reply
					// was lost. The server retained no output/removal authority.
					input.Action, input.OperationID, input.Recovery = workspace.StorageCleanup, original, nil
					input.Preparation = workspace.PrepareRequest{SessionID: session, MachineID: machine}
					input.SnapshotID = snapshot
					recovery = original
					raw, _ = json.Marshal(input)
					job.Input = raw
					assigned.Id = string(original)
					result.JobID, result.Problem, result.Output = original, nil, json.RawMessage(`{"malformed":true}`)
				}
				if err := security.PrivateDir(filepath.Join(root, "jobs")); err != nil {
					t.Fatal(err)
				}
				if err := writeJSON(filepath.Join(root, "jobs", string(recovery)+".json"), result); err != nil {
					t.Fatal(err)
				}
				intent := filepath.Join(root, "storage-removal-intents", string(original)+".json")
				if err := security.PrivateDir(filepath.Dir(intent)); err != nil {
					t.Fatal(err)
				}
				// This sentinel is retained predecessor evidence, never removal authority.
				if err := security.WriteAtomic(intent, []byte("retained predecessor evidence")); err != nil {
					t.Fatal(err)
				}
				if err := prepareStorageRetirement(config, job, result); err != nil {
					t.Fatal(err)
				}
				accepted := job
				accepted.State, accepted.Problem = state, result.Problem
				if state == domain.JobUncertain {
					accepted.Problem = workspace.ResultUncertain()
				}
				document, _ = json.Marshal(accepted)
				ack := &pb.Resource{Id: assigned.Id, SessionId: assigned.SessionId, Kind: assigned.Kind, SchemaVersion: 1, Revision: 3, DocumentJson: document}
				if replay {
					handler := &failedStorageRecoveryReport{ack: ack, result: result}
					_, h := delidevv1connect.NewWorkerServiceHandler(handler)
					server := httptest.NewServer(h)
					defer server.Close()
					client := delidevv1connect.NewWorkerServiceClient(server.Client(), server.URL)
					if err := replayPendingStorageReports(context.Background(), config, client, Credential{MachineID: machine}); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := acknowledgeStorageRemoval(context.Background(), config, assigned, job, result, ack); err != nil {
						t.Fatal(err)
					}
					result.State = journalReported
					if err := writeJSON(filepath.Join(root, "jobs", string(recovery)+".json"), result); err != nil {
						t.Fatal(err)
					}
				}
				if raw, err := os.ReadFile(intent); err != nil || string(raw) != "retained predecessor evidence" {
					t.Fatal("failed recovery retired original intent", err)
				}
				if _, err := os.Lstat(filepath.Join(root, "storage-removal-retirements", string(recovery)+".json")); !os.IsNotExist(err) {
					t.Fatal("failed recovery retained pending report receipt", err)
				}
				if err := replayPendingStorageReports(context.Background(), config, nil, Credential{}); err != nil {
					t.Fatal("reported failure blocked reconnect", err)
				}
				if err := retireStorageReports(context.Background(), config); err != nil {
					t.Fatal("startup could not preserve predecessor", err)
				}
			})
		}
	}
}
