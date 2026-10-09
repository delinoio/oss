package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/proto"
)

type executionStartupAttempt struct {
	config            Config
	job               domain.ID
	input             domain.ExecutionJobInput
	observation       domain.ExecutionStartupObservation
	executable        string
	readyReported     bool
	firstFailure      error
	cleanupUncertain  bool
	processIndexOwned bool
	imageRejection    *imageStartupRejectionClaim
}

func newExecutionStartupAttempt(config Config, job domain.ID, input domain.ExecutionJobInput) *executionStartupAttempt {
	return &executionStartupAttempt{config: config, job: job, input: input, observation: domain.ExecutionStartupObservation{Phase: domain.StartupResolve, Harness: input.Configuration.Harness, CorrelationID: job, InputDelivery: domain.StartupNotSent}}
}

func resolveExecutionStartup(ctx context.Context, config Config, job domain.ID, input domain.ExecutionJobInput) (domain.Installation, error) {
	selection := *input.Startup
	source := domain.ID("")
	if input.Continuation != nil {
		source = input.Continuation.Previous.JobID
	}
	if input.Fork != nil {
		source = input.Fork.JobID
	}
	if source != "" && selection.ExecutableSHA256 != "" {
		path := filepath.Join(config.Root, "jobs", string(source), "startup-executable.json")
		if input.Fork != nil {
			// Published Fork runtimes belong to the child. Parent deletion may
			// remove the creation job's journal while retaining this identity.
			child := filepath.Join(config.Root, "runtimes", string(input.Fork.RuntimeID), "startup-executable.json")
			if _, err := os.Lstat(child); err == nil {
				path = child
			} else if !errors.Is(err, os.ErrNotExist) {
				return domain.Installation{}, executionCheckpointUncertain()
			}
		}
		raw, err := security.ReadPrivate(path, 8192)
		var previous domain.Installation
		if errors.Is(err, os.ErrNotExist) && filepath.IsAbs(selection.ExplicitPath) {
			// Legacy assignments already pin an absolute executable and digest.
			// Absence never selects PATH or creates new native ownership.
		} else if err != nil || domain.Decode(raw, &previous) != nil || previous.Harness != selection.Harness || previous.ExecutableSHA256 != selection.ExecutableSHA256 || !filepath.IsAbs(previous.ResolvedPath) {
			return domain.Installation{}, executionCheckpointUncertain()
		}
		if err == nil {
			selection.ExplicitPath = previous.ResolvedPath
		}
	}
	installation, err := harness.ResolveExecution(ctx, selection)
	if err != nil {
		// Only this resolver failure is known to precede workspace/native admission.
		// Original-history and journal-publication uncertainty cannot mint proof.
		if domain.SafeError(err).Code != domain.RecoveryRequired {
			config.startup.preparePrelaunchCleanup(err, security.CreatePrivateDirExclusive, security.SyncParent)
		}
		return installation, err
	}
	if err := writeStartupExecutable(config.Root, job, installation); err != nil {
		return domain.Installation{}, publicationUncertain()
	}
	config.startup.executable = installation.ResolvedPath
	config.progress.native(domain.StartupResolve, domain.StartupProgressCompleted)
	return installation, nil
}

func (a *executionStartupAttempt) setPhase(phase domain.ExecutionStartupPhase) {
	if a == nil {
		return
	}
	a.observation.Phase = phase
	if phase >= domain.StartupSettings && phase <= domain.StartupExecution {
		a.config.progress.native(phase, domain.StartupProgressRunning)
	}
	if a.config.Logger != nil {
		a.config.Logger.Info("execution_startup_phase", "job_id", a.job, "phase", phase)
	}
}

func (a *executionStartupAttempt) report(ctx context.Context, name string, o domain.ExecutionStartupObservation) error {
	c := a.config.execution
	request := &pb.ReportExecutionStartupRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(a.job), ExpectedRevision: c.Assignment.Revision}, MachineId: string(a.input.MachineID), InstanceId: string(c.Instance), Observation: rpc.StartupMessage(o)}
	// Retain the exact once-only report before transmission. Response loss never
	// permits an input send; the original journal remains available for recovery.
	raw, err := proto.Marshal(request)
	if err != nil {
		return publicationUncertain()
	}
	directory := filepath.Join(a.config.Root, "jobs", string(a.job))
	if err := security.PrivateDir(directory); err != nil {
		return publicationUncertain()
	}
	if err := writeJSON(filepath.Join(directory, "startup-"+name+".json"), struct {
		Request []byte `json:"request"`
	}{raw}); err != nil {
		return publicationUncertain()
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	response, err := c.Client.ReportExecutionStartup(bounded, authenticated(c.Credential, request))
	if err != nil {
		return rpc.ClientError(err)
	}
	if response == nil || response.Msg == nil || !proto.Equal(response.Msg.Observation, request.Observation) {
		return publicationUncertain()
	}
	return nil
}

func (a *executionStartupAttempt) ready(ctx context.Context, version string) error {
	if a == nil {
		return nil
	}
	a.setPhase(domain.StartupSettings)
	digest, err := harness.InspectExecutable(ctx, a.executable)
	if err != nil || digest != a.observation.ExecutableSHA256 {
		return domain.Fail(domain.RecoveryRequired, "The executable changed during startup.", "Recover the original execution before continuing.")
	}
	if domain.ValidNativeVersionMetadata(version) {
		a.observation.NativeVersion = version
	}
	o := a.observation
	o.State, o.Protocol = domain.StartupReady, domain.ProtocolFor(o.Harness)
	if err := a.report(ctx, "ready", o); err != nil {
		return err
	}
	a.readyReported = true
	// Settings completion follows the original validated live process and
	// successful settings receipt, never phase order or input acceptance.
	a.config.progress.native(domain.StartupSettings, domain.StartupProgressCompleted)
	return nil
}

func (a *executionStartupAttempt) claimInput() {
	if a != nil {
		a.setPhase(domain.StartupInput)
		a.observation.InputDelivery = domain.StartupClaimed
	}
}
func (a *executionStartupAttempt) acknowledgeInput() {
	if a != nil {
		a.setPhase(domain.StartupExecution)
		a.observation.InputDelivery = domain.StartupAcknowledged
	}
}

func (a *executionStartupAttempt) finish(original error) error {
	if a == nil || original == nil {
		return original
	}
	o := a.observation
	first := original
	if a.firstFailure != nil {
		first = a.firstFailure
	}
	o.State, o.ProblemCode, o.Cleanup = domain.StartupUncertain, domain.SafeError(first).Code, domain.StartupCleanupUncertain
	if d := domain.CodexErrorDiagnostic(first); d != nil {
		o.ProblemCode = d.Code
		if domain.ValidNativeVersionMetadata(d.DetectedVersion) {
			o.NativeVersion = d.DetectedVersion
		}
	}
	if a.processIndexOwned && !a.cleanupUncertain && domain.SafeError(original).Code != domain.RecoveryRequired && process.ReconcileOwner(filepath.Join(a.config.Root, "processes"), a.job) == nil {
		o.Cleanup = domain.StartupCleanupConfirmed
	}
	if a.imageRejectionRetained() && o.Cleanup == domain.StartupCleanupConfirmed && o.Phase == domain.StartupInput && o.InputDelivery == domain.StartupClaimed && o.ProblemCode == domain.Unsupported {
		o.InputDelivery = domain.StartupNotSent
		o.FailureKind = domain.StartupImageInputRejected
	}
	if o.InputDelivery == domain.StartupNotSent && o.Cleanup == domain.StartupCleanupConfirmed {
		o.State = domain.StartupFailed
	}
	if o.Validate() != nil {
		o.ProblemCode = domain.Unavailable
	}
	if a.config.Logger != nil {
		a.config.Logger.Warn("execution_startup_failed", "job_id", a.job, "stage", o.Phase, "options", a.input.Configuration.SelectedNativeOptionNames(), "code", o.ProblemCode, "input_delivery", o.InputDelivery, "cleanup", o.Cleanup, "failure_kind", o.FailureKind)
	}
	if err := a.report(context.Background(), "failure", o); err != nil {
		if a.config.Logger != nil {
			a.config.Logger.Warn("execution_startup_report_uncertain", "job_id", a.job, "code", domain.SafeError(err).Code)
		}
		return publicationUncertain()
	}
	return original
}

// Source operations reuse the original private resolved identity, never current
// discovery or PATH. The actual source process still validates its protocol.
func resolveOriginalStartup(ctx context.Context, config Config, source domain.ID, input domain.ExecutionJobInput) (domain.Installation, error) {
	if input.Version != 4 {
		return input.Installation, nil
	}
	raw, err := security.ReadPrivate(filepath.Join(config.Root, "jobs", string(source), "startup-executable.json"), 8192)
	var original domain.Installation
	if err != nil || domain.Decode(raw, &original) != nil || original.Harness != input.Configuration.Harness || !filepath.IsAbs(original.ResolvedPath) || !canonicalDigest(original.ExecutableSHA256) || input.Startup == nil || input.Startup.ExecutableSHA256 != "" && input.Startup.ExecutableSHA256 != original.ExecutableSHA256 {
		return original, executionCheckpointUncertain()
	}
	return harness.ResolveExecution(ctx, domain.ExecutionStartupSelection{Harness: original.Harness, ExplicitPath: original.ResolvedPath, ExecutableSHA256: original.ExecutableSHA256})
}

func (a *executionStartupAttempt) cleanupFailure(original, cleanup error) error {
	if a == nil {
		return errors.Join(cleanup, original)
	}
	if a != nil {
		if a.firstFailure == nil && original != nil {
			a.firstFailure = original
		}
		a.cleanupUncertain = true
	}
	return domain.Fail(domain.RecoveryRequired, "Original execution cleanup could not be confirmed.", "Recover the original attempt before another send.")
}

// A no-send continuation retry may have failed before replacing the original
// closed workspace claim. Preserve that exact native or compaction predecessor.
func retryOriginalWorkspace(input domain.ExecutionJobInput) []workspace.ExecutionPredecessor {
	c := input.Continuation
	if c == nil {
		return nil
	}
	if c.Compaction != nil {
		return []workspace.ExecutionPredecessor{{JobID: c.Compaction.JobID, ExecutionID: c.Compaction.ActionID}}
	}
	return []workspace.ExecutionPredecessor{{JobID: c.Previous.JobID, ExecutionID: c.Previous.ExecutionID}}
}

func writeStartupExecutable(root string, job domain.ID, installation domain.Installation) error {
	directory := filepath.Join(root, "jobs", string(job))
	if err := security.PrivateDir(directory); err != nil {
		return err
	}
	return writeJSON(filepath.Join(directory, "startup-executable.json"), installation)
}

// preparePrelaunchCleanup retains an empty index only for a definite resolver
// failure. Successful startup leaves index creation to the workspace claim.
func (a *executionStartupAttempt) preparePrelaunchCleanup(original error, create func(string) error, syncParent func(string) error) {
	if a == nil {
		return
	}
	root := filepath.Join(a.config.Root, "processes")
	owner := filepath.Join(root, string(a.job))
	err := security.PrivateDir(root)
	if err == nil {
		err = create(owner)
	}
	if err == nil {
		err = syncParent(owner)
	}
	if err != nil {
		// Even an empty retained directory is not proof after failed creation or
		// synchronization. Do not reconcile it or remove another owner's scope.
		a.firstFailure, a.cleanupUncertain = original, true
		if a.config.Logger != nil {
			a.config.Logger.Warn("execution_startup_prelaunch_cleanup_uncertain", "job_id", a.job, "code", domain.SafeError(err).Code)
		}
		return
	}
	a.processIndexOwned = true
}

func (a *executionStartupAttempt) claimedWorkspace() {
	if a != nil {
		a.processIndexOwned = true
	}
}

// Keep the original resolved executable with the published child runtime. This
// file follows child deletion ownership, independently of the source job.
func writeForkStartupExecutable(root string, runtime domain.ID, installation domain.Installation) error {
	directory := filepath.Join(root, "runtimes", string(runtime))
	if err := security.CheckPrivateDir(directory); err != nil {
		return err
	}
	return writeJSON(filepath.Join(directory, "startup-executable.json"), installation)
}
