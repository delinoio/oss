// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestOpenCodeCompactionEnvelopeRetainsLargeAcceptedComponents(t *testing.T) {
	p, source := openCodeCheckpointMetadataFixture(t)
	assignment := p.input
	assignment.Input.Prompt = strings.Repeat(`"`, domain.MaxPromptBytes)
	template := domain.AppliedTemplate{ID: domain.NewID(), Revision: 1, Contents: strings.Repeat(`"`, 128<<10)}
	assignment.Configuration.Templates = []domain.AppliedTemplate{template}
	assignment.Configuration.Instructions = template.Contents
	assignment.ConfigurationDigest, _ = assignment.Configuration.Digest()
	done := source.Reference.Completion
	done.Version, done.NativeCheckpointDigest = 2, strings.Repeat("ab", 32)
	progress := domain.ExecutionProgress{JobID: p.job, ExecutionID: assignment.ExecutionID, InputID: assignment.InputID, NativeThreadID: string(done.NativeThreadID), NativeTurnID: string(done.NativeTurnID), LastSequence: done.LastSequence, Outcome: domain.ExecutionSucceeded, CleanupVerified: true, Observed: domain.ObservedExecutionSettings{Model: assignment.Configuration.NativeModel, Permission: domain.PermissionDefault, OpenCodeAgent: domain.OpenCodeBuildAgent}, AcceptedInputs: []domain.ExecutionInputBinding{domain.BindExecutionInput(assignment.InputID, assignment.Input.Prompt)}}
	action := domain.NewID()
	restore := assignment
	restore.Version, restore.ExecutionID, restore.InputID = 2, action, domain.NewID()
	restore.ThreadRequestID, restore.TurnRequestID = domain.NewID(), domain.NewID()
	original, _ := json.Marshal(assignment)
	restore.Continuation = &domain.ExecutionContinuation{HistoryExecutionID: assignment.ExecutionID, HistoryRequestID: domain.NewID(), Previous: progress, Completion: done, AssignmentInputDigest: executionInputDigest(original), InputMode: assignment.Input.Mode, PromptDigest: executionInputDigest([]byte(assignment.Input.Prompt)), Intent: domain.ContinueAutomatically}
	input := domain.SessionCompactionInput{Version: 3, ActionID: action, SourceJobID: p.job, Assignment: assignment, Restore: restore, Completion: done, Dispatch: domain.DispatchReady, Intent: domain.ContinueAutomatically}
	inputBytes, err := json.Marshal(input)
	if err != nil || len(inputBytes) <= 2<<20 || len(inputBytes) > domain.MaxCompactionInputBytes || input.Validate() != nil {
		t.Fatal("fixture needs a valid large accepted OpenCode input", len(inputBytes), input.Validate())
	}
	// This private synthetic payload proves only the outer persistence bound;
	// it deliberately lacks native history/cleanup authority.
	native := json.RawMessage(`{"private_fixture":"` + strings.Repeat("a", (8<<20)-len(`{"private_fixture":""}`)) + `"}`)
	checkpoint := openCodeSessionCompactionCheckpoint{Version: 1, ServerID: p.config.Credential.ServerID, DeviceID: p.config.Credential.DeviceID, JobID: domain.NewID(), InstanceID: domain.NewID(), AssignmentRevision: 2, AssignmentDigest: strings.Repeat("cd", 32), Input: input, Native: native}
	raw, err := encodeOpenCodeCompactionDocument(checkpoint)
	if err != nil || len(raw) <= maxCompactionCheckpoint || len(raw) >= maxOpenCodeCompactionCheckpoint {
		t.Fatal("valid component envelope did not exceed the old bound", len(raw), err)
	}
	root := p.config.Root
	if err := security.PrivateDir(filepath.Join(root, "compaction-checkpoints")); err != nil {
		t.Fatal(err)
	}
	path, err := compactionCheckpointPath(root, action)
	if err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(path, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := security.ReadPrivate(path, maxCompactionCheckpoint); err == nil {
		t.Fatal("fixture fits obsolete 10 MiB bound")
	}
	// A fresh read from disk represents replacement-process persistence, not an
	// in-memory copy or reconstructed native observation.
	read, restored, err := readOpenCodeCompactionDocument(path)
	if err != nil || !bytes.Equal(read, raw) || !reflect.DeepEqual(restored, checkpoint) {
		t.Fatal("large durable envelope cannot survive replacement", err)
	}
	ref := domain.SessionCompactionRef{JobID: checkpoint.JobID, ActionID: action, ExecutionID: assignment.ExecutionID, CheckpointDigest: executionInputDigest(raw), NativeDigest: executionInputDigest(native)}
	if _, err := readOpenCodeSessionCompactionCheckpoint(context.Background(), root, p.config.Credential, restore, ref, source, 0); err == nil {
		t.Fatal("larger envelope manufactured missing native or command evidence")
	}
	for _, invalid := range [][]byte{append([]byte(" "), raw...), bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1), bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"foreign":true`), 1)} {
		if err := security.WriteAtomic(path, invalid); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readOpenCodeCompactionDocument(path); err == nil {
			t.Fatal("noncanonical or mixed document passed the larger reader")
		}
	}
	if err := security.WriteAtomic(path, raw); err != nil {
		t.Fatal(err)
	}
	if err := removeSessionTree(context.Background(), root, path); err != nil {
		t.Fatal("large retained action prevented original cleanup", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("original cleanup left the large checkpoint")
	}
	checkpoint.Native = json.RawMessage(`{"private_fixture":"` + strings.Repeat("a", 8<<20) + `"}`)
	if _, err := encodeOpenCodeCompactionDocument(checkpoint); err == nil {
		t.Fatal("outer allowance enlarged the independent native bound")
	}
}
