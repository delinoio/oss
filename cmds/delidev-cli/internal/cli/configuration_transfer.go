package cli

import (
	"context"
	"encoding/json"
	"os"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func configurationTransfer(ctx context.Context, c client, o options, args []string, streams IO) (any, error) {
	if len(args) == 0 {
		return nil, usage()
	}
	operation := args[0]
	if operation != "export" && operation != "preview" && operation != "apply" {
		return nil, usage()
	}
	f := flags("configuration " + operation)
	input, output := new(string), new(string)
	if operation != "export" {
		input = f.String("input", "-", "versioned selection or exact reviewed preview JSON")
	}
	if operation != "apply" {
		output = f.String("output", "", "new private file for the portable document; never overwrites")
	}
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	var raw []byte
	var err error
	if operation != "export" {
		if o.tokenStdin && *input == "-" {
			return nil, domain.Fail(domain.MissingInput, "Authentication and configuration cannot share stdin.", "Use --input PATH for the configuration document.")
		}
		raw, err = readDocument(*input, streams.In)
		if err != nil {
			return nil, err
		}
	}
	if operation != "export" {
		hasRepository := false
		if operation == "preview" {
			var selection domain.ConfigurationImportSelection
			if domain.Decode(raw, &selection) == nil {
				for _, entry := range selection.Bundle.Entries {
					hasRepository = hasRepository || entry.Kind == domain.RepositoryKind
				}
			}
		} else {
			var preview domain.ConfigurationImportPreview
			if domain.Decode(raw, &preview) == nil {
				for _, entry := range preview.Plan.Changes {
					hasRepository = hasRepository || entry.Kind == domain.RepositoryKind
				}
			}
		}
		if hasRepository {
			status, err := c.system.GetStatus(ctx, request(c, &pb.GetStatusRequest{}))
			if err != nil {
				return nil, rpc.ClientError(err)
			}
			if !slices.Contains(status.Msg.Capabilities, pb.SystemCapability_SYSTEM_CAPABILITY_REMOTE_REPOSITORIES_V1) {
				return nil, domain.Fail(domain.Unsupported, "This server does not support remote repositories.", "Update the server before importing repositories.")
			}
		}
	}
	switch operation {
	case "export":
		result, e := c.configuration.ExportConfiguration(ctx, request(c, &pb.ExportConfigurationRequest{}))
		if e != nil {
			return nil, rpc.ClientError(e)
		}
		raw = result.Msg.DocumentJson
	case "preview":
		result, e := c.configuration.PreviewConfigurationImport(ctx, request(c, &pb.PreviewConfigurationImportRequest{SelectionJson: raw}))
		if e != nil {
			return nil, rpc.ClientError(e)
		}
		raw = result.Msg.PreviewJson
	case "apply":
		result, e := c.configuration.ApplyConfigurationImport(ctx, request(c, &pb.ApplyConfigurationImportRequest{RequestId: string(o.requestID), PreviewJson: raw}))
		if e != nil {
			return nil, rpc.ClientError(e)
		}
		return struct {
			RequestID string          `json:"request_id"`
			Replayed  bool            `json:"replayed"`
			Import    json.RawMessage `json:"import"`
		}{result.Msg.RequestId, result.Msg.Replayed, result.Msg.ResultJson}, nil
	}
	var document json.RawMessage
	if err := domain.Decode(raw, &document); err != nil {
		return nil, err
	}
	if *output == "" {
		return document, nil
	}
	file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, domain.Fail(domain.Conflict, "The configuration output file could not be created.", "Choose a new writable path; existing files are never replaced.")
	}
	// A failed or uncertain write remains at its explicitly chosen path. Never
	// remove it by path after closing: another process could replace that name.
	_, err = file.Write(raw)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = security.SyncParent(*output)
	}
	if err != nil {
		return nil, domain.Fail(domain.Unavailable, "The configuration output file was not confirmed durable.", "Preserve and inspect the selected file before exporting to a new path.")
	}
	return struct {
		Written bool `json:"written"`
	}{true}, nil
}
