package cli

import (
	"encoding/json"
	"testing"

	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestOverviewJSONPreservesObservedZeroAndExactLargeCounts(t *testing.T) {
	raw, err := json.Marshal(overviewOutput(&pb.GetOverviewResponse{ObservedAt: "2026-09-27T00:01:00Z", ActiveSessions: 9007199254740993, TodayFromUnixMs: 1790467200000, TodayUntilUnixMs: 1790467260000}))
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]string
	if json.Unmarshal(raw, &values) != nil || values["active_sessions"] != "9007199254740993" || values["pending_interactions"] != "0" || values["registered_workers"] != "0" || values["connected_workers"] != "0" || values["today_from_unix_ms"] != "1790467200000" || len(values) != 7 {
		t.Fatal("overview lost explicit exact observations", string(raw))
	}
}
