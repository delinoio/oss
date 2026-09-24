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
		if input.Configuration.Harness != domain.Codex || domain.ID(event.NativeThreadID).Validate() != nil || (event.NativeTurnID != "" && domain.ID(event.NativeTurnID).Validate() != nil) {
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
	if event.Kind == domain.ExecutionThreadBound || event.Kind == domain.ExecutionInputAccepted || event.Kind == domain.ExecutionTurnFinished || event.Kind.IsInteraction() || event.Kind == domain.ExecutionWaitingChanged || event.Kind == domain.ExecutionQuestionDeliveryObserved || event.Kind == domain.ExecutionApprovalDeliveryObserved || event.Kind == domain.ExecutionQuestionAccepted || event.Kind == domain.ExecutionSteerObserved {
		s.logger.InfoContext(ctx, "execution_event_committed", "job_id", identity.Job, "execution_id", event.ExecutionID, "kind", event.Kind, "sequence", event.Sequence, "replayed", result.Replayed)
	}
	response := connect.NewResponse(&pb.PublishExecutionResponse{AcknowledgedSequence: receipt.Sequence, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func applyExecutionEvent(tx *store.Tx, job store.Record, input domain.ExecutionJobInput, actor domain.ID, sr store.Record, session *domain.Session, ir store.Record, queued *domain.QueuedInput, event domain.ExecutionEvent) error {
	var responseUncertain bool
	var responseErr error
	progress := session.Execution
	if progress == nil {
		if event.Kind != domain.ExecutionThreadBound || event.Sequence != 1 || queued.Delivery != domain.InputClaimed {
			return executionEventConflict()
		}
		if err := event.Observed.Validate(input.Configuration); err != nil {
			return err
		}
		if c := input.Continuation; c != nil && (c.Previous.NativeThreadID != event.NativeThreadID || !reflect.DeepEqual(c.Previous.Observed, *event.Observed)) {
			return executionEventConflict()
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
				if err := retireSteer(tx, sr, session, true); err != nil {
					return err
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
				responseUncertain, responseErr = publishExecutionInteraction(tx, input, sr, event)
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
		value = domain.ExecutionMessage{ExecutionID: input.ExecutionID, NativeThreadID: event.NativeThreadID, NativeTurnID: event.NativeTurnID, NativeID: update.NativeID, Role: update.Role, Phase: update.Phase, InputID: update.InputID, Text: update.Text, State: domain.MessageStreaming, FirstSequence: event.Sequence}
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
		if r.SessionID != session.ID || value.ExecutionID != input.ExecutionID || value.NativeThreadID != event.NativeThreadID || value.NativeTurnID != event.NativeTurnID || value.NativeID != update.NativeID || value.Role != update.Role || value.InputID != update.InputID || !phaseMatches || value.State != domain.MessageStreaming {
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
