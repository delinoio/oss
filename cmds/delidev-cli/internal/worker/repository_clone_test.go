// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestRepositoryCloneJournalNeverReexecutesAfterCapabilityChangeOrRestart(t *testing.T) {
	root := t.TempDir()
	if err := security.PrivateDir(filepath.Join(root, "jobs")); err != nil {
		t.Fatal(err)
	}
	machine, instance := domain.NewID(), domain.NewID()
	input, _ := json.Marshal(domain.RepositoryCloneInput{RepositoryID: domain.NewID(), MachineID: machine, LocalOrigin: domain.LocalOrigin{MachineID: machine, DeviceID: domain.NewID()}, URL: "https://example.com/repo.git", ParentPath: "/unavailable/parent", DirectoryName: "repo"})
	job := domain.Job{Type: domain.CloneRepositoryJob, State: domain.JobClaimed, MachineID: machine, InstanceID: instance, Input: input, AcceptedAt: time.Now().UTC()}
	body, _ := json.Marshal(job)
	resource := &pb.Resource{Id: string(domain.NewID()), Kind: pb.EntityKind_ENTITY_KIND_JOB, Revision: 2, DocumentJson: body}
	first, err := runJob(context.Background(), Config{Root: root}, instance, resource, job)
	if err != nil || first.Problem == nil || first.Problem.Code != domain.Unsupported {
		t.Fatal("missing capability executed", err)
	}
	replay, err := runJob(context.Background(), Config{Root: root, repositoryClone: true}, instance, resource, job)
	if err != nil || replay.ReportID != first.ReportID || replay.Problem.Code != domain.Unsupported {
		t.Fatal("finished clone reran", err)
	}
	first.State, first.Output, first.Problem = journalStarted, nil, nil
	if err := writeJSON(filepath.Join(root, "jobs", resource.Id+".json"), first); err != nil {
		t.Fatal(err)
	}
	interrupted, err := runJob(context.Background(), Config{Root: root, repositoryClone: true}, instance, resource, job)
	if err != nil || interrupted.Problem == nil || interrupted.Problem.Code != domain.RecoveryRequired {
		t.Fatal("interrupted clone reran", err)
	}
	if _, err := runJob(context.Background(), Config{Root: root, repositoryClone: true}, domain.NewID(), resource, job); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("replacement Worker adopted original clone", err)
	}
}
