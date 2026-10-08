// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestRepositoryBranchesLargeJournalReplaysOriginalResultWithoutNativeWork(t *testing.T) {
	root, instance, id := t.TempDir(), domain.NewID(), domain.NewID()
	input := domain.RepositoryBranchesInput{ProjectID: domain.NewID(), ProjectRevision: 1, RepositoryID: domain.NewID(), RepositoryRevision: 1, MachineID: domain.NewID(), MachineRevision: 1, Source: "https://github.com/fixture/repo.git", Remote: "origin"}
	input.SourceIdentity, _ = domain.RepositoryCloneSourceIdentity(input.Source)
	rawInput, _ := json.Marshal(input)
	result := domain.RepositoryBranchesResult{ProjectID: input.ProjectID, ProjectRevision: 1, RepositoryID: input.RepositoryID, RepositoryRevision: 1, MachineID: input.MachineID, MachineRevision: 1, Remote: "origin", Branches: []string{}, ObservedAt: time.Now().UTC()}
	for i := 0; i < domain.MaxRepositoryBranches; i++ {
		result.Branches = append(result.Branches, fmt.Sprintf("branch%05d-", i)+strings.Repeat("x", 600))
	}
	if result.Validate(input) != nil {
		t.Fatal("invalid complete fixture")
	}
	output, _ := json.Marshal(result)
	if len(output) <= 4<<20 {
		t.Fatal("fixture does not reach previous document reader limits")
	}
	job := domain.Job{Type: domain.DiscoverRepositoryBranchesJob, State: domain.JobClaimed, MachineID: input.MachineID, InstanceID: instance, Input: rawInput, AcceptedAt: time.Now().UTC()}
	document, _ := json.Marshal(job)
	digest := sha256.Sum256(document)
	original := journal{Version: 1, JobID: id, InstanceID: instance, Revision: 2, Digest: hex.EncodeToString(digest[:]), State: journalFinished, ReportID: domain.NewID(), Output: output}
	if err := os.Mkdir(filepath.Join(root, "jobs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(root, "jobs", string(id)+".json"), original); err != nil {
		t.Fatal(err)
	}
	resource := &pb.Resource{Id: string(id), Kind: pb.EntityKind_ENTITY_KIND_JOB, SchemaVersion: 1, Revision: 2, DocumentJson: document}
	observed, err := runJob(context.Background(), Config{Root: root, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, instance, resource, job)
	if err != nil || observed.ReportID != original.ReportID || string(observed.Output) != string(original.Output) {
		t.Fatal("lost complete original journal", err)
	}
	job.State = domain.JobSucceeded
	job.Output = output
	full, _ := json.Marshal(job)
	var decoded domain.Job
	if decodeAssignedJob(full, &decoded) != nil || len(decoded.Output) != len(output) {
		t.Fatal("complete result cannot be read")
	}
	job.Type = domain.InspectRepositoryJob
	full, _ = json.Marshal(job)
	if decodeAssignedJob(full, &decoded) == nil {
		t.Fatal("generic job acquired branch reader allowance")
	}
}
