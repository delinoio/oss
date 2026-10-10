// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	"os"
	"path/filepath"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const maxDirectoryCheckpointBytes = 8 << 20

type directoryClaimName string

const (
	directoryRegistrationClaim directoryClaimName = "directory-registration.json"
	directoryNativeClaim       directoryClaimName = "directory-native-intent.json"
)

type directoryOwnership struct {
	ServerID         domain.ID `json:"server_id"`
	DeviceID         domain.ID `json:"device_id"`
	JobID            domain.ID `json:"job_id"`
	InstanceID       domain.ID `json:"instance_id"`
	Revision         uint64    `json:"revision"`
	AssignmentDigest string    `json:"assignment_digest"`
	InputDigest      string    `json:"input_digest"`
}

type directoryRegistration struct {
	Ownership        directoryOwnership `json:"ownership"`
	RequestID        domain.ID          `json:"request_id"`
	CredentialDigest string             `json:"credential_digest"`
}

type directoryNativeIntent struct {
	Ownership          directoryOwnership    `json:"ownership"`
	GenerationID       domain.ID             `json:"generation_id"`
	RegistrationDigest string                `json:"registration_digest"`
	Native             codex.DirectoryIntent `json:"native"`
}

// This private file retains the new settings beside the immutable original
// execution checkpoint. It cannot manufacture a missing claim or completion,
// clear an uncertain outer journal, or replace original source/account ownership.
type codexDirectoryCheckpoint struct {
	Context            *codex.CompactedCheckpoint    `json:"context,omitempty"`
	Version            uint32                        `json:"version"`
	Ownership          directoryOwnership            `json:"ownership"`
	Input              domain.SessionDirectoryInput  `json:"input"`
	RegistrationDigest string                        `json:"registration_digest"`
	NativeIntentDigest string                        `json:"native_intent_digest"`
	Before             codex.ContinuationCheckpoint  `json:"before"`
	Source             CodexExecutionCheckpoint      `json:"source"`
	Selected           codex.ContinuationCheckpoint  `json:"selected"`
	Reload             codex.DirectoryReloadEvidence `json:"reload"`
}

func directoryClaimPath(root string, job domain.ID, name directoryClaimName, create bool) (string, error) {
	if job.Validate() != nil || name != directoryRegistrationClaim && name != directoryNativeClaim {
		return "", domain.DirectoryUncertain()
	}
	dir := filepath.Join(root, "jobs", string(job))
	for _, path := range []string{root, filepath.Join(root, "jobs"), dir} {
		var err error
		if create {
			err = security.PrivateDir(path)
		} else {
			err = security.CheckPrivateDir(path)
		}
		if err != nil {
			return "", domain.DirectoryUncertain()
		}
	}
	return filepath.Join(dir, string(name)), nil
}
func writeDirectoryClaim(root string, job domain.ID, name directoryClaimName, value any) error {
	path, err := directoryClaimPath(root, job, name, true)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 2<<20 {
		return domain.DirectoryUncertain()
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return domain.DirectoryUncertain()
	}
	defer f.Close()
	if _, err = f.Write(raw); err != nil {
		return domain.DirectoryUncertain()
	}
	if f.Sync() != nil || f.Close() != nil || security.SyncParent(path) != nil {
		return domain.DirectoryUncertain()
	}
	return nil
}
func readDirectoryClaim(root string, job domain.ID, name directoryClaimName, digest string, target any) error {
	path, err := directoryClaimPath(root, job, name, false)
	if err != nil {
		return err
	}
	raw, err := security.ReadPrivate(path, 2<<20)
	if err != nil || !canonicalDigest(digest) || executionInputDigest(raw) != digest || domain.DecodeWithLimit(raw, target, 2<<20) != nil {
		return domain.DirectoryUncertain()
	}
	canonical, err := json.Marshal(target)
	if err != nil || !bytes.Equal(canonical, raw) {
		return domain.DirectoryUncertain()
	}
	return nil
}
func directoryCheckpointPath(root string, generation domain.ID, create bool) (string, error) {
	if generation.Validate() != nil || !filepath.IsAbs(root) {
		return "", domain.DirectoryUncertain()
	}
	dir := filepath.Join(root, "directory-checkpoints")
	for _, path := range []string{root, dir} {
		var err error
		if create {
			err = security.PrivateDir(path)
		} else {
			err = security.CheckPrivateDir(path)
		}
		if err != nil {
			return "", domain.DirectoryUncertain()
		}
	}
	return filepath.Join(dir, string(generation)+".json"), nil
}
func retainDirectoryCheckpoint(root string, p codexDirectoryCheckpoint) (domain.SessionDirectoryRef, error) {
	var empty domain.SessionDirectoryRef
	if p.validateClaims(root) != nil {
		return empty, domain.DirectoryUncertain()
	}
	var registration directoryRegistration
	var intent directoryNativeIntent
	if readDirectoryClaim(root, p.Ownership.JobID, directoryRegistrationClaim, p.RegistrationDigest, &registration) != nil || readDirectoryClaim(root, p.Ownership.JobID, directoryNativeClaim, p.NativeIntentDigest, &intent) != nil || registration.RequestID.Validate() != nil || !canonicalDigest(registration.CredentialDigest) || registration.Ownership != p.Ownership || intent.Ownership != p.Ownership || intent.GenerationID != p.Input.GenerationID || intent.RegistrationDigest != p.RegistrationDigest || intent.Native.RequestID != p.Input.RequestID || intent.Native.Destination != p.Selected.Effective.Cwd {
		return empty, domain.DirectoryUncertain()
	}
	raw, err := json.Marshal(p)
	if err != nil || len(raw) > maxDirectoryCheckpointBytes {
		return empty, domain.DirectoryUncertain()
	}
	path, err := directoryCheckpointPath(root, p.Input.GenerationID, true)
	if err != nil {
		return empty, err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return empty, domain.DirectoryUncertain()
	}
	if security.WriteAtomic(path, raw) != nil {
		return empty, domain.DirectoryUncertain()
	}
	ref := domain.SessionDirectoryRef{GenerationID: p.Input.GenerationID, JobID: p.Ownership.JobID, RequestID: p.Input.RequestID, ExecutionID: p.Input.Assignment.ExecutionID, RepositoryID: p.Input.RepositoryID, RelativePath: p.Input.RelativePath, CheckpointDigest: executionInputDigest(raw)}
	if p.Input.Previous != nil {
		ref.PreviousGenerationID = p.Input.Previous.GenerationID
	}
	if ref.Validate() != nil {
		return empty, domain.DirectoryUncertain()
	}
	return ref, nil
}

func sameDirectoryValue(a, b any) bool {
	x, xe := json.Marshal(a)
	y, ye := json.Marshal(b)
	return xe == nil && ye == nil && bytes.Equal(x, y)
}
func (p codexDirectoryCheckpoint) validateClaims(root string) error {
	if p.Version != 1 || p.Input.Validate() != nil || domain.UniqueIDs([]domain.ID{p.Ownership.ServerID, p.Ownership.DeviceID, p.Ownership.JobID, p.Ownership.InstanceID}) != nil || p.Ownership.Revision == 0 || !canonicalDigest(p.Ownership.AssignmentDigest) || !canonicalDigest(p.Ownership.InputDigest) || !canonicalDigest(p.Reload.ConfigDigest) || p.Reload.Instructions == nil || (p.Input.ContextAction == nil) != (p.Context == nil) {
		return domain.DirectoryUncertain()
	}
	input, err := json.Marshal(p.Input)
	if err != nil || executionInputDigest(input) != p.Ownership.InputDigest {
		return domain.DirectoryUncertain()
	}
	var registration directoryRegistration
	var intent directoryNativeIntent
	if readDirectoryClaim(root, p.Ownership.JobID, directoryRegistrationClaim, p.RegistrationDigest, &registration) != nil || readDirectoryClaim(root, p.Ownership.JobID, directoryNativeClaim, p.NativeIntentDigest, &intent) != nil || registration.RequestID.Validate() != nil || !canonicalDigest(registration.CredentialDigest) || registration.Ownership != p.Ownership || intent.Ownership != p.Ownership || intent.GenerationID != p.Input.GenerationID || intent.RegistrationDigest != p.RegistrationDigest || intent.Native.RequestID != p.Input.RequestID || intent.Native.Destination != p.Selected.Effective.Cwd || !sameDirectoryValue(intent.Native.Source, p.Before) || !sameDirectoryValue(intent.Native.WorkspaceRoots, directoryOriginalRoots(p.Before.Effective)) {
		return domain.DirectoryUncertain()
	}
	selected := p.Before
	selected.Effective = p.Selected.Effective
	if !sameDirectoryValue(selected, p.Selected) || !codex.DirectorySettingsEqual(p.Before.Effective, p.Selected.Effective) || p.Source.JobID != p.Input.SourceJobID || p.Source.Completion.ExecutionID != p.Input.Assignment.ExecutionID || p.Source.SessionID != p.Input.Assignment.SessionID || p.Source.AccountID != p.Input.Assignment.AccountID || p.Source.ConnectionID != p.Input.Assignment.ConnectionID || p.Source.ConfigurationDigest != p.Input.Assignment.ConfigurationDigest || p.Selected.ThreadID != p.Source.Native.ThreadID || p.Selected.TurnID != p.Source.Native.TurnID {
		return domain.DirectoryUncertain()
	}
	for _, instruction := range p.Reload.Instructions {
		if !filepath.IsAbs(instruction.Path) || !canonicalDigest(instruction.Digest) {
			return domain.DirectoryUncertain()
		}
	}
	return nil
}

type directoryProofVisitKey struct{}

func readDirectoryCheckpoint(root string, credential Credential, input domain.ExecutionJobInput, ref domain.SessionDirectoryRef) (codexDirectoryCheckpoint, error) {
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	return readDirectoryCheckpointContext(ctx, root, credential, input, ref)
}

func readDirectoryCheckpointContext(ctx context.Context, root string, credential Credential, input domain.ExecutionJobInput, ref domain.SessionDirectoryRef) (codexDirectoryCheckpoint, error) {
	visited, _ := ctx.Value(directoryProofVisitKey{}).(map[domain.ID]bool)
	if visited == nil {
		visited = make(map[domain.ID]bool)
		ctx = context.WithValue(ctx, directoryProofVisitKey{}, visited)
	}
	if ctx.Err() != nil || visited[ref.GenerationID] {
		return codexDirectoryCheckpoint{}, domain.DirectoryUncertain()
	}
	visited[ref.GenerationID] = true
	defer delete(visited, ref.GenerationID)

	var empty codexDirectoryCheckpoint
	if ref.Validate() != nil || input.Validate() != nil || input.Configuration.Harness != domain.Codex {
		return empty, domain.DirectoryUncertain()
	}
	path, err := directoryCheckpointPath(root, ref.GenerationID, false)
	if err != nil {
		return empty, err
	}
	raw, err := security.ReadPrivate(path, maxDirectoryCheckpointBytes)
	if err != nil || executionInputDigest(raw) != ref.CheckpointDigest {
		return empty, domain.DirectoryUncertain()
	}
	var p codexDirectoryCheckpoint
	if domain.DecodeWithLimit(raw, &p, maxDirectoryCheckpointBytes) != nil || p.validateClaims(root) != nil || p.Ownership.ServerID != credential.ServerID || p.Ownership.DeviceID != credential.DeviceID || p.Ownership.JobID != ref.JobID || p.Input.GenerationID != ref.GenerationID || p.Input.RequestID != ref.RequestID || p.Input.Assignment.ExecutionID != ref.ExecutionID || p.Input.Assignment.SessionID != input.SessionID || p.Input.Assignment.MachineID != input.MachineID || p.Input.Assignment.ConfigurationDigest != input.ConfigurationDigest || p.Input.RepositoryID != ref.RepositoryID || p.Input.RelativePath != ref.RelativePath {
		return empty, domain.DirectoryUncertain()
	}
	var manifest workspace.Manifest
	if domain.Decode(p.Input.Assignment.Manifest, &manifest) != nil {
		return empty, domain.DirectoryUncertain()
	}
	original, sourceErr := directorySourceCheckpoint(root, p.Input, manifest)
	if sourceErr != nil || !sameDirectoryValue(original, p.Source) {
		return empty, domain.DirectoryUncertain()
	}
	contextProof, contextErr := directoryContextCheckpoint(ctx, root, credential, p.Input, p.Source)
	if contextErr != nil || !sameDirectoryValue(contextProof, p.Context) {
		return empty, domain.DirectoryUncertain()
	}
	canonical, err := json.Marshal(p)
	if err != nil || !bytes.Equal(raw, canonical) {
		return empty, domain.DirectoryUncertain()
	}
	previous := domain.ID("")
	if p.Input.Previous != nil {
		previous = p.Input.Previous.GenerationID
	}
	if previous != ref.PreviousGenerationID {
		return empty, domain.DirectoryUncertain()
	}
	journalPath := filepath.Join(root, "jobs", string(ref.JobID)+".json")
	journalRaw, err := security.ReadPrivate(journalPath, 2<<20)
	var j journal
	var result domain.SessionDirectoryResult
	if err != nil || domain.Decode(journalRaw, &j) != nil || j.Version != 1 || j.JobID != ref.JobID || j.InstanceID != p.Ownership.InstanceID || j.Revision != p.Ownership.Revision || j.Digest != p.Ownership.AssignmentDigest || j.ReportID.Validate() != nil || j.State != journalFinished && j.State != journalReported || j.Problem != nil || domain.Decode(j.Output, &result) != nil || result.Validate() != nil || result.Checkpoint != ref {
		return empty, domain.DirectoryUncertain()
	}
	return p, nil
}

func directorySourceCheckpoint(root string, i domain.SessionDirectoryInput, manifest workspace.Manifest) (CodexExecutionCheckpoint, error) {
	if i.Validate() != nil {
		return CodexExecutionCheckpoint{}, domain.DirectoryUncertain()
	}
	binding := domain.BindSessionInput(i.Assignment.InputID, i.Assignment.Input)
	raw, err := hex.DecodeString(binding.PromptDigest)
	if err != nil || len(raw) != sha256.Size {
		return CodexExecutionCheckpoint{}, domain.DirectoryUncertain()
	}
	var prompt [sha256.Size]byte
	copy(prompt[:], raw)
	assignment, err := json.Marshal(i.Assignment)
	if err != nil {
		return CodexExecutionCheckpoint{}, domain.DirectoryUncertain()
	}
	return ReadCodexExecutionCheckpoint(root, ExecutionCheckpointRef{Directory: i.Assignment.Directory, ContextRevision: i.Assignment.ContextRevision, Subscription: i.Assignment.Configuration.Subscription, ApprovalsReviewer: i.Assignment.Configuration.Options.ApprovalsReviewer, JobID: i.SourceJobID, SessionID: i.Assignment.SessionID, MachineID: i.Assignment.MachineID, HistoryExecutionID: i.HistoryExecutionID, AssignmentInputDigest: executionInputDigest(assignment), ConfigurationDigest: i.Assignment.ConfigurationDigest, AccountID: i.Assignment.AccountID, ConnectionID: i.Assignment.ConnectionID, Completion: i.Completion, InputMode: i.Assignment.Input.Mode, PromptDigest: prompt, AcceptedInputs: i.PreviousExecution.AcceptedInputs, WorkspaceRoots: nativeCheckpointWorkspaceRoots(manifest, i.Assignment.Directory)})
}

func directoryOriginalRoots(settings codex.EffectiveSettings) []string {
	if len(settings.WorkspaceRoots) == 0 {
		return []string{settings.Cwd}
	}
	return settings.WorkspaceRoots
}

func directoryContextCheckpoint(ctx context.Context, root string, credential Credential, i domain.SessionDirectoryInput, source CodexExecutionCheckpoint) (*codex.CompactedCheckpoint, error) {
	if i.ContextAction == nil {
		return nil, nil
	}
	input := i.Assignment
	input.ContextRevision = i.ContextRevision
	input.Continuation = &domain.ExecutionContinuation{PreviousDirectory: i.Assignment.Directory, HistoryExecutionID: i.HistoryExecutionID, Previous: i.PreviousExecution, Completion: i.Completion, AssignmentInputDigest: executionInputDigest(mustDirectoryJSON(i.Assignment)), InputMode: i.Assignment.Input.Mode, PromptDigest: domain.BindSessionInput(i.Assignment.InputID, i.Assignment.Input).PromptDigest, Intent: domain.ContinueAutomatically, Compaction: i.ContextAction}
	proof, err := readCodexSessionCompactionCheckpoint(ctx, root, credential, input, *i.ContextAction, source)
	if err != nil {
		return nil, err
	}
	return &proof, nil
}

// A generation owns its original account and history. Later accepted executions
// carry their own account authority; only the directory projection is inherited.
func directoryContinuationProjection(ctx context.Context, root string, credential Credential, input domain.ExecutionJobInput, source CodexExecutionCheckpoint) (CodexExecutionCheckpoint, *codexDirectoryCheckpoint, error) {
	if _, bounded := ctx.Deadline(); !bounded {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeout(ctx, 5*time.Second)
		defer stop()
	}
	if input.Directory == nil {
		return source, nil, nil
	}
	c := input.Continuation
	if c == nil || source.SessionID != input.SessionID || source.MachineID != input.MachineID || source.ConfigurationDigest != input.ConfigurationDigest || source.JobID != c.Previous.JobID || !sameDirectoryValue(source.Directory, c.PreviousDirectory) {
		return CodexExecutionCheckpoint{}, nil, domain.DirectoryUncertain()
	}
	account, connection := input.AccountID, input.ConnectionID
	if c.PreviousAccountID != "" {
		account, connection = c.PreviousAccountID, c.PreviousConnectionID
	}
	if source.AccountID != account || source.ConnectionID != connection {
		return CodexExecutionCheckpoint{}, nil, domain.DirectoryUncertain()
	}
	p, err := readDirectoryCheckpointContext(ctx, root, credential, input, *input.Directory)
	if err != nil {
		return CodexExecutionCheckpoint{}, nil, err
	}
	if source.Completion.ExecutionID == p.Source.Completion.ExecutionID {
		if !sameDirectoryValue(source, p.Source) {
			return CodexExecutionCheckpoint{}, nil, domain.DirectoryUncertain()
		}
		source.Native = p.Selected
	} else if source.Native.Effective.Cwd != p.Selected.Effective.Cwd || !codex.DirectorySettingsEqual(p.Selected.Effective, source.Native.Effective) {
		return CodexExecutionCheckpoint{}, nil, domain.DirectoryUncertain()
	}
	return source, &p, nil
}

func verifyDirectoryReload(ctx context.Context, native *codex.Client, request domain.ID, bound codex.ThreadResult, generation *codexDirectoryCheckpoint, selection *workspace.DirectorySelection) error {
	if generation == nil {
		return nil
	}
	reload, err := native.ReadDirectoryReloadEvidence(ctx, request, bound)
	if err != nil || !sameDirectoryValue(reload, generation.Reload) || selection == nil || selection.Verify() != nil {
		return domain.DirectoryUncertain()
	}
	return nil
}
