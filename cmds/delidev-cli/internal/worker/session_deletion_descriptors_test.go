// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const (
	descriptorChildEnv    = "DELIDEV_SESSION_DELETION_DESCRIPTOR_CHILD"
	descriptorChildLimit  = 1024
	descriptorChildCopies = 600
)

func stubDescriptorUsage(t *testing.T, usage security.DescriptorUsage) {
	t.Helper()
	previous := sessionDeletionDescriptorUsage
	sessionDeletionDescriptorUsage = func() security.DescriptorUsage { return usage }
	t.Cleanup(func() { sessionDeletionDescriptorUsage = previous })
}

// addDeletionCopies appends valid present-journal copies (no job directory, no
// process owner) to the plan and returns it with its recomputed digest.
func addDeletionCopies(t *testing.T, c Config, w domain.SessionDeletionWork, total int) domain.SessionDeletionWork {
	t.Helper()
	first := w.Copies[0]
	w.Copies = append([]domain.SessionDeletionCopy(nil), w.Copies...)
	for len(w.Copies) < total {
		copy := first
		copy.JobID = domain.NewID()
		w.Copies = append(w.Copies, copy)
		j := journal{Version: 1, JobID: copy.JobID, InstanceID: copy.InstanceID, Revision: copy.Revision, Digest: copy.Digest, State: journalReported, ReportID: domain.NewID()}
		if e := writeJSON(filepath.Join(c.Root, "jobs", string(copy.JobID)+".json"), j); e != nil {
			t.Fatal(e)
		}
	}
	return w
}

func assertCopiesRemoved(t *testing.T, c Config, w domain.SessionDeletionWork) {
	t.Helper()
	for _, copy := range w.Copies {
		if _, err := os.Lstat(filepath.Join(c.Root, "jobs", string(copy.JobID)+".json")); !os.IsNotExist(err) {
			t.Fatal("journal survived a completed deletion", err)
		}
	}
}

// The child re-executes this test with a real 1,024-descriptor soft limit. A
// 600-copy plan holds 600 present journals, so identity handles alone used to
// exhaust the limit before the 600 job locks were open.
func TestSessionDeletionCompletesUnderDescriptorSoftLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("RLIMIT_NOFILE does not exist on Windows")
	}
	if scenario := os.Getenv(descriptorChildEnv); scenario != "" {
		runDescriptorLimitedDeletion(t, scenario)
		return
	}
	for _, scenario := range []string{"direct", "retry"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command(os.Args[0], "-test.run=^TestSessionDeletionCompletesUnderDescriptorSoftLimit$", "-test.count=1")
			cmd.Env = append(os.Environ(), descriptorChildEnv+"="+scenario)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatal(err, string(out))
			}
		})
	}
}

func runDescriptorLimitedDeletion(t *testing.T, scenario string) {
	c, w, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	w = addDeletionCopies(t, c, w, descriptorChildCopies)
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatal(err)
	}
	if limit.Max < descriptorChildLimit {
		t.Skip("hard descriptor limit is below the fixture limit")
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &syscall.Rlimit{Cur: descriptorChildLimit, Max: limit.Max}); err != nil {
		t.Skip("cannot lower the descriptor soft limit", err)
	}
	usage := security.CurrentDescriptorUsage()
	if !usage.LimitKnown || usage.Limit != descriptorChildLimit || !usage.OpenKnown || usage.Open >= 100 {
		t.Fatal("fixture does not run under the intended limit", usage)
	}
	if scenario == "retry" {
		// A first attempt persists the identity inventory and stays pending
		// because a copy was restored where absence was proven.
		foreign := filepath.Join(c.Root, "execution-claims", string(w.SessionID)+".json")
		installDeletionHook(t, func() { mustWrite(t, foreign, "restored foreign copy") })
		expectPendingDeletion(t, c, w)
		sessionDeletionBeforeRemovalHook = nil
		if err := os.Remove(foreign); err != nil {
			t.Fatal(err)
		}
		if saved := readDeletionProof(t, sessionDeletionObligationPath(c.Root, w, ".json")); !saved.RemovalStarted || !saved.IdentityInventory {
			t.Fatal("first attempt did not retain its inventory", saved)
		}
	}
	proof, err := deleteSessionCopies(context.Background(), c, w)
	if err != nil || !proof.Complete {
		t.Fatal("deletion stalled under the descriptor soft limit", proof, err)
	}
	assertCopiesRemoved(t, c, w)
	// Replaying the completed obligation also finishes.
	if proof, err := deleteSessionCopies(context.Background(), c, w); err != nil || !proof.Complete {
		t.Fatal("replay stalled under the descriptor soft limit", proof, err)
	}
}

// When the mandatory locks alone cannot fit, the attempt stays pending before
// any lock is opened, logs counts only, and succeeds once the limit allows.
func TestSessionDeletionMandatoryLocksExceedingDescriptorLimitStayPending(t *testing.T) {
	c, w, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	w = addDeletionCopies(t, c, w, 40)
	var logs bytes.Buffer
	c.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	stubDescriptorUsage(t, security.DescriptorUsage{Limit: 100, LimitKnown: true, Open: 30, OpenKnown: true})
	expectPendingDeletion(t, c, w)
	assertNoCopyRemoved := func() {
		for _, copy := range w.Copies {
			if _, err := os.Lstat(filepath.Join(c.Root, "jobs", string(copy.JobID)+".json")); err != nil {
				t.Fatal("a journal was removed without proof", err)
			}
			if _, err := os.Lstat(filepath.Join(c.Root, "jobs", string(copy.JobID)+".lock")); !os.IsNotExist(err) {
				t.Fatal("a job lock was opened although the budget was exhausted", err)
			}
		}
	}
	assertNoCopyRemoved()
	if saved := readDeletionProof(t, sessionDeletionObligationPath(c.Root, w, ".json")); saved.RemovalStarted || saved.Complete {
		t.Fatal("an exhausted budget advanced the proof", saved)
	}
	out := logs.String()
	if !strings.Contains(out, "session_deletion_descriptor_budget_exceeded") || !strings.Contains(out, "descriptor_limit=100") || !strings.Contains(out, "job_locks=40") {
		t.Fatal("structured diagnostic is missing", out)
	}
	for _, secret := range []string{c.Root, string(w.SessionID), string(w.Copies[0].JobID)} {
		if strings.Contains(out, secret) {
			t.Fatal("diagnostic is not content-free", secret)
		}
	}
	// Nothing leaked: the same plan completes once the limit fits.
	stubDescriptorUsage(t, security.DescriptorUsage{Limit: 4096, LimitKnown: true, Open: 30, OpenKnown: true})
	proof, err := deleteSessionCopies(context.Background(), c, w)
	if err != nil || !proof.Complete {
		t.Fatal(proof, err)
	}
	assertCopiesRemoved(t, c, w)
}

func TestPlanSessionDescriptorBudgetReservesMandatoryDemand(t *testing.T) {
	c, w, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	w = addDeletionCopies(t, c, w, 10)
	root := c.Root
	// Two present job directories each need a publication lock.
	for _, copy := range w.Copies[:2] {
		if err := os.MkdirAll(filepath.Join(root, "jobs", string(copy.JobID)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	mandatory := 10 + 2 + sessionDeletionSessionLockDescriptors
	stubDescriptorUsage(t, security.DescriptorUsage{Limit: 1024, LimitKnown: true, Open: 50, OpenKnown: true})
	budget, err := planSessionDescriptorBudget(context.Background(), c, root, w)
	if err != nil {
		t.Fatal(err)
	}
	if want := 1024 - 50 - mandatory - sessionDeletionTransientDescriptors; budget.remaining != want {
		t.Fatal("identity allowance does not follow the reserve", budget.remaining, want)
	}
	// The existing cap still bounds a generous limit.
	stubDescriptorUsage(t, security.DescriptorUsage{Limit: 1 << 20, LimitKnown: true, Open: 50, OpenKnown: true})
	if budget, err = planSessionDescriptorBudget(context.Background(), c, root, w); err != nil || budget.remaining != maxHeldIdentityHandles {
		t.Fatal(budget, err)
	}
	// An uncountable baseline reserves a fixed amount instead of assuming none.
	stubDescriptorUsage(t, security.DescriptorUsage{Limit: 1024, LimitKnown: true})
	if budget, err = planSessionDescriptorBudget(context.Background(), c, root, w); err != nil || budget.remaining != 1024-sessionDeletionUnknownBaselineDescriptors-mandatory-sessionDeletionTransientDescriptors {
		t.Fatal(budget, err)
	}
	// Without a known limit the previous fixed cap applies.
	stubDescriptorUsage(t, security.DescriptorUsage{})
	if budget, err = planSessionDescriptorBudget(context.Background(), c, root, w); err != nil || budget.remaining != maxHeldIdentityHandles {
		t.Fatal(budget, err)
	}
}

func TestIdentityBudgetReleasesBeyondAllowanceAndKeepsRecord(t *testing.T) {
	dir := t.TempDir()
	budget := &identityBudget{remaining: 1}
	var identities []security.PathIdentity
	for _, name := range []string{"a", "b"} {
		mustWrite(t, filepath.Join(dir, name), "x")
		identity, err := security.CapturePathIdentity(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		budget.retain(&identity)
		identities = append(identities, identity)
	}
	defer closeSessionCopyIdentities(map[string]security.PathIdentity{"a": identities[0], "b": identities[1]})
	if !identities[0].Held() || identities[1].Held() || !identities[1].Present() {
		t.Fatal("allowance was not applied in order", identities[0].Held(), identities[1].Held())
	}
	if err := identities[1].StillAt(filepath.Join(dir, "b")); err != nil {
		t.Fatal("a released identity must still compare by its record", err)
	}
}

type countdownContext struct {
	context.Context
	remaining int
}

func (c *countdownContext) Err() error {
	if c.remaining--; c.remaining < 0 {
		return context.Canceled
	}
	return nil
}
func (c *countdownContext) Done() <-chan struct{}       { return nil }
func (c *countdownContext) Deadline() (time.Time, bool) { return time.Time{}, false }

// Cancellation during capture closes every handle already opened.
func TestSessionDeletionCaptureReleasesDescriptorsOnCancellation(t *testing.T) {
	c, w, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	w = addDeletionCopies(t, c, w, 20)
	root, err := filepath.EvalSymlinks(c.Root)
	if err != nil {
		t.Fatal(err)
	}
	before := security.CurrentDescriptorUsage()
	identities, err := prepareSessionCopyIdentities(&countdownContext{Context: context.Background(), remaining: 12}, c, root, w, &identityBudget{remaining: 1 << 20}, false, false)
	if err == nil || identities != nil {
		t.Fatal("cancellation was not reported", err)
	}
	if after := security.CurrentDescriptorUsage(); before.OpenKnown && after.Open != before.Open {
		t.Fatal("cancelled capture leaked descriptors", before.Open, after.Open)
	}
}
