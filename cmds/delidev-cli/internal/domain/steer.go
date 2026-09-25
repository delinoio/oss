package domain

import "time"

const MaxExecutionSteers = 4096

type SteerState string

const (
	SteerQueued    SteerState = "queued"
	SteerClaimed   SteerState = "claimed"
	SteerAccepted  SteerState = "accepted"
	SteerRejected  SteerState = "rejected"
	SteerUncertain SteerState = "uncertain"
	SteerCanceled  SteerState = "canceled"
)

type SteerDelivery string

const (
	SteerNotSent         SteerDelivery = "not-sent"
	SteerNativeAccepted  SteerDelivery = "accepted"
	SteerNativeUncertain SteerDelivery = "uncertain"
)

type SteerEvidence string

const (
	SteerNativeAcknowledgment SteerEvidence = "native-acknowledgment"
	SteerNativeHistory        SteerEvidence = "native-history"
	SteerNativeRejection      SteerEvidence = "native-rejection"
	SteerPreflightRejection   SteerEvidence = "preflight-rejection"
)

// SteerAttempt is server-owned acceptance and delivery state. The prompt stays
// in its original queue record; an attempt never rewrites the immutable session
// configuration or grants ordinary FIFO dispatch of a claimed input.
type SteerAttempt struct {
	Version            uint32                `json:"version"`
	JobID              ID                    `json:"job_id"`
	ExecutionID        ID                    `json:"execution_id"`
	InputID            ID                    `json:"input_id"`
	ContentRevision    uint64                `json:"content_revision"`
	NativeThreadID     ID                    `json:"native_thread_id"`
	NativeTurnID       ID                    `json:"native_turn_id"`
	Mode               SessionMode           `json:"mode"`
	PromptDigest       string                `json:"prompt_digest"`
	State              SteerState            `json:"state"`
	AcceptedAt         time.Time             `json:"accepted_at"`
	Claim              *SteerClaim           `json:"claim,omitempty"`
	Observation        *ExecutionSteerUpdate `json:"observation,omitempty"`
	Sequence           uint64                `json:"sequence,omitempty"`
	Resolution         *ExecutionSteerUpdate `json:"resolution,omitempty"`
	ResolutionSequence uint64                `json:"resolution_sequence,omitempty"`
}

type SteerClaim struct {
	ID         ID        `json:"id"`
	MachineID  ID        `json:"machine_id"`
	InstanceID ID        `json:"instance_id"`
	DeviceID   ID        `json:"device_id"`
	ClaimedAt  time.Time `json:"claimed_at"`
}

type ExecutionSteerUpdate struct {
	SteerID     ID            `json:"steer_id"`
	InputID     ID            `json:"input_id"`
	ClaimID     ID            `json:"claim_id"`
	Delivery    SteerDelivery `json:"delivery"`
	Evidence    SteerEvidence `json:"evidence,omitempty"`
	ProblemCode Code          `json:"problem_code,omitempty"`
}

func (u ExecutionSteerUpdate) Validate() error {
	for _, id := range []ID{u.SteerID, u.InputID, u.ClaimID} {
		if err := id.Validate(); err != nil {
			return err
		}
	}
	valid := false
	switch u.Delivery {
	case SteerNativeAccepted:
		valid = (u.Evidence == SteerNativeAcknowledgment || u.Evidence == SteerNativeHistory) && (u.ProblemCode == "" || u.ProblemCode == RecoveryRequired)
	case SteerNotSent:
		valid = (u.Evidence == SteerNativeRejection || u.Evidence == SteerPreflightRejection) && (u.ProblemCode == Conflict || u.ProblemCode == Unsupported || u.ProblemCode == Canceled || u.ProblemCode == ResourceExhausted || u.ProblemCode == InvalidArgument)
	case SteerNativeUncertain:
		valid = u.Evidence == "" && u.ProblemCode == RecoveryRequired
	}
	if !valid {
		return Fail(InvalidArgument, "Invalid native Steer delivery observation.", "Retain exact claim ownership and one closed delivery/evidence classification without input content.")
	}
	return nil
}
