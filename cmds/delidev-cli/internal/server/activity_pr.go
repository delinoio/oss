package server

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func activityPREntry(tx *store.Tx, r store.Record, entry *pb.ActivityEntry) (*pb.ActivityEntry, error) {
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
