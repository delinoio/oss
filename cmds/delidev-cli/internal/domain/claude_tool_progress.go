package domain

import (
	"encoding/json"
	"math"
	"strconv"
)

// Preserve the original native number spelling as inert text. Elapsed time is
// advisory, never usage, a timeout decision or proof that a tool has finished.
type ClaudeToolProgressObservation struct {
	Tool           ClaudeToolReference `json:"tool"`
	ParentToolID   *string             `json:"parent_tool_use_id"`
	ElapsedSeconds string              `json:"elapsed_time_seconds"`
	Heartbeat      *bool               `json:"heartbeat"`
}

func (v ClaudeToolProgressObservation) Validate() error {
	if v.Tool.Validate() != nil || v.ParentToolID != nil || len(v.ElapsedSeconds) == 0 || len(v.ElapsedSeconds) > 64 || v.ElapsedSeconds[0] < '0' || v.ElapsedSeconds[0] > '9' || !json.Valid([]byte(v.ElapsedSeconds)) {
		return invalidClaudeProgress()
	}
	seconds, err := strconv.ParseFloat(v.ElapsedSeconds, 64)
	if err != nil || seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return invalidClaudeProgress()
	}
	return nil
}

type ClaudeToolSummaryObservation struct {
	Summary string                `json:"summary"`
	Tools   []ClaudeToolReference `json:"preceding_tools"`
}

func (v ClaudeToolSummaryObservation) Validate() error {
	if Text(v.Summary, "native tool summary", MaxMessageText, false) != nil || len(v.Tools) == 0 || len(v.Tools) > 128 {
		return invalidClaudeProgress()
	}
	ids, native := map[ID]bool{}, map[string]bool{}
	for _, tool := range v.Tools {
		if tool.Validate() != nil || ids[tool.ID] || native[tool.NativeID] {
			return invalidClaudeProgress()
		}
		ids[tool.ID], native[tool.NativeID] = true, true
	}
	return nil
}
