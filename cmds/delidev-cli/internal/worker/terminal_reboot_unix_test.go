// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package worker

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/terminal"
)

func TestTerminalReportedRebootCleanupRetiresOnLocalRetry(t *testing.T) {
	ctx := context.Background()
	m := newTerminalManager(ctx, Config{Root: t.TempDir(), Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, nil, Credential{}, domain.NewID())
	id, operation := domain.NewID(), domain.NewID()
	owner := filepath.Join(m.processRoot(), string(id))
	scope := filepath.Join(owner, string(domain.NewID()))
	for _, path := range []string{filepath.Join(m.config.Root, "terminal-operations"), scope} {
		if err := security.PrivateDir(path); err != nil {
			t.Fatal(err)
		}
	}
	// This is an old-boot metadata fixture, not a reboot or native shell test.
	journal := struct {
		Version  int             `json:"version"`
		OwnerID  domain.ID       `json:"owner_id"`
		Owner    process.Process `json:"owner"`
		Boot     string          `json:"boot"`
		Started  bool            `json:"started"`
		Complete bool            `json:"complete"`
	}{Version: 1, OwnerID: id, Owner: process.Process{PID: 2147483647, Birth: "fixture-supervisor"}, Boot: "fixture-previous-boot", Started: true}
	raw, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(scope, "ownership.json")
	if err := security.WriteAtomic(path, raw); err != nil {
		t.Fatal(err)
	}
	controller, err := security.TryLock(filepath.Join(scope, "controller.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	result := terminal.Result{State: domain.TerminalClosed, CleanupVerified: true, OutputLost: true, Rows: 24, Columns: 80}
	j := terminalOperationJournal{TerminalID: id, OperationID: operation, InstanceID: m.instance, ReportID: domain.NewID(), Phase: terminalReported, Result: &result}
	if err := m.saveJournal(j); err != nil {
		t.Fatal(err)
	}
	if err := m.saveShutdown(id, result); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := m.retireReported(j); err == nil {
			t.Fatal("held controller permitted terminal retirement")
		}
		raw, err := security.ReadPrivate(path, 1<<20)
		if err != nil || domain.Decode(raw, &journal) != nil || !journal.Complete || journal.OwnerID != id || journal.Boot != "fixture-previous-boot" {
			t.Fatal("terminal reconciliation did not retain original reboot completion", err)
		}
		for _, path := range []string{owner, m.shutdownPath(id), m.journalPath(operation)} {
			if _, err := os.Stat(path); err != nil {
				t.Fatal("interrupted retirement discarded terminal ownership", err)
			}
		}
	}
	if err := controller.Close(); err != nil {
		t.Fatal(err)
	}
	// A replacement has no server client: acknowledged retirement is local only.
	replacement := newTerminalManager(ctx, m.config, nil, m.credential, domain.NewID())
	defer replacement.close()
	replacement.observeExits(ctx)
	for _, path := range []string{owner, owner + ".recovery.lock", m.shutdownPath(id), m.journalPath(operation)} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("reboot-proven cleanup retained acknowledged terminal metadata", err)
		}
	}
}
