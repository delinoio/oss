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
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/terminal"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestTerminalFreshCloseReconcilesAfterUncertainAcknowledgement(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		name := "same-instance"
		if replacement {
			name = "replacement-instance"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			machine, instance := domain.NewID(), domain.NewID()
			a := terminal.Assignment{ID: domain.NewID(), SessionID: domain.NewID(), Terminal: domain.Terminal{MachineID: machine, OwnerInstanceID: instance, InstanceID: instance, State: domain.TerminalUncertain, Rows: 24, Columns: 80}, Operation: domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalClose}}
			fixture := &terminalCloseRecoveryFixture{assignment: a, loseReport: true}
			_, handler := delidevv1connect.NewWorkerServiceHandler(fixture)
			server := httptest.NewServer(handler)
			defer server.Close()
			m := newTerminalManager(ctx, Config{Root: t.TempDir(), Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, delidevv1connect.NewWorkerServiceClient(http.DefaultClient, server.URL), Credential{MachineID: machine}, instance)
			owner := filepath.Join(m.processRoot(), string(a.ID))
			for _, path := range []string{m.processRoot(), owner} {
				if err := security.PrivateDir(path); err != nil {
					t.Fatal(err)
				}
			}
			// The original positive empty owner index remains valid, but another
			// reconciliation holds its exclusive lock. Never infer cleanup from
			// missing ownership or repeat the uncertain operation's result.
			lock, err := security.TryLock(owner + ".recovery.lock")
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if err := m.apply(ctx, a); err == nil {
				t.Fatal("expected lost uncertain-report acknowledgement")
			}
			j, err := m.loadJournal(a)
			if err != nil || j.Result == nil || j.Result.State != domain.TerminalUncertain || j.Result.CleanupVerified {
				t.Fatal("held reconciliation lock manufactured cleanup", err)
			}
			original, _ := json.Marshal(j.Result)
			if err := m.apply(ctx, a); err != nil {
				t.Fatal("uncertain acknowledgement retry failed", err)
			}
			fixture.Lock()
			if len(fixture.reports) != 2 || fixture.reports[0].RequestId != fixture.reports[1].RequestId || !bytes.Equal(fixture.reports[0].ResultJson, fixture.reports[1].ResultJson) || !bytes.Equal(fixture.reports[0].ResultJson, original) {
				t.Error("uncertain acknowledgement retry changed original facts")
			}
			fixture.Unlock()
			if _, err := os.Stat(owner); err != nil {
				t.Fatal("uncertain acknowledgement discarded process ownership", err)
			}
			if _, err := os.Stat(m.journalPath(a.Operation.ID)); !os.IsNotExist(err) {
				t.Fatal("acknowledged old close retained executable operation metadata", err)
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
			if replacement {
				m = newTerminalManager(ctx, m.config, m.client, m.credential, domain.NewID())
			}
			a.Operation.ID = domain.NewID()
			fixture.Lock()
			fixture.assignment = a
			fixture.Unlock()
			if err := m.apply(ctx, a); err != nil {
				t.Fatal("fresh close could not independently reconcile original evidence", err)
			}
			fixture.Lock()
			defer fixture.Unlock()
			if len(fixture.claims) != 2 || len(fixture.reports) != 3 || fixture.claims[0].RequestId == fixture.claims[1].RequestId || fixture.reports[1].RequestId == fixture.reports[2].RequestId || fixture.reports[2].OperationId != string(a.Operation.ID) {
				t.Fatal("fresh reconciliation reused the original finished operation")
			}
			var closed terminal.Result
			if err := domain.Decode(fixture.reports[2].ResultJson, &closed); err != nil || closed.State != domain.TerminalClosed || !closed.CleanupVerified {
				t.Fatal("fresh reconciliation replayed the earlier uncertainty", err)
			}
			if _, err := os.Stat(owner); !os.IsNotExist(err) {
				t.Fatal("confirmed fresh cleanup retained the empty owner index", err)
			}
		})
	}
}
