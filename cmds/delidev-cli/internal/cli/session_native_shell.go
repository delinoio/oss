// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"flag"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func sessionNativeShellCommand(ctx context.Context, c client, o options, action string, args []string) (any, error) {
	f := flags("session " + action)
	if action == "shell" {
		id := f.String("id", "", "original settled Codex session")
		revision := f.Uint64("revision", 0, "exact original session revision")
		command := f.String("command", "", "explicit native shell command; runs outside the native sandbox")
		confirmed := f.Bool("confirm-full-access", false, "authorize full-access execution outside the native sandbox")
		timeout := f.Uint64("timeout-ms", 0, "optional original native timeout, from 0 through 3600000 milliseconds")
		if err := parse(f, args); err != nil {
			return nil, err
		}
		if !*confirmed {
			return nil, domain.Fail(domain.InvalidArgument, "Native shell commands run outside the native sandbox with full access.", "Review the exact command and pass --confirm-full-access to authorize it explicitly.")
		}
		if err := domain.ID(*id).Validate(); err != nil {
			return nil, err
		}
		if *revision == 0 {
			return nil, domain.Fail(domain.MissingInput, "Native shell execution requires the original session revision.", "Provide --id and --revision from the selected original session.")
		}
		if err := domain.Text(*command, "native shell command", 64<<10, true); err != nil {
			return nil, err
		}
		if *timeout > 3600000 {
			return nil, domain.Fail(domain.InvalidArgument, "The native shell timeout exceeds its bound.", "Use --timeout-ms from 0 through 3600000 or omit it.")
		}
		var timeoutValue *uint64
		f.Visit(func(value *flag.Flag) {
			if value.Name == "timeout-ms" {
				timeoutValue = timeout
			}
		})
		response, err := c.sessions.RunNativeShell(ctx, request(c, &pb.RunNativeShellRequest{
			Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision},
			Command:  *command, TimeoutMs: timeoutValue, FullAccessConfirmed: *confirmed,
		}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"job": resourceJSON(response.Msg.Job), "request_id": response.Msg.RequestId, "replayed": response.Msg.Replayed}, nil
	}
	job := f.String("job-id", "", "original accepted native shell job; never resubmit its command")
	var revision *uint64
	if action == "shell-cancel" {
		revision = f.Uint64("revision", 0, "exact original job revision")
	}
	if err := parse(f, args); err != nil {
		return nil, err
	}
	if err := domain.ID(*job).Validate(); err != nil {
		return nil, err
	}
	switch action {
	case "shell-status":
		response, err := c.sessions.GetNativeShell(ctx, request(c, &pb.GetNativeShellRequest{JobId: *job}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"job": resourceJSON(response.Msg.Job)}, nil
	case "shell-cancel":
		if *revision == 0 {
			return nil, domain.Fail(domain.MissingInput, "Native shell cancellation requires the original job revision.", "Inspect --job-id and pass its --revision; cancellation does not replay execution.")
		}
		response, err := c.sessions.CancelNativeShell(ctx, request(c, &pb.CancelNativeShellRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *job, ExpectedRevision: *revision}}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"job": resourceJSON(response.Msg.Job), "request_id": response.Msg.RequestId, "replayed": response.Msg.Replayed}, nil
	default:
		return nil, usage()
	}
}
