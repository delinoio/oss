package cli

import (
	"context"
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func usageCommand(ctx context.Context, c client, args []string) (any, error) {
	if len(args) == 0 || args[0] != "summary" {
		return nil, usage()
	}
	f := flags("usage summary")
	from := f.String("from", "", "inclusive RFC3339 timestamp (default: 30 days before until)")
	until := f.String("until", "", "exclusive RFC3339 timestamp (default: server now)")
	granularity := f.String("granularity", "", "analytics granularity (day)")
	timezone := f.String("timezone", "", "explicit IANA timezone for daily analytics")
	profile := f.String("accounting-profile", "", "native-units-v1 for distinct Codex responses, Grok closed inputs, Claude main-loop inputs and OpenCode steps")
	requestBody := &pb.GetUsageSummaryRequest{}
	f.StringVar(&requestBody.SessionId, "session-id", "", "original session filter")
	f.StringVar(&requestBody.ProjectId, "project-id", "", "original project filter")
	f.StringVar(&requestBody.AccountId, "account-id", "", "event-time account filter")
	f.StringVar(&requestBody.ProviderId, "provider-id", "", "event-time provider filter")
	f.StringVar(&requestBody.ModelId, "model-id", "", "event-time model filter")
	f.BoolVar(&requestBody.GeneralChat, "general-chat", false, "projectless General Chat only")
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if *profile != "" {
		if *profile != "native-units-v1" {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid usage accounting profile.", "Supported value: native-units-v1.")
		}
		requestBody.AccountingProfile = pb.UsageAccountingProfile_USAGE_ACCOUNTING_PROFILE_NATIVE_UNITS_V1
	}
	if *granularity == "" {
		if *timezone != "" {
			return nil, domain.Fail(domain.InvalidArgument, "A timezone requires a usage granularity.", "Set --granularity day with --timezone, or omit both flags.")
		}
	} else if *granularity == "day" {
		if *timezone == "" {
			return nil, domain.Fail(domain.InvalidArgument, "Daily usage requires a timezone.", "Set --timezone to an explicit IANA timezone such as UTC or Asia/Seoul.")
		}
		requestBody.Granularity = pb.UsageTimeGranularity_USAGE_TIME_GRANULARITY_DAY
		requestBody.TimeZone = *timezone
	} else {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid usage granularity.", "Supported value: day.")
	}
	for _, part := range []struct {
		value  string
		target *int64
	}{{*from, &requestBody.FromUnixMs}, {*until, &requestBody.UntilUnixMs}} {
		if part.value == "" {
			continue
		}
		value, err := time.Parse(time.RFC3339, part.value)
		if err != nil || value.UnixMilli() <= 0 {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid usage timestamp.", "Use an RFC3339 timestamp with an explicit timezone after the Unix epoch.")
		}
		*part.target = value.UnixMilli()
	}
	response, err := c.usage.GetUsageSummary(ctx, request(c, requestBody))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	if requestBody.AccountingProfile != response.Msg.AccountingProfile {
		return nil, domain.Fail(domain.Unsupported, "The server did not negotiate native accounting.", "Update the server or omit the accounting profile for response-only reads.")
	}
	if requestBody.AccountingProfile == pb.UsageAccountingProfile_USAGE_ACCOUNTING_PROFILE_NATIVE_UNITS_V1 {
		seen := make(map[pb.AccountingUnitKind]bool, 2)
		for _, summary := range response.Msg.NativeAccounting {
			if summary == nil || summary.Totals == nil || seen[summary.Totals.Kind] || (summary.Totals.Kind != pb.AccountingUnitKind_ACCOUNTING_UNIT_KIND_CLAUDE_MAIN_LOOP_INPUT && summary.Totals.Kind != pb.AccountingUnitKind_ACCOUNTING_UNIT_KIND_OPENCODE_STEP) {
				return nil, nativeAccountingUnavailable()
			}
			seen[summary.Totals.Kind] = true
		}
		if len(seen) != 2 {
			return nil, nativeAccountingUnavailable()
		}
	}
	raw, err := (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}).Marshal(response.Msg)
	if err != nil {
		return nil, domain.SafeError(err)
	}
	return json.RawMessage(raw), nil
}

func nativeAccountingUnavailable() error {
	return domain.Fail(domain.Unsupported, "The server did not return complete native input accounting support.", "Update the server to support Claude and OpenCode input accounting, or omit the accounting profile for response-only reads.")
}
