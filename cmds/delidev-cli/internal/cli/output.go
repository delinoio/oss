// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func emitResult(streams IO, o options, value any, err error) int {
	response := envelope{Version: 1, RequestID: o.requestID, Result: value}
	code := 0
	if err != nil {
		response.Error = domain.SafeError(err)
		code = response.Error.ExitCode()
	}
	if e := json.NewEncoder(streams.Out).Encode(response); e != nil {
		return 1
	}
	return code
}

type envelope struct {
	Version   int           `json:"version"`
	RequestID domain.ID     `json:"request_id,omitempty"`
	Result    any           `json:"result,omitempty"`
	Error     *domain.Error `json:"error,omitempty"`
}

func resourceJSON(r *pb.Resource) any {
	if r == nil {
		return nil
	}
	kind, _ := rpc.Kind(r.Kind)
	return struct {
		ID        string          `json:"id"`
		Kind      domain.Kind     `json:"kind"`
		Revision  uint64          `json:"revision"`
		SessionID string          `json:"session_id,omitempty"`
		ProjectID string          `json:"project_id,omitempty"`
		Data      json.RawMessage `json:"data"`
		CreatedAt string          `json:"created_at"`
		UpdatedAt string          `json:"updated_at"`
	}{r.Id, kind, r.Revision, r.SessionId, r.ProjectId, json.RawMessage(r.DocumentJson), r.CreatedAt, r.UpdatedAt}
}
func resourcesJSON(records []*pb.Resource) []any {
	result := make([]any, 0, len(records))
	for _, r := range records {
		result = append(result, resourceJSON(r))
	}
	return result
}
