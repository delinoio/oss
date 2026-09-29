package server

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
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
		var typeInfo struct {
			Type domain.PRProblemRecordType `json:"type"`
		}
		if json.Unmarshal(r.Data, &typeInfo) != nil {
			return nil, invalidActivity()
		}
		var v domain.PRActivity
		var err error
		if typeInfo.Type == domain.PRHandlingVerificationRecord {
			proof, readErr := store.Decode[domain.PRHandlingVerification](r)
			if readErr != nil || proof.Validate() != nil {
				return nil, invalidActivity()
			}
			_, original, readErr := tx.GetPRProblem(proof.Problems[0].ID)
			if readErr != nil {
				return nil, readErr
			}
			// Original version navigation is immutable even when later collection
			// changes the current set's repository owner or name.
			target := original.Target
			v = domain.PRActivity{Version: 1, Type: domain.PRActivityRecord, Action: domain.PRActivityVerifiedHandled, SourceID: r.ID, SourceRevision: r.Revision, VerificationID: r.ID, SetID: proof.SetID, RemoteRepositoryID: target.RemoteRepositoryID, PullRequestID: target.PullRequestID, Number: target.Number, Owner: target.Owner, Name: target.Name, Problems: proof.Problems, Actor: proof.Actor}
		} else {
			v, err = store.Decode[domain.PRActivity](r)
		}
		if err != nil || v.Validate() != nil || r.Revision != 1 || !r.CreatedAt.Equal(v.Actor.At.Truncate(time.Millisecond)) {
			return nil, invalidActivity()
		}
		if err := validatePRActivitySource(tx, r, v); err != nil {
			return nil, err
		}
		entry.SourceRevision = v.SourceRevision
		entry.ExecutionId = string(v.ExecutionID)
		entry.PullRequest = &pb.ActivityPRMetadata{ProblemSetId: string(v.SetID), RemoteRepositoryId: v.RemoteRepositoryID, PullRequestId: v.PullRequestID, Number: v.Number, Owner: v.Owner, Name: v.Name, SourceId: string(v.SourceID), DeviceId: string(v.Actor.DeviceID), RequestId: string(v.Actor.RequestID)}
		entry.PullRequest.VerificationId = string(v.VerificationID)
		if v.Actor.ActorType == domain.OwnerDevice {
			entry.PullRequest.ActorType = pb.ActivityPRActorType_ACTIVITY_PR_ACTOR_TYPE_OWNER
		} else {
			entry.PullRequest.ActorType = pb.ActivityPRActorType_ACTIVITY_PR_ACTOR_TYPE_CLIENT
		}
		for _, p := range v.Problems {
			entry.PullRequest.Problems = append(entry.PullRequest.Problems, &pb.ActivityPRProblemReference{Id: string(p.ID), ContentVersion: p.ContentVersion})
		}
		switch v.Action {
		case domain.PRActivityObserved:
			entry.Kind = pb.ActivityKind_ACTIVITY_KIND_PR_PROBLEM_OBSERVED
		case domain.PRActivityDismissed:
			entry.Kind = pb.ActivityKind_ACTIVITY_KIND_PR_PROBLEM_DISMISSED
		case domain.PRActivityVerifiedHandled:
			entry.Kind = pb.ActivityKind_ACTIVITY_KIND_PR_VERIFIED_HANDLED
		case domain.PRActivityAttempt:
			entry.Kind = pb.ActivityKind_ACTIVITY_KIND_PR_REMEDIATION_ATTEMPT
			if v.Mode == domain.PRRemediationManual {
				entry.PullRequest.Mode = pb.ActivityPRMode_ACTIVITY_PR_MODE_MANUAL
			} else {
				entry.PullRequest.Mode = pb.ActivityPRMode_ACTIVITY_PR_MODE_AUTOMATIC
			}
			state := map[domain.PRRemediationAttemptState]pb.ActivityPRAttemptState{
				domain.PRRemediationReserved:  pb.ActivityPRAttemptState_ACTIVITY_PR_ATTEMPT_STATE_RESERVED,
				domain.PRRemediationBound:     pb.ActivityPRAttemptState_ACTIVITY_PR_ATTEMPT_STATE_BOUND,
				domain.PRRemediationRunning:   pb.ActivityPRAttemptState_ACTIVITY_PR_ATTEMPT_STATE_RUNNING,
				domain.PRRemediationUncertain: pb.ActivityPRAttemptState_ACTIVITY_PR_ATTEMPT_STATE_UNCERTAIN,
				domain.PRRemediationCanceled:  pb.ActivityPRAttemptState_ACTIVITY_PR_ATTEMPT_STATE_CANCELED,
			}[v.AttemptState]
			if v.AttemptState == domain.PRRemediationFinished {
				state = map[domain.ExecutionOutcome]pb.ActivityPRAttemptState{
					domain.ExecutionSucceeded:  pb.ActivityPRAttemptState_ACTIVITY_PR_ATTEMPT_STATE_SUCCEEDED,
					domain.ExecutionFailed:     pb.ActivityPRAttemptState_ACTIVITY_PR_ATTEMPT_STATE_FAILED,
					domain.ExecutionStopped:    pb.ActivityPRAttemptState_ACTIVITY_PR_ATTEMPT_STATE_STOPPED,
					domain.ExecutionNotStarted: pb.ActivityPRAttemptState_ACTIVITY_PR_ATTEMPT_STATE_NOT_STARTED,
				}[v.Outcome]
			}
			entry.PullRequest.AttemptState = state
		}
	default:
		return nil, invalidActivity()
	}
	return entry, nil
}

func validatePRActivitySource(tx *store.Tx, activity store.Record, v domain.PRActivity) error {
	setRow, set, err := tx.GetPRProblemSet(v.SetID)
	if err != nil {
		return err
	}
	if set.Target.RemoteRepositoryID != v.RemoteRepositoryID || set.Target.PullRequestID != v.PullRequestID || set.Target.Number != v.Number || setRow.Kind != domain.ProblemKind {
		return invalidActivity()
	}
	if v.Action == domain.PRActivityVerifiedHandled {
		proof, err := store.Decode[domain.PRHandlingVerification](activity)
		if err != nil || proof.Validate() != nil || activity.ID != v.VerificationID || proof.SetID != v.SetID || !slices.Equal(proof.Problems, v.Problems) || proof.Actor != v.Actor {
			return invalidActivity()
		}
		for _, ref := range v.Problems {
			_, p, err := tx.GetPRProblem(ref.ID)
			if err != nil {
				return err
			}
			if p.SetID != v.SetID || p.ContentVersion != ref.ContentVersion {
				return invalidActivity()
			}
		}
		return nil
	}
	if v.Action == domain.PRActivityAttempt {
		r, attempt, err := tx.GetPRRemediationAttempt(v.SourceID)
		if err != nil {
			return err
		}
		if r.Revision < v.SourceRevision || attempt.SetID != v.SetID || !slices.Equal(attempt.Problems, v.Problems) || attempt.Mode != v.Mode || v.ExecutionID != "" && v.ExecutionID != attempt.ExecutionID || v.AttemptState != domain.PRRemediationReserved && activity.SessionID != attempt.SessionID {
			return invalidActivity()
		}
		return nil
	}
	r, p, err := tx.GetPRProblem(v.SourceID)
	if err != nil {
		return err
	}
	if r.Revision < v.SourceRevision || p.SetID != v.SetID || p.Target.Owner != v.Owner || p.Target.Name != v.Name || p.ContentVersion != v.Problems[0].ContentVersion || activity.SessionID != "" || activity.ProjectID != "" {
		return invalidActivity()
	}
	if v.Action == domain.PRActivityDismissed && (p.Dismissal == nil || *p.Dismissal != v.Actor) {
		return invalidActivity()
	}
	return nil
}

func (s *Service) ListActivity(ctx context.Context, req *connect.Request[pb.ListActivityRequest]) (*connect.Response[pb.ListActivityResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return nil, rpc.Error(domain.Fail(domain.PermissionDenied, "Only an owner or paired client can read activity.", "Use an authorized product client."), correlation)
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
