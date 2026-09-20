package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

func (s *Service) claim(r Run, c Check) (bool, error) {
	claimed := false
	err := s.Store.Transaction(func(tx *sql.Tx) error {
		var state State
		var cancel string
		if err := tx.QueryRow("SELECT state,cancel FROM checks WHERE id=?", c.ID).Scan(&state, &cancel); err != nil {
			return err
		}
		if state != Queued || cancel != "" {
			return nil
		}
		if c.Policy != Parallel {
			if c.Policy == Replace {
				// The incoming attempt cannot displace a newer accepted attempt.
				var newer int
				if err := tx.QueryRow("SELECT count(*) FROM checks c JOIN runs r ON r.id=c.run_id WHERE c.group_key=? AND r.seq>? AND c.state IN ('preparing','running','collecting')", c.Group, r.Sequence).Scan(&newer); err != nil {
					return err
				}
				if newer > 0 {
					_, err := tx.Exec("UPDATE checks SET cancel='replaced' WHERE id=?", c.ID)
					return err
				}
				if _, err := tx.Exec("UPDATE checks SET cancel='replaced' WHERE group_key=? AND id<>? AND run_id IN (SELECT id FROM runs WHERE seq<?) AND state IN ('queued','preparing','running','collecting')", c.Group, c.ID, r.Sequence); err != nil {
					return err
				}
			}
			var active int
			if err := tx.QueryRow("SELECT count(*) FROM checks WHERE group_key=? AND id<>? AND state IN ('preparing','running','collecting')", c.Group, c.ID).Scan(&active); err != nil {
				return err
			}
			if active > 0 {
				return nil
			}
			if c.Policy == Queue {
				var earlier int
				if err := tx.QueryRow("SELECT count(*) FROM checks c JOIN runs r ON c.run_id=r.id WHERE c.group_key=? AND r.seq<? AND c.state='queued' AND c.cancel=''", c.Group, r.Sequence).Scan(&earlier); err != nil {
					return err
				}
				if earlier > 0 {
					return nil
				}
			}
		}
		c.State = Preparing
		if err := saveCheck(tx, c); err != nil {
			return err
		}
		claimed = true
		return nil
	})
	return claimed, err
}
func (s *Service) finishCheck(c Check, state State, code, message string) error {
	c.State = state
	t := time.Now().UTC()
	c.FinishedAt = &t
	if code != "" {
		c.Diagnostics = append(c.Diagnostics, Diagnostic{Code: code, Message: message})
	}
	var runID string
	err := s.Store.Transaction(func(tx *sql.Tx) error {
		var reason State
		// The worker has reconciled owned processes before reaching this point.
		// Serialize cancellation with publication so a request committed during
		// evidence collection cannot be overwritten by a stale successful result.
		if err := tx.QueryRow("SELECT run_id,cancel FROM checks WHERE id=?", c.ID).Scan(&runID, &reason); err != nil {
			return err
		}
		if reason != "" {
			c.State = reason
		}
		return saveCheck(tx, c)
	})
	if err == nil {
		s.Log.Info("check.finished", "run_id", runID, "check_id", c.ID, "state", c.State)
	}
	return err
}
func (s *Service) execute(ctx context.Context, r Run, c Check, workspace string) error {
	command := r.Config.Checks[c.Name]
	env, secrets, err := s.Personal.CommandEnvironment(command, r.Environment)
	if err != nil {
		return s.finishCheck(c, Failed, "environment-unavailable", err.Error())
	}
	if err = clearReportOutputs(workspace, command.Reports); err != nil {
		return s.finishCheck(c, Failed, "report-preparation-failed", "declared report outputs could not be cleared safely")
	}
	c.Log = Evidence{ID: ID(), Name: "combined.log"}
	logPath, _ := s.Store.EvidencePath(r.ID, c.Log.ID)
	if err = PrivateDir(filepath.Dir(logPath)); err != nil {
		return s.finishCheck(c, Failed, "evidence-write-failed", "cannot create log directory")
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return s.finishCheck(c, Failed, "evidence-write-failed", "cannot open log")
	}
	defer f.Close()
	redactor := NewRedactor(f, secrets)
	finishUnstarted := func(state State, code, message string) error {
		if diagnostic := finalizeCapturedLog(f, redactor, &c.Log); diagnostic != nil {
			c.Diagnostics = append(c.Diagnostics, *diagnostic)
		}
		return s.finishCheck(c, state, code, message)
	}
	args := []string{"-c", command.Command}
	if c.Shell == PowerShell || c.Shell == Pwsh {
		args = []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command.Command}
	}
	cmd := exec.Command(string(c.Shell), args...)
	cmd.Dir = workspace
	cmd.Env = append(env, "ACH_MANAGED=1")
	cmd.Stdin = nil
	cmd.Stdout = redactor
	cmd.Stderr = redactor
	if cancel := s.Store.Cancellation(c.ID); cancel != "" {
		return finishUnstarted(cancel, "", "")
	}
	scopeDir := filepath.Join(s.Store.Root, "evidence", r.ID, "processes", c.ID)
	if runtime.GOOS != "windows" {
		c.State = Preparing
		c.Process.ScopeDir = scopeDir
		if err = s.Store.SaveCheck(c); err != nil {
			return err
		}
	}
	p, err := startProcess(cmd, scopeDir)
	if err != nil {
		s.Log.Error("process.owner_start_failed", "run_id", r.ID, "check_id", c.ID, "platform", runtime.GOOS, "error", err.Error())
		return finishUnstarted(Failed, "command-start-failed", "cannot start selected shell; run ach doctor")
	}
	defer p.close()
	if runtime.GOOS != "windows" {
		_, leave, leaseErr := s.enterProcess("check-supervisor", p.identity)
		if leaseErr != nil {
			return leaseErr
		}
		defer func() {
			// Keep the account lease if descendant reconciliation failed, even
			// when the original run worker is about to disappear.
			if p.terminate() == nil {
				leave()
			}
		}()
	}
	c.Process = p.identity
	c.State = Running
	t := time.Now().UTC()
	c.StartedAt = &t
	if err = s.Store.SaveCheck(c); err != nil {
		if terminationErr := p.terminate(); terminationErr != nil {
			return terminationErr
		}
		_ = p.wait()
		return err
	}
	s.Log.Info("check.started", "run_id", r.ID, "check_id", c.ID, "check", c.Name)
	done := make(chan error, 1)
	if err = p.resume(); err != nil {
		_ = p.terminate()
		return err
	}
	go func() { done <- p.wait() }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var waitErr error
	cancel := State("")
	terminated := false
loop:
	for {
		select {
		case waitErr = <-done:
			break loop
		case <-ctx.Done():
			// Storage failures must not leave commands running while their owner
			// exits. This path works even when SQLite cannot record cancellation.
			cancel = Interrupted
			if err = p.terminate(); err != nil {
				s.Log.Error("process.reconciliation_failed", "run_id", r.ID, "check_id", c.ID, "code", "process-reconciliation-failed")
				return err
			}
			waitErr = <-done
			break loop
		case <-ticker.C:
			if err = p.sample(); err != nil {
				s.Log.Error("process.sample_failed", "run_id", r.ID, "check_id", c.ID, "code", "process-reconciliation-failed")
			}
			c.Process = p.snapshot()
			if saveErr := s.Store.SaveCheck(c); saveErr != nil {
				terminationErr := p.terminate()
				if terminationErr != nil {
					return terminationErr
				}
				<-done
				return saveErr
			}
			if reason := s.Store.Cancellation(c.ID); reason != "" && !terminated {
				cancel = reason
				if err = p.terminate(); err != nil {
					c.Diagnostics = append(c.Diagnostics, Diagnostic{Code: "process-reconciliation-failed", Message: "owned descendants remain active; cancellation is not complete"})
					_ = s.Store.SaveCheck(c)
				} else {
					terminated = true
				}
			}
		}
	}
	// A successful shell exit does not transfer ownership of background descendants.
	if err = p.terminate(); err != nil {
		c.Diagnostics = append(c.Diagnostics, Diagnostic{Code: "process-reconciliation-failed", Message: "owned descendants remain active"})
		_ = s.Store.SaveCheck(c)
		return err
	}
	diagnostic := finalizeCapturedLog(f, redactor, &c.Log)
	c.State = Collecting
	if saveErr := s.Store.SaveCheck(c); saveErr != nil {
		return saveErr
	}
	if diagnostic != nil {
		return s.finishCheck(c, Failed, diagnostic.Code, diagnostic.Message)
	}
	exit := 0
	if waitErr != nil {
		exit = -1
		if ee, ok := waitErr.(interface{ ExitCode() int }); ok {
			exit = ee.ExitCode()
		}
	}
	c.ExitCode = &exit
	for _, report := range command.Reports {
		b, e := ReadOwned(workspace, report.Path, maxReportBytes)
		if e != nil {
			c.Diagnostics = append(c.Diagnostics, Diagnostic{Code: "report-missing", Message: "declared report unavailable: " + report.Path})
			continue
		}
		// Report syntax is not user text: a secret may itself be a quote or an
		// XML token. Parse only in memory, then redact extracted text and the
		// evidence copy before either reaches persistent storage.
		failures, truncated, e := parseReportSummaries(report.Kind, b, c.Name, command.Command, c.Log.ID, secrets)
		if e != nil {
			c.Diagnostics = append(c.Diagnostics, Diagnostic{Code: "report-malformed", Message: "declared report invalid: " + report.Path})
		}
		for i := range failures {
			failures[i].ID = Hash(Encode([]string{string(report.Kind), report.Path, failures[i].ID}))

		}
		if truncated {
			c.Diagnostics = append(c.Diagnostics, Diagnostic{Code: "failure-summaries-truncated", Message: "Structured failure summaries were truncated; inspect the paginated report evidence for full details."})
		}
		c.Failures = append(c.Failures, failures...)
		ev, e := s.saveRedactedEvidence(r.ID, report.Path, b, secrets)
		if e != nil {
			c.Diagnostics = append(c.Diagnostics, Diagnostic{Code: "report-collection-failed", Message: "report could not be persisted"})
		} else {
			c.Reports = append(c.Reports, ev)
		}
	}
	state := Passed
	if exit != 0 || len(c.Failures) > 0 || len(c.Diagnostics) > 0 {
		state = Failed
	}
	if exit != 0 && len(c.Failures) == 0 {
		c.Failures = append(c.Failures, Failure{ID: Hash([]byte(c.Name + "/exit")), Check: c.Name, Command: command.Command, Message: fmt.Sprintf("command exited with status %d", exit), LogID: c.Log.ID})
	}
	if cancel != "" {
		state = cancel
	}
	return s.finishCheck(c, state, "", "")
}

// Even a command that never starts owns an empty log. Finish its durable
// bytes before terminal publication so comparison can verify that evidence.
func finalizeCapturedLog(f *os.File, redactor *Redactor, evidence *Evidence) *Diagnostic {
	err := redactor.Close()
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return &Diagnostic{Code: "evidence-write-failed", Message: "captured output could not be persisted"}
	}
	if err = digestFile(f.Name(), evidence); err != nil {
		return &Diagnostic{Code: "evidence-read-failed", Message: "captured output could not be verified"}
	}
	return nil
}

func digestFile(path string, e *Evidence) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, f)
	if err != nil {
		return err
	}
	e.Size = size
	e.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return nil
}
func (s *Service) RunOne(id string) error {
	return s.runOne(context.Background(), id)
}
func (s *Service) runOne(ctx context.Context, id string) error {
	ctx, cancelOwned := context.WithCancel(ctx)
	defer cancelOwned()
	lock, err := TryLock(filepath.Join(s.Store.Root, "locks", id+".lock"))
	if err != nil || lock == nil {
		return err
	}
	defer lock.Close()
	r, err := s.Store.Run(id)
	if err != nil || r.State.Terminal() {
		return err
	}
	if r.State == Preparing || r.State == Running {
		unstarted := true
		for _, c := range r.Checks {
			// A preparing check was already claimed and may have spawned a
			// process before persisting its identity. Only queued/skipped and
			// explicitly inherited results prove no local command started.
			if c.InheritedFrom == "" && (c.Process.PID != 0 || c.StartedAt != nil || (c.State != Queued && c.State != Skipped)) {
				unstarted = false
			}
		}
		if unstarted {
			if err = os.RemoveAll(filepath.Join(s.Store.Root, "workspaces", r.ID)); err != nil {
				r.Diagnostics = append(r.Diagnostics, Diagnostic{Code: "workspace-cleanup-failed", Message: "unstarted recovery could not remove the partial owned workspace"})
				if saveErr := s.Store.SaveRun(r); saveErr != nil {
					return saveErr
				}
				return E("workspace-cleanup-failed", "unstarted workspace recovery failed", 3)
			}
			r.State = Queued
			if err = s.Store.SaveRun(r); err != nil {
				return err
			}
			s.Log.Info("run.preparation_recovered", "run_id", r.ID)
		}
	}
	if r.State != Queued {
		for _, c := range r.Checks {
			if !c.State.Terminal() {
				if err = ReconcileProcess(c.Process); err != nil {
					return err
				}
				if err = s.finishCheck(c, Interrupted, "worker-interrupted", "previous worker stopped; explicit rerun is required"); err != nil {
					return err
				}
			}
		}
		r.State = Interrupted
		r.Diagnostics = append(r.Diagnostics, Diagnostic{Code: "worker-interrupted", Message: "previous worker stopped; interrupted commands were not replayed"})
		return s.finalize(r)
	}
	r.State = Preparing
	if err = s.Store.SaveRun(r); err != nil {
		return err
	}
	workspace, err := s.Store.Prepare(ctx, r)
	if err != nil {
		for _, c := range r.Checks {
			if c.State == Queued {
				if saveErr := s.finishCheck(c, Failed, "workspace-preparation-failed", err.Error()); saveErr != nil {
					return saveErr
				}
			}
		}
		r.State = Failed
		r.Diagnostics = append(r.Diagnostics, Diagnostic{Code: "workspace-preparation-failed", Message: err.Error()})
		return s.finalize(r)
	}
	r.State = Running
	if err = s.Store.SaveRun(r); err != nil {
		return err
	}
	active := map[string]bool{}
	type completion struct {
		id  string
		err error
	}
	done := make(chan completion, len(r.Checks))
	var wg sync.WaitGroup
	defer func() { cancelOwned(); wg.Wait() }()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, e := s.Store.Run(id)
		if e != nil {
			return e
		}
		byName := map[string]Check{}
		for _, c := range current.Checks {
			byName[c.Name] = c
		}
		remaining := false
		for _, c := range current.Checks {
			if c.State.Terminal() {
				continue
			}
			remaining = true
			if active[c.ID] {
				continue
			}
			if reason := s.Store.Cancellation(c.ID); reason != "" {
				if e = ReconcileProcess(c.Process); e != nil {
					return e
				}
				if e = s.finishCheck(c, reason, "", ""); e != nil {
					return e
				}
				continue
			}
			if c.State != Queued {
				continue
			}
			ready := true
			blocked := false
			for _, dep := range r.Config.Checks[c.Name].DependsOn {
				d := byName[dep]
				if !d.State.Terminal() {
					ready = false
				} else if d.State != Passed {
					blocked = true
				}
			}
			if blocked {
				if e = s.finishCheck(c, Blocked, "dependency-failed", "a prerequisite did not pass"); e != nil {
					return e
				}
				continue
			}
			if !ready {
				continue
			}
			claimed, e := s.claim(r, c)
			if e != nil {
				return e
			}
			if claimed {
				active[c.ID] = true
				wg.Add(1)
				go func(c Check) { defer wg.Done(); done <- completion{c.ID, s.execute(ctx, r, c, workspace)} }(c)
			}
		}
		if !remaining {
			break
		}
		select {
		case result := <-done:
			if result.err != nil {
				return result.err
			}
			delete(active, result.id)
		case <-time.After(75 * time.Millisecond):
		}
	}
	wg.Wait()
	r, err = s.Store.Run(id)
	if err != nil {
		return err
	}
	state, applicable := aggregateCheckState(r.Checks)
	r.State = state
	if applicable == 0 {
		r.State = Failed
		r.Diagnostics = append(r.Diagnostics, Diagnostic{Code: "no-applicable-checks", Message: "this execution does not validate any applicable checks"})
	}
	return s.finalize(r)
}

func aggregateCheckState(checks []Check) (State, int) {
	seen := map[State]bool{}
	applicable := 0
	for _, c := range checks {
		if c.State == Skipped {
			continue
		}
		applicable++
		if c.Optional && (c.State == Failed || c.State == Blocked) {
			continue
		}
		seen[c.State] = true
		if !c.State.Terminal() {
			seen[Interrupted] = true
		}
	}
	if applicable == 0 {
		return Failed, 0
	}
	// Lifecycle/evidence loss takes precedence over validation failures;
	// a failed prerequisite takes precedence over its blocked dependents.
	for _, state := range []State{Interrupted, Cancelled, Replaced, Expired, Failed, Blocked} {
		if seen[state] {
			return state, applicable
		}
	}
	return Passed, applicable
}
func (s *Service) finalize(r Run) error {
	workspace := filepath.Join(s.Store.Root, "workspaces", r.ID)
	if err := os.RemoveAll(workspace); err != nil {
		r.Diagnostics = append(r.Diagnostics, Diagnostic{Code: "workspace-cleanup-failed", Message: "owned workspace cleanup failed; run ach doctor"})
		if r.State == Passed {
			r.State = Failed
		}
	}
	t := time.Now().UTC()
	r.FinishedAt = &t
	return s.Store.SaveRun(r)
}
func (s *Service) Work(ctx context.Context, persistent bool, component string) error {
	return s.work(ctx, persistent, component, s.runOne)
}

func (s *Service) work(ctx context.Context, persistent bool, component string, runOne func(context.Context, string) error) error {
	var wg sync.WaitGroup
	// A graceful stop drains using its own context. An unexpected worker return
	// still cancels every owned command before relinquishing process ownership.
	owned, cancelOwned := context.WithCancel(context.Background())
	defer func() { cancelOwned(); wg.Wait() }()
	active := map[string]bool{}
	retries := map[string]workerRetry{}
	type completion struct {
		id  string
		err error
	}
	done := make(chan completion, 1024)
	stopping := false
	force := false
	for {
		pending := false
		if component != "" {
			var stop string
			_ = s.Store.DB.QueryRow("SELECT stop FROM components WHERE id=?", component).Scan(&stop)
			if stop != "" {
				stopping = true
				force = stop == "force"
			}
		}
		if ctx.Err() != nil {
			stopping = true
			force = force || !persistent
		}
		if force {
			for id := range active {
				_ = s.Store.Cancel(id, Cancelled)
			}
		}
		if !stopping {
			ids, e := s.Store.Pending()
			if e != nil {
				return e
			}
			pending = len(ids) > 0
			present := map[string]bool{}
			for _, id := range ids {
				present[id] = true
			}
			for id := range retries {
				if !present[id] {
					delete(retries, id)
				}
			}
			for _, id := range ids {
				if active[id] || !retries[id].ready(time.Now()) {
					continue
				}
				active[id] = true
				wg.Add(1)
				go func(id string) {
					defer wg.Done()
					// Shutdown stops receiving completions. Never let a full queue
					// prevent an already-reaped run from joining its worker.
					result := completion{id, runOne(owned, id)}
					select {
					case done <- result:
					case <-owned.Done():
					}
				}(id)
			}
		}
		if len(active) == 0 && (stopping || (!persistent && !pending)) {
			break
		}
		select {
		case result := <-done:
			delete(active, result.id)
			retry := retries[result.id]
			if result.err != nil {
				retry.fail(time.Now())
				s.Log.Error("run.worker_failed", "run_id", result.id, "code", "runner-error", "retry_after_ms", retry.delay.Milliseconds())
			} else {
				// Another worker may own this pending run and return without work.
				// Avoid a hot lock-probing loop even in that successful case.
				retry = workerRetry{next: time.Now().Add(200 * time.Millisecond)}
			}
			retries[result.id] = retry
			if s.Personal.Retention.MaxAgeDays > 0 || s.Personal.Retention.MaxBytes > 0 {
				if _, err := s.Prune(false, s.Personal.Retention.MaxAgeDays, s.Personal.Retention.MaxBytes); err != nil {
					s.Log.Error("retention.failed", "code", "retention-failed")
				}
			}
		case <-time.After(200 * time.Millisecond):
		}
	}
	wg.Wait()
	if s.Personal.Retention.MaxAgeDays > 0 || s.Personal.Retention.MaxBytes > 0 {
		if _, err := s.Prune(false, s.Personal.Retention.MaxAgeDays, s.Personal.Retention.MaxBytes); err != nil {
			return err
		}
	}
	return nil
}

type workerRetry struct {
	next  time.Time
	delay time.Duration
}

func (r workerRetry) ready(now time.Time) bool { return !now.Before(r.next) }
func (r *workerRetry) fail(now time.Time) {
	if r.delay == 0 {
		r.delay = time.Second
	} else {
		r.delay = min(2*r.delay, time.Minute)
	}
	r.next = now.Add(r.delay)
}

func SupportedTarget() bool {
	return (runtime.GOOS == "darwin" || runtime.GOOS == "linux" || runtime.GOOS == "windows") && (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64")
}
