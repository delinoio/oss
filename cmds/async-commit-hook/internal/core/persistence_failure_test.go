package core

import (
	"context"
	"path/filepath"
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
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			err = s.runOne(ctx, receipt.RunID)
			if err == nil || ctx.Err() != nil {
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
