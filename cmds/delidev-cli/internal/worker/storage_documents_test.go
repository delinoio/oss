// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func TestStorageRecoveryLargeDispatchAndAcknowledgementRemainExact(t *testing.T) {
	// Byte-bound protocol fixture; contextual filesystem validation is separate.
	original := workspace.StorageRequest{Version: 1, OperationID: domain.NewID(), Action: workspace.StoragePreview, Preparation: workspace.PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), ForkSourcePath: "/" + strings.Repeat("metadata", 80_000)}}
	input := original
	input.OperationID, input.Action = domain.NewID(), workspace.StorageRecover
	input.Recovery = &workspace.StorageRecovery{Original: original, Claims: []workspace.StorageJournalClaim{{JobID: original.OperationID, InstanceID: domain.NewID(), Revision: 2, AssignmentDigest: strings.Repeat("a", 64)}}}
	raw, _ := json.Marshal(input)
	job := domain.Job{Type: domain.WorkspaceStorageJob, State: domain.JobClaimed, MachineID: original.Preparation.MachineID, InstanceID: domain.NewID(), Input: raw, AcceptedAt: time.Now().UTC()}
	document, _ := json.Marshal(job)
	if len(document) <= 1<<20 {
		t.Fatal("fixture did not exceed the ordinary envelope bound")
	}
	var decoded domain.Job
	if decodeAssignedJob(document, &decoded) != nil || !bytes.Equal(decoded.Input, job.Input) || decoded.InstanceID != job.InstanceID {
		t.Fatal("large dispatch changed original ownership")
	}
	job.State = domain.JobSucceeded
	job.Output = json.RawMessage(`{"closed":true}`)
	document, _ = json.Marshal(job)
	if workspace.DecodeStorageJob(document, &decoded) != nil || !bytes.Equal(decoded.Input, raw) || !bytes.Equal(decoded.Output, job.Output) {
		t.Fatal("large acknowledgement changed original bytes")
	}
	// Merely selecting the storage job type cannot expand ordinary operations.
	input.Action, input.Recovery = workspace.StoragePreview, nil
	input.Manifest.PrimaryPath = strings.Repeat("metadata", 100_000)
	job.Input, _ = json.Marshal(input)
	document, _ = json.Marshal(job)
	if decodeAssignedJob(document, &decoded) == nil {
		t.Fatal("ordinary storage inherited the recovery bound")
	}
	if workspace.DecodeStorageRequest(append(raw, []byte(`{}`)...), &input) == nil || workspace.DecodeStorageJob(bytes.Repeat([]byte(" "), workspace.MaxStorageRecoveryJobBytes+1), &decoded) == nil {
		t.Fatal("unbounded or trailing recovery document accepted")
	}
}
