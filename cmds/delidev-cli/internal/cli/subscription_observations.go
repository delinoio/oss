// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"slices"
)

func subscriptionObservationCommand(ctx context.Context, c client, o options, args []string) (any, error) {
	f := flags("account " + args[0])
	id := f.String("id", "", "")
	revision := f.Uint64("revision", 0, "")
	machine := f.String("machine-id", "", "")
	connection := f.String("connection-id", "", "")
	generation := f.String("generation-id", "", "")
	credit := f.String("credit-id", "", "")
	next := f.Bool("next-credit", false, "")
	inventory := f.String("credits-observation-id", "", "")
	confirm := f.Bool("confirm", false, "")
	operation := f.String("operation-id", "", "")
	if err := f.Parse(args[1:]); err != nil || f.NArg() != 0 {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid native subscription arguments.", "Use the current account, connection and credential generation with an exact revision.")
	}
	status, err := c.system.GetStatus(ctx, request(c, &pb.GetStatusRequest{}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	capability := pb.SystemCapability_SYSTEM_CAPABILITY_SUBSCRIPTION_QUOTA_V1
	if args[0] == "consume-reset-credit" || args[0] == "reconcile-reset-credit" {
		capability = pb.SystemCapability_SYSTEM_CAPABILITY_SUBSCRIPTION_RESET_CREDITS_V1
	}
	serverCredits := slices.Contains(status.Msg.Capabilities, pb.SystemCapability_SYSTEM_CAPABILITY_SERVER_SUBSCRIPTION_RESET_CREDITS_V1)
	if !slices.Contains(status.Msg.Capabilities, capability) && !((args[0] == "consume-reset-credit" && *machine == "" || args[0] == "reconcile-reset-credit") && serverCredits) && !((args[0] == "refresh-quota" && *machine == "" || args[0] == "refresh-all-quotas") && slices.Contains(status.Msg.Capabilities, pb.SystemCapability_SYSTEM_CAPABILITY_SERVER_SUBSCRIPTION_QUOTA_V2)) {
		return nil, domain.Fail(domain.Unsupported, "The server lacks this native subscription capability.", "Update the selected server and Runner Device before requesting quota or credit operations.")
	}
	if args[0] == "refresh-all-quotas" {
		if !slices.Contains(status.Msg.Capabilities, pb.SystemCapability_SYSTEM_CAPABILITY_SERVER_SUBSCRIPTION_QUOTA_V2) {
			return nil, domain.Fail(domain.Unsupported, "The server cannot refresh all quotas independently of execution.", "Update the selected server.")
		}
		if *id != "" || *revision != 0 || *machine != "" || *connection != "" || *generation != "" || *credit != "" || *next || *inventory != "" || *confirm || *operation != "" {
			return nil, domain.InvalidSubscriptionObservation()
		}
		response, err := c.subscriptions.RefreshAllSubscriptionQuotas(ctx, request(c, &pb.RefreshAllSubscriptionQuotasRequest{RequestId: string(o.requestID)}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"account_ids": response.Msg.Accounts, "replayed": response.Msg.Replayed}, nil
	}
	if domain.ID(*id).Validate() != nil || *revision == 0 || domain.ID(*connection).Validate() != nil || domain.ID(*generation).Validate() != nil {
		return nil, domain.InvalidSubscriptionObservation()
	}
	mutation := &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision}
	if args[0] == "reconcile-reset-credit" {
		if !*confirm || domain.ID(*operation).Validate() != nil || *machine != "" || *credit != "" || *next || *inventory != "" {
			return nil, domain.Fail(domain.ConfirmationRequired, "Explicit same-key reconciliation is required.", "Use --confirm and the original --operation-id; a replacement key cannot consume this attempt.")
		}
		response, err := c.subscriptions.ReconcileSubscriptionCredit(ctx, request(c, &pb.ReconcileSubscriptionCreditRequest{Mutation: mutation, OperationId: *operation, ConnectionId: *connection, GenerationId: *generation}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"account": resourceJSON(response.Msg.Account), "replayed": response.Msg.Replayed}, nil
	}
	if *machine == "" && args[0] == "refresh-quota" {
		if !slices.Contains(status.Msg.Capabilities, pb.SystemCapability_SYSTEM_CAPABILITY_SERVER_SUBSCRIPTION_QUOTA_V2) {
			return nil, domain.Fail(domain.Unsupported, "The server cannot observe quota without a Runner Device.", "Update the selected server.")
		}
	} else if *machine == "" && args[0] == "consume-reset-credit" {
		if !serverCredits {
			return nil, domain.Fail(domain.Unsupported, "The server cannot consume reset credits without a Runner Device.", "Update the server or select the original Runner Device.")
		}
	} else if domain.ID(*machine).Validate() != nil {
		return nil, domain.InvalidSubscriptionObservation()
	}
	if *operation != "" {
		return nil, domain.InvalidSubscriptionObservation()
	}
	action := pb.SubscriptionObservationAction_SUBSCRIPTION_OBSERVATION_ACTION_QUOTA
	if args[0] == "consume-reset-credit" {
		action = pb.SubscriptionObservationAction_SUBSCRIPTION_OBSERVATION_ACTION_RESET_CREDIT
		if !*confirm || *next == (*credit != "") || domain.ID(*inventory).Validate() != nil {
			return nil, domain.Fail(domain.ConfirmationRequired, "Reset-credit consumption requires explicit confirmation.", "Use --confirm with the current inventory and either --credit-id or count-only --next-credit.")
		}
	} else if *confirm || *credit != "" || *next || *inventory != "" {
		return nil, domain.InvalidSubscriptionObservation()
	}
	response, err := c.subscriptions.RequestSubscriptionObservation(ctx, request(c, &pb.RequestSubscriptionObservationRequest{Mutation: mutation, MachineId: *machine, Action: action, ConnectionId: *connection, GenerationId: *generation, CreditId: *credit, NextCredit: *next, CreditsObservationId: *inventory, Confirmed: *confirm}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	return map[string]any{"account": resourceJSON(response.Msg.Account), "operation_id": response.Msg.OperationId, "replayed": response.Msg.Replayed}, nil
}
