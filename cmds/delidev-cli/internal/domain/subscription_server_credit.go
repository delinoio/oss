// SPDX-License-Identifier: Apache-2.0
package domain

import "time"

// Server credit is independent of quota-only and Worker operations. ID and the
// confirmed selector never change; each explicit reconciliation owns an attempt.
type ServerCreditOperation struct {
	ID                   ID                           `json:"id"`
	Epoch                ID                           `json:"epoch"`
	AttemptID            ID                           `json:"attempt_id"`
	AttemptEpoch         ID                           `json:"attempt_epoch"`
	FinishID             ID                           `json:"finish_id"`
	ConnectionID         ID                           `json:"connection_id"`
	Generation           ID                           `json:"generation"`
	Actor                Principal                    `json:"actor"`
	Phase                SubscriptionObservationPhase `json:"phase"`
	RequestedAt          time.Time                    `json:"requested_at"`
	CreditID             string                       `json:"credit_id,omitempty"`
	NextCredit           bool                         `json:"next_credit,omitempty"`
	CreditsObservationID ID                           `json:"credits_observation_id"`
	EverSent             bool                         `json:"ever_sent"`
	SendClaimed          bool                         `json:"send_claimed"`
	CleanupConfirmed     bool                         `json:"cleanup_confirmed"`
	Outcome              SubscriptionResetOutcome     `json:"outcome,omitempty"`
	ErrorCode            Code                         `json:"error_code,omitempty"`
	QuotaErrorCode       Code                         `json:"quota_error_code,omitempty"`
}

func (o ServerCreditOperation) Active() bool {
	return o.Phase == SubscriptionObservationQueued || o.Phase == SubscriptionObservationSending || o.Phase == SubscriptionObservationUncertain
}
func (s SubscriptionState) ServerCreditActive() bool {
	return s.ServerCredit != nil && s.ServerCredit.Active()
}
func (s SubscriptionState) ServerObservationActive() bool {
	return s.ServerQuotaActive() || s.ServerCreditActive()
}
func (o ServerCreditOperation) Validate() error {
	for _, id := range []ID{o.ID, o.Epoch, o.AttemptID, o.AttemptEpoch, o.FinishID, o.ConnectionID, o.Generation, o.CreditsObservationID} {
		if id.Validate() != nil {
			return InvalidSubscriptionObservation()
		}
	}
	if o.RequestedAt.IsZero() || o.Actor.Type != OwnerDevice && o.Actor.Type != ClientDevice || o.Actor.Type == ClientDevice && o.Actor.DeviceID.Validate() != nil || o.NextCredit == (o.CreditID != "") || o.CreditID != "" && !ValidSubscriptionOpaqueID(o.CreditID) || o.Outcome != "" && !o.Outcome.Valid() || !ValidSubscriptionObservationCode(o.ErrorCode) || !ValidSubscriptionObservationCode(o.QuotaErrorCode) {
		return InvalidSubscriptionObservation()
	}
	switch o.Phase {
	case SubscriptionObservationQueued:
		if o.SendClaimed || o.CleanupConfirmed || o.Outcome != "" {
			return InvalidSubscriptionObservation()
		}
	case SubscriptionObservationSending, SubscriptionObservationUncertain:
	case SubscriptionObservationSucceeded:
		if !o.EverSent || !o.CleanupConfirmed || !o.Outcome.Valid() {
			return InvalidSubscriptionObservation()
		}
	case SubscriptionObservationFailed:
		if !o.CleanupConfirmed || o.EverSent {
			return InvalidSubscriptionObservation()
		}
	default:
		return InvalidSubscriptionObservation()
	}
	if (o.Outcome != "" || o.SendClaimed) && !o.EverSent {
		return InvalidSubscriptionObservation()
	}
	return nil
}
