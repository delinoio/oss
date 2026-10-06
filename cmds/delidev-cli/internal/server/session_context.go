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

// Current utilization is unavailable in this profile. Compaction boundaries
// remain timestamped historical measurements, independently of response usage.
type sessionContextView struct {
	SessionRevision   string                      `json:"session_revision"`
	SessionID         domain.ID                   `json:"session_id"`
	ExecutionID       domain.ID                   `json:"execution_id,omitempty"`
	CurrentTokens     *domain.ClaudeProgressCount `json:"current_tokens"`
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
