// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestReconciledStaleStorageReportDiscardsOnlyPendingReceipt(t *testing.T) {
	root := t.TempDir()
	config := Config{Root: root, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	machine, instance, session, original, snapshot := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	input := workspace.StorageRequest{Version: 1, OperationID: original, Action: workspace.StorageCleanup, SnapshotID: snapshot, Preparation: workspace.PrepareRequest{SessionID: session, MachineID: machine}}
	raw, _ := json.Marshal(input)
	job := domain.Job{Type: domain.WorkspaceStorageJob, State: domain.JobClaimed, MachineID: machine, InstanceID: instance, Input: raw, AcceptedAt: time.Now().UTC()}
	result := journal{Version: 1, JobID: original, InstanceID: instance, Revision: 2, Digest: strings.Repeat("a", 64), State: journalFinished, ReportID: domain.NewID(), Output: json.RawMessage(`{"locally_completed":true}`)}
	if err := security.PrivateDir(filepath.Join(root, "jobs")); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(root, "jobs", string(original)+".json"), result); err != nil {
		t.Fatal(err)
	}
	if err := prepareStorageRetirement(config, job, result); err != nil {
		t.Fatal(err)
	}
	intent := filepath.Join(root, "storage-removal-intents", string(original)+".json")
	if err := security.PrivateDir(filepath.Dir(intent)); err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(intent, []byte("original native recovery evidence")); err != nil {
		t.Fatal(err)
	}
	accepted := job
	accepted.State = domain.JobSucceeded
	accepted.StorageReconciledBy = domain.NewID()
	accepted.Output = json.RawMessage(`{"independently_recovered":true}`)
	document, _ := json.Marshal(accepted)
	ack := &pb.Resource{Id: string(original), SessionId: string(session), Kind: pb.EntityKind_ENTITY_KIND_JOB, SchemaVersion: 1, Revision: 5, DocumentJson: document}
	handler := &failedStorageRecoveryReport{ack: ack, result: result}
	_, h := delidevv1connect.NewWorkerServiceHandler(handler)
	server := httptest.NewServer(h)
	defer server.Close()
	client := delidevv1connect.NewWorkerServiceClient(server.Client(), server.URL)
	if err := replayPendingStorageReports(context.Background(), config, client, Credential{MachineID: machine}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "storage-removal-retirements", string(original)+".json")); !os.IsNotExist(err) {
		t.Fatal("stale pending report remains", err)
	}
	if raw, err := os.ReadFile(intent); err != nil || string(raw) != "original native recovery evidence" {
		t.Fatal("stale acknowledgement retired recovery evidence", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "jobs", string(original)+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var reported journal
	if domain.Decode(raw, &reported) != nil || reported.State != journalReported || reported.ReportID != result.ReportID || storageJournalResultDigest(reported) != storageJournalResultDigest(result) {
		t.Fatal("original local journal identity changed")
	}
	if err := replayPendingStorageReports(context.Background(), config, nil, Credential{}); err != nil {
		t.Fatal("stale receipt blocks restart", err)
	}
	if err := retireStorageReports(context.Background(), config); err != nil {
		t.Fatal("native evidence blocks startup", err)
	}
}
