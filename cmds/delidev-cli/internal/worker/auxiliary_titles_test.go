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
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type auxiliaryWatchFixture struct {
	delidevv1connect.UnimplementedWorkerServiceHandler
	watch func(context.Context, *connect.ServerStream[pb.WatchAuxiliaryWorkResponse]) error
}

func (f *auxiliaryWatchFixture) WatchAuxiliaryWork(ctx context.Context, _ *connect.Request[pb.WatchAuxiliaryWorkRequest], stream *connect.ServerStream[pb.WatchAuxiliaryWorkResponse]) error {
	return f.watch(ctx, stream)
}

type auxiliaryJobFixture struct {
	delidevv1connect.WorkerServiceClient
	register func(context.Context) error
	reports  atomic.Int32
	reported chan struct{}
	resource *pb.Resource
}

func (f *auxiliaryJobFixture) RegisterExecution(ctx context.Context, _ *connect.Request[pb.RegisterExecutionRequest]) (*connect.Response[pb.RegisterExecutionResponse], error) {
	return nil, f.register(ctx)
}
func (f *auxiliaryJobFixture) ReportWork(_ context.Context, request *connect.Request[pb.ReportWorkRequest]) (*connect.Response[pb.ReportWorkResponse], error) {
	f.reports.Add(1)
	close(f.reported)
	return connect.NewResponse(&pb.ReportWorkResponse{Job: f.resource}), nil
}

func auxiliaryTitleResource(t *testing.T, credential Credential, instance domain.ID) *pb.Resource {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	provider := domain.NewID()
	input := domain.AuxiliaryTitleInput{Version: 1, SessionID: domain.NewID(), OperationID: domain.NewID(), NameGeneration: 1, OriginalJobID: domain.NewID(), OriginalExecutionID: domain.NewID(), MachineID: credential.MachineID, OriginalDeviceID: credential.DeviceID, OriginalInstanceID: instance, AgentID: domain.NewID(), Harness: domain.Codex, NativeVersion: domain.CodexProtocolVersion, Executable: executable, AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: provider, ProviderProtocol: domain.OpenAIResponses, NativeModel: "fixture-model", Prompt: "private fixture message"}
	input.ModelID = (domain.ModelIdentity{ProviderID: provider, NativeID: input.NativeModel}).Key()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	job := domain.Job{Type: domain.GenerateSessionTitleJob, State: domain.JobClaimed, MachineID: credential.MachineID, InstanceID: instance, AssignedDeviceID: credential.DeviceID, ParentID: input.OriginalJobID, Input: raw}
	raw, err = json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	return &pb.Resource{Id: string(domain.NewID()), SessionId: string(input.SessionID), Revision: 9, Kind: pb.EntityKind_ENTITY_KIND_JOB, SchemaVersion: 1, DocumentJson: raw}
}

func TestAuxiliaryStreamLossCancelsBlockedTitleBeforeCleanupCompletes(t *testing.T) {
	for _, ending := range []string{"eof", "error"} {
		t.Run(ending, func(t *testing.T) {
			credential := Credential{Token: "fixture-worker-token", MachineID: domain.NewID(), DeviceID: domain.NewID()}
			instance := domain.NewID()
			resource := auxiliaryTitleResource(t, credential, instance)
			entered, canceled, cleanup := make(chan struct{}), make(chan struct{}), make(chan struct{})
			defer close(cleanup)
			service := &auxiliaryWatchFixture{watch: func(ctx context.Context, stream *connect.ServerStream[pb.WatchAuxiliaryWorkResponse]) error {
				if err := stream.Send(&pb.WatchAuxiliaryWorkResponse{Job: resource}); err != nil {
					return err
				}
				select {
				case <-entered:
				case <-ctx.Done():
					return ctx.Err()
				}
				if ending == "error" {
					return connect.NewError(connect.CodeUnavailable, nil)
				}
				return nil
			}}
			_, handler := delidevv1connect.NewWorkerServiceHandler(service)
			server := httptest.NewServer(handler)
			defer server.Close()
			client := &auxiliaryJobFixture{WorkerServiceClient: delidevv1connect.NewWorkerServiceClient(server.Client(), server.URL), reported: make(chan struct{}), resource: resource}
			client.register = func(ctx context.Context) error {
				close(entered)
				<-ctx.Done()
				close(canceled)
				// Hold the original cleanup owner after cancellation to prove the watch
				// cannot return, report or release ownership before that owner finishes.
				<-cleanup
				return ctx.Err()
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			root := t.TempDir()
			if err := security.PrivateDir(filepath.Join(root, "jobs")); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				done <- watchAuxiliary(ctx, Config{Root: root, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, client, credential, instance)
			}()
			select {
			case <-canceled:
			case err := <-done:
				t.Fatalf("watch exited before blocked cancellation: %v", err)
			case <-time.After(2 * time.Second):
				cancel()
				t.Fatal("stream loss waited for the heartbeat instead of canceling active work")
			}
			select {
			case err := <-done:
				t.Fatalf("watch returned before cleanup joined: %v", err)
			default:
			}
			if client.reports.Load() != 0 {
				t.Fatal("reported after original stream authority was lost")
			}
			cleanup <- struct{}{}
			select {
			case err := <-done:
				if err == nil || ending == "error" && connect.CodeOf(err) != connect.CodeUnavailable || ending == "eof" && domain.SafeError(err).Code != domain.Unavailable {
					t.Fatalf("lost original terminal cause: %v", err)
				}
			case <-time.After(2 * time.Second):
				cancel()
				t.Fatal("watch did not join canceled title work")
			}
			entries, err := os.ReadDir(filepath.Join(root, "title-runtimes"))
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatal("private title runtime remained after joined cleanup")
			}
			if client.reports.Load() != 0 {
				t.Fatal("late original title published after authority loss")
			}
		})
	}
}

func TestAuxiliaryHeartbeatAndExactJobCancelPreserveHealthyWatch(t *testing.T) {
	for _, action := range []string{"heartbeat", "cancel"} {
		t.Run(action, func(t *testing.T) {
			credential := Credential{Token: "fixture-worker-token", MachineID: domain.NewID(), DeviceID: domain.NewID()}
			instance := domain.NewID()
			resource := auxiliaryTitleResource(t, credential, instance)
			entered, release := make(chan struct{}), make(chan struct{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			service := &auxiliaryWatchFixture{watch: func(ctx context.Context, stream *connect.ServerStream[pb.WatchAuxiliaryWorkResponse]) error {
				if err := stream.Send(&pb.WatchAuxiliaryWorkResponse{Job: resource}); err != nil {
					return err
				}
				select {
				case <-entered:
				case <-ctx.Done():
					return ctx.Err()
				}
				message := &pb.WatchAuxiliaryWorkResponse{Heartbeat: true}
				if action == "cancel" {
					message = &pb.WatchAuxiliaryWorkResponse{CancelJobId: resource.Id}
				}
				if err := stream.Send(message); err != nil {
					return err
				}
				if action == "heartbeat" {
					close(release)
				}
				<-ctx.Done()
				return ctx.Err()
			}}
			_, handler := delidevv1connect.NewWorkerServiceHandler(service)
			server := httptest.NewServer(handler)
			defer server.Close()
			client := &auxiliaryJobFixture{WorkerServiceClient: delidevv1connect.NewWorkerServiceClient(server.Client(), server.URL), reported: make(chan struct{}), resource: resource}
			client.register = func(jobCtx context.Context) error {
				close(entered)
				if action == "cancel" {
					<-jobCtx.Done()
					return jobCtx.Err()
				}
				select {
				case <-release:
					return connect.NewError(connect.CodeUnavailable, nil)
				case <-jobCtx.Done():
					return jobCtx.Err()
				}
			}
			root := t.TempDir()
			if err := security.PrivateDir(filepath.Join(root, "jobs")); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				done <- watchAuxiliary(ctx, Config{Root: root, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, client, credential, instance)
			}()
			select {
			case <-client.reported:
			case err := <-done:
				t.Fatalf("watch exited before reporting: %v", err)
			case <-time.After(2 * time.Second):
				cancel()
				t.Fatal("healthy stream did not retain ordinary result reporting")
			}
			select {
			case err := <-done:
				t.Fatalf("heartbeat or exact-job cancellation ended healthy watch: %v", err)
			default:
			}
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("watch did not stop after owner cancellation")
			}
			if client.reports.Load() != 1 {
				t.Fatal("original result report was duplicated")
			}
		})
	}
}

func TestAuxiliaryCompletedOriginalTitleReportsOnceOnHealthyStream(t *testing.T) {
	credential := Credential{Token: "fixture-worker-token", MachineID: domain.NewID(), DeviceID: domain.NewID()}
	instance := domain.NewID()
	resource := auxiliaryTitleResource(t, credential, instance)
	root := t.TempDir()
	if err := security.PrivateDir(filepath.Join(root, "jobs")); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(resource.DocumentJson)
	completed := journal{Version: 1, JobID: domain.ID(resource.Id), InstanceID: instance, Revision: resource.Revision, Digest: hex.EncodeToString(digest[:]), State: journalFinished, ReportID: domain.NewID(), Output: json.RawMessage(`{"version":1,"title":"Fixture title"}`)}
	if err := writeJSON(filepath.Join(root, "jobs", resource.Id+".json"), completed); err != nil {
		t.Fatal(err)
	}
	service := &auxiliaryWatchFixture{watch: func(ctx context.Context, stream *connect.ServerStream[pb.WatchAuxiliaryWorkResponse]) error {
		if err := stream.Send(&pb.WatchAuxiliaryWorkResponse{Heartbeat: true}); err != nil {
			return err
		}
		if err := stream.Send(&pb.WatchAuxiliaryWorkResponse{Job: resource}); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}}
	_, handler := delidevv1connect.NewWorkerServiceHandler(service)
	server := httptest.NewServer(handler)
	defer server.Close()
	client := &auxiliaryJobFixture{WorkerServiceClient: delidevv1connect.NewWorkerServiceClient(server.Client(), server.URL), reported: make(chan struct{}), resource: resource}
	client.register = func(context.Context) error {
		t.Error("retained completed title repeated inference registration")
		return context.Canceled
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- watchAuxiliary(ctx, Config{Root: root, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, client, credential, instance)
	}()
	select {
	case <-client.reported:
	case err := <-done:
		t.Fatalf("healthy original completion failed: %v", err)
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("completed title was not reported")
	}
	select {
	case err := <-done:
		t.Fatalf("completed title ended healthy watch: %v", err)
	default:
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not join after owner departure")
	}
	if client.reports.Load() != 1 {
		t.Fatal("original completed report was duplicated")
	}
}
