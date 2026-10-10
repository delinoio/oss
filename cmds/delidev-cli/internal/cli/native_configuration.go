// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"slices"
)

func nativeConfigurationTransfer(ctx context.Context, c client, o options, args []string, streams IO) (any, error) {
	operation := args[0]
	f := flags("configuration " + operation)
	input := f.String("input", "-", "explicit native scope selection or exact reviewed preview JSON")
	machine := f.String("machine", "", "selected paired Worker for native-preview")
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if o.tokenStdin && *input == "-" {
		return nil, domain.Fail(domain.MissingInput, "Authentication and native configuration cannot share stdin.", "Use --input PATH.")
	}
	raw, err := readDocument(*input, streams.In)
	if err != nil {
		return nil, err
	}
	status, err := c.system.GetStatus(ctx, request(c, &pb.GetStatusRequest{}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	if !slices.Contains(status.Msg.Capabilities, pb.SystemCapability_SYSTEM_CAPABILITY_CODEX_CONFIGURATION_IMPORT_V1) {
		return nil, domain.Fail(domain.Unsupported, "This server cannot import selected Codex configuration.", "Update the server and selected Worker.")
	}
	switch operation {
	case "native-preview":
		response, err := c.configuration.RequestCodexConfigurationPreview(ctx, request(c, &pb.RequestCodexConfigurationPreviewRequest{RequestId: string(o.requestID), MachineId: *machine, SelectionJson: raw}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return struct {
			RequestID string `json:"request_id"`
			Replayed  bool   `json:"replayed"`
			Job       any    `json:"job"`
		}{response.Msg.RequestId, response.Msg.Replayed, resourceJSON(response.Msg.Job)}, nil
	case "native-review":
		response, err := c.configuration.PreviewCodexConfigurationImport(ctx, request(c, &pb.PreviewCodexConfigurationImportRequest{SelectionJson: raw}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return json.RawMessage(response.Msg.PreviewJson), nil
	case "native-apply":
		response, err := c.configuration.ApplyCodexConfigurationImport(ctx, request(c, &pb.ApplyCodexConfigurationImportRequest{RequestId: string(o.requestID), PreviewJson: raw}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return struct {
			RequestID string `json:"request_id"`
			Replayed  bool   `json:"replayed"`
			Job       any    `json:"job"`
		}{response.Msg.RequestId, response.Msg.Replayed, resourceJSON(response.Msg.Job)}, nil
	}
	return nil, usage()
}
