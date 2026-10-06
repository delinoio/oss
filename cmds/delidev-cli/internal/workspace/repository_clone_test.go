// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func cloneFixture(t *testing.T) (Git, string, CloneRequest, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable fixture; native Windows clone acceptance is separate")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	source, parent, private := repository(t), t.TempDir(), t.TempDir()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	executable := filepath.Join(t.TempDir(), "git-fixture")
	marker := filepath.Join(t.TempDir(), "clone-invoked")
	body := "#!/bin/sh\ncase \" $* \" in\n*' clone '*)\n"
	body += "printf invoked >> " + quote(marker) + "\n"
	body += "for operand do target=$operand; done\n"
	body += quote(git) + " -c core.hooksPath=/dev/null clone --no-local -- " + quote(source) + " \"$target\" || exit 1\n"
	body += quote(git) + " -C \"$target\" remote set-url origin https://github.com/fixture/repo.git\n;;\n*) exec " + quote(git) + " \"$@\" ;;\nesac\n"
	if err := os.WriteFile(executable, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	job := domain.NewID()
	return Git{Executable: executable, ProcessRoot: filepath.Join(private, "processes"), OwnerID: job}, private, CloneRequest{JobID: job, ParentPath: parent, URL: "https://github.com/fixture/repo.git", DirectoryName: "repo"}, marker
}

func TestRepositoryClonePublishesFullCheckoutOnce(t *testing.T) {
	g, private, request, marker := cloneFixture(t)
	var logs bytes.Buffer
	g.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	result, err := g.Clone(context.Background(), private, request)
	if err != nil {
		t.Fatal(err)
	}
	parent, _ := filepath.EvalSymlinks(request.ParentPath)
	if result.Root != filepath.Join(parent, "repo") || result.Name != "repo" || result.GitHubRepositories["origin"].Owner != "fixture" {
		t.Fatal(result)
	}
	if _, err := os.Stat(filepath.Join(result.Root, "tracked.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(result.Root, ".git", "shallow")); !os.IsNotExist(err) {
		t.Fatal("unexpected shallow clone", err)
	}
	if _, err := os.Stat(filepath.Join(parent, ".delidev-clone-"+string(request.JobID))); !os.IsNotExist(err) {
		t.Fatal("staging retained after success", err)
	}
	if _, err := g.Clone(context.Background(), private, request); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(marker)
	if err != nil || string(raw) != "invoked" {
		t.Fatal(string(raw), err)
	}
	if strings.Contains(logs.String(), request.URL) || strings.Contains(logs.String(), parent) {
		t.Fatal("clone content entered logs")
	}
}

func TestRepositoryClonePreservesExistingEmptyDestination(t *testing.T) {
	g, private, request, marker := cloneFixture(t)
	final := filepath.Join(request.ParentPath, request.DirectoryName)
	if err := os.Mkdir(final, 0700); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(final)
	if _, err := g.Clone(context.Background(), private, request); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal(err)
	}
	after, err := os.Stat(final)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("existing destination changed")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("clone launched on collision")
	}
}

func TestRepositoryCloneDefiniteFailureRemovesOnlyOwnedStaging(t *testing.T) {
	g, private, request, _ := cloneFixture(t)
	if err := os.WriteFile(g.Executable, []byte("#!/bin/sh\nexit 128\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Clone(context.Background(), private, request); domain.SafeError(err).Code != domain.Unavailable {
		t.Fatal(err)
	}
	parent, _ := filepath.EvalSymlinks(request.ParentPath)
	if _, err := os.Stat(filepath.Join(parent, ".delidev-clone-"+string(request.JobID))); !os.IsNotExist(err) {
		t.Fatal("owned failure staging remained", err)
	}
	if _, err := os.Stat(filepath.Join(parent, request.DirectoryName)); !os.IsNotExist(err) {
		t.Fatal("failure published a checkout")
	}
	if _, err := g.Clone(context.Background(), private, request); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("original journal replayed", err)
	}
}

func TestRepositoryCloneCleanupRejectsReplacedStaging(t *testing.T) {
	parent, private := t.TempDir(), t.TempDir()
	parent, _ = filepath.EvalSymlinks(parent)
	staging := filepath.Join(parent, "owned")
	if err := security.CreatePrivateDirExclusive(staging); err != nil {
		t.Fatal(err)
	}
	identity, _ := directoryPathIdentity(staging)
	parentIdentity, _ := directoryPathIdentity(parent)
	claim := cloneClaim{Version: 1, JobID: domain.NewID(), Parent: parent, ParentIdentity: parentIdentity, Staging: staging, StagingIdentity: identity, Phase: cloneCreated}
	raw, _ := json.Marshal(claim)
	claimPath := filepath.Join(private, "claim.json")
	if err := security.WriteAtomic(claimPath, raw); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staging, staging+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(staging, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(staging, "foreign")
	if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := removeCloneStaging(context.Background(), claimPath, claim); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("foreign staging removed")
	}
}

func TestRepositoryCloneDoesNotAdoptReplacedCheckout(t *testing.T) {
	g, private, request, _ := cloneFixture(t)
	raw, err := os.ReadFile(g.Executable)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(raw), "for operand do target=$operand; done", "for operand do target=$operand; done\nmv \"$target\" \"$target-original\"\nmkdir \"$target\"", 1)
	if err := os.WriteFile(g.Executable, []byte(changed), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Clone(context.Background(), private, request); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("replaced checkout accepted", err)
	}
	parent, _ := filepath.EvalSymlinks(request.ParentPath)
	if _, err := os.Stat(filepath.Join(parent, request.DirectoryName)); !os.IsNotExist(err) {
		t.Fatal("foreign checkout published")
	}
	staging := filepath.Join(parent, ".delidev-clone-"+string(request.JobID))
	if _, err := os.Stat(filepath.Join(staging, "checkout", "tracked.txt")); err != nil {
		t.Fatal("unproven replacement files were removed", err)
	}
}

func TestRepositoryCloneConcurrentDestinationPublishesOnlyOne(t *testing.T) {
	g, private, first, _ := cloneFixture(t)
	second := first
	second.JobID = domain.NewID()
	other := g
	other.OwnerID = second.JobID
	outcomes := make(chan error, 2)
	go func() { _, err := g.Clone(context.Background(), private, first); outcomes <- err }()
	go func() { _, err := other.Clone(context.Background(), private, second); outcomes <- err }()
	success, conflict := 0, 0
	for range 2 {
		err := <-outcomes
		if err == nil {
			success++
		} else if domain.SafeError(err).Code == domain.Conflict {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("concurrent clone publication did not conflict")
	}
	parent, _ := filepath.EvalSymlinks(first.ParentPath)
	if _, err := os.Stat(filepath.Join(parent, "repo", "tracked.txt")); err != nil {
		t.Fatal("winning checkout lost", err)
	}
	for _, job := range []domain.ID{first.JobID, second.JobID} {
		if _, err := os.Stat(filepath.Join(parent, ".delidev-clone-"+string(job))); !os.IsNotExist(err) {
			t.Fatal("owned staging retained after definite result", err)
		}
	}
}
func TestRepositoryCloneAuthenticationFailureIsRedacted(t *testing.T) {
	g, private, request, _ := cloneFixture(t)
	if err := os.WriteFile(g.Executable, []byte("#!/bin/sh\nprintf 'fatal: Authentication failed for private-native-content\\n' >&2\nexit 128\n"), 0700); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	g.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	_, err := g.Clone(context.Background(), private, request)
	problem := domain.SafeError(err)
	if problem.Code != domain.PermissionDenied || problem.Cause != "clone_authentication" || strings.Contains(problem.Error(), "private-native-content") || strings.Contains(logs.String(), "private-native-content") {
		t.Fatal("unclassified or disclosed authentication error")
	}
}
func TestRepositoryCloneTimeoutJoinsBeforeOwnedCleanup(t *testing.T) {
	g, private, request, _ := cloneFixture(t)
	if err := os.WriteFile(g.Executable, []byte("#!/bin/sh\nexec sleep 20\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := g.Clone(ctx, private, request)
	problem := domain.SafeError(err)
	if problem.Code != domain.Unavailable || problem.Cause != "clone_timeout" {
		t.Fatal(problem)
	}
	parent, _ := filepath.EvalSymlinks(request.ParentPath)
	if _, err := os.Stat(filepath.Join(parent, ".delidev-clone-"+string(request.JobID))); !os.IsNotExist(err) {
		t.Fatal("joined failure staging survived", err)
	}
}
