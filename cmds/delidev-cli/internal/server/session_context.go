// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Exact live utilization is unavailable. Native snapshots and compaction
// boundaries retain separate timestamped observations and never imply billing.
type sessionContextView struct {
	SessionRevision   string                      `json:"session_revision"`
	SessionID         domain.ID                   `json:"session_id"`
	ExecutionID       domain.ID                   `json:"execution_id,omitempty"`
	CurrentTokens     *domain.ClaudeProgressCount `json:"current_tokens"`
	NativeContext     *sessionNativeContext       `json:"native_context,omitempty"`
	AutomaticBoundary *sessionContextResource     `json:"automatic_boundary"`
	AutomaticSummary  *sessionContextResource     `json:"automatic_summary"`
	ManualAction      *sessionContextResource     `json:"manual_action"`
}

// JSON documents remain readable JSON, rather than nested protobuf bytes.
// The action projection deliberately excludes its native paths and old prompt.
type sessionContextResource struct {
	ID         domain.ID       `json:"id"`
	Revision   string          `json:"revision"`
	ObservedAt time.Time       `json:"observed_at"`
	Document   json.RawMessage `json:"document"`
}

func contextResource(r store.Record, document json.RawMessage) *sessionContextResource {
	return &sessionContextResource{ID: r.ID, Revision: strconv.FormatUint(r.Revision, 10), ObservedAt: r.UpdatedAt, Document: document}
}

func (s *Service) GetSessionContext(ctx context.Context, req *connect.Request[pb.GetSessionContextRequest]) (*connect.Response[pb.GetSessionContextResponse], error) {
	corr := req.Header().Get(rpc.CorrelationHeader)
	if err := compactionActor(ctx); err != nil {
		return nil, rpc.Error(err, corr)
	}
	id := domain.ID(req.Msg.SessionId)
	if err := id.Validate(); err != nil {
		return nil, rpc.Error(err, corr)
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	view := sessionContextView{SessionID: id}
	caps := []pb.SessionContextCapability{}
	err := s.Store.Read(bounded, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		sr, session, err := sessionRecord(tx, id)
		if err != nil {
			return err
		}
		if session.InitialExecution == nil || (session.InitialExecution.Configuration.Harness != domain.ClaudeCode && session.InitialExecution.Configuration.Harness != domain.Codex && session.InitialExecution.Configuration.Harness != domain.OpenCode) {
			return nil
		}
		view.SessionRevision = strconv.FormatUint(sr.Revision, 10)
		view.ExecutionID = session.ExecutionSelection().ID
		h := session.InitialExecution.Configuration.Harness
		if h == domain.OpenCode {
			caps = append(caps, pb.SessionContextCapability_SESSION_CONTEXT_CAPABILITY_OPENCODE_NATIVE_OBSERVATIONS_V1)
		} else if h == domain.Codex {
			caps = append(caps, pb.SessionContextCapability_SESSION_CONTEXT_CAPABILITY_CODEX_NATIVE_OBSERVATIONS_V1)
		} else {
			caps = append(caps, pb.SessionContextCapability_SESSION_CONTEXT_CAPABILITY_CLAUDE_NATIVE_OBSERVATIONS_V1)
		}
		p := session.Execution
		if p != nil && p.ClaudeProgress != nil {
			refs := []struct {
				id     domain.ID
				kind   domain.ClaudeProgressKind
				target **sessionContextResource
			}{{p.ClaudeProgress.LatestCompactionID, domain.ClaudeCompactionProgress, &view.AutomaticBoundary}, {p.ClaudeProgress.LatestCompactionSummaryID, domain.ClaudeCompactionSummaryProgress, &view.AutomaticSummary}}
			for _, ref := range refs {
				if ref.id == "" {
					continue
				}
				r, err := tx.Get(domain.MessageKind, ref.id)
				if err != nil {
					return err
				}
				v, err := store.Decode[domain.ExecutionMessage](r)
				if err != nil || r.SessionID != id || v.ExecutionID != p.ExecutionID || v.ClaudeProgress == nil || v.ClaudeProgress.Kind != ref.kind {
					return domain.CompactionUncertain()
				}
				*ref.target = contextResource(r, r.Data)
			}
		}
		if (h == domain.Codex || h == domain.OpenCode) && p != nil && p.LatestNativeCompactionID != "" {
			r, err := tx.Get(domain.MessageKind, p.LatestNativeCompactionID)
			if err != nil {
				return err
			}
			v, err := store.Decode[domain.ExecutionMessage](r)
			if err != nil || r.SessionID != id || v.ExecutionID != p.ExecutionID || v.Progress == nil || v.Progress.Compaction == nil || v.Progress.Compaction.Harness != h {
				return domain.CompactionUncertain()
			}
			view.AutomaticBoundary = contextResource(r, r.Data)
		}
		jobID := session.LastCompactionJobID
		if jobID == "" {
			jobID = session.CompactionJobID
		}
		if jobID == "" && session.Compaction != nil {
			jobID = session.Compaction.JobID
		}
		if jobID != "" {
			r, err := tx.Get(domain.JobKind, jobID)
			if err != nil {
				return err
			}
			j, err := store.Decode[domain.Job](r)
			if err != nil || r.SessionID != id || j.Type != domain.CompactSessionJob {
				return domain.CompactionUncertain()
			}
			var input domain.SessionCompactionInput
			if domain.DecodeCompactionInput(j.Input, &input) != nil || input.Validate() != nil {
				return domain.CompactionUncertain()
			}
			document, err := json.Marshal(struct {
				ActionID    domain.ID       `json:"action_id"`
				ExecutionID domain.ID       `json:"execution_id"`
				State       domain.JobState `json:"state"`
				Problem     *domain.Error   `json:"problem"`
				Result      json.RawMessage `json:"result"`
				AcceptedAt  time.Time       `json:"accepted_at"`
				FinishedAt  *time.Time      `json:"finished_at"`
			}{input.ActionID, input.Assignment.ExecutionID, j.State, j.Problem, j.Output, j.AcceptedAt, j.FinishedAt})
			if err != nil {
				return err
			}
			view.ManualAction = contextResource(r, document)
		}
		if h == domain.Codex {
			if err := projectNativeContext(tx, session, &view); err != nil {
				return err
			}
		}
		if _, err := compactionSource(tx, sr, session, domain.NewID()); err == nil {
			if h == domain.OpenCode {
				caps = append(caps, pb.SessionContextCapability_SESSION_CONTEXT_CAPABILITY_OPENCODE_MANUAL_COMPACTION_V1)
			} else if h == domain.Codex {
				caps = append(caps, pb.SessionContextCapability_SESSION_CONTEXT_CAPABILITY_CODEX_MANUAL_COMPACTION_V1)
			} else {
				caps = append(caps, pb.SessionContextCapability_SESSION_CONTEXT_CAPABILITY_CLAUDE_MANUAL_COMPACTION_V1)
			}
		}
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	raw, err := json.Marshal(view)
	if err != nil || len(raw) > 1<<20 {
		return nil, rpc.Error(domain.Fail(domain.ResourceExhausted, "The context observation exceeds its response bound.", "Read the original retained job and progress resources individually."), corr)
	}
	response := connect.NewResponse(&pb.GetSessionContextResponse{DocumentJson: raw, Capabilities: caps})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

type nativeContextSource string

const nativeContextLastRequest nativeContextSource = "last-request-total"

type nativeContextStatus string

const (
	nativeContextLatest     nativeContextStatus = "latest"
	nativeContextHistorical nativeContextStatus = "historical"
)

// This is the native last-request snapshot, never exact live occupancy.
type sessionNativeContext struct {
	Harness        domain.Harness      `json:"harness"`
	Source         nativeContextSource `json:"source"`
	Tokens         string              `json:"tokens"`
	ObservationID  domain.ID           `json:"observation_id"`
	ExecutionID    domain.ID           `json:"execution_id"`
	NativeThreadID string              `json:"native_thread_id"`
	NativeTurnID   string              `json:"native_turn_id"`
	Sequence       string              `json:"sequence"`
	ObservedAt     time.Time           `json:"observed_at"`
	Status         nativeContextStatus `json:"status"`
}

func projectNativeContext(tx *store.Tx, session domain.Session, view *sessionContextView) error {
	p := session.Execution
	if p == nil || p.LatestUsageID == "" {
		return nil
	}
	r, err := tx.Get(domain.UsageKind, p.LatestUsageID)
	if err != nil {
		return err
	}
	usage, err := store.Decode[domain.ExecutionUsageObservation](r)
	if err != nil || r.SessionID != view.SessionID || r.UpdatedAt.IsZero() || usage.Harness != domain.Codex || usage.ExecutionID != p.ExecutionID || usage.ThreadID != p.NativeThreadID || domain.NativeIdentity(usage.ThreadID).Validate(domain.Codex, domain.NativeThreadIdentity) != nil || domain.NativeIdentity(usage.TurnID).Validate(domain.Codex, domain.NativeTurnIdentity) != nil || usage.Sequence == 0 || usage.Sequence > p.LastSequence || usage.Usage.Validate() != nil {
		return domain.CompactionUncertain()
	}
	// Read original immutable assignment attribution, including historical account
	// selection. Current account health grants no observation or native authority.
	jr, err := tx.Get(domain.JobKind, p.JobID)
	if err != nil {
		return err
	}
	job, err := store.Decode[domain.Job](jr)
	if err != nil {
		return domain.CompactionUncertain()
	}
	var input domain.ExecutionJobInput
	if jr.SessionID != view.SessionID || job.Type != domain.ExecuteSessionJob || domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.SessionID != view.SessionID || input.ExecutionID != p.ExecutionID || input.InputID != p.InputID || session.InitialExecution == nil || input.ConfigurationDigest != session.InitialExecution.ConfigurationDigest || input.Configuration.AgentID != session.AgentID || input.MachineID != session.MachineID || job.MachineID != input.MachineID || job.InstanceID.Validate() != nil || input.Configuration.Harness != domain.Codex || !supportsExecutionPublication(input, domain.ExecutionUsageObserved) || usage.AccountID != input.AccountID || usage.ConnectionID != input.ConnectionID || usage.ProviderID != input.Configuration.ProviderID || usage.SubscriptionService != input.Configuration.SubscriptionService || usage.ModelID != input.Configuration.ModelID {
		return domain.CompactionUncertain()
	}
	status := nativeContextHistorical
	if usage.ExecutionID == session.ExecutionSelection().ID && usage.TurnID == p.NativeTurnID && session.PendingInputs == 0 && session.PendingSteerID == "" {
		status = nativeContextLatest
	}
	// Same-turn Steer is also new input. Its original retained acceptance
	// sequence, rather than queue update time, identifies the snapshot boundary.
	if len(p.AcceptedInputs) > 1 {
		binding := p.AcceptedInputs[len(p.AcceptedInputs)-1]
		qr, err := tx.Get(domain.QueueKind, binding.InputID)
		if err != nil {
			return err
		}
		queued, err := store.Decode[domain.QueuedInput](qr)
		if err != nil || qr.SessionID != view.SessionID || queued.ExecutionID != p.ExecutionID || queued.Delivery != domain.InputAccepted || domain.BindSessionInput(binding.InputID, domain.SessionInput{Mode: queued.Mode, Prompt: queued.Prompt, Skills: queued.Skills, Attachments: queued.Attachments}) != binding {
			return domain.CompactionUncertain()
		}
		ar, err := tx.Get(domain.SteerKind, queued.NativeRequestID)
		if err != nil {
			return err
		}
		attempt, err := store.Decode[domain.SteerAttempt](ar)
		if err != nil || ar.SessionID != view.SessionID || attempt.ExecutionID != p.ExecutionID || attempt.InputID != binding.InputID || attempt.JobID != p.JobID || string(attempt.NativeThreadID) != usage.ThreadID || string(attempt.NativeTurnID) != p.NativeTurnID || attempt.State != domain.SteerAccepted {
			return domain.CompactionUncertain()
		}
		sequence := attempt.Sequence
		if attempt.ResolutionSequence > sequence {
			sequence = attempt.ResolutionSequence
		}
		if sequence == 0 || sequence > p.LastSequence {
			return domain.CompactionUncertain()
		}
		if sequence > usage.Sequence {
			status = nativeContextHistorical
		}
	}
	if view.AutomaticBoundary != nil {
		var boundary domain.ExecutionMessage
		if domain.Decode(view.AutomaticBoundary.Document, &boundary) != nil || boundary.NativeThreadID != usage.ThreadID || boundary.FirstSequence == 0 || boundary.LastSequence < boundary.FirstSequence || boundary.LastSequence > p.LastSequence {
			return domain.CompactionUncertain()
		}
		if boundary.LastSequence > usage.Sequence {
			status = nativeContextHistorical
		}
	}
	if view.ManualAction != nil {
		var action struct {
			ActionID    domain.ID       `json:"action_id"`
			ExecutionID domain.ID       `json:"execution_id"`
			State       domain.JobState `json:"state"`
			Problem     *domain.Error   `json:"problem"`
			Result      json.RawMessage `json:"result"`
			AcceptedAt  time.Time       `json:"accepted_at"`
			FinishedAt  *time.Time      `json:"finished_at"`
		}
		if domain.Decode(view.ManualAction.Document, &action) != nil {
			return domain.CompactionUncertain()
		}
		if !action.AcceptedAt.Before(r.UpdatedAt) {
			status = nativeContextHistorical
		}
	}
	view.NativeContext = &sessionNativeContext{Harness: domain.Codex, Source: nativeContextLastRequest, Tokens: strconv.FormatInt(*usage.Usage.Last.Total, 10), ObservationID: r.ID, ExecutionID: usage.ExecutionID, NativeThreadID: usage.ThreadID, NativeTurnID: usage.TurnID, Sequence: strconv.FormatUint(usage.Sequence, 10), ObservedAt: r.UpdatedAt, Status: status}
	return nil
}
