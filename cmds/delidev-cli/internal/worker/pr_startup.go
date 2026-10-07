package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func reportPRStartupRejection(config Config, owner domain.ID, job domain.Job, cause error) (json.RawMessage, error) {
	fail := domain.StartupRejectionUncertain
	c := config.execution
	if c == nil || c.Assignment == nil || c.Client == nil || c.Assignment.Kind != pb.EntityKind_ENTITY_KIND_JOB || c.Assignment.SchemaVersion != 1 || domain.ID(c.Assignment.Id) != owner || c.Credential.Validate() != nil || domain.OwnershipBlocks(domain.OwnershipActor, domain.ID(owner), c.Credential.Type != domain.WorkerDevice) {
		return nil, fail()
	}
	var original domain.Job
	var input domain.ExecutionJobInput
	if domain.Decode(c.Assignment.DocumentJson, &original) != nil || domain.Decode(original.Input, &input) != nil || input.Validate() != nil || input.Continuation != nil || c.Assignment.SessionId != string(input.SessionID) ||
		domain.OwnershipBlocks(domain.OwnershipMachine, domain.ID(owner), c.Credential.MachineID != input.MachineID) {
		return nil, fail()
	}
	current, err := json.Marshal(job)
	accepted, acceptedErr := json.Marshal(original)
	if err != nil || acceptedErr != nil || !bytes.Equal(current, accepted) {
		return nil, fail()
	}
	// Only the once-started immutable job may report this phase. Neither an
	// arbitrary error nor a missing publisher/native journal can replace it.
	raw, err := security.ReadPrivate(filepath.Join(config.Root, "jobs", string(owner)+".json"), 2<<20)
	var operation journal
	if err != nil || domain.Decode(raw, &operation) != nil || operation.Version != 1 || operation.State != journalStarted || operation.JobID != owner ||
		domain.OwnershipBlocks(domain.OwnershipInstance, domain.ID(owner), operation.InstanceID != c.Instance) ||
		operation.Revision != c.Assignment.Revision || operation.Digest != executionInputDigest(c.Assignment.DocumentJson) || operation.ReportID.Validate() != nil || operation.Problem != nil || len(operation.Output) != 0 {
		return nil, fail()
	}
	if _, err := os.Lstat(filepath.Join(config.Root, "jobs", string(owner))); !errors.Is(err, os.ErrNotExist) {
		return nil, fail()
	}
	var preparation workspace.PrepareRequest
	var manifest workspace.Manifest
	if domain.Decode(input.Preparation, &preparation) != nil || domain.Decode(input.Manifest, &manifest) != nil {
		return nil, fail()
	}
	manager := workspace.Manager{Root: config.Root, Logger: config.Logger}
	// The preflight may have been canceled. This separate bounded metadata read
	// proves its already joined rejection without extending native lifetime.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	proof, err := manager.ReadPRStartupRejection(ctx, owner, input.ExecutionID, preparation, manifest, runtime.GOOS)
	if err != nil {
		return nil, err
	}
	if !workspace.MatchesPRStartupRejection(cause, proof) {
		return nil, fail()
	}
	result := domain.ExecutionStartupRejection{Version: 1, Type: domain.PRStartupRejectedResult, ServerID: c.Credential.ServerID, DeviceID: c.Credential.DeviceID, InstanceID: c.Instance, MachineID: input.MachineID, InputID: input.InputID, AccountID: input.AccountID, ConnectionID: input.ConnectionID, AssignmentRevision: c.Assignment.Revision, AssignmentDigest: operation.Digest, AssignmentInputDigest: executionInputDigest(original.Input), ConfigurationDigest: input.ConfigurationDigest, Workspace: proof}
	if result.ValidateAssignment(owner, c.Assignment.Revision, c.Assignment.DocumentJson) != nil {
		return nil, fail()
	}
	return json.Marshal(result)
}
