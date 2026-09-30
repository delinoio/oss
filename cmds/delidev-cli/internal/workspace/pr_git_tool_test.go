// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"bytes"
	"context"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func preparePRFixGitTransport(t *testing.T, f prPreparationFixture) {
	t.Helper()
	native, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	// All network-shaped operands are mapped to two test-owned local repos.
	// Only these fixtures can receive a push; user credentials are disabled.
	wrapper := filepath.Join(t.TempDir(), "git")
	script := fmt.Sprintf(`#!/bin/sh
push=no
network=no
geturl=no
for arg do
 case "$arg" in push|fetch|ls-remote) network=yes;; --get-url) geturl=yes;; esac
done
for arg do
 case "$arg" in push) push=yes;; esac
 if [ "$network" = yes ] && [ "$geturl" = no ]; then
 case "$arg" in
  https://github.com/fixture-owner/repo.git) arg=%s;;
  https://github.com/fixture-author/fork-repo.git) arg=%s;;
 esac
 fi
 shift
 set -- "$@" "$arg"
done
if [ "$push" = yes ] && [ -f %s ]; then exit 128; fi
exec %s "$@"
`, fixtureShellQuote(f.source), fixtureShellQuote(f.fork), fixtureShellQuote(f.marker), fixtureShellQuote(native))
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	f.manager.Git.Executable = wrapper
	gitTest(t, f.fork, "config", "receive.denyCurrentBranch", "updateInstead")
}

func TestPRGitToolForkPushIsOriginalBoundAndNeverReplayed(t *testing.T) {
	f := newPRPreparationFixture(t)
	preparePRFixGitTransport(t, f)
	manifest, err := f.manager.Prepare(context.Background(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := f.manager.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), f.request, manifest)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	root := manifest.Repositories[0].Path
	gitTest(t, root, "config", "user.name", "PR fixture")
	gitTest(t, root, "config", "user.email", "fixture@example.invalid")
	selection := domain.PRFixExecution{AttemptID: domain.NewID(), Target: *f.request.Repositories[0].PRTarget, Strategy: domain.RebaseConflictStrategy}
	os.WriteFile(f.marker, []byte("deny-push"), 0600)
	if _, err := lease.PreparePRGitTool(context.Background(), selection, f.request, manifest); domain.SafeError(err).Code != domain.MissingInput {
		t.Fatal("missing write auth accepted", err)
	}
	os.Remove(f.marker)
	tool, err := lease.PreparePRGitTool(context.Background(), selection, f.request, manifest)
	if err != nil {
		t.Fatal(err)
	}
	defer tool.Close()
	if p := tool.VerifyPush(context.Background()); p.State != domain.PRPushUnchanged {
		t.Fatal("native success without push", p.State)
	}
	for _, entry := range tool.Environment(nil) {
		name, value, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, prGitEnvironmentPrefix) && !slices.Contains([]string{prGitEnvironmentPrefix + "SCOPE", prGitEnvironmentPrefix + "ENDPOINT", prGitEnvironmentPrefix + "TOKEN"}, name) {
			t.Fatal("native lookup context reached harness", name)
		}
		if strings.HasPrefix(name, prGitEnvironmentPrefix) {
			t.Setenv(name, value)
		}
	}
	var plan bytes.Buffer
	if err := RunPRGit(context.Background(), tool.path, []string{"delidev-target"}, &plan); err != nil || !strings.Contains(plan.String(), "fixture-author/fork-repo.git") {
		t.Fatal("fork command plan", err)
	}
	os.WriteFile(filepath.Join(root, "fix.txt"), []byte("isolated fix\n"), 0600)
	for _, args := range [][]string{{"add", "fix.txt"}, {"commit", "-m", "controlled fix"}} {
		if err := RunPRGit(context.Background(), tool.path, args, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
	}
	push := tool.scope.commandPlan()["push_argv"].([]string)
	gitTest(t, root, "config", "url.https://example.invalid/.pushInsteadOf", tool.scope.commandPlan()["push_argv"].([]string)[3])
	if err := RunPRGit(context.Background(), tool.path, push, &bytes.Buffer{}); err == nil {
		t.Fatal("push-only rewrite accepted", err)
	}
	gitTest(t, root, "config", "--unset-all", "url.https://example.invalid/.pushInsteadOf")

	// Forging mutable private metadata supplies no Worker-owned push proof.
	claimPath := filepath.Join(filepath.Dir(tool.path), "push.json")
	os.WriteFile(claimPath, []byte(`{"selection_digest":"`+selection.Digest()+`","head":"`+f.next+`","mac":"forged"}`), 0600)
	if p := tool.VerifyPush(context.Background()); p.State != domain.PRPushUncertain {
		t.Fatal("forged claim supplied proof", p.State)
	}
	os.Remove(claimPath)
	alteredScope := slices.Clone(tool.raw)
	alteredScope = bytes.Replace(alteredScope, []byte("fixture-author"), []byte("foreign-author"), 1)
	os.WriteFile(tool.path, alteredScope, 0600)
	if err := RunPRGit(context.Background(), tool.path, []string{"status"}, &bytes.Buffer{}); err == nil {
		t.Fatal("changed scope supplied command authority")
	}
	os.WriteFile(tool.path, tool.raw, 0600)

	gitTest(t, f.fork, "update-ref", "refs/heads/feature", f.next)
	if err := RunPRGit(context.Background(), tool.path, push, &bytes.Buffer{}); err == nil {
		t.Fatal("moved remote head accepted")
	}
	gitTest(t, f.fork, "update-ref", "refs/heads/feature", f.head)
	// restore the checked-out test fixture after update-ref before updateInstead.
	gitTest(t, f.fork, "reset", "--hard", f.head)
	if err := RunPRGit(context.Background(), tool.path, push, &bytes.Buffer{}); err != nil {
		t.Fatal("bound fork push", err)
	}
	p := tool.VerifyPush(context.Background())
	if p.State != domain.PRPushVerified || !p.Matches(selection, lease.claim.ExecutionID) || gitTest(t, f.fork, "rev-parse", "feature") != p.ResultHead || gitTest(t, f.source, "rev-parse", "main") != f.base {
		t.Fatal("wrong independently verified source", p.State)
	}
	if err := RunPRGit(context.Background(), tool.path, push, &bytes.Buffer{}); err == nil {
		t.Fatal("push replay accepted")
	}
	if err := tool.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RunPRGit(context.Background(), tool.path, []string{"status"}, &bytes.Buffer{}); err == nil {
		t.Fatal("closed bridge accepted native command")
	}
	altered := append(slices.Clone(push), "--force")
	if tool.scope.validateArgs(altered) == nil {
		t.Fatal("unrestricted force accepted")
	}
	tool.scope.Selection.Conflict = true
	rebasePush := tool.scope.commandPlan()["push_argv"].([]string)
	if !slices.Contains(rebasePush, "--force-with-lease=refs/heads/feature:"+f.head) || tool.scope.validateArgs(rebasePush) != nil {
		t.Fatal("rebase lost exact lease")
	}
	for _, args := range [][]string{{"rebase", f.next}, {"push", "--force"}, {"remote", "set-url", "origin", "other"}} {
		if tool.scope.validateArgs(args) == nil {
			t.Fatal("foreign operation accepted")
		}
	}
}

// This isolated read exercises Git's real push-only configuration grammar
// without opening a network transport or invoking the inference harness.
func TestPRGitPushOnlyRewriteCannotSubstituteDestination(t *testing.T) {
	home := t.TempDir()
	for key, value := range map[string]string{"HOME": home, "USERPROFILE": home, "GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_SYSTEM": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1", "XDG_CONFIG_HOME": home} {
		t.Setenv(key, value)
	}
	root := repository(t)
	m := manager(t)
	if err := m.initialize(); err != nil {
		t.Fatal(err)
	}
	git := m.Git
	git.OwnerID, git.readOnly = domain.NewID(), true
	address := "https://github.com/fixture-author/fork.git"
	if err := git.rejectPRPushRewrite(context.Background(), root, address); err != nil {
		t.Fatal("missing rewrite", err)
	}
	gitTest(t, root, "config", "url.https://example.invalid/.pushInsteadOf", "https://github.com/fixture-author/")
	if err := git.rejectPRPushRewrite(context.Background(), root, address); domain.SafeError(err).Code != domain.MissingInput {
		t.Fatal("matching rewrite accepted", err)
	}
	if err := git.rejectPRPushRewrite(context.Background(), root, "https://github.com/other-owner/repo.git"); err != nil {
		t.Fatal("unrelated rewrite changed source", err)
	}
}

// This real conflict requires the normal noninteractive rebase editor path.
// The original message and exact-head lease survive resolution and publication.
func TestPRGitToolRebaseConflictContinuesWithoutHarnessEditor(t *testing.T) {
	f := newPRPreparationFixture(t)
	preparePRFixGitTransport(t, f)
	for _, key := range []string{"EDITOR", "VISUAL", "GIT_EDITOR", "GIT_SEQUENCE_EDITOR"} {
		t.Setenv(key, "")
	}
	t.Setenv("TERM", "dumb")
	if err := os.WriteFile(filepath.Join(f.source, "tracked.txt"), []byte("Base changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, f.source, "commit", "-am", "base change")
	f.base = gitTest(t, f.source, "rev-parse", "HEAD")
	f.request.Repositories[0].PRTarget.BaseSHA = f.base
	f.request.Repositories[0].Base.Name = f.base
	manifest, err := f.manager.Prepare(context.Background(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := f.manager.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), f.request, manifest)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	root := manifest.Repositories[0].Path
	gitTest(t, root, "config", "user.name", "PR fixture")
	gitTest(t, root, "config", "user.email", "fixture@example.invalid")
	selection := domain.PRFixExecution{AttemptID: domain.NewID(), Target: *f.request.Repositories[0].PRTarget, Strategy: domain.RebaseConflictStrategy, Conflict: true}
	tool, err := lease.PreparePRGitTool(context.Background(), selection, f.request, manifest)
	if err != nil {
		t.Fatal(err)
	}
	defer tool.Close()
	for _, entry := range tool.Environment(nil) {
		name, value, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, prGitEnvironmentPrefix) {
			t.Setenv(name, value)
		}
	}
	if err := RunPRGit(context.Background(), tool.path, []string{"rebase", f.base}, &bytes.Buffer{}); err == nil {
		t.Fatal("fixture did not create the real conflict")
	}
	if !strings.Contains(gitTest(t, root, "status", "--porcelain"), "UU tracked.txt") {
		t.Fatal("fixture lacks the conflicted index")
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("Resolved base and PR change\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "tracked.txt"}, {"rebase", "--continue"}} {
		if err := RunPRGit(context.Background(), tool.path, args, &bytes.Buffer{}); err != nil {
			t.Fatal("resolved rebase could not continue", err)
		}
	}
	if gitTest(t, root, "log", "-1", "--format=%B") != "PR change" {
		t.Fatal("rebase rewrote the original commit message")
	}
	push := tool.scope.commandPlan()["push_argv"].([]string)
	if !slices.Contains(push, "--force-with-lease=refs/heads/feature:"+f.head) {
		t.Fatal("rebase lost the original head lease")
	}
	if err := RunPRGit(context.Background(), tool.path, push, &bytes.Buffer{}); err != nil {
		t.Fatal("resolved exact-lease push", err)
	}
	if err := tool.Close(); err != nil {
		t.Fatal(err)
	}
	proof := tool.VerifyPush(context.Background())
	if proof.State != domain.PRPushVerified || !proof.Matches(selection, lease.claim.ExecutionID) || proof.ResultHead == f.head || gitTest(t, f.fork, "rev-parse", "feature") != proof.ResultHead {
		t.Fatal("resolved rebase lacks independent original proof", proof.State)
	}
}
