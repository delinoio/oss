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
				if _, err := tx.Exec("UPDATE checks SET cancel='replaced' WHERE group_key=? AND id<>? AND run_id IN (SELECT id FROM runs WHERE seq<?) AND state IN ('preparing','running','collecting')", c.Group, c.ID, r.Sequence); err != nil {
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
	return s.Store.SaveCheck(c)
}
func (s *Service) execute(r Run, c Check, workspace string) {
	command := r.Config.Checks[c.Name]
	env, secrets, err := s.Personal.CommandEnvironment(command, r.Environment)
	if err != nil {
		_ = s.finishCheck(c, Failed, "environment-unavailable", err.Error())
		return
	}
	c.Log = Evidence{ID: ID(), Name: "combined.log"}
	logPath, _ := s.Store.EvidencePath(r.ID, c.Log.ID)
	if err = PrivateDir(filepath.Dir(logPath)); err != nil {
		_ = s.finishCheck(c, Failed, "evidence-write-failed", "cannot create log directory")
		return
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		_ = s.finishCheck(c, Failed, "evidence-write-failed", "cannot open log")
		return
	}
	defer f.Close()
	redactor := NewRedactor(f, secrets)
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
		_ = redactor.Close()
		_ = s.finishCheck(c, cancel, "", "")
		return
	}
	p, err := startProcess(cmd)
	if err != nil {
		_ = redactor.Close()
		_ = s.finishCheck(c, Failed, "command-start-failed", "cannot start selected shell; run ach doctor")
		return
	}
	defer p.close()
	c.Process = p.identity
	c.State = Running
	t := time.Now().UTC()
	c.StartedAt = &t
	if err = s.Store.SaveCheck(c); err != nil {
		_ = p.terminate()
		_, _ = cmd.Process.Wait()
		return
	}
	s.Log.Info("check.started", "run_id", r.ID, "check_id", c.ID, "check", c.Name)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
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
		case <-ticker.C:
			if err = p.sample(); err != nil {
				s.Log.Error("process.sample_failed", "run_id", r.ID, "check_id", c.ID, "code", "process-reconciliation-failed")
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
		return
	}
	err = redactor.Close()
	if err == nil {
		err = f.Sync()
	}
	_ = f.Close()
	c.State = Collecting
	_ = s.Store.SaveCheck(c)
	if err != nil {
		_ = s.finishCheck(c, Failed, "evidence-write-failed", "captured output could not be persisted")
		return
	}
	if err = digestFile(logPath, &c.Log); err != nil {
		_ = s.finishCheck(c, Failed, "evidence-read-failed", "captured output could not be verified")
		return
	}
	exit := 0
	if waitErr != nil {
		exit = -1
		if ee, ok := waitErr.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		}
	}
	c.ExitCode = &exit
	for _, report := range command.Reports {
		b, e := ReadOwned(workspace, report.Path, 64*1024*1024)
		if e != nil {
			c.Diagnostics = append(c.Diagnostics, Diagnostic{Code: "report-missing", Message: "declared report unavailable: " + report.Path})
			continue
		}
		failures, e := ParseReport(report.Kind, b, c.Name, command.Command, c.Log.ID)
		if e != nil {
			c.Diagnostics = append(c.Diagnostics, Diagnostic{Code: "report-malformed", Message: "declared report invalid: " + report.Path})
		}
		for i := range failures {
			failures[i].Message = string(Redact([]byte(failures[i].Message), secrets))
			failures[i].File = string(Redact([]byte(failures[i].File), secrets))
			failures[i].Test = string(Redact([]byte(failures[i].Test), secrets))
			failures[i].Command = string(Redact([]byte(failures[i].Command), secrets))
		}
		c.Failures = append(c.Failures, failures...)
		ev, e := s.SaveEvidence(r.ID, report.Path, Redact(b, secrets))
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
	if reason := s.Store.Cancellation(c.ID); reason != "" {
		cancel = reason
	}
	if cancel != "" {
		state = cancel
	}
	_ = s.finishCheck(c, state, "", "")
	s.Log.Info("check.finished", "run_id", r.ID, "check_id", c.ID, "state", state)
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
	lock, err := TryLock(filepath.Join(s.Store.Root, "locks", id+".lock"))
	if err != nil || lock == nil {
		return err
	}
	defer lock.Close()
	r, err := s.Store.Run(id)
	if err != nil || r.State.Terminal() {
		return err
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
	workspace, err := s.Store.Prepare(context.Background(), r)
	if err != nil {
		for _, c := range r.Checks {
			if c.State == Queued {
				_ = s.finishCheck(c, Failed, "workspace-preparation-failed", err.Error())
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
	done := make(chan string, len(r.Checks))
	var wg sync.WaitGroup
	for {
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
				_ = s.finishCheck(c, reason, "", "")
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
				_ = s.finishCheck(c, Blocked, "dependency-failed", "a prerequisite did not pass")
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
				go func(c Check) { defer wg.Done(); s.execute(r, c, workspace); done <- c.ID }(c)
			}
		}
		if !remaining {
			break
		}
		select {
		case id := <-done:
			delete(active, id)
		case <-time.After(75 * time.Millisecond):
		}
	}
	wg.Wait()
	r, err = s.Store.Run(id)
	if err != nil {
		return err
	}
	r.State = Passed
	applicable := 0
	for _, c := range r.Checks {
		if c.State != Skipped {
			applicable++
		}
		if !c.Optional && c.State != Passed && c.State != Skipped {
			r.State = c.State
		}
	}
	if applicable == 0 {
		r.State = Failed
		r.Diagnostics = append(r.Diagnostics, Diagnostic{Code: "no-applicable-checks", Message: "this execution does not validate any applicable checks"})
	}
	return s.finalize(r)
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
	var wg sync.WaitGroup
	active := map[string]bool{}
	done := make(chan string, 1024)
	stopping := false
	force := false
	for {
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
			force = true
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
			for _, id := range ids {
				if active[id] {
					continue
				}
				active[id] = true
				wg.Add(1)
				go func(id string) {
					defer wg.Done()
					if e := s.RunOne(id); e != nil {
						s.Log.Error("run.worker_failed", "run_id", id, "code", "runner-error")
					}
					done <- id
				}(id)
			}
		}
		if len(active) == 0 && (stopping || !persistent) {
			break
		}
		select {
		case id := <-done:
			delete(active, id)
		case <-time.After(200 * time.Millisecond):
		}
	}
	wg.Wait()
	return nil
}

func SupportedTarget() bool {
	return (runtime.GOOS == "darwin" || runtime.GOOS == "linux" || runtime.GOOS == "windows") && (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64")
}
