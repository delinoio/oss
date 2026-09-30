package worker

import (
	"context"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/forwarding"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// Forwarding is an independent, joined outbound lane. Agent Stop cannot cancel
// it; primary connectivity loss cancels and joins all original TCP lifetimes.
func watchForwards(ctx context.Context, config Config, credential Credential, instance domain.ID) {
	httpClient, transport := rpc.HTTPClient()
	defer transport.CloseIdleConnections()
	client := delidevv1connect.NewForwardServiceClient(httpClient, credential.Endpoint, connect.WithReadMaxBytes(64<<10), connect.WithSendMaxBytes(64<<10))
	work := delidevv1connect.NewWorkerServiceClient(httpClient, credential.Endpoint, connect.WithReadMaxBytes(16<<10), connect.WithSendMaxBytes(16<<10))
	runtime := forwarding.Config{Client: client, Token: credential.Token, Root: config.Root, Endpoint: credential.Endpoint, Logger: config.Logger}
	backoff := time.Second
	for ctx.Err() == nil {
		reconcile, stop := context.WithTimeout(ctx, 10*time.Second)
		err := forwarding.ReconcileWorker(reconcile, runtime, credential.MachineID)
		stop()
		if err != nil && config.Logger != nil {
			config.Logger.WarnContext(ctx, "worker_forward_cleanup_pending", "machine_id", credential.MachineID, "code", domain.SafeError(err).Code)
		}
		err = receiveForwards(ctx, config, work, credential, instance, runtime)
		if ctx.Err() != nil {
			return
		}
		if config.Logger != nil {
			config.Logger.WarnContext(ctx, "worker_forward_lane_interrupted", "machine_id", credential.MachineID, "code", rpc.ClientError(err).Code)
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		backoff = min(15*time.Second, backoff*2)
	}
}
func receiveForwards(ctx context.Context, config Config, client delidevv1connect.WorkerServiceClient, credential Credential, instance domain.ID, runtime forwarding.Config) error {
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	deadline := time.AfterFunc(domain.WorkerConnectionTimeout, cancel)
	defer deadline.Stop()
	stream, err := client.WatchForwardRequests(child, authenticated(credential, &pb.WatchForwardRequestsRequest{MachineId: string(credential.MachineID), InstanceId: string(instance)}))
	if err != nil {
		return err
	}
	defer stream.Close()
	var work sync.WaitGroup
	defer func() { cancel(); work.Wait() }()
	seen := map[string]bool{}
	active := make(chan struct{}, 16)
	for stream.Receive() {
		deadline.Reset(domain.WorkerConnectionTimeout)
		m := stream.Msg()
		if m.Heartbeat {
			if m.Forward != nil {
				return publicationUncertain()
			}
			continue
		}
		resource := m.Forward
		var value domain.Forward
		if resource == nil || resource.Kind != pb.EntityKind_ENTITY_KIND_FORWARD || domain.ID(resource.Id).Validate() != nil || domain.ID(resource.SessionId).Validate() != nil || domain.Decode(resource.DocumentJson, &value) != nil || value.MachineID != credential.MachineID || value.WorkerDeviceID != credential.DeviceID || value.WorkerInstanceID != instance || value.WorkerRuntimeID.Validate() != nil || value.WorkerPort == 0 || value.WorkerPort > 65535 || value.State != domain.ForwardPending || !value.ClientClaimed || value.WorkerClaimed || seen[resource.Id] || len(seen) >= 4096 {
			return publicationUncertain()
		}
		seen[resource.Id] = true
		select {
		case active <- struct{}{}:
		default:
			return domain.Fail(domain.ResourceExhausted, "The Worker forward lifetime bound is reached.", "Stop and confirm cleanup of unused forwards.")
		}
		selected := runtime
		selected.Forward = value
		selected.Peer = &pb.ForwardPeer{ForwardId: resource.Id, SessionId: resource.SessionId, RuntimeId: string(value.WorkerRuntimeID), MachineId: string(value.MachineID), InstanceId: string(instance)}
		work.Add(1)
		go func(c forwarding.Config) {
			defer work.Done()
			defer func() { <-active }()
			if err := forwarding.Run(child, c); err != nil && config.Logger != nil {
				config.Logger.WarnContext(child, "worker_forward_lifetime_ended", "forward_id", c.Peer.ForwardId, "code", domain.SafeError(err).Code)
			}
		}(selected)
	}
	if err := stream.Err(); err != nil {
		return err
	}
	return domain.Fail(domain.Unavailable, "The Worker forwarding lane ended.", "Reconnect without replaying native lifetimes.")
}
