package domain

// Notification presentation never changes the inbox's independent read state
// or the original request's execution/response authority.
type NotificationKind string
type NotificationState string

const (
	RequestNotification   NotificationKind  = "request"
	SucceededNotification NotificationKind  = "succeeded"
	FailedNotification    NotificationKind  = "failed"
	StoppedNotification   NotificationKind  = "stopped"
	NotificationClaimed   NotificationState = "claimed"
	NotificationSubmitted NotificationState = "submitted"
	NotificationDenied    NotificationState = "denied"
	NotificationFailed    NotificationState = "failed"
	NotificationUncertain NotificationState = "uncertain"
)

func (k NotificationKind) Valid() bool {
	return k == RequestNotification || k == SucceededNotification || k == FailedNotification || k == StoppedNotification
}
func (s NotificationState) Reportable() bool {
	return s == NotificationSubmitted || s == NotificationDenied || s == NotificationFailed || s == NotificationUncertain
}

type NotificationPreferences struct {
	Revision     uint64 `json:"revision,string"`
	Interactions bool   `json:"interactions"`
	Terminals    bool   `json:"terminals"`
}
type NotificationCandidate struct {
	InboxID   ID               `json:"inbox_id"`
	SessionID ID               `json:"session_id"`
	Kind      NotificationKind `json:"kind"`
}
type NotificationDelivery struct {
	NotificationCandidate
	ClaimID ID                `json:"claim_id"`
	State   NotificationState `json:"state"`
}
