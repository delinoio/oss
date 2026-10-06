// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
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
	var codeStdin, recover bool
	if op == "start" {
		f.StringVar(&id, "provider-id", "", "")
	} else {
		f.StringVar(&id, "attempt-id", "", "")
	}
	if op != "status" {
		f.Uint64Var(&revision, "revision", 0, "")
	}
	if op == "complete" {
		f.BoolVar(&codeStdin, "code-stdin", false, "")
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
		r, err := c.accounts.StartAccountOAuth(ctx, request(c, &pb.StartAccountOAuthRequest{Provider: m}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"attempt": r.Msg.Attempt, "authorization_url": r.Msg.AuthorizationUrl, "request_id": r.Msg.RequestId, "replayed": r.Msg.Replayed}, nil
	}
	if op == "cancel" {
		r, err := c.accounts.CancelAccountOAuth(ctx, request(c, &pb.CancelAccountOAuthRequest{Mutation: m}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return oauthCLIResponse(r.Msg), nil
	}
	if codeStdin == recover {
		return nil, domain.Fail(domain.MissingInput, "Choose exactly one completion input.", "Use --code-stdin for the original exchange or --recover with its original request ID.")
	}
	var code []byte
	var err error
	if codeStdin {
		code, err = readOAuthCode(streams.In)
		if err != nil {
			return nil, err
		}
	}
	defer clear(code)
	r, err := c.accounts.CompleteAccountOAuth(ctx, request(c, &pb.CompleteAccountOAuthRequest{Mutation: m, AuthorizationCode: code}))
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
