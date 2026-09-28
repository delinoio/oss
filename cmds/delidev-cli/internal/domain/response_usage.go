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
type NativeResponseUsage struct {
	ResponseDigest string             `json:"response_digest"`
	Counts         *NativeTokenCounts `json:"counts"`
	CostEvidence   UsageCostEvidence  `json:"cost_evidence"`
}

func (u NativeResponseUsage) Validate() error {
	value, err := hex.DecodeString(u.ResponseDigest)
	if err != nil || len(value) != 32 || hex.EncodeToString(value) != u.ResponseDigest || (u.CostEvidence != UsageCostMissing && u.CostEvidence != UsageCostUnspecified) {
		return invalidObservation()
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
	SessionID    ID                  `json:"session_id"`
	ProjectID    ID                  `json:"project_id,omitempty"`
	ExecutionID  ID                  `json:"execution_id"`
	AccountID    ID                  `json:"account_id"`
	ConnectionID ID                  `json:"connection_id"`
	ProviderID   ID                  `json:"provider_id"`
	ModelID      ID                  `json:"model_id"`
	Harness      Harness             `json:"harness"`
	Version      string              `json:"native_version"`
	ThreadID     string              `json:"native_thread_id"`
	TurnID       string              `json:"native_turn_id"`
	Sequence     uint64              `json:"sequence"`
	Usage        NativeResponseUsage `json:"response"`
}

func (u ResponseUsageRecord) Validate() error {
	for _, id := range []ID{u.SessionID, u.ExecutionID, u.AccountID, u.ConnectionID, u.ProviderID, u.ModelID} {
		if id.Validate() != nil {
			return invalidObservation()
		}
	}
	if (u.ProjectID != "" && u.ProjectID.Validate() != nil) || u.Harness != Codex || u.Version != CodexProtocolVersion || ID(u.ThreadID).Validate() != nil || ID(u.TurnID).Validate() != nil || u.Sequence == 0 || u.Sequence > MaxExecutionEvents {
		return invalidObservation()
	}
	return u.Usage.Validate()
}
