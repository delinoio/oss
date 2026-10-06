package domain

import "encoding/hex"

type UsageCostEvidence string

const (
	UsageCostMissing UsageCostEvidence = "missing"
	// The pinned native amount has no authoritative currency/billing semantics.
	// Its presence cannot become actual API cost or a token-price estimate.
	UsageCostUnspecified UsageCostEvidence = "unspecified-native-metadata"
)

// NativeResponseUsage is one completed native model response, independent of
// context/cumulative counters. Raw upstream response IDs and amounts stay private.
type ResponseUsageSource string

const CompactionHTTPResponse ResponseUsageSource = "compaction-http"

type NativeResponseUsage struct {
	Source         ResponseUsageSource `json:"source,omitempty"`
	ResponseDigest string              `json:"response_digest"`
	Counts         *NativeTokenCounts  `json:"counts"`
	CostEvidence   UsageCostEvidence   `json:"cost_evidence"`
}

type UsagePurpose string

const (
	ConversationUsage UsagePurpose = "conversation"
	SessionTitleUsage UsagePurpose = "session-title"
)

func (u NativeResponseUsage) Validate() error {
	value, err := hex.DecodeString(u.ResponseDigest)
	if err != nil || len(value) != 32 || hex.EncodeToString(value) != u.ResponseDigest || (u.CostEvidence != UsageCostMissing && u.CostEvidence != UsageCostUnspecified) {
		return invalidObservation()
	}
	if u.Source != "" && u.Source != CompactionHTTPResponse {
		return invalidObservation()
	}
	if u.Source == CompactionHTTPResponse {
		if u.Counts == nil {
			return nil
		}
		found := false
		for _, count := range []*int64{u.Counts.Input, u.Counts.Cached, u.Counts.CacheWrite, u.Counts.Output, u.Counts.Reasoning, u.Counts.Total} {
			if count != nil {
				found = true
				if *count < 0 {
					return invalidObservation()
				}
			}
		}
		if !found {
			return invalidObservation()
		}
		return nil
	}
	if u.Counts != nil {
		return (NativeTokenUsage{Total: *u.Counts, Last: *u.Counts}).Validate()
	}
	return nil
}

// ResponseUsageRecord pins event-time execution/account attribution. Sequence
// records first publication; subsequent identical observations do not charge it
// again. Nullable counts/cost evidence cannot become measured zero or spend.
type ResponseUsageRecord struct {
	// A manual context action has no ordinary input or native turn binding at
	// the relay boundary. Preserve its original source turn separately; an
	// unavailable action turn must never borrow the preceding native turn.
	CompactionSourceTurn NativeIdentity      `json:"compaction_source_turn,omitempty"`
	Purpose              UsagePurpose        `json:"purpose,omitempty"`
	SessionID            ID                  `json:"session_id"`
	ProjectID            ID                  `json:"project_id,omitempty"`
	ExecutionID          ID                  `json:"execution_id"`
	AccountID            ID                  `json:"account_id"`
	ConnectionID         ID                  `json:"connection_id"`
	ProviderID           ID                  `json:"provider_id,omitempty"`
	SubscriptionService  SubscriptionService `json:"subscription_service,omitempty"`
	ModelID              ID                  `json:"model_id"`
	Harness              Harness             `json:"harness"`
	Version              string              `json:"native_version"`
	ThreadID             string              `json:"native_thread_id"`
	TurnID               string              `json:"native_turn_id"`
	Sequence             uint64              `json:"sequence"`
	Usage                NativeResponseUsage `json:"response"`
}

func (u ResponseUsageRecord) Validate() error {
	for _, id := range []ID{u.SessionID, u.ExecutionID, u.AccountID, u.ConnectionID, u.ModelID} {
		if id.Validate() != nil {
			return invalidObservation()
		}
	}
	if u.SubscriptionService == "" {
		if u.ProviderID.Validate() != nil {
			return invalidObservation()
		}
	} else if u.SubscriptionService != SubscriptionChatGPT || u.ProviderID != "" || u.Purpose == SessionTitleUsage {
		return invalidObservation()
	}
	if (u.Purpose != "" && u.Purpose != ConversationUsage && u.Purpose != SessionTitleUsage) || (u.ProjectID != "" && u.ProjectID.Validate() != nil) || u.Harness != Codex || !CodexVersionAllowed(u.Version) || ID(u.ThreadID).Validate() != nil || (u.CompactionSourceTurn == "" && ID(u.TurnID).Validate() != nil) || u.Sequence == 0 || u.Sequence > MaxExecutionEvents {
		return invalidObservation()
	}
	if (u.CompactionSourceTurn != "") != (u.Usage.Source == CompactionHTTPResponse) {
		return invalidObservation()
	}
	if u.CompactionSourceTurn != "" && (u.CompactionSourceTurn.Validate(Codex, NativeTurnIdentity) != nil || u.TurnID != "" || u.Purpose == SessionTitleUsage) {
		return invalidObservation()
	}
	return u.Usage.Validate()
}
