// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func deletionWorkerFixture(t *testing.T, kind domain.WorkspaceType) (Config, domain.SessionDeletionWork, workspace.Manifest, string) {
	t.Helper()
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "worker")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := workspace.Manager{Root: root, Logger: logger}
	machine, session, job, instance := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	p := workspace.PrepareRequest{SessionID: session, MachineID: machine, OriginMachineID: machine, Type: kind, Repositories: []workspace.RepositorySpec{}}
	source := ""
	if kind != domain.GeneralChat {
		source = t.TempDir()
		cmd := exec.Command("git", "-C", source, "init", "-b", "main")
		if b, e := cmd.CombinedOutput(); e != nil {
			t.Fatal(e, string(b))
		}
		if e := os.WriteFile(filepath.Join(source, "tracked.txt"), []byte("original"), 0600); e != nil {
			t.Fatal(e)
		}
		for _, args := range [][]string{{"add", "tracked.txt"}, {"-c", "user.name=Deletion Test", "-c", "user.email=deletion@example.invalid", "-c", "commit.gpgSign=false", "commit", "-m", "initial"}} {
			cmd := exec.Command("git", append([]string{"-C", source}, args...)...)
			if b, e := cmd.CombinedOutput(); e != nil {
				t.Fatal(e, string(b))
			}
		}
		id := domain.NewID()
		p.Repositories = []workspace.RepositorySpec{{ID: id, Checkout: source, Starting: domain.Reference{Type: domain.LocalBranch, Name: "main"}}}
		p.PrimaryRepository = id
	}
	manifest, e := m.Prepare(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	root = m.Root
	if e := security.PrivateDir(filepath.Join(root, "jobs")); e != nil {
		t.Fatal(e)
	}
	input, _ := json.Marshal(p)
	j := domain.Job{Type: domain.PrepareWorkspaceJob, State: domain.JobClaimed, MachineID: machine, InstanceID: instance, AssignedDeviceID: domain.NewID(), Input: input, AcceptedAt: time.Now().UTC()}
	raw, _ := json.Marshal(j)
	hash := sha256.Sum256(raw)
	output, _ := json.Marshal(manifest)
	if e := writeJSON(filepath.Join(root, "jobs", string(job)+".json"), journal{Version: 1, JobID: job, InstanceID: instance, Revision: 2, Digest: hex.EncodeToString(hash[:]), State: journalReported, ReportID: domain.NewID(), Output: output}); e != nil {
		t.Fatal(e)
	}
	w := domain.SessionDeletionWork{Version: 1, DeletionID: domain.NewID(), ServerID: domain.NewID(), SessionID: session, MachineID: machine, DeviceID: j.AssignedDeviceID, Copies: []domain.SessionDeletionCopy{{JobID: job, Type: j.Type, Revision: 2, Digest: hex.EncodeToString(hash[:]), InstanceID: instance}}, PreparationDigests: []string{manifest.InputDigest}}
	return Config{Root: root, Logger: logger}, w, manifest, source
}

func TestSessionDeletionWorkerPreservesLocalDirtyIgnoredAndSharedData(t *testing.T) {
	c, w, m, source := deletionWorkerFixture(t, domain.Local)
	for name, body := range map[string]string{"tracked.txt": "dirty original", "ignored.txt": "ignored original", ".gitignore": "ignored.txt\n"} {
		if e := os.WriteFile(filepath.Join(source, name), []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
	}
	shared := filepath.Join(c.Root, "browser-profiles")
	if e := os.Mkdir(shared, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(shared, "cookies"), []byte("shared"), 0600); e != nil {
		t.Fatal(e)
	}
	proof, e := deleteSessionCopies(context.Background(), c, w)
	if e != nil || !proof.Complete {
		t.Fatal(proof, e)
	}
	for name, body := range map[string]string{"tracked.txt": "dirty original", "ignored.txt": "ignored original", ".gitignore": "ignored.txt\n"} {
		b, e := os.ReadFile(filepath.Join(source, name))
		if e != nil || string(b) != body {
			t.Fatal("original checkout changed", name, e)
		}
	}
	if b, e := os.ReadFile(filepath.Join(shared, "cookies")); e != nil || string(b) != "shared" {
		t.Fatal("shared profile changed", e)
	}
	if _, e := os.Lstat(filepath.Dir(filepath.Join(c.Root, "workspaces", string(m.SessionID), "manifest.json"))); !os.IsNotExist(e) {
		t.Fatal("managed workspace retained", e)
	}
	again, e := deleteSessionCopies(context.Background(), c, w)
	if e != nil || again.ReportID != proof.ReportID {
		t.Fatal("cleanup retry lost original acknowledgement", e)
	}
	manager := workspace.Manager{Root: c.Root, Logger: c.Logger}
	if _, e := manager.Prepare(context.Background(), workspace.PrepareRequest{SessionID: w.SessionID, MachineID: w.MachineID, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}}); e == nil {
		t.Fatal("stale preparation recreated deleted session")
	}
}

func TestSessionDeletionWorkerRemovesOnlySelectedWorktreeAndResumesAfterRemoval(t *testing.T) {
	c, w, manifest, source := deletionWorkerFixture(t, domain.Worktree)
	manager := workspace.Manager{Root: c.Root, Logger: c.Logger}
	other, e := manager.Prepare(context.Background(), workspace.PrepareRequest{SessionID: domain.NewID(), MachineID: w.MachineID, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}})
	if e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(manifest.PrimaryPath, "untracked.txt"), []byte("session only"), 0600); e != nil {
		t.Fatal(e)
	}
	proof, e := deleteSessionCopies(context.Background(), c, w)
	if e != nil || !proof.Complete {
		t.Fatal(proof, e)
	}
	if _, e := os.Stat(other.PrimaryPath); e != nil {
		t.Fatal("other session removed", e)
	}
	if b, e := os.ReadFile(filepath.Join(source, "tracked.txt")); e != nil || string(b) != "original" {
		t.Fatal("source changed", e)
	}
	if _, e := os.Stat(manifest.PrimaryPath); !os.IsNotExist(e) {
		t.Fatal("owned files retained", e)
	}
	// Reproduce a crash after all unlinks but before completion persistence.
	proof.Complete = false
	if e := writeJSON(sessionDeletionPath(c.Root, w.SessionID), proof); e != nil {
		t.Fatal(e)
	}
	again, e := deleteSessionCopies(context.Background(), c, w)
	if e != nil || !again.Complete || again.ReportID != proof.ReportID {
		t.Fatal("removal recovery failed", e)
	}
}

func TestSessionDeletionWorkerKeepsUncertainOrForeignOwnershipPending(t *testing.T) {
	c, w, manifest, _ := deletionWorkerFixture(t, domain.GeneralChat)
	path := filepath.Join(c.Root, "jobs", string(w.Copies[0].JobID)+".json")
	raw, e := security.ReadPrivate(path, 2<<20)
	if e != nil {
		t.Fatal(e)
	}
	var j journal
	_ = json.Unmarshal(raw, &j)
	j.Digest = "foreign"
	if e := writeJSON(path, j); e != nil {
		t.Fatal(e)
	}
	if _, e := deleteSessionCopies(context.Background(), c, w); e == nil {
		t.Fatal("foreign ownership removed")
	}
	if _, e := os.Stat(manifest.PrimaryPath); e != nil {
		t.Fatal("uncertain files removed", e)
	}
}

func TestSessionDeletionWorkerKeepsMissingWorkspaceWithoutOriginalProofPending(t *testing.T) {
	c, w, manifest, _ := deletionWorkerFixture(t, domain.GeneralChat)
	if e := os.RemoveAll(filepath.Join(c.Root, "workspaces", string(manifest.SessionID))); e != nil {
		t.Fatal(e)
	}
	if _, e := deleteSessionCopies(context.Background(), c, w); e == nil {
		t.Fatal("absence was treated as ownership proof")
	}
	if _, e := os.Stat(filepath.Join(c.Root, "jobs", string(w.Copies[0].JobID)+".json")); e != nil {
		t.Fatal("original evidence removed", e)
	}
}

func TestSessionDeletionWorkerWaitsForOriginalFinalJournalOwner(t *testing.T) {
	c, w, manifest, _ := deletionWorkerFixture(t, domain.GeneralChat)
	l, e := security.TryLock(filepath.Join(c.Root, "jobs", string(w.Copies[0].JobID)+".lock"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e := deleteSessionCopies(context.Background(), c, w); e == nil {
		t.Fatal("deleted while original job still owned final publication")
	}
	if _, e := os.Stat(manifest.PrimaryPath); e != nil {
		t.Fatal("removed live-owned workspace", e)
	}
	if e := l.Close(); e != nil {
		t.Fatal(e)
	}
	if p, e := deleteSessionCopies(context.Background(), c, w); e != nil || !p.Complete {
		t.Fatal(p, e)
	}
}

func TestSessionDeletionWorkerPreservesReplacementAfterCompletedCleanup(t *testing.T) {
	c, w, manifest, _ := deletionWorkerFixture(t, domain.GeneralChat)
	if _, e := deleteSessionCopies(context.Background(), c, w); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(c.Root, "workspaces", string(manifest.SessionID))
	if e := os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(path, "replacement"), []byte("foreign"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := deleteSessionCopies(context.Background(), c, w); e == nil {
		t.Fatal("completed cleanup ignored a replacement")
	}
	if b, e := os.ReadFile(filepath.Join(path, "replacement")); e != nil || string(b) != "foreign" {
		t.Fatal("foreign replacement removed", e)
	}
}

type deletionReportBarrier struct {
	delidevv1connect.WorkerServiceClient
	entered chan struct{}
	release chan struct{}
}

func (b *deletionReportBarrier) ReportWork(ctx context.Context, _ *connect.Request[pb.ReportWorkRequest]) (*connect.Response[pb.ReportWorkResponse], error) {
	close(b.entered)
	select {
	case <-b.release:
		return connect.NewResponse(&pb.ReportWorkResponse{}), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestSessionDeletionJoinsProductionFinalReportPublication(t *testing.T) {
	c, w, manifest, _ := deletionWorkerFixture(t, domain.GeneralChat)
	copy := &w.Copies[0]
	input, _ := json.Marshal(workspace.PrepareRequest{SessionID: w.SessionID, MachineID: w.MachineID, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}})
	job := domain.Job{Type: domain.PrepareWorkspaceJob, State: domain.JobClaimed, MachineID: w.MachineID, InstanceID: copy.InstanceID, AssignedDeviceID: w.DeviceID, Input: input, AcceptedAt: time.Now().UTC()}
	raw, _ := json.Marshal(job)
	h := sha256.Sum256(raw)
	copy.Digest = hex.EncodeToString(h[:])
	output, _ := json.Marshal(manifest)
	path := filepath.Join(c.Root, "jobs", string(copy.JobID)+".json")
	if err := writeJSON(path, journal{Version: 1, JobID: copy.JobID, InstanceID: copy.InstanceID, Revision: copy.Revision, Digest: copy.Digest, State: journalFinished, ReportID: domain.NewID(), Output: output}); err != nil {
		t.Fatal(err)
	}
	barrier := &deletionReportBarrier{entered: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workCtx, stop := context.WithCancel(ctx)
	resource := &pb.Resource{Id: string(copy.JobID), SessionId: string(w.SessionID), Revision: copy.Revision, DocumentJson: raw}
	done := make(chan error, 1)
	go func() {
		done <- runAndReportJob(ctx, c, barrier, Credential{MachineID: w.MachineID}, copy.InstanceID, assignment{context: workCtx, cancel: stop}, resource, job)
	}()
	select {
	case <-barrier.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("original report did not reach the barrier")
	}
	if _, err := deleteSessionCopies(ctx, c, w); err == nil {
		t.Fatal("removed copies while production final publication was still owned")
	}
	if _, err := os.Stat(manifest.PrimaryPath); err != nil {
		t.Fatal("removed the original workspace before final publication", err)
	}
	close(barrier.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if proof, err := deleteSessionCopies(ctx, c, w); err != nil || !proof.Complete {
		t.Fatal("final publication did not release cleanup", proof, err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("final journal was recreated after deletion", err)
	}
}

func TestSessionDeletionCompletedProofRechecksAllManagedCopies(t *testing.T) {
	c, w, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	for _, kind := range []domain.JobType{domain.ExecuteSessionJob, domain.GenerateSessionTitleJob} {
		copy := domain.SessionDeletionCopy{JobID: domain.NewID(), Type: kind, Revision: 1, Digest: w.Copies[0].Digest, InstanceID: w.Copies[0].InstanceID}
		if kind == domain.ExecuteSessionJob {
			copy.ExecutionID = domain.NewID()
		}
		if err := writeJSON(filepath.Join(c.Root, "jobs", string(copy.JobID)+".json"), journal{Version: 1, JobID: copy.JobID, InstanceID: copy.InstanceID, Revision: copy.Revision, Digest: copy.Digest, State: journalReported, ReportID: domain.NewID()}); err != nil {
			t.Fatal(err)
		}
		w.Copies = append(w.Copies, copy)
	}
	proof, err := deleteSessionCopies(context.Background(), c, w)
	if err != nil || !proof.Complete {
		t.Fatal(proof, err)
	}
	job, execution, title := w.Copies[0], w.Copies[1], w.Copies[2]
	paths := []string{
		filepath.Join("processes", string(job.JobID), "restored.json"),
		filepath.Join("processes", string(job.JobID)+".recovery.lock"),
		filepath.Join("processes", string(w.SessionID), "restored.json"),
		filepath.Join("processes", string(w.SessionID)+".recovery.lock"),
		filepath.Join("title-runtimes", string(title.JobID)+"-restored", "content"),
		filepath.Join("runtimes", string(execution.ExecutionID), "content"),
		filepath.Join("pr-git", string(execution.ExecutionID), "scope.json"),
		filepath.Join("jobs", string(job.JobID), "outbox"),
		filepath.Join("jobs", string(job.JobID)+".json"),
		filepath.Join("workspace-recovery", string(job.JobID)+".json"),
		filepath.Join("execution-claims", string(w.SessionID)+".json"),
		filepath.Join("execution-history", string(w.SessionID), "content"),
		filepath.Join("pr-startup", string(w.SessionID), "content"),
	}
	for _, relative := range paths {
		t.Run(relative, func(t *testing.T) {
			path := filepath.Join(c.Root, relative)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("restored private copy"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := deleteSessionCopies(context.Background(), c, w); err == nil {
				t.Fatal("restored managed copy reused completion")
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != "restored private copy" {
				t.Fatal("replacement copy was removed", err)
			}
			// Remove only this test's restored copy and its empty directory. No
			// cleanup replay is authorized to remove a replacement on its own.
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if filepath.Base(path) == "restored.json" || filepath.Base(path) == "content" || filepath.Base(path) == "outbox" {
				if err := os.Remove(filepath.Dir(path)); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	if again, err := deleteSessionCopies(context.Background(), c, w); err != nil || again.ReportID != proof.ReportID {
		t.Fatal("unchanged completed proof failed after fixture cleanup", again, err)
	}
}
