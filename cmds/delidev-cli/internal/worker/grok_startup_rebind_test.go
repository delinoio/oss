// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"os"
	"path/filepath"
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
	installation, err := f.attempt.rebindExecutable(context.Background(), f.config, f.job, f.input, authorized, "1.0.99")
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
	run := &grokSubscriptionRun{installation: domain.Installation{Harness: domain.GrokBuild, Version: "1.0.99", ResolvedPath: authorized}}
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
	if _, err := f.attempt.rebindExecutable(context.Background(), f.config, f.job, f.input, authorized, "1.0.99"); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("a mismatching authorized executable was accepted: %v", err)
	}
	f.requireUnchanged()
}

// A pinned digest that the authorized executable satisfies is accepted even at a
// new path: the pin is the identity, not the path.
func TestGrokStartupAcceptsAnAuthorizedPathWithThePinnedDigest(t *testing.T) {
	f := newStartupRebindFixture(t, true)
	moved := f.write("moved-grok", "original executable fixture")
	if _, err := f.attempt.rebindExecutable(context.Background(), f.config, f.job, f.input, moved, ""); err != nil {
		t.Fatal(err)
	}
	if f.attempt.executable != moved || f.attempt.observation.ExecutableSHA256 != f.resolved.ExecutableSHA256 {
		t.Fatal("the pinned executable at its new path was not bound")
	}
}

// An unchanged path keeps its evidence, and the ready report still fails closed
// when the bytes at that path changed since the resolution.
func TestGrokStartupKeepsUnchangedEvidenceAndStillFailsClosedOnChangedBytes(t *testing.T) {
	f := newStartupRebindFixture(t, false)
	installation, err := f.attempt.rebindExecutable(context.Background(), f.config, f.job, f.input, f.resolved.ResolvedPath, f.resolved.Version)
	if err != nil || installation.ResolvedPath != f.resolved.ResolvedPath || installation.ExecutableSHA256 != f.resolved.ExecutableSHA256 {
		t.Fatalf("an unchanged path moved the evidence: %v", err)
	}
	f.requireUnchanged()
	if err := os.WriteFile(f.resolved.ResolvedPath, []byte("replaced after the resolution"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := f.attempt.ready(context.Background(), "1.0.99"); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("the ready report accepted an executable whose bytes changed: %v", err)
	}
}

// An authorized executable that cannot be observed fails closed instead of
// leaving the evidence on the previous executable.
func TestGrokStartupFailsClosedWhenTheAuthorizedExecutableCannotBeObserved(t *testing.T) {
	f := newStartupRebindFixture(t, false)
	missing := filepath.Join(f.config.Root, "missing-grok")
	if _, err := f.attempt.rebindExecutable(context.Background(), f.config, f.job, f.input, missing, "1.0.99"); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("an unobservable authorized executable was accepted: %v", err)
	}
	f.requireUnchanged()
}

// A job without direct startup evidence has nothing to rebind.
func TestGrokStartupWithoutEvidenceHasNothingToRebind(t *testing.T) {
	f := newStartupRebindFixture(t, false)
	var none *executionStartupAttempt
	installation, err := none.rebindExecutable(context.Background(), f.config, f.job, f.input, f.write("other-grok", "other"), "1.0.99")
	if err != nil || installation.ResolvedPath != f.resolved.ResolvedPath {
		t.Fatalf("a job without startup evidence was rebound: %v", err)
	}
	f.requireUnchanged()
}
