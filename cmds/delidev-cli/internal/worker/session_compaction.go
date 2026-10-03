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
	"runtime"
	"slices"
	"strconv"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type sessionCompactionCheckpoint struct {
	Version            uint32                        `json:"version"`
	ServerID           domain.ID                     `json:"server_id"`
	DeviceID           domain.ID                     `json:"device_id"`
	JobID              domain.ID                     `json:"job_id"`
	AssignmentRevision uint64                        `json:"assignment_revision"`
	InstanceID         domain.ID                     `json:"instance_id"`
	AssignmentDigest   string                        `json:"assignment_digest"`
	RegistrationDigest string                        `json:"registration_digest"`
	CommandDigest      string                        `json:"command_digest"`
	Input              domain.SessionCompactionInput `json:"input"`
	NativeRef          claude.CheckpointReference    `json:"native_reference"`
	Native             json.RawMessage               `json:"native"`
}

const (
	maxCompactionCheckpoint            = 10 << 20
	compactionCheckpointVersion uint32 = 2
)

// Claims use a closed filename set inside the original action's private scope.
type compactionClaimName string

const (
	compactionRegistrationClaim compactionClaimName = "compaction-registration.json"
	compactionCommandClaim      compactionClaimName = "compaction-command.json"
)

type sessionCompactionRegistration struct {
	ActionID         domain.ID `json:"action_id"`
	ExecutionID      domain.ID `json:"execution_id"`
	RequestID        domain.ID `json:"request_id"`
	CredentialDigest string    `json:"credential_digest"`
}

type sessionCompactionCommand struct {
	ActionID              domain.ID `json:"action_id"`
	ExecutionID           domain.ID `json:"execution_id"`
	RegistrationRequestID domain.ID `json:"registration_request_id"`
	CredentialDigest      string    `json:"credential_digest"`
}

func compactionClaimDigest(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return executionInputDigest(raw)
}

// Checkpoint bytes are pinned by the server, but their original private claims
// must independently survive. Neither a completed outer journal nor a native
// snapshot can manufacture missing registration/send authority after replacement.
func readSessionCompactionClaims(root string, p sessionCompactionCheckpoint) error {
	if p.Version != compactionCheckpointVersion {
		return domain.CompactionUncertain()
	}
	return readCompactionClaimRecords(root, p.JobID, p.Input, p.RegistrationDigest, p.CommandDigest)
}

func readCompactionClaimRecords(root string, job domain.ID, input domain.SessionCompactionInput, registrationDigest, commandDigest string) error {
	if job.Validate() != nil || !canonicalDigest(registrationDigest) || !canonicalDigest(commandDigest) {
		return domain.CompactionUncertain()
	}
	directory := filepath.Join(root, "jobs", string(job))
	for _, path := range []string{root, filepath.Join(root, "jobs"), directory} {
		if security.CheckPrivateDir(path) != nil {
			return domain.CompactionUncertain()
		}
	}
	read := func(name compactionClaimName, digest string, target any) error {
		raw, err := security.ReadPrivate(filepath.Join(directory, string(name)), 4<<10)
		if err != nil || executionInputDigest(raw) != digest || domain.Decode(raw, target) != nil {
			return domain.CompactionUncertain()
		}
		canonical, err := json.Marshal(target)
		if err != nil || !bytes.Equal(raw, canonical) {
			return domain.CompactionUncertain()
		}
		return nil
	}
	var registration sessionCompactionRegistration
	var command sessionCompactionCommand
	if read(compactionRegistrationClaim, registrationDigest, &registration) != nil || read(compactionCommandClaim, commandDigest, &command) != nil ||
		registration.ActionID != input.ActionID || registration.ExecutionID != input.Assignment.ExecutionID ||
		domain.UniqueIDs([]domain.ID{job, registration.ActionID, registration.ExecutionID, registration.RequestID}) != nil || !canonicalDigest(registration.CredentialDigest) ||
		command.ActionID != registration.ActionID || command.ExecutionID != registration.ExecutionID || command.RegistrationRequestID != registration.RequestID || command.CredentialDigest != registration.CredentialDigest {
		return domain.CompactionUncertain()
	}
	return nil
}

func writeCompactionClaim(root string, job domain.ID, name compactionClaimName, value any) error {
	if job.Validate() != nil || name != compactionRegistrationClaim && name != compactionCommandClaim {
		return domain.CompactionUncertain()
	}
	// Compaction has no execution publisher to create its per-job directory.
	// Validate every component again before either claim, including the command
	// claim written after native launch, without repairing a foreign scope.
	directory := filepath.Join(root, "jobs", string(job))
	for _, path := range []string{root, filepath.Join(root, "jobs"), directory} {
		if err := security.PrivateDir(path); err != nil {
			return domain.CompactionUncertain()
		}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return domain.CompactionUncertain()
	}
	// Creation itself claims the send exactly once. Even a partial write must
	// remain authoritative after loss of the outer journal; never replace or
	// remove it to manufacture a fresh native command opportunity.
	path := filepath.Join(directory, string(name))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return domain.CompactionUncertain()
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return domain.CompactionUncertain()
	}
	if err := f.Sync(); err != nil {
		return domain.CompactionUncertain()
	}
	if err := f.Close(); err != nil {
		return domain.CompactionUncertain()
	}
	if err := security.SyncParent(path); err != nil {
		return domain.CompactionUncertain()
	}
	return nil
}

type compactionPhase string

const (
	compactionPrepare  compactionPhase = "prepare"
	compactionRestore  compactionPhase = "restore"
	compactionRegister compactionPhase = "register"
	compactionLaunch   compactionPhase = "launch"
	compactionCommand  compactionPhase = "command"
	compactionSettle   compactionPhase = "settle"
	compactionCleanup  compactionPhase = "cleanup"
	compactionRetain   compactionPhase = "retain"
)

func compactionCheckpointPath(root string, action domain.ID) (string, error) {
	if action.Validate() != nil {
		return "", domain.CompactionUncertain()
	}
	dir := filepath.Join(root, "compaction-checkpoints")
	return filepath.Join(dir, string(action)+".json"), nil
}
func executeSessionCompaction(ctx context.Context, config Config, owner domain.ID, job domain.Job) (output json.RawMessage, returned error) {
	bounded, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	ctx = bounded
	c := config.execution
	var i domain.SessionCompactionInput
	if c == nil || c.Assignment == nil || domain.ID(c.Assignment.Id) != owner || config.executionContext == nil || domain.Decode(job.Input, &i) != nil || i.Validate() != nil || c.Credential.MachineID != i.Assignment.MachineID || job.ParentID != i.SourceJobID || job.AssignedDeviceID != c.Credential.DeviceID || job.InstanceID != c.Instance || job.MachineID != c.Credential.MachineID || c.Assignment.Revision == 0 {
		return nil, domain.CompactionUncertain()
	}
	if i.Assignment.Configuration.Harness == domain.Codex {
		return executeCodexSessionCompaction(ctx, config, owner, job, i)
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("job_id", owner, "action_id", i.ActionID, "session_id", i.Assignment.SessionID)
	logger.InfoContext(ctx, "session_compaction_started")
	phase := compactionPrepare
	defer func() {
		if returned != nil {
			logger.WarnContext(ctx, "session_compaction_uncertain", "phase", phase, "code", domain.SafeError(returned).Code)
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
	permission, effort, err := claudeExecutionSettings(i.Assignment.Configuration, i.Assignment.Input.Mode)
	if err != nil {
		return nil, err
	}
	executable := i.Assignment.Installation.ResolvedPath
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil || !filepath.IsAbs(executable) || resolved != executable {
		return nil, domain.CompactionUncertain()
	}
	runtimeRoot := filepath.Join(config.Root, "runtimes")
	if err := security.PrivateDir(runtimeRoot); err != nil {
		return nil, domain.SafeError(err)
	}
	home := filepath.Join(runtimeRoot, string(i.ActionID))
	if _, err := os.Lstat(home); !errors.Is(err, os.ErrNotExist) {
		return nil, domain.CompactionUncertain()
	}
	env, err := harness.PrivateRuntimeEnvironment(home)
	if err != nil {
		return nil, domain.SafeError(err)
	}
	// The existing original checkpoint derives the private runtime paths. The
	// action never creates a replacement conversation or submits its old input.
	native := claude.APIStreamConfig{Process: process.Config{Directory: filepath.Join(config.Root, "processes"), OwnerID: owner, Executable: executable, Logger: logger, Env: env}, Version: claude.SupportedVersion, Workspace: lease.WorkingDirectory(), WorkspaceRoots: nativeWorkspaceRoots(manifest), SessionID: i.Assignment.SessionID, Model: i.Assignment.Configuration.NativeModel, Permission: permission, Effort: effort, Instructions: i.Assignment.Configuration.Instructions, API: claude.APIConfig{ServerOrigin: c.Credential.Endpoint}}
	phase = compactionRestore
	closed, err := readClaudeContinuation(ctx, config.Root, c.Credential, i.Restore, manifest, native)
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
	intent := sessionCompactionRegistration{ActionID: i.ActionID, ExecutionID: i.Assignment.ExecutionID, RequestID: registration, CredentialDigest: hex.EncodeToString(digest[:])}
	if err := writeCompactionClaim(config.Root, owner, compactionRegistrationClaim, intent); err != nil {
		return nil, domain.CompactionUncertain()
	}
	phase = compactionRegister
	registerCtx, stop := context.WithTimeout(ctx, 30*time.Second)
	reply, err := c.Client.RegisterExecution(registerCtx, authenticated(c.Credential, &pb.RegisterExecutionRequest{Mutation: &pb.Mutation{RequestId: string(registration), Id: string(owner), ExpectedRevision: c.Assignment.Revision}, MachineId: string(i.Assignment.MachineID), InstanceId: string(c.Instance), CredentialDigest: digest[:]}))
	stop()
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	if reply == nil || reply.Msg == nil || reply.Msg.ProxyPath != apiproxy.Prefix {
		return nil, domain.CompactionUncertain()
	}
	resume := claude.ContinueSuccessfulRun
	if i.Previous != nil && i.Previous.RequiresResume {
		resume = claude.ResumeTerminalRun
	}
	phase = compactionLaunch
	api, err := claude.ContinueAPISession(ctx, closed, owner, claude.APIConfig{ServerOrigin: c.Credential.Endpoint, Token: token}, resume)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := api.Close(); err != nil {
			output, returned = nil, err
		}
	}()
	// Synchronize the original command claim before its one native send. The
	// outer Worker journal refuses all interrupted starts, including pre-ack loss.
	commandClaim := sessionCompactionCommand{ActionID: i.ActionID, ExecutionID: i.Assignment.ExecutionID, RegistrationRequestID: registration, CredentialDigest: intent.CredentialDigest}
	if err := writeCompactionClaim(config.Root, owner, compactionCommandClaim, commandClaim); err != nil {
		return nil, domain.CompactionUncertain()
	}
	phase = compactionCommand
	if _, err := api.StartCompaction(ctx, i.ActionID); err != nil {
		return nil, err
	}
	phase = compactionSettle
	result, err := consumeSessionCompaction(ctx, api.Next, i.Assignment.SessionID, i.ActionID, i.Assignment.ExecutionID)
	if err != nil {
		return nil, err
	}
	phase = compactionCleanup
	retained, err := api.CloseForContinuation(ctx)
	if err != nil {
		return nil, err
	}
	if err := lease.Close(); err != nil {
		return nil, err
	}
	phase = compactionRetain
	raw, ref, err := retained.RetainCheckpoint(ctx)
	if err != nil {
		return nil, err
	}
	p := sessionCompactionCheckpoint{Version: compactionCheckpointVersion, ServerID: c.Credential.ServerID, DeviceID: c.Credential.DeviceID, JobID: owner, AssignmentRevision: c.Assignment.Revision, InstanceID: c.Instance, AssignmentDigest: executionInputDigest(c.Assignment.DocumentJson), RegistrationDigest: compactionClaimDigest(intent), CommandDigest: compactionClaimDigest(commandClaim), Input: i, NativeRef: ref, Native: raw}
	if err := readSessionCompactionClaims(config.Root, p); err != nil {
		return nil, err
	}
	data, err := json.Marshal(p)
	if err != nil || len(data) > maxCompactionCheckpoint {
		return nil, domain.CompactionUncertain()
	}
	if err := security.PrivateDir(filepath.Join(config.Root, "compaction-checkpoints")); err != nil {
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
	result.CleanupVerified = true
	result.Checkpoint = domain.SessionCompactionRef{JobID: owner, ActionID: i.ActionID, ExecutionID: i.Assignment.ExecutionID, CheckpointDigest: executionInputDigest(data), NativeDigest: ref.SHA256, RequiresResume: ref.RequiresResume}
	if result.Validate() != nil {
		return nil, domain.CompactionUncertain()
	}
	logger.InfoContext(ctx, "session_compaction_cleanup_verified", "compact_result", result.Outcome)
	return json.Marshal(result)
}

// The native controller validates original compact_result independently of the
// outer success envelope. This collector never interprets diagnostic text.
func consumeSessionCompaction(ctx context.Context, next func(context.Context) (claude.LifecycleObservation, error), session, action, execution domain.ID) (domain.SessionCompactionResult, error) {
	r := domain.SessionCompactionResult{Version: 1, ActionID: action, ExecutionID: execution}
	for n := 0; n < domain.MaxExecutionEvents; n++ {
		o, err := next(ctx)
		if err != nil {
			return r, err
		}
		if o.ActionID != action || o.InputID != "" || o.Accepted || o.SessionID != session {
			return r, domain.CompactionUncertain()
		}
		switch o.Kind {
		case claude.SessionInitialized, claude.ProgressObserved:
		case claude.CommandObserved:
			if o.Command == claude.CommandCompleted {
				r.CommandCompletedID = o.NativeID
			}
		case claude.CompactionCommandObserved:
			if o.CompactCommand == nil {
				return r, domain.CompactionUncertain()
			}
			if o.CompactCommand.Kind == claude.CompactionCommandEcho {
				r.CommandEchoID = o.NativeID
			}
		case claude.CompactionObserved:
			if r.Boundary != nil || o.Compaction == nil {
				return r, domain.CompactionUncertain()
			}
			b := o.Compaction
			r.BoundaryID = o.NativeID
			r.Boundary = &domain.ClaudeCompactionBoundary{Trigger: domain.ClaudeCompactionTrigger(b.Trigger), Before: domain.ClaudeProgressCount(strconv.FormatUint(b.Before, 10)), After: claudeCompactionCount(b.After), DurationMS: claudeCompactionCount(b.DurationMS), CumulativeDropped: claudeCompactionCount(b.CumulativeDropped), LogicalParent: cloneClaudeTaskField(b.LogicalParent)}
			if b.Segment != nil {
				r.Boundary.Segment = &domain.ClaudePreservedSegment{Head: b.Segment.Head, Anchor: b.Segment.Anchor, Tail: b.Segment.Tail}
			}
			if b.Messages != nil {
				r.Boundary.Messages = &domain.ClaudePreservedMessages{Anchor: b.Messages.Anchor, IDs: slices.Clone(b.Messages.IDs), AllIDs: slices.Clone(b.Messages.AllIDs)}
			}
		case claude.CompactionSummaryObserved:
			if r.Summary != nil || o.Summary == nil || o.Summary.Text == nil || len(o.Summary.Blocks) != 0 {
				return r, domain.CompactionUncertain()
			}
			r.SummaryID = o.NativeID
			r.Summary = &domain.SessionCompactionSummary{BoundaryID: o.Summary.BoundaryID, Text: *o.Summary.Text}
		case claude.CompactionResultObserved:
			if o.CompactResult == nil || r.ResultID != "" {
				return r, domain.CompactionUncertain()
			}
			r.ResultID = o.NativeID
			r.Outcome = domain.CompactionOutcome(o.CompactResult.Status)
			r.OuterKind, r.OuterError = domain.ClaudeResultKind(o.CompactResult.Kind), o.CompactResult.Error
		case claude.RunStateObserved:
			if o.Run == nil {
				return r, domain.CompactionUncertain()
			}
			if o.Run.State == claude.RunIdle {
				r.IdleID = o.NativeID
				return r, nil
			}
		default:
			return r, domain.CompactionUncertain()
		}
	}
	return r, domain.CompactionUncertain()
}
func readSessionCompactionCheckpoint(ctx context.Context, root string, credential Credential, input domain.ExecutionJobInput, ref domain.SessionCompactionRef, config claude.APIStreamConfig) (*claude.ClosedAPISession, error) {
	if ref.Validate() != nil || input.Continuation == nil || ref.ExecutionID != input.Continuation.Previous.ExecutionID {
		return nil, domain.CompactionUncertain()
	}
	path, err := compactionCheckpointPath(root, ref.ActionID)
	if err != nil {
		return nil, err
	}
	data, err := security.ReadPrivate(path, maxCompactionCheckpoint)
	if err != nil || executionInputDigest(data) != ref.CheckpointDigest {
		return nil, domain.CompactionUncertain()
	}
	var p sessionCompactionCheckpoint
	if json.Unmarshal(data, &p) != nil {
		return nil, domain.CompactionUncertain()
	}
	originalRaw, originalErr := json.Marshal(p.Input.Assignment)
	canonical, err := json.Marshal(p)
	if err != nil || originalErr != nil || !bytes.Equal(data, canonical) || p.Version != compactionCheckpointVersion || p.Input.Validate() != nil || p.JobID != ref.JobID || p.Input.ActionID != ref.ActionID || p.ServerID != credential.ServerID || p.DeviceID != credential.DeviceID || p.Input.Assignment.ExecutionID != ref.ExecutionID || p.Input.Assignment.SessionID != input.SessionID || p.Input.Assignment.ConfigurationDigest != input.ConfigurationDigest || p.Input.Assignment.AccountID != input.AccountID || p.Input.Assignment.ConnectionID != input.ConnectionID || p.Input.SourceJobID != input.Continuation.Previous.JobID || executionInputDigest(originalRaw) != input.Continuation.AssignmentInputDigest {
		return nil, domain.CompactionUncertain()
	}
	var journal journal
	raw, err := security.ReadPrivate(filepath.Join(root, "jobs", string(ref.JobID)+".json"), 2<<20)
	var result domain.SessionCompactionResult
	if err != nil || domain.Decode(raw, &journal) != nil || journal.Version != 1 || journal.InstanceID.Validate() != nil || journal.ReportID.Validate() != nil || journal.Revision != p.AssignmentRevision || p.AssignmentRevision == 0 || journal.InstanceID != p.InstanceID || journal.JobID != ref.JobID || journal.Digest != p.AssignmentDigest || (journal.State != journalFinished && journal.State != journalReported) || journal.Problem != nil || domain.Decode(journal.Output, &result) != nil || result.Validate() != nil || result.Checkpoint != ref {
		return nil, domain.CompactionUncertain()
	}
	if err := readSessionCompactionClaims(root, p); err != nil {
		return nil, err
	}
	original := p.Input.Assignment
	expected := claude.CheckpointReference{SHA256: ref.NativeDigest, SessionID: input.SessionID, OwnerID: ref.JobID, InputID: original.InputID, InputSHA256: executionInputDigest([]byte(original.Input.Prompt)), NativeTurnID: string(p.Input.Completion.NativeTurnID), RequiresResume: ref.RequiresResume}
	if p.NativeRef != expected || executionInputDigest(p.Native) != ref.NativeDigest {
		return nil, domain.CompactionUncertain()
	}
	config.Process.OwnerID = ref.JobID
	return claude.RestoreCheckpoint(ctx, config, p.Native, expected)
}
