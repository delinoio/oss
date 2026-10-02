// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type earlyExecutionSubscriptionRPC struct {
	delidevv1connect.UnimplementedSubscriptionServiceHandler
	bundle                 []byte
	finished               chan *pb.FinishSubscriptionRequest
	failFinish             bool
	failTake               bool
	denyAuthenticationHome string
}

func (f *earlyExecutionSubscriptionRPC) TakeSubscription(_ context.Context, req *connect.Request[pb.TakeSubscriptionRequest]) (*connect.Response[pb.TakeSubscriptionResponse], error) {
	if f.failTake {
		return nil, connect.NewError(connect.CodeUnavailable, nil)
	}
	if f.denyAuthenticationHome != "" {
		if err := os.Chmod(f.denyAuthenticationHome, 0500); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	return connect.NewResponse(&pb.TakeSubscriptionResponse{LeaseId: req.Msg.Mutation.RequestId, LeaseRevision: 3, GenerationId: string(domain.NewID()), Bundle: bytes.Clone(f.bundle)}), nil
}
func (f *earlyExecutionSubscriptionRPC) FinishSubscription(_ context.Context, req *connect.Request[pb.FinishSubscriptionRequest]) (*connect.Response[pb.FinishSubscriptionResponse], error) {
	f.finished <- req.Msg
	if f.failFinish {
		return nil, connect.NewError(connect.CodeUnavailable, nil)
	}
	return connect.NewResponse(&pb.FinishSubscriptionResponse{}), nil
}

type earlyExecutionRegistrationRPC struct {
	delidevv1connect.WorkerServiceClient
	mode     string
	reported bool
}

func (f *earlyExecutionRegistrationRPC) ReportWork(context.Context, *connect.Request[pb.ReportWorkRequest]) (*connect.Response[pb.ReportWorkResponse], error) {
	f.reported = true
	return connect.NewResponse(&pb.ReportWorkResponse{}), nil
}

func (f *earlyExecutionRegistrationRPC) RegisterExecution(context.Context, *connect.Request[pb.RegisterExecutionRequest]) (*connect.Response[pb.RegisterExecutionResponse], error) {
	if f.mode == "registration" {
		return nil, connect.NewError(connect.CodeUnavailable, nil)
	}
	if f.mode == "response" {
		return connect.NewResponse(&pb.RegisterExecutionResponse{ProxyPath: "/api-proxy/v1"}), nil
	}
	return connect.NewResponse(&pb.RegisterExecutionResponse{}), nil
}

func TestManagedExecutionPreNativeFailureRemovesAuthentication(t *testing.T) {
	for _, mode := range []string{"auth-write", "publisher", "registration", "response", "native-open", "finish", "take"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "auth-write" && (runtime.GOOS == "windows" || os.Geteuid() == 0) {
				t.Skip("the failed CreateTemp fixture requires Unix owner permission enforcement")
			}
			f := newCheckpointFixture(t)
			// Use a fresh execution while preserving the helper's unrelated empty runtime.
			f.input.ExecutionID = domain.NewID()
			f.input.Configuration.Subscription = true
			var err error
			f.input.ConfigurationDigest, err = f.input.Configuration.Digest()
			if err != nil {
				t.Fatal(err)
			}
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			if mode == "native-open" {
				// Invalid executable bytes fail before an OS process can launch.
				binary = filepath.Join(t.TempDir(), "invalid-codex")
				if err := os.WriteFile(binary, []byte("invalid native fixture\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			f.input.Installation.ResolvedPath, err = filepath.EvalSymlinks(binary)
			if err != nil {
				t.Fatal(err)
			}
			manager := &workspace.Manager{Root: f.root}
			preparation := workspace.PrepareRequest{SessionID: f.input.SessionID, MachineID: f.input.MachineID, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}}
			manifest, err := manager.Prepare(context.Background(), preparation)
			if err != nil {
				t.Fatal(err)
			}
			f.input.Preparation, _ = json.Marshal(preparation)
			f.input.Manifest, _ = json.Marshal(manifest)
			if err := f.input.Validate(); err != nil {
				t.Fatal(err)
			}
			f.job.Input, _ = json.Marshal(f.input)
			f.job.InstanceID = domain.NewID()
			document, _ := json.Marshal(f.job)
			resource := &pb.Resource{Id: string(f.jobID), Kind: pb.EntityKind_ENTITY_KIND_JOB, SchemaVersion: 1, Revision: 9, SessionId: string(f.input.SessionID), DocumentJson: document}
			bundle := workerSubscriptionBundle("first")
			defer clear(bundle)
			subscriptions := &earlyExecutionSubscriptionRPC{bundle: bundle, finished: make(chan *pb.FinishSubscriptionRequest, 1)}
			if mode == "auth-write" {
				subscriptions.denyAuthenticationHome = filepath.Join(f.root, "runtimes", string(f.input.ExecutionID), "codex")
				t.Cleanup(func() { _ = os.Chmod(subscriptions.denyAuthenticationHome, 0700) })
			}
			_, handler := delidevv1connect.NewSubscriptionServiceHandler(subscriptions)
			server := httptest.NewServer(handler)
			defer server.Close()
			token, err := security.RandomToken()
			if err != nil {
				t.Fatal(err)
			}
			credential := Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: server.URL, ServerID: domain.NewID(), DeviceID: domain.NewID(), PairingID: domain.NewID(), MachineID: f.input.MachineID, Token: token}
			publication := &PublicationConfig{Credential: credential, Instance: f.job.InstanceID, Assignment: resource, Client: &earlyExecutionRegistrationRPC{mode: mode}}
			if mode == "publisher" {
				publication.Client = nil
			}

			var output json.RawMessage
			if mode == "finish" || mode == "take" {
				subscriptions.failFinish = mode == "finish"
				subscriptions.failTake = mode == "take"
				client := &earlyExecutionRegistrationRPC{mode: "registration"}
				if err := security.PrivateDir(filepath.Join(f.root, "jobs")); err != nil {
					t.Fatal(err)
				}
				config := Config{Root: f.root, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
				work := assignment{context: context.Background(), cancel: func() {}}
				err = runAndReportJob(context.Background(), config, client, credential, f.job.InstanceID, work, resource, f.job)
				var uncertain *managedExecutionUncertain
				if !errors.As(err, &uncertain) || client.reported {
					t.Fatal("uncertain protected completion did not stop the work lane", err)
				}
				raw, readErr := security.ReadPrivate(filepath.Join(f.root, "jobs", string(f.jobID)+".json"), 2<<20)
				var j journal
				if readErr != nil || domain.Decode(raw, &j) != nil || j.State != journalStarted {
					t.Fatal("uncertain completion replaced the original claim journal", readErr)
				}
			} else {
				output, err = executeSession(context.Background(), Config{Root: f.root, execution: publication, executionContext: context.Background()}, f.jobID, f.job)
				var uncertain *managedExecutionUncertain
				if errors.As(err, &uncertain) {
					t.Fatal("verified unused-original cleanup interrupted the work lane", err)
				}
			}
			if err == nil || len(output) != 0 {
				t.Fatal("pre-native failure was reported as completed execution", err)
			}
			auth := filepath.Join(f.root, "runtimes", string(f.input.ExecutionID), "codex", "auth.json")
			if _, err := os.Lstat(auth); !os.IsNotExist(err) {
				t.Fatal("pre-native failure retained plaintext authentication", err)
			}
			select {
			case finish := <-subscriptions.finished:
				if mode == "take" {
					t.Fatal("uncertain delivery invented a completion")
				}
				if !finish.CleanupConfirmed || finish.Succeeded || !bytes.Equal(finish.Bundle, bundle) {
					t.Fatal("pre-native cleanup outcome was not independently reported")
				}
			default:
				if mode != "take" {
					t.Fatal("protected execution completion was not reported")
				}
			}
			assertManagedWorkerFilesRedacted(t, f.root, bundle)
		})
	}
}
