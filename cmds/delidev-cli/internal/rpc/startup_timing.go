// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"time"
)

// Every Session read/change/event uses Resource, keeping old strict JSON readers
// independent of this additive envelope. Invalid private metadata never leaks.
func sessionStartupTimingDocument(raw []byte) ([]byte, *pb.SessionStartupTiming) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return raw, nil
	}
	private, exists := fields["startup_timing"]
	if !exists {
		return raw, nil
	}
	delete(fields, "startup_timing")
	public, err := json.Marshal(fields)
	if err != nil {
		return nil, nil
	}
	var timing domain.SessionStartupTiming
	if domain.Decode(private, &timing) != nil {
		return public, nil
	}
	project := func(t *domain.StartupGroupTiming, agent bool) *pb.StartupGroupTiming {
		if !t.Valid() || (agent && t.ExecutionID == "") || (!agent && t.ExecutionID != "") {
			return nil
		}
		result := &pb.StartupGroupTiming{JobId: string(t.JobID), ExecutionId: string(t.ExecutionID), StartedAt: t.StartedAt.Format(time.RFC3339Nano)}
		if t.FinishedAt != nil {
			result.FinishedAt = t.FinishedAt.Format(time.RFC3339Nano)
		}
		return result
	}
	result := &pb.SessionStartupTiming{Workspace: project(timing.Workspace, false), Agent: project(timing.Agent, true)}
	if result.Workspace == nil && result.Agent == nil {
		return public, nil
	}
	return public, result
}
