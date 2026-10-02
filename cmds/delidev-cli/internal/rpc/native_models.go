// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func nativeModelJobDocument(raw []byte) []byte {
	var job domain.Job
	if domain.Decode(raw, &job) != nil || job.Type != domain.NativeModelsJob {
		return raw
	}
	var input map[string]json.RawMessage
	if domain.Decode(job.Input, &input) != nil {
		return nil
	}
	delete(input, "executable")
	job.Input, _ = json.Marshal(input)
	if len(job.Output) > 0 {
		var observation domain.NativeModelObservation
		if domain.Decode(job.Output, &observation) != nil {
			return nil
		}
		job.Output, _ = json.Marshal(struct {
			Version         uint32 `json:"version"`
			ObservedAt      string `json:"observed_at"`
			ModelCount      int    `json:"model_count"`
			CleanupVerified bool   `json:"cleanup_verified"`
		}{observation.Version, observation.ObservedAt.Format("2006-01-02T15:04:05.999999999Z07:00"), len(observation.Models), observation.CleanupVerified})
	}
	result, _ := json.Marshal(job)
	return result
}

// Only the authenticated original Worker stream receives executable selection
// and complete private job payloads. Public resources share the redacted view.
func WorkerAssignment(record store.Record) *pb.Resource {
	resource := Resource(record)
	if record.Kind == domain.JobKind {
		resource.DocumentJson = record.Data
	}
	return resource
}
