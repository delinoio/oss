package domain

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// Preserve the original native number spelling as inert text. Elapsed time is
// advisory, never usage, a timeout decision or proof that a tool has finished.
type ClaudeToolProgressObservation struct {
	Tool           ClaudeToolReference `json:"tool"`
	ParentToolID   *string             `json:"parent_tool_use_id"`
	NativeToolID   string              `json:"tool_use_id,omitempty"`
	ElapsedSeconds string              `json:"elapsed_time_seconds"`
	TaskID         *string             `json:"task_id,omitempty"`
	Heartbeat      *bool               `json:"heartbeat"`
}

func (v ClaudeToolProgressObservation) Validate() error {
	if v.Tool.Validate() != nil || v.TaskID != nil && Text(*v.TaskID, "native task identity", 1024, true) != nil || len(v.ElapsedSeconds) == 0 || len(v.ElapsedSeconds) > 64 || v.ElapsedSeconds[0] < '0' || v.ElapsedSeconds[0] > '9' || !json.Valid([]byte(v.ElapsedSeconds)) {
		return invalidClaudeProgress()
	}
	if v.ParentToolID == nil {
		if v.NativeToolID != "" {
			return invalidClaudeProgress()
		}
	} else if *v.ParentToolID != v.Tool.NativeID || v.Heartbeat == nil || !*v.Heartbeat || v.TaskID != nil || !ValidClaudeToolHeartbeat(v.Tool.NativeID, v.NativeToolID) {
		return invalidClaudeProgress()
	}
	seconds, err := strconv.ParseFloat(v.ElapsedSeconds, 64)
	if err != nil || seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return invalidClaudeProgress()
	}
	return nil
}

// Claude 2.1.236 reports a heartbeat's own progress identity separately from
// the original tool, which is its parent. This pinned wire profile does not
// grant child-tool or task ownership; callers must verify the original tool.
func ValidClaudeToolHeartbeat(owner, progress string) bool {
	if Text(owner, "native heartbeat owner", 1024, true) != nil || Text(progress, "native heartbeat identity", 1024, true) != nil {
		return false
	}
	index, ok := strings.CutPrefix(progress, owner+"-heartbeat-")
	if !ok {
		return false
	}
	n, err := strconv.ParseUint(index, 10, 32)
	return err == nil && strconv.FormatUint(n, 10) == index
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
