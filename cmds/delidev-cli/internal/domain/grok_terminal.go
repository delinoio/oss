package domain

import (
	"encoding/hex"
)

type GrokTerminalKind string

const GrokClosedFirstText GrokTerminalKind = "closed-first-text"

// This closed profile represents the matching original end_turn RPC/turn/prompt
// facts, original native close and independently compared first-text history.
// Its counters are one native input's report, not billable response aggregation.
type GrokTextTerminal struct {
	User          *GrokUserHistory   `json:"user,omitempty"`
	Kind          GrokTerminalKind   `json:"kind"`
	NativeEventID string             `json:"native_event_id"`
	TimestampMS   string             `json:"timestamp_ms"`
	ElapsedMS     string             `json:"elapsed_ms"`
	Model         string             `json:"model"`
	Counts        GrokResponseCounts `json:"counts"`
	TotalTokens   string             `json:"total_tokens"`
	ModelCalls    string             `json:"model_calls"`
	APIDurationMS string             `json:"api_duration_ms"`
	Turns         string             `json:"turns"`
	ClosureID     ID                 `json:"closure_id"`
	HistoryDigest string             `json:"history_digest"`
}

func (v GrokTextTerminal) Validate(thread string) error {
	if v.Kind != GrokClosedFirstText || Text(v.Model, "original Grok model", 256, true) != nil || v.ClosureID.Validate() != nil || v.ModelCalls != "1" || v.Turns != "1" {
		return invalidGrokContent()
	}
	if _, err := GrokEventIndex(v.NativeEventID, thread); err != nil {
		return err
	}
	if n, ok := grokCount(v.TimestampMS); !ok || n > 253402300799999 {
		return invalidGrokContent()
	}
	for _, s := range []string{v.ElapsedMS, v.TotalTokens, v.APIDurationMS} {
		if _, ok := grokCount(s); !ok {
			return invalidGrokContent()
		}
	}
	if (GrokResponseUsage{Ordinal: 1, Counts: v.Counts}).Validate() != nil {
		return invalidGrokContent()
	}
	hash, err := hex.DecodeString(v.HistoryDigest)
	if err != nil || len(hash) != 32 || hex.EncodeToString(hash) != v.HistoryDigest {
		return invalidGrokContent()
	}
	if v.User != nil {
		user, e1 := GrokEventIndex(v.User.NativeEventID, thread)
		terminal, e2 := GrokEventIndex(v.NativeEventID, thread)
		if v.User.Validate(thread) != nil || v.User.Model != v.Model || e1 != nil || e2 != nil || user >= terminal {
			return invalidGrokContent()
		}
	}
	return nil
}
