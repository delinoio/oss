// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// The pinned native document retains its independent 8 MiB ceiling and the
// accepted input retains 3 MiB. Reserve another 1 MiB for the closed ownership,
// digest and claim metadata so either valid component can fit after mutation.
// Keep Claude/Codex's original private checkpoint bound unchanged.
const maxOpenCodeCompactionCheckpoint = (8 << 20) + domain.MaxCompactionInputBytes + (1 << 20)

type openCodeSessionCompactionCheckpoint struct {
	Version            uint32                        `json:"version"`
	ServerID           domain.ID                     `json:"server_id"`
	DeviceID           domain.ID                     `json:"device_id"`
	JobID              domain.ID                     `json:"job_id"`
	InstanceID         domain.ID                     `json:"instance_id"`
	AssignmentRevision uint64                        `json:"assignment_revision"`
	AssignmentDigest   string                        `json:"assignment_digest"`
	RegistrationDigest string                        `json:"registration_digest"`
	CommandDigest      string                        `json:"command_digest"`
	Input              domain.SessionCompactionInput `json:"input"`
	SourceDigest       string                        `json:"source_digest"`
	Resume             opencode.SessionClaim         `json:"resume"`
	Command            opencode.SessionClaim         `json:"command"`
	NativeReference    opencode.CheckpointReference  `json:"native_reference"`
	Native             json.RawMessage               `json:"native"`
}

func executeOpenCodeSessionCompaction(ctx context.Context, config Config, owner domain.ID, job domain.Job, i domain.SessionCompactionInput) (output json.RawMessage, returned error) {
	c := config.execution
	if c == nil || c.Assignment == nil || config.executionContext == nil || i.Validate() != nil || i.Version != 3 || i.Assignment.Configuration.Harness != domain.OpenCode || domain.ID(c.Assignment.Id) != owner {
		return nil, domain.CompactionUncertain()
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("job_id", owner, "action_id", i.ActionID, "session_id", i.Assignment.SessionID)
	phase := compactionPrepare
	defer func() {
		if returned != nil {
			logger.WarnContext(ctx, "opencode_session_compaction_uncertain", "phase", phase, "code", domain.SafeError(returned).Code)
		}
	}()
	var prep workspace.PrepareRequest
	var manifest workspace.Manifest
	if domain.Decode(i.Assignment.Preparation, &prep) != nil || domain.Decode(i.Assignment.Manifest, &manifest) != nil || workspace.ValidateResult(prep, manifest, runtime.GOOS) != nil {
		return nil, domain.CompactionUncertain()
	}
	manager := &workspace.Manager{Root: config.Root, Logger: logger}
	previous := workspace.ExecutionPredecessor{JobID: i.SourceJobID, ExecutionID: i.Assignment.ExecutionID}
	if i.Previous != nil {
		previous = workspace.ExecutionPredecessor{JobID: i.Previous.JobID, ExecutionID: i.Previous.ActionID}
	}
	lease, err := manager.ClaimContinuation(ctx, owner, i.ActionID, previous, prep, manifest)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := lease.Close(); err != nil {
			output, returned = nil, err
		}
	}()
	installation, err := resolveOriginalStartup(ctx, config, i.SourceJobID, i.Assignment)
	if err != nil {
		return nil, err
	}
	executable := installation.ResolvedPath
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil || !filepath.IsAbs(executable) || resolved != executable {
		return nil, domain.CompactionUncertain()
	}
	phase = compactionRestore
	source, err := readOpenCodeContinuationCheckpoint(ctx, config.Root, c.Credential, i.Restore)
	if err != nil {
		return nil, err
	}
	nativeSource, sourceRef := source.Native, source.NativeReference
	sourceHome := filepath.Join(config.Root, "runtimes", string(source.Reference.Claim.ExecutionID))
	if i.Previous != nil {
		prior, err := readOpenCodeSessionCompactionCheckpoint(ctx, config.Root, c.Credential, i.Restore, *i.Previous, source, 0)
		if err != nil {
			return nil, err
		}
		nativeSource, sourceRef = prior.Native, prior.NativeReference
		sourceHome = filepath.Join(config.Root, "runtimes", string(i.Previous.ActionID))
	}
	root, err := openCodeWorkspaceRoot(manifest, lease.WorkingDirectory())
	if err != nil {
		return nil, err
	}
	settings, err := openCodeExecutionSettings(i.Assignment.Configuration, i.Assignment.Input.Mode, "DeliDev session")
	if err != nil {
		return nil, err
	}
	runtimeRoot := filepath.Join(config.Root, "runtimes")
	if security.PrivateDir(runtimeRoot) != nil {
		return nil, domain.CompactionUncertain()
	}
	home := filepath.Join(runtimeRoot, string(i.ActionID))
	if _, err := os.Lstat(home); !errors.Is(err, os.ErrNotExist) {
		return nil, domain.CompactionUncertain()
	}
	env, err := harness.PrivateRuntimeEnvironment(home)
	if err != nil {
		return nil, err
	}
	token, err := security.RandomToken()
	if err != nil {
		return nil, domain.SafeError(err)
	}
	token = apiproxy.TokenPrefix + token
	digest := sha256.Sum256([]byte(token))
	registration := domain.NewID()
	registrationClaim := sessionCompactionRegistration{ActionID: i.ActionID, ExecutionID: i.Assignment.ExecutionID, RequestID: registration, CredentialDigest: hex.EncodeToString(digest[:])}
	if err := writeCompactionClaim(config.Root, owner, compactionRegistrationClaim, registrationClaim); err != nil {
		return nil, err
	}
	phase = compactionRegister
	bounded, stop := context.WithTimeout(ctx, 30*time.Second)
	reply, err := c.Client.RegisterExecution(bounded, authenticated(c.Credential, &pb.RegisterExecutionRequest{Mutation: &pb.Mutation{RequestId: string(registration), Id: string(owner), ExpectedRevision: c.Assignment.Revision}, MachineId: string(i.Assignment.MachineID), InstanceId: string(c.Instance), CredentialDigest: digest[:]}))
	stop()
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	if reply == nil || reply.Msg == nil || reply.Msg.ProxyPath != apiproxy.Prefix {
		return nil, domain.CompactionUncertain()
	}
	nativeCtx, cancel := context.WithCancel(config.executionContext)
	defer cancel()
	stopCancellation := context.AfterFunc(ctx, cancel)
	defer stopCancellation()
	nativeConfig := opencode.APIExecutionConfig{Probe: opencode.ProbeConfig{Version: installation.Version, Home: filepath.Join(home, "opencode"), Process: process.Config{Directory: filepath.Join(config.Root, "processes"), OwnerID: owner, Executable: executable, Cwd: home, Env: env, Logger: logger}}, Workspace: lease.WorkingDirectory(), Root: root, References: openCodeWorkspaceReferences(manifest), ServerOrigin: c.Credential.Endpoint, Token: token, Settings: settings.Session, Instructions: settings.Instructions, Rejection: settings.Rejection}
	if i.Assignment.Configuration.OpenCodeContext != nil {
		nativeConfig.ContextLimit = int64(i.Assignment.Configuration.OpenCodeContext.Tokens)
		nativeConfig.Prune = i.Assignment.Configuration.OpenCodeContext.Policy == domain.OpenCodeNativeContextV1
	}
	resume, err := opencode.CheckpointResumeClaim(nativeConfig, sourceRef, i.Restore.ThreadRequestID)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(struct {
		Provider string `json:"providerID"`
		Model    string `json:"modelID"`
		Auto     bool   `json:"auto"`
	}{settings.Session.Provider, settings.Session.Model, false})
	if err != nil {
		return nil, domain.CompactionUncertain()
	}
	command := opencode.SessionClaim{RequestID: i.ActionID, Kind: opencode.CompactSessionMutation, SessionID: sourceRef.SessionID, MessageID: sourceRef.InputID, PartID: sourceRef.PartID, InputRequestID: sourceRef.InputRequestID, BodyDigest: executionInputDigest(body)}
	nativeConfig.Claim = func(ctx context.Context, claim opencode.SessionClaim) error {
		if ctx.Err() != nil || claim.Validate() != nil {
			return domain.CompactionUncertain()
		}
		var name compactionClaimName
		switch {
		case claim == resume:
			name = openCodeCompactionResumeClaim
		case claim == command:
			name = openCodeCompactionNativeClaim
		default:
			return domain.CompactionUncertain()
		}
		return writeCompactionClaim(config.Root, owner, name, claim)
	}
	phase = compactionLaunch
	api, err := opencode.OpenResumedAPI(nativeCtx, nativeConfig, sourceHome, nativeSource, sourceRef, i.Restore.ThreadRequestID, settings.Session.Agent, false)
	if err != nil {
		return nil, err
	}
	defer func() {
		bounded, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if err := api.Close(bounded); err != nil {
			output, returned = nil, err
		}
	}()
	commandClaim := sessionCompactionCommand{ActionID: i.ActionID, ExecutionID: i.Assignment.ExecutionID, RegistrationRequestID: registration, CredentialDigest: registrationClaim.CredentialDigest}
	if err := writeCompactionClaim(config.Root, owner, compactionCommandClaim, commandClaim); err != nil {
		return nil, err
	}
	phase = compactionCommand
	if _, err := api.StartCompaction(nativeCtx, i.ActionID); err != nil {
		return nil, err
	}
	phase = compactionSettle
	usages := []domain.OpenCodeUsageObservation{}
	seen := map[string]domain.OpenCodeUsageObservation{}
	for count := 0; ; count++ {
		if count >= domain.MaxExecutionEvents {
			return nil, domain.CompactionUncertain()
		}
		o, err := api.Next(ctx)
		if err != nil {
			return nil, err
		}
		if o.ContextOnly && o.Part != nil && o.Part.Kind == opencode.StepFinishPartKind {
			p := o.Part
			if p.Step == nil || p.Step.Usage == nil || p.Step.Cost == nil {
				return nil, domain.CompactionUncertain()
			}
			n := p.Step.Usage
			value := domain.OpenCodeUsageObservation{Source: domain.OpenCodeStepUsage, NativeID: p.ID, NativeParentID: p.MessageID, NativeEstimate: string(*p.Step.Cost), Counts: domain.OpenCodeTokenCounts{Input: strconv.FormatUint(n.Input, 10), Output: strconv.FormatUint(n.Output, 10), Reasoning: strconv.FormatUint(n.Reasoning, 10), CacheRead: strconv.FormatUint(n.CacheRead, 10), CacheWrite: strconv.FormatUint(n.CacheWrite, 10)}}
			if n.Total != nil {
				total := strconv.FormatUint(*n.Total, 10)
				value.Counts.Total = &total
			}
			if value.Validate() != nil || len(seen) >= 128 {
				return nil, domain.CompactionUncertain()
			}
			if old, exists := seen[value.NativeID]; exists {
				if !reflect.DeepEqual(old, value) {
					return nil, domain.CompactionUncertain()
				}
			} else {
				seen[value.NativeID] = value
				usages = append(usages, value)
			}
		}
		progress, err := api.Progress(ctx)
		if err != nil || progress.NeedsRecovery {
			return nil, domain.CompactionUncertain()
		}
		if progress.SettledObserved {
			break
		}
	}
	phase = compactionCleanup
	if _, err := api.CloseCompleted(ctx); err != nil {
		return nil, err
	}
	receipt, err := api.CompactionReceipt(ctx)
	if err != nil || !receipt.NativeAttempted || !receipt.HTTPAccepted || !receipt.LifecycleCompleted || !receipt.CleanupVerified || receipt.Context.Auto || receipt.Context.ActionID != i.ActionID {
		return nil, domain.CompactionUncertain()
	}
	native, nativeRef, err := api.RetainCheckpoint(ctx)
	if err != nil {
		return nil, err
	}
	record, actions, err := opencode.InspectCompactionCheckpoint(ctx, home, native, nativeRef, sourceHome, nativeSource, sourceRef, i.ActionID)
	if err != nil || record.HistoryDigest != receipt.HistorySHA256 {
		return nil, domain.CompactionUncertain()
	}
	if err := lease.Close(); err != nil {
		return nil, err
	}
	phase = compactionRetain
	p := openCodeSessionCompactionCheckpoint{Version: 1, ServerID: c.Credential.ServerID, DeviceID: c.Credential.DeviceID, JobID: owner, InstanceID: c.Instance, AssignmentRevision: c.Assignment.Revision, AssignmentDigest: executionInputDigest(c.Assignment.DocumentJson), RegistrationDigest: compactionClaimDigest(registrationClaim), CommandDigest: compactionClaimDigest(commandClaim), Input: i, SourceDigest: source.NativeReference.SHA256, Resume: resume, Command: command, NativeReference: nativeRef, Native: native}
	if readCompactionClaimRecords(config.Root, owner, i, p.RegistrationDigest, p.CommandDigest) != nil || readOpenCodeCompactionNativeClaims(config.Root, p) != nil {
		return nil, domain.CompactionUncertain()
	}
	data, err := encodeOpenCodeCompactionDocument(p)
	if err != nil || security.PrivateDir(filepath.Join(config.Root, "compaction-checkpoints")) != nil {
		return nil, domain.CompactionUncertain()
	}
	path, err := compactionCheckpointPath(config.Root, i.ActionID)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return nil, domain.CompactionUncertain()
	}
	if err := security.WriteAtomic(path, data); err != nil {
		return nil, domain.CompactionUncertain()
	}
	result := domain.SessionCompactionResult{Version: 3, Harness: domain.OpenCode, ActionID: i.ActionID, ExecutionID: i.Assignment.ExecutionID, Outcome: domain.CompactionSucceeded, CleanupVerified: true, Checkpoint: domain.SessionCompactionRef{JobID: owner, ActionID: i.ActionID, ExecutionID: i.Assignment.ExecutionID, CheckpointDigest: executionInputDigest(data), NativeDigest: nativeRef.SHA256}, OpenCode: &domain.OpenCodeCompactionResult{NativeSessionID: domain.NativeIdentity(sourceRef.SessionID), SourceNativeInputID: domain.NativeIdentity(sourceRef.InputID), UserID: domain.NativeIdentity(record.UserID), PartID: domain.NativeIdentity(record.PartID), SummaryID: domain.NativeIdentity(record.SummaryID), CompletedEventID: domain.NativeIdentity(record.CompletedEventID), HistoryDigest: record.HistoryDigest, Actions: actions, Acknowledged: true, LifecycleCompleted: true, Usages: usages}}
	if result.Validate() != nil {
		return nil, domain.CompactionUncertain()
	}
	logger.InfoContext(ctx, "opencode_session_compaction_cleanup_verified", "native_actions", actions)
	return json.Marshal(result)
}

func readOpenCodeCompactionNativeClaims(root string, p openCodeSessionCompactionCheckpoint) error {
	for _, claim := range []struct {
		name  compactionClaimName
		value opencode.SessionClaim
	}{{openCodeCompactionResumeClaim, p.Resume}, {openCodeCompactionNativeClaim, p.Command}} {
		raw, err := security.ReadPrivate(filepath.Join(root, "jobs", string(p.JobID), string(claim.name)), 4<<10)
		expected, encodeErr := json.Marshal(claim.value)
		if err != nil || encodeErr != nil || claim.value.Validate() != nil || !bytes.Equal(raw, expected) {
			return domain.CompactionUncertain()
		}
	}
	return nil
}

func readOpenCodeSessionCompactionCheckpoint(ctx context.Context, root string, credential Credential, input domain.ExecutionJobInput, ref domain.SessionCompactionRef, source openCodeExecutionCheckpoint, depth int) (openCodeSessionCompactionCheckpoint, error) {
	var empty openCodeSessionCompactionCheckpoint
	if depth >= 128 || ctx.Err() != nil || input.Validate() != nil || ref.Validate() != nil || ref.RequiresResume || input.Configuration.Harness != domain.OpenCode || input.Continuation == nil || ref.ExecutionID != input.Continuation.Previous.ExecutionID {
		return empty, domain.CompactionUncertain()
	}
	path, err := compactionCheckpointPath(root, ref.ActionID)
	if err != nil {
		return empty, err
	}
	raw, p, err := readOpenCodeCompactionDocument(path)
	if err != nil || executionInputDigest(raw) != ref.CheckpointDigest || p.Version != 1 || p.Input.Validate() != nil || p.Input.Version != 3 || p.JobID != ref.JobID || p.Input.ActionID != ref.ActionID ||
		domain.OwnershipBlocks(domain.OwnershipInstance, "", p.ServerID != credential.ServerID) ||
		domain.OwnershipBlocks(domain.OwnershipDevice, "", p.DeviceID != credential.DeviceID) ||
		p.Input.Assignment.ExecutionID != ref.ExecutionID || p.Input.Assignment.SessionID != input.SessionID || p.Input.Assignment.ConfigurationDigest != input.ConfigurationDigest ||
		domain.OwnershipBlocks(domain.OwnershipResource, "", p.Input.Assignment.AccountID != input.AccountID) ||
		domain.OwnershipBlocks(domain.OwnershipResource, "", p.Input.Assignment.ConnectionID != input.ConnectionID) ||
		p.Input.SourceJobID != input.Continuation.Previous.JobID || p.SourceDigest != source.NativeReference.SHA256 || p.NativeReference.SHA256 != ref.NativeDigest ||
		domain.OwnershipBlocks(domain.OwnershipResource, "", p.NativeReference.OwnerID != ref.JobID) {
		return empty, domain.CompactionUncertain()
	}
	canonical, err := json.Marshal(p)
	original, originalErr := json.Marshal(p.Input.Assignment)
	if err != nil || originalErr != nil || !bytes.Equal(canonical, raw) || executionInputDigest(original) != input.Continuation.AssignmentInputDigest {
		return empty, domain.CompactionUncertain()
	}
	sourceRaw, sourceRef := source.Native, source.NativeReference
	sourceHome := filepath.Join(root, "runtimes", string(source.Reference.Claim.ExecutionID))
	if p.Input.Previous != nil {
		prior, err := readOpenCodeSessionCompactionCheckpoint(ctx, root, credential, p.Input.Restore, *p.Input.Previous, source, depth+1)
		if err != nil {
			return empty, err
		}
		sourceRaw, sourceRef = prior.Native, prior.NativeReference
		sourceHome = filepath.Join(root, "runtimes", string(p.Input.Previous.ActionID))
	}
	home := filepath.Join(root, "runtimes", string(ref.ActionID))
	registrationRaw, registrationErr := security.ReadPrivate(filepath.Join(root, "jobs", string(p.JobID), string(compactionRegistrationClaim)), 4<<10)
	var registration sessionCompactionRegistration
	if registrationErr != nil || domain.Decode(registrationRaw, &registration) != nil {
		return empty, domain.CompactionUncertain()
	}
	intent, intentErr := json.Marshal(struct {
		CheckpointSHA256 string
		OwnerID          domain.ID
		RuntimeSHA256    string
		CredentialSHA256 string
	}{sourceRef.SHA256, p.JobID, executionInputDigest([]byte(home)), registration.CredentialDigest})
	if intentErr != nil || p.Resume.BodyDigest != executionInputDigest(intent) {
		return empty, domain.CompactionUncertain()
	}
	body, bodyErr := json.Marshal(struct {
		Provider string `json:"providerID"`
		Model    string `json:"modelID"`
		Auto     bool   `json:"auto"`
	}{openCodeAPIProvider, p.Input.Assignment.Configuration.NativeModel, false})
	if bodyErr != nil || p.Command.BodyDigest != executionInputDigest(body) {
		return empty, domain.CompactionUncertain()
	}

	record, actions, err := opencode.InspectCompactionCheckpoint(ctx, home, p.Native, p.NativeReference, sourceHome, sourceRaw, sourceRef, ref.ActionID)
	if err != nil {
		return empty, err
	}
	if p.Resume.Kind != opencode.ResumeSessionMutation || p.Resume.RequestID != p.Input.Restore.ThreadRequestID || p.Resume.SessionID != sourceRef.SessionID || p.Resume.MessageID != sourceRef.InputID || p.Resume.PartID != sourceRef.PartID || p.Resume.InputRequestID != sourceRef.InputRequestID || p.Command.Kind != opencode.CompactSessionMutation || p.Command.RequestID != ref.ActionID || p.Command.SessionID != sourceRef.SessionID || p.Command.MessageID != sourceRef.InputID || p.Command.PartID != sourceRef.PartID || p.Command.InputRequestID != sourceRef.InputRequestID {
		return empty, domain.CompactionUncertain()
	}
	operationRaw, err := security.ReadPrivate(filepath.Join(root, "jobs", string(ref.JobID)+".json"), 2<<20)
	var operation journal
	var result domain.SessionCompactionResult
	if err != nil || domain.Decode(operationRaw, &operation) != nil || operation.Version != 1 || operation.JobID != ref.JobID || operation.InstanceID.Validate() != nil || operation.ReportID.Validate() != nil ||
		domain.OwnershipBlocks(domain.OwnershipInstance, "", operation.InstanceID != p.InstanceID) ||
		operation.Revision != p.AssignmentRevision || p.AssignmentRevision == 0 || operation.Digest != p.AssignmentDigest || operation.State != journalFinished && operation.State != journalReported || operation.Problem != nil || domain.Decode(operation.Output, &result) != nil || result.Validate() != nil || result.Version != 3 || result.Checkpoint != ref {
		return empty, domain.CompactionUncertain()
	}
	v := result.OpenCode
	if v.NativeSessionID != domain.NativeIdentity(sourceRef.SessionID) || v.SourceNativeInputID != domain.NativeIdentity(sourceRef.InputID) || v.UserID != domain.NativeIdentity(record.UserID) || v.PartID != domain.NativeIdentity(record.PartID) || v.SummaryID != domain.NativeIdentity(record.SummaryID) || v.CompletedEventID != domain.NativeIdentity(record.CompletedEventID) || v.HistoryDigest != record.HistoryDigest || v.Actions != actions || readCompactionClaimRecords(root, p.JobID, p.Input, p.RegistrationDigest, p.CommandDigest) != nil || readOpenCodeCompactionNativeClaims(root, p) != nil {
		return empty, domain.CompactionUncertain()
	}
	return p, nil
}

func encodeOpenCodeCompactionDocument(p openCodeSessionCompactionCheckpoint) ([]byte, error) {
	input, err := json.Marshal(p.Input)
	if err != nil || len(input) > domain.MaxCompactionInputBytes || len(p.Native) > 8<<20 {
		return nil, domain.CompactionUncertain()
	}
	raw, err := json.Marshal(p)
	if err != nil || len(raw) > maxOpenCodeCompactionCheckpoint {
		return nil, domain.CompactionUncertain()
	}
	return raw, nil
}

func readOpenCodeCompactionDocument(path string) ([]byte, openCodeSessionCompactionCheckpoint, error) {
	var p openCodeSessionCompactionCheckpoint
	raw, err := security.ReadPrivate(path, maxOpenCodeCompactionCheckpoint)
	if err != nil || domain.DecodeWithLimit(raw, &p, maxOpenCodeCompactionCheckpoint) != nil {
		return nil, p, domain.CompactionUncertain()
	}
	canonical, err := encodeOpenCodeCompactionDocument(p)
	if err != nil || !bytes.Equal(raw, canonical) {
		return nil, p, domain.CompactionUncertain()
	}
	return raw, p, nil
}
