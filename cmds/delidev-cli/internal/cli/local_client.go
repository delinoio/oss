package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func credentialMetadata(c worker.Credential) any {
	return map[string]any{"version": c.Version, "type": c.Type, "endpoint": c.Endpoint, "server_id": c.ServerID, "device_id": c.DeviceID, "machine_id": c.MachineID, "pairing_id": c.PairingID}
}

type localPairingAttempt struct {
	RequestID domain.ID `json:"request_id"`
	ServerID  domain.ID `json:"server_id"`
	Endpoint  string    `json:"endpoint"`
}

// pairLocalDevice is an explicit local bootstrap operation, never server
// startup. It uses the existing owner authority only inside Go and retains the
// exact grant request before contacting the server. A retry cannot silently
// replace a revoked, damaged or differently paired client.
func pairLocalDevice(ctx context.Context, o options, root string, kind domain.DeviceType) (any, error) {
	return pairLocalDeviceJoined(ctx, o, root, kind, false)
}

func pairLocalDeviceJoined(ctx context.Context, o options, root string, kind domain.DeviceType, join bool) (any, error) {
	return pairLocalDeviceAt(ctx, o, root, kind, join, "")
}

// The desktop's original endpoint is checked before creating pairing state or
// issuing a grant. A changed local listener never authorizes another pairing.
func pairLocalDeviceAt(ctx context.Context, o options, root string, kind domain.DeviceType, join bool, expected string) (any, error) {
	name := "DeliDev desktop"
	if kind == domain.WorkerDevice {
		name = "DeliDev local Worker"
	} else if kind != domain.ClientDevice {
		return nil, usage()
	}
	if o.server != "" || o.tokenStdin {
		return nil, domain.Fail(domain.InvalidArgument, "Local pairing requires the selected local owner scope.", "Use device pair with an explicit private grant for a remote server.")
	}
	ownerPath, ownerErr := filepath.Abs(o.dataDir)
	clientPath, clientErr := filepath.Abs(root)
	if ownerErr != nil || clientErr != nil || ownerPath == clientPath {
		return nil, domain.Fail(domain.InvalidArgument, "The device requires a separate private directory.", "Do not place a device credential in the server owner directory.")
	}
	if err := security.CheckPrivateDir(o.dataDir); err != nil {
		return nil, err
	}
	lockClient := lockDesktopClient
	if join {
		lockClient = func(owner, selected string) (*security.Lock, error) {
			return joinDesktopClient(ctx, owner, selected)
		}
	}
	recoveryLock, err := lockClient(o.dataDir, root)
	if err != nil {
		return nil, err
	}
	if recoveryLock != nil {
		defer recoveryLock.Close()
	}
	identity, err := security.LoadIdentity(o.dataDir)
	if err != nil {
		return nil, err
	}
	endpoint, err := localEndpoint(o)
	if err != nil {
		return nil, err
	}
	if expected != "" && endpoint.URL != expected {
		return nil, recoveryRequired()
	}
	if endpoint.Version != rpc.Version || endpoint.ProtocolVersion != rpc.ProtocolVersion {
		return nil, domain.Fail(domain.Unsupported, "The local server is incompatible with this client.", "Preserve running work and select a compatible client.")
	}
	parsed, err := url.Parse(endpoint.URL)
	if err != nil || (parsed.Hostname() != "localhost" && !net.ParseIP(parsed.Hostname()).IsLoopback()) ||
		domain.OwnershipBlocks(domain.OwnershipInstance, domain.ID(endpoint.ServerID), endpoint.ServerID != identity.ServerID) {
		return nil, domain.Fail(domain.Conflict, "The selected scope does not identify its local server.", "Inspect its original endpoint and owner identity without replacing either.")
	}
	if err := security.PrivateDir(root); err != nil {
		return nil, err
	}
	lock, err := security.TryLock(filepath.Join(root, "local-pairing.lock"))
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if saved, err := worker.LoadCredential(root); err == nil {
		if saved.Type != kind ||
			domain.OwnershipBlocks(domain.OwnershipInstance, domain.ID(saved.ServerID), saved.ServerID != identity.ServerID) ||
			!localEndpointMatches(o, saved.ServerID, saved.Endpoint, endpoint.URL) {
			return nil, domain.Fail(domain.Conflict, "The device is paired to a different authority.", "Use its original server or explicitly select another client scope.")
		}
		c, err := connectClient(options{dataDir: o.dataDir, server: saved.Endpoint, tokenStdin: true, desktop: o.desktop}, strings.NewReader(saved.Token))
		if err != nil {
			return nil, err
		}
		defer c.transport.CloseIdleConnections()
		status, err := c.system.GetStatus(ctx, request(c, &pb.GetStatusRequest{}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		if status.Msg.ServerId != string(saved.ServerID) || status.Msg.ProtocolVersion != rpc.ProtocolVersion || status.Msg.Version != rpc.Version {
			return nil, domain.Fail(domain.Unsupported, "The local server is incompatible with this client.", "Preserve running work and select a compatible client.")
		}
		return credentialMetadata(saved), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	path := filepath.Join(root, "local-pairing.json")
	var attempt localPairingAttempt
	raw, err := security.ReadPrivate(path, 4096)
	if errors.Is(err, os.ErrNotExist) {
		attempt.RequestID, attempt.ServerID, attempt.Endpoint = domain.NewID(), identity.ServerID, endpoint.URL
		raw, err = json.Marshal(attempt)
		if err != nil {
			return nil, err
		}
		if err := security.WriteAtomic(path, raw); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else {
		if err := domain.Decode(raw, &attempt); err != nil {
			return nil, err
		}
		if err := attempt.RequestID.Validate(); err != nil {
			return nil, err
		}
		if domain.OwnershipBlocks(domain.OwnershipInstance, domain.ID(attempt.ServerID), attempt.ServerID != identity.ServerID) ||
			!localEndpointMatches(o, attempt.ServerID, attempt.Endpoint, endpoint.URL) {
			return nil, domain.Fail(domain.Conflict, "A different local pairing attempt is retained.", "Restore its original authority or explicitly select a separate device scope.")
		}
	}
	o.requestID = attempt.RequestID
	if o.desktop != nil {
		o.server = attempt.Endpoint
	}
	c, err := connectClient(o, nil)
	if err != nil {
		return nil, err
	}
	defer c.transport.CloseIdleConnections()
	status, err := c.system.GetStatus(ctx, request(c, &pb.GetStatusRequest{}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	if status.Msg.ServerId != string(identity.ServerID) || status.Msg.ProtocolVersion != rpc.ProtocolVersion || status.Msg.Version != rpc.Version {
		return nil, domain.Fail(domain.Unsupported, "The local server is incompatible with this client.", "Preserve running work and select a compatible client.")
	}
	if _, err := deviceRemote(ctx, c, o, []string{"create-pairing", "--type", string(kind), "--name", name}); err != nil {
		return nil, err
	}
	raw, err = security.ReadPrivate(filepath.Join(o.dataDir, "pairing-codes", string(attempt.RequestID)+".json"), 32<<10)
	if err != nil {
		return nil, err
	}
	var grant worker.PairingCode
	if err := domain.Decode(raw, &grant); err != nil {
		return nil, err
	}
	if domain.OwnershipBlocks(domain.OwnershipInstance, domain.ID(grant.ServerID), grant.ServerID != attempt.ServerID) ||
		grant.Endpoint != attempt.Endpoint {
		return nil, domain.Fail(domain.Conflict, "The retained grant belongs to a different authority.", "Preserve the original local pairing attempt for inspection.")
	}
	var credential worker.Credential
	if kind == domain.ClientDevice && recoveryLock != nil {
		credential, err = worker.PairWithCommitment(ctx, root, grant, kind, name, func(candidate worker.Credential) error {
			if err := verifyDesktopCredential(ctx, o, candidate); err != nil {
				return err
			}
			return retainDesktopCredential(o.dataDir, candidate)
		})
	} else {
		credential, err = worker.Pair(ctx, root, grant, kind, name)
	}
	if err != nil {
		return nil, err
	}
	return credentialMetadata(credential), nil
}
