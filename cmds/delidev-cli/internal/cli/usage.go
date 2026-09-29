package cli

import (
	"context"
	"encoding/json"
	"slices"
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
	profile := f.String("accounting-profile", "", "accounting profile (native-input-v1; default: response-only)")
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
		if *profile != "native-input-v1" {
			return nil, domain.Fail(domain.InvalidArgument, "Unknown accounting profile.", "Use --accounting-profile native-input-v1, or omit it for response-only reads.")
		}
		status, err := c.system.GetStatus(ctx, request(c, &pb.GetStatusRequest{}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		if !slices.Contains(status.Msg.Capabilities, pb.SystemCapability_SYSTEM_CAPABILITY_NATIVE_INPUT_ACCOUNTING_V1) {
			return nil, domain.Fail(domain.Unsupported, "The server cannot negotiate native input accounting.", "Update the server before requesting this profile.")
		}
		requestBody.AccountingProfile = pb.UsageAccountingProfile_USAGE_ACCOUNTING_PROFILE_NATIVE_INPUT_V1
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
	if requestBody.AccountingProfile != pb.UsageAccountingProfile_USAGE_ACCOUNTING_PROFILE_UNSPECIFIED && (response.Msg.AccountingProfile != requestBody.AccountingProfile || response.Msg.NativeAccounting == nil) {
		return nil, domain.Fail(domain.Unsupported, "The server omitted the requested accounting profile.", "Update the server; response-only data cannot satisfy a native accounting read.")
	}
	raw, err := (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}).Marshal(response.Msg)
	if err != nil {
		return nil, domain.SafeError(err)
	}
	if requestBody.AccountingProfile == pb.UsageAccountingProfile_USAGE_ACCOUNTING_PROFILE_UNSPECIFIED {
		// EmitUnpopulated preserves the legacy fields, but the additive negotiated
		// profile must remain absent for existing response-only JSON consumers.
		var legacy map[string]json.RawMessage
		if err := json.Unmarshal(raw, &legacy); err != nil {
			return nil, domain.SafeError(err)
		}
		delete(legacy, "accounting_profile")
		delete(legacy, "native_accounting")
		raw, err = json.Marshal(legacy)
		if err != nil {
			return nil, domain.SafeError(err)
		}
	}
	return json.RawMessage(raw), nil
}
