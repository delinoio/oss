// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func sessionNativeShellCommand(ctx context.Context, c client, o options, args []string, streams IO) (any, error) {
	if len(args) == 0 {
		return nil, domain.Fail(domain.MissingInput, "Choose a native-shell action.", "Use run, status or cancel.")
	}
	f := flags("session native-shell " + args[0])
	id := f.String("id", "", "original session UUID")
	action := f.String("action", "", "original native shell action UUID")
	var revision *uint64
	var command *string
	var confirmed *bool
	var timeout *uint64
	if args[0] == "run" || args[0] == "cancel" {
		revision = f.Uint64("revision", 0, "exact current session/action revision")
	}
	if args[0] == "run" {
		command = f.String("command", "", "exact native shell command")
		confirmed = f.Bool("confirm-full-access", false, domain.NativeShellDisclosure)
		timeout = f.Uint64("timeout-ms", 3600000, "maximum native execution time (0 requests immediate timeout)")
	}
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if err := domain.ID(*id).Validate(); err != nil {
		return nil, err
	}
	switch args[0] {
	case "run":
		disclosure := streams.Err
		if disclosure == nil {
			return nil, domain.Fail(domain.Unavailable, "The full-access disclosure has no output stream.", "Restore CLI output before confirming native execution.")
		}
		if _, err := fmt.Fprintf(disclosure, "%s\nCommand:\n%s\n", domain.NativeShellDisclosure, *command); err != nil {
			return nil, domain.Fail(domain.Unavailable, "The full-access disclosure could not be displayed.", "Restore the CLI output before confirming native execution.")
		}
		if !*confirmed || *revision == 0 || domain.Text(*command, "native shell command", 64<<10, true) != nil {
			return nil, domain.Fail(domain.MissingInput, "Explicit full-access confirmation and the current session revision are required.", "Read the disclosure and exact command, then provide --confirm-full-access and --revision.")
		}
		message := &pb.RunNativeShellRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision}, Command: *command, Access: pb.NativeShellAccess_NATIVE_SHELL_ACCESS_FULL_ACCESS, TimeoutMs: timeout}
		r, err := c.sessions.RunNativeShell(ctx, request(c, message))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"action": resourceJSON(r.Msg.Action), "request_id": r.Msg.RequestId, "replayed": r.Msg.Replayed}, nil
	case "status":
		if err := domain.ID(*action).Validate(); err != nil {
			return nil, err
		}
		r, err := c.sessions.GetNativeShell(ctx, request(c, &pb.GetNativeShellRequest{SessionId: *id, ActionId: *action}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return resourceJSON(r.Msg.Action), nil
	case "cancel":
		if err := domain.ID(*action).Validate(); err != nil {
			return nil, err
		}
		if *revision == 0 {
			return nil, domain.NativeShellRecovery()
		}
		r, err := c.sessions.CancelNativeShell(ctx, request(c, &pb.CancelNativeShellRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *action, ExpectedRevision: *revision}, SessionId: *id}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"action": resourceJSON(r.Msg.Action), "request_id": r.Msg.RequestId, "replayed": r.Msg.Replayed}, nil
	default:
		return nil, domain.Fail(domain.InvalidArgument, "Unknown native-shell action.", "Use run, status or cancel.")
	}
}
