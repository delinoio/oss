// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type workerModelSelection struct {
	ModelID       domain.ID `json:"model_id,omitempty"`
	NativeID      string    `json:"native_id,omitempty"`
	ModelRevision uint64    `json:"model_revision"`
}

func agentWorkerRequest(mutation *pb.Mutation, body []byte, revision uint64, nativeID, routeFile string) (*pb.SaveAgentWorkerRequest, error) {
	var agent domain.Agent
	if err := domain.Decode(body, &agent); err != nil {
		return nil, err
	}
	result := &pb.SaveAgentWorkerRequest{Mutation: mutation, DocumentJson: body, SchemaVersion: rpc.ResourceSchemaVersion(domain.AgentKind, body)}
	selections := []workerModelSelection{{ModelID: agent.ModelID, NativeID: nativeID, ModelRevision: revision}}
	if nativeID != "" {
		selections[0].ModelID = ""
	}
	if len(agent.Routes) > 0 {
		if routeFile == "" || routeFile == "-" || revision != 0 || nativeID != "" {
			return nil, domain.Fail(domain.MissingInput, "Ordered sources require model selections.", "Pass --route-models-file PATH without single-source model flags or shared stdin.")
		}
		raw, err := readDocument(routeFile, nil)
		if err != nil {
			return nil, err
		}
		if err := domain.Decode(raw, &selections); err != nil {
			return nil, err
		}
		if len(selections) != len(agent.Routes) {
			return nil, domain.Fail(domain.InvalidArgument, "Model selections do not match the source routes.", "Provide exactly one selection per source in route order.")
		}
	} else if routeFile != "" {
		return nil, domain.Fail(domain.InvalidArgument, "Single-source configuration cannot use route model selections.", "Use --model-revision or --native-model-id.")
	}
	for index, selection := range selections {
		wire := &pb.AgentWorkerModelSelection{ExpectedModelRevision: selection.ModelRevision}
		if selection.NativeID != "" {
			if selection.ModelID != "" || selection.ModelRevision != 0 {
				return nil, domain.Fail(domain.InvalidArgument, "Native model selection has no canonical revision.", "Specify only native_id for an exact native selection.")
			}
			wire.Selection = &pb.AgentWorkerModelSelection_NativeId{NativeId: selection.NativeID}
		} else {
			if selection.ModelRevision == 0 {
				return nil, domain.Fail(domain.MissingInput, "Canonical model selection requires its revision.", "Provide --model-revision, or model_revision for every canonical route selection.")
			}
			if selection.ModelID.Validate() != nil || selection.ModelID != agent.SourceRoutes()[index].ModelID {
				return nil, domain.Fail(domain.InvalidArgument, "Canonical model selection does not match the Worker document.", "Use the exact configured model ID and its current revision.")
			}
			wire.Selection = &pb.AgentWorkerModelSelection_ModelId{ModelId: string(selection.ModelID)}
		}
		if len(agent.Routes) > 0 {
			result.RouteModels = append(result.RouteModels, wire)
		} else {
			result.Model = wire
		}
	}
	return result, nil
}
