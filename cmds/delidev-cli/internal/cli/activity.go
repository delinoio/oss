package cli

import (
	"context"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func activityCommand(ctx context.Context, c client, args []string) (any, error) {
	if len(args) == 0 || args[0] != "list" {
		return nil, usage()
	}
	f := flags("activity list")
	session := f.String("session-id", "", "session filter")
	project := f.String("project-id", "", "project filter")
	limit := f.Uint("limit", 50, "page size")
	cursor := f.String("page-token", "", "activity page token")
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if *limit < 1 || *limit > 200 {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid activity page size.", "Use --limit between 1 and 200.")
	}
	for _, id := range []string{*session, *project} {
		if id != "" {
			if err := domain.ID(id).Validate(); err != nil {
				return nil, err
			}
		}
	}
	response, err := c.activity.ListActivity(ctx, request(c, &pb.ListActivityRequest{SessionId: *session, ProjectId: *project, PageSize: uint32(*limit), PageToken: *cursor}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	entries := make([]any, 0, len(response.Msg.Entries))
	for _, entry := range response.Msg.Entries {
		kind, err := rpc.Kind(entry.SourceKind)
		if err != nil {
			return nil, err
		}
		value := map[string]any{"id": entry.Id, "source_kind": kind, "source_revision": entry.SourceRevision, "observed_at_unix_ms": entry.ObservedAtUnixMs, "kind": strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(entry.Kind.String(), "ACTIVITY_KIND_")), "_", "-")}
		for _, field := range []struct{ name, value string }{{"session_id", entry.SessionId}, {"project_id", entry.ProjectId}, {"execution_id", entry.ExecutionId}, {"account_id", entry.AccountId}, {"job_id", entry.JobId}, {"schedule_id", entry.ScheduleId}, {"occurrence_id", entry.OccurrenceId}} {
			if field.value != "" {
				value[field.name] = field.value
			}
		}
		if entry.JobState != pb.ActivityJobState_ACTIVITY_JOB_STATE_UNSPECIFIED {
			value["job_state"] = strings.ToLower(strings.TrimPrefix(entry.JobState.String(), "ACTIVITY_JOB_STATE_"))
		}
		if entry.OccurrenceState != pb.ActivityOccurrenceState_ACTIVITY_OCCURRENCE_STATE_UNSPECIFIED {
			value["occurrence_state"] = strings.ToLower(strings.TrimPrefix(entry.OccurrenceState.String(), "ACTIVITY_OCCURRENCE_STATE_"))
		}
		entries = append(entries, value)
	}
	return map[string]any{"entries": entries, "next_page_token": response.Msg.NextPageToken}, nil
}
