// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
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

func TestTerminalReportSanitizesWorkerProblemsAndPreservesExactReceipt(t *testing.T) {
	f, session, worker, client, instance, _ := terminalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	product := delidevv1connect.NewTerminalServiceClient(http.DefaultClient, f.endpoint.URL)
	accepted, err := product.CreateTerminal(ctx, ownerRequest(f.identity, &pb.CreateTerminalRequest{Mutation: acctMutation(session, domain.NewID()), Rows: 24, Columns: 80}))
	if err != nil {
		t.Fatal(err)
	}
	var value domain.Terminal
	if err := domain.Decode(accepted.Msg.Terminal.DocumentJson, &value); err != nil {
		t.Fatal(err)
	}
	claim := &pb.ClaimTerminalRequest{RequestId: string(domain.NewID()), MachineId: string(value.MachineID), InstanceId: instance, TerminalId: accepted.Msg.Terminal.Id, OperationId: string(value.Pending.ID)}
	if _, err := client.ClaimTerminal(ctx, ownerRequest(worker, claim)); err != nil {
		t.Fatal(err)
	}
	marker := "private-native-token-path-stderr"
	problem := &domain.Error{Code: "unknown_native_code", Message: strings.Repeat(marker, 256), Guidance: marker, Cause: marker, CorrelationID: marker}
	result := terminal.Result{State: domain.TerminalUncertain, Rows: 24, Columns: 80, Problem: problem}
	raw, _ := json.Marshal(result)
	report := &pb.ReportTerminalRequest{RequestId: string(domain.NewID()), MachineId: claim.MachineId, InstanceId: instance, TerminalId: claim.TerminalId, OperationId: claim.OperationId, ResultJson: raw}
	if _, err := client.ReportTerminal(ctx, ownerRequest(worker, report)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("unknown failure code crossed the report boundary", err)
	}
	// A rejected report must not consume the request ID or operation. The
	// corrected classification can commit while its text remains untrusted.
	problem.Code = domain.RecoveryRequired
	report.ResultJson, _ = json.Marshal(result)
	reported, err := client.ReportTerminal(ctx, ownerRequest(worker, report))
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.Decode(reported.Msg.Terminal.DocumentJson, &value); err != nil {
		t.Fatal(err)
	}
	if value.Problem == nil || value.Problem.Code != domain.RecoveryRequired || value.Problem.Message == problem.Message || value.Problem.Guidance == problem.Guidance || value.Problem.Cause != "" || value.Problem.CorrelationID != "" || bytes.Contains(reported.Msg.Terminal.DocumentJson, []byte(marker)) {
		t.Fatal("Worker diagnostic text entered public terminal state")
	}
	stored := currentCatalogResource(t, f, reported.Msg.Terminal)
	if bytes.Contains(stored.DocumentJson, []byte(marker)) {
		t.Fatal("Worker diagnostic text persisted")
	}
	replayed, err := client.ReportTerminal(ctx, ownerRequest(worker, report))
	if err != nil || replayed.Msg.Terminal.Revision != reported.Msg.Terminal.Revision {
		t.Fatal("exact original report did not replay read-only", err)
	}
	problem.Message = "different untrusted diagnostic with the same classification"
	report.ResultJson, _ = json.Marshal(result)
	if _, err := client.ReportTerminal(ctx, ownerRequest(worker, report)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("sanitized projection replaced original receipt identity", err)
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

func TestTerminalReportEscapedPathBoundsRemainRetryableThroughArchive(t *testing.T) {
	f, session, worker, client, instance, _ := terminalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	product := delidevv1connect.NewTerminalServiceClient(http.DefaultClient, f.endpoint.URL)
	shell, cwd := "/"+strings.Repeat("<", 4095), "/"+strings.Repeat("&", 4095)
	accepted, err := product.CreateTerminal(ctx, ownerRequest(f.identity, &pb.CreateTerminalRequest{Mutation: acctMutation(session, domain.NewID()), Rows: 24, Columns: 80, ShellOverride: shell}))
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
	result := terminal.Result{State: domain.TerminalRunning, Shell: shell, Cwd: cwd, Rows: 24, Columns: 80}
	raw, _ := json.Marshal(result)
	if result.Validate() != nil || len(raw) <= 16<<10 {
		t.Fatal("fixture must contain valid paths beyond the old encoded report limit")
	}
	report := &pb.ReportTerminalRequest{RequestId: string(domain.NewID()), MachineId: claim.MachineId, InstanceId: instance, TerminalId: claim.TerminalId, OperationId: claim.OperationId, ResultJson: raw}
	running, err := client.ReportTerminal(ctx, ownerRequest(worker, report))
	if err != nil {
		t.Fatal("valid escaped paths were rejected", err)
	}
	replayed, err := client.ReportTerminal(ctx, ownerRequest(worker, report))
	if err != nil || replayed.Msg.Terminal.Revision != running.Msg.Terminal.Revision {
		t.Fatal("running report retry changed its outcome", err)
	}
	archived, err := sessionClient(f).ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(currentCatalogResource(t, f, session), domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_ARCHIVE}))
	if err != nil || sessionBody(t, archived.Msg.Change.Session).Archive != domain.ArchivePending {
		t.Fatal("Archive did not await the terminal", err)
	}
	current := currentCatalogResource(t, f, running.Msg.Terminal)
	if domain.Decode(current.DocumentJson, &value) != nil {
		t.Fatal("invalid closing terminal")
	}
	claim.RequestId, claim.OperationId = string(domain.NewID()), string(value.CloseRequestID)
	if _, err := client.ClaimTerminal(ctx, ownerRequest(worker, claim)); err != nil {
		t.Fatal(err)
	}
	result.State, result.CleanupVerified = domain.TerminalClosed, true
	raw, _ = json.Marshal(result)
	report.RequestId, report.OperationId, report.ResultJson = string(domain.NewID()), claim.OperationId, raw
	closed, err := client.ReportTerminal(ctx, ownerRequest(worker, report))
	if err != nil {
		t.Fatal("valid escaped cleanup paths were rejected", err)
	}
	replayed, err = client.ReportTerminal(ctx, ownerRequest(worker, report))
	if err != nil || replayed.Msg.Terminal.Revision != closed.Msg.Terminal.Revision {
		t.Fatal("cleanup report retry changed its outcome", err)
	}
	if sessionBody(t, currentCatalogResource(t, f, session)).Archive != domain.Archived {
		t.Fatal("accepted paths prevented confirmed Archive cleanup")
	}
	report.RequestId = string(domain.NewID())
	report.ResultJson = append(bytes.Clone(raw), bytes.Repeat([]byte{' '}, terminal.MaxResultBytes-len(raw)+1)...)
	if _, err := client.ReportTerminal(ctx, ownerRequest(worker, report)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("oversized result envelope was accepted", err)
	}
}

func TestTerminalAtomicReuseOrCreateAcrossClients(t *testing.T) {
	f, session, worker, _, _, _ := terminalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	devices := delidevv1connect.NewDeviceServiceClient(http.DefaultClient, f.endpoint.URL)
	code, token := randomCode(), randomCode()
	codeHash, tokenHash := sha256.Sum256([]byte(code)), sha256.Sum256([]byte(token))
	grant, err := devices.CreatePairing(ctx, ownerRequest(f.identity, &pb.CreatePairingRequest{RequestId: string(domain.NewID()), Name: "Terminal fixture client", Type: pb.DeviceType_DEVICE_TYPE_CLIENT, CodeDigest: codeHash[:]}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := devices.PairDevice(ctx, connect.NewRequest(&pb.PairDeviceRequest{RequestId: string(domain.NewID()), PairingId: grant.Msg.Pairing.Id, Code: code, DeviceId: string(domain.NewID()), CredentialDigest: tokenHash[:]})); err != nil {
		t.Fatal(err)
	}
	paired := security.Identity{Token: token}
	product := delidevv1connect.NewTerminalServiceClient(http.DefaultClient, f.endpoint.URL)
	type outcome struct {
		response *connect.Response[pb.CreateTerminalResponse]
		err      error
	}
	start, results := make(chan struct{}), make(chan outcome, 2)
	requests := make([]*pb.CreateTerminalRequest, 2)
	for index, identity := range []security.Identity{f.identity, paired} {
		request := &pb.CreateTerminalRequest{Mutation: acctMutation(session, domain.NewID()), Rows: 24, Columns: 80, CreationMode: pb.TerminalCreationMode_TERMINAL_CREATION_MODE_REUSE_OR_CREATE}
		requests[index] = request
		go func() {
			<-start
			response, err := product.CreateTerminal(ctx, ownerRequest(identity, request))
			results <- outcome{response, err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatal("concurrent admission failed", first.err, second.err)
	}
	if first.response.Msg.Terminal.Id != second.response.Msg.Terminal.Id {
		t.Fatal("independent clients created separate shells")
	}
	replayed, err := product.CreateTerminal(ctx, ownerRequest(f.identity, requests[0]))
	if err != nil || !replayed.Msg.Replayed || replayed.Msg.Terminal.Id != first.response.Msg.Terminal.Id {
		t.Fatal("automatic receipt changed original terminal", err)
	}
	if _, err := product.CreateTerminal(ctx, ownerRequest(worker, requests[0])); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker gained automatic product admission", err)
	}
	unknown := &pb.CreateTerminalRequest{Mutation: acctMutation(session, domain.NewID()), Rows: 24, Columns: 80, CreationMode: pb.TerminalCreationMode(99)}
	if _, err := product.CreateTerminal(ctx, ownerRequest(f.identity, unknown)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("unknown mode was accepted", err)
	}
	additional, err := product.CreateTerminal(ctx, ownerRequest(f.identity, &pb.CreateTerminalRequest{Mutation: acctMutation(session, domain.NewID()), Rows: 24, Columns: 80}))
	if err != nil || additional.Msg.Terminal.Id == first.response.Msg.Terminal.Id {
		t.Fatal("unspecified explicit additional creation stopped creating", err)
	}
	preferred := &pb.CreateTerminalRequest{Mutation: acctMutation(session, domain.NewID()), Rows: 24, Columns: 80, CreationMode: pb.TerminalCreationMode_TERMINAL_CREATION_MODE_REUSE_OR_CREATE, PreferredTerminalId: additional.Msg.Terminal.Id}
	chosen, err := product.CreateTerminal(ctx, ownerRequest(f.identity, preferred))
	if err != nil || chosen.Msg.Terminal.Id != additional.Msg.Terminal.Id {
		t.Fatal("atomic admission lost eligible original preference", err)
	}
	missing := &pb.CreateTerminalRequest{Mutation: acctMutation(session, domain.NewID()), Rows: 24, Columns: 80, CreationMode: pb.TerminalCreationMode_TERMINAL_CREATION_MODE_REUSE_OR_CREATE, PreferredTerminalId: string(domain.NewID())}
	fallback, err := product.CreateTerminal(ctx, ownerRequest(f.identity, missing))
	if err != nil || fallback.Msg.Terminal.Id != first.response.Msg.Terminal.Id {
		t.Fatal("foreign preference gained authority or lost ordered fallback", err)
	}
	for _, row := range []*pb.Resource{first.response.Msg.Terminal, additional.Msg.Terminal} {
		if _, err := product.ControlTerminal(ctx, ownerRequest(f.identity, &pb.ControlTerminalRequest{Mutation: acctMutation(row, domain.NewID()), Action: pb.TerminalAction_TERMINAL_ACTION_CLOSE})); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := product.CreateTerminal(ctx, ownerRequest(f.identity, &pb.CreateTerminalRequest{Mutation: acctMutation(session, domain.NewID()), Rows: 24, Columns: 80, CreationMode: pb.TerminalCreationMode_TERMINAL_CREATION_MODE_REUSE_OR_CREATE})); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("pending original closes permitted a replacement shell", err)
	}
}
