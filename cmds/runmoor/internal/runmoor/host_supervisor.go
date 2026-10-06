package runmoor

import (
	"context"
	"os"
	"time"
)

type HostWorkerNative interface {
	Start(context.Context, *os.Root, HostBootstrap) (HostProcess, <-chan int, error)
	Members(HostProcess) ([]HostProcess, error)
	Terminate(HostProcess, bool) error
	Deadline(HostBootstrap, time.Time) time.Time
}

const hostDeadlinePollInterval = 5 * time.Second

// Detached supervision continues across a manager-only restart. Completion
// is published only after every process remaining in the owned group exits.
func superviseHost(ctx context.Context, root *os.Root, in HostBootstrap, status HostExecutionStatus, worker HostWorkerNative) int {
	if ctx.Err() != nil || !time.Now().Before(in.Deadline) {
		status.Phase = HostFailed
		status.ExitCode = 1
		_ = hostRootWrite(root, "status.json", status)
		return 1
	}
	process, done, err := worker.Start(ctx, root, in)
	if err != nil {
		status.Phase = HostFailed
		status.ExitCode = 1
		_ = hostRootWrite(root, "status.json", status)
		return 1
	}
	status.Worker = process
	status.Phase = HostStarting
	if err = hostRootWrite(root, "status.json", status); err != nil {
		// Keep supervising even after a publication failure. Losing a state write
		// never authorizes abandoning children or deleting their workspace.
		return finishHostGroup(context.Background(), root, status, worker, done, true)
	}
	startup := time.NewTimer(time.Second)
	defer startup.Stop()
	deadline := time.NewTimer(time.Until(in.Deadline))
	defer deadline.Stop()
	select {
	case code := <-done:
		status.ExitCode = code
		status.Phase = HostFailed
		return finishHostGroup(context.Background(), root, status, worker, nil, true)
	case <-ctx.Done():
		return finishHostGroup(context.Background(), root, status, worker, done, true)
	case <-deadline.C:
		return finishHostGroup(context.Background(), root, status, worker, done, true)
	case <-startup.C:
	}
	select {
	case code := <-done:
		status.ExitCode = code
		status.Phase = HostFailed
		return finishHostGroup(context.Background(), root, status, worker, nil, true)
	default:
	}
	status.Phase = HostRunning
	if hostRootWrite(root, "status.json", status) != nil {
		return finishHostGroup(context.Background(), root, status, worker, done, true)
	}
	// SQLite reads are bounded to one per interval while idle. A separate timer
	// enforces an observed job deadline without polling the database at 10 Hz.
	check := time.NewTicker(hostDeadlinePollInterval)
	defer check.Stop()
	// The bootstrap deadline bounds startup only. Idle runners have no job
	// deadline until assignment or busy-aware removal establishes Busy state.
	jobDeadline := worker.Deadline(in, time.Time{})
	deadline.Stop()
	var jobTimeout <-chan time.Time
	armDeadline := func() {
		if !jobDeadline.IsZero() {
			deadline.Reset(time.Until(jobDeadline))
			jobTimeout = deadline.C
		}
	}
	armDeadline()
	for {
		select {
		case code := <-done:
			status.ExitCode = code
			return finishHostGroup(context.Background(), root, status, worker, nil, false)
		case <-ctx.Done():
			return finishHostGroup(context.Background(), root, status, worker, done, true)
		case <-jobTimeout:
			// Recheck durable authority before enforcing the cached deadline.
			jobDeadline = worker.Deadline(in, jobDeadline)
			if !time.Now().Before(jobDeadline) {
				return finishHostGroup(context.Background(), root, status, worker, done, true)
			}
			armDeadline()
		case <-check.C:
			// Assignment resets the durable six-hour job deadline. Preparation or
			// idle time must not shorten a subsequently observed busy job's timeout.
			if next := worker.Deadline(in, jobDeadline); !next.Equal(jobDeadline) {
				jobDeadline = next
				armDeadline()
			}
		}
	}
}

func hostJobDeadline(in HostBootstrap, previous time.Time, s Snapshot) time.Time {
	r := s.Runners[in.Directory.ID]
	d := s.HostDirectories[in.Directory.ID]
	if s.Installation != in.Directory.Installation || r == nil || d == nil || *d != in.Directory || r.Phase != Busy || r.Deadline.IsZero() {
		return previous
	}
	return r.Deadline
}

func finishHostGroup(ctx context.Context, root *os.Root, status HostExecutionStatus, worker HostWorkerNative, done <-chan int, failed bool) int {
	until := time.Now().Add(5 * time.Second)
	// Uncertain termination deliberately keeps this supervisor and its ownership
	// record alive. The manager's bounded cleanup reports pending and reserves it.
	for {
		if done != nil {
			select {
			case code := <-done:
				status.ExitCode = code
				done = nil
			default:
			}
		}
		members, err := worker.Members(status.Worker)
		if err == nil && len(members) == 0 && done == nil {
			break
		}
		if err == nil {
			for _, p := range members {
				_ = worker.Terminate(p, time.Now().After(until))
			}
		}
		if !waitContext(ctx, 50*time.Millisecond) {
			return 1
		}
	}
	if failed || status.Phase == HostFailed {
		status.Phase = HostFailed
		if status.ExitCode == 0 {
			status.ExitCode = 1
		}
	} else {
		status.Phase = HostFinished
	}
	if hostRootWrite(root, "status.json", status) != nil {
		return 1
	}
	return 0
}
