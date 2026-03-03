package service

import "strings"

type SubTaskType uint8

const (
	SubTaskTypeInitialImplementation SubTaskType = iota + 1
	SubTaskTypeRequestChanges
	SubTaskTypePRCreate
	SubTaskTypePRReviewFix
	SubTaskTypePRCIFix
	SubTaskTypeManualRetry
)

type SubTaskStatus uint8

const (
	SubTaskStatusQueued SubTaskStatus = iota + 1
	SubTaskStatusInProgress
	SubTaskStatusWaitingForPlanApproval
	SubTaskStatusWaitingForUserInput
	SubTaskStatusCompleted
	SubTaskStatusFailed
	SubTaskStatusCancelled
)

type SubTaskCompletionReason uint8

const (
	SubTaskCompletionReasonSucceeded SubTaskCompletionReason = iota + 1
	SubTaskCompletionReasonRevised
	SubTaskCompletionReasonPlanRejected
	SubTaskCompletionReasonFailed
	SubTaskCompletionReasonCancelledByUser
)

type SubTask struct {
	SubTaskID        string
	UnitTaskID       string
	Type             SubTaskType
	Status           SubTaskStatus
	CompletionReason *SubTaskCompletionReason
}

type PlanDecision uint8

const (
	PlanDecisionApprove PlanDecision = iota + 1
	PlanDecisionRevise
	PlanDecisionReject
)

type SubmitPlanDecisionRequest struct {
	Decision      PlanDecision
	RevisionNote  string
	NextSubTaskID string
}

type SubmitPlanDecisionResultType uint8

const (
	SubmitPlanDecisionResultTypeResumed SubmitPlanDecisionResultType = iota + 1
	SubmitPlanDecisionResultTypeRevised
	SubmitPlanDecisionResultTypeRejected
)

type SubmitPlanDecisionResult struct {
	Type           SubmitPlanDecisionResultType
	UpdatedSubTask SubTask
	CreatedSubTask *SubTask
	RevisionNote   string
}

type SubmitPlanDecisionErrorCode uint8

const (
	SubmitPlanDecisionErrorCodeInvalidSubTaskStatus SubmitPlanDecisionErrorCode = iota + 1
	SubmitPlanDecisionErrorCodeRevisionNoteRequired
	SubmitPlanDecisionErrorCodeNextSubTaskIDRequired
)

type SubmitPlanDecisionError struct {
	Code SubmitPlanDecisionErrorCode
}

func (e *SubmitPlanDecisionError) Error() string {
	if e == nil {
		return "submit plan decision error"
	}

	return e.Code.String()
}

func (c SubmitPlanDecisionErrorCode) String() string {
	switch c {
	case SubmitPlanDecisionErrorCodeInvalidSubTaskStatus:
		return "invalid subtask status"
	case SubmitPlanDecisionErrorCodeRevisionNoteRequired:
		return "revision note is required"
	case SubmitPlanDecisionErrorCodeNextSubTaskIDRequired:
		return "next subtask id is required"
	default:
		return "unknown submit plan decision error"
	}
}

func SubmitPlanDecision(currentSubTask SubTask, request SubmitPlanDecisionRequest) (SubmitPlanDecisionResult, error) {
	if currentSubTask.Status != SubTaskStatusWaitingForPlanApproval {
		return SubmitPlanDecisionResult{}, &SubmitPlanDecisionError{Code: SubmitPlanDecisionErrorCodeInvalidSubTaskStatus}
	}

	switch request.Decision {
	case PlanDecisionApprove:
		updatedSubTask := currentSubTask
		updatedSubTask.Status = SubTaskStatusInProgress

		return SubmitPlanDecisionResult{
			Type:           SubmitPlanDecisionResultTypeResumed,
			UpdatedSubTask: updatedSubTask,
		}, nil
	case PlanDecisionRevise:
		revisionNote := strings.TrimSpace(request.RevisionNote)
		if revisionNote == "" {
			return SubmitPlanDecisionResult{}, &SubmitPlanDecisionError{Code: SubmitPlanDecisionErrorCodeRevisionNoteRequired}
		}

		nextSubTaskID := strings.TrimSpace(request.NextSubTaskID)
		if nextSubTaskID == "" {
			return SubmitPlanDecisionResult{}, &SubmitPlanDecisionError{Code: SubmitPlanDecisionErrorCodeNextSubTaskIDRequired}
		}

		completionReason := SubTaskCompletionReasonRevised
		updatedSubTask := currentSubTask
		updatedSubTask.Status = SubTaskStatusCompleted
		updatedSubTask.CompletionReason = &completionReason

		createdSubTask := SubTask{
			SubTaskID:  nextSubTaskID,
			UnitTaskID: currentSubTask.UnitTaskID,
			Type:       SubTaskTypeRequestChanges,
			Status:     SubTaskStatusQueued,
		}

		return SubmitPlanDecisionResult{
			Type:           SubmitPlanDecisionResultTypeRevised,
			UpdatedSubTask: updatedSubTask,
			CreatedSubTask: &createdSubTask,
			RevisionNote:   revisionNote,
		}, nil
	case PlanDecisionReject:
		completionReason := SubTaskCompletionReasonPlanRejected
		updatedSubTask := currentSubTask
		updatedSubTask.Status = SubTaskStatusCancelled
		updatedSubTask.CompletionReason = &completionReason

		return SubmitPlanDecisionResult{
			Type:           SubmitPlanDecisionResultTypeRejected,
			UpdatedSubTask: updatedSubTask,
		}, nil
	default:
		return SubmitPlanDecisionResult{}, &SubmitPlanDecisionError{Code: SubmitPlanDecisionErrorCodeInvalidSubTaskStatus}
	}
}
