package server

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/updates"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

const workerLease = domain.WorkerConnectionTimeout

type workerStream struct {
	Instance domain.ID
	Done     <-chan struct{}
	ID       domain.ID
	Cancel   context.CancelFunc
}

type workerStoreWake struct {
	store   *store.Store
	changed <-chan struct{}
}

func (w *workerStoreWake) channel() <-chan struct{} {
	if w.changed == nil {
		w.changed = w.store.Changed()
	}
	return w.changed
}

func (w *workerStoreWake) consume() { w.changed = nil }

func workerActor(ctx context.Context, machine, instance string) error {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.Type != domain.WorkerDevice || string(actor.MachineID) != machine {
		return domain.Fail(domain.PermissionDenied, "This credential does not own the execution machine.", "Use the selected Worker's paired credential.")
	}
	return domain.ID(instance).Validate()
}
func currentInstance(tx *store.Tx, machine, instance domain.ID) error {
	current, _, err := tx.WorkerInstance(machine)
	if err != nil {
		return err
	}
	if current != instance {
		return domain.Fail(domain.Conflict, "The Worker process no longer owns this machine.", "Reattach and reconcile previous execution before continuing.")
	}
	return nil
}
func activeMachine(tx *store.Tx, id domain.ID) (store.Record, domain.Machine, error) {
	r, err := tx.Get(domain.MachineKind, id)
	if err != nil {
		return r, domain.Machine{}, err
	}
	m, err := store.Decode[domain.Machine](r)
	if err != nil {
		return r, m, err
	}
	if m.Disabled {
		return r, m, domain.Fail(domain.PermissionDenied, "The execution machine is disabled.", "Pair an authorized Worker before requesting work.")
	}
	return r, m, nil
}
func (s *Service) AttachWorker(ctx context.Context, req *connect.Request[pb.AttachWorkerRequest]) (*connect.Response[pb.AttachWorkerResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if req.Msg.ProtocolVersion != rpc.ProtocolVersion {
		return nil, rpc.Error(domain.Fail(domain.Unsupported, "The Worker protocol does not match this server.", "Use a current protocol-2 Worker; no legacy data or execution is adopted."), correlation)
	}

	if req.Msg.Version != rpc.Version {
		actor, _ := domain.PrincipalFrom(ctx)
		err := s.Store.Read(ctx, func(tx *store.Tx) error {
			if err := tx.Authorize(); err != nil {
				return err
			}
			if !s.signedWorkerVersion(tx, actor.MachineID, actor.DeviceID, req.Msg.Version) {
				return installationFailure(domain.Unsupported)
			}
			return nil
		})
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
	}
	machine, instance := domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId)
	capabilities := make([]domain.WorkerCapability, 0, len(req.Msg.Capabilities))
	for _, capability := range req.Msg.Capabilities {
		switch capability {
		case pb.WorkerCapability_WORKER_CAPABILITY_INLINE_MODEL_EXECUTION_V1:
			capabilities = append(capabilities, domain.InlineModelExecutionV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_BRANCH_PREFIX_INSTRUCTIONS_V1:
			capabilities = append(capabilities, domain.BranchPrefixInstructionsV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_BRANCH_PREFIX_EXACT_QUOTE_V1:
			capabilities = append(capabilities, domain.BranchPrefixExactQuoteV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_PARALLEL_SESSIONS_V1:
			capabilities = append(capabilities, domain.ParallelSessionsV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_SHARED_SUBSCRIPTION_OWNER_V1:
			capabilities = append(capabilities, domain.SharedSubscriptionOwnerV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_CLAUDE_DEVICE_OWNED_MEMBERS_V1:
			capabilities = append(capabilities, domain.ClaudeDeviceOwnedMembersV1)

		case pb.WorkerCapability_WORKER_CAPABILITY_OPENCODE_GO_SUBSCRIPTIONS_V1:
			capabilities = append(capabilities, domain.OpenCodeGoSubscriptionsV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_REPOSITORY_BRANCH_DISCOVERY_V1:
			capabilities = append(capabilities, domain.RepositoryBranchDiscoveryV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_NATIVE_SKILLS_V1:
			capabilities = append(capabilities, domain.NativeSkillsV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_NATIVE_IMAGE_GENERATION_V1:
			capabilities = append(capabilities, domain.NativeImageGenerationV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_IMAGE_INPUTS_V1:
			capabilities = append(capabilities, domain.ImageInputsV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_CLAUDE_IMAGE_INPUTS_V1:
			capabilities = append(capabilities, domain.ClaudeImageInputsV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_OPENCODE_IMAGE_INPUTS_V1:
			capabilities = append(capabilities, domain.OpenCodeImageInputsV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_SESSION_STARTUP_PROGRESS_V1:
			capabilities = append(capabilities, domain.SessionStartupProgressV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_EXECUTION_STARTUP_V1:
			capabilities = append(capabilities, domain.ExecutionStartupV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_NAMED_WORKSPACE_DIRECTORIES_V1:
			capabilities = append(capabilities, domain.NamedWorkspaceDirectoriesV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_REMOTE_WORKSPACE_CLONE_V1:
			capabilities = append(capabilities, domain.RemoteWorkspaceCloneV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_REPOSITORY_CLONE_V1:
			capabilities = append(capabilities, domain.RepositoryCloneV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_SIGNED_WORKER_UPDATES_V1:
			capabilities = append(capabilities, domain.SignedWorkerUpdatesV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_MANAGED_CODEX_FORK_V1:
			capabilities = append(capabilities, domain.ManagedCodexForkV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_MANAGED_CODEX_TITLES_V1:
			capabilities = append(capabilities, domain.ManagedCodexTitlesV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_SIDECHAT_QUESTION_RETRY_V1:
			capabilities = append(capabilities, domain.SidechatQuestionRetryV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_MANAGED_CODEX_SIDECHAT_V1:
			capabilities = append(capabilities, domain.ManagedCodexSidechatV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_CODEX_READ_ONLY_SIDECHAT_V1:
			capabilities = append(capabilities, domain.CodexReadOnlySidechatWorkerV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_OPENCODE_GENERAL_CHAT_FORK_V1:
			capabilities = append(capabilities, domain.OpenCodeGeneralChatForkV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_OPENCODE_GENERAL_CHAT_FORK_REASONING_V1:
			capabilities = append(capabilities, domain.OpenCodeGeneralChatForkReasoningV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_NATIVE_SESSION_COMPACTION_V1:
			capabilities = append(capabilities, domain.NativeSessionCompactionV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_OPENCODE_SESSION_COMPACTION_V1:
			capabilities = append(capabilities, domain.OpenCodeSessionCompactionV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_CODEX_SESSION_REVERT_V1:
			capabilities = append(capabilities, domain.CodexSessionRevertV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_CODEX_SESSION_REVERT_TARGET_PROOF_V1:
			capabilities = append(capabilities, domain.CodexSessionRevertTargetProofV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_CODEX_SESSION_COMPACTION_V1:
			capabilities = append(capabilities, domain.CodexSessionCompactionV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_OPENCODE_FOREGROUND_SUBAGENTS_V1:
			capabilities = append(capabilities, domain.OpenCodeForegroundSubagentsV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_CODEX_APPROVAL_REVIEW_V1:
			capabilities = append(capabilities, domain.CodexApprovalReviewV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_CODEX_SUBAGENT_CONFIGURATION_V1:
			capabilities = append(capabilities, domain.CodexSubagentConfigurationV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_NETWORK_BOOTSTRAP_V1:
			capabilities = append(capabilities, domain.NetworkBootstrapV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_CODEX_API_PROXY_V1:
			capabilities = append(capabilities, domain.CodexAPIProxyV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_REPOSITORY_INSPECTION_METADATA_V1:
			capabilities = append(capabilities, domain.RepositoryInspectionMetadataV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_NATIVE_CODEX_MODEL_DISCOVERY_V1:
			capabilities = append(capabilities, domain.NativeModelsV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_SESSION_TERMINALS_V1:
			capabilities = append(capabilities, domain.SessionTerminalsV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_SESSION_FORWARDING_V1:
			capabilities = append(capabilities, domain.SessionForwardingV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_CODEX_FUNCTION_OUTPUTS_V1:
			capabilities = append(capabilities, domain.CodexFunctionOutputsV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_CODEX_DYNAMIC_TOOLS_V1:
			capabilities = append(capabilities, domain.CodexDynamicToolsV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_AUTOMATIC_TITLES_CODEX_V1:
			capabilities = append(capabilities, domain.AutomaticTitlesCodexV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_CODEX_QUOTA_BLOCK_V1:
			capabilities = append(capabilities, domain.CodexQuotaBlockV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_NATIVE_CODEX_REVIEW_V1:
			capabilities = append(capabilities, domain.NativeCodexReviewV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_SUBSCRIPTION_PAID_CREDITS_V1:
			capabilities = append(capabilities, domain.SubscriptionPaidCreditsV1)

		case pb.WorkerCapability_WORKER_CAPABILITY_SUBSCRIPTION_OBSERVATIONS_V1:
			capabilities = append(capabilities, domain.SubscriptionObservationsV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_NATIVE_CLAUDE_SUBSCRIPTIONS_V1:
			capabilities = append(capabilities, domain.NativeClaudeSubscriptionsV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_MANAGED_CLAUDE_EXECUTION_V1:
			capabilities = append(capabilities, domain.ManagedClaudeExecutionV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_MANAGED_CODEX_SUBSCRIPTIONS_V1:
			capabilities = append(capabilities, domain.ManagedCodexSubscriptionsV1)
		case pb.WorkerCapability_WORKER_CAPABILITY_MANAGED_GROK_SUBSCRIPTIONS_V1:
			// Worker capability 21 is declared but stays an unknown value until the
			// managed Grok execution switch is turned on (see grok_execution_switch.go).
			if !grokManagedExecutionAdvertised {
				return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "The Worker reported an unknown native capability.", "Upgrade the Worker and report only verified capability identifiers."), correlation)
			}
			capabilities = append(capabilities, domain.ManagedGrokSubscriptionsV1)
		default:
			return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "The Worker reported an unknown native capability.", "Upgrade the Worker and report only verified capability identifiers."), correlation)
		}
	}
	if len(capabilities) != len(slices.Compact(slices.Sorted(slices.Values(capabilities)))) {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "The Worker reported a duplicate native capability.", "Report each verified capability once."), correlation)
	}
	// Profile v2 peers hold both pairs; a reasoning-only report is never a
	// valid peer state and must not be persisted.
	if !domain.OpenCodeImageInputsPaired(capabilities) || !domain.ClaudeImageInputsPaired(capabilities) {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "The Worker reported native image support without base image input support.", "Report each native image capability only together with the base image input capability."), correlation)
	}
	if !domain.OpenCodeForkReasoningPaired(capabilities) {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "The Worker reported OpenCode Fork reasoning without base OpenCode Fork support.", "Report the reasoning capability only together with the base OpenCode Fork capability."), correlation)
	}
	input := struct {
		Machine, Instance            domain.ID
		Version                      string
		Capabilities                 []domain.WorkerCapability
		NetworkGeneration            uint64
		NetworkRouteID, NetworkKeyID string
		NetworkRecipient             string
	}{machine, instance, req.Msg.Version, capabilities, req.Msg.NetworkGeneration, req.Msg.NetworkRouteId, req.Msg.NetworkKeyId, req.Msg.NetworkRecipient}
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "worker.attach", input, func(tx *store.Tx) (any, error) {
		r, m, err := activeMachine(tx, machine)
		if err != nil {
			return nil, err
		}
		if newer, _ := updates.Newer(m.Version, req.Msg.Version); req.Msg.Version != rpc.Version || newer {
			a, _ := domain.PrincipalFrom(ctx)
			if !s.signedWorkerVersion(tx, machine, a.DeviceID, req.Msg.Version) {
				return nil, installationFailure(domain.Unsupported)
			}
		}
		previous, seen, err := tx.WorkerInstance(machine)
		if err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		if previous != "" && previous != instance && now.Sub(seen) < workerLease && !s.signedWorkerTransition(tx, machine, previous, func() domain.ID { a, _ := domain.PrincipalFrom(ctx); return a.DeviceID }(), req.Msg.Version) {
			return nil, domain.Fail(domain.Conflict, "Another Worker instance still owns this machine.", "Stop that instance and wait for its connection lease to expire.")
		}
		if previous != "" && previous != instance {
			if err := loseTerminalAuthority(tx, machine); err != nil {
				return nil, err
			}
			// A missing connection is not proof that execution never began. Preserve
			// accepted ownership and require explicit reconciliation, never redispatch.
			for {
				jobs, err := tx.Jobs(machine, "", domain.JobClaimed, "", store.MaxPage)
				if err != nil {
					return nil, err
				}
				for _, record := range jobs {
					job, err := store.Decode[domain.Job](record)
					if err != nil {
						return nil, err
					}
					job.State = domain.JobUncertain
					job.Problem = domain.Fail(domain.RecoveryRequired, "The previous Worker process did not confirm completion.", "Inspect the Worker journal and reconcile this accepted job before retrying.")
					if _, err := tx.PutJob(record.ID, record.Revision, record.SessionID, record.ProjectID, job); err != nil {
						return nil, err
					}
					if err := finishLostNativeExecution(tx, record, job); err != nil {
						return nil, err
					}
					if job.Type == domain.GenerateSessionTitleJob {
						if err := finishLostSessionTitle(tx, record); err != nil {
							return nil, err
						}
						continue
					}
					if err := finishSessionWorkspace(tx, record.ID); err != nil {
						return nil, err
					}
					if err := finishRepositorySave(tx, job.ParentID); err != nil {
						return nil, err
					}
				}
				if len(jobs) < store.MaxPage {
					break
				}
			}
		}
		if err := tx.SetWorkerInstance(machine, instance, now); err != nil {
			return nil, err
		}
		if m.Network == nil || m.Network.InstanceID != instance {
			m.Network = &domain.WorkerNetworkState{InstanceID: instance, NativeState: domain.WorkerRouteUnsupported, ObservedAt: now}
		}
		if req.Msg.NetworkKeyId != "" || req.Msg.NetworkRecipient != "" {
			if domain.ValidateWorkerRecipient(domain.ID(req.Msg.NetworkKeyId), req.Msg.NetworkRecipient) != nil {
				return nil, networkConflict()
			}
		}
		// Reported configuration is observation only. Sync independently checks
		// the issuing transfer digest before admitting an effective generation.
		m.LastSeen, m.Version, m.WorkerCapabilities = now, req.Msg.Version, capabilities
		return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	// Receipts acknowledge acceptance, not an indefinite lease. A retry of an
	// older attachment must not revive a replaced process.
	if err := s.Store.Heartbeat(ctx, machine, instance); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var record store.Record
	if err := domain.Decode(result.Data, &record); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "worker attached", "machine_id", machine, "instance_id", instance, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.AttachWorkerResponse{Machine: rpc.Resource(record), ServerId: string(s.Identity.ServerID), SupportedWorkerCapabilities: []pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_CODEX_FUNCTION_OUTPUTS_V1, pb.WorkerCapability_WORKER_CAPABILITY_CODEX_DYNAMIC_TOOLS_V1, pb.WorkerCapability_WORKER_CAPABILITY_NATIVE_CODEX_REVIEW_V1, pb.WorkerCapability_WORKER_CAPABILITY_NAMED_WORKSPACE_DIRECTORIES_V1, pb.WorkerCapability_WORKER_CAPABILITY_CODEX_QUOTA_BLOCK_V1, pb.WorkerCapability_WORKER_CAPABILITY_SUBSCRIPTION_PAID_CREDITS_V1, pb.WorkerCapability_WORKER_CAPABILITY_INLINE_MODEL_EXECUTION_V1, pb.WorkerCapability_WORKER_CAPABILITY_SESSION_STARTUP_PROGRESS_V1, pb.WorkerCapability_WORKER_CAPABILITY_NATIVE_IMAGE_GENERATION_V1, pb.WorkerCapability_WORKER_CAPABILITY_CODEX_SESSION_REVERT_V1, pb.WorkerCapability_WORKER_CAPABILITY_CODEX_SESSION_REVERT_TARGET_PROOF_V1, pb.WorkerCapability_WORKER_CAPABILITY_SIDECHAT_QUESTION_RETRY_V1, pb.WorkerCapability_WORKER_CAPABILITY_MANAGED_CODEX_FORK_V1, pb.WorkerCapability_WORKER_CAPABILITY_MANAGED_CODEX_TITLES_V1, pb.WorkerCapability_WORKER_CAPABILITY_OPENCODE_GO_SUBSCRIPTIONS_V1, pb.WorkerCapability_WORKER_CAPABILITY_CODEX_APPROVAL_REVIEW_V1, pb.WorkerCapability_WORKER_CAPABILITY_BRANCH_PREFIX_INSTRUCTIONS_V1, pb.WorkerCapability_WORKER_CAPABILITY_BRANCH_PREFIX_EXACT_QUOTE_V1, pb.WorkerCapability_WORKER_CAPABILITY_REPOSITORY_BRANCH_DISCOVERY_V1, pb.WorkerCapability_WORKER_CAPABILITY_MANAGED_CODEX_SIDECHAT_V1, pb.WorkerCapability_WORKER_CAPABILITY_IMAGE_INPUTS_V1, pb.WorkerCapability_WORKER_CAPABILITY_OPENCODE_IMAGE_INPUTS_V1, pb.WorkerCapability_WORKER_CAPABILITY_CLAUDE_IMAGE_INPUTS_V1, pb.WorkerCapability_WORKER_CAPABILITY_NATIVE_SKILLS_V1, pb.WorkerCapability_WORKER_CAPABILITY_EXECUTION_STARTUP_V1, pb.WorkerCapability_WORKER_CAPABILITY_REMOTE_WORKSPACE_CLONE_V1, pb.WorkerCapability_WORKER_CAPABILITY_REPOSITORY_CLONE_V1, pb.WorkerCapability_WORKER_CAPABILITY_SIGNED_WORKER_UPDATES_V1, pb.WorkerCapability_WORKER_CAPABILITY_CODEX_READ_ONLY_SIDECHAT_V1, pb.WorkerCapability_WORKER_CAPABILITY_OPENCODE_GENERAL_CHAT_FORK_V1, pb.WorkerCapability_WORKER_CAPABILITY_OPENCODE_GENERAL_CHAT_FORK_REASONING_V1, pb.WorkerCapability_WORKER_CAPABILITY_OPENCODE_SESSION_COMPACTION_V1, pb.WorkerCapability_WORKER_CAPABILITY_NATIVE_SESSION_COMPACTION_V1, pb.WorkerCapability_WORKER_CAPABILITY_CODEX_SESSION_COMPACTION_V1, pb.WorkerCapability_WORKER_CAPABILITY_OPENCODE_FOREGROUND_SUBAGENTS_V1, pb.WorkerCapability_WORKER_CAPABILITY_CODEX_SUBAGENT_CONFIGURATION_V1, pb.WorkerCapability_WORKER_CAPABILITY_REPOSITORY_INSPECTION_METADATA_V1, pb.WorkerCapability_WORKER_CAPABILITY_NETWORK_BOOTSTRAP_V1, pb.WorkerCapability_WORKER_CAPABILITY_CODEX_API_PROXY_V1, pb.WorkerCapability_WORKER_CAPABILITY_NATIVE_CLAUDE_SUBSCRIPTIONS_V1, pb.WorkerCapability_WORKER_CAPABILITY_MANAGED_CLAUDE_EXECUTION_V1, pb.WorkerCapability_WORKER_CAPABILITY_PARALLEL_SESSIONS_V1, pb.WorkerCapability_WORKER_CAPABILITY_SHARED_SUBSCRIPTION_OWNER_V1, pb.WorkerCapability_WORKER_CAPABILITY_CLAUDE_DEVICE_OWNED_MEMBERS_V1}})
	if grokManagedExecutionAdvertised {
		response.Msg.SupportedWorkerCapabilities = append(response.Msg.SupportedWorkerCapabilities, pb.WorkerCapability_WORKER_CAPABILITY_MANAGED_GROK_SUBSCRIPTIONS_V1)
	}
	var networkStatus domain.WorkerNetworkStatus
	if err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		if err := currentInstance(tx, machine, instance); err != nil {
			return err
		}
		// Attachment receipts retain immutable acceptance. Negotiation reads the
		// current authorized observation, without repeating the accepted write.
		var err error
		record, _, err = activeMachine(tx, machine)
		if err != nil {
			return err
		}
		networkStatus, err = workerNetworkStatus(tx, machine)
		return err
	}); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response.Msg.Machine = rpc.Resource(record)
	response.Msg.NetworkStatusJson, _ = json.Marshal(networkStatus)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

// workerInFlight is the exact per-job dispatch context of one assignment sent
// on a Worker stream. Control state is never shared between jobs, so an event
// for one job cannot satisfy, cancel or exhaust the bounds of another.
type workerInFlight struct {
	session              domain.ID
	parallelLane         bool
	cancellationSent     bool
	responseControlsSent map[domain.ID]bool
	steerControlsSent    map[domain.ID]bool
}

func newWorkerInFlight(session domain.ID, parallelLane bool) *workerInFlight {
	return &workerInFlight{session: session, parallelLane: parallelLane, responseControlsSent: map[domain.ID]bool{}, steerControlsSent: map[domain.ID]bool{}}
}

// workerJobControls holds the pending controls read for one in-flight job.
type workerJobControls struct {
	id        domain.ID
	flight    *workerInFlight
	cancel    bool
	questions []*pb.QuestionResponseControl
	approvals []*pb.ApprovalResponseControl
	steer     *pb.SteerInputControl
}

// parallelSessionJob reports whether a primary job may use the negotiated
// parallel lane. Only session-scoped preparation and native execution do. Every
// other primary job keeps the serial lane and its single unresolved assignment.
func parallelSessionJob(record store.Record, job domain.Job) bool {
	return record.SessionID != "" && (job.Type == domain.PrepareWorkspaceJob || job.Type == domain.ExecuteSessionJob)
}

// workerParallelSessions reads the capability negotiated by the current attach.
// Peers that did not negotiate it keep the original singleton dispatch.
func (s *Service) workerParallelSessions(ctx context.Context, machine domain.ID) (bool, error) {
	var negotiated bool
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, current, err := activeMachine(tx, machine)
		if err != nil {
			return err
		}
		negotiated = slices.Contains(current.WorkerCapabilities, domain.ParallelSessionsV1)
		return nil
	})
	return negotiated, err
}

func (s *Service) WatchWork(ctx context.Context, req *connect.Request[pb.WatchWorkRequest], stream *connect.ServerStream[pb.WatchWorkResponse]) error {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return rpc.Error(err, correlation)
	}
	machine, instance := domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId)
	defer func() {
		if err := s.retainLostSubscriptionLeases(machine, instance, true); err != nil {
			s.logger.Warn("subscription_execution_owner_loss_unconfirmed", "code", domain.SafeError(err).Code)
		}
	}()
	if err := s.Store.Heartbeat(ctx, machine, instance); err != nil {
		return rpc.Error(err, correlation)
	}
	parallel, err := s.workerParallelSessions(ctx, machine)
	if err != nil {
		return rpc.Error(err, correlation)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	id := domain.NewID()
	s.connectionsMu.Lock()
	if s.workerStreams == nil {
		s.workerStreams = map[domain.ID]workerStream{}
	}
	if previous, ok := s.workerStreams[machine]; ok {
		previous.Cancel()
	}
	s.workerStreams[machine] = workerStream{ID: id, Cancel: cancel, Instance: instance, Done: ctx.Done()}
	s.connectionsMu.Unlock()
	defer func() {
		s.connectionsMu.Lock()
		defer s.connectionsMu.Unlock()
		if s.workerStreams[machine].ID == id {
			delete(s.workerStreams, machine)
		}
	}()
	controller, ok := ctx.Value(writeControllerKey{}).(*http.ResponseController)
	if !ok {
		return rpc.Error(domain.Fail(domain.Internal, "Streaming deadline controller is unavailable.", "Reconnect to the server."), correlation)
	}
	stream.ResponseHeader().Set(rpc.CorrelationHeader, correlation)
	send := func(message *pb.WatchWorkResponse) error {
		if err := controller.SetWriteDeadline(time.Now().Add(15 * time.Second)); err != nil {
			return err
		}
		return stream.Send(message)
	}
	if err := send(&pb.WatchWorkResponse{Heartbeat: true}); err != nil {
		return err
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	var after domain.ID
	// Keep one earliest predecessor cursor for transient dependency exclusions.
	// Later independent work may advance the ordinary cursor; a wake or completed
	// assignment restores this bounded retry boundary without preclaiming work.
	var dependencyAfter domain.ID
	var dependencyBlocked bool
	skipDependency := func(id domain.ID) {
		if !dependencyBlocked {
			dependencyAfter, dependencyBlocked = after, true
		}
		// Advance even when a whole page is blocked, so later work and the
		// heartbeat wait remain reachable. Retry from the saved predecessor.
		after = id
	}
	// blocked keeps later jobs of a skipped session behind it, so parallel
	// dispatch cannot reorder one session's accepted jobs. otherSkipped records
	// whether any skip needs a store-change retry; lane-busy skips are retried
	// only when a live assignment retires.
	blocked := map[domain.ID]bool{}
	otherSkipped := false
	retryDependencies := func() {
		if dependencyBlocked {
			after, dependencyBlocked = dependencyAfter, false
		}
		clear(blocked)
		otherSkipped = false
	}
	// Legacy peers keep one unresolved assignment. A negotiated parallel stream
	// tracks one exact context per assigned job and never preclaims more work
	// than the independent lane rules admit.
	inFlight := map[domain.ID]*workerInFlight{}
	// capabilityLogged keeps one log line per job that waits for a capability.
	capabilityLogged := map[domain.ID]bool{}
	skipSession := func(record store.Record, busy bool) {
		if record.SessionID != "" {
			blocked[record.SessionID] = true
		}
		if !busy {
			otherSkipped = true
		}
		skipDependency(record.ID)
	}
	retireJob := func(job domain.ID, reason string) {
		flight := inFlight[job]
		delete(inFlight, job)
		retryDependencies()
		if flight != nil {
			s.logger.InfoContext(ctx, "worker_job_retired", "machine_id", machine, "instance_id", instance, "job_id", job, "session_id", flight.session, "reason", reason, "in_flight", len(inFlight))
		}
	}
	serialBusy := func() bool {
		for _, flight := range inFlight {
			if !flight.parallelLane {
				return true
			}
		}
		return false
	}
	sessionBusy := func(session domain.ID) bool {
		if session == "" {
			return false
		}
		for _, flight := range inFlight {
			if flight.session == session {
				return true
			}
		}
		return false
	}
	s.logger.InfoContext(ctx, "worker_stream_opened", "machine_id", machine, "instance_id", instance, "parallel_sessions", parallel)
	defer func() {
		// Owned claimed jobs stay claimed for explicit reconciliation; they are
		// never redispatched or replayed by the closed stream.
		s.logger.InfoContext(context.WithoutCancel(ctx), "worker_stream_closed", "machine_id", machine, "instance_id", instance, "parallel_sessions", parallel, "in_flight", len(inFlight))
	}()
	wake := workerStoreWake{store: s.Store}
	for {
		changed := wake.channel()
		var records []store.Record
		var controls []workerJobControls
		err := s.Store.Read(ctx, func(tx *store.Tx) error {
			if err := currentInstance(tx, machine, instance); err != nil {
				return err
			}
			// Keep receiving heartbeats while the Worker performs this operation.
			// Legacy streams do not preclaim later jobs into a blocked native-input
			// backlog; parallel streams only claim jobs their lane rules admit.
			for _, jobID := range slices.Sorted(maps.Keys(inFlight)) {
				flight := inFlight[jobID]
				record, err := tx.Get(domain.JobKind, jobID)
				if err != nil {
					return err
				}
				job, err := store.Decode[domain.Job](record)
				if err != nil {
					return err
				}
				if job.State != domain.JobClaimed || job.InstanceID != instance {
					retireJob(jobID, "left_claimed")
					continue
				}
				pending := workerJobControls{id: jobID, flight: flight}
				requested, err := tx.JobCancellationRequested(jobID)
				if err != nil {
					return err
				}
				if requested && !flight.cancellationSent {
					pending.cancel = true
				}
				if !requested {
					pending.questions, err = s.pendingQuestionResponses(tx, record, job, flight.responseControlsSent)
					if err == nil {
						pending.approvals, err = s.pendingApprovalResponses(tx, record, job, flight.responseControlsSent)
					}
					if err == nil {
						pending.steer, err = s.pendingSteer(tx, record, job, flight.steerControlsSent)
					}
					if err != nil {
						return err
					}
				}
				controls = append(controls, pending)
			}
			if len(inFlight) != 0 && !parallel {
				return nil
			}
			if err := tx.WorkerUpdateAdmission(machine); err != nil {
				return nil
			}
			if err := workerNetworkReady(tx, machine); err != nil {
				if domain.SafeError(err).Code == domain.RecoveryRequired {
					return nil
				}
				return err
			}
			var err error
			records, err = tx.Jobs(machine, "", "", after, store.MaxPage)
			return err
		})
		if err != nil {
			return rpc.Error(err, correlation)
		}
		for _, pending := range controls {
			flight := pending.flight
			if pending.cancel {
				if err := send(&pb.WatchWorkResponse{CancelJobId: string(pending.id)}); err != nil {
					return err
				}
				flight.cancellationSent = true
			}
			for _, control := range pending.questions {
				if len(flight.responseControlsSent) >= domain.MaxExecutionInteractions {
					return rpc.Error(domain.Fail(domain.ResourceExhausted, "The execution response control bound is reached.", "Reconcile its original interactions without replaying native answers."), correlation)
				}
				if err := send(&pb.WatchWorkResponse{QuestionResponse: control}); err != nil {
					return err
				}
				flight.responseControlsSent[domain.ID(control.ResponseId)] = true
			}
			for _, control := range pending.approvals {
				if len(flight.responseControlsSent) >= domain.MaxExecutionInteractions {
					return rpc.Error(domain.Fail(domain.ResourceExhausted, "The execution response control bound is reached.", "Reconcile its original interactions without replaying native answers."), correlation)
				}
				if err := send(&pb.WatchWorkResponse{ApprovalResponse: control}); err != nil {
					return err
				}
				flight.responseControlsSent[domain.ID(control.ResponseId)] = true
			}
			if pending.steer != nil {
				if len(flight.steerControlsSent) >= domain.MaxExecutionSteers {
					return rpc.Error(steerConflict(), correlation)
				}
				if err := send(&pb.WatchWorkResponse{SteerInput: pending.steer}); err != nil {
					return err
				}
				flight.steerControlsSent[domain.ID(pending.steer.SteerId)] = true
			}
		}
		assigned := false
		waitForNetwork := false
		for _, record := range records {
			job, err := store.Decode[domain.Job](record)
			if err != nil {
				return rpc.Error(err, correlation)
			}
			// Automatic titles belong exclusively to the auxiliary admission lane,
			// including already claimed jobs. Primary dispatch cannot acquire their
			// authority or compete with the original auxiliary owner.
			if job.Type == domain.GenerateSessionTitleJob {
				after = record.ID
				continue
			}
			if _, live := inFlight[record.ID]; live {
				// A dependency rescan can revisit a job this stream already sent.
				after = record.ID
				continue
			}
			if parallel && job.State == domain.JobQueued {
				// Preserve per-session order and exclusion: a session with a skipped
				// or in-flight job runs nothing else, and serial-lane jobs wait for
				// the one serial slot without stalling unrelated sessions.
				lane := parallelSessionJob(record, job)
				if record.SessionID != "" && (blocked[record.SessionID] || sessionBusy(record.SessionID)) || !lane && serialBusy() {
					skipSession(record, true)
					continue
				}
			}
			if job.State == domain.JobQueued && job.Type == domain.WorkspaceStorageJob {
				var storageInput workspace.StorageRequest
				if workspace.DecodeStorageRequest(job.Input, &storageInput) != nil {
					return rpc.Error(workspace.ResultUncertain(), correlation)
				}
				pending := false
				if err := s.Store.Read(ctx, func(tx *store.Tx) error {
					ids, err := tx.SidechatDependents(record.SessionID)
					pending = len(ids) != 0
					return err
				}); err != nil {
					return rpc.Error(err, correlation)
				}
				if pending && (storageInput.Action == workspace.StorageCleanup || storageInput.Action == workspace.StorageRecover) {
					skipSession(record, false)
					continue
				}
			}
			if job.State == domain.JobQueued {
				claimDependencyBlocked := false
				claimCapabilityBlocked := false
				var releasedRetry domain.ID
				result, err := s.Store.Mutate(ctx, domain.NewID(), "worker.claim", struct{ Job, Instance domain.ID }{record.ID, instance}, func(tx *store.Tx) (any, error) {
					if err := currentInstance(tx, machine, instance); err != nil {
						return nil, err
					}
					if err := workerNetworkAdmission(tx, machine); err != nil {
						return nil, err
					}
					r, err := tx.Get(domain.JobKind, record.ID)
					if err != nil {
						return nil, err
					}
					j, err := store.Decode[domain.Job](r)
					if err != nil {
						return nil, err
					}
					if j.State != domain.JobQueued || j.Type == domain.GenerateSessionTitleJob {
						return r, nil
					}
					if j.Type == domain.DiscoverRepositoryBranchesJob {
						var input domain.RepositoryBranchesInput
						if err := domain.Decode(j.Input, &input); err != nil {
							return nil, err
						}
						if err := repositoryBranchAuthority(tx, input); err != nil {
							now := time.Now().UTC()
							j.State, j.Problem, j.FinishedAt = domain.JobFailed, domain.SafeError(err), &now
							return tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, j)
						}
					}
					if mr, err := tx.Get(domain.MachineKind, machine); err != nil {
						return nil, err
					} else {
						current, err := store.Decode[domain.Machine](mr)
						if err != nil {
							return nil, err
						}
						err = requireNamedWorkspaceJob(current, j)
						if err == nil {
							err = requireQueuedRevertCapabilities(current, j)
						}
						if err != nil {
							if j.Type != domain.PrepareWorkspaceJob && j.Type != domain.WorkspaceStorageJob && j.Type != domain.RecoverWorkspaceJob && j.Type != domain.RecoverExecutionJob && j.Type != domain.ExecuteSessionJob && j.Type != domain.CompactSessionJob {
								return nil, err
							}
							return rejectDowngradedNamedWorkspaceJob(tx, r, j, err)
						}
						// A Worker that predates the OpenCode image pair rejects the
						// frozen declaration, so the job waits for one that reports it.
						if queuedOpenCodeImageWorkerMissing(current, j) {
							claimCapabilityBlocked = true
							return r, nil
						}
					}
					if j.Type == domain.PrepareWorkspaceJob {
						var input workspace.PrepareRequest
						if domain.Decode(j.Input, &input) != nil {
							return nil, workspace.ResultUncertain()
						}
						mr, err := tx.Get(domain.MachineKind, machine)
						if err != nil {
							return nil, err
						}
						current, err := store.Decode[domain.Machine](mr)
						if err != nil {
							return nil, err
						}
						for _, repository := range input.Repositories {
							if (repository.SourceKind == workspace.RemoteCloneSource || repository.SourceKind == workspace.IndependentForkSource) && !slices.Contains(current.WorkerCapabilities, domain.RemoteWorkspaceCloneV1) {
								now := time.Now().UTC()
								j.State, j.Problem, j.FinishedAt = domain.JobFailed, domain.Fail(domain.Unsupported, "The selected Runner Device no longer supports remote workspace cloning.", "Update and reconnect the Worker before starting another session."), &now
								if _, err := tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, j); err != nil {
									return nil, err
								}
								if err := finishSessionWorkspace(tx, r.ID); err != nil {
									return nil, err
								}
								return tx.Get(domain.JobKind, r.ID)
							}
						}
					}
					if j.Type == domain.CloneRepositoryJob {
						var input domain.RepositoryCloneInput
						problem := domain.Decode(j.Input, &input)
						actor, _ := domain.PrincipalFrom(ctx)
						if problem == nil {
							problem = cloneAuthority(tx, input)
						}
						if problem == nil && input.LocalOrigin.DeviceID != actor.DeviceID {
							problem = localOriginRequired()
						}
						if problem != nil {
							now := time.Now().UTC()
							j.State, j.Problem, j.FinishedAt = domain.JobFailed, domain.SafeError(problem), &now
							return tx.PutJob(r.ID, r.Revision, "", "", j)
						}
					}
					if j.Type == domain.NativeModelsJob {
						var scope domain.NativeModelScope
						problem := domain.Decode(j.Input, &scope)
						if problem == nil {
							problem = nativeModelAuthority(tx, scope)
						}
						if problem != nil {
							now := time.Now().UTC()
							j.State, j.Problem, j.FinishedAt = domain.JobFailed, domain.SafeError(problem), &now
							return tx.PutJob(r.ID, r.Revision, "", "", j)
						}
					}
					if j.Type == domain.WorkspaceStorageJob {
						var storageInput workspace.StorageRequest
						if workspace.DecodeStorageRequest(j.Input, &storageInput) != nil {
							return nil, workspace.ResultUncertain()
						}
						if storageInput.Action == workspace.StorageCleanup || storageInput.Action == workspace.StorageRecover {
							if err := tx.RequireNoSidechatDependents(r.SessionID); err != nil {
								claimDependencyBlocked = true
								return r, nil
							}
						}
					}
					if j.Type == domain.ForkSessionJob {
						var input domain.ForkJobInput
						problem := domain.Decode(j.Input, &input)
						if problem == nil {
							problem = validateForkAuthority(tx, input, forkAuthorityClaim)
						}
						if problem != nil {
							failed, generation, err := failForkBeforeClaim(tx, r, j, problem)
							releasedRetry = generation
							return failed, err
						}
					}
					actor, _ := domain.PrincipalFrom(ctx)
					j.State, j.InstanceID, j.AssignedDeviceID = domain.JobClaimed, instance, actor.DeviceID
					return tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, j)
				})
				if err != nil {
					if domain.SafeError(err).Code == domain.ResourceExhausted {
						waitForNetwork = true
						break
					}
					return rpc.Error(err, correlation)
				}
				claimID := record.ID
				record, job, err = decodeWorkerClaim(result.Data)
				if err != nil {
					s.logger.WarnContext(ctx, "worker claim receipt rejected", "machine_id", machine, "job_id", claimID, "code", domain.SafeError(err).Code)
					return rpc.Error(err, correlation)
				}
				if releasedRetry != "" {
					s.logger.InfoContext(ctx, "sidechat_retry_preclaim_rejected", "job_id", record.ID, "generation_id", releasedRetry, "code", job.Problem.Code)
				}
				if claimDependencyBlocked {
					skipSession(record, false)
					continue
				}
				if claimCapabilityBlocked {
					if !capabilityLogged[record.ID] {
						capabilityLogged[record.ID] = true
						s.logger.InfoContext(ctx, "worker_claim_waiting_for_capability", "machine_id", machine, "instance_id", instance, "job_id", record.ID, "session_id", record.SessionID, "type", job.Type, "capability", "opencode_image_inputs_v1")
					}
					skipSession(record, false)
					continue
				}
			}
			if job.Type != domain.GenerateSessionTitleJob && job.State == domain.JobClaimed && job.InstanceID == instance {
				var cancelRequested bool
				if err := s.Store.Read(ctx, func(tx *store.Tx) error {
					var err error
					cancelRequested, err = tx.JobCancellationRequested(record.ID)
					return err
				}); err != nil {
					return rpc.Error(err, correlation)
				}
				lane := parallel && parallelSessionJob(record, job)
				if parallel && !lane && serialBusy() || parallel && sessionBusy(record.SessionID) {
					// A claimed job is re-sent only to a stream that has no conflicting
					// live assignment for its serial slot or session.
					skipSession(record, true)
					continue
				}
				if err := send(&pb.WatchWorkResponse{Job: rpc.WorkerAssignment(record), CancelRequested: cancelRequested}); err != nil {
					return err
				}
				flight := newWorkerInFlight(record.SessionID, lane)
				flight.cancellationSent = cancelRequested
				inFlight[record.ID] = flight
				s.logger.InfoContext(ctx, "worker_job_dispatched", "machine_id", machine, "instance_id", instance, "job_id", record.ID, "session_id", record.SessionID, "type", job.Type, "parallel_lane", lane, "in_flight", len(inFlight))
				assigned = true
				after = record.ID
				if !parallel {
					break
				}
				continue
			}
			after = record.ID
		}
		if assigned {
			// Legacy streams keep the assigned job as the sole in-flight operation.
			// A later terminal report restores the dependency cursor before another
			// scan. Parallel streams rescan immediately for further admitted work.
			wake.consume()
			continue
		}
		if len(records) == store.MaxPage && !waitForNetwork {
			// Keep the scan's original wake channel across pages. Replacing it here
			// could lose a dependency-retirement signal delivered after an earlier
			// blocked job was checked.
			continue
		}
		select {
		case <-ctx.Done():
			return rpc.Error(ctx.Err(), correlation)
		case <-changed:
			wake.consume()
			if !parallel || otherSkipped {
				retryDependencies()
			}
		case <-ticker.C:
			wake.consume()
			retryDependencies()
			if err := s.Store.Heartbeat(ctx, machine, instance); err != nil {
				return rpc.Error(err, correlation)
			}
			if err := send(&pb.WatchWorkResponse{Heartbeat: true}); err != nil {
				return err
			}
		}
	}
}
func (s *Service) InspectRepository(ctx context.Context, req *connect.Request[pb.InspectRepositoryRequest]) (*connect.Response[pb.InspectRepositoryResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	input := domain.RepositoryInspectionInput{Path: req.Msg.Path, PreferredRemote: req.Msg.PreferredRemote}
	if err := domain.Text(input.Path, "checkout path", 4096, true); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if err := domain.Text(input.PreferredRemote, "preferred remote", 256, false); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	machine := domain.ID(req.Msg.MachineId)
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "repository.inspect", struct {
		Machine domain.ID
		Input   domain.RepositoryInspectionInput
	}{machine, input}, func(tx *store.Tx) (any, error) {
		if _, _, err := activeMachine(tx, machine); err != nil {
			return nil, err
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		return tx.PutJob(domain.NewID(), 0, "", "", domain.Job{Type: domain.InspectRepositoryJob, State: domain.JobQueued, MachineID: machine, Input: raw, AcceptedAt: time.Now().UTC()})
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var record store.Record
	if err := domain.Decode(result.Data, &record); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(&pb.InspectRepositoryResponse{Job: rpc.Resource(record), RequestId: req.Msg.RequestId, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func workerProblem(detail *pb.ErrorDetail) *domain.Error {
	// Remote diagnostics are an untrusted data boundary. Persist only a closed
	// classification and our own messages; never forward paths or native stderr.
	code := domain.Code(detail.Code)
	switch code {
	case domain.InvalidArgument, domain.NotFound, domain.Conflict, domain.PermissionDenied, domain.Unavailable, domain.MissingInput, domain.Unsupported, domain.RecoveryRequired, domain.ResourceExhausted, domain.Canceled:
	default:
		code = domain.Internal
	}
	return domain.Fail(code, "The Worker could not complete the accepted operation.", "Inspect the selected Worker and the operation's typed diagnostic before retrying.")
}
func (s *Service) ReportWork(ctx context.Context, req *connect.Request[pb.ReportWorkRequest]) (*connect.Response[pb.ReportWorkResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	meta := req.Msg.Mutation
	if meta == nil || meta.ExpectedRevision == 0 {
		return nil, rpc.Error(domain.Fail(domain.MissingInput, "The claimed job and revision are required.", "Report the exact accepted job."), correlation)
	}
	machine, instance := domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId)
	actor, _ := domain.PrincipalFrom(ctx)
	reportOperation := "worker.report"
	// Check immutable assigned-device ownership before receipt lookup as well
	// as before mutation. Historical jobs retain their existing machine scope.
	if err := s.Store.Read(ctx, func(tx *store.Tx) error {
		r, err := tx.Get(domain.JobKind, domain.ID(meta.Id))
		if err != nil {
			return err
		}
		j, err := store.Decode[domain.Job](r)
		if err != nil {
			return err
		}
		if j.Type == domain.DiscoverRepositoryBranchesJob {
			reportOperation = "worker.branches.report"
		}
		if j.AssignedDeviceID != "" && j.AssignedDeviceID != actor.DeviceID {
			return domain.Fail(domain.PermissionDenied, "The assigned Worker device does not own this report.", "Use the original paired Worker device.")
		}
		return nil
	}); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var problem *domain.Error
	if req.Msg.Problem != nil {
		problem = workerProblem(req.Msg.Problem)
	}
	if problem != nil && len(req.Msg.OutputJson) > 0 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "A result cannot contain both failure and output.", "Report one typed outcome."), correlation)
	}
	input := struct {
		ID                domain.ID
		Revision          uint64
		Machine, Instance domain.ID
		Output            json.RawMessage
		Problem           *domain.Error
	}{domain.ID(meta.Id), meta.ExpectedRevision, machine, instance, req.Msg.OutputJson, problem}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), reportOperation, input, func(tx *store.Tx) (any, error) {
		record, err := tx.Get(domain.JobKind, domain.ID(meta.Id))
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](record)
		if err != nil {
			return nil, err
		}
		if job.MachineID != machine || job.InstanceID != instance || job.AssignedDeviceID != "" && job.AssignedDeviceID != actor.DeviceID {
			return nil, domain.Fail(domain.PermissionDenied, "The Worker does not own this job.", "Report only work assigned to this machine and process.")
		}
		if job.StorageReconciledBy != "" {
			if err := validateReconciledStorageReport(tx, record, job, actor, machine, instance, meta.ExpectedRevision); err != nil {
				return nil, err
			}
			return record, nil
		}
		if err := currentInstance(tx, machine, instance); err != nil {
			return nil, err
		}
		if job.State != domain.JobClaimed {
			return nil, domain.Fail(domain.Conflict, "The job is no longer awaiting this result.", "Inspect its current accepted outcome.")
		}
		if job.Type == domain.CloneRepositoryJob {
			return finishRepositoryClone(tx, record, job, meta.ExpectedRevision, req.Msg.OutputJson, problem)
		}
		if job.Type == domain.NativeCodeReviewJob {
			return finishNativeCodeReview(tx, record, job, meta.ExpectedRevision, req.Msg.OutputJson, problem)
		}
		if job.Type == domain.CompactSessionJob {
			return finishSessionCompaction(tx, record, job, meta.ExpectedRevision, req.Msg.OutputJson, problem)
		}
		if job.Type == domain.ExecuteSessionJob {
			if problem == nil {
				if saved, handled, err := s.finishPRStartupRejection(tx, actor, record, job, meta.ExpectedRevision, req.Msg.OutputJson); handled {
					return saved, err
				}
			}
			return finishNativeExecution(tx, record, job, meta.ExpectedRevision, req.Msg.OutputJson, problem)
		}
		if job.Type == domain.GenerateSessionTitleJob {
			return finishSessionTitle(tx, record, job, meta.ExpectedRevision, req.Msg.OutputJson, problem)
		}
		if job.Type == domain.ForkSessionJob {
			return finishSessionFork(tx, record, job, meta.ExpectedRevision, req.Msg.OutputJson, problem)
		}
		if job.Type == domain.NativeModelsJob {
			if record.Revision != meta.ExpectedRevision {
				return nil, domain.Fail(domain.Conflict, "The observation assignment revision changed.", "Report only the original claimed assignment.")
			}
			return finishNativeModels(tx, record, job, req.Msg.OutputJson, problem)
		}
		if problem == nil {
			outputJSON := req.Msg.OutputJson
			switch job.Type {
			case domain.WorkspaceStorageJob:
				var expected workspace.StorageRequest
				if workspace.DecodeStorageRequest(job.Input, &expected) != nil || validateWorkspaceStorageResult(expected, req.Msg.OutputJson) != nil {
					problem = workspace.ResultUncertain()
				}
			case domain.RecoverExecutionJob:
				if validateExecutionRecoveryResult(tx, record, job, req.Msg.OutputJson) != nil {
					problem = domain.ExecutionRecoveryUncertain()
				}
			case domain.RecoverWorkspaceJob:
				var expected workspace.RecoveryRequest
				if err := domain.Decode(job.Input, &expected); err != nil {
					return nil, err
				}
				machineRecord, err := tx.Get(domain.MachineKind, machine)
				if err != nil {
					return nil, err
				}
				machineValue, err := store.Decode[domain.Machine](machineRecord)
				if err != nil {
					return nil, err
				}
				var output workspace.RecoveryResult
				_, session, sessionErr := sessionRecord(tx, record.SessionID)
				if sessionErr != nil || validateRecoveryPreparation(tx, record.SessionID, session, expected.Preparation) != nil || domain.Decode(req.Msg.OutputJson, &output) != nil || workspace.ValidateRecoveryResult(expected, output, machineValue.OS) != nil {
					problem = workspace.ResultUncertain()
				}
			case domain.PrepareWorkspaceJob:
				var expected workspace.PrepareRequest
				if err := domain.Decode(job.Input, &expected); err != nil {
					return nil, err
				}
				machineRecord, err := tx.Get(domain.MachineKind, machine)
				if err != nil {
					return nil, err
				}
				machineValue, err := store.Decode[domain.Machine](machineRecord)
				if err != nil {
					return nil, err
				}
				var manifest workspace.Manifest
				if err := domain.Decode(req.Msg.OutputJson, &manifest); err != nil {
					problem = workspace.ResultUncertain()
				} else if err := workspace.ValidateResult(expected, manifest, machineValue.OS); err != nil {
					problem = workspace.ResultUncertain()
				}
			case domain.HarnessDiscoveryJob:
				outputJSON, problem, err = finishDiscovery(tx, job, req.Msg.OutputJson)
				if err != nil {
					return nil, err
				}
			case domain.DiscoverRepositoryBranchesJob:
				if err := finishRepositoryBranches(tx, job, outputJSON); err != nil {
					problem = domain.SafeError(err)
				}
			case domain.InspectRepositoryJob:
				var output workspace.Inspection
				if err := domain.Decode(req.Msg.OutputJson, &output); err != nil {
					return nil, err
				}
				if err := domain.Text(output.Root, "canonical checkout", 4096, true); err != nil {
					return nil, err
				}
				if err := domain.Text(output.Name, "repository name", 256, true); err != nil {
					return nil, err
				}
				if len(output.Remotes) > 128 || len(output.DefaultRefs) > 128 {
					return nil, domain.Fail(domain.InvalidArgument, "Repository metadata exceeds its bound.", "Reduce the remote metadata.")
				}
				for _, remote := range output.Remotes {
					if err := domain.Text(remote, "remote name", 256, true); err != nil {
						return nil, err
					}
				}
				for remote, ref := range output.DefaultRefs {
					if !slices.Contains(output.Remotes, remote) {
						return nil, domain.Fail(domain.InvalidArgument, "Default reference has no corresponding remote.", "Reinspect the repository.")
					}
					if err := domain.Text(ref, "default reference", 4096, true); err != nil {
						return nil, err
					}
				}
				machineRecord, err := tx.Get(domain.MachineKind, machine)
				if err != nil {
					return nil, err
				}
				machineValue, err := store.Decode[domain.Machine](machineRecord)
				if err != nil {
					return nil, err
				}
				// Presence, including a null field, requires negotiation. A null value
				// cannot masquerade as legacy omission or a validated metadata map.
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(outputJSON, &fields); err != nil {
					return nil, domain.Fail(domain.InvalidArgument, "Repository inspection is unreadable.", "Reinspect the repository.")
				}
				_, enriched := fields["github_repositories"]
				if enriched && (output.GitHubRepositories == nil || !slices.Contains(machineValue.WorkerCapabilities, domain.RepositoryInspectionMetadataV1)) {
					return nil, domain.Fail(domain.InvalidArgument, "Repository metadata was not negotiated.", "Reattach a compatible Worker before reporting enrichment.")
				}
				if err := output.ValidateGitHubRepositories(); err != nil {
					return nil, err
				}
				var expected domain.RepositoryInspectionInput
				if err := domain.Decode(job.Input, &expected); err != nil {
					return nil, err
				}
				remotes := append([]string{}, expected.RequiredRemotes...)
				if expected.PreferredRemote != "" {
					remotes = append(remotes, expected.PreferredRemote)
				}
				for _, remote := range remotes {
					if !slices.Contains(output.Remotes, remote) {
						problem = domain.Fail(domain.InvalidArgument, "The selected remote no longer exists on the Worker.", "Refresh repository inspection and choose an existing remote.")
						break
					}
				}
			default:
				return nil, domain.Fail(domain.Unsupported, "This job has no supported completion protocol.", "Use a compatible Worker and server.")
			}
			job.Output = outputJSON
		}
		now := time.Now().UTC()
		job.FinishedAt = &now
		if problem == nil {
			job.State = domain.JobSucceeded
		} else {
			job.Output = nil
			job.State, job.Problem = domain.JobFailed, problem
			if problem.Code == domain.Canceled {
				job.State = domain.JobCanceled
			}
			if problem.Code == domain.RecoveryRequired {
				job.State = domain.JobUncertain
			}
		}
		saved, err := tx.PutJob(record.ID, meta.ExpectedRevision, record.SessionID, record.ProjectID, job)
		if err != nil {
			return nil, err
		}
		if err := finishSessionWorkspace(tx, record.ID); err != nil {
			return nil, err
		}
		if err := finishRepositorySave(tx, job.ParentID); err != nil {
			return nil, err
		}
		if job.Type == domain.RecoverExecutionJob {
			return store.Record{ID: saved.ID}, nil
		}
		return saved, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var record store.Record
	if err := domain.DecodeWithLimit(result.Data, &record, reportResultLimit(reportOperation)); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if record.Kind == "" {
		err = s.Store.Read(ctx, func(tx *store.Tx) error {
			if err := currentInstance(tx, machine, instance); err != nil {
				return err
			}
			var err error
			record, err = tx.Get(domain.JobKind, record.ID)
			if err != nil {
				return err
			}
			job, err := store.Decode[domain.Job](record)
			if err != nil {
				return err
			}
			if job.MachineID != machine || job.InstanceID != instance || (job.Type != domain.NativeCodeReviewJob && job.Type != domain.ExecuteSessionJob && job.Type != domain.RecoverExecutionJob && job.Type != domain.CompactSessionJob) {
				return executionEventConflict()
			}
			return nil
		})
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
	}
	s.logger.InfoContext(ctx, "worker job reported", "machine_id", machine, "job_id", meta.Id, "replayed", result.Replayed)
	if job, err := store.Decode[domain.Job](record); err == nil && job.Type == domain.NativeModelsJob {
		code := domain.Code("")
		if job.Problem != nil {
			code = job.Problem.Code
		}
		s.logger.InfoContext(ctx, "native model observation published", "request_id", meta.RequestId, "job_id", meta.Id, "machine_id", machine, "account_id", job.ParentID, "phase", "publish", "state", job.State, "code", code, "replayed", result.Replayed)
	}
	response := connect.NewResponse(&pb.ReportWorkResponse{Job: rpc.Resource(record), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func revokeMachineJobs(tx *store.Tx, machine domain.ID) error {
	for _, state := range []domain.JobState{domain.JobQueued, domain.JobClaimed} {
		for {
			records, err := tx.Jobs(machine, "", state, "", store.MaxPage)
			if err != nil {
				return err
			}
			for _, record := range records {
				job, err := store.Decode[domain.Job](record)
				if err != nil {
					return err
				}
				if state == domain.JobQueued {
					now := time.Now().UTC()
					job.State, job.FinishedAt = domain.JobCanceled, &now
					job.Problem = domain.Fail(domain.PermissionDenied, "The Worker was revoked before dispatch.", "Pair an authorized Worker and submit new work explicitly.")
				} else {
					// Revocation cannot prove that an already accepted native operation had
					// no side effects, even when the connection is canceled immediately.
					job.State, job.FinishedAt = domain.JobUncertain, nil
					job.Problem = domain.Fail(domain.RecoveryRequired, "The Worker was revoked before completion was acknowledged.", "Reconcile its private execution journal before retrying.")
				}
				if _, err := tx.PutJob(record.ID, record.Revision, record.SessionID, record.ProjectID, job); err != nil {
					return err
				}
				if err := finishLostNativeExecution(tx, record, job); err != nil {
					return err
				}
				if job.Type == domain.GenerateSessionTitleJob {
					if err := finishLostSessionTitle(tx, record); err != nil {
						return err
					}
					continue
				}
				if err := finishSessionWorkspace(tx, record.ID); err != nil {
					return err
				}
				if err := finishRepositorySave(tx, job.ParentID); err != nil {
					return err
				}
			}
			if len(records) < store.MaxPage {
				break
			}
		}
	}
	return nil
}

// Retire a queued assignment that the reconnected Worker can no longer honor.
// The failure and its session reconciliation commit before WatchWork continues.
func rejectDowngradedNamedWorkspaceJob(tx *store.Tx, record store.Record, job domain.Job, problem error) (store.Record, error) {
	now := time.Now().UTC()
	job.State, job.Problem, job.FinishedAt = domain.JobFailed, domain.SafeError(problem), &now
	saved, err := tx.PutJob(record.ID, record.Revision, record.SessionID, record.ProjectID, job)
	if err != nil {
		return store.Record{}, err
	}
	switch job.Type {
	case domain.ExecuteSessionJob:
		err = finishLostNativeExecution(tx, saved, job)
	case domain.CompactSessionJob:
		err = finishUnclaimedNamedCompaction(tx, saved, job)
	default:
		err = finishSessionWorkspace(tx, record.ID)
	}
	if err != nil {
		return store.Record{}, err
	}
	return tx.Get(domain.JobKind, record.ID)
}
