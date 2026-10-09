// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"regexp"
	"slices"
	"time"
)

type SubscriptionObservationPhase string

const (
	SubscriptionObservationQueued           SubscriptionObservationPhase = "queued"
	SubscriptionObservationSending          SubscriptionObservationPhase = "sending"
	SubscriptionObservationSucceeded        SubscriptionObservationPhase = "succeeded"
	SubscriptionObservationFailed           SubscriptionObservationPhase = "failed"
	SubscriptionObservationUncertain        SubscriptionObservationPhase = "uncertain"
	SubscriptionObservationRetiredUncertain SubscriptionObservationPhase = "retired-uncertain"
)

type SubscriptionResetOutcome string

const (
	SubscriptionReset           SubscriptionResetOutcome = "reset"
	SubscriptionAlreadyRedeemed SubscriptionResetOutcome = "alreadyRedeemed"
	SubscriptionNothingToReset  SubscriptionResetOutcome = "nothingToReset"
	SubscriptionNoCredit        SubscriptionResetOutcome = "noCredit"
)

func (v SubscriptionResetOutcome) Valid() bool {
	return slices.Contains([]SubscriptionResetOutcome{SubscriptionReset, SubscriptionAlreadyRedeemed, SubscriptionNothingToReset, SubscriptionNoCredit}, v)
}

// One durable logical attempt. ID is the official reset idempotency key and
// never changes during explicit reconciliation. Presentation grants no lease.
type SubscriptionObservationOperation struct {
	ID                   ID                           `json:"id"`
	Action               SubscriptionAction           `json:"action"`
	MachineID            ID                           `json:"machine_id"`
	Actor                Principal                    `json:"actor"`
	ConnectionID         ID                           `json:"connection_id"`
	Generation           ID                           `json:"generation"`
	Phase                SubscriptionObservationPhase `json:"phase"`
	RequestedAt          time.Time                    `json:"requested_at"`
	CreditID             string                       `json:"credit_id,omitempty"`
	NextCredit           bool                         `json:"next_credit,omitempty"`
	CreditsObservationID ID                           `json:"credits_observation_id,omitempty"`
	Outcome              SubscriptionResetOutcome     `json:"outcome,omitempty"`
	ErrorCode            Code                         `json:"error_code,omitempty"`
}

func (v SubscriptionObservationOperation) Active() bool {
	return v.Phase == SubscriptionObservationQueued || v.Phase == SubscriptionObservationSending || v.Phase == SubscriptionObservationUncertain
}
func (v SubscriptionObservationOperation) Validate() error {
	for _, id := range []ID{v.ID, v.MachineID, v.ConnectionID, v.Generation} {
		if id.Validate() != nil {
			return InvalidSubscriptionObservation()
		}
	}
	if !ValidSubscriptionObservationCode(v.ErrorCode) || v.RequestedAt.IsZero() || !slices.Contains([]SubscriptionObservationPhase{SubscriptionObservationQueued, SubscriptionObservationSending, SubscriptionObservationSucceeded, SubscriptionObservationFailed, SubscriptionObservationUncertain, SubscriptionObservationRetiredUncertain}, v.Phase) || (v.Actor.Type != OwnerDevice && v.Actor.Type != ClientDevice) || (v.Actor.Type == ClientDevice && v.Actor.DeviceID.Validate() != nil) {
		return InvalidSubscriptionObservation()
	}
	if v.Action == SubscriptionQuota {
		if v.CreditID != "" || v.NextCredit || v.CreditsObservationID != "" || v.Outcome != "" {
			return InvalidSubscriptionObservation()
		}
	} else if v.Action == SubscriptionResetCredit {
		if v.CreditsObservationID.Validate() != nil || v.NextCredit == (v.CreditID != "") || (v.CreditID != "" && !ValidSubscriptionOpaqueID(v.CreditID)) || (v.Outcome != "" && !v.Outcome.Valid()) {
			return InvalidSubscriptionObservation()
		}
	} else {
		return InvalidSubscriptionObservation()
	}
	return nil
}

var subscriptionOpaqueID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

func ValidSubscriptionOpaqueID(value string) bool { return subscriptionOpaqueID.MatchString(value) }

type SubscriptionQuotaWindow struct {
	ID              string     `json:"id"`
	Remaining       *float64   `json:"remaining,omitempty"`
	DurationMinutes *int64     `json:"duration_minutes,omitempty"`
	ResetAt         *time.Time `json:"reset_at,omitempty"`
}
type SubscriptionResetType string
type SubscriptionCreditStatus string

const (
	CodexRateLimitsReset        SubscriptionResetType    = "codexRateLimits"
	UnknownSubscriptionReset    SubscriptionResetType    = "unknown"
	SubscriptionCreditAvailable SubscriptionCreditStatus = "available"
	SubscriptionCreditRedeeming SubscriptionCreditStatus = "redeeming"
	SubscriptionCreditRedeemed  SubscriptionCreditStatus = "redeemed"
	SubscriptionCreditUnknown   SubscriptionCreditStatus = "unknown"
)

type SubscriptionResetCreditDetail struct {
	ID        string                   `json:"id"`
	ResetType SubscriptionResetType    `json:"reset_type"`
	Status    SubscriptionCreditStatus `json:"status"`
	GrantedAt time.Time                `json:"granted_at"`
	ExpiresAt *time.Time               `json:"expires_at,omitempty"`
}
type SubscriptionResetCredits struct {
	ObservationID  ID        `json:"observation_id"`
	ObservedAt     time.Time `json:"observed_at"`
	AvailableCount int64     `json:"available_count,string"`
	// nil means unavailable details; [] means an observed empty detail list.
	Credits *[]SubscriptionResetCreditDetail `json:"credits"`
}

func (v SubscriptionResetCredits) Validate() error {
	if v.ObservationID.Validate() != nil || v.ObservedAt.IsZero() || v.AvailableCount < 0 || v.Credits != nil && len(*v.Credits) > 100 {
		return InvalidSubscriptionObservation()
	}
	seen := map[string]bool{}
	if v.Credits != nil {
		for _, c := range *v.Credits {
			if !ValidSubscriptionOpaqueID(c.ID) || seen[c.ID] || !slices.Contains([]SubscriptionResetType{CodexRateLimitsReset, UnknownSubscriptionReset}, c.ResetType) || !slices.Contains([]SubscriptionCreditStatus{SubscriptionCreditAvailable, SubscriptionCreditRedeeming, SubscriptionCreditRedeemed, SubscriptionCreditUnknown}, c.Status) || (c.GrantedAt.Unix() <= 0 || c.GrantedAt.Year() > 9999) || c.ExpiresAt != nil && (c.ExpiresAt.Before(c.GrantedAt) || c.ExpiresAt.Year() > 9999) {
				return InvalidSubscriptionObservation()
			}
			seen[c.ID] = true
		}
	}
	return nil
}

// The Worker projects only bounded quota metadata, never native bodies, display
// names, credit titles, unrelated billing text, provider identity or credentials.
type SubscriptionQuotaObservation struct {
	PaidCredits         []SubscriptionPaidCreditBucket `json:"paid_credits,omitempty"`
	ObservedAt          time.Time                      `json:"observed_at"`
	Windows             []SubscriptionQuotaWindow      `json:"windows"`
	SpendControlReached *bool                          `json:"spend_control_reached,omitempty"`
	Credits             *SubscriptionResetCredits      `json:"credits,omitempty"`
}
type SubscriptionObservationResult struct {
	Quota            *SubscriptionQuotaObservation `json:"quota,omitempty"`
	QuotaError       Code                          `json:"quota_error,omitempty"`
	Outcome          SubscriptionResetOutcome      `json:"outcome,omitempty"`
	ConsumeUncertain bool                          `json:"consume_uncertain,omitempty"`
}

func InvalidSubscriptionObservation() *Error {
	return Fail(InvalidArgument, "Invalid native subscription observation.", "Use the original authenticated native owner and bounded quota/reset-credit metadata.")
}

func (v SubscriptionQuotaObservation) Validate(now time.Time) error {
	if v.ObservedAt.IsZero() || v.ObservedAt.After(now.Add(time.Second)) || now.Sub(v.ObservedAt) > time.Minute || len(v.Windows) > 64 {
		return InvalidSubscriptionObservation()
	}
	if ValidatePaidCreditBuckets(v.PaidCredits) != nil {
		return InvalidSubscriptionObservation()
	}
	for _, b := range v.PaidCredits {
		if !b.ObservedAt.Equal(v.ObservedAt) {
			return InvalidSubscriptionObservation()
		}
	}
	seen := map[string]bool{}
	for _, w := range v.Windows {
		if !ValidSubscriptionOpaqueID(w.ID) || seen[w.ID] || w.Remaining != nil && !(*w.Remaining >= 0 && *w.Remaining <= 1) || w.DurationMinutes != nil && (*w.DurationMinutes <= 0 || *w.DurationMinutes > 5256000) || w.ResetAt != nil && (w.ResetAt.Unix() <= 0 || w.ResetAt.Year() > 9999) {
			return InvalidSubscriptionObservation()
		}
		seen[w.ID] = true
	}
	if v.Credits != nil && (v.Credits.Validate() != nil || !v.Credits.ObservedAt.Equal(v.ObservedAt)) {
		return InvalidSubscriptionObservation()
	}
	return nil
}

// Merge sparse fields independently. Missing windows never refresh retained
// remaining evidence. Reset time passing alone cannot clear exhaustion.
func ApplySubscriptionQuota(a *Account, v SubscriptionQuotaObservation, now time.Time) (bool, error) {
	if a.Type != SubscriptionAccount || a.SubscriptionService != SubscriptionChatGPT || a.Subscription == nil || v.Validate(now) != nil {
		return false, InvalidSubscriptionObservation()
	}
	state := a.Subscription
	if state.QuotaObservedAt != nil && v.ObservedAt.Before(*state.QuotaObservedAt) {
		return false, nil
	}
	if err := mergePaidCredits(state, v.PaidCredits); err != nil {
		return false, err
	}
	wasExhausted := a.ConfirmedExhausted
	freshPositive := v.SpendControlReached != nil && !*v.SpendControlReached
	for _, incoming := range v.Windows {
		index := slices.IndexFunc(a.Quota, func(w QuotaWindow) bool { return w.ID == incoming.ID })
		if index < 0 {
			if len(a.Quota) >= 64 {
				return false, InvalidSubscriptionObservation()
			}
			a.Quota = append(a.Quota, QuotaWindow{ID: incoming.ID, ComparisonGroup: "chatgpt", Blocking: true, State: ObservationUnknown})
			index = len(a.Quota) - 1
		}
		w := &a.Quota[index]
		if incoming.Remaining != nil {
			value := *incoming.Remaining
			freshPositive = freshPositive || value > 0
			w.Remaining = &value
			w.ObservedAt = v.ObservedAt
			w.State = Observed
		}
		if incoming.DurationMinutes != nil {
			value := *incoming.DurationMinutes
			w.DurationMinutes = &value
		}
		if incoming.ResetAt != nil {
			value := *incoming.ResetAt
			w.ResetAt = &value
		}
	}
	if v.SpendControlReached != nil {
		value := *v.SpendControlReached
		state.SpendControlReached = &value
		state.SpendControlObservedAt = &v.ObservedAt
	}
	if v.Credits != nil {
		state.ResetCredits = v.Credits
	}
	state.QuotaObservedAt = &v.ObservedAt
	state.QuotaState = Observed
	blocked := state.SpendControlReached != nil && *state.SpendControlReached
	for _, w := range a.Quota {
		if w.Blocking && w.Remaining != nil && *w.Remaining == 0 {
			blocked = true
		}
	}
	if blocked {
		a.ConfirmedExhausted = true
	} else {
		evidence, score, _, _ := quotaEvidence(a.Quota, now)
		spendFresh := state.SpendControlReached == nil || state.SpendControlObservedAt != nil && now.Sub(*state.SpendControlObservedAt) <= 5*time.Minute
		if freshPositive && evidence == Observed && score != nil && *score > 0 && spendFresh {
			a.ConfirmedExhausted = false
		}
	}
	return wasExhausted && !a.ConfirmedExhausted && a.RecoveryNotifications, nil
}

func ValidSubscriptionObservationCode(v Code) bool {
	return v == "" || slices.Contains([]Code{Conflict, InvalidArgument, Unauthenticated, PermissionDenied, Unavailable, ServerUnavailable, Unsupported, RecoveryRequired, ResourceExhausted, Canceled, Internal}, v)
}
