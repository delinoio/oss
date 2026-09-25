package cli

import (
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func scheduleCommand(ctx context.Context, c client, o options, args []string, streams IO) (any, error) {
	if len(args) == 0 {
		return nil, usage()
	}
	action := args[0]
	f := flags("schedule " + action)
	switch action {
	case "create", "edit":
		input := f.String("input", "-", "version-1 schedule definition")
		id := f.String("id", "", "schedule ID")
		revision := f.Uint64("revision", 0, "current schedule resource revision")
		localRoot := f.String("local-worker-dir", "", "private paired Worker scope for new or relocated Local selection")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if action == "create" && *revision != 0 {
			return nil, domain.Fail(domain.InvalidArgument, "Schedule creation requires revision zero.", "Use edit with the current revision to replace an existing definition.")
		}
		if action == "edit" && (*id == "" || *revision == 0) {
			return nil, domain.Fail(domain.MissingInput, "Schedule editing requires its ID and current revision.", "Inspect the schedule and provide --id and --revision.")
		}
		if *id != "" {
			if err := domain.ID(*id).Validate(); err != nil {
				return nil, err
			}
		}
		if o.tokenStdin && *input == "-" {
			return nil, domain.Fail(domain.InvalidArgument, "Credential stdin and schedule input cannot share one stream.", "Pass the definition using --input PATH.")
		}
		raw, err := readDocument(*input, streams.In)
		if err != nil {
			return nil, err
		}
		var value domain.ScheduleDefinition
		if err := domain.Decode(raw, &value); err != nil {
			return nil, err
		}
		value.ApplyDefaults()
		if err := value.Validate(); err != nil {
			return nil, err
		}
		var token string
		if *localRoot != "" || (action == "create" && value.Workspace == domain.Local) {
			token, err = localCreationCredential(ctx, c, value.Selection(), *localRoot)
			if err != nil {
				return nil, err
			}
		}
		raw, err = json.Marshal(value)
		if err != nil {
			return nil, err
		}
		response, err := c.schedules.SaveSchedule(ctx, request(c, &pb.SaveScheduleRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision}, SchemaVersion: 1, DefinitionJson: raw, LocalWorkerToken: token}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"schedule": resourceJSON(response.Msg.Schedule), "replayed": response.Msg.Replayed}, nil
	case "list":
		project := f.String("project-id", "", "project filter")
		status := f.String("enabled", "all", "all, true or false")
		limit := f.Uint("limit", 50, "page size")
		cursor := f.String("page-token", "", "schedule page token")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if *limit == 0 || *limit > 200 {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid schedule page size.", "Use --limit between 1 and 200.")
		}
		if *project != "" {
			if err := domain.ID(*project).Validate(); err != nil {
				return nil, err
			}
		}
		var enabled *bool
		switch *status {
		case "all":
		case "true", "false":
			v := *status == "true"
			enabled = &v
		default:
			return nil, domain.Fail(domain.InvalidArgument, "Invalid schedule enabled filter.", "Use --enabled all, true or false.")
		}
		response, err := c.schedules.ListSchedules(ctx, request(c, &pb.ListSchedulesRequest{ProjectId: *project, Enabled: enabled, PageSize: uint32(*limit), PageToken: *cursor}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		rows := make([]any, 0, len(response.Msg.Schedules))
		for _, r := range response.Msg.Schedules {
			rows = append(rows, resourceJSON(r))
		}
		return map[string]any{"schedules": rows, "next_page_token": response.Msg.NextPageToken}, nil
	case "history":
		id := f.String("id", "", "schedule ID")
		limit := f.Uint("limit", 50, "page size")
		cursor := f.String("page-token", "", "history page token")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if err := domain.ID(*id).Validate(); err != nil {
			return nil, err
		}
		if *limit == 0 || *limit > 200 {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid history page size.", "Use --limit between 1 and 200.")
		}
		response, err := c.schedules.ListScheduleOccurrences(ctx, request(c, &pb.ListScheduleOccurrencesRequest{ScheduleId: *id, PageSize: uint32(*limit), PageToken: *cursor}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		rows := make([]any, 0, len(response.Msg.Occurrences))
		for _, r := range response.Msg.Occurrences {
			rows = append(rows, resourceJSON(r))
		}
		return map[string]any{"occurrences": rows, "next_page_token": response.Msg.NextPageToken}, nil
	case "occurrence":
		id := f.String("id", "", "schedule ID")
		occurrence := f.String("occurrence-id", "", "occurrence ID")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		for _, v := range []string{*id, *occurrence} {
			if err := domain.ID(v).Validate(); err != nil {
				return nil, err
			}
		}
		response, err := c.schedules.GetScheduleOccurrence(ctx, request(c, &pb.GetScheduleOccurrenceRequest{ScheduleId: *id, Id: *occurrence}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"occurrence": resourceJSON(response.Msg.Occurrence)}, nil
	case "get", "inspect", "next-run", "pause", "resume", "delete", "run-now":
		id := f.String("id", "", "schedule ID")
		write := action == "pause" || action == "resume" || action == "delete" || action == "run-now"
		var revision uint64
		if write {
			f.Uint64Var(&revision, "revision", 0, "current schedule resource revision")
		}
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if err := domain.ID(*id).Validate(); err != nil {
			return nil, err
		}
		if write && revision == 0 {
			return nil, domain.Fail(domain.MissingInput, "This schedule action requires its current revision.", "Inspect the schedule and supply --revision; controls never replay old actions against new state.")
		}
		if !write {
			response, err := c.schedules.GetSchedule(ctx, request(c, &pb.GetScheduleRequest{Id: *id}))
			if err != nil {
				return nil, rpc.ClientError(err)
			}
			if action == "next-run" {
				var value domain.Schedule
				if err := domain.Decode(response.Msg.Schedule.DocumentJson, &value); err != nil {
					return nil, err
				}
				return map[string]any{"id": *id, "revision": response.Msg.Schedule.Revision, "configuration_revision": value.ConfigurationRevision, "enabled": value.Definition.Enabled, "timezone": value.Definition.Timezone, "next_run_at": value.NextRunAt, "problem": value.Problem}, nil
			}
			return map[string]any{"schedule": resourceJSON(response.Msg.Schedule)}, nil
		}
		meta := &pb.Mutation{Id: *id, ExpectedRevision: revision, RequestId: string(o.requestID)}
		if action == "delete" {
			response, err := c.schedules.DeleteSchedule(ctx, request(c, &pb.DeleteScheduleRequest{Mutation: meta}))
			if err != nil {
				return nil, rpc.ClientError(err)
			}
			return map[string]any{"id": response.Msg.Id, "deleted": true, "replayed": response.Msg.Replayed}, nil
		}
		if action == "run-now" {
			response, err := c.schedules.RunScheduleNow(ctx, request(c, &pb.RunScheduleNowRequest{Mutation: meta}))
			if err != nil {
				return nil, rpc.ClientError(err)
			}
			return map[string]any{"occurrence": resourceJSON(response.Msg.Occurrence), "session": resourceJSON(response.Msg.Session), "replayed": response.Msg.Replayed}, nil
		}
		control := pb.ScheduleAction_SCHEDULE_ACTION_PAUSE
		if action == "resume" {
			control = pb.ScheduleAction_SCHEDULE_ACTION_RESUME
		}
		response, err := c.schedules.ControlSchedule(ctx, request(c, &pb.ControlScheduleRequest{Mutation: meta, Action: control}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"schedule": resourceJSON(response.Msg.Schedule), "replayed": response.Msg.Replayed}, nil
	default:
		return nil, usage()
	}
}
