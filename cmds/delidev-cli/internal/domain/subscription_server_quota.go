// SPDX-License-Identifier: Apache-2.0
package domain

import "time"

// Server quota owns a distinct credential fence. It cannot represent a Worker
// claim, reset-credit consumption, login or execution authority.
type ServerQuotaOperation struct {
	// AccessOnly separates V2 observations from historical managed-auth owners.
	AccessOnly       bool                         `json:"access_only,omitempty"`
	ID               ID                           `json:"id"`
	Epoch            ID                           `json:"epoch"`
	FinishID         ID                           `json:"finish_id"`
	ConnectionID     ID                           `json:"connection_id"`
	Generation       ID                           `json:"generation"`
	Actor            Principal                    `json:"actor"`
	Phase            SubscriptionObservationPhase `json:"phase"`
	RequestedAt      time.Time                    `json:"requested_at"`
	CleanupConfirmed bool                         `json:"cleanup_confirmed"`
	ErrorCode        Code                         `json:"error_code,omitempty"`
}

func (o ServerQuotaOperation) Active() bool {
	return o.Phase == SubscriptionObservationQueued || o.Phase == SubscriptionObservationSending || o.Phase == SubscriptionObservationUncertain
}
func (s SubscriptionState) ServerQuotaActive() bool {
	return s.ServerQuota != nil && s.ServerQuota.Active()
}
func (o ServerQuotaOperation) Validate() error {
	for _, id := range []ID{o.ID, o.Epoch, o.FinishID, o.ConnectionID, o.Generation} {
		if id.Validate() != nil {
			return InvalidSubscriptionObservation()
		}
	}
	if o.RequestedAt.IsZero() || !ValidSubscriptionObservationCode(o.ErrorCode) || o.Actor.Type != OwnerDevice && o.Actor.Type != ClientDevice || o.Actor.Type == ClientDevice && o.Actor.DeviceID.Validate() != nil {
		return InvalidSubscriptionObservation()
	}
	switch o.Phase {
	case SubscriptionObservationQueued, SubscriptionObservationSending, SubscriptionObservationSucceeded, SubscriptionObservationFailed, SubscriptionObservationUncertain:
	default:
		return InvalidSubscriptionObservation()
	}
	if o.Phase == SubscriptionObservationQueued && o.CleanupConfirmed || o.Phase == SubscriptionObservationSucceeded && !o.CleanupConfirmed {
		return InvalidSubscriptionObservation()
	}
	return nil
}

// AccessOnlyQuota is independent of the credential writer but retains deletion fences.
func (s SubscriptionState) AccessOnlyQuota() bool {
	return s.ServerQuota != nil && s.ServerQuota.AccessOnly
}

func (s SubscriptionState) ExclusiveServerObservationActive() bool {
	return s.ServerCreditActive() || s.ServerQuotaActive() && !s.AccessOnlyQuota()
}
