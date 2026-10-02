//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/terminal"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type terminalLogBuffer struct {
	sync.Mutex
	bytes.Buffer
}

func (b *terminalLogBuffer) Write(raw []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.Write(raw)
}
func (b *terminalLogBuffer) snapshot() []byte {
	b.Lock()
	defer b.Unlock()
	return bytes.Clone(b.Buffer.Bytes())
}

type terminalTransportFixture struct {
	delidevv1connect.UnimplementedWorkerServiceHandler
	sync.Mutex
	assignments map[domain.ID]terminal.Assignment
	claims      map[string]bool
	reports     map[string][]byte
	lose        map[domain.ID]bool
}

func (f *terminalTransportFixture) ClaimTerminal(_ context.Context, request *connect.Request[pb.ClaimTerminalRequest]) (*connect.Response[pb.ClaimTerminalResponse], error) {
	f.Lock()
	defer f.Unlock()
	a := f.assignments[domain.ID(request.Msg.OperationId)]
	a.Terminal.InstanceID = domain.ID(request.Msg.InstanceId)
	raw, _ := json.Marshal(a)
	f.claims[request.Msg.RequestId] = true
	return connect.NewResponse(&pb.ClaimTerminalResponse{AssignmentJson: raw}), nil
}
func (f *terminalTransportFixture) ReportTerminal(_ context.Context, request *connect.Request[pb.ReportTerminalRequest]) (*connect.Response[pb.ReportTerminalResponse], error) {
	f.Lock()
	defer f.Unlock()
	if prior := f.reports[request.Msg.RequestId]; prior != nil && !bytes.Equal(prior, request.Msg.ResultJson) {
		return nil, connect.NewError(connect.CodeAborted, nil)
	}
	f.reports[request.Msg.RequestId] = bytes.Clone(request.Msg.ResultJson)
	op := domain.ID(request.Msg.OperationId)
	if f.lose[op] {
		f.lose[op] = false
		return nil, connect.NewError(connect.CodeUnavailable, nil)
	}
	return connect.NewResponse(&pb.ReportTerminalResponse{}), nil
}
func (f *terminalTransportFixture) PublishTerminalOutput(_ context.Context, _ *connect.Request[pb.PublishTerminalOutputRequest]) (*connect.Response[pb.PublishTerminalOutputResponse], error) {
	return connect.NewResponse(&pb.PublishTerminalOutputResponse{}), nil
}

func TestTerminalNativeCreateAndInputReceiptLossNeverReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	root := filepath.Join(t.TempDir(), "worker")
	workspaceManager := workspace.Manager{Root: root, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	input := workspace.PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
	manifest, err := workspaceManager.Prepare(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	assignment := terminal.Assignment{ID: domain.NewID(), SessionID: input.SessionID, Terminal: domain.Terminal{MachineID: input.MachineID, InstanceID: domain.NewID(), ShellOverride: "/bin/sh", State: domain.TerminalStarting, Rows: 24, Columns: 80}, Operation: domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalCreate}, Preparation: &input, Manifest: &manifest}
	fixture := &terminalTransportFixture{assignments: map[domain.ID]terminal.Assignment{assignment.Operation.ID: assignment}, claims: map[string]bool{}, reports: map[string][]byte{}, lose: map[domain.ID]bool{assignment.Operation.ID: true}}
	_, handler := delidevv1connect.NewWorkerServiceHandler(fixture)
	server := httptest.NewServer(handler)
	defer server.Close()
	var logs terminalLogBuffer
	manager := newTerminalManager(ctx, Config{Root: root, Logger: slog.New(slog.NewJSONHandler(&logs, nil))}, delidevv1connect.NewWorkerServiceClient(http.DefaultClient, server.URL), Credential{MachineID: input.MachineID, Token: "fixture-only"}, assignment.Terminal.InstanceID)
	defer manager.close()
	if err := manager.apply(ctx, assignment); err == nil {
		t.Fatal("expected lost create acknowledgment")
	}
	identity := manager.live[assignment.ID].handle.Identity()
	if err := manager.apply(ctx, assignment); err != nil {
		t.Fatal(err)
	}
	if len(manager.live) != 1 || manager.live[assignment.ID].handle.Identity() != identity {
		t.Fatal("receipt retry created another shell")
	}
	operation := assignment
	operation.Preparation, operation.Manifest = nil, nil
	operation.Terminal.State, operation.Terminal.Shell, operation.Terminal.Cwd = domain.TerminalRunning, "/bin/sh", manifest.PrimaryPath
	operation.Operation = domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalInput, Input: []byte("printf 'once-private-terminal-input' >> receipt-marker\n")}
	fixture.Lock()
	fixture.assignments[operation.Operation.ID] = operation
	fixture.lose[operation.Operation.ID] = true
	fixture.Unlock()
	if err := manager.apply(ctx, operation); err == nil {
		t.Fatal("expected lost input acknowledgment")
	}
	if err := manager.apply(ctx, operation); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		raw, err := os.ReadFile(filepath.Join(manifest.PrimaryPath, "receipt-marker"))
		// Redirect creation precedes printf's complete write. A readable partial
		// file is not duplicate-input evidence; the joined close checks it again.
		if err == nil && len(raw) >= len("once-private-terminal-input") {
			if string(raw) != "once-private-terminal-input" {
				t.Fatal("native input was repeated")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("original shell input never ran")
		}
		time.Sleep(20 * time.Millisecond)
	}
	unknown := operation
	unknown.ID = domain.NewID()
	unknown.Operation = domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalClose}
	result := manager.execute(unknown)
	if result.State != domain.TerminalUncertain || result.CleanupVerified {
		t.Fatal("unknown process ownership manufactured cleanup")
	}
	interrupted := operation
	interrupted.Operation = domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalInput, Input: []byte("printf 'duplicated' >> receipt-marker\n")}
	journal := terminalOperationJournal{TerminalID: interrupted.ID, OperationID: interrupted.Operation.ID, InstanceID: manager.instance, Digest: terminalOperationDigest(interrupted), ClaimID: domain.NewID(), ReportID: domain.NewID(), Phase: terminalStarted}
	if err := manager.saveJournal(journal); err != nil {
		t.Fatal(err)
	}
	if err := manager.apply(ctx, interrupted); err != nil {
		t.Fatal(err)
	}
	fixture.Lock()
	var uncertain terminal.Result
	err = domain.Decode(fixture.reports[string(journal.ReportID)], &uncertain)
	fixture.Unlock()
	if err != nil || uncertain.State != domain.TerminalUncertain || uncertain.CleanupVerified {
		t.Fatal("interrupted native operation was replayed or confirmed", err)
	}
	operation.Operation = domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalClose}
	fixture.Lock()
	fixture.assignments[operation.Operation.ID] = operation
	fixture.lose[operation.Operation.ID] = true
	fixture.Unlock()
	if err := manager.apply(ctx, operation); err == nil {
		t.Fatal("expected lost close acknowledgement")
	}
	// Mutable resize/display progress cannot change an already claimed close's
	// identity or cause native cleanup to run a second time after response loss.
	operation.Terminal.Rows, operation.Terminal.Columns = 37, 91
	if err := manager.apply(ctx, operation); err != nil {
		t.Fatal(err)
	}
	if len(manager.live) != 0 {
		t.Fatal("close retained a live shell")
	}
	marker, err := os.ReadFile(filepath.Join(manifest.PrimaryPath, "receipt-marker"))
	if err != nil || string(marker) != "once-private-terminal-input" {
		t.Fatal("interrupted input was sent to the shell", err)
	}
	if bytes.Contains(logs.snapshot(), []byte("once-private-terminal-input")) {
		t.Fatal("terminal input entered operational logs")
	}
}

func TestTerminalCloseBeforeNativeStartUsesOriginalJournal(t *testing.T) {
	root := t.TempDir()
	instance, id, creation := domain.NewID(), domain.NewID(), domain.NewID()
	manager := newTerminalManager(context.Background(), Config{Root: root}, nil, Credential{}, instance)
	if err := security.PrivateDir(filepath.Join(root, "terminal-operations")); err != nil {
		t.Fatal(err)
	}
	assignment := terminal.Assignment{ID: id, Terminal: domain.Terminal{OwnerInstanceID: instance, InstanceID: instance, Rows: 24, Columns: 80, State: domain.TerminalStarting, Pending: &domain.TerminalOperation{ID: creation, Action: domain.TerminalCreate, Claimed: true}}, Operation: domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalClose}}
	journal := terminalOperationJournal{TerminalID: id, OperationID: creation, InstanceID: instance, Phase: terminalPrepared}
	if err := manager.saveJournal(journal); err != nil {
		t.Fatal(err)
	}
	result := manager.execute(assignment)
	if result.State != domain.TerminalClosed || !result.CleanupVerified {
		t.Fatal("lost claim acknowledgement prevented proven pre-native cleanup")
	}
	journal.Phase = terminalStarted
	if err := manager.saveJournal(journal); err != nil {
		t.Fatal(err)
	}
	result = manager.execute(assignment)
	if result.State != domain.TerminalUncertain || result.CleanupVerified {
		t.Fatal("native-start intent without a process index manufactured cleanup")
	}
}

func TestTerminalFailedCreateRetainsAbandonedPublisherOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	root := filepath.Join(t.TempDir(), "worker")
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	workspaces := workspace.Manager{Root: root, Logger: logger}
	input := workspace.PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
	manifest, err := workspaces.Prepare(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	// Losing the original anchored directory rejects creation after the output
	// publisher starts. Its joined cancellation must remain visible in the
	// native result rather than silently claiming complete retained output.
	if err := os.Rename(manifest.PrimaryPath, manifest.PrimaryPath+"-moved"); err != nil {
		t.Fatal(err)
	}
	manager := newTerminalManager(ctx, Config{Root: root, Logger: logger}, nil, Credential{MachineID: input.MachineID}, domain.NewID())
	result := manager.execute(terminal.Assignment{ID: domain.NewID(), SessionID: input.SessionID, Terminal: domain.Terminal{State: domain.TerminalStarting, ShellOverride: "/bin/sh", Rows: 24, Columns: 80}, Operation: domain.TerminalOperation{Action: domain.TerminalCreate}, Preparation: &input, Manifest: &manifest})
	if !result.OutputLost || result.State != domain.TerminalUncertain || result.CleanupVerified || result.Problem == nil || len(manager.live) != 0 {
		t.Fatal("failed creation discarded output loss or manufactured ownership cleanup", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalFailedControlPreservesCleanupRecoveryProblem(t *testing.T) {
	for _, action := range []domain.TerminalAction{domain.TerminalInput, domain.TerminalResize} {
		t.Run(string(action), func(t *testing.T) {
			for _, corrupt := range []bool{false, true} {
				name := "confirmed-cleanup"
				if corrupt {
					name = "unconfirmed-cleanup"
				}
				t.Run(name, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					root, id := t.TempDir(), domain.NewID()
					logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
					manager := newTerminalManager(ctx, Config{Root: root, Logger: logger}, nil, Credential{}, domain.NewID())
					nativeCtx, nativeCancel := context.WithCancel(ctx)
					defer nativeCancel()
					handle, err := process.Start(nativeCtx, process.Config{Directory: manager.processRoot(), OwnerID: id, Executable: "/bin/sh", Args: []string{"-i"}, Cwd: t.TempDir(), Env: []string{"PATH=/usr/bin:/bin"}, Terminal: &process.TerminalSize{Rows: 24, Columns: 80}, Stdout: io.Discard, Stderr: io.Discard, Logger: logger})
					if err != nil {
						t.Fatal(err)
					}
					defer handle.Close()
					// Leave the native start barrier closed so input fails
					// deterministically; an invalid size similarly fails resize.
					outputDone := make(chan struct{})
					close(outputDone)
					manager.live[id] = &nativeTerminal{handle: handle, result: terminal.Result{State: domain.TerminalRunning, Shell: "/bin/sh", Rows: 24, Columns: 80}, cancel: nativeCancel, outputDone: outputDone, writer: &terminalWriter{chunks: make(chan []byte)}}
					unexpected := filepath.Join(manager.processRoot(), string(id), "unknown-owner-entry")
					if corrupt {
						if err := security.WriteAtomic(unexpected, []byte("untrusted ownership fixture")); err != nil {
							t.Fatal(err)
						}
						defer os.Remove(unexpected)
					}
					result := manager.execute(terminal.Assignment{ID: id, Operation: domain.TerminalOperation{ID: domain.NewID(), Action: action, Input: []byte("unsent"), Rows: 0, Columns: 80}})
					if corrupt {
						if result.State != domain.TerminalUncertain || result.CleanupVerified || result.Problem == nil || result.Problem.Code != domain.RecoveryRequired {
							t.Fatal("control error concealed unconfirmed ownership cleanup", result)
						}
						if err := os.Remove(unexpected); err != nil {
							t.Fatal(err)
						}
						if err := manager.reconcile(id); err != nil {
							t.Fatal("fixture did not restore independently confirmed cleanup", err)
						}
					} else {
						expected := domain.Conflict
						if action == domain.TerminalResize {
							expected = domain.InvalidArgument
						}
						if !result.CleanupVerified || result.Problem == nil || result.Problem.Code != expected {
							t.Fatal("confirmed cleanup lost the original control problem", result)
						}
					}
					if len(manager.live) != 0 {
						t.Fatal("failed control retained native input authority")
					}
				})
			}
		})
	}
}
