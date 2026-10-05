// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"io"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func sshSetupCommand(ctx context.Context, c client, o options, args []string, input io.Reader) (any, error) {
	if len(args) == 0 {
		return nil, usage()
	}
	status, e := c.system.GetStatus(ctx, request(c, &pb.GetStatusRequest{}))
	if e != nil {
		return nil, rpc.ClientError(e)
	}
	if !slices.Contains(status.Msg.Capabilities, pb.SystemCapability_SYSTEM_CAPABILITY_SSH_WORKER_SETUP_V1) {
		return nil, domain.Fail(domain.Unsupported, "This server does not support SSH Runner Device setup.", "Update the original server before starting a setup.")
	}
	fs := flags("machine ssh " + args[0])
	id := fs.String("id", "", "original setup ID")
	revision := fs.Uint64("revision", 0, "original exact revision")
	meta := &pb.Mutation{RequestId: string(o.requestID)}
	switch args[0] {
	case "inspect":
		host := fs.String("host", "", "SSH host")
		port := fs.Uint("port", 22, "SSH port")
		user := fs.String("user", "", "SSH user")
		if parse(fs, args[1:]) != nil || *id != "" || *revision != 0 || *port > 65535 {
			return nil, usage()
		}
		r, e := c.installation.InspectSSHHost(ctx, request(c, &pb.InspectSSHHostRequest{RequestId: string(o.requestID), Host: *host, Port: uint32(*port), User: *user}))
		if e != nil {
			return nil, rpc.ClientError(e)
		}
		return map[string]any{"setup": resourceJSON(r.Msg.Setup), "replayed": r.Msg.Replayed}, nil
	case "start":
		name := fs.String("name", "", "Runner Device name")
		fingerprint := fs.String("confirm-host-key", "", "explicitly confirmed original SHA256 host fingerprint")
		credential := fs.Bool("credential-stdin", false, "bounded write-only credential document")
		if parse(fs, args[1:]) != nil || !*credential || o.tokenStdin || terminalInput(input) {
			return nil, usage()
		}
		raw, e := io.ReadAll(io.LimitReader(input, (64<<10)+1))
		if e != nil || len(raw) > 64<<10 {
			return nil, usage()
		}
		defer clear(raw)
		meta.Id = *id
		meta.ExpectedRevision = *revision
		r, e := c.installation.StartSSHSetup(ctx, request(c, &pb.StartSSHSetupRequest{Mutation: meta, Credential: raw, Name: *name, ConfirmedFingerprint: *fingerprint}))
		if e != nil {
			return nil, rpc.ClientError(e)
		}
		return map[string]any{"setup": resourceJSON(r.Msg.Setup), "replayed": r.Msg.Replayed}, nil
	case "get":
		if parse(fs, args[1:]) != nil || *revision != 0 {
			return nil, usage()
		}
		r, e := c.installation.GetSSHSetup(ctx, request(c, &pb.GetSSHSetupRequest{Id: *id}))
		if e != nil {
			return nil, rpc.ClientError(e)
		}
		return resourceJSON(r.Msg.Setup), nil
	case "cancel", "reconcile":
		if parse(fs, args[1:]) != nil {
			return nil, usage()
		}
		meta.Id = *id
		meta.ExpectedRevision = *revision
		if args[0] == "cancel" {
			r, e := c.installation.CancelSSHSetup(ctx, request(c, &pb.CancelSSHSetupRequest{Mutation: meta}))
			if e != nil {
				return nil, rpc.ClientError(e)
			}
			return map[string]any{"setup": resourceJSON(r.Msg.Setup), "replayed": r.Msg.Replayed}, nil
		}
		r, e := c.installation.ReconcileSSHSetup(ctx, request(c, &pb.ReconcileSSHSetupRequest{Mutation: meta}))
		if e != nil {
			return nil, rpc.ClientError(e)
		}
		return map[string]any{"setup": resourceJSON(r.Msg.Setup), "replayed": r.Msg.Replayed}, nil
	default:
		return nil, usage()
	}
}
