package cli

import (
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Preserve false, measured zero and uint64 precision in the public JSON view.
func notificationOutput(value proto.Message) (any, error) {
	encoded, err := (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(value)
	return json.RawMessage(encoded), err
}

func notificationCommand(ctx context.Context, c client, o options, args []string) (any, error) {
	if len(args) == 0 {
		return nil, usage()
	}
	f := flags("notification " + args[0])
	switch args[0] {
	case "preferences":
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		value, err := c.inbox.GetNotificationPreferences(ctx, request(c, &pb.GetNotificationPreferencesRequest{}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return notificationOutput(value.Msg)
	case "configure":
		revision := f.Uint64("revision", 0, "original preferences revision")
		interactions := f.String("interactions", "", "on or off")
		terminals := f.String("terminals", "", "on or off")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if *revision == 0 || (*interactions != "on" && *interactions != "off") || (*terminals != "on" && *terminals != "off") {
			return nil, domain.Fail(domain.InvalidArgument, "Complete revision-bound notification preferences are required.", "Use --revision and explicit --interactions on|off and --terminals on|off.")
		}
		value, err := c.inbox.SetNotificationPreferences(ctx, request(c, &pb.SetNotificationPreferencesRequest{RequestId: string(o.requestID), Preferences: &pb.NotificationPreferences{Revision: *revision, Interactions: *interactions == "on", Terminals: *terminals == "on"}}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return notificationOutput(value.Msg)
	case "list":
		limit := f.Uint("limit", 20, "maximum candidate count, at most 50")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if *limit < 1 || *limit > 50 {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid notification batch size.", "Use --limit between 1 and 50.")
		}
		value, err := c.inbox.ListNotificationCandidates(ctx, request(c, &pb.ListNotificationCandidatesRequest{Limit: uint32(*limit)}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return notificationOutput(value.Msg)
	case "inspect", "claim", "report":
		id := f.String("id", "", "original inbox entry ID")
		var claim, state string
		if args[0] == "report" {
			f.StringVar(&claim, "claim-id", "", "original notification claim ID")
			f.StringVar(&state, "state", "", "submitted, denied, failed or uncertain")
		}
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if err := domain.ID(*id).Validate(); err != nil {
			return nil, err
		}
		if args[0] == "inspect" {
			value, err := c.inbox.GetNotificationDelivery(ctx, request(c, &pb.GetNotificationDeliveryRequest{InboxId: *id}))
			if err != nil {
				return nil, rpc.ClientError(err)
			}
			return notificationOutput(value.Msg)
		}
		if args[0] == "claim" {
			value, err := c.inbox.ClaimNotification(ctx, request(c, &pb.ClaimNotificationRequest{InboxId: *id, RequestId: string(o.requestID)}))
			if err != nil {
				return nil, rpc.ClientError(err)
			}
			return notificationOutput(value.Msg)
		}
		if err := domain.ID(claim).Validate(); err != nil {
			return nil, err
		}
		wire := map[domain.NotificationState]pb.NotificationState{domain.NotificationSubmitted: pb.NotificationState_NOTIFICATION_STATE_SUBMITTED, domain.NotificationDenied: pb.NotificationState_NOTIFICATION_STATE_DENIED, domain.NotificationFailed: pb.NotificationState_NOTIFICATION_STATE_FAILED, domain.NotificationUncertain: pb.NotificationState_NOTIFICATION_STATE_UNCERTAIN}[domain.NotificationState(state)]
		if wire == pb.NotificationState_NOTIFICATION_STATE_UNSPECIFIED {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid notification report state.", "Use submitted, denied, failed or uncertain. Never repeat an uncertain display attempt.")
		}
		value, err := c.inbox.ReportNotification(ctx, request(c, &pb.ReportNotificationRequest{InboxId: *id, ClaimId: claim, RequestId: string(o.requestID), State: wire}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return notificationOutput(value.Msg)
	default:
		return nil, usage()
	}
}
