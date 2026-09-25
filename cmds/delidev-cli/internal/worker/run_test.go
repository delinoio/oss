package worker

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestJournalReusesCompletionAndPreservesInterruptedExecution(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"jobs", "empty-hooks"} {
		if err := security.PrivateDir(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	repo := filepath.Join(t.TempDir(), "checkout")
	cmd := exec.Command("git", "init", "--quiet", repo)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(domain.RepositoryInspectionInput{Path: repo})
	instance := domain.NewID()
	job := domain.Job{Type: domain.InspectRepositoryJob, State: domain.JobClaimed, MachineID: domain.NewID(), InstanceID: instance, Input: input, AcceptedAt: time.Now().UTC()}
	raw, _ := json.Marshal(job)
	resource := &pb.Resource{Id: string(domain.NewID()), Revision: 2, Kind: pb.EntityKind_ENTITY_KIND_JOB, DocumentJson: raw}
	first, err := runJob(context.Background(), Config{Root: root}, instance, resource, job)
	if err != nil || first.Problem != nil {
		t.Fatalf("execution: %v %v", err, first.Problem)
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	replay, err := runJob(context.Background(), Config{Root: root}, instance, resource, job)
	if err != nil || string(replay.Output) != string(first.Output) || replay.ReportID != first.ReportID {
		t.Fatalf("completion was reexecuted: %v", err)
	}
	first.State = journalStarted
	first.Output = nil
	if err := writeJSON(filepath.Join(root, "jobs", resource.Id+".json"), first); err != nil {
		t.Fatal(err)
	}
	interrupted, err := runJob(context.Background(), Config{Root: root}, instance, resource, job)
	if err != nil || interrupted.Problem == nil || interrupted.Problem.Code != domain.RecoveryRequired {
		t.Fatalf("interrupted execution replayed: %v %+v", err, interrupted)
	}
	resource.Revision++
	if _, err := runJob(context.Background(), Config{Root: root}, instance, resource, job); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("changed assignment reused journal: %v", err)
	}
}
