// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	bundle   []byte
	finished chan *pb.FinishSubscriptionRequest
}

func (f *earlyExecutionSubscriptionRPC) TakeSubscription(_ context.Context, req *connect.Request[pb.TakeSubscriptionRequest]) (*connect.Response[pb.TakeSubscriptionResponse], error) {
	return connect.NewResponse(&pb.TakeSubscriptionResponse{LeaseId: req.Msg.Mutation.RequestId, LeaseRevision: 3, GenerationId: string(domain.NewID()), Bundle: bytes.Clone(f.bundle)}), nil
}
func (f *earlyExecutionSubscriptionRPC) FinishSubscription(_ context.Context, req *connect.Request[pb.FinishSubscriptionRequest]) (*connect.Response[pb.FinishSubscriptionResponse], error) {
	f.finished <- req.Msg
	return connect.NewResponse(&pb.FinishSubscriptionResponse{}), nil
}

type earlyExecutionRegistrationRPC struct {
	delidevv1connect.WorkerServiceClient
	mode string
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
	for _, mode := range []string{"publisher", "registration", "response", "native-open"} {
		t.Run(mode, func(t *testing.T) {
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
			assignment := &pb.Resource{Id: string(f.jobID), Kind: pb.EntityKind_ENTITY_KIND_JOB, SchemaVersion: 1, Revision: 9, SessionId: string(f.input.SessionID), DocumentJson: document}
			bundle := workerSubscriptionBundle("first")
			defer clear(bundle)
			subscriptions := &earlyExecutionSubscriptionRPC{bundle: bundle, finished: make(chan *pb.FinishSubscriptionRequest, 1)}
			_, handler := delidevv1connect.NewSubscriptionServiceHandler(subscriptions)
			server := httptest.NewServer(handler)
			defer server.Close()
			token, err := security.RandomToken()
			if err != nil {
				t.Fatal(err)
			}
			credential := Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: server.URL, ServerID: domain.NewID(), DeviceID: domain.NewID(), PairingID: domain.NewID(), MachineID: f.input.MachineID, Token: token}
			publication := &PublicationConfig{Credential: credential, Instance: f.job.InstanceID, Assignment: assignment, Client: &earlyExecutionRegistrationRPC{mode: mode}}
			if mode == "publisher" {
				publication.Client = nil
			}

			output, err := executeSession(context.Background(), Config{Root: f.root, execution: publication, executionContext: context.Background()}, f.jobID, f.job)
			if err == nil || len(output) != 0 {
				t.Fatal("pre-native failure was reported as completed execution", err)
			}
			auth := filepath.Join(f.root, "runtimes", string(f.input.ExecutionID), "codex", "auth.json")
			if _, err := os.Lstat(auth); !os.IsNotExist(err) {
				t.Fatal("pre-native failure retained plaintext authentication", err)
			}
			select {
			case finish := <-subscriptions.finished:
				if !finish.CleanupConfirmed || finish.Succeeded {
					t.Fatal("pre-native cleanup outcome was not independently reported")
				}
			default:
				t.Fatal("protected execution completion was not reported")
			}
			assertManagedWorkerFilesRedacted(t, f.root, bundle)
		})
	}
}
