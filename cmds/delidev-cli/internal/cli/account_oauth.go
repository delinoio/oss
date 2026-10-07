// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"google.golang.org/protobuf/encoding/protojson"
	"io"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func readOAuthCode(input io.Reader) ([]byte, error) {
	if terminalInput(input) {
		return nil, domain.Fail(domain.MissingInput, "Authorization code requires bounded stdin.", "Pipe the exact code with printf; do not put it in arguments.")
	}
	raw, err := io.ReadAll(io.LimitReader(input, 8193))
	if err == nil {
		err = domain.ValidateOAuthCode(raw)
	}
	if err != nil {
		clear(raw)
		return nil, domain.Fail(domain.InvalidArgument, "Authorization code input is invalid.", "Provide 1–8192 exact UTF-8 bytes without control characters or line endings.")
	}
	return raw, nil
}
func accountOAuthCommand(ctx context.Context, c client, o options, args []string, streams IO) (any, error) {
	if len(args) == 0 {
		return nil, domain.Fail(domain.MissingInput, "An OAuth operation is required.", "Use start, complete, status or cancel.")
	}
	op := args[0]
	if op != "start" && op != "complete" && op != "status" && op != "cancel" {
		return nil, domain.Fail(domain.InvalidArgument, "Unknown OAuth operation.", "Use start, complete, status or cancel.")
	}
	f := flags("account oauth " + op)
	var id string
	var revision uint64
	var codeStdin, callbackStdin, recover bool
	var callbackURL, googleProject, apiProtocol string
	if op == "start" {
		f.StringVar(&id, "provider-id", "", "")
		f.StringVar(&callbackURL, "callback-url", "", "")
		f.StringVar(&googleProject, "google-project-id", "", "")
		f.StringVar(&apiProtocol, "api-protocol", "", "")
	} else {
		f.StringVar(&id, "attempt-id", "", "")
	}
	if op != "status" {
		f.Uint64Var(&revision, "revision", 0, "")
	}
	if op == "complete" {
		f.BoolVar(&codeStdin, "code-stdin", false, "")
		f.BoolVar(&callbackStdin, "callback-stdin", false, "")
		f.BoolVar(&recover, "recover", false, "")
	}
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if err := domain.ID(id).Validate(); err != nil {
		return nil, err
	}
	if op == "status" {
		r, err := c.accounts.GetAccountOAuthStatus(ctx, request(c, &pb.GetAccountOAuthStatusRequest{AttemptId: id}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return oauthCLIResponse(r.Msg), nil
	}
	if revision == 0 {
		return nil, domain.Fail(domain.MissingInput, "The original current revision is required.", "Supply --revision from the original provider or attempt.")
	}
	if recover && o.requestID == "" {
		return nil, domain.Fail(domain.MissingInput, "Recovery requires the original completion request ID.", "Supply --request-id and the original completion revision; recovery never exchanges again.")
	}
	ensureRequest(&o)
	m := &pb.Mutation{Id: id, ExpectedRevision: revision, RequestId: string(o.requestID)}
	if op == "start" {
		start := &pb.StartAccountOAuthRequest{Provider: m, CallbackUrl: callbackURL}
		if apiProtocol != "" {
			format := domain.APIProtocol(apiProtocol)
			if !format.API() {
				return nil, domain.Fail(domain.InvalidArgument, "Invalid OAuth API format.", "Select openai-responses, openai-chat or anthropic-messages.")
			}
			if err := requireProviderInventoryCapability(ctx, c, pb.ProviderInventoryCapability_PROVIDER_INVENTORY_CAPABILITY_ACCOUNT_OAUTH_API_PROTOCOL_V1); err != nil {
				return nil, err
			}
			start.ApiProtocol = rpc.WireAPIFormat(domain.ProviderAPIFormat{Protocol: format}).Protocol
		}
		if googleProject != "" {
			start.Google = &pb.AccountOAuthGoogleOptions{QuotaProjectId: googleProject}
		}
		r, err := c.accounts.StartAccountOAuth(ctx, request(c, start))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"attempt": r.Msg.Attempt, "authorization_url": r.Msg.AuthorizationUrl, "request_id": r.Msg.RequestId, "replayed": r.Msg.Replayed, "flow": r.Msg.Flow.String(), "user_code": r.Msg.UserCode}, nil
	}
	if op == "cancel" {
		r, err := c.accounts.CancelAccountOAuth(ctx, request(c, &pb.CancelAccountOAuthRequest{Mutation: m}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return oauthCLIResponse(r.Msg), nil
	}
	choices := 0
	for _, chosen := range []bool{codeStdin, callbackStdin, recover} {
		if chosen {
			choices++
		}
	}
	if choices != 1 {
		return nil, domain.Fail(domain.MissingInput, "Choose exactly one completion input.", "Use --code-stdin for OpenRouter, --callback-stdin for a state-bound callback, or --recover with the original request ID.")
	}
	var code, state []byte
	var err error
	if codeStdin {
		code, err = readOAuthCode(streams.In)
		if err != nil {
			return nil, err
		}
	}
	if callbackStdin {
		code, state, err = readOAuthCallback(streams.In)
		if err != nil {
			return nil, err
		}
	}
	defer clear(code)
	defer clear(state)
	r, err := c.accounts.CompleteAccountOAuth(ctx, request(c, &pb.CompleteAccountOAuthRequest{Mutation: m, AuthorizationCode: code, AuthorizationState: state}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	return oauthCLIResponse(r.Msg), nil
}
func oauthCLIResponse(r interface {
	GetAttempt() *pb.AccountOAuthAttempt
	GetAccount() *pb.Resource
	GetRequestId() string
	GetReplayed() bool
}) map[string]any {
	value := map[string]any{"attempt": r.GetAttempt(), "request_id": r.GetRequestId(), "replayed": r.GetReplayed()}
	if r.GetAccount() != nil {
		value["account"] = resourceJSON(r.GetAccount())
	}
	return value
}

// The callback envelope stays on bounded write-only stdin. Mutation authority
// always comes from the explicit original CLI flags, never from callback input.
func readOAuthCallback(input io.Reader) ([]byte, []byte, error) {
	failure := func() ([]byte, []byte, error) {
		return nil, nil, domain.Fail(domain.InvalidArgument, "OAuth callback input is invalid.", "Pipe a protobuf JSON object with only authorizationCode and authorizationState as base64 bytes.")
	}
	if terminalInput(input) {
		return failure()
	}
	raw, err := io.ReadAll(io.LimitReader(input, 16385))
	defer clear(raw)
	if err != nil || len(raw) > 16384 {
		return failure()
	}
	var callback pb.CompleteAccountOAuthRequest
	if protojson.Unmarshal(raw, &callback) != nil || callback.Mutation != nil || domain.ValidateOAuthCode(callback.AuthorizationState) != nil || len(callback.AuthorizationState) != 43 || len(callback.AuthorizationCode) > 0 && domain.ValidateOAuthCode(callback.AuthorizationCode) != nil {
		clear(callback.AuthorizationCode)
		clear(callback.AuthorizationState)
		return failure()
	}
	return callback.AuthorizationCode, callback.AuthorizationState, nil
}
