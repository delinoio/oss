// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"slices"
	"time"
)

// Consent belongs to one authenticated actor and immutable account generation.
// A retained consent for an older generation never authorizes spending.
type AutomaticResetCreditConsent struct {
	Actor        Principal `json:"actor"`
	ConnectionID ID        `json:"connection_id"`
	Generation   ID        `json:"generation"`
	ConfirmedAt  time.Time `json:"confirmed_at"`
}

type CodexQuotaBlock string

const (
	CodexUsageLimitExceeded CodexQuotaBlock = "usageLimitExceeded"
	CodexRateLimitExceeded  CodexQuotaBlock = "rateLimitExceeded"
)

func (b CodexQuotaBlock) Valid() bool {
	return b == CodexUsageLimitExceeded || b == CodexRateLimitExceeded
}

// The episode is recorded even when inventory or native ownership prevents
// admission. Time passing, restarts and repeated failures do not rearm it.
type AutomaticResetCreditEpisode struct {
	ID                 ID              `json:"id"`
	RefreshOperationID ID              `json:"refresh_operation_id,omitempty"`
	SessionID          ID              `json:"session_id,omitempty"`
	LeaseID            ID              `json:"lease_id,omitempty"`
	Generation         ID              `json:"generation"`
	ExecutionID        ID              `json:"execution_id"`
	NativeThreadID     NativeIdentity  `json:"native_thread_id"`
	NativeTurnID       NativeIdentity  `json:"native_turn_id"`
	Block              CodexQuotaBlock `json:"block"`
	ObservedAt         time.Time       `json:"observed_at"`
	OperationID        ID              `json:"operation_id,omitempty"`
}

func (c AutomaticResetCreditConsent) Validate() error {
	if c.ConnectionID.Validate() != nil || c.Generation.Validate() != nil || c.ConfirmedAt.Unix() <= 0 || c.ConfirmedAt.Year() > 9999 || (c.Actor.Type != OwnerDevice && c.Actor.Type != ClientDevice) || c.Actor.Type == ClientDevice && c.Actor.DeviceID.Validate() != nil {
		return InvalidSubscriptionObservation()
	}
	return nil
}
func (e AutomaticResetCreditEpisode) Validate() error {
	if e.ID.Validate() != nil || e.Generation.Validate() != nil || e.ExecutionID.Validate() != nil || e.NativeThreadID.Validate(Codex, NativeThreadIdentity) != nil || e.NativeTurnID.Validate(Codex, NativeTurnIdentity) != nil || !e.Block.Valid() || e.ObservedAt.Unix() <= 0 || e.ObservedAt.Year() > 9999 || e.OperationID != "" && e.OperationID.Validate() != nil || e.RefreshOperationID != "" && e.RefreshOperationID.Validate() != nil || e.SessionID != "" && e.SessionID.Validate() != nil || e.LeaseID != "" && e.LeaseID.Validate() != nil {
		return InvalidSubscriptionObservation()
	}
	return nil
}

// Selection sorts a copy. The original inventory retains its display order.
func SelectAutomaticResetCredit(v *SubscriptionResetCredits, now time.Time) (string, bool) {
	if v == nil || v.Validate() != nil || v.ObservedAt.After(now) || now.Sub(v.ObservedAt) > 5*time.Minute || v.AvailableCount <= 0 {
		return "", false
	}
	if v.Credits == nil {
		return "", true
	}
	eligible := []SubscriptionResetCreditDetail{}
	for _, c := range *v.Credits {
		if c.Status == SubscriptionCreditAvailable && c.ResetType == CodexRateLimitsReset && !c.GrantedAt.After(now) && (c.ExpiresAt == nil || c.ExpiresAt.After(now)) {
			eligible = append(eligible, c)
		}
	}
	slices.SortFunc(eligible, func(a, b SubscriptionResetCreditDetail) int {
		if a.ExpiresAt == nil && b.ExpiresAt != nil {
			return 1
		}
		if a.ExpiresAt != nil && b.ExpiresAt == nil {
			return -1
		}
		if a.ExpiresAt != nil {
			if v := a.ExpiresAt.Compare(*b.ExpiresAt); v != 0 {
				return v
			}
		}
		if v := a.GrantedAt.Compare(b.GrantedAt); v != 0 {
			return v
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	if len(eligible) == 0 {
		return "", false
	}
	return eligible[0].ID, true
}

// QuotaBlock is emitted only from the typed failed original Codex turn. It
// carries no native diagnostic text and cannot authorize another execution.
type SubscriptionQuotaBlock struct {
	SessionID      ID              `json:"session_id"`
	ExecutionID    ID              `json:"execution_id"`
	NativeThreadID NativeIdentity  `json:"native_thread_id"`
	NativeTurnID   NativeIdentity  `json:"native_turn_id"`
	Reason         CodexQuotaBlock `json:"reason"`
}

func (b SubscriptionQuotaBlock) Validate() error {
	if b.SessionID.Validate() != nil || b.ExecutionID.Validate() != nil || b.NativeThreadID.Validate(Codex, NativeThreadIdentity) != nil || b.NativeTurnID.Validate(Codex, NativeTurnIdentity) != nil || !b.Reason.Valid() {
		return InvalidSubscriptionObservation()
	}
	return nil
}
