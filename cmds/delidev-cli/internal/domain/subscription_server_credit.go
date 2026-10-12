// SPDX-License-Identifier: Apache-2.0
package domain

import "time"

// Server credit is independent of quota-only and Worker operations. ID and the
// confirmed selector never change; each explicit reconciliation owns an attempt.
type ServerCreditOperation struct {
	AccessOnly           bool                         `json:"access_only,omitempty"`
	Automatic            *ServerAutomaticCreditProof  `json:"automatic,omitempty"`
	ExecuteLease         *ServerCreditExecuteLease    `json:"execute_lease,omitempty"`
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

// ServerCreditExecuteLease binds a manual access-only credit accepted beside
// Execute to that exact lease. FinishGeneration is written only by its
// successful Finish transaction, so later credential rotations cannot inherit
// the claim.
type ServerCreditExecuteLease struct {
	ID               ID `json:"id"`
	OperationID      ID `json:"operation_id"`
	MachineID        ID `json:"machine_id"`
	InstanceID       ID `json:"instance_id"`
	DeviceID         ID `json:"device_id"`
	Epoch            ID `json:"epoch"`
	Generation       ID `json:"generation"`
	FinishGeneration ID `json:"finish_generation,omitempty"`
}

func (p ServerCreditExecuteLease) Validate(generation ID) error {
	for _, id := range []ID{p.ID, p.OperationID, p.MachineID, p.InstanceID, p.DeviceID, p.Epoch, p.Generation} {
		if id.Validate() != nil {
			return InvalidSubscriptionObservation()
		}
	}
	if p.Generation != generation || p.FinishGeneration != "" && p.FinishGeneration.Validate() != nil {
		return InvalidSubscriptionObservation()
	}
	return nil
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
	if o.Automatic != nil && (!o.AccessOnly || o.Automatic.Validate() != nil || o.Automatic.Consent.Actor != o.Actor || o.Automatic.Consent.ConnectionID != o.ConnectionID || o.Automatic.Consent.Generation != o.Generation || o.Automatic.Epoch != o.Epoch) {
		return InvalidSubscriptionObservation()
	}
	if o.ExecuteLease != nil && (!o.AccessOnly || o.Automatic != nil || o.ExecuteLease.Validate(o.Generation) != nil) {
		return InvalidSubscriptionObservation()
	}
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

// Frozen proof binds automatic spending to the original failed Execute and
// consent. A completed Worker finish may clear live consent and rotate its
// generation without revoking the already-admitted server operation.
type ServerAutomaticCreditProof struct {
	EpisodeID     ID                          `json:"episode_id"`
	Epoch         ID                          `json:"epoch"`
	LeaseID       ID                          `json:"lease_id"`
	OperationID   ID                          `json:"operation_id"`
	MachineID     ID                          `json:"machine_id"`
	DeviceID      ID                          `json:"device_id"`
	InstanceID    ID                          `json:"instance_id"`
	LeaseRevision uint64                      `json:"lease_revision"`
	Consent       AutomaticResetCreditConsent `json:"consent"`
	Block         SubscriptionQuotaBlock      `json:"block"`
}

func (p ServerAutomaticCreditProof) Validate() error {
	for _, id := range []ID{p.EpisodeID, p.Epoch, p.LeaseID, p.OperationID, p.MachineID, p.DeviceID, p.InstanceID} {
		if id.Validate() != nil {
			return InvalidSubscriptionObservation()
		}
	}
	if p.LeaseRevision == 0 || p.Consent.Validate() != nil || p.Block.Validate() != nil {
		return InvalidSubscriptionObservation()
	}
	return nil
}
func (s SubscriptionState) AccessOnlyCredit() bool {
	return s.ServerCredit != nil && s.ServerCredit.AccessOnly
}
