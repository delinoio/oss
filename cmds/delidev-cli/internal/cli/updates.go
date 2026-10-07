// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/connections"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"path/filepath"
	"slices"
	"time"
)

func updateCommand(ctx context.Context, c client, o options, args []string) (any, error) {
	if len(args) == 0 {
		return nil, usage()
	}
	status, e := c.system.GetStatus(ctx, request(c, &pb.GetStatusRequest{}))
	if e != nil {
		return nil, rpc.ClientError(e)
	}
	if !slices.Contains(status.Msg.Capabilities, pb.SystemCapability_SYSTEM_CAPABILITY_SIGNED_UPDATES_V1) {
		return nil, domain.Fail(domain.Unsupported, "This server has no signed DeliDev update support.", "Update the server explicitly without replacing its active processes.")
	}
	fs := flags("update " + args[0])
	id := fs.String("id", "", "original update")
	revision := fs.Uint64("revision", 0, "exact revision")
	switch args[0] {
	case "check":
		component := fs.String("component", "", "desktop or worker")
		target := fs.String("target", "", "one of six official targets")
		current := fs.String("current-version", "", "installed public version")
		machine := fs.String("machine-id", "", "original Runner Device")
		machineRev := fs.Uint64("machine-revision", 0, "exact Runner Device revision")
		if parse(fs, args[1:]) != nil || *id != "" || *revision != 0 {
			return nil, usage()
		}
		var cpt pb.UpdateComponent
		switch *component {
		case "desktop":
			cpt = pb.UpdateComponent_UPDATE_COMPONENT_DESKTOP
		case "worker":
			cpt = pb.UpdateComponent_UPDATE_COMPONENT_WORKER
		default:
			return nil, usage()
		}
		var t pb.UpdateTarget
		for i, name := range []string{"darwin-amd64", "darwin-arm64", "windows-amd64", "windows-arm64", "linux-amd64", "linux-arm64"} {
			if *target == name {
				t = pb.UpdateTarget(i + 1)
			}
		}
		bounded, cancel := context.WithTimeout(ctx, 35*time.Second)
		defer cancel()
		r, e := c.installation.CheckUpdate(bounded, request(c, &pb.CheckUpdateRequest{RequestId: string(o.requestID), Component: cpt, Target: t, CurrentVersion: *current, MachineId: *machine, ExpectedMachineRevision: *machineRev}))
		if e != nil {
			return nil, rpc.ClientError(e)
		}
		return map[string]any{"update": resourceJSON(r.Msg.Update), "replayed": r.Msg.Replayed}, nil
	case "get":
		if parse(fs, args[1:]) != nil || *revision != 0 {
			return nil, usage()
		}
		r, e := c.installation.GetUpdate(ctx, request(c, &pb.GetUpdateRequest{Id: *id}))
		if e != nil {
			return nil, rpc.ClientError(e)
		}
		return map[string]any{"update": resourceJSON(r.Msg.Update), "production_root_ready": r.Msg.ProductionRootReady}, nil
	case "worker-request", "cancel":
		if parse(fs, args[1:]) != nil {
			return nil, usage()
		}
		m := &pb.Mutation{Id: *id, ExpectedRevision: *revision, RequestId: string(o.requestID)}
		if args[0] == "cancel" {
			r, e := c.installation.CancelUpdate(ctx, request(c, &pb.CancelUpdateRequest{Mutation: m}))
			if e != nil {
				return nil, rpc.ClientError(e)
			}
			return map[string]any{"update": resourceJSON(r.Msg.Update), "replayed": r.Msg.Replayed}, nil
		}
		r, e := c.installation.RequestWorkerUpdate(ctx, request(c, &pb.RequestWorkerUpdateRequest{Mutation: m}))
		if e != nil {
			return nil, rpc.ClientError(e)
		}
		return map[string]any{"update": resourceJSON(r.Msg.Update), "replayed": r.Msg.Replayed}, nil
	default:
		return nil, usage()
	}
}

// Native selects these fixed paired scopes; no token, endpoint or path is an IPC
// field. Ordinary commands cannot prepare/install by choosing a remote authority.
func nativeUpdateClient(o options, saved string) (client, error) {
	scope := filepath.Join(o.dataDir, "desktop-client")
	if saved != "" {
		if domain.ID(saved).Validate() != nil {
			return client{}, usage()
		}
		profile, e := connections.Inspect(o.dataDir, domain.ID(saved))
		if e != nil || profile.State != connections.Paired {
			return client{}, workerUpdateFailure()
		}
		scope = filepath.Join(o.dataDir, "connections", saved, "client")
		credential, e := worker.LoadCredential(scope)
		if e != nil || domain.OwnershipBlocks(domain.OwnershipActor, "", credential.Type != domain.ClientDevice) ||
			domain.OwnershipBlocks(domain.OwnershipInstance, domain.ID(credential.ServerID), credential.ServerID != profile.ServerID) ||
			domain.OwnershipBlocks(domain.OwnershipDevice, domain.ID(credential.DeviceID), credential.DeviceID != profile.DeviceID) ||
			credential.PairingID != profile.PairingID || credential.Endpoint != profile.Endpoint {
			return client{}, workerUpdateFailure()
		}
	}
	credential, e := worker.LoadCredential(scope)
	if e != nil || domain.OwnershipBlocks(domain.OwnershipActor, "", credential.Type != domain.ClientDevice) {
		return client{}, workerUpdateFailure()
	}
	if saved == "" && (o.desktop == nil || o.desktop.Validate() != nil || o.desktop.ServerID != credential.ServerID || o.desktop.Root != o.dataDir) {
		return client{}, workerUpdateFailure()
	}
	return connectClient(options{dataDir: scope, desktop: o.desktop}, nil)
}
