#[derive(Clone, Debug, PartialEq, Eq)]
pub enum SubTaskType {
    InitialImplementation,
    RequestChanges,
    PrCreate,
    PrReviewFix,
    PrCiFix,
    ManualRetry,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum SubTaskStatus {
    Queued,
    InProgress,
    WaitingForPlanApproval,
    WaitingForUserInput,
    Completed,
    Failed,
    Cancelled,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum SubTaskCompletionReason {
    Succeeded,
    Revised,
    PlanRejected,
    Failed,
    CancelledByUser,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct SubTask {
    pub sub_task_id: String,
    pub unit_task_id: String,
    pub task_type: SubTaskType,
    pub status: SubTaskStatus,
    pub completion_reason: Option<SubTaskCompletionReason>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum PlanDecision {
    Approve,
    Revise,
    Reject,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct SubmitPlanDecisionRequest {
    pub decision: PlanDecision,
    pub revision_note: Option<String>,
    pub next_sub_task_id: Option<String>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum SubmitPlanDecisionResult {
    Resumed {
        updated_sub_task: SubTask,
    },
    Revised {
        updated_sub_task: SubTask,
        created_sub_task: SubTask,
        revision_note: String,
    },
    Rejected {
        updated_sub_task: SubTask,
    },
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum SubmitPlanDecisionError {
    InvalidSubTaskStatus,
    RevisionNoteRequired,
    NextSubTaskIdRequired,
}

pub fn submit_plan_decision(
    current_sub_task: &SubTask,
    request: SubmitPlanDecisionRequest,
) -> Result<SubmitPlanDecisionResult, SubmitPlanDecisionError> {
    if current_sub_task.status != SubTaskStatus::WaitingForPlanApproval {
        return Err(SubmitPlanDecisionError::InvalidSubTaskStatus);
    }

    match request.decision {
        PlanDecision::Approve => Ok(SubmitPlanDecisionResult::Resumed {
            updated_sub_task: SubTask {
                status: SubTaskStatus::InProgress,
                ..current_sub_task.clone()
            },
        }),
        PlanDecision::Revise => {
            let revision_note = request
                .revision_note
                .as_deref()
                .map(str::trim)
                .filter(|value| !value.is_empty())
                .map(str::to_owned)
                .ok_or(SubmitPlanDecisionError::RevisionNoteRequired)?;
            let next_sub_task_id = request
                .next_sub_task_id
                .as_deref()
                .map(str::trim)
                .filter(|value| !value.is_empty())
                .map(str::to_owned)
                .ok_or(SubmitPlanDecisionError::NextSubTaskIdRequired)?;

            let updated_sub_task = SubTask {
                status: SubTaskStatus::Completed,
                completion_reason: Some(SubTaskCompletionReason::Revised),
                ..current_sub_task.clone()
            };
            let created_sub_task = SubTask {
                sub_task_id: next_sub_task_id,
                unit_task_id: current_sub_task.unit_task_id.clone(),
                task_type: SubTaskType::RequestChanges,
                status: SubTaskStatus::Queued,
                completion_reason: None,
            };

            Ok(SubmitPlanDecisionResult::Revised {
                updated_sub_task,
                created_sub_task,
                revision_note,
            })
        }
        PlanDecision::Reject => Ok(SubmitPlanDecisionResult::Rejected {
            updated_sub_task: SubTask {
                status: SubTaskStatus::Cancelled,
                completion_reason: Some(SubTaskCompletionReason::PlanRejected),
                ..current_sub_task.clone()
            },
        }),
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum StreamEventType {
    TaskUpdated,
    SubTaskUpdated,
    SessionOutput,
    SessionStateChanged,
    PrUpdated,
    ReviewAssistUpdated,
    InlineCommentUpdated,
    NotificationCreated,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct WorkspaceStreamEnvelope {
    pub workspace_id: String,
    pub sequence: u64,
    pub event_type: StreamEventType,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct CursorOutOfRange {
    pub earliest_available_sequence: u64,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum StreamReplayError {
    CursorOutOfRange(CursorOutOfRange),
    InvalidSequence,
}

pub fn replay_workspace_events(
    events: &[WorkspaceStreamEnvelope],
    from_sequence: Option<u64>,
    earliest_available_sequence: u64,
) -> Result<Vec<WorkspaceStreamEnvelope>, StreamReplayError> {
    if earliest_available_sequence == 0 {
        return Err(StreamReplayError::InvalidSequence);
    }

    let normalized_from_sequence = from_sequence.unwrap_or(earliest_available_sequence - 1);
    if normalized_from_sequence
        .checked_add(1)
        .is_some_and(|sequence| sequence < earliest_available_sequence)
    {
        return Err(StreamReplayError::CursorOutOfRange(CursorOutOfRange {
            earliest_available_sequence,
        }));
    }

    let mut replayed = Vec::new();
    let mut previous_sequence: Option<u64> = None;

    for event in events {
        if event.sequence == 0 {
            return Err(StreamReplayError::InvalidSequence);
        }

        if let Some(previous) = previous_sequence {
            if event.sequence <= previous {
                return Err(StreamReplayError::InvalidSequence);
            }
        }

        previous_sequence = Some(event.sequence);

        if event.sequence > normalized_from_sequence {
            replayed.push(event.clone());
        }
    }

    Ok(replayed)
}

#[cfg(test)]
mod tests {
    use super::{
        replay_workspace_events, submit_plan_decision, PlanDecision, StreamEventType,
        StreamReplayError, SubTask, SubTaskCompletionReason, SubTaskStatus, SubTaskType,
        SubmitPlanDecisionError, SubmitPlanDecisionRequest, SubmitPlanDecisionResult,
        WorkspaceStreamEnvelope,
    };

    fn waiting_plan_sub_task() -> SubTask {
        SubTask {
            sub_task_id: "sub-1".to_owned(),
            unit_task_id: "unit-1".to_owned(),
            task_type: SubTaskType::InitialImplementation,
            status: SubTaskStatus::WaitingForPlanApproval,
            completion_reason: None,
        }
    }

    #[test]
    fn approve_resumes_current_sub_task() {
        let result = submit_plan_decision(
            &waiting_plan_sub_task(),
            SubmitPlanDecisionRequest {
                decision: PlanDecision::Approve,
                revision_note: None,
                next_sub_task_id: None,
            },
        )
        .unwrap();

        assert_eq!(
            result,
            SubmitPlanDecisionResult::Resumed {
                updated_sub_task: SubTask {
                    status: SubTaskStatus::InProgress,
                    ..waiting_plan_sub_task()
                },
            }
        );
    }

    #[test]
    fn revise_closes_current_and_creates_request_changes_sub_task() {
        let result = submit_plan_decision(
            &waiting_plan_sub_task(),
            SubmitPlanDecisionRequest {
                decision: PlanDecision::Revise,
                revision_note: Some("Need stronger test coverage".to_owned()),
                next_sub_task_id: Some("sub-2".to_owned()),
            },
        )
        .unwrap();

        assert_eq!(
            result,
            SubmitPlanDecisionResult::Revised {
                updated_sub_task: SubTask {
                    status: SubTaskStatus::Completed,
                    completion_reason: Some(SubTaskCompletionReason::Revised),
                    ..waiting_plan_sub_task()
                },
                created_sub_task: SubTask {
                    sub_task_id: "sub-2".to_owned(),
                    unit_task_id: "unit-1".to_owned(),
                    task_type: SubTaskType::RequestChanges,
                    status: SubTaskStatus::Queued,
                    completion_reason: None,
                },
                revision_note: "Need stronger test coverage".to_owned(),
            }
        );
    }

    #[test]
    fn revise_requires_revision_note() {
        let error = submit_plan_decision(
            &waiting_plan_sub_task(),
            SubmitPlanDecisionRequest {
                decision: PlanDecision::Revise,
                revision_note: Some("   ".to_owned()),
                next_sub_task_id: Some("sub-2".to_owned()),
            },
        )
        .unwrap_err();

        assert_eq!(error, SubmitPlanDecisionError::RevisionNoteRequired);
    }

    #[test]
    fn reject_cancels_current_without_new_sub_task() {
        let result = submit_plan_decision(
            &waiting_plan_sub_task(),
            SubmitPlanDecisionRequest {
                decision: PlanDecision::Reject,
                revision_note: None,
                next_sub_task_id: None,
            },
        )
        .unwrap();

        assert_eq!(
            result,
            SubmitPlanDecisionResult::Rejected {
                updated_sub_task: SubTask {
                    status: SubTaskStatus::Cancelled,
                    completion_reason: Some(SubTaskCompletionReason::PlanRejected),
                    ..waiting_plan_sub_task()
                },
            }
        );
    }

    #[test]
    fn replay_from_sequence_is_exclusive() {
        let events = vec![
            WorkspaceStreamEnvelope {
                workspace_id: "workspace-1".to_owned(),
                sequence: 10,
                event_type: StreamEventType::TaskUpdated,
            },
            WorkspaceStreamEnvelope {
                workspace_id: "workspace-1".to_owned(),
                sequence: 11,
                event_type: StreamEventType::SubTaskUpdated,
            },
            WorkspaceStreamEnvelope {
                workspace_id: "workspace-1".to_owned(),
                sequence: 12,
                event_type: StreamEventType::SessionOutput,
            },
        ];

        let replayed = replay_workspace_events(&events, Some(11), 10).unwrap();

        assert_eq!(replayed.len(), 1);
        assert_eq!(replayed[0].sequence, 12);
    }

    #[test]
    fn replay_fails_with_out_of_range_when_cursor_is_older_than_retention() {
        let events = vec![WorkspaceStreamEnvelope {
            workspace_id: "workspace-1".to_owned(),
            sequence: 20,
            event_type: StreamEventType::TaskUpdated,
        }];

        let error = replay_workspace_events(&events, Some(17), 20).unwrap_err();

        assert_eq!(
            error,
            StreamReplayError::CursorOutOfRange(super::CursorOutOfRange {
                earliest_available_sequence: 20,
            })
        );
    }

    #[test]
    fn replay_rejects_non_monotonic_sequences() {
        let events = vec![
            WorkspaceStreamEnvelope {
                workspace_id: "workspace-1".to_owned(),
                sequence: 10,
                event_type: StreamEventType::TaskUpdated,
            },
            WorkspaceStreamEnvelope {
                workspace_id: "workspace-1".to_owned(),
                sequence: 10,
                event_type: StreamEventType::SubTaskUpdated,
            },
        ];

        let error = replay_workspace_events(&events, Some(9), 10).unwrap_err();
        assert_eq!(error, StreamReplayError::InvalidSequence);
    }
}
