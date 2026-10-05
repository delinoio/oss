package cli

import (
	"bytes"
	"context"
	"io"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func readAPIKey(input io.Reader) ([]byte, error) {
	if terminalInput(input) {
		return nil, domain.Fail(domain.MissingInput, "An API key is required through stdin.", "Pipe the key from protected storage; never include it in command arguments.")
	}
	raw, err := io.ReadAll(io.LimitReader(input, domain.MaxAPIKeyBytes+3))
	if err != nil {
		clear(raw)
		return nil, domain.Fail(domain.InvalidArgument, "The API key input could not be read.", "Provide only the key through stdin.")
	}
	// Accept one conventional line ending from a secret reader, without silently
	// trimming spaces, extra lines or other unsupported credential bytes.
	key := raw
	if bytes.HasSuffix(key, []byte("\r\n")) {
		key = key[:len(key)-2]
	} else if bytes.HasSuffix(key, []byte("\n")) {
		key = key[:len(key)-1]
	}
	if err = domain.ValidateAPIKey(key, false); err != nil {
		clear(raw)
		return nil, err
	}
	return key, nil
}
func accountCommand(ctx context.Context, c client, o options, args []string, streams IO) (any, error) {
	operation := args[0]
	f := flags("account " + operation)
	id := f.String("id", "", "")
	var revision uint64
	var keyStdin, keyless bool
	var machine, operationID string
	var deviceCode bool
	if operation == "login" || operation == "refresh" || operation == "logout" {
		f.StringVar(&machine, "machine-id", "", "")
	}
	if operation == "login" {
		f.BoolVar(&deviceCode, "device-code", false, "")
	}
	if operation == "login-progress" {
		f.StringVar(&operationID, "operation-id", "", "")
	}
	if operation != "status" && operation != "login-progress" {
		f.Uint64Var(&revision, "revision", 0, "")
	}
	if operation == "connect" {
		f.BoolVar(&keyStdin, "key-stdin", false, "")
		f.BoolVar(&keyless, "keyless", false, "")
	}
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if *id == "" {
		return nil, domain.Fail(domain.MissingInput, "An account ID is required.", "Select a saved account with --id.")
	}
	if err := domain.ID(*id).Validate(); err != nil {
		return nil, err
	}
	if operation == "status" {
		response, err := c.accounts.GetAccountStatus(ctx, request(c, &pb.GetAccountStatusRequest{Id: *id}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"account": resourceJSON(response.Msg.Account)}, nil
	}
	if operation == "login-progress" {
		if err := domain.ID(operationID).Validate(); err != nil {
			return nil, err
		}
		response, err := c.subscriptions.GetSubscriptionProgress(ctx, request(c, &pb.GetSubscriptionProgressRequest{AccountId: *id, OperationId: operationID}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"url": response.Msg.Url, "user_code": response.Msg.UserCode, "canceled": response.Msg.Canceled, "state": response.Msg.State.String(), "suggested_name": response.Msg.SuggestedName, "generation": response.Msg.Generation}, nil
	}
	if revision == 0 {
		return nil, domain.Fail(domain.MissingInput, "The account's current revision is required.", "Read account status and provide --revision.")
	}
	mutation := &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: revision}
	if operation == "cancel-login" {
		response, err := c.subscriptions.CancelSubscription(ctx, request(c, &pb.CancelSubscriptionRequest{Mutation: mutation}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"account": resourceJSON(response.Msg.Account), "replayed": response.Msg.Replayed}, nil
	}
	if operation == "login" || operation == "refresh" || operation == "logout" {
		if machine != "" {
			if err := domain.ID(machine).Validate(); err != nil {
				return nil, err
			}
		} else {
			status, err := c.system.GetStatus(ctx, request(c, &pb.GetStatusRequest{}))
			if err != nil {
				return nil, rpc.ClientError(err)
			}
			if !slices.Contains(status.Msg.Capabilities, pb.SystemCapability_SYSTEM_CAPABILITY_SERVER_SUBSCRIPTION_LOGIN_V1) {
				return nil, domain.Fail(domain.Unsupported, "This server does not support independent subscription login.", "Update the server or explicitly select an existing Worker with --machine-id.")
			}
		}
		action := pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN
		if operation == "refresh" {
			action = pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH
		}
		if operation == "logout" {
			action = pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT
		}
		response, err := c.subscriptions.RequestSubscription(ctx, request(c, &pb.RequestSubscriptionRequest{Mutation: mutation, MachineId: machine, Action: action, DeviceCode: deviceCode}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"account": resourceJSON(response.Msg.Account), "operation_id": response.Msg.OperationId, "replayed": response.Msg.Replayed}, nil
	}
	if operation == "validate" {
		response, err := c.accounts.ValidateAccount(ctx, request(c, &pb.ValidateAccountRequest{Mutation: mutation}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		var validation domain.AccountValidation
		if err = domain.Decode(response.Msg.ValidationJson, &validation); err != nil {
			return nil, err
		}
		value := map[string]any{"account": resourceJSON(response.Msg.Account), "validation": validation, "replayed": response.Msg.Replayed}
		if validation.Problem != nil {
			return value, validation.Problem
		}
		return value, nil
	}
	if operation == "connect" {
		if keyStdin == keyless {
			return nil, domain.Fail(domain.MissingInput, "Choose exactly one connection input.", "Use --key-stdin for an API key or --keyless for a keyless local provider.")
		}
		var key []byte
		if keyStdin {
			var err error
			key, err = readAPIKey(streams.In)
			if err != nil {
				return nil, err
			}
			defer clear(key)
		}
		response, err := c.accounts.ConnectAccount(ctx, request(c, &pb.ConnectAccountRequest{Mutation: mutation, ApiKey: key, Keyless: keyless}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"account": resourceJSON(response.Msg.Account), "replayed": response.Msg.Replayed}, nil
	}
	response, err := c.accounts.DisconnectAccount(ctx, request(c, &pb.DisconnectAccountRequest{Mutation: mutation}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	value := map[string]any{"account": resourceJSON(response.Msg.Account), "replayed": response.Msg.Replayed}
	if len(response.Msg.CleanupProblemJson) > 0 {
		var problem domain.Error
		if err = domain.Decode(response.Msg.CleanupProblemJson, &problem); err != nil {
			return value, err
		}
		return value, &problem
	}
	return value, nil
}
