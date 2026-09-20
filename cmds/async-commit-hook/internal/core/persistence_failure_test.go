package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This storage-failure fixture needs a real owned process, but does not test a
// shell engine. Re-exec the test binary so Windows cold PowerShell startup cannot
// consume the watchdog before the injected collecting/passed writes are reached.
// Native PowerShell behavior remains covered by ordinary runner integration tests.
func init() {
	if len(os.Args) == 3 && os.Args[1] == "-c" && os.Args[2] == "__ach_test_persistence_command" && os.Getenv("ACH_MANAGED") == "1" {
		fmt.Fprintln(os.Stdout, "persistence fixture completed")
		os.Exit(0)
	}
}

func TestPreparationFailureCannotFinalizeAfterCheckSaveFailure(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand='unused'\n")
	receipt, err := s.Submit(context.Background(), repo, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(repo, repo+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	_, err = s.Store.DB.Exec("CREATE TRIGGER fail_preparation_check BEFORE UPDATE ON checks WHEN NEW.state='failed' BEGIN SELECT RAISE(FAIL,'injected preparation write failure'); END")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RunOne(receipt.RunID); err == nil || !strings.Contains(err.Error(), "injected preparation write failure") {
		t.Fatal("check save error was discarded", err)
	}
	r, err := s.Store.Run(receipt.RunID)
	if err != nil || r.State.Terminal() || r.Checks[0].State != Queued {
		t.Fatal("inconsistent terminal run published", r, err)
	}
	pending, err := s.Store.Pending()
	if err != nil || len(pending) != 1 || pending[0] != r.ID {
		t.Fatal("recovery lost pending run", pending, err)
	}
	if _, err = s.Store.DB.Exec("DROP TRIGGER fail_preparation_check"); err != nil {
		t.Fatal(err)
	}
	if err = s.RunOne(r.ID); err != nil {
		t.Fatal("preparation recovery failed", err)
	}
	r, err = s.Store.Run(r.ID)
	if err != nil || r.State != Failed || r.Checks[0].State != Failed {
		t.Fatal("recovery did not finish failed preparation", r, err)
	}
	if len(r.Checks[0].Diagnostics) != 1 || r.Checks[0].Diagnostics[0].Code != "workspace-preparation-failed" {
		t.Fatal("per-check diagnostic was lost", r.Checks[0])
	}
}

func TestCheckPersistenceFailureReleasesWorkerForReconciliation(t *testing.T) {
	for _, state := range []State{Running, Collecting, Passed} {
		t.Run(string(state), func(t *testing.T) {
			s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"__ach_test_persistence_command\"\n")
			receipt, err := s.Submit(context.Background(), repo, "", false)
			if err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			accepted, err := s.Store.Run(receipt.RunID)
			if err != nil {
				t.Fatal(err)
			}
			// Override only this isolated fixture's persisted launch adapter;
			// accepted configuration validation and production shell enums stay strict.
			accepted.Checks[0].Shell = Shell(executable)
			if err = s.Store.SaveCheck(accepted.Checks[0]); err != nil {
				t.Fatal(err)
			}
			// Only this state write fails: reads and later recovery remain available.
			_, err = s.Store.DB.Exec("CREATE TRIGGER fail_check_save BEFORE UPDATE ON checks WHEN NEW.state='" + string(state) + "' BEGIN SELECT RAISE(FAIL,'injected write failure'); END")
			if err != nil {
				t.Fatal(err)
			}
			// This remains a deadlock watchdog for worker joining/reconciliation;
			// it is not a product command timeout or a native-shell startup SLA.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			err = s.runOne(ctx, receipt.RunID)
			if err == nil || ctx.Err() != nil || !strings.Contains(err.Error(), "injected write failure") {
				snapshot, readErr := s.Store.Run(receipt.RunID)
				t.Fatalf("worker did not return its persistence error: %v; watchdog=%v read=%v run=%s checks=%+v", err, ctx.Err(), readErr, snapshot.State, snapshot.Checks)
			}
			lock, err := TryLock(filepath.Join(s.Store.Root, "locks", receipt.RunID+".lock"))
			if err != nil || lock == nil {
				t.Fatal("worker ownership leaked", err)
			}
			lock.Close()
			r, err := s.Store.Run(receipt.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if r.State.Terminal() || s.GateRun(r).Passed {
				t.Fatal("failed persistence published success")
			}
			if ProcessAlive(r.Checks[0].Process) {
				t.Fatal("failed persistence left its command alive")
			}
			if state != Running {
				path, err := s.Store.EvidencePath(r.ID, r.Checks[0].Log.ID)
				if err != nil {
					t.Fatal(err)
				}
				output, err := os.ReadFile(path)
				if err != nil || string(output) != "persistence fixture completed\n" {
					t.Fatalf("fixture did not finish before injected write: %q %v", output, err)
				}
			}
			if _, err = s.Store.DB.Exec("DROP TRIGGER fail_check_save"); err != nil {
				t.Fatal(err)
			}
			if err = s.RunOne(r.ID); err != nil {
				t.Fatal(err)
			}
			r, err = s.Store.Run(r.ID)
			if err != nil || r.State != Interrupted {
				t.Fatal("recovery did not interrupt ambiguous execution", err)
			}
		})
	}
}
