// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

type openCodeForkCheckpoint struct {
	Version             uint32                              `json:"version"`
	JobID               domain.ID                           `json:"job_id"`
	JobInputDigest      string                              `json:"job_input_digest"`
	ServerID            domain.ID                           `json:"server_id"`
	DeviceID            domain.ID                           `json:"device_id"`
	InstanceID          domain.ID                           `json:"instance_id"`
	Revision            uint64                              `json:"revision"`
	AssignmentDigest    string                              `json:"assignment_digest"`
	SessionID           domain.ID                           `json:"session_id"`
	MachineID           domain.ID                           `json:"machine_id"`
	RuntimeID           domain.ID                           `json:"runtime_id"`
	ConfigurationDigest string                              `json:"configuration_digest"`
	AccountID           domain.ID                           `json:"account_id"`
	ConnectionID        domain.ID                           `json:"connection_id"`
	ManifestDigest      string                              `json:"manifest_digest"`
	Requests            domain.OpenCodeForkRequests         `json:"requests"`
	Claims              []opencode.SessionClaim             `json:"claims"`
	NativeReference     opencode.CheckpointReference        `json:"native_reference"`
	Native              json.RawMessage                     `json:"native"`
	Mappings            []domain.OpenCodeForkMessageMapping `json:"mappings"`
}

// Fork inspects an accepted ordinary source directly. It never constructs a
// pretend continuation assignment to borrow replacement or execution authority.
func readOpenCodeForkSource(ctx context.Context, root string, credential Credential, i domain.ForkJobInput) (openCodeExecutionCheckpoint, error) {
	var empty openCodeExecutionCheckpoint
	a := i.SourceAssignment
	if i.Validate() != nil || a.Configuration.Harness != domain.OpenCode || credential.MachineID != a.MachineID {
		return empty, executionCheckpointUncertain()
	}
	path, err := openCodeCheckpointPath(root, i.SourceJobID)
	if err != nil {
		return empty, err
	}
	raw, err := security.ReadPrivate(path, maxOpenCodeExecutionCheckpointBytes)
	var saved openCodeExecutionCheckpoint
	if err != nil || executionInputDigest(raw) != i.Completion.NativeCheckpointDigest || domain.Decode(raw, &saved) != nil || saved.Version != 2 {
		return empty, executionCheckpointUncertain()
	}
	terminal := i.Completion
	terminal.Version, terminal.NativeCheckpointDigest = 1, ""
	r, ref := saved.Reference, saved.Reference.Claim
	history := a.ExecutionID
	if a.Continuation != nil {
		history = a.Continuation.HistoryExecutionID
	}
	if r.Completion != terminal || r.InputMode != a.Input.Mode || r.PromptSHA256 != executionInputDigest([]byte(a.Input.Prompt)) || r.AssignmentInputSHA256 != executionInputDigest(mustForkJSON(a)) || r.HistoryExecutionID != history || ref.ServerID != credential.ServerID || ref.DeviceID != credential.DeviceID || ref.MachineID != a.MachineID || ref.SessionID != i.SourceSessionID || ref.ExecutionID != a.ExecutionID || ref.JobID != i.SourceJobID || ref.InputID != a.InputID || ref.AccountID != a.AccountID || ref.ConnectionID != a.ConnectionID || ref.ConfigurationDigest != a.ConfigurationDigest || ref.ThreadRequestID != a.ThreadRequestID || ref.InputRequestID != a.TurnRequestID {
		return empty, executionCheckpointUncertain()
	}
	checkpoint, err := readOpenCodeExecutionCheckpoint(ctx, root, r, i.Completion.NativeCheckpointDigest)
	if err != nil {
		return empty, err
	}
	claims, err := readOpenCodeClaims(root, ref)
	claimBytes, encodeErr := json.Marshal(claims)
	if err != nil || encodeErr != nil || executionInputDigest(claimBytes) != r.ClaimsSHA256 || len(claims) < 2 || claims[1].SessionID != checkpoint.NativeReference.SessionID || claims[1].MessageID != checkpoint.NativeReference.InputID || claims[1].PartID != checkpoint.NativeReference.PartID || claims[1].RequestID != checkpoint.NativeReference.InputRequestID || verifyOpenCodeContinuationJournals(root, ref, i.Completion) != nil {
		return empty, executionCheckpointUncertain()
	}
	home := filepath.Join(root, "runtimes", string(a.ExecutionID))
	if err := opencode.InspectForkSourceCheckpoint(ctx, home, checkpoint.Native, checkpoint.NativeReference); err != nil {
		return empty, err
	}
	return checkpoint, nil
}

func forkOpenCodeSession(ctx context.Context, config Config, owner domain.ID, job domain.Job, i domain.ForkJobInput) (output json.RawMessage, returned error) {
	c := config.execution
	if runtime.GOOS == "windows" || c == nil || c.Assignment == nil || domain.ID(c.Assignment.Id) != owner || c.Instance != job.InstanceID || c.Credential.DeviceID != job.AssignedDeviceID || i.Validate() != nil || i.Version != 2 || i.SourceAssignment.Installation.Version != opencode.SupportedVersion {
		return nil, executionCheckpointUncertain()
	}
	var prep workspace.PrepareRequest
	var manifest workspace.Manifest
	a := i.SourceAssignment
	if domain.Decode(a.Preparation, &prep) != nil || domain.Decode(a.Manifest, &manifest) != nil || workspace.ValidateResult(prep, manifest, runtime.GOOS) != nil || manifest.Type != domain.GeneralChat {
		return nil, executionCheckpointUncertain()
	}
	manager := &workspace.Manager{Root: config.Root, Logger: config.Logger}
	inspection, err := manager.InspectClosedExecution(ctx, workspace.ExecutionPredecessor{JobID: i.SourceJobID, ExecutionID: a.ExecutionID}, prep, manifest)
	if err != nil {
		return nil, err
	}
	defer func() {
		if inspection.Close() != nil {
			output, returned = nil, executionCheckpointUncertain()
		}
	}()
	source, err := readOpenCodeForkSource(ctx, manager.Root, c.Credential, i)
	if err != nil {
		return nil, err
	}
	executable := a.Installation.ResolvedPath
	canonical, err := filepath.EvalSymlinks(executable)
	if err != nil || canonical != executable || !filepath.IsAbs(executable) {
		return nil, executionCheckpointUncertain()
	}
	childPrep, err := manager.ForkPreparation(ctx, manifest, i.ChildSessionID, domain.GeneralChat)
	if err != nil {
		return nil, err
	}
	childPrep.ForkProfile = workspace.OpenCodeGeneralChatForkV1
	snapshot, err := manager.InspectForkSnapshot(ctx, manifest, childPrep)
	if err != nil {
		return nil, err
	}
	childManifest, err := manager.PrepareFork(ctx, childPrep, snapshot)
	if err != nil {
		return nil, err
	}
	// A ready unpublished copy now exists. Any later uncertain native/profile
	// failure retains this original job's source reservation and owned evidence.
	defer func() {
		if returned != nil {
			output, returned = nil, executionCheckpointUncertain()
		}
	}()

	// The canonical managed session roots are independently owned siblings. Both
	// manifests must come from the same Worker namespace before native relocation.
	if filepath.Dir(filepath.Dir(childManifest.PrimaryPath)) != filepath.Dir(filepath.Dir(manifest.PrimaryPath)) || childManifest.PrimaryPath == manifest.PrimaryPath {
		return nil, executionCheckpointUncertain()
	}
	runtimeRoot := filepath.Join(manager.Root, "runtimes")
	if security.PrivateDir(runtimeRoot) != nil {
		return nil, executionCheckpointUncertain()
	}
	home := filepath.Join(runtimeRoot, string(i.RuntimeID))
	if _, err := os.Lstat(home); !errors.Is(err, os.ErrNotExist) {
		return nil, executionCheckpointUncertain()
	}
	if security.PrivateDir(home) != nil {
		return nil, executionCheckpointUncertain()
	}
	// Outer Worker claims/envelope stay outside the native snapshot's complete
	// file inventory. Child permanent deletion owns the entire original runtime.
	nativeHome := filepath.Join(home, "native")
	env, err := harness.PrivateRuntimeEnvironment(nativeHome)
	if err != nil {
		return nil, err
	}
	settings, err := openCodeExecutionSettings(a.Configuration, domain.ExecuteMode, "DeliDev session")
	if err != nil {
		return nil, err
	}
	root, err := openCodeWorkspaceRoot(manifest, manifest.PrimaryPath)
	if err != nil {
		return nil, err
	}
	nonce, err := security.RandomToken()
	if err != nil {
		return nil, domain.SafeError(err)
	}
	nativeConfig := opencode.APIExecutionConfig{Probe: opencode.ProbeConfig{Version: a.Installation.Version, Home: filepath.Join(nativeHome, "opencode"), Process: process.Config{Directory: filepath.Join(manager.Root, "processes"), OwnerID: owner, Executable: executable, Cwd: nativeHome, Env: env, Logger: config.Logger}}, Workspace: manifest.PrimaryPath, Root: root, Settings: settings.Session, ServerOrigin: c.Credential.Endpoint, Token: apiproxy.TokenPrefix + nonce, Instructions: settings.Instructions, Rejection: settings.Rejection}
	if a.Configuration.OpenCodeContext != nil {
		nativeConfig.ContextLimit = int64(a.Configuration.OpenCodeContext.Tokens)
		nativeConfig.Prune = a.Configuration.OpenCodeContext.Policy == domain.OpenCodeNativeContextV1
	}
	requests := opencode.ForkRequests{Restore: i.OpenCode.Restore, Fork: i.OpenCode.Fork, Move: i.OpenCode.Move, Mark: i.OpenCode.Mark, DeleteSource: i.OpenCode.DeleteSource}
	resume, err := opencode.CheckpointResumeClaim(nativeConfig, source.NativeReference, requests.Restore)
	if err != nil {
		return nil, err
	}
	claims := []opencode.SessionClaim{}
	nativeConfig.Claim = func(ctx context.Context, claim opencode.SessionClaim) error {
		if ctx.Err() != nil || claim.Validate() != nil || len(claims) >= 5 {
			return executionCheckpointUncertain()
		}
		kinds := []opencode.SessionMutation{opencode.ResumeSessionMutation, opencode.ForkSessionMutation, opencode.MoveForkMutation, opencode.MarkForkMutation, opencode.DeleteForkSourceMutation}
		ids := []domain.ID{requests.Restore, requests.Fork, requests.Move, requests.Mark, requests.DeleteSource}
		index := len(claims)
		if claim.Kind != kinds[index] || claim.RequestID != ids[index] || claim.InputRequestID != source.NativeReference.InputRequestID {
			return executionCheckpointUncertain()
		}
		if index == 0 && claim != resume {
			return executionCheckpointUncertain()
		}
		if index == 1 || index == 4 {
			if claim.SessionID != source.NativeReference.SessionID || claim.MessageID != source.NativeReference.InputID || claim.PartID != source.NativeReference.PartID {
				return executionCheckpointUncertain()
			}
		} else if index > 0 {
			if claim.SessionID == source.NativeReference.SessionID || claim.MessageID == source.NativeReference.InputID || claim.PartID == source.NativeReference.PartID {
				return executionCheckpointUncertain()
			}
			if index == 3 && (claim.SessionID != claims[2].SessionID || claim.MessageID != claims[2].MessageID || claim.PartID != claims[2].PartID) {
				return executionCheckpointUncertain()
			}
		}
		path := filepath.Join(home, "fork-claim-"+strconv.Itoa(index)+".json")
		raw := mustForkJSON(claim)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return executionCheckpointUncertain()
		}
		_, writeErr := file.Write(raw)
		syncErr := file.Sync()
		closeErr := file.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil || security.SyncParent(path) != nil {
			return executionCheckpointUncertain()
		}
		claims = append(claims, claim)
		return nil
	}
	// The fresh nonce is never registered. All provider/model credentials remain
	// in Go's vault; preparation inference fails relay authentication before reads.
	raw, ref, err := opencode.PrepareForkAPI(ctx, nativeConfig, filepath.Join(runtimeRoot, string(a.ExecutionID)), source.Native, source.NativeReference, childManifest.PrimaryPath, requests)
	if err != nil {
		return nil, executionCheckpointUncertain()
	}
	if len(claims) != 5 || snapshot.Verify(ctx, childManifest) != nil || opencode.InspectForkSourceCheckpoint(ctx, filepath.Join(runtimeRoot, string(a.ExecutionID)), source.Native, source.NativeReference) != nil {
		return nil, executionCheckpointUncertain()
	}
	ids, err := opencode.InspectForkCheckpoint(ctx, nativeHome, raw, ref)
	if err != nil {
		return nil, err
	}
	mappings := make([]domain.OpenCodeForkMessageMapping, 0, len(ids))
	for _, id := range ids {
		m := domain.OpenCodeForkMessageMapping{Source: domain.NativeIdentity(id.Source), Child: domain.NativeIdentity(id.Child), Parts: []domain.OpenCodeForkPartMapping{}}
		for _, p := range id.Parts {
			m.Parts = append(m.Parts, domain.OpenCodeForkPartMapping{Source: domain.NativeIdentity(p.Source), Child: domain.NativeIdentity(p.Child)})
		}
		mappings = append(mappings, m)
	}
	value := openCodeForkCheckpoint{Version: 2, JobID: owner, JobInputDigest: executionInputDigest(job.Input), ServerID: c.Credential.ServerID, DeviceID: c.Credential.DeviceID, InstanceID: c.Instance, Revision: c.Assignment.Revision, AssignmentDigest: executionInputDigest(c.Assignment.DocumentJson), SessionID: i.ChildSessionID, MachineID: job.MachineID, RuntimeID: i.RuntimeID, ConfigurationDigest: a.ConfigurationDigest, AccountID: a.AccountID, ConnectionID: a.ConnectionID, ManifestDigest: executionInputDigest(mustForkJSON(childManifest)), Requests: *i.OpenCode, Claims: claims, NativeReference: ref, Native: raw, Mappings: mappings}
	data, err := json.Marshal(value)
	if err != nil || len(data) > maxOpenCodeExecutionCheckpointBytes {
		return nil, executionCheckpointUncertain()
	}
	path := filepath.Join(home, "fork-completion.json")
	if security.WriteAtomic(path, data) != nil || security.SyncParent(path) != nil || ctx.Err() != nil {
		return nil, executionCheckpointUncertain()
	}
	result := domain.ForkJobResult{Version: 2, ChildSessionID: i.ChildSessionID, RuntimeID: i.RuntimeID, NativeThreadID: domain.NativeIdentity(ref.SessionID), NativeTurnID: domain.NativeIdentity(ref.InputID), CheckpointDigest: executionInputDigest(data), Preparation: mustForkJSON(childPrep), Manifest: mustForkJSON(childManifest), CleanupVerified: true, OpenCodeMappings: mappings}
	if result.ValidateIdentity(i) != nil {
		return nil, executionCheckpointUncertain()
	}
	return json.Marshal(result)
}

func readOpenCodeForkCheckpoint(ctx context.Context, root string, credential Credential, i domain.ExecutionJobInput) (openCodeForkCheckpoint, error) {
	var value openCodeForkCheckpoint
	f := i.Fork
	if f == nil || i.Validate() != nil || f.Validate(i) != nil || i.Configuration.Harness != domain.OpenCode || runtime.GOOS == "windows" {
		return value, executionCheckpointUncertain()
	}
	if _, err := executionCheckpointPath(root, f.RuntimeID); err != nil {
		return value, err
	}
	home := filepath.Join(root, "runtimes", string(f.RuntimeID))
	raw, err := security.ReadPrivate(filepath.Join(home, "fork-completion.json"), maxOpenCodeExecutionCheckpointBytes)
	if err != nil || executionInputDigest(raw) != f.CheckpointDigest || domain.Decode(raw, &value) != nil || value.Version != 2 || !bytes.Equal(mustForkJSON(value), raw) || value.JobID != f.JobID || value.ServerID != credential.ServerID || value.DeviceID != credential.DeviceID || value.SessionID != i.SessionID || value.MachineID != i.MachineID || credential.MachineID != i.MachineID || value.RuntimeID != f.RuntimeID || value.ConfigurationDigest != i.ConfigurationDigest || value.AccountID != i.AccountID || value.ConnectionID != i.ConnectionID || value.ManifestDigest != executionInputDigest(i.Manifest) || value.NativeReference.SessionID != string(f.NativeThreadID) || value.NativeReference.InputID != string(f.NativeTurnID) || value.NativeReference.CreationRequestID != value.Requests.Fork || value.Requests.Validate() != nil || value.InstanceID.Validate() != nil || value.Revision == 0 || !canonicalDigest(value.AssignmentDigest) || !canonicalDigest(value.JobInputDigest) || len(value.Claims) != 5 {
		return openCodeForkCheckpoint{}, executionCheckpointUncertain()
	}
	ids, err := opencode.InspectForkCheckpoint(ctx, filepath.Join(home, "native"), value.Native, value.NativeReference)
	if err != nil || len(ids) != len(value.Mappings) {
		return openCodeForkCheckpoint{}, executionCheckpointUncertain()
	}
	proofRequests := opencode.ForkRequests{Restore: value.Requests.Restore, Fork: value.Requests.Fork, Move: value.Requests.Move, Mark: value.Requests.Mark, DeleteSource: value.Requests.DeleteSource}
	if opencode.InspectForkClaims(value.Native, value.NativeReference, filepath.Join(home, "native"), proofRequests, value.Claims) != nil {
		return openCodeForkCheckpoint{}, executionCheckpointUncertain()
	}
	kinds := []opencode.SessionMutation{opencode.ResumeSessionMutation, opencode.ForkSessionMutation, opencode.MoveForkMutation, opencode.MarkForkMutation, opencode.DeleteForkSourceMutation}
	requests := []domain.ID{value.Requests.Restore, value.Requests.Fork, value.Requests.Move, value.Requests.Mark, value.Requests.DeleteSource}
	for index, claim := range value.Claims {
		record, err := security.ReadPrivate(filepath.Join(home, "fork-claim-"+strconv.Itoa(index)+".json"), maxOpenCodeClaimBytes)
		if err != nil || !bytes.Equal(record, mustForkJSON(claim)) || claim.Validate() != nil || claim.Kind != kinds[index] || claim.RequestID != requests[index] {
			return openCodeForkCheckpoint{}, executionCheckpointUncertain()
		}
	}
	for index, id := range ids {
		m := value.Mappings[index]
		if m.Source != domain.NativeIdentity(id.Source) || m.Child != domain.NativeIdentity(id.Child) || len(m.Parts) != len(id.Parts) {
			return openCodeForkCheckpoint{}, executionCheckpointUncertain()
		}
		for p, pair := range id.Parts {
			if m.Parts[p].Source != domain.NativeIdentity(pair.Source) || m.Parts[p].Child != domain.NativeIdentity(pair.Child) {
				return openCodeForkCheckpoint{}, executionCheckpointUncertain()
			}
		}
	}
	return value, nil
}
