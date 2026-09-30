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

func TestPRGitToolForkPushIsOriginalBoundAndNeverReplayed(t *testing.T) {
	f := newPRPreparationFixture(t)
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
	manifest, err := f.manager.Prepare(context.Background(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := f.manager.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), f.request, manifest)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
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
	if p := tool.VerifyPush(context.Background()); p.State != domain.PRPushUnchanged {
		t.Fatal("native success without push", p.State)
	}
	for _, entry := range tool.Environment(nil) {
		name, value, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, prGitEnvironmentPrefix) {
			t.Setenv(name, value)
		}
	}
	var plan bytes.Buffer
	if err := RunPRGit(context.Background(), tool.path, []string{"delidev-target"}, &plan); err != nil || !strings.Contains(plan.String(), "fixture-author/fork-repo.git") {
		t.Fatal("fork command plan", err)
	}
	root := manifest.Repositories[0].Path
	gitTest(t, root, "config", "user.name", "PR fixture")
	gitTest(t, root, "config", "user.email", "fixture@example.invalid")
	os.WriteFile(filepath.Join(root, "fix.txt"), []byte("isolated fix\n"), 0600)
	for _, args := range [][]string{{"add", "fix.txt"}, {"commit", "-m", "controlled fix"}} {
		if err := RunPRGit(context.Background(), tool.path, args, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
	}
	push := tool.scope.commandPlan()["push_argv"].([]string)
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
