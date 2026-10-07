package server

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func invalidActivity() error {
	return domain.Fail(domain.RecoveryRequired, "The retained activity source is inconsistent.", "Preserve and inspect the original source; activity cannot infer missing execution, scheduling or PR handling evidence.")
}

func activityExecution(tx *store.Tx, id, session, project domain.ID) (domain.ExecutionJobInput, pb.ActivityJobState, error) {
	r, err := tx.Get(domain.JobKind, id)
	if err != nil {
		return domain.ExecutionJobInput{}, 0, err
	}
	j, err := store.Decode[domain.Job](r)
	if err != nil {
		return domain.ExecutionJobInput{}, 0, err
	}
	var input domain.ExecutionJobInput
	if j.Type != domain.ExecuteSessionJob || r.SessionID != session || r.ProjectID != project || domain.Decode(j.Input, &input) != nil || input.SessionID != session || input.ExecutionID.Validate() != nil || input.AccountID.Validate() != nil {
		return input, 0, invalidActivity()
	}
	state, ok := map[domain.JobState]pb.ActivityJobState{
		domain.JobQueued:    pb.ActivityJobState_ACTIVITY_JOB_STATE_QUEUED,
		domain.JobClaimed:   pb.ActivityJobState_ACTIVITY_JOB_STATE_CLAIMED,
		domain.JobSucceeded: pb.ActivityJobState_ACTIVITY_JOB_STATE_SUCCEEDED,
		domain.JobFailed:    pb.ActivityJobState_ACTIVITY_JOB_STATE_FAILED,
		domain.JobUncertain: pb.ActivityJobState_ACTIVITY_JOB_STATE_UNCERTAIN,
		domain.JobCanceled:  pb.ActivityJobState_ACTIVITY_JOB_STATE_CANCELED,
	}[j.State]
	if !ok {
		return input, 0, invalidActivity()
	}
	return input, state, nil
}

func activityEntry(tx *store.Tx, r store.Record) (*pb.ActivityEntry, error) {
	entry := &pb.ActivityEntry{Id: string(r.ID), SourceKind: rpc.WireKind(r.Kind), SourceRevision: r.Revision, ObservedAtUnixMs: r.CreatedAt.UnixMilli(), SessionId: string(r.SessionID), ProjectId: string(r.ProjectID)}
	switch r.Kind {
	case domain.JobKind:
		input, state, err := activityExecution(tx, r.ID, r.SessionID, r.ProjectID)
		if err != nil {
			return nil, err
		}
		entry.JobState = state
		entry.Kind = pb.ActivityKind_ACTIVITY_KIND_EXECUTION_ACCEPTED
		entry.JobId, entry.ExecutionId, entry.AccountId = string(r.ID), string(input.ExecutionID), string(input.AccountID)
	case domain.InboxKind:
		inbox, err := store.Decode[domain.InboxEntry](r)
		if err != nil {
			return nil, err
		}
		if inbox.Validate() != nil || inbox.Source != domain.ExecutionTerminalInbox {
			return nil, invalidActivity()
		}
		input, state, err := activityExecution(tx, inbox.Terminal.JobID, r.SessionID, r.ProjectID)
		if err != nil {
			return nil, err
		}
		if input.ExecutionID != inbox.SourceID || input.InputID != inbox.Terminal.InputID {
			return nil, invalidActivity()
		}
		entry.JobState = state
		entry.JobId, entry.ExecutionId, entry.AccountId = string(inbox.Terminal.JobID), string(inbox.SourceID), string(input.AccountID)
		switch inbox.Terminal.Outcome {
		case domain.ExecutionSucceeded:
			entry.Kind = pb.ActivityKind_ACTIVITY_KIND_EXECUTION_SUCCEEDED
		case domain.ExecutionFailed:
			entry.Kind = pb.ActivityKind_ACTIVITY_KIND_EXECUTION_FAILED
		case domain.ExecutionStopped:
			entry.Kind = pb.ActivityKind_ACTIVITY_KIND_EXECUTION_STOPPED
		default:
			return nil, invalidActivity()
		}
	case domain.OccurrenceKind:
		occurrence, err := store.Decode[domain.ScheduleOccurrence](r)
		if err != nil {
			return nil, err
		}
		if occurrence.Validate() != nil || occurrence.SessionID != r.SessionID || occurrence.Selection.ProjectID != r.ProjectID {
			return nil, invalidActivity()
		}
		if occurrence.Trigger == domain.CronOccurrence {
			entry.Kind = pb.ActivityKind_ACTIVITY_KIND_SCHEDULE_CRON
		} else {
			entry.Kind = pb.ActivityKind_ACTIVITY_KIND_SCHEDULE_RUN_NOW
		}
		entry.ScheduleId, entry.OccurrenceId = string(occurrence.ScheduleID), string(r.ID)
		switch occurrence.State {
		case domain.OccurrenceWaiting:
			entry.OccurrenceState = pb.ActivityOccurrenceState_ACTIVITY_OCCURRENCE_STATE_WAITING
		case domain.OccurrenceActive:
			entry.OccurrenceState = pb.ActivityOccurrenceState_ACTIVITY_OCCURRENCE_STATE_ACTIVE
		case domain.OccurrenceSucceeded:
			entry.OccurrenceState = pb.ActivityOccurrenceState_ACTIVITY_OCCURRENCE_STATE_SUCCEEDED
		case domain.OccurrenceFailed:
			entry.OccurrenceState = pb.ActivityOccurrenceState_ACTIVITY_OCCURRENCE_STATE_FAILED
		case domain.OccurrenceStopped:
			entry.OccurrenceState = pb.ActivityOccurrenceState_ACTIVITY_OCCURRENCE_STATE_STOPPED
		case domain.OccurrenceSkipped:
			entry.OccurrenceState = pb.ActivityOccurrenceState_ACTIVITY_OCCURRENCE_STATE_SKIPPED
		}
	case domain.ProblemKind:
		return activityPREntry(tx, r, entry)
	default:
		return nil, invalidActivity()
	}
	return entry, nil
}

func (s *Service) ListActivity(ctx context.Context, req *connect.Request[pb.ListActivityRequest]) (*connect.Response[pb.ListActivityResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok {
		return nil, rpc.Error(domain.Fail(domain.PermissionDenied, "Server authentication is required.", "Use the server token or a registered device credential."), correlation)
	}
	f := store.ActivityFilter{SessionID: domain.ID(req.Msg.SessionId), ProjectID: domain.ID(req.Msg.ProjectId), Limit: int(req.Msg.PageSize)}
	if f.Limit == 0 {
		f.Limit = 50
	}
	if err := f.Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	scope := fmt.Sprintf("activity:%s:%s:%s:%s", actor.Type, actor.DeviceID, f.SessionID, f.ProjectID)
	if req.Msg.PageToken != "" {
		cursor, err := s.Identity.DecodeCursor(req.Msg.PageToken, scope)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		if cursor.After == "" {
			return nil, rpc.Error(domain.Fail(domain.CursorExpired, "The activity cursor has no source position.", "Restart activity pagination."), correlation)
		}
		f.After, f.Epoch = cursor.After, cursor.Sequence
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result := &pb.ListActivityResponse{Capabilities: []pb.ActivityCapability{pb.ActivityCapability_ACTIVITY_CAPABILITY_PR_HANDLING_V1}}
	err := s.Store.Read(bounded, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		rows, more, epoch, err := tx.ActivityPage(f)
		if err != nil {
			return err
		}
		var last domain.ID
		var size int
		for _, r := range rows {
			entry, err := activityEntry(tx, r)
			if err != nil {
				return err
			}
			encoded, err := protojson.Marshal(entry)
			if err != nil {
				return invalidActivity()
			}
			n := max(proto.Size(entry), len(encoded)) + 32
			// Reserve space for the signed cursor and response capability/framing.
			if size+n > (3<<20)-8192 {
				if len(result.Entries) == 0 {
					return domain.Fail(domain.ResourceExhausted, "The original activity metadata exceeds the page bound.", "Preserve the complete source; no activity was truncated.")
				}
				more = true
				break
			}
			size += n
			result.Entries = append(result.Entries, entry)
			last = r.ID
		}
		// PR version references are complete and bounded in both encodings.
		if more && last != "" {
			result.NextPageToken, err = s.Identity.EncodeCursor(security.Cursor{Scope: scope, After: last, Sequence: epoch})
		}
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.DebugContext(ctx, "activity_read_completed", "correlation_id", correlation, "entry_count", len(result.Entries), "has_more", result.NextPageToken != "")
	response := connect.NewResponse(result)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
