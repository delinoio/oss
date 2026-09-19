package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckPersistenceFailureReleasesWorkerForReconciliation(t *testing.T) {
	for _, state := range []State{Running, Collecting, Passed} {
		t.Run(string(state), func(t *testing.T) {
			s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"echo test\"\n")
			receipt, err := s.Submit(context.Background(), repo, "", false)
			if err != nil {
				t.Fatal(err)
			}
			// Only this state write fails: reads and later recovery remain available.
			_, err = s.Store.DB.Exec("CREATE TRIGGER fail_check_save BEFORE UPDATE ON checks WHEN NEW.state='" + string(state) + "' BEGIN SELECT RAISE(FAIL,'injected write failure'); END")
			if err != nil {
				t.Fatal(err)
			}
			// This is a deadlock watchdog, not a command-startup SLA. Native
			// PowerShell cold starts exceeded 15 seconds on loaded Windows CI
			// runners before the injected collecting/passed write was reached.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			err = s.runOne(ctx, receipt.RunID)
			if err == nil || ctx.Err() != nil || !strings.Contains(err.Error(), "injected write failure") {
				t.Fatalf("worker did not return its persistence error: %v", err)
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
