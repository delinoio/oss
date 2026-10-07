// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outbound"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workernetwork"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func networkAuthority(c Credential) workernetwork.Authority {
	return workernetwork.Authority{ServerID: c.ServerID, Endpoint: c.Endpoint, MachineID: c.MachineID, DeviceID: c.DeviceID, PairingID: c.PairingID}
}

func attachNetworkObservation(request *pb.AttachWorkerRequest, config Config) *pb.AttachWorkerRequest {
	if config.network != nil {
		snapshot := config.network.current()
		request.NetworkGeneration = snapshot.metadata.Generation
		request.NetworkRouteId = string(snapshot.metadata.RouteID)
		request.NetworkKeyId = string(config.network.recipient.KeyID)
		request.NetworkRecipient = config.network.recipient.PublicKey
	}
	return request
}
func PrepareNetwork(ctx context.Context, root string, grant *PairingCode, name string, logger *slog.Logger) (workernetwork.Recipient, error) {
	var credential Credential
	var err error
	if grant != nil {
		credential, err = pair(ctx, root, *grant, domain.WorkerDevice, name, false, nil, true)
	} else {
		credential, err = LoadCredential(root)
	}
	if err != nil {
		return workernetwork.Recipient{}, err
	}
	if domain.OwnershipBlocks(domain.OwnershipActor, "", credential.Type != domain.WorkerDevice) {
		return workernetwork.Recipient{}, domain.Fail(domain.PermissionDenied, "Only a Worker can prepare a network recipient.", "Select the original Worker scope.")
	}
	vault, err := credentials.Open(filepath.Join(root, "network-vault"), credential.ServerID, logger)
	if err != nil {
		return workernetwork.Recipient{}, err
	}
	defer vault.Close()
	return workernetwork.Prepare(ctx, root, vault, networkAuthority(credential))
}
func ImportNetwork(ctx context.Context, root string, ciphertext []byte, digest string, logger *slog.Logger) (workernetwork.Cache, error) {
	recipient, err := workernetwork.LoadRecipient(root)
	if err != nil {
		return workernetwork.Cache{}, err
	}
	// The separately prepared scope is authority; the ciphertext cannot select
	// its server, pairing grant or native credential-store namespace.
	vault, err := credentials.Open(filepath.Join(root, "network-vault"), recipient.Authority.ServerID, logger)
	if err != nil {
		return workernetwork.Cache{}, err
	}
	defer vault.Close()
	return workernetwork.Import(ctx, root, vault, recipient.Authority, ciphertext, digest)
}
func pairingNetworkRecipient(root string, credential Credential) (string, domain.ID, error) {
	r, err := workernetwork.LoadRecipient(root)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	if r.Authority != networkAuthority(credential) {
		return "", "", domain.Fail(domain.RecoveryRequired, "The prepared network recipient belongs to another Worker scope.", "Preserve the original key and pairing journal.")
	}
	return r.PublicKey, r.KeyID, nil
}

type networkSnapshot struct {
	metadata workernetwork.Cache
	bundle   workernetwork.Bundle
}
type workerNetworkRuntime struct {
	mu        sync.Mutex
	root      string
	vault     *credentials.Vault
	recipient workernetwork.Recipient
	snapshot  networkSnapshot
}

func openWorkerNetwork(ctx context.Context, root string, c Credential, logger *slog.Logger) (*workerNetworkRuntime, error) {
	r, err := workernetwork.LoadRecipient(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if r.Authority != networkAuthority(c) {
		return nil, domain.Fail(domain.RecoveryRequired, "The prepared route belongs to another Worker authority.", "Use the original server, pairing and protected recipient.")
	}
	vault, err := credentials.Open(filepath.Join(root, "network-vault"), c.ServerID, logger)
	if err != nil {
		return nil, err
	}
	cache, bundle, err := workernetwork.Load(ctx, root, vault, r.Authority)
	if err != nil {
		vault.Close()
		return nil, domain.Fail(domain.RecoveryRequired, "The prepared Worker network cache is unavailable.", "Import the current authenticated encrypted export before pairing or reconnecting; no direct fallback is permitted.")
	}
	return &workerNetworkRuntime{root: root, vault: vault, recipient: r, snapshot: networkSnapshot{cache, bundle}}, nil
}
func (n *workerNetworkRuntime) current() networkSnapshot {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.snapshot
}
func (n *workerNetworkRuntime) resolver(ctx context.Context) (domain.NetworkProfile, []byte, error) {
	if ctx.Err() != nil {
		return domain.NetworkProfile{}, nil, domain.SafeError(ctx.Err())
	}
	s := n.current()
	var raw []byte
	if s.bundle.Credential != nil {
		var err error
		raw, err = json.Marshal(s.bundle.Credential)
		if err != nil {
			return domain.NetworkProfile{}, nil, domain.SafeError(err)
		}
	}
	return s.bundle.Route.Profile, raw, nil
}

type networkClientCloser struct {
	base    *http.Transport
	runtime *workerNetworkRuntime
}

func (c *networkClientCloser) CloseIdleConnections() {
	c.base.CloseIdleConnections()
	if c.runtime != nil {
		c.runtime.vault.Close()
	}
}
func networkHTTPClient(ctx context.Context, root string, credential Credential) (*http.Client, *networkClientCloser, error) {
	runtime, err := openWorkerNetwork(ctx, root, credential, nil)
	if err != nil {
		return nil, nil, err
	}
	client, base := rpc.HTTPClient()
	if runtime != nil {
		client.Transport = &outbound.Transport{Base: base, Resolve: runtime.resolver}
	}
	return client, &networkClientCloser{base: base, runtime: runtime}, nil
}
func (n *workerNetworkRuntime) sync(ctx context.Context, client delidevv1connect.WorkerServiceClient, c Credential, instance domain.ID) error {
	snapshot := n.current()
	request := &pb.SyncWorkerNetworkRequest{RequestId: string(domain.NewID()), MachineId: string(c.MachineID), InstanceId: string(instance), Recipient: n.recipient.PublicKey, KeyId: string(n.recipient.KeyID), EffectiveGeneration: snapshot.metadata.Generation, RouteId: string(snapshot.metadata.RouteID), CiphertextDigest: snapshot.metadata.CiphertextDigest}
	response, err := client.SyncWorkerNetwork(ctx, authenticated(c, request))
	if err != nil {
		return rpc.ClientError(err)
	}
	if response.Msg.InSync {
		if len(response.Msg.Ciphertext) != 0 || response.Msg.CiphertextDigest != "" || response.Msg.Route == nil || response.Msg.Route.Id != string(snapshot.metadata.RouteID) || response.Msg.Route.Revision != snapshot.metadata.Generation {
			return publicationUncertain()
		}
		return nil
	}
	if response.Msg.Route == nil || len(response.Msg.Ciphertext) == 0 || response.Msg.Route.Revision < snapshot.metadata.Generation {
		return publicationUncertain()
	}
	metadata, err := workernetwork.Import(ctx, n.root, n.vault, n.recipient.Authority, response.Msg.Ciphertext, response.Msg.CiphertextDigest)
	if err != nil {
		return err
	}
	retained, bundle, err := workernetwork.Load(ctx, n.root, n.vault, n.recipient.Authority)
	if err != nil || retained != metadata || metadata.Generation != response.Msg.Route.Revision || string(metadata.RouteID) != response.Msg.Route.Id {
		return publicationUncertain()
	}
	// Active native owners already retained their independent snapshot. Changing
	// this pointer affects only new control attempts and future eligible runtimes.
	n.mu.Lock()
	n.snapshot = networkSnapshot{metadata, bundle}
	n.mu.Unlock()
	request.RequestId = string(domain.NewID())
	request.EffectiveGeneration = metadata.Generation
	request.RouteId = string(metadata.RouteID)
	request.CiphertextDigest = metadata.CiphertextDigest
	ack, err := client.SyncWorkerNetwork(ctx, authenticated(c, request))
	if err != nil {
		return rpc.ClientError(err)
	}
	if !ack.Msg.InSync || ack.Msg.Route == nil || ack.Msg.Route.Id != string(metadata.RouteID) || ack.Msg.Route.Revision != metadata.Generation {
		return domain.Fail(domain.Conflict, "The Worker route changed during synchronization.", "Keep control-only access and synchronize the latest generation before execution.")
	}
	return nil
}
func maintainWorkerNetwork(ctx context.Context, config Config, client delidevv1connect.WorkerServiceClient, c Credential, instance domain.ID) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := config.network.sync(bounded, client, c, instance)
		cancel()
		if err != nil {
			code := domain.SafeError(err).Code
			config.Logger.WarnContext(ctx, "worker_network_sync_failed", "machine_id", c.MachineID, "code", code)
			if code == domain.Unauthenticated || code == domain.PermissionDenied || code == domain.RecoveryRequired {
				return err
			}
		}
	}
}

func networkHTTPClientFor(ctx context.Context, config Config, credential Credential) (*http.Client, func(), error) {
	if config.network == nil {
		client, closer, err := networkHTTPClient(ctx, config.Root, credential)
		if err != nil {
			return nil, nil, err
		}
		return client, closer.CloseIdleConnections, nil
	}
	client, base := rpc.HTTPClient()
	client.Transport = &outbound.Transport{Base: base, Resolve: config.network.resolver}
	return client, base.CloseIdleConnections, nil
}
