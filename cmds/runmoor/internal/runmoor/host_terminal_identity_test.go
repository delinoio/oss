package runmoor

import (
	"context"
	"os"
	"testing"
)

func TestHostTerminalStatusRetainsRecordedWorker(t *testing.T) {
	for _, phase := range []HostExecutionPhase{HostFinished, HostFailed} {
		for _, change := range []string{"omitted", "PID", "start", "group"} {
			t.Run(string(phase)+"/"+change, func(t *testing.T) {
				c, store, native, p := hostFixture(t)
				p = buildHostFixture(t, c, store, native, p)
				r := prepareHostFixture(t, c, store, native, p)
				state := store.View()
				execution := *state.HostExecutions[r.ID]
				directory := *state.HostDirectories[r.ID]
				if execution.Worker.PID == 0 {
					t.Fatal("fixture did not journal a worker")
				}
				status := native.statuses[execution.Supervisor.PID]
				status.Phase = phase
				switch change {
				case "omitted":
					status.Worker = HostProcess{}
				case "PID":
					status.Worker.PID++
				case "start":
					status.Worker.Start = "changed"
				case "group":
					status.Worker.Group++
				}
				if err := hostRootWrite(native.roots[execution.Supervisor.PID], "status.json", status); err != nil {
					t.Fatal(err)
				}
				// Only the supervisor has exited. The original worker/group remains live.
				delete(native.alive, execution.Supervisor.PID)
				driver := HostDriver{Store: store, Native: native}
				_, err := driver.Inspect(context.Background(), c, r, state)
				requireCode(t, err, ErrOwnership)
				requireCode(t, driver.Stop(context.Background(), c, r, state), ErrOwnership)
				if err := driver.Cleanup(context.Background(), c, r, store.View()); err == nil {
					t.Fatal("unconfirmed worker workspace was deleted")
				}
				final := store.View()
				if *final.HostExecutions[r.ID] != execution || *final.HostDirectories[r.ID] != directory || final.Runners[r.ID].Resources != r.Resources {
					t.Fatal("terminal status changed durable execution ownership")
				}
				if native.stopped != 0 || !sameHostProcess(native.alive[execution.Worker.PID], execution.Worker) || len(native.members[execution.Worker.Group]) == 0 {
					t.Fatal("uncertain status signaled or lost original worker")
				}
				if _, err := os.Stat(hostDirectoryPath(c, directory)); err != nil {
					t.Fatal("original workspace was not preserved", err)
				}
				if reserved, _, _ := usage(final); reserved.CPU < r.Resources.CPU || reserved.MemoryMiB < r.Resources.MemoryMiB {
					t.Fatal("worker reservation was released")
				}
			})
		}
	}
}

func TestHostTerminalMatchingWorkerAndPreWorkerFailureCleanup(t *testing.T) {
	for _, scenario := range []string{"finished", "failed", "pre-worker failure"} {
		t.Run(scenario, func(t *testing.T) {
			c, store, native, p := hostFixture(t)
			p = buildHostFixture(t, c, store, native, p)
			driver := HostDriver{Store: store, Native: native}
			var r Runner
			if scenario == "pre-worker failure" {
				native.phase = HostFailed
				native.writeStatus = func(root *os.Root, status HostExecutionStatus) error {
					status.Worker = HostProcess{}
					return hostRootWrite(root, "status.json", status)
				}
				r = seedHostFixture(t, c, store, p)
				requireCode(t, driver.Prepare(context.Background(), c, p, r, store.View(), "fixture-jit", func(Handle) error { return nil }), ErrPreparation)
				if store.View().HostExecutions[r.ID].Worker.PID != 0 {
					t.Fatal("pre-worker failure journaled a worker")
				}
			} else {
				r = prepareHostFixture(t, c, store, native, p)
				execution := store.View().HostExecutions[r.ID]
				status := native.statuses[execution.Supervisor.PID]
				status.Phase = HostFinished
				if scenario == "failed" {
					status.Phase = HostFailed
				}
				if err := hostRootWrite(native.roots[execution.Supervisor.PID], "status.json", status); err != nil {
					t.Fatal(err)
				}
				delete(native.alive, execution.Supervisor.PID)
				delete(native.alive, execution.Worker.PID)
				delete(native.members, execution.Worker.Group)
			}
			directory := *store.View().HostDirectories[r.ID]
			if err := driver.Stop(context.Background(), c, r, store.View()); err != nil {
				t.Fatal(err)
			}
			if err := driver.Cleanup(context.Background(), c, r, store.View()); err != nil {
				t.Fatal(err)
			}
			execution := store.View().HostExecutions[r.ID]
			if !execution.Terminated || !execution.Cleaned || native.stopped != 0 {
				t.Fatal("verified terminal cleanup failed")
			}
			if _, err := os.Stat(hostDirectoryPath(c, directory)); !os.IsNotExist(err) {
				t.Fatal("verified terminated workspace was retained", err)
			}
		})
	}
}
