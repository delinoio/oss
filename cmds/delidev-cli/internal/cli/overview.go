package cli

import pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"

type overviewResult struct {
	ObservedAt          string `json:"observed_at"`
	ActiveSessions      uint64 `json:"active_sessions,string"`
	PendingInteractions uint64 `json:"pending_interactions,string"`
	RegisteredWorkers   uint64 `json:"registered_workers,string"`
	ConnectedWorkers    uint64 `json:"connected_workers,string"`
	TodayFromUnixMS     int64  `json:"today_from_unix_ms,string"`
	TodayUntilUnixMS    int64  `json:"today_until_unix_ms,string"`
}

func overviewOutput(value *pb.GetOverviewResponse) overviewResult {
	// Ordinary CLI JSON uses encoding/json, not protobuf JSON. Preserve explicit
	// measured zeros and exact 64-bit values in the same decimal-string form.
	return overviewResult{value.ObservedAt, value.ActiveSessions, value.PendingInteractions, value.RegisteredWorkers, value.ConnectedWorkers, value.TodayFromUnixMs, value.TodayUntilUnixMs}
}
