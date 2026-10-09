// SPDX-License-Identifier: Apache-2.0
package domain

// Notification presentation never changes the inbox's independent read state
// or the original request's execution/response authority.
type NotificationKind string
type NotificationState string

const (
	SubscriptionRecoveryNotification NotificationKind  = "subscription-recovery"
	RequestNotification              NotificationKind  = "request"
	SucceededNotification            NotificationKind  = "succeeded"
	FailedNotification               NotificationKind  = "failed"
	StoppedNotification              NotificationKind  = "stopped"
	NotificationClaimed              NotificationState = "claimed"
	NotificationSubmitted            NotificationState = "submitted"
	NotificationDenied               NotificationState = "denied"
	NotificationFailed               NotificationState = "failed"
	NotificationUncertain            NotificationState = "uncertain"
)

func (k NotificationKind) Valid() bool {
	return k.Operational() || k == QuestionNotification || k == ApprovalNotification || k == RequestNotification || k == SucceededNotification || k == FailedNotification || k == StoppedNotification || k == SubscriptionRecoveryNotification
}
func (s NotificationState) Reportable() bool {
	return s == NotificationSubmitted || s == NotificationDenied || s == NotificationFailed || s == NotificationUncertain
}

type SituationNotificationPreferences struct {
	Questions           bool `json:"questions"`
	Approvals           bool `json:"approvals"`
	Succeeded           bool `json:"succeeded"`
	Failed              bool `json:"failed"`
	Stopped             bool `json:"stopped"`
	ServerLost          bool `json:"server_lost"`
	ServerRestored      bool `json:"server_restored"`
	WorkerUnavailable   bool `json:"worker_unavailable"`
	WorkerAvailable     bool `json:"worker_available"`
	QuotaExhausted      bool `json:"quota_exhausted"`
	ScheduleStartFailed bool `json:"schedule_start_failed"`
	ScheduleOffline     bool `json:"schedule_offline"`
}

func DefaultSituationNotifications(v NotificationPreferences) SituationNotificationPreferences {
	return SituationNotificationPreferences{Questions: v.Interactions, Approvals: v.Interactions, Succeeded: v.Terminals, Failed: v.Terminals, Stopped: v.Terminals, ServerLost: true, WorkerUnavailable: true, QuotaExhausted: true, ScheduleStartFailed: true, ScheduleOffline: true}
}
func (v SituationNotificationPreferences) Enabled(k NotificationKind) bool {
	switch k {
	case QuestionNotification:
		return v.Questions
	case ApprovalNotification:
		return v.Approvals
	case SucceededNotification:
		return v.Succeeded
	case FailedNotification:
		return v.Failed
	case StoppedNotification:
		return v.Stopped
	case WorkerUnavailableNotification:
		return v.WorkerUnavailable
	case WorkerAvailableNotification:
		return v.WorkerAvailable
	case QuotaExhaustedNotification:
		return v.QuotaExhausted
	case ScheduleStartFailedNotification:
		return v.ScheduleStartFailed
	case ScheduleServerOfflineNotification, ScheduleWorkerOfflineNotification:
		return v.ScheduleOffline
	}
	return false
}

var OperationalNotificationKinds = []NotificationKind{WorkerUnavailableNotification, WorkerAvailableNotification, QuotaExhaustedNotification, ScheduleStartFailedNotification, ScheduleServerOfflineNotification, ScheduleWorkerOfflineNotification}

const (
	QuestionNotification              NotificationKind = "question"
	ApprovalNotification              NotificationKind = "approval"
	WorkerUnavailableNotification     NotificationKind = "worker-unavailable"
	WorkerAvailableNotification       NotificationKind = "worker-available"
	QuotaExhaustedNotification        NotificationKind = "quota-exhausted"
	ScheduleStartFailedNotification   NotificationKind = "schedule-start-failed"
	ScheduleServerOfflineNotification NotificationKind = "schedule-server-offline"
	ScheduleWorkerOfflineNotification NotificationKind = "schedule-worker-offline"
)

func (k NotificationKind) Operational() bool {
	for _, v := range OperationalNotificationKinds {
		if k == v {
			return true
		}
	}
	return false
}

type NotificationPreferences struct {
	Situations   *SituationNotificationPreferences `json:"situations,omitempty"`
	Revision     uint64                            `json:"revision,string"`
	Interactions bool                              `json:"interactions"`
	Terminals    bool                              `json:"terminals"`
}
type NotificationCandidate struct {
	MachineID    ID               `json:"machine_id,omitempty"`
	OccurrenceID ID               `json:"occurrence_id,omitempty"`
	AccountID    ID               `json:"account_id,omitempty"`
	InboxID      ID               `json:"inbox_id"`
	SessionID    ID               `json:"session_id"`
	Kind         NotificationKind `json:"kind"`
}
type NotificationDelivery struct {
	NotificationCandidate
	ClaimID ID                `json:"claim_id"`
	State   NotificationState `json:"state"`
}
