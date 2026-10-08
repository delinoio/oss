// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func projectPromptHistoryCommand(ctx context.Context, c client, o options, args []string) (any, error) {
	if len(args) == 0 {
		return nil, usage()
	}
	f := flags("project prompt-history " + args[0])
	project := f.String("project-id", "", "project scope")
	limit := f.Uint("limit", 50, "page size")
	cursor := f.String("page-token", "", "history cursor")
	confirm := f.Bool("confirm", false, "confirm live history removal; older backups are unchanged")
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if err := domain.ID(*project).Validate(); err != nil {
		return nil, err
	}
	switch args[0] {
	case "list":
		if *limit < 1 || *limit > 100 {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid history page size.", "Use 1 through 100.")
		}
		r, err := c.configuration.ListProjectPromptHistory(ctx, request(c, &pb.ListProjectPromptHistoryRequest{ProjectId: *project, PageSize: uint32(*limit), PageToken: *cursor}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		entries := make([]any, 0, len(r.Msg.Entries))
		for _, e := range r.Msg.Entries {
			entries = append(entries, map[string]any{"id": e.Id, "project_id": e.ProjectId, "prompt": e.Prompt, "accepted_at": e.AcceptedAt, "acceptance_sequence": e.AcceptanceSequence})
		}
		return map[string]any{"entries": entries, "next_page_token": r.Msg.NextPageToken}, nil
	case "clear":
		if !*confirm {
			return nil, domain.Fail(domain.ConfirmationRequired, "Confirm prompt history removal.", "Pass --confirm; older backups are unchanged.")
		}
		ensureRequest(&o)
		r, err := c.configuration.ClearProjectPromptHistory(ctx, request(c, &pb.ClearProjectPromptHistoryRequest{ProjectId: *project, RequestId: string(o.requestID), Confirmed: true}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		if r.Msg.ProjectId != *project || r.Msg.RequestId != string(o.requestID) {
			return nil, domain.Fail(domain.RecoveryRequired, "The clear receipt does not match.", "Preserve the original request ID.")
		}
		return map[string]any{"project_id": r.Msg.ProjectId, "request_id": r.Msg.RequestId, "replayed": r.Msg.Replayed, "removed_count": r.Msg.RemovedCount}, nil
	default:
		return nil, usage()
	}
}
