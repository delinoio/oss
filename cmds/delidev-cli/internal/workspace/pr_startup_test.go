package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func readPRStartupTestFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// These tests exercise private phase persistence without invoking a harness or
// fabricating a native completion. Real-Git lease integration is tested below.
func newPRStartupGateTest(t *testing.T) (*Manager, PrepareRequest, *prStartupGate) {
	t.Helper()
	m := manager(t)
	if err := m.initialize(); err != nil {
		t.Fatal(err)
	}
	input, _ := requestFor(filepath.Join(m.Root, "source-not-inspected"))
	input.Repositories[0].PRTarget = &domain.PRGitTarget{BaseRef: "private-base-name", HeadRef: "private-head-name"}
	if err := security.PrivateDir(filepath.Join(m.Git.ProcessRoot, string(input.SessionID))); err != nil {
		t.Fatal(err)
	}
	gate, err := m.beginPRStartup(domain.NewID(), domain.NewID(), input, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	return m, input, gate
}

func TestPRStartupInterruptedPhasesCannotAcquireNewExecution(t *testing.T) {
	for _, passed := range []bool{false, true} {
		t.Run(map[bool]string{false: "checking", true: "passed"}[passed], func(t *testing.T) {
			m, input, gate := newPRStartupGateTest(t)
			if passed {
				if err := gate.finish(nil); err != nil {
					t.Fatal(err)
				}
			}
			before := readPRStartupTestFile(t, gate.path)
			for _, ids := range [][2]domain.ID{{gate.record.JobID, gate.record.ExecutionID}, {gate.record.JobID, domain.NewID()}, {domain.NewID(), domain.NewID()}} {
				if _, err := m.beginPRStartup(ids[0], ids[1], input, gate.record.ManifestDigest); domain.SafeError(err).Code != domain.RecoveryRequired {
					t.Fatal("interrupted phase acquired execution authority", err)
				}
			}
			if readPRStartupTestFile(t, gate.path) != before {
				t.Fatal("interrupted phase was rewritten")
			}
		})
	}
}

func TestPRStartupRejectionBindsOriginalIdentityAndContainsNoSourceContent(t *testing.T) {
	m, input, gate := newPRStartupGateTest(t)
	err := gate.finish(prWorkspaceChanged())
	var rejected *prStartupRejection
	if !errors.As(err, &rejected) || rejected.record.validate() != nil || rejected.record.Reason != domain.Conflict {
		t.Fatal("missing positive rejection", err)
	}
	before := readPRStartupTestFile(t, gate.path)
	for _, private := range []string{m.Root, "source-not-inspected", "private-base-name", "private-head-name"} {
		if strings.Contains(before, private) || strings.Contains(err.Error(), private) {
			t.Fatal("startup metadata exposed source content")
		}
	}
	for _, mutation := range []string{"job", "preparation", "manifest", "target", "same-job-new-execution"} {
		t.Run(mutation, func(t *testing.T) {
			copy := input
			copy.Repositories = append([]RepositorySpec(nil), input.Repositories...)
			job, execution, digest := gate.record.JobID, gate.record.ExecutionID, gate.record.ManifestDigest
			switch mutation {
			case "job":
				job = domain.NewID()
			case "preparation":
				copy.MachineID = domain.NewID()
			case "manifest":
				digest = strings.Repeat("b", 64)
			case "target":
				target := *copy.Repositories[0].PRTarget
				target.HeadRef = "changed-head"
				copy.Repositories[0].PRTarget = &target
			case "same-job-new-execution":
				execution = domain.NewID()
			}
			if _, err := m.beginPRStartup(job, execution, copy, digest); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("changed original binding borrowed a rejection", err)
			}
		})
	}
	if readPRStartupTestFile(t, gate.path) != before {
		t.Fatal("rejected history was modified")
	}
}

func TestPRStartupUncertaintyCannotBecomeRejection(t *testing.T) {
	for _, scenario := range []string{"missing-phase", "changed-phase", "missing-owner", "malformed-owner", "execution-claim", "execution-owner", "native-runtime", "uncertain-error", "unknown-error"} {
		t.Run(scenario, func(t *testing.T) {
			m, _, gate := newPRStartupGateTest(t)
			reason := prWorkspaceChanged()
			var err error
			switch scenario {
			case "missing-phase":
				err = os.Remove(gate.path)
			case "changed-phase":
				value := gate.record
				value.JobID = domain.NewID()
				raw, _ := json.Marshal(value)
				err = security.WriteAtomic(gate.path, raw)
			case "missing-owner":
				err = os.Remove(filepath.Join(m.Git.ProcessRoot, string(gate.record.SessionID)))
			case "malformed-owner":
				err = os.WriteFile(filepath.Join(m.Git.ProcessRoot, string(gate.record.SessionID), "unexpected"), nil, 0600)
			case "execution-claim":
				err = security.WriteAtomic(m.executionClaimPath(gate.record.SessionID), []byte("unexpected"))
			case "execution-owner":
				err = security.PrivateDir(filepath.Join(m.Git.ProcessRoot, string(gate.record.JobID)))
			case "native-runtime":
				err = security.PrivateDir(filepath.Join(m.Root, "runtimes", string(gate.record.ExecutionID)))
			case "uncertain-error":
				reason = ResultUncertain()
			case "unknown-error":
				reason = errors.New("untrusted error text")
			}
			if err != nil {
				t.Fatal(err)
			}
			before, beforeErr := os.ReadFile(gate.path)
			err = gate.finish(reason)
			var rejected *prStartupRejection
			if err == nil || errors.As(err, &rejected) {
				t.Fatal("uncertain phase became a proven rejection", err)
			}
			after, afterErr := os.ReadFile(gate.path)
			if (beforeErr == nil) != (afterErr == nil) || string(before) != string(after) {
				t.Fatal("uncertain original phase was replaced")
			}
		})
	}
}

func TestPRStartupIdentityFailureRetainsCheckingAndBlocksNewAttempt(t *testing.T) {
	f := newPRPreparationFixture(t)
	manifest, err := f.manager.Prepare(context.Background(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	gitTest(t, manifest.PrimaryPath, "switch", "-c", "user-branch")
	job, execution := domain.NewID(), domain.NewID()
	if _, err := f.manager.ClaimFirstExecution(context.Background(), job, execution, f.request, manifest); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("changed identity was treated as an ordinary PR rejection", err)
	}
	path := f.manager.prStartupPath(f.request.SessionID, execution)
	before := readPRStartupTestFile(t, path)
	var phase prStartupRecord
	if domain.Decode([]byte(before), &phase) != nil || phase.Phase != prStartupChecking {
		t.Fatal("interrupted ownership check lost its original phase")
	}
	gitTest(t, manifest.PrimaryPath, "switch", "--detach", f.head)
	network := readPRStartupTestFile(t, f.log)
	if _, err := f.manager.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), f.request, manifest); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("new identity bypassed unresolved original phase", err)
	}
	if readPRStartupTestFile(t, path) != before || readPRStartupTestFile(t, f.log) != network {
		t.Fatal("blocked attempt rewrote evidence or accessed the network")
	}
}

func TestPRStartupRejectedReplayRejectsContradictoryNativeEvidence(t *testing.T) {
	for _, scenario := range []string{"claim", "owner", "runtime"} {
		t.Run(scenario, func(t *testing.T) {
			m, input, gate := newPRStartupGateTest(t)
			var rejected *prStartupRejection
			if err := gate.finish(prWorkspaceChanged()); !errors.As(err, &rejected) {
				t.Fatal(err)
			}
			before := readPRStartupTestFile(t, gate.path)
			var err error
			switch scenario {
			case "claim":
				err = security.WriteAtomic(m.executionClaimPath(input.SessionID), []byte("contradictory claim"))
			case "owner":
				err = security.PrivateDir(filepath.Join(m.Git.ProcessRoot, string(gate.record.JobID)))
			case "runtime":
				err = security.PrivateDir(filepath.Join(m.Root, "runtimes", string(gate.record.ExecutionID)))
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = m.beginPRStartup(gate.record.JobID, gate.record.ExecutionID, input, gate.record.ManifestDigest)
			if domain.SafeError(err).Code != domain.RecoveryRequired || errors.As(err, &rejected) || readPRStartupTestFile(t, gate.path) != before {
				t.Fatal("contradictory native evidence borrowed a retained rejection", err)
			}
		})
	}
}
