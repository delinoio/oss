package cli

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"fmt"
	"time"

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
	case "observe-connection":
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		return observeNotificationConnection(ctx, c)
	case "preferences":
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		value, err := c.inbox.GetNotificationPreferences(ctx, request(c, &pb.GetNotificationPreferencesRequest{Situations: true}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return notificationOutput(value.Msg)
	case "configure":
		revision := f.Uint64("revision", 0, "original preferences revision")
		interactions := f.String("interactions", "", "on or off")
		terminals := f.String("terminals", "", "on or off")
		controls := map[string]*string{}
		controls["questions"] = f.String("questions", "", "on or off; preserve other situations")
		controls["approvals"] = f.String("approvals", "", "on or off; preserve other situations")
		controls["succeeded"] = f.String("succeeded", "", "on or off; preserve other situations")
		controls["failed"] = f.String("failed", "", "on or off; preserve other situations")
		controls["stopped"] = f.String("stopped", "", "on or off; preserve other situations")
		controls["server-lost"] = f.String("server-lost", "", "on or off; preserve other situations")
		controls["server-restored"] = f.String("server-restored", "", "on or off; preserve other situations")
		controls["worker-unavailable"] = f.String("worker-unavailable", "", "on or off; preserve other situations")
		controls["worker-available"] = f.String("worker-available", "", "on or off; preserve other situations")
		controls["quota-exhausted"] = f.String("quota-exhausted", "", "on or off; preserve other situations")
		controls["schedule-start-failed"] = f.String("schedule-start-failed", "", "on or off; preserve other situations")
		controls["schedule-offline"] = f.String("schedule-offline", "", "on or off; preserve other situations")

		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		individual := false
		for _, value := range controls {
			if *value != "" {
				individual = true
				if *value != "on" && *value != "off" {
					return nil, domain.Fail(domain.InvalidArgument, "Notification choices must be on or off.", "Use an explicit typed situation.")
				}
			}
		}
		if individual {
			if *revision == 0 || *interactions != "" || *terminals != "" {
				return nil, domain.Fail(domain.InvalidArgument, "Use a revision and individual notification choices.", "Do not mix individual and legacy combined controls.")
			}
			changes := &pb.SituationNotificationChanges{}
			if raw := *controls["questions"]; raw != "" {
				enabled := raw == "on"
				changes.Questions = &enabled
			}
			if raw := *controls["approvals"]; raw != "" {
				enabled := raw == "on"
				changes.Approvals = &enabled
			}
			if raw := *controls["succeeded"]; raw != "" {
				enabled := raw == "on"
				changes.Succeeded = &enabled
			}
			if raw := *controls["failed"]; raw != "" {
				enabled := raw == "on"
				changes.Failed = &enabled
			}
			if raw := *controls["stopped"]; raw != "" {
				enabled := raw == "on"
				changes.Stopped = &enabled
			}
			if raw := *controls["server-lost"]; raw != "" {
				enabled := raw == "on"
				changes.ServerLost = &enabled
			}
			if raw := *controls["server-restored"]; raw != "" {
				enabled := raw == "on"
				changes.ServerRestored = &enabled
			}
			if raw := *controls["worker-unavailable"]; raw != "" {
				enabled := raw == "on"
				changes.WorkerUnavailable = &enabled
			}
			if raw := *controls["worker-available"]; raw != "" {
				enabled := raw == "on"
				changes.WorkerAvailable = &enabled
			}
			if raw := *controls["quota-exhausted"]; raw != "" {
				enabled := raw == "on"
				changes.QuotaExhausted = &enabled
			}
			if raw := *controls["schedule-start-failed"]; raw != "" {
				enabled := raw == "on"
				changes.ScheduleStartFailed = &enabled
			}
			if raw := *controls["schedule-offline"]; raw != "" {
				enabled := raw == "on"
				changes.ScheduleOffline = &enabled
			}
			result, err := c.inbox.SetNotificationPreferences(ctx, request(c, &pb.SetNotificationPreferencesRequest{RequestId: string(o.requestID), ExpectedRevision: *revision, Changes: changes}))
			if err != nil {
				return nil, rpc.ClientError(err)
			}
			return notificationOutput(result.Msg)
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

// Native observation consumes this closed metadata projection, not mapped
// product errors. A wire Unavailable is not evidence of a network edge.
func observeNotificationConnection(parent context.Context, c client) (any, error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	status, err := c.system.GetStatus(ctx, request(c, &pb.GetStatusRequest{}))
	if err != nil {
		state := "unusable"
		if !connect.IsWireError(err) {
			switch connect.CodeOf(err) {
			case connect.CodeUnavailable:
				state = "network-unavailable"
			case connect.CodeDeadlineExceeded:
				state = "network-deadline"
			}
		}
		return map[string]any{"state": state}, nil
	}
	v := status.Msg
	if v.Stopping || v.ProtocolVersion != rpc.ProtocolVersion {
		return map[string]any{"state": "unusable"}, nil
	}
	supported := false
	for _, capability := range v.Capabilities {
		supported = supported || capability == pb.SystemCapability_SYSTEM_CAPABILITY_SITUATION_NOTIFICATIONS_V1
	}
	if !supported {
		return map[string]any{"state": "unusable"}, nil
	}
	preferences, err := c.inbox.GetNotificationPreferences(ctx, request(c, &pb.GetNotificationPreferencesRequest{Situations: true}))
	if err != nil {
		// A successful original status read is recovery evidence even if the
		// subsequent preference refresh loses transport. Retain only native's
		// previously acknowledged cache; wire/authentication errors dispose it.
		if !connect.IsWireError(err) && (connect.CodeOf(err) == connect.CodeUnavailable || connect.CodeOf(err) == connect.CodeDeadlineExceeded) {
			return map[string]any{"state": "authenticated-success", "server_id": v.ServerId}, nil
		}
		return map[string]any{"state": "unusable"}, nil
	}
	p := preferences.Msg.Preferences
	if p == nil || p.Revision == 0 || p.Situations == nil {
		return map[string]any{"state": "unusable"}, nil
	}
	return map[string]any{"state": "authenticated-success", "server_id": v.ServerId, "revision": fmt.Sprint(p.Revision), "lost": p.Situations.ServerLost, "restored": p.Situations.ServerRestored}, nil
}
