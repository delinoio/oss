// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func nativeConfigurationImport(ctx context.Context, c client, o options, args []string, streams IO) (any, error) {
	if len(args) == 0 || args[0] != "native-preview" && args[0] != "native-apply" && args[0] != "native-prepare" && args[0] != "native-status" {
		return nil, usage()
	}
	operation := args[0]
	f := flags("configuration " + operation)
	input := f.String("input", "-", "closed native source descriptor or reviewed entry selection JSON")
	previewID := f.String("preview-job", "", "original completed preview job UUID for preparation")
	originalID := f.String("original-request", "", "original preview/apply request UUID for read-only status")
	wait := f.Bool("wait", false, "wait for the original accepted Worker job without applying again")
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if operation == "native-status" {
		if domain.ID(*originalID).Validate() != nil {
			return nil, domain.NativeConfigurationInvalid()
		}
		response, err := c.configuration.GetNativeConfigurationImport(ctx, request(c, &pb.GetNativeConfigurationImportRequest{RequestId: *originalID}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return response.Msg, nil
	}
	if *originalID != "" {
		return nil, domain.NativeConfigurationInvalid()
	}
	if o.tokenStdin && *input == "-" {
		return nil, domain.Fail(domain.MissingInput, "Authentication and configuration cannot share stdin.", "Use --input PATH for the non-secret descriptor.")
	}
	raw, err := readDocument(*input, streams.In)
	if err != nil {
		return nil, err
	}
	if len(raw) > domain.MaxNativeConfigurationSelectionBytes {
		return nil, domain.NativeConfigurationInvalid()
	}
	status, err := c.system.GetStatus(ctx, request(c, &pb.GetStatusRequest{}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	if !slices.Contains(status.Msg.Capabilities, pb.SystemCapability_SYSTEM_CAPABILITY_NATIVE_CONFIGURATION_IMPORT_V1) {
		return nil, domain.Fail(domain.Unsupported, "This server does not support native configuration import.", "Update the server before submitting a native source descriptor.")
	}
	var job *pb.Resource
	var replayed bool
	var requestID string
	if operation == "native-preview" {
		if *previewID != "" {
			return nil, usage()
		}
		response, err := c.configuration.PreviewNativeConfigurationImport(ctx, request(c, &pb.PreviewNativeConfigurationImportRequest{RequestId: string(o.requestID), SelectionJson: raw}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		job, replayed, requestID = response.Msg.Job, response.Msg.Replayed, response.Msg.RequestId
	} else if operation == "native-prepare" {
		if domain.ID(*previewID).Validate() != nil {
			return nil, domain.NativeConfigurationInvalid()
		}
		response, err := c.configuration.PrepareNativeConfigurationImport(ctx, request(c, &pb.PrepareNativeConfigurationImportRequest{PreviewJobId: *previewID, SelectionJson: raw}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return json.RawMessage(response.Msg.PlanJson), nil
	} else {
		if *previewID != "" {
			return nil, domain.NativeConfigurationInvalid()
		}

		response, err := c.configuration.ApplyNativeConfigurationImport(ctx, request(c, &pb.ApplyNativeConfigurationImportRequest{RequestId: string(o.requestID), PlanJson: raw}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		job, replayed, requestID = response.Msg.Job, response.Msg.Replayed, response.Msg.RequestId
	}
	if job == nil {
		return nil, domain.NativeConfigurationInvalid()
	}
	if *wait {
		job, err = awaitJob(ctx, c, job)
		if err != nil {
			return nil, err
		}
	}
	var value domain.Job
	if domain.Decode(job.DocumentJson, &value) != nil {
		return nil, domain.NativeConfigurationInvalid()
	}
	digest := ""
	if value.Type == domain.NativeConfigurationPreviewJob && value.State == domain.JobSucceeded {
		digest = domain.HarnessEvidenceDigest(value.Output)
	}
	return struct {
		RequestID     string          `json:"request_id"`
		Replayed      bool            `json:"replayed"`
		Job           any             `json:"job"`
		PreviewDigest string          `json:"preview_digest,omitempty"`
		Observation   json.RawMessage `json:"observation,omitempty"`
	}{requestID, replayed, resourceJSON(job), digest, value.Output}, nil
}
