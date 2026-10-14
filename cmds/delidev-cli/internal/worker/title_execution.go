package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type titleIntent struct {
	Version         uint32    `json:"version"`
	JobID           domain.ID `json:"job_id"`
	OperationID     domain.ID `json:"operation_id"`
	RegistrationID  domain.ID `json:"registration_id"`
	ThreadRequestID domain.ID `json:"thread_request_id"`
	InputID         domain.ID `json:"input_id"`
	TurnRequestID   domain.ID `json:"turn_request_id"`
	TokenDigest     string    `json:"token_digest"`
}

func matchesTitleOption(observed *string, expected string) bool {
	if expected == "" {
		return observed == nil || *observed == ""
	}
	return observed != nil && *observed == expected
}

type titleReasoningBudget struct {
	items map[string]int
	total int
}

func (b *titleReasoningBudget) observe(event codex.Event) bool {
	itemID := event.ItemID
	itemBytes := 0
	switch event.Kind {
	case codex.ArtifactStartedEvent, codex.ArtifactCompletedEvent:
		if event.Artifact == nil || event.Artifact.Kind != codex.ReasoningArtifact || event.Artifact.ID != itemID {
			return false
		}
		itemBytes = len(event.Artifact.Text)
		for _, part := range event.Artifact.Summary {
			itemBytes += len(part)
		}
		for _, part := range event.Artifact.Content {
			itemBytes += len(part)
		}
	case codex.ArtifactDeltaEvent:
		if event.ArtifactDelta == nil || (event.ArtifactDelta.Kind != codex.ReasoningSummaryDelta && event.ArtifactDelta.Kind != codex.ReasoningContentDelta && event.ArtifactDelta.Kind != codex.ReasoningSummaryAdded) {
			return false
		}
		if b.items != nil {
			itemBytes = b.items[itemID]
		}
		itemBytes += len(event.ArtifactDelta.Text)
	default:
		return false
	}
	if itemID == "" || itemBytes < 0 {
		return false
	}
	if b.items == nil {
		b.items = make(map[string]int)
	}
	previous, exists := b.items[itemID]
	if !exists && len(b.items) >= codex.MaxArtifactParts {
		return false
	}
	if itemBytes > previous {
		b.total += itemBytes - previous
		b.items[itemID] = itemBytes
	}
	return b.total <= domain.MaxAutomaticTitleReasoningBytes
}

// Title execution shares passive notification discard boundaries, but retains
// its explicit narrow metadata profile for every auxiliary capability.
func validateTitleMetadata(kind codex.MetadataKind) error {
	switch kind {
	case codex.ThreadIdentityChecked, codex.ThreadSettingsChecked, codex.RemoteControlDisabled, codex.QuotaUnavailable, codex.RawSupplementDiscarded, codex.NativeGoalAbsent, codex.FuzzySearchDiscarded, codex.GatewayOAuthStatusDiscarded, codex.WindowsSandboxWarningDiscarded, codex.WindowsSandboxSetupDiscarded, codex.CurrentTimeReplySent:
		return nil
	default:
		return domain.Fail(domain.Unsupported, "Codex emitted an unsupported title-runtime metadata event.", "Preserve the isolated runtime; the title profile accepts no auxiliary capability or interaction.")
	}
}

func executeSessionTitle(ctx context.Context, config Config, jobID domain.ID, job domain.Job) (output json.RawMessage, returned error) {
	connection := config.execution
	if connection == nil || connection.Assignment == nil || domain.ID(connection.Assignment.Id) != jobID || connection.Client == nil || connection.Instance != job.InstanceID {
		return nil, publicationUncertain()
	}
	var input domain.AuxiliaryTitleInput
	if domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.MachineID != connection.Credential.MachineID || input.OriginalJobID != job.ParentID {
		return nil, publicationUncertain()
	}
	if input.IsManaged() {
		return executeManagedSessionTitle(ctx, config, jobID, job, input)
	}
	runtimeRoot := filepath.Join(config.Root, "title-runtimes")
	if err := security.PrivateDir(runtimeRoot); err != nil {
		return nil, err
	}
	home, err := os.MkdirTemp(runtimeRoot, string(jobID)+"-")
	if err != nil {
		return nil, err
	}
	cleanup := newTitleFinalizer(home, jobID, config.Logger)
	defer cleanup.finish(&output, &returned)
	if err := security.PrivateDir(home); err != nil {
		return nil, err
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return nil, err
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return nil, err
	}
	cleanup.home = home
	processRoot := filepath.Join(config.Root, "processes")
	workdir := filepath.Join(home, "work")
	if err := security.PrivateDir(workdir); err != nil {
		return nil, err
	}
	env, err := harness.PrivateRuntimeEnvironment(home)
	if err != nil {
		return nil, err
	}
	if input.Version == 2 {
		if err := resolveStartupTitleExecutable(ctx, config, &input); err != nil {
			return nil, err
		}
	}
	resolved, err := filepath.EvalSymlinks(input.Executable)
	if err != nil || resolved != input.Executable || !filepath.IsAbs(input.Executable) {
		return nil, domain.Fail(domain.RecoveryRequired, "The frozen Codex executable identity changed after profile verification.", "Preserve the title operation and refresh native installation evidence before another session.")
	}
	rawToken, err := security.RandomToken()
	if err != nil {
		return nil, err
	}
	token := apiproxy.TokenPrefix + rawToken
	digest := sha256.Sum256([]byte(token))
	intent := titleIntent{Version: 1, JobID: jobID, OperationID: input.OperationID, RegistrationID: domain.NewID(), ThreadRequestID: domain.NewID(), InputID: domain.NewID(), TurnRequestID: domain.NewID(), TokenDigest: hex.EncodeToString(digest[:])}
	intentBytes, err := json.Marshal(intent)
	if err != nil || security.WriteAtomic(filepath.Join(home, "title-intent.json"), intentBytes) != nil {
		return nil, publicationUncertain()
	}
	attempt, cancelRegistration := context.WithTimeout(ctx, 30*time.Second)
	registered, err := connection.Client.RegisterExecution(attempt, authenticated(connection.Credential, &pb.RegisterExecutionRequest{
		Mutation:  &pb.Mutation{RequestId: string(intent.RegistrationID), Id: string(jobID), ExpectedRevision: connection.Assignment.Revision},
		MachineId: string(input.MachineID), InstanceId: string(connection.Instance), CredentialDigest: digest[:],
	}))
	cancelRegistration()
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	if registered == nil || registered.Msg == nil || registered.Msg.ProxyPath != apiproxy.Prefix {
		return nil, publicationUncertain()
	}
	// Registration owns this attempt. Validate the actual title process once.
	cleanup.reconcile = func() error { return process.ReconcileOwner(processRoot, jobID) }
	nativeConfig := codex.Config{
		Mode: codex.ThreadProtocol, Version: input.NativeVersion, Home: filepath.Join(home, "codex"),
		API:     &codex.APIConfig{ServerOrigin: connection.Credential.Endpoint, Token: token, TitleProfile: true},
		Process: process.Config{Directory: processRoot, OwnerID: jobID, Executable: resolved, Cwd: workdir, Env: env, Logger: config.Logger},
	}
	proxy, err := openCodexNativeProxy(ctx, config, input.OriginalExecutionID)
	if proxy != nil {
		cleanup.closeProxy = proxy.Close
	}
	if err != nil {
		return nil, err
	}
	if proxy != nil {
		nativeConfig.API.LoopbackProxyURL = proxy.NativeURL()
		nativeConfig.Process.ProtectedValues = proxy.ProtectedValues()
	} else {
		// The title profile follows the same relocated Local runtime as the
		// conversation; the forwarder and proxy are mutually exclusive.
		forwarder, err := openCodexNativeForwarder(ctx, config, input.OriginalExecutionID, token, true)
		if forwarder != nil {
			cleanup.closeProxy = forwarder.Close
		}
		if err != nil {
			return nil, err
		}
		if forwarder != nil {
			nativeConfig.API.ForwarderOrigin = forwarder.NativeOrigin()
		}
	}
	client, err := codex.Open(ctx, nativeConfig)
	if client != nil {
		cleanup.closeNative = client.Close
	}
	if err != nil {
		return nil, err
	}
	settings := codex.ThreadSettings{
		Model: input.NativeModel, Provider: codex.APIProvider, Effort: input.Effort, Cwd: workdir,
		Instructions: domain.AutomaticTitleInstructions,
		Options:      domain.AgentOptions{Permission: domain.PermissionReadOnly, ApprovalPolicy: string(codex.ApprovalNever), ServiceTier: input.ServiceTier},
	}
	thread, err := client.StartThread(ctx, intent.ThreadRequestID, settings)
	if err != nil || thread.Thread == nil || thread.Thread.ID.Validate() != nil || thread.Effective == nil || thread.Effective.Model != input.NativeModel || thread.Effective.Provider != codex.APIProvider || !matchesTitleOption(thread.Effective.Effort, input.Effort) || !matchesTitleOption(thread.Effective.ServiceTier, input.ServiceTier) || thread.Effective.Cwd != workdir || thread.Effective.ApprovalPolicy != codex.ApprovalNever || thread.Effective.Sandbox.Type != codex.ReadOnly {
		if err != nil {
			return nil, err
		}
		return nil, publicationUncertain()
	}
	inferenceCtx, cancelInference := context.WithTimeout(ctx, 30*time.Second)
	defer cancelInference()
	turn, err := client.StartTurn(inferenceCtx, intent.TurnRequestID, intent.InputID, domain.SessionInput{Prompt: input.Prompt, Mode: domain.ExecuteMode})
	if err != nil {
		return nil, err
	}
	if turn.TurnID.Validate() != nil {
		return nil, publicationUncertain()
	}
	response, err := readTitleResponse(inferenceCtx, ctx, config, client, jobID, input, intent, thread, turn)
	if err != nil {
		return nil, err
	}
	final, usage := response.Title, response.Usage
	if err := cleanup.run(); err != nil {
		return nil, err
	}
	var usageRecord *domain.ResponseUsageRecord
	if usage != nil {
		usageRecord = &domain.ResponseUsageRecord{
			Purpose: domain.SessionTitleUsage, SessionID: input.SessionID, ProjectID: input.ProjectID,
			ExecutionID: input.OriginalExecutionID, AccountID: input.AccountID, ConnectionID: input.ConnectionID,
			ProviderID: input.ProviderID, ModelID: input.ModelID, Harness: domain.Codex, Version: input.NativeVersion,
			ThreadID: string(thread.Thread.ID), TurnID: string(turn.TurnID), Sequence: 1, Usage: *usage,
		}
	}
	result := domain.AuxiliaryTitleResult{Version: 1, OperationID: input.OperationID, NameGeneration: input.NameGeneration, Title: final, CleanupVerified: true, UsageRecord: usageRecord}
	if err := result.Validate(input); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, publicationUncertain()
	}
	if config.Logger != nil {
		config.Logger.InfoContext(ctx, "automatic title inference completed", "job_id", jobID, "operation_id", input.OperationID, "session_id", input.SessionID, "usage_observed", usageRecord != nil)
	}
	return encoded, nil
}

// titleResponse is the validated outcome of one bounded native title turn.
type titleResponse struct {
	Title string
	Usage *domain.NativeResponseUsage
}

// readTitleResponse consumes native events for exactly one title turn. It is
// shared by the API relay and managed profiles: both accept only the fixed user
// input, one bounded final assistant message and bounded reasoning, and fail
// closed on any tool, interaction or unexpected event. It never repairs,
// truncates or retries invalid output.
func readTitleResponse(inferenceCtx, ctx context.Context, config Config, client *codex.Client, jobID domain.ID, input domain.AuxiliaryTitleInput, intent titleIntent, thread codex.ThreadResult, turn codex.TurnResult) (*titleResponse, error) {
	var final string
	var finalCount int
	var titleDeltas strings.Builder
	var titleDeltaItem string
	var usage *domain.NativeResponseUsage
	var completed *codex.Turn
	reasoning := titleReasoningBudget{}
	var sawTurnStart, sawUserInput bool
	var userInputCompletions int
	for completed == nil || finalCount != 1 || !sawUserInput || !sawTurnStart {
		event, err := client.NextEvent(inferenceCtx)
		if err != nil {
			return nil, err
		}
		if config.Logger != nil {
			config.Logger.DebugContext(ctx, "automatic title native event received", "job_id", jobID, "event_kind", event.Kind)
		}
		if !event.Correlated || event.Late || event.ThreadID != thread.Thread.ID || (event.TurnID != "" && event.TurnID != turn.TurnID) {
			return nil, domain.Fail(domain.RecoveryRequired, "The native title response escaped its isolated thread and turn.", "Keep the placeholder and reconcile the private Worker runtime.")
		}
		switch event.Kind {
		case codex.ModelSafetyEvent:
			if !event.Safety.MatchesPassiveEvent(event, thread.Thread.ID, turn.TurnID) {
				return nil, publicationUncertain()
			}
		case codex.MetadataEvent:
			if event.Safety != nil {
				if !event.Safety.MatchesPassiveEvent(event, thread.Thread.ID, turn.TurnID) {
					return nil, publicationUncertain()
				}
				continue
			}
			if err := validateTitleMetadata(event.Metadata); err != nil {
				return nil, err
			}
		case codex.NoticeEvent:
			if config.Logger != nil {
				config.Logger.WarnContext(ctx, "automatic title observed a native warning", "job_id", jobID, "notice", event.Notice)
			}
			if event.Notice == domain.NativeWarning {
				continue
			}
			return nil, domain.Fail(domain.Unsupported, "Codex reported a configuration warning in the isolated title runtime.", "Keep the placeholder and resolve the private native configuration warning before retrying.")
		case codex.TurnStartedEvent:
			if event.Turn == nil || event.Turn.ID != turn.TurnID || event.Turn.Status != codex.TurnRunning {
				return nil, publicationUncertain()
			}
			sawTurnStart = true
		case codex.ThreadStatusEvent:
			if event.Status == nil || len(event.Status.ActiveFlags) != 0 || (event.Status.Type != codex.ThreadActive && event.Status.Type != codex.ThreadIdle) {
				return nil, domain.Fail(domain.Unsupported, "Codex entered an unsupported title-runtime state.", "Keep the placeholder; title inference cannot wait for tools, approval or follow-up input.")
			}
		case codex.MessageStartedEvent, codex.MessageCompletedEvent:
			if event.Message == nil || event.ItemID != event.Message.ID {
				return nil, publicationUncertain()
			}
			message := event.Message
			if config.Logger != nil {
				phase := ""
				if message.Phase != nil {
					phase = string(*message.Phase)
				}
				config.Logger.DebugContext(ctx, "automatic title native message observed", "job_id", jobID, "event_kind", event.Kind, "role", message.Role, "phase", phase, "text_bytes", len(message.Text), "has_client_input", message.ClientInputID != "")
			}
			if message.Role == codex.UserRole {
				if message.ClientInputID != intent.InputID || message.Text != input.Prompt {
					return nil, domain.Fail(domain.RecoveryRequired, "Codex did not accept the exact first session message for title inference.", "Preserve the original title operation and do not send another prompt.")
				}
				if event.Kind == codex.MessageCompletedEvent {
					userInputCompletions++
					if userInputCompletions != 1 {
						return nil, publicationUncertain()
					}
					sawUserInput = true
				}
				continue
			}
			if event.Kind == codex.MessageCompletedEvent && message.Role == codex.AssistantRole {
				if message.Phase != nil && *message.Phase != codex.FinalAnswerPhase {
					return nil, domain.Fail(domain.Unsupported, "Codex emitted a non-final assistant message in the isolated title runtime.", "Keep the placeholder; title inference accepts exactly one final answer and no commentary.")
				}
				finalCount++
				if finalCount != 1 || len(message.Text) > 4<<10 || domain.Text(message.Text, "raw automatic title output", 4<<10, true) != nil || (titleDeltaItem != "" && (titleDeltaItem != message.ID || titleDeltas.String() != message.Text)) {
					return nil, domain.Fail(domain.InvalidArgument, "The raw title response exceeded its single-response bound.", "Keep the placeholder; do not repair, truncate or retry the native response.")
				}
				final = strings.TrimSpace(message.Text)
			}
		case codex.TextDeltaEvent:
			if event.ItemID == "" || domain.Text(event.TextDelta, "raw automatic title output", 4<<10, false) != nil || titleDeltas.Len()+len(event.TextDelta) > 4<<10 || (titleDeltaItem != "" && titleDeltaItem != event.ItemID) {
				return nil, domain.Fail(domain.InvalidArgument, "The raw title response exceeded its single-response bound.", "Keep the placeholder; do not repair, truncate or retry the native response.")
			}
			titleDeltaItem = event.ItemID
			titleDeltas.WriteString(event.TextDelta)
		case codex.ResponseUsageEvent:
			if event.ResponseUsage == nil || event.ResponseUsage.Validate() != nil || usage != nil {
				return nil, publicationUncertain()
			}
			usage = event.ResponseUsage
		case codex.UsageEvent:
			if event.Usage == nil || event.Usage.Validate() != nil {
				return nil, publicationUncertain()
			}
		case codex.ArtifactStartedEvent, codex.ArtifactCompletedEvent, codex.ArtifactDeltaEvent:
			if !reasoning.observe(event) {
				return nil, domain.Fail(domain.Unsupported, "Codex emitted an unsupported or oversized title reasoning item.", "Keep reasoning private and discard it after checking the bounded final title response.")
			}
		case codex.TurnCompletedEvent:
			if event.Turn == nil || event.Turn.ID != turn.TurnID || event.Turn.Status != codex.TurnCompleted {
				return nil, domain.Fail(domain.Unavailable, "Codex did not complete the automatic title turn successfully.", "Keep the session title unchanged; no native inference will be replayed.")
			}
			completed = event.Turn
		default:
			if config.Logger != nil {
				config.Logger.WarnContext(ctx, "automatic title received an unsupported native event", "job_id", jobID, "event_kind", event.Kind)
			}
			return nil, domain.Fail(domain.Unsupported, "The automatic title runtime received a tool, interaction or unsupported native event.", "Keep the title unchanged; automatic titles never execute tools or ask for follow-up input.")
		}
	}
	if completed == nil || finalCount != 1 || !sawUserInput || !sawTurnStart || len(final) > 256 || domain.Text(final, "automatic title", 256, true) != nil || strings.ContainsAny(final, "\r\n\x00") {
		return nil, domain.Fail(domain.InvalidArgument, "The native title response is not one bounded single-line title.", "Keep the placeholder; do not repair, truncate or retry it.")
	}
	return &titleResponse{Title: final, Usage: usage}, nil
}

// resolveStartupTitleExecutable recovers the original executable identity from
// the immutable startup proof retained by the original execution. It never
// substitutes another PATH executable.
func resolveStartupTitleExecutable(ctx context.Context, config Config, input *domain.AuxiliaryTitleInput) error {
	raw, err := security.ReadPrivate(filepath.Join(config.Root, "jobs", string(input.OriginalJobID), "startup-executable.json"), 8192)
	var original domain.Installation
	if err != nil || domain.Decode(raw, &original) != nil || original.Harness != domain.Codex || original.ExecutableSHA256 != input.Startup.ExecutableSHA256 {
		return publicationUncertain()
	}
	selection := *input.Startup
	selection.ExplicitPath = original.ResolvedPath
	resolved, err := harness.ResolveExecution(ctx, selection)
	if err != nil {
		return err
	}
	input.Executable = resolved.ResolvedPath
	return nil
}
