package server

import (
	"context"
	"reflect"
	"strings"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type executionEventReceipt struct {
	Sequence uint64 `json:"sequence"`
}

func executionEventConflict() *domain.Error {
	return domain.Fail(domain.Conflict, "The execution event does not follow retained native ownership.", "Reconcile the original event sequence, input and native identities without replaying input.")
}

func (s *Service) PublishExecution(ctx context.Context, req *connect.Request[pb.PublishExecutionRequest]) (*connect.Response[pb.PublishExecutionResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	meta := req.Msg.Mutation
	if meta == nil || meta.ExpectedRevision == 0 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "An execution publication requires the exact claimed job revision.", "Use the original Worker assignment."), correlation)
	}
	var event domain.ExecutionEvent
	if err := domain.Decode(req.Msg.EventJson, &event); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if err := event.Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	identity := struct {
		Job, Machine, Instance, Device domain.ID
		Revision                       uint64
		Event                          domain.ExecutionEvent
	}{domain.ID(meta.Id), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), actor.DeviceID, meta.ExpectedRevision, event}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "execution.publish", identity, func(tx *store.Tx) (any, error) {
		if err := currentInstance(tx, identity.Machine, identity.Instance); err != nil {
			return nil, err
		}
		jobRecord, err := tx.Get(domain.JobKind, identity.Job)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](jobRecord)
		if err != nil {
			return nil, err
		}
		if jobRecord.Revision != identity.Revision || job.Type != domain.ExecuteSessionJob || job.State != domain.JobClaimed || job.InstanceID != identity.Instance || job.MachineID != identity.Machine {
			return nil, executionEventConflict()
		}
		var input domain.ExecutionJobInput
		if domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.ExecutionID != event.ExecutionID || input.SessionID != jobRecord.SessionID || input.MachineID != identity.Machine {
			return nil, executionEventConflict()
		}
		sr, session, err := sessionRecord(tx, input.SessionID)
		if err != nil {
			return nil, err
		}
		if !session.OwnsExecution(input) || session.ActiveExecutionID != input.ExecutionID {
			return nil, executionEventConflict()
		}
		if !supportsExecutionPublication(input, event.Kind) || domain.NativeIdentity(event.NativeThreadID).Validate(input.Configuration.Harness, domain.NativeThreadIdentity) != nil || (event.NativeTurnID != "" && domain.NativeIdentity(event.NativeTurnID).Validate(input.Configuration.Harness, domain.NativeTurnIdentity) != nil) {
			return nil, domain.Fail(domain.Unsupported, "This native event identity has no supported publication profile.", "Validate the installed harness adapter before publishing events.")
		}
		ir, err := tx.Get(domain.QueueKind, input.InputID)
		if err != nil {
			return nil, err
		}
		queued, err := store.Decode[domain.QueuedInput](ir)
		if err != nil {
			return nil, err
		}
		if ir.SessionID != sr.ID || queued.ExecutionID != input.ExecutionID || queued.NativeRequestID != input.TurnRequestID || queued.Prompt != input.Input.Prompt || queued.Mode != input.Input.Mode || (queued.Delivery != domain.InputClaimed && queued.Delivery != domain.InputAccepted && queued.Delivery != domain.InputUncertain) {
			return nil, executionEventConflict()
		}
		if err := validateNativeMessageOrigin(input, event); err != nil {
			return nil, err
		}
		if err := applyExecutionEvent(tx, jobRecord, input, actor.DeviceID, sr, &session, ir, &queued, event); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		return executionEventReceipt{Sequence: event.Sequence}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	// Receipt replay acknowledges a retained fact, never a right to send more
	// input. A replaced process still cannot use this RPC as a live lease.
	if err := s.Store.Read(ctx, func(tx *store.Tx) error { return currentInstance(tx, identity.Machine, identity.Instance) }); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var receipt executionEventReceipt
	if err := domain.Decode(result.Data, &receipt); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if event.Kind == domain.ExecutionThreadBound || event.Kind == domain.ExecutionInputAccepted || event.Kind == domain.ExecutionTurnFinished || event.Kind.IsInteraction() || event.Kind == domain.ExecutionWaitingChanged || event.Kind == domain.ExecutionQuestionDeliveryObserved || event.Kind == domain.ExecutionApprovalDeliveryObserved || event.Kind == domain.ExecutionQuestionAccepted || event.Kind == domain.ExecutionApprovalAccepted || event.Kind == domain.ExecutionSteerObserved || event.Kind == domain.ExecutionResponseUsageObserved || event.Kind == domain.ExecutionOpenCodeUsageObserved {
		s.logger.InfoContext(ctx, "execution_event_committed", "job_id", identity.Job, "execution_id", event.ExecutionID, "kind", event.Kind, "sequence", event.Sequence, "replayed", result.Replayed)
	}
	response := connect.NewResponse(&pb.PublishExecutionResponse{AcknowledgedSequence: receipt.Sequence, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

// Profile-specific publication stays closed as adapters are composed. Native
// identity syntax and relay registration cannot authorize unimplemented event
// families, terminal reports or another native input.
func supportsExecutionPublication(input domain.ExecutionJobInput, kind domain.ExecutionEventKind) bool {
	switch input.Configuration.Harness {
	case domain.Codex:
		return kind != domain.ExecutionOpenCodeUsageObserved && kind != domain.ExecutionClaudeMessageObserved && kind != domain.ExecutionClaudeUsageObserved
	case domain.ClaudeCode:
		return (kind == domain.ExecutionThreadBound || kind == domain.ExecutionInputAccepted || kind == domain.ExecutionMessageStarted || kind == domain.ExecutionMessageCompleted || kind == domain.ExecutionClaudeMessageObserved || kind == domain.ExecutionClaudeUsageObserved) && len(executionAPIOperations(input, domain.AnthropicMessages)) != 0
	case domain.OpenCode:
		return (kind == domain.ExecutionThreadBound || kind == domain.ExecutionInputAccepted || kind == domain.ExecutionMessageStarted || kind == domain.ExecutionTextAppended || kind == domain.ExecutionMessageCompleted || kind.IsArtifact() || kind == domain.ExecutionToolStarted || kind == domain.ExecutionToolUpdated || kind == domain.ExecutionToolCompleted || kind == domain.ExecutionOpenCodeUsageObserved || kind == domain.ExecutionProgressObserved || kind == domain.ExecutionInteractionRequested || kind == domain.ExecutionInteractionClosed || kind == domain.ExecutionQuestionDeliveryObserved || kind == domain.ExecutionApprovalDeliveryObserved || kind == domain.ExecutionQuestionAccepted || kind == domain.ExecutionApprovalAccepted || kind == domain.ExecutionTurnFinished) && len(executionAPIOperations(input, domain.OpenAIChat)) != 0
	}
	return false
}

// OpenCode transcript records represent individual native text parts. Preserve
// their parent message explicitly instead of flattening several parts into a
// fabricated message identity or using the assistant as the execution turn.
func validateNativeMessageOrigin(input domain.ExecutionJobInput, event domain.ExecutionEvent) error {
	if input.Configuration.Harness == domain.ClaudeCode && event.NativeThreadID != string(input.SessionID) {
		return executionEventConflict()
	}
	if event.ClaudeMessage != nil && (input.Configuration.Harness != domain.ClaudeCode || event.ClaudeMessage.Model != input.Configuration.NativeModel) {
		return executionEventConflict()
	}
	if input.Configuration.Harness == domain.ClaudeCode && event.Message != nil && (event.Message.Role != domain.UserMessage || event.Message.NativeID != string(input.InputID) || event.Message.InputID != input.InputID || event.Message.NativeParentID != "" || event.Message.Phase != nil) {
		return executionEventConflict()
	}
	if (event.OpenCodeStop != nil || event.Interaction != nil && event.Interaction.OpenCodeStop != nil) && input.Configuration.Harness != domain.OpenCode {
		return executionEventConflict()
	}
	if u := event.Interaction; u != nil && event.Kind == domain.ExecutionInteractionRequested {
		if (input.Configuration.Harness == domain.OpenCode) != (u.OpenCode != nil) || u.OpenCode != nil && u.OpenCode.NativeMessageID == event.NativeTurnID {
			return executionEventConflict()
		}
	}
	if event.Progress != nil && (input.Configuration.Harness == domain.OpenCode) != (event.Progress.Progress.Kind == domain.OpenCodeTodoProgressKind || event.Progress.Progress.Kind == domain.OpenCodeChangesProgressKind || event.Progress.Progress.Kind == domain.OpenCodeWorkspaceProgressKind) {
		return executionEventConflict()
	}
	if event.Progress != nil && event.Progress.Progress.Changes != nil && event.Progress.Progress.Changes.Source == domain.OpenCodeInputSummary && event.Progress.Progress.Changes.NativeMessageID != event.NativeTurnID {
		return executionEventConflict()
	}
	if event.OpenCodeUsage != nil && (input.Configuration.Harness != domain.OpenCode || event.OpenCodeUsage.NativeParentID == event.NativeTurnID) {
		return executionEventConflict()
	}
	if tool := event.Tool; tool != nil {
		if input.Configuration.Harness == domain.OpenCode {
			if tool.Snapshot == nil || !tool.Snapshot.Kind.IsOpenCode() || domain.NativeIdentity(tool.NativeID).Validate(domain.OpenCode, domain.NativePartIdentity) != nil || domain.NativeIdentity(tool.NativeParentID).Validate(domain.OpenCode, domain.NativeMessageIdentity) != nil || tool.NativeParentID == event.NativeTurnID {
				return executionEventConflict()
			}
		} else if tool.NativeParentID != "" || tool.Snapshot != nil && tool.Snapshot.Kind.IsOpenCode() {
			return executionEventConflict()
		}
	}
	if artifact := event.Artifact; artifact != nil {
		textReasoning := artifact.Snapshot != nil && artifact.Snapshot.Kind == domain.ReasoningTextArtifact || artifact.Delta != nil && artifact.Delta.Kind == domain.ReasoningTextDelta
		if input.Configuration.Harness == domain.OpenCode {
			if !(textReasoning || artifact.Snapshot != nil && artifact.Snapshot.Kind == domain.OpenCodeRevisionArtifact) || domain.NativeIdentity(artifact.NativeID).Validate(domain.OpenCode, domain.NativePartIdentity) != nil || domain.NativeIdentity(artifact.NativeParentID).Validate(domain.OpenCode, domain.NativeMessageIdentity) != nil || artifact.NativeParentID == event.NativeTurnID {
				return executionEventConflict()
			}
		} else if artifact.NativeParentID != "" || textReasoning || artifact.Snapshot != nil && artifact.Snapshot.Kind == domain.OpenCodeRevisionArtifact {
			return executionEventConflict()
		}
	}
	message := event.Message
	if message == nil {
		return nil
	}
	if input.Configuration.Harness == domain.OpenCode {
		if domain.NativeIdentity(message.NativeID).Validate(domain.OpenCode, domain.NativePartIdentity) != nil || domain.NativeIdentity(message.NativeParentID).Validate(domain.OpenCode, domain.NativeMessageIdentity) != nil || message.Phase != nil || (message.Role == domain.UserMessage && message.NativeParentID != event.NativeTurnID) || (message.Role == domain.AssistantMessage && message.NativeParentID == event.NativeTurnID) {
			return executionEventConflict()
		}
	} else if message.NativeParentID != "" {
		return executionEventConflict()
	}
	return nil
}

func applyExecutionEvent(tx *store.Tx, job store.Record, input domain.ExecutionJobInput, actor domain.ID, sr store.Record, session *domain.Session, ir store.Record, queued *domain.QueuedInput, event domain.ExecutionEvent) error {
	var responseUncertain bool
	var responseErr error
	progress := session.Execution
	if progress == nil {
		if event.Kind != domain.ExecutionThreadBound || event.Sequence != 1 || queued.Delivery != domain.InputClaimed {
			return executionEventConflict()
		}
		if err := event.Observed.ValidateForInput(input.Configuration, input.Input.Mode); err != nil {
			return err
		}
		if c := input.Continuation; c != nil {
			previous := c.Previous.Observed
			if input.Configuration.Harness == domain.OpenCode {
				// Both agents are independently validated against their own
				// immutable queued-input modes. All other native settings stay
				// identical; the predecessor observation itself is never changed.
				if err := previous.ValidateForInput(input.Configuration, c.InputMode); err != nil {
					return err
				}
				previous.OpenCodeAgent = event.Observed.OpenCodeAgent
			}
			if c.Previous.NativeThreadID != event.NativeThreadID || !reflect.DeepEqual(previous, *event.Observed) {
				return executionEventConflict()
			}
		}
		progress = &domain.ExecutionProgress{JobID: job.ID, ExecutionID: input.ExecutionID, InputID: input.InputID, NativeThreadID: event.NativeThreadID, Observed: *event.Observed, Outcome: domain.ExecutionNotStarted}
		session.Execution = progress
	} else {
		if event.Kind == domain.ExecutionThreadBound || progress.JobID != job.ID || progress.ExecutionID != input.ExecutionID || progress.InputID != input.InputID || progress.NativeThreadID != event.NativeThreadID || event.Sequence != progress.LastSequence+1 {
			return executionEventConflict()
		}
		if event.Kind == domain.ExecutionInputAccepted {
			if progress.NativeTurnID != "" || progress.Outcome != domain.ExecutionNotStarted || queued.Delivery == domain.InputAccepted {
				return executionEventConflict()
			}
			if c := input.Continuation; c != nil && c.Previous.NativeTurnID == event.NativeTurnID {
				return executionEventConflict()
			}
			completed, err := tx.NativeTurnCompleted(sr.ID, event.NativeThreadID, event.NativeTurnID)
			if err != nil {
				return err
			}
			if completed {
				return executionEventConflict()
			}
			if session.PendingInputs == 0 || session.PendingInputBytes < uint64(len(queued.Prompt)) {
				return domain.Fail(domain.RecoveryRequired, "Input queue accounting is inconsistent.", "Retain native acceptance and reconcile the queue before another send.")
			}
			progress.NativeTurnID = event.NativeTurnID
			progress.Outcome = domain.ExecutionRunning
			progress.AcceptedInputs = []domain.ExecutionInputBinding{domain.BindExecutionInput(input.InputID, input.Input.Prompt)}
			queued.Delivery = domain.InputAccepted
			session.PendingInputs--
			session.PendingInputBytes -= uint64(len(queued.Prompt))
			if _, err := tx.Put(domain.QueueKind, ir.ID, ir.Revision, sr.ID, sr.ProjectID, *queued); err != nil {
				return err
			}
			if session.Outcome == domain.ExecutionNotStarted {
				session.Outcome = domain.ExecutionRunning
			}
		} else {
			if progress.NativeTurnID == "" || progress.NativeTurnID != event.NativeTurnID || progress.Outcome != domain.ExecutionRunning || queued.Delivery != domain.InputAccepted {
				return executionEventConflict()
			}
			if event.Kind == domain.ExecutionTurnFinished {
				if event.OpenCodeStop != nil {
					if err := bindOpenCodeStop(tx, input, progress, event.OpenCodeStop); err != nil {
						return err
					}
				} else if progress.OpenCodeStop != nil {
					return executionEventConflict()
				}
				if input.Configuration.Harness == domain.OpenCode {
					complete, err := tx.HasCompletedOpenCodeInput(input.ExecutionID, input.InputID, event.NativeTurnID)
					if err != nil {
						return err
					}
					if !complete || progress.LatestUsageID == "" {
						return executionEventConflict()
					}
					record, err := tx.Get(domain.UsageKind, progress.LatestUsageID)
					if err != nil {
						return err
					}
					usage, err := store.Decode[domain.OpenCodeUsageRecord](record)
					if err != nil || usage.ExecutionID != input.ExecutionID || usage.ThreadID != event.NativeThreadID || usage.TurnID != event.NativeTurnID || usage.Usage.Source != domain.OpenCodeMessageUsage || usage.Usage.Validate() != nil {
						return executionEventConflict()
					}
					if event.OpenCodeStop != nil && event.OpenCodeStop.AssistantID != usage.Usage.NativeParentID {
						return executionEventConflict()
					}
				}
				if err := retireSteer(tx, sr, session, true); err != nil {
					return err
				}
				if input.Configuration.Harness == domain.OpenCode {
					pending, err := tx.OpenExecutionInteractions(input.ExecutionID)
					if err != nil {
						return err
					}
					if len(pending) != 0 {
						return executionEventConflict()
					}
				}
				responseUncertain, responseErr = endPublishedInteractions(tx, input, event)
				if responseErr != nil {
					return responseErr
				}
				// Request closure and a terminal turn do not prove that native
				// core accepted a transmitted answer. Retain that independent gate
				// after recording the actual native outcome below.
				responseUncertain = responseUncertain || progress.UnconfirmedResponses != 0
				progress.Waiting = domain.NativeWaiting{}
				if event.Outcome == domain.ExecutionSucceeded {
					complete, err := tx.ExecutionMessagesComplete(input.ExecutionID)
					if err != nil {
						return err
					}
					if !complete {
						return executionEventConflict()
					}
				}
				progress.Outcome = event.Outcome
				// Stop/recovery and earlier failure are product facts. Late native
				// completion can be retained but cannot promote those facts to success.
				canceled, err := tx.JobCancellationRequested(job.ID)
				if err != nil {
					return err
				}
				if session.Outcome == domain.ExecutionRunning || session.Outcome == domain.ExecutionNotStarted {
					if canceled || session.Dispatch == domain.DispatchPaused || session.Archive != domain.NotArchived {
						session.Outcome = domain.ExecutionStopped
					} else if session.Recovery != domain.NoRecovery {
						session.Outcome = domain.ExecutionFailed
					} else {
						session.Outcome = event.Outcome
					}
				}
				if event.Outcome != domain.ExecutionSucceeded {
					code := event.ProblemCode
					if code == "" {
						code = domain.Unavailable
						if event.Outcome == domain.ExecutionStopped {
							code = domain.Canceled
						}
					}
					if session.Problem == nil {
						session.Problem = domain.Fail(code, "The native execution did not complete successfully.", "Inspect the retained execution and confirm owned cleanup before explicit Resume.")
					}
					session.Dispatch = domain.DispatchPaused
				}
				// Retain native outcome independently of session Stop/recovery and
				// process cleanup. Inbox read state never changes this evidence.
				terminal := domain.InboxTerminal{JobID: job.ID, InputID: input.InputID, NativeThreadID: event.NativeThreadID, NativeTurnID: event.NativeTurnID, Sequence: event.Sequence, Outcome: event.Outcome}
				if _, err := tx.CreateInboxEntry(sr.ID, sr.ProjectID, domain.InboxEntry{Source: domain.ExecutionTerminalInbox, SourceID: input.ExecutionID, ReadState: domain.InboxUnread, Terminal: &terminal}); err != nil {
					return err
				}
			} else if event.Kind == domain.ExecutionClaudeMessageObserved {
				if err := publishClaudeMessage(tx, input, sr, event); err != nil {
					return err
				}
			} else if event.Kind == domain.ExecutionSteerObserved {
				if err := publishSteer(tx, job, input, actor, sr, session, event); err != nil {
					return err
				}
			} else if event.Kind == domain.ExecutionQuestionDeliveryObserved {
				responseUncertain, responseErr = publishQuestionDelivery(tx, job, input, actor, progress, event)
				if responseErr != nil {
					return responseErr
				}
			} else if event.Kind == domain.ExecutionApprovalDeliveryObserved {
				responseUncertain, responseErr = publishApprovalDelivery(tx, job, input, actor, progress, event)
				if responseErr != nil {
					return responseErr
				}
			} else if event.Kind == domain.ExecutionApprovalAccepted {
				if err := publishApprovalAcceptance(tx, job, input, actor, progress, event); err != nil {
					return err
				}
			} else if event.Kind == domain.ExecutionQuestionAccepted {
				if err := publishQuestionAcceptance(tx, job, input, actor, progress, event); err != nil {
					return err
				}
			} else if event.Kind.IsInteraction() {
				if event.Kind == domain.ExecutionInteractionRequested {
					if err := retireSteer(tx, sr, session, false); err != nil {
						return err
					}
				}
				responseUncertain, responseErr = publishExecutionInteraction(tx, input, sr, progress, event)
				if responseErr != nil {
					return responseErr
				}
			} else if event.Kind == domain.ExecutionWaitingChanged {
				if *event.Waiting != (domain.NativeWaiting{}) {
					if err := retireSteer(tx, sr, session, false); err != nil {
						return err
					}
				}
				progress.Waiting = *event.Waiting
			} else if event.Kind == domain.ExecutionResponseUsageObserved {
				observation := domain.ResponseUsageRecord{SessionID: sr.ID, ProjectID: sr.ProjectID, ExecutionID: input.ExecutionID, AccountID: input.AccountID, ConnectionID: input.ConnectionID, ProviderID: input.Configuration.ProviderID, ModelID: input.Configuration.ModelID, Harness: input.Configuration.Harness, Version: input.Installation.Version, ThreadID: event.NativeThreadID, TurnID: event.NativeTurnID, Sequence: event.Sequence, Usage: *event.ResponseUsage}
				id, _, err := tx.PutResponseUsage(event.ObservationID, observation)
				if err != nil {
					return err
				}
				progress.LatestResponseUsageID = id
				// Counts, native response digests and provider metadata never enter
				// logs. Publication acknowledgments retain the original event identity.
			} else if event.Kind == domain.ExecutionClaudeUsageObserved {
				if err := publishClaudeUsage(tx, input, sr, event); err != nil {
					return err
				}
				progress.LatestUsageID = event.ObservationID
			} else if event.Kind == domain.ExecutionOpenCodeUsageObserved {
				observation := domain.OpenCodeUsageRecord{ExecutionID: input.ExecutionID, AccountID: input.AccountID, ConnectionID: input.ConnectionID, ProviderID: input.Configuration.ProviderID, ModelID: input.Configuration.ModelID, Harness: input.Configuration.Harness, Version: input.Installation.Version, ThreadID: event.NativeThreadID, TurnID: event.NativeTurnID, Sequence: event.Sequence, Usage: *event.OpenCodeUsage}
				if err := tx.PutOpenCodeUsage(event.ObservationID, sr.ID, sr.ProjectID, observation); err != nil {
					return err
				}
				progress.LatestUsageID = event.ObservationID
			} else if event.Kind == domain.ExecutionUsageObserved {
				observation := domain.ExecutionUsageObservation{ExecutionID: input.ExecutionID, AccountID: input.AccountID, ConnectionID: input.ConnectionID, ProviderID: input.Configuration.ProviderID, ModelID: input.Configuration.ModelID, Harness: input.Configuration.Harness, Version: input.Installation.Version, ThreadID: event.NativeThreadID, TurnID: event.NativeTurnID, Sequence: event.Sequence, Usage: *event.Usage}
				if _, err := tx.Put(domain.UsageKind, event.ObservationID, 0, sr.ID, sr.ProjectID, observation); err != nil {
					return err
				}
				progress.LatestUsageID = event.ObservationID
			} else if event.Kind == domain.ExecutionNoticeObserved {
				progress.NoticeCount++
				progress.LastNotice = event.Notice
			} else if event.Kind.IsArtifact() {
				if err := publishExecutionArtifact(tx, input, sr, event); err != nil {
					return err
				}
			} else if event.Kind == domain.ExecutionProgressObserved {
				if err := publishExecutionProgress(tx, input, sr, progress, event); err != nil {
					return err
				}
			} else if event.Kind.IsTool() {
				if err := publishExecutionTool(tx, input, sr, event); err != nil {
					return err
				}
				if err := publishSingleUseApprovalExecution(tx, job, input, actor, progress, event); err != nil {
					return err
				}
			} else if err := publishExecutionMessage(tx, input, sr, progress, event); err != nil {
				return err
			}
		}
	}
	progress.LastSequence = event.Sequence
	if responseUncertain {
		session.Recovery, session.Dispatch = domain.NeedsRecovery, domain.DispatchPaused
		if session.Problem == nil {
			session.Problem = domain.Fail(domain.RecoveryRequired, "The native question response requires reconciliation.", "Preserve its original claim and inspect native state before another send; closure does not prove answer acceptance.")
		}
	}
	return nil
}

func publishExecutionMessage(tx *store.Tx, input domain.ExecutionJobInput, session store.Record, progress *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	update := event.Message
	if update == nil {
		return executionEventConflict()
	}
	if update.Role == domain.UserMessage {
		primary := domain.BindExecutionInput(input.InputID, input.Input.Prompt)
		bindings, err := domain.CheckedExecutionInputs(primary.InputID, primary.PromptDigest, progress.AcceptedInputs)
		if err != nil {
			return err
		}
		binding := domain.BindExecutionInput(update.InputID, update.Text)
		found := false
		for _, accepted := range bindings {
			if accepted == binding {
				found = true
				break
			}
		}
		if !found {
			return executionEventConflict()
		}
	}
	var value domain.ExecutionMessage
	var revision uint64
	if event.Kind == domain.ExecutionMessageStarted {
		value = domain.ExecutionMessage{ExecutionID: input.ExecutionID, NativeThreadID: event.NativeThreadID, NativeTurnID: event.NativeTurnID, NativeID: update.NativeID, NativeParentID: update.NativeParentID, Role: update.Role, Phase: update.Phase, InputID: update.InputID, Text: update.Text, State: domain.MessageStreaming, FirstSequence: event.Sequence}
	} else {
		r, err := tx.Get(domain.MessageKind, update.ID)
		if err != nil {
			return err
		}
		value, err = store.Decode[domain.ExecutionMessage](r)
		if err != nil {
			return err
		}
		phaseMatches := value.Phase == nil || (update.Phase != nil && *value.Phase == *update.Phase)
		if r.SessionID != session.ID || value.ExecutionID != input.ExecutionID || value.NativeThreadID != event.NativeThreadID || value.NativeTurnID != event.NativeTurnID || value.NativeID != update.NativeID || value.NativeParentID != update.NativeParentID || value.Role != update.Role || value.InputID != update.InputID || !phaseMatches || value.State != domain.MessageStreaming {
			return executionEventConflict()
		}
		revision = r.Revision
		value.Phase = update.Phase
		if event.Kind == domain.ExecutionTextAppended {
			value.Text += update.Text
		} else if event.Kind == domain.ExecutionMessageCompleted {
			if !strings.HasPrefix(update.Text, value.Text) {
				return executionEventConflict()
			}
			value.Text = update.Text
			value.State = domain.MessageComplete
		} else {
			return executionEventConflict()
		}
	}
	if err := domain.Text(value.Text, "retained native message", domain.MaxMessageText, false); err != nil {
		return err
	}
	value.LastSequence = event.Sequence
	if _, err := tx.Put(domain.MessageKind, update.ID, revision, session.ID, session.ProjectID, value); err != nil {
		return err
	}
	return tx.BindExecutionMessage(session.ID, input.ExecutionID, update.ID, event.NativeThreadID, event.NativeTurnID, update.NativeID, value.State)
}
