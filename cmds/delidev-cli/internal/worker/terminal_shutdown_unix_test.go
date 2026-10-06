//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
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

type terminalShutdownFixture struct {
	terminalTransportFixture
	blocked    chan struct{}
	prefix     chan struct{}
	prefixOnce sync.Once
	once       sync.Once
}

func (f *terminalShutdownFixture) PublishTerminalOutput(ctx context.Context, req *connect.Request[pb.PublishTerminalOutputRequest]) (*connect.Response[pb.PublishTerminalOutputResponse], error) {
	if req.Msg.Sequence == 1 {
		f.prefixOnce.Do(func() { close(f.prefix) })
		return connect.NewResponse(&pb.PublishTerminalOutputResponse{}), nil
	}
	f.once.Do(func() { close(f.blocked) })
	<-ctx.Done()
	return nil, connect.NewError(connect.CodeCanceled, nil)
}

func TestTerminalShutdownLossSurvivesReplacementCloseAndResponseLoss(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	root := filepath.Join(t.TempDir(), "worker")
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	input := workspace.PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
	manifest, err := (&workspace.Manager{Root: root, Logger: logger}).Prepare(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	instance := domain.NewID()
	a := terminal.Assignment{ID: domain.NewID(), SessionID: input.SessionID, Terminal: domain.Terminal{MachineID: input.MachineID, InstanceID: instance, OwnerInstanceID: instance, ShellOverride: "/bin/sh", State: domain.TerminalStarting, Rows: 24, Columns: 80}, Operation: domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalCreate}, Preparation: &input, Manifest: &manifest}
	f := &terminalShutdownFixture{terminalTransportFixture: terminalTransportFixture{assignments: map[domain.ID]terminal.Assignment{a.Operation.ID: a}, claims: map[string]bool{}, reports: map[string][]byte{}, lose: map[domain.ID]bool{}}, blocked: make(chan struct{}), prefix: make(chan struct{})}
	_, handler := delidevv1connect.NewWorkerServiceHandler(f)
	server := httptest.NewServer(handler)
	defer server.Close()
	client := delidevv1connect.NewWorkerServiceClient(http.DefaultClient, server.URL)
	m := newTerminalManager(ctx, Config{Root: root, Logger: logger}, client, Credential{MachineID: input.MachineID}, instance)
	defer m.close()
	if err := m.apply(ctx, a); err != nil {
		t.Fatal(err)
	}
	// PTY startup and command echo can share one native read. Wait for an
	// independently captured prefix before sending the suffix whose reply blocks.
	if _, err := m.live[a.ID].handle.Write([]byte("printf 'prefix'\r")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.prefix:
	case <-ctx.Done():
		t.Fatal("output did not publish its original prefix")
	}
	if _, err := m.live[a.ID].handle.Write([]byte("printf 'suffix'\r")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.blocked:
	case <-ctx.Done():
		t.Fatal("output did not retain a prefix before blocking its suffix")
	}
	m.close()
	if len(m.live) != 0 {
		t.Fatal("shutdown did not synchronize its joined result")
	}
	a.Terminal.State, a.Terminal.Shell, a.Terminal.Cwd = domain.TerminalUncertain, "/bin/sh", manifest.PrimaryPath
	a.Preparation, a.Manifest = nil, nil
	a.Operation = domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalClose}
	f.Lock()
	f.assignments[a.Operation.ID], f.lose[a.Operation.ID] = a, true
	f.Unlock()
	replacement := newTerminalManager(ctx, m.config, client, m.credential, domain.NewID())
	if err := replacement.apply(ctx, a); err == nil {
		t.Fatal("expected lost close response")
	}
	j, err := replacement.loadJournal(a)
	if err != nil || j.Result == nil || !j.Result.OutputLost || !j.Result.CleanupVerified || j.Result.State != domain.TerminalClosed {
		t.Fatal("replacement dropped shutdown loss or cleanup", err, j.Result)
	}
	before, _ := json.Marshal(j.Result)
	if err := replacement.apply(ctx, a); err != nil {
		t.Fatal(err)
	}
	f.Lock()
	defer f.Unlock()
	if string(f.reports[string(j.ReportID)]) != string(before) {
		t.Fatal("close retry changed retained loss result")
	}
}

func TestTerminalShutdownRecordsEveryOwnerBeforeAnyCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	root := filepath.Join(t.TempDir(), "worker")
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	input := workspace.PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
	manifest, err := (&workspace.Manager{Root: root, Logger: logger}).Prepare(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	f := &terminalTransportFixture{assignments: map[domain.ID]terminal.Assignment{}, claims: map[string]bool{}, reports: map[string][]byte{}, lose: map[domain.ID]bool{}}
	_, handler := delidevv1connect.NewWorkerServiceHandler(f)
	server := httptest.NewServer(handler)
	defer server.Close()
	m := newTerminalManager(ctx, Config{Root: root, Logger: logger}, delidevv1connect.NewWorkerServiceClient(http.DefaultClient, server.URL), Credential{MachineID: input.MachineID}, domain.NewID())
	defer m.close()
	ids := []domain.ID{domain.NewID(), domain.NewID()}
	for _, id := range ids {
		a := terminal.Assignment{ID: id, SessionID: input.SessionID, Terminal: domain.Terminal{MachineID: input.MachineID, InstanceID: m.instance, OwnerInstanceID: m.instance, ShellOverride: "/bin/sh", State: domain.TerminalStarting, Rows: 24, Columns: 80}, Operation: domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalCreate}, Preparation: &input, Manifest: &manifest}
		f.Lock()
		f.assignments[a.Operation.ID] = a
		f.Unlock()
		if err := m.apply(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	var cancellations atomic.Int32
	for _, id := range ids {
		native := m.live[id]
		originalCancel := native.cancel
		native.cancel = func() {
			if cancellations.Add(1) == 1 {
				for _, other := range ids {
					raw, err := security.ReadPrivate(m.shutdownPath(other), terminalOperationJournalMaxBytes)
					var record terminalShutdownRecord
					if err != nil || domain.Decode(raw, &record) != nil || record.TerminalID != other || record.OwnerInstanceID != m.instance || !record.Result.OutputLost || record.Result.CleanupVerified || record.Result.State != domain.TerminalUncertain {
						t.Error("first cancellation preceded another terminal's synchronized loss", err)
					}
				}
			}
			originalCancel()
		}
	}
	m.close()
	if len(m.live) != 0 {
		t.Fatal("shutdown did not join both independent owners")
	}
	for _, id := range ids {
		raw, err := security.ReadPrivate(m.shutdownPath(id), terminalOperationJournalMaxBytes)
		var record terminalShutdownRecord
		if err != nil || domain.Decode(raw, &record) != nil || !record.Result.CleanupVerified {
			t.Fatal("joined shutdown did not retain independent cleanup", err)
		}
	}
}
