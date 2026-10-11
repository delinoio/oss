// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type auxiliaryStreamStep struct {
	message *pb.WatchAuxiliaryWorkResponse
	problem error
	end     bool
}

type auxiliaryStreamFixture struct {
	delidevv1connect.UnimplementedWorkerServiceHandler
	job   *pb.Resource
	steps chan auxiliaryStreamStep
}

func (f *auxiliaryStreamFixture) WatchAuxiliaryWork(ctx context.Context, _ *connect.Request[pb.WatchAuxiliaryWorkRequest], stream *connect.ServerStream[pb.WatchAuxiliaryWorkResponse]) error {
	if err := stream.Send(&pb.WatchAuxiliaryWorkResponse{Job: f.job}); err != nil {
		return err
	}
	for {
		select {
		case step := <-f.steps:
			if step.end {
				return step.problem
			}
			if err := stream.Send(step.message); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Hold the original registration call after its context is canceled. This
// exercises the real job consumer and private-runtime defer without launching
// Codex; returning from the call releases the simulated cleanup boundary.
type auxiliaryJobFixture struct {
	delidevv1connect.WorkerServiceClient
	started, canceled, release, reported chan struct{}
	registrations, reports               atomic.Int32
	output                               []byte
}

func (f *auxiliaryJobFixture) RegisterExecution(ctx context.Context, _ *connect.Request[pb.RegisterExecutionRequest]) (*connect.Response[pb.RegisterExecutionResponse], error) {
	f.registrations.Add(1)
	close(f.started)
	<-ctx.Done()
	close(f.canceled)
	<-f.release
	return nil, connect.NewError(connect.CodeCanceled, ctx.Err())
}

func (f *auxiliaryJobFixture) ReportWork(_ context.Context, req *connect.Request[pb.ReportWorkRequest]) (*connect.Response[pb.ReportWorkResponse], error) {
	f.reports.Add(1)
	f.output = append([]byte(nil), req.Msg.OutputJson...)
	close(f.reported)
	return connect.NewResponse(&pb.ReportWorkResponse{}), nil
}

type auxiliaryTestRun struct {
	root    string
	jobID   domain.ID
	service *auxiliaryStreamFixture
	client  *auxiliaryJobFixture
	done    chan error
	cancel  context.CancelFunc
	release func()
}

func startAuxiliaryTest(t *testing.T, completed bool) *auxiliaryTestRun {
	t.Helper()
	root := t.TempDir()
	if err := security.PrivateDir(filepath.Join(root, "jobs")); err != nil {
		t.Fatal(err)
	}
	machine, instance, device, jobID := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	provider := domain.NewID()
	input := domain.AuxiliaryTitleInput{
		Version: 1, SessionID: domain.NewID(), OperationID: domain.NewID(), NameGeneration: 1,
		OriginalJobID: domain.NewID(), OriginalExecutionID: domain.NewID(), MachineID: machine,
		OriginalDeviceID: device, OriginalInstanceID: instance, AgentID: domain.NewID(),
		Harness: domain.Codex, NativeVersion: domain.CodexProtocolVersion, Executable: executable,
		AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: provider,
		ProviderProtocol: domain.OpenAIResponses, NativeModel: "fixture-model", Prompt: "private fixture input",
	}
	input.ModelID = (domain.ModelIdentity{ProviderID: provider, NativeID: input.NativeModel}).Key()
	if err := input.Validate(); err != nil {
		t.Fatal(err)
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	job := domain.Job{Type: domain.GenerateSessionTitleJob, State: domain.JobClaimed, MachineID: machine, InstanceID: instance, AssignedDeviceID: device, ParentID: input.OriginalJobID, Input: inputJSON, AcceptedAt: time.Now().UTC()}
	jobJSON, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	resource := &pb.Resource{Id: string(jobID), SessionId: string(input.SessionID), Kind: pb.EntityKind_ENTITY_KIND_JOB, Revision: 3, SchemaVersion: 1, DocumentJson: jobJSON}
	if completed {
		digest := sha256.Sum256(jobJSON)
		output, err := json.Marshal(domain.AuxiliaryTitleResult{Version: 1, OperationID: input.OperationID, NameGeneration: 1, Title: "Retained fixture title", CleanupVerified: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := writeJSON(filepath.Join(root, "jobs", string(jobID)+".json"), journal{Version: 1, JobID: jobID, InstanceID: instance, Revision: resource.Revision, Digest: hex.EncodeToString(digest[:]), State: journalFinished, ReportID: domain.NewID(), Output: output}); err != nil {
			t.Fatal(err)
		}
	}
	service := &auxiliaryStreamFixture{job: resource, steps: make(chan auxiliaryStreamStep, 4)}
	_, handler := delidevv1connect.NewWorkerServiceHandler(service)
	server := httptest.NewServer(handler)
	client := &auxiliaryJobFixture{WorkerServiceClient: delidevv1connect.NewWorkerServiceClient(server.Client(), server.URL), started: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{}), reported: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(client.release) }) }
	done, joined := make(chan error, 1), make(chan struct{})
	t.Cleanup(func() {
		cancel()
		release()
		<-joined
		server.Close()
	})
	go func() {
		defer close(joined)
		done <- watchAuxiliary(ctx, Config{Root: root, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, client, Credential{Token: "fixture-token", MachineID: machine, DeviceID: device}, instance)
	}()
	return &auxiliaryTestRun{root: root, jobID: jobID, service: service, client: client, done: done, cancel: cancel, release: release}
}

func awaitAuxiliarySignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s before the heartbeat deadline", description)
	}
}

func TestAuxiliaryStreamTerminationCancelsAndJoinsActiveTitle(t *testing.T) {
	for _, code := range []connect.Code{0, connect.CodePermissionDenied, connect.CodeUnavailable} {
		t.Run(code.String(), func(t *testing.T) {
			run := startAuxiliaryTest(t, false)
			awaitAuxiliarySignal(t, run.client.started, "original title registration")
			var problem error
			if code != 0 {
				problem = connect.NewError(code, nil)
			}
			run.service.steps <- auxiliaryStreamStep{end: true, problem: problem}
			awaitAuxiliarySignal(t, run.client.canceled, "active title cancellation")
			select {
			case err := <-run.done:
				t.Fatalf("watch returned before the original job joined: %v", err)
			default:
			}
			entries, err := os.ReadDir(filepath.Join(run.root, "title-runtimes"))
			if err != nil || len(entries) != 1 {
				t.Fatalf("active private runtime disappeared before its owner returned: %v", err)
			}
			run.release()
			select {
			case err := <-run.done:
				if code == 0 {
					if domain.SafeError(err).Code != domain.Unavailable {
						t.Fatalf("clean EOF lost its safe cause: %v", err)
					}
				} else if connect.CodeOf(err) != code {
					t.Fatalf("stream failure lost its original code: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("watch did not return after original job cleanup")
			}
			entries, err = os.ReadDir(filepath.Join(run.root, "title-runtimes"))
			if err != nil || len(entries) != 0 || run.client.reports.Load() != 0 || run.client.registrations.Load() != 1 {
				t.Fatalf("stream loss did not retain one attempt and join cleanup without reporting: entries=%d reports=%d registrations=%d error=%v", len(entries), run.client.reports.Load(), run.client.registrations.Load(), err)
			}
		})
	}
}

func TestAuxiliaryExactCancelKeepsWatchAlive(t *testing.T) {
	run := startAuxiliaryTest(t, false)
	awaitAuxiliarySignal(t, run.client.started, "original title registration")
	run.service.steps <- auxiliaryStreamStep{message: &pb.WatchAuxiliaryWorkResponse{Heartbeat: true}}
	run.service.steps <- auxiliaryStreamStep{message: &pb.WatchAuxiliaryWorkResponse{CancelJobId: string(run.jobID)}}
	awaitAuxiliarySignal(t, run.client.canceled, "exact title cancellation")
	run.release()
	awaitAuxiliarySignal(t, run.client.reported, "canceled title result")
	select {
	case err := <-run.done:
		t.Fatalf("exact job cancellation ended the healthy watch: %v", err)
	default:
	}
	if run.client.reports.Load() != 1 || len(run.client.output) != 0 {
		t.Fatal("exact cancellation reported successful title output")
	}
}

func TestAuxiliaryReportsRetainedCompletedTitleOnce(t *testing.T) {
	run := startAuxiliaryTest(t, true)
	awaitAuxiliarySignal(t, run.client.reported, "original retained result")
	var output domain.AuxiliaryTitleResult
	if json.Unmarshal(run.client.output, &output) != nil || output.Title != "Retained fixture title" || !output.CleanupVerified || run.client.registrations.Load() != 0 || run.client.reports.Load() != 1 {
		t.Fatal("healthy watch did not report the original completed title exactly once")
	}
	select {
	case err := <-run.done:
		t.Fatalf("successful title ended the healthy watch: %v", err)
	default:
	}
}
