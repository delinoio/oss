package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/terminal"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func terminalFixture(t *testing.T) (*accountFixture, *pb.Resource, security.Identity, delidevv1connect.WorkerServiceClient, string, *workspace.Manifest) {
	t.Helper()
	f := newAccountFixture(t)
	selection, identity := sessionSelection(t, f)
	ctx, client, instance, stream := workspaceStream(t, f, identity, selection.MachineID)
	_, err := client.AttachWorker(ctx, ownerRequest(identity, &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: string(selection.MachineID), InstanceId: instance, Version: "0.1.0", Capabilities: []pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_SESSION_TERMINALS_V1}}))
	if err != nil {
		t.Fatal(err)
	}
	_, change := createSessionFixture(t, f, selection)
	if !stream.Receive() || stream.Msg().Job == nil {
		t.Fatal("missing preparation", stream.Err())
	}
	job := stream.Msg().Job
	var value domain.Job
	var input workspace.PrepareRequest
	if domain.Decode(job.DocumentJson, &value) != nil || domain.Decode(value.Input, &input) != nil {
		t.Fatal("invalid preparation")
	}
	manager := workspace.Manager{Root: t.TempDir() + "/worker"}
	manifest, err := manager.Prepare(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(manifest)
	if _, err := client.ReportWork(ctx, ownerRequest(identity, &pb.ReportWorkRequest{Mutation: acctMutation(job, domain.NewID()), MachineId: string(selection.MachineID), InstanceId: instance, OutputJson: raw})); err != nil {
		t.Fatal(err)
	}
	// Terminals are independent of agent dispatch. Pause this isolated fixture
	// through the public control so the live coordinator cannot advance a session
	// revision between the terminal test's intentional read and mutation.
	deadline := time.Now().Add(5 * time.Second)
	for {
		current := currentCatalogResource(t, f, change.Session)
		stopped, err := sessionClient(f).ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(current, domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_STOP}))
		if err == nil {
			return f, stopped.Msg.Change.Session, identity, client, instance, &manifest
		}
		if connect.CodeOf(err) != connect.CodeAborted || time.Now().After(deadline) {
			t.Fatal(err)
		}
	}
}

func TestTerminalReceiptsOutputReattachAndArchiveBarrier(t *testing.T) {
	f, session, worker, client, instance, manifest := terminalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	product := delidevv1connect.NewTerminalServiceClient(http.DefaultClient, f.endpoint.URL)
	create := &pb.CreateTerminalRequest{Mutation: acctMutation(session, domain.NewID()), Rows: 24, Columns: 80, ShellOverride: "/fixture/shell"}
	accepted, err := product.CreateTerminal(ctx, ownerRequest(f.identity, create))
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := product.CreateTerminal(ctx, ownerRequest(f.identity, create))
	if err != nil || !repeated.Msg.Replayed || repeated.Msg.Terminal.Id != accepted.Msg.Terminal.Id {
		t.Fatal("creation receipt duplicated the terminal", err)
	}
	if _, err := product.CreateTerminal(ctx, ownerRequest(worker, create)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker gained product authority", err)
	}
	var value domain.Terminal
	if domain.Decode(accepted.Msg.Terminal.DocumentJson, &value) != nil {
		t.Fatal("invalid terminal")
	}
	claim := &pb.ClaimTerminalRequest{RequestId: string(domain.NewID()), MachineId: string(value.MachineID), InstanceId: instance, TerminalId: accepted.Msg.Terminal.Id, OperationId: string(value.Pending.ID)}
	claimed, err := client.ClaimTerminal(ctx, ownerRequest(worker, claim))
	if err != nil {
		t.Fatal(err)
	}
	var assignment terminal.Assignment
	if domain.Decode(claimed.Msg.AssignmentJson, &assignment) != nil || assignment.Manifest.PrimaryPath != manifest.PrimaryPath {
		t.Fatal("wrong Worker or cwd")
	}
	result := terminal.Result{State: domain.TerminalRunning, Rows: 24, Columns: 80, Shell: "/fixture/shell", Cwd: manifest.PrimaryPath}
	raw, _ := json.Marshal(result)
	reported, err := client.ReportTerminal(ctx, ownerRequest(worker, &pb.ReportTerminalRequest{RequestId: string(domain.NewID()), MachineId: claim.MachineId, InstanceId: instance, TerminalId: claim.TerminalId, OperationId: claim.OperationId, ResultJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	view, err := product.WatchTerminalOutput(ctx, ownerRequest(f.identity, &pb.WatchTerminalOutputRequest{TerminalId: claim.TerminalId}))
	if err != nil || !view.Receive() {
		t.Fatal("cannot attach output", err)
	}
	epoch := view.Msg().Epoch
	nativeEpoch := domain.NewID()
	for i, data := range [][]byte{{0xe2, 0x82}, {0xac}} {
		frame := &pb.PublishTerminalOutputRequest{MachineId: claim.MachineId, InstanceId: instance, TerminalId: claim.TerminalId, Epoch: string(nativeEpoch), Sequence: uint64(i + 1), Data: data}
		if _, err := client.PublishTerminalOutput(ctx, ownerRequest(worker, frame)); err != nil {
			t.Fatal(err)
		}
		if _, err := client.PublishTerminalOutput(ctx, ownerRequest(worker, frame)); err != nil {
			t.Fatal("identical output retry failed", err)
		}
	}
	var output []byte
	for len(output) < 3 {
		if !view.Receive() {
			t.Fatal(view.Err())
		}
		output = append(output, view.Msg().Data...)
	}
	if !bytes.Equal(output, []byte("€")) {
		t.Fatal("split multibyte output changed")
	}
	view.Close()
	reattached, err := product.WatchTerminalOutput(ctx, ownerRequest(f.identity, &pb.WatchTerminalOutputRequest{TerminalId: claim.TerminalId, Epoch: epoch, AfterSequence: 2}))
	if err != nil || !reattached.Receive() || reattached.Msg().Gap || reattached.Msg().Sequence != 2 {
		t.Fatal("reattach duplicated bytes or lost cursor", err)
	}
	reattached.Close()
	for i := uint64(3); i < 23; i++ {
		_, err := client.PublishTerminalOutput(ctx, ownerRequest(worker, &pb.PublishTerminalOutputRequest{MachineId: claim.MachineId, InstanceId: instance, TerminalId: claim.TerminalId, Epoch: string(nativeEpoch), Sequence: i, Data: bytes.Repeat([]byte{'x'}, 32768)}))
		if err != nil {
			t.Fatal(err)
		}
	}
	stale, err := product.WatchTerminalOutput(ctx, ownerRequest(f.identity, &pb.WatchTerminalOutputRequest{TerminalId: claim.TerminalId, Epoch: epoch, AfterSequence: 2}))
	if err != nil || !stale.Receive() || !stale.Msg().Gap {
		t.Fatal("missing retained-output gap", err)
	}
	stale.Close()
	stopped, err := sessionClient(f).ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(currentCatalogResource(t, f, session), domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_STOP}))
	if err != nil {
		t.Fatal(err)
	}
	terminalNow := currentCatalogResource(t, f, reported.Msg.Terminal)
	domain.Decode(terminalNow.DocumentJson, &value)
	if value.CloseRequestID != "" || value.State != domain.TerminalRunning {
		t.Fatal("Agent Stop closed a terminal")
	}
	archived, err := sessionClient(f).ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(stopped.Msg.Change.Session, domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_ARCHIVE}))
	if err != nil || sessionBody(t, archived.Msg.Change.Session).Archive != domain.ArchivePending {
		t.Fatal("Archive bypassed terminal cleanup", err)
	}
	terminalNow = currentCatalogResource(t, f, terminalNow)
	domain.Decode(terminalNow.DocumentJson, &value)
	if value.CloseRequestID == "" {
		t.Fatal("Archive did not request exact terminal closure")
	}
	claim.RequestId, claim.OperationId = string(domain.NewID()), string(value.CloseRequestID)
	if _, err := client.ClaimTerminal(ctx, ownerRequest(worker, claim)); err != nil {
		t.Fatal(err)
	}
	result.State, result.CleanupVerified = domain.TerminalClosed, true
	raw, _ = json.Marshal(result)
	if _, err := client.ReportTerminal(ctx, ownerRequest(worker, &pb.ReportTerminalRequest{RequestId: string(domain.NewID()), MachineId: claim.MachineId, InstanceId: instance, TerminalId: claim.TerminalId, OperationId: claim.OperationId, ResultJson: raw})); err != nil {
		t.Fatal(err)
	}
	if sessionBody(t, currentCatalogResource(t, f, session)).Archive != domain.Archived {
		t.Fatal("Archive did not join terminal cleanup")
	}
}

func TestTerminalCloseBeforeCreateReportAndExitRacingQueuedInput(t *testing.T) {
	f, session, worker, client, instance, manifest := terminalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	product := delidevv1connect.NewTerminalServiceClient(http.DefaultClient, f.endpoint.URL)
	create := func() (*pb.Resource, *pb.ClaimTerminalRequest) {
		t.Helper()
		accepted, err := product.CreateTerminal(ctx, ownerRequest(f.identity, &pb.CreateTerminalRequest{Mutation: acctMutation(currentCatalogResource(t, f, session), domain.NewID()), Rows: 24, Columns: 80}))
		if err != nil {
			t.Fatal(err)
		}
		var value domain.Terminal
		if domain.Decode(accepted.Msg.Terminal.DocumentJson, &value) != nil {
			t.Fatal("invalid terminal")
		}
		claim := &pb.ClaimTerminalRequest{RequestId: string(domain.NewID()), MachineId: string(value.MachineID), InstanceId: instance, TerminalId: accepted.Msg.Terminal.Id, OperationId: string(value.Pending.ID)}
		if _, err := client.ClaimTerminal(ctx, ownerRequest(worker, claim)); err != nil {
			t.Fatal(err)
		}
		return currentCatalogResource(t, f, accepted.Msg.Terminal), claim
	}
	report := func(claim *pb.ClaimTerminalRequest, result terminal.Result) *pb.Resource {
		t.Helper()
		raw, _ := json.Marshal(result)
		response, err := client.ReportTerminal(ctx, ownerRequest(worker, &pb.ReportTerminalRequest{RequestId: string(domain.NewID()), MachineId: claim.MachineId, InstanceId: instance, TerminalId: claim.TerminalId, OperationId: claim.OperationId, ResultJson: raw}))
		if err != nil {
			t.Fatal(err)
		}
		return response.Msg.Terminal
	}
	resource, claim := create()
	closed, err := product.ControlTerminal(ctx, ownerRequest(f.identity, &pb.ControlTerminalRequest{Mutation: acctMutation(resource, domain.NewID()), Action: pb.TerminalAction_TERMINAL_ACTION_CLOSE}))
	if err != nil {
		t.Fatal(err)
	}
	var value domain.Terminal
	domain.Decode(closed.Msg.Terminal.DocumentJson, &value)
	claim.RequestId, claim.OperationId = string(domain.NewID()), string(value.CloseRequestID)
	if _, err := client.ClaimTerminal(ctx, ownerRequest(worker, claim)); err != nil {
		t.Fatal(err)
	}
	// The shell started, but its creation report never reached the server.
	// Exact close must accept its independently observed native metadata.
	result := terminal.Result{State: domain.TerminalClosed, CleanupVerified: true, Rows: 24, Columns: 80, Shell: "/fixture/shell", Cwd: manifest.PrimaryPath}
	report(claim, result)
	resource, claim = create()
	result.State, result.CleanupVerified = domain.TerminalRunning, false
	resource = report(claim, result)
	input := &pb.ControlTerminalRequest{Mutation: acctMutation(resource, domain.NewID()), Action: pb.TerminalAction_TERMINAL_ACTION_INPUT, Input: []byte("never-claimed\n")}
	if _, err := product.ControlTerminal(ctx, ownerRequest(f.identity, input)); err != nil {
		t.Fatal(err)
	}
	claim.OperationId = ""
	result.State, result.CleanupVerified = domain.TerminalExited, true
	code := 0
	result.ExitCode = &code
	report(claim, result)
	replayed, err := product.ControlTerminal(ctx, ownerRequest(f.identity, input))
	if err != nil || !replayed.Msg.Replayed {
		t.Fatal("queued input receipt could not reconcile exit", err)
	}
	value = domain.Terminal{}
	domain.Decode(replayed.Msg.Terminal.DocumentJson, &value)
	if value.Pending != nil || value.State != domain.TerminalExited || !value.CleanupVerified {
		t.Fatal("receipt replay redispatched input after original shell exit")
	}
}
