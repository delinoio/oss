package server

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type losePublicationAck struct {
	delidevv1connect.WorkerServiceClient
	t      *testing.T
	path   string
	calls  []string
	dropAt int
}

func (c *losePublicationAck) PublishExecution(ctx context.Context, req *connect.Request[pb.PublishExecutionRequest]) (*connect.Response[pb.PublishExecutionResponse], error) {
	raw, err := security.ReadPrivate(c.path, 1<<20)
	if err != nil || !strings.Contains(string(raw), req.Msg.Mutation.RequestId) {
		c.t.Error("publication crossed RPC before its private durable outbox")
	}
	c.calls = append(c.calls, req.Msg.Mutation.RequestId)
	response, err := c.WorkerServiceClient.PublishExecution(ctx, req)
	dropAt := c.dropAt
	if dropAt == 0 {
		dropAt = 1
	}
	if err == nil && len(c.calls) == dropAt {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("fixture lost acknowledgment"))
	}
	return response, err
}

func TestWorkerUsagePublicationRetainsIdentityAcrossLostAcknowledgment(t *testing.T) {
	f := newPublicationFixture(t)
	cfg := publicationWorkerConfig(t, f)
	cfg.Client = &losePublicationAck{WorkerServiceClient: f.client, t: t, path: filepath.Join(cfg.Root, "jobs", string(f.job), "publication.json"), dropAt: 3}
	publisher, err := worker.OpenExecutionPublisher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	mapper := worker.NewCodexEventPublisher(publisher)
	err = mapper.BindThread(context.Background(), codex.ThreadResult{RequestID: f.input.ThreadRequestID, Thread: &codex.Thread{ID: f.thread}, Effective: &codex.EffectiveSettings{Model: f.input.Configuration.NativeModel, Provider: codex.APIProvider, Sandbox: codex.Sandbox{Type: codex.ReadOnly}, ApprovalPolicy: codex.ApprovalOnRequest, ApprovalsReviewer: "user"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := mapper.AcceptInput(context.Background(), codex.TurnResult{RequestID: f.input.TurnRequestID, InputID: f.input.InputID, TurnID: f.turn}); err != nil {
		t.Fatal(err)
	}
	for _, metadata := range []codex.MetadataKind{codex.ThreadSettingsChecked, codex.ThreadIdentityChecked, codex.RemoteControlDisabled, codex.QuotaUnavailable} {
		if handled, err := mapper.PublishCore(context.Background(), codex.Event{Kind: codex.MetadataEvent, ThreadID: f.thread, Correlated: true, Metadata: metadata}); err != nil || !handled {
			t.Fatalf("validated metadata was not handled: %v", err)
		}
	}
	handled, err := mapper.PublishCore(context.Background(), codex.Event{Kind: codex.UsageEvent, ThreadID: f.thread, TurnID: f.turn, Correlated: true, Usage: &domain.NativeTokenUsage{Total: reportedCounts(30), Last: reportedCounts(20)}})
	if !handled || err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("lost usage acknowledgment was promoted to success")
	}
	if _, err := mapper.PublishCore(context.Background(), codex.Event{Kind: codex.NoticeEvent, ThreadID: f.thread, Correlated: true, Notice: domain.NativeWarning}); err == nil {
		t.Fatal("later observation replaced uncertain usage")
	}
	if err := publisher.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.UsageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatalf("lost usage acknowledgment generated another observation: %v", err)
	}
	observation, err := store.Decode[domain.ExecutionUsageObservation](rows[0])
	if err != nil || observation.Sequence != 3 || *observation.Usage.Total.Total != 30 {
		t.Fatal("retained usage changed after replay")
	}
}

func publicationWorkerConfig(t *testing.T, f *publicationFixture) worker.PublicationConfig {
	t.Helper()
	r, err := f.service.Store.Get(context.Background(), domain.JobKind, f.job)
	if err != nil {
		t.Fatal(err)
	}
	return worker.PublicationConfig{Root: filepath.Join(t.TempDir(), "worker"), Credential: worker.Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: f.http.URL, ServerID: f.service.Identity.ServerID, DeviceID: f.device, MachineID: f.input.MachineID, PairingID: domain.NewID(), Token: f.workerToken}, Instance: f.instance, Assignment: rpc.Resource(r), Client: f.client}
}

func TestWorkerPublicationOutboxReplaysLostAcknowledgmentWithoutSubstitution(t *testing.T) {
	f := newPublicationFixture(t)
	cfg := publicationWorkerConfig(t, f)
	path := filepath.Join(cfg.Root, "jobs", string(f.job), "publication.json")
	client := &losePublicationAck{WorkerServiceClient: f.client, t: t, path: path}
	cfg.Client = client
	publisher, err := worker.OpenExecutionPublisher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := worker.OpenExecutionPublisher(cfg); err == nil {
		_ = other.Close()
		t.Fatal("another publisher acquired live outbox ownership")
	}
	if err := publisher.Publish(context.Background(), f.event(domain.ExecutionThreadBound, 1)); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("lost native event acknowledgment was not retained: %v", err)
	}
	if err := publisher.Publish(context.Background(), f.event(domain.ExecutionInputAccepted, 2)); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("an unresolved outbox was overwritten by a later event")
	}
	if err := publisher.Close(); err != nil {
		t.Fatal(err)
	}
	publisher, err = worker.OpenExecutionPublisher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	if err := publisher.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != 2 || client.calls[0] != client.calls[1] {
		t.Fatal("outbox recovery did not reuse its exact durable receipt identity")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := publisher.Publish(canceled, f.event(domain.ExecutionInputAccepted, 2)); domain.SafeError(err).Code != domain.Canceled {
		t.Fatal("pre-send cancellation created publication work")
	}
	if err := publisher.Publish(context.Background(), f.event(domain.ExecutionInputAccepted, 2)); err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != 3 || client.calls[2] == client.calls[0] {
		t.Fatal("the next native fact lost its own request identity")
	}
	raw, err := security.ReadPrivate(path, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), f.workerToken) || strings.Contains(string(raw), f.token) || strings.Contains(string(raw), "pending") {
		t.Fatal("acknowledged outbox retained credentials or its acknowledged event payload")
	}
	r, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Decode[domain.Session](r)
	if err != nil || s.Execution.LastSequence != 2 || s.PendingInputs != 0 {
		t.Fatal("outbox recovery duplicated input acceptance")
	}
	if err := publisher.Close(); err != nil {
		t.Fatal(err)
	}
	cfg.Assignment.Revision++
	if invalid, err := worker.OpenExecutionPublisher(cfg); err == nil {
		_ = invalid.Close()
		t.Fatal("changed assignment inherited another publication journal")
	}
}
