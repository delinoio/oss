// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// startupRebindFixture is a Grok job whose local startup resolution picked one
// executable before the subscription Take authorized another.
type startupRebindFixture struct {
	t        *testing.T
	config   Config
	job      domain.ID
	input    domain.ExecutionJobInput
	attempt  *executionStartupAttempt
	resolved domain.Installation
}

func newStartupRebindFixture(t *testing.T, pinned bool) *startupRebindFixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "private")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	f := &startupRebindFixture{t: t, job: domain.NewID(), config: Config{Root: root}}
	original := f.write("original-grok", "original executable fixture")
	selection := domain.ExecutionStartupSelection{Harness: domain.GrokBuild, ExplicitPath: original}
	f.input = domain.ExecutionJobInput{Version: 4, Startup: &selection}
	resolved, err := harness.ResolveExecution(context.Background(), selection)
	if err != nil {
		t.Fatal(err)
	}
	if pinned {
		selection.ExecutableSHA256 = resolved.ExecutableSHA256
		f.input.Startup = &selection
	}
	f.resolved = resolved
	f.input.Installation = resolved
	f.attempt = newExecutionStartupAttempt(f.config, f.job, f.input)
	f.attempt.executable, f.attempt.observation.ExecutableSHA256 = resolved.ResolvedPath, resolved.ExecutableSHA256
	if err := writeStartupExecutable(root, f.job, resolved); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *startupRebindFixture) write(name, content string) string {
	f.t.Helper()
	path := filepath.Join(f.config.Root, name)
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		f.t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		f.t.Fatal(err)
	}
	return resolved
}

// digest is the executable identity discovery would report for path.
func (f *startupRebindFixture) digest(path string) string {
	f.t.Helper()
	digest, err := harness.InspectExecutable(context.Background(), path)
	if err != nil {
		f.t.Fatal(err)
	}
	return digest
}

func (f *startupRebindFixture) journal() domain.Installation {
	f.t.Helper()
	raw, err := security.ReadPrivate(filepath.Join(f.config.Root, "jobs", string(f.job), "startup-executable.json"), 8192)
	if err != nil {
		f.t.Fatal(err)
	}
	var installation domain.Installation
	if err := domain.Decode(raw, &installation); err != nil {
		f.t.Fatal(err)
	}
	return installation
}

// requireUnchanged proves that the startup evidence still describes the
// executable resolved before the Take.
func (f *startupRebindFixture) requireUnchanged() {
	f.t.Helper()
	if f.attempt.executable != f.resolved.ResolvedPath || f.attempt.observation.ExecutableSHA256 != f.resolved.ExecutableSHA256 {
		f.t.Fatal("the startup evidence moved")
	}
	if journal := f.journal(); journal.ResolvedPath != f.resolved.ResolvedPath || journal.ExecutableSHA256 != f.resolved.ExecutableSHA256 {
		f.t.Fatal("the startup journal moved")
	}
}

// When discovery changes the Grok path between the local resolution and the
// Take, the lease launches the authorized executable, so the ready report must
// inspect that executable and the journal must pin it.
func TestGrokStartupEvidenceFollowsTheExecutableTheTakeAuthorized(t *testing.T) {
	f := newStartupRebindFixture(t, false)
	authorized := f.write("authorized-grok", "authorized executable fixture")
	installation, err := f.attempt.rebindExecutable(context.Background(), f.config, f.job, f.input, authorized, "1.0.99", f.digest(authorized))
	if err != nil {
		t.Fatal(err)
	}
	digest, err := harness.InspectExecutable(context.Background(), authorized)
	if err != nil {
		t.Fatal(err)
	}
	if digest == f.resolved.ExecutableSHA256 {
		t.Fatal("the fixture executables are indistinguishable")
	}
	if f.attempt.executable != authorized || f.attempt.observation.ExecutableSHA256 != digest {
		t.Fatal("the startup evidence still describes the executable resolved before the Take")
	}
	if installation.ResolvedPath != authorized || installation.ExecutableSHA256 != digest || installation.Version != "1.0.99" {
		t.Fatalf("the returned installation is not the authorized one: %+v", installation)
	}
	if journal := f.journal(); journal.ResolvedPath != authorized || journal.ExecutableSHA256 != digest {
		t.Fatal("the continuation journal still pins the executable resolved before the Take")
	}
	// The ready report compares the inspected bytes with the observation.
	inspected, err := harness.InspectExecutable(context.Background(), f.attempt.executable)
	if err != nil || inspected != f.attempt.observation.ExecutableSHA256 {
		t.Fatal("the ready report would inspect a different executable than the one launched")
	}
}

// The lease run hands the launcher the authorized executable together with the
// moved evidence, and refuses a swapped identity before touching the evidence.
func TestGrokLaunchTargetMovesTheEvidenceWithTheLeaseExecutable(t *testing.T) {
	f := newStartupRebindFixture(t, false)
	authorized := f.write("authorized-grok", "authorized executable fixture")
	f.config.startup = f.attempt
	run := &grokSubscriptionRun{installation: domain.Installation{Harness: domain.GrokBuild, Version: "1.0.99", ResolvedPath: authorized, ExecutableSHA256: f.digest(authorized)}}
	executable, version, installation, err := run.launchTarget(context.Background(), f.config, f.job, f.input)
	if err != nil || executable != authorized || version != "1.0.99" || installation.ResolvedPath != authorized || f.attempt.executable != authorized {
		t.Fatalf("the launch target did not follow the lease: %q %q %v", executable, version, err)
	}
	link := filepath.Join(f.config.Root, "swapped-grok")
	if err := os.Symlink(authorized, link); err != nil {
		t.Skip("symlinks are unavailable:", err)
	}
	f.attempt.executable = f.resolved.ResolvedPath
	run.installation.ResolvedPath = link
	if _, _, _, err := run.launchTarget(context.Background(), f.config, f.job, f.input); err == nil || f.attempt.executable != f.resolved.ResolvedPath {
		t.Fatal("a swapped executable identity reached the evidence")
	}
}

// An authorized executable whose digest differs from the digest the selection
// pinned fails closed and leaves the evidence untouched.
func TestGrokStartupFailsClosedWhenTheAuthorizedExecutableDiffersFromThePinnedDigest(t *testing.T) {
	f := newStartupRebindFixture(t, true)
	authorized := f.write("authorized-grok", "authorized executable fixture")
	if _, err := f.attempt.rebindExecutable(context.Background(), f.config, f.job, f.input, authorized, "1.0.99", f.digest(authorized)); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("a mismatching authorized executable was accepted: %v", err)
	}
	f.requireUnchanged()
}

// A pinned digest that the authorized executable satisfies is accepted even at a
// new path: the pin is the identity, not the path.
func TestGrokStartupAcceptsAnAuthorizedPathWithThePinnedDigest(t *testing.T) {
	f := newStartupRebindFixture(t, true)
	moved := f.write("moved-grok", "original executable fixture")
	if _, err := f.attempt.rebindExecutable(context.Background(), f.config, f.job, f.input, moved, "", f.digest(moved)); err != nil {
		t.Fatal(err)
	}
	if f.attempt.executable != moved || f.attempt.observation.ExecutableSHA256 != f.resolved.ExecutableSHA256 {
		t.Fatal("the pinned executable at its new path was not bound")
	}
}

// An unchanged path with unchanged bytes keeps its evidence.
func TestGrokStartupKeepsEvidenceOfAnUnchangedExecutable(t *testing.T) {
	f := newStartupRebindFixture(t, false)
	installation, err := f.attempt.rebindExecutable(context.Background(), f.config, f.job, f.input, f.resolved.ResolvedPath, f.resolved.Version, f.resolved.ExecutableSHA256)
	if err != nil || installation.ResolvedPath != f.resolved.ResolvedPath || installation.ExecutableSHA256 != f.resolved.ExecutableSHA256 {
		t.Fatalf("an unchanged executable moved the evidence: %v", err)
	}
	f.requireUnchanged()
}

// The bytes behind the canonical path are a state of their own. The startup
// evidence follows the digest the Take admitted and never the bytes found now:
//
//	bytes now at the authorized path        | startup resolution | outcome
//	hash to the admitted digest             | same bytes         | evidence kept
//	hash to the admitted digest             | other bytes        | evidence moves to the admitted bytes
//	differ from the admitted digest         | any                | fail closed (RECOVERY_REQUIRED), evidence untouched
//	differ, and the selection pinned a digest | any              | fail closed, evidence untouched
//	missing or unreadable                   | any                | fail closed, evidence untouched
//
// A replacement is therefore never promoted into the evidence, and the ready
// report is not the first place it is noticed.
func TestGrokStartupRefusesAnExecutableReplacedAtTheSamePathAfterTheTake(t *testing.T) {
	t.Run("unpinned", func(t *testing.T) {
		f := newStartupRebindFixture(t, false)
		if err := os.WriteFile(f.resolved.ResolvedPath, []byte("replaced after the take"), 0o700); err != nil {
			t.Fatal(err)
		}
		if f.digest(f.resolved.ResolvedPath) == f.resolved.ExecutableSHA256 {
			t.Fatal("the fixture replacement is indistinguishable")
		}
		// The Take admitted the bytes that were there before the replacement.
		if _, err := f.attempt.rebindExecutable(context.Background(), f.config, f.job, f.input, f.resolved.ResolvedPath, f.resolved.Version, f.resolved.ExecutableSHA256); domain.SafeError(err).Code != domain.RecoveryRequired {
			t.Fatalf("a replacement was promoted into the startup evidence: %v", err)
		}
		f.requireUnchanged()
	})
	t.Run("pinned", func(t *testing.T) {
		f := newStartupRebindFixture(t, true)
		if err := os.WriteFile(f.resolved.ResolvedPath, []byte("replaced after the resolution"), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := f.attempt.rebindExecutable(context.Background(), f.config, f.job, f.input, f.resolved.ResolvedPath, f.resolved.Version, f.digest(f.resolved.ResolvedPath)); domain.SafeError(err).Code != domain.RecoveryRequired {
			t.Fatalf("a replaced executable passed its pinned digest: %v", err)
		}
		f.requireUnchanged()
	})
	t.Run("removed", func(t *testing.T) {
		f := newStartupRebindFixture(t, false)
		if err := os.Remove(f.resolved.ResolvedPath); err != nil {
			t.Fatal(err)
		}
		if _, err := f.attempt.rebindExecutable(context.Background(), f.config, f.job, f.input, f.resolved.ResolvedPath, f.resolved.Version, f.resolved.ExecutableSHA256); domain.SafeError(err).Code != domain.RecoveryRequired {
			t.Fatalf("a removed executable was accepted: %v", err)
		}
		f.requireUnchanged()
	})
}

// Bytes replaced before the Take, then admitted by it, are what the lease
// launches: the evidence moves to them, and the ready report agrees.
func TestGrokStartupFollowsBytesReplacedBeforeTheTakeAdmittedThem(t *testing.T) {
	f := newStartupRebindFixture(t, false)
	if err := os.WriteFile(f.resolved.ResolvedPath, []byte("replaced before the take"), 0o700); err != nil {
		t.Fatal(err)
	}
	admitted := f.digest(f.resolved.ResolvedPath)
	installation, err := f.attempt.rebindExecutable(context.Background(), f.config, f.job, f.input, f.resolved.ResolvedPath, f.resolved.Version, admitted)
	if err != nil {
		t.Fatal(err)
	}
	if installation.ExecutableSHA256 != admitted || f.attempt.observation.ExecutableSHA256 != admitted || f.attempt.executable != f.resolved.ResolvedPath {
		t.Fatal("the evidence does not describe the admitted bytes")
	}
	if journal := f.journal(); journal.ExecutableSHA256 != admitted {
		t.Fatal("the continuation journal does not pin the admitted bytes")
	}
	if err := os.WriteFile(f.resolved.ResolvedPath, []byte("replaced again after the rebind"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := f.attempt.ready(context.Background(), "1.0.99"); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("the ready report accepted bytes that changed after the rebind: %v", err)
	}
}

// launchTarget enforces the admitted digest whether or not the job has startup
// evidence, because the credential reaches the executable either way.
func TestGrokLaunchTargetFailsClosedWhenTheBytesDifferFromTheAdmittedDigest(t *testing.T) {
	for _, withEvidence := range []bool{true, false} {
		name := "without startup evidence"
		if withEvidence {
			name = "with startup evidence"
		}
		t.Run(name, func(t *testing.T) {
			f := newStartupRebindFixture(t, false)
			authorized := f.write("authorized-grok", "authorized executable fixture")
			admitted := f.digest(authorized)
			if withEvidence {
				f.config.startup = f.attempt
			}
			run := &grokSubscriptionRun{installation: domain.Installation{Harness: domain.GrokBuild, Version: "1.0.99", ResolvedPath: authorized, ExecutableSHA256: admitted}}
			if err := os.WriteFile(authorized, []byte("replaced after the take"), 0o700); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := run.launchTarget(context.Background(), f.config, f.job, f.input); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatalf("replaced bytes passed the launch gate: %v", err)
			}
			f.requireUnchanged()
			if err := os.WriteFile(authorized, []byte("authorized executable fixture"), 0o700); err != nil {
				t.Fatal(err)
			}
			if executable, _, _, err := run.launchTarget(context.Background(), f.config, f.job, f.input); err != nil || executable != authorized {
				t.Fatalf("the admitted bytes were refused: %v", err)
			}
		})
	}
}

// prepare is the last gate before the credential exists: bytes replaced after the
// launch target was verified receive no credential, and nothing is journaled as
// started.
func TestGrokPrepareRefusesBytesReplacedAfterTheLaunchTargetWasVerified(t *testing.T) {
	f := newStartupRebindFixture(t, false)
	authorized := f.write("authorized-grok", "authorized executable fixture")
	run := &grokSubscriptionRun{installation: domain.Installation{Harness: domain.GrokBuild, Version: "1.0.99", ResolvedPath: authorized, ExecutableSHA256: f.digest(authorized)}}
	if _, _, _, err := run.launchTarget(context.Background(), f.config, f.job, f.input); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authorized, []byte("replaced after the launch target"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := run.prepare(context.Background()); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("a credential was prepared for replaced bytes: %v", err)
	}
	if run.attempted {
		t.Fatal("the run was marked as attempted although no credential was prepared")
	}
}

// The Take's installation must carry the identity it authorizes.
func TestAuthorizedGrokInstallationRequiresTheAdmittedDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grok")
	valid := domain.Installation{Harness: domain.GrokBuild, Version: domain.GrokSubscriptionBaselineVersion, ResolvedPath: path, ExecutableSHA256: strings.Repeat("a", 64)}
	encode := func(i domain.Installation) []byte {
		raw, err := json.Marshal(i)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	if _, err := authorizedGrokInstallation(encode(valid)); err != nil {
		t.Fatalf("a complete observation was refused: %v", err)
	}
	for name, mutate := range map[string]func(*domain.Installation){
		"missing digest":     func(i *domain.Installation) { i.ExecutableSHA256 = "" },
		"short digest":       func(i *domain.Installation) { i.ExecutableSHA256 = strings.Repeat("a", 63) },
		"upper-case":         func(i *domain.Installation) { i.ExecutableSHA256 = strings.Repeat("A", 64) },
		"non-hex digest":     func(i *domain.Installation) { i.ExecutableSHA256 = strings.Repeat("g", 64) },
		"relative path":      func(i *domain.Installation) { i.ResolvedPath = "grok" },
		"foreign harness":    func(i *domain.Installation) { i.Harness = domain.Codex },
		"below the baseline": func(i *domain.Installation) { i.Version = "1.0.1" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := valid
			mutate(&bad)
			if _, err := authorizedGrokInstallation(encode(bad)); err == nil {
				t.Fatal("an unusable observation was accepted")
			}
		})
	}
}

// An authorized executable that cannot be observed fails closed instead of
// leaving the evidence on the previous executable.
func TestGrokStartupFailsClosedWhenTheAuthorizedExecutableCannotBeObserved(t *testing.T) {
	f := newStartupRebindFixture(t, false)
	missing := filepath.Join(f.config.Root, "missing-grok")
	if _, err := f.attempt.rebindExecutable(context.Background(), f.config, f.job, f.input, missing, "1.0.99", f.resolved.ExecutableSHA256); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("an unobservable authorized executable was accepted: %v", err)
	}
	f.requireUnchanged()
}

// A job without direct startup evidence has nothing to rebind.
func TestGrokStartupWithoutEvidenceHasNothingToRebind(t *testing.T) {
	f := newStartupRebindFixture(t, false)
	var none *executionStartupAttempt
	installation, err := none.rebindExecutable(context.Background(), f.config, f.job, f.input, f.write("other-grok", "other"), "1.0.99", "")
	if err != nil || installation.ResolvedPath != f.resolved.ResolvedPath {
		t.Fatalf("a job without startup evidence was rebound: %v", err)
	}
	f.requireUnchanged()
}
