package connections

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

const workerName = "DeliDev local Worker"

// Registration retains both remote grant issuance and local pairing ownership.
// It is private intent, never a product response. PairStarted is committed before
// Pair so a crash cannot authorize replacement of a lost request/token journal.
type workerRegistration struct {
	Version     uint32             `json:"version"`
	ProfileID   domain.ID          `json:"profile_id"`
	ClientID    domain.ID          `json:"client_id"`
	RequestID   domain.ID          `json:"request_id"`
	Grant       worker.PairingCode `json:"grant"`
	PairStarted bool               `json:"pair_started"`
	DeviceID    domain.ID          `json:"device_id,omitempty"`
	MachineID   domain.ID          `json:"machine_id,omitempty"`
}

func WorkerRoot(root string, id domain.ID) (string, error) {
	if err := id.Validate(); err != nil {
		return "", err
	}
	return filepath.Join(profileRoot(root, id), "worker"), nil
}
func readRegistration(root string, profile Metadata) (workerRegistration, error) {
	var value workerRegistration
	raw, err := security.ReadPrivate(filepath.Join(profileRoot(root, profile.ID), "local-worker.json"), 16<<10)
	if err != nil {
		return value, err
	}
	defer clear(raw)
	if domain.Decode(raw, &value) != nil || value.Version != 1 || value.ProfileID != profile.ID || value.ClientID != profile.DeviceID || value.RequestID.Validate() != nil || value.Grant.ServerID != profile.ServerID || value.Grant.Endpoint != profile.Endpoint {
		return workerRegistration{}, invalid()
	}
	grant := value.Grant
	if grant.PairingID == "" {
		// The grant's ID is returned only after durable server acceptance.
		grant.PairingID = value.RequestID
	}
	if validateGrant(grant) != nil || (value.PairStarted && value.Grant.PairingID == "") || (value.DeviceID == "") != (value.MachineID == "") || (value.DeviceID != "" && (!value.PairStarted || value.DeviceID.Validate() != nil || value.MachineID.Validate() != nil)) {
		return workerRegistration{}, invalid()
	}
	return value, nil
}
func writeRegistration(root string, value workerRegistration) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	defer clear(raw)
	return security.WriteAtomic(filepath.Join(profileRoot(root, value.ProfileID), "local-worker.json"), raw)
}
func registeredCredential(root string, value workerRegistration) (worker.Credential, error) {
	path, err := WorkerRoot(root, value.ProfileID)
	if err != nil {
		return worker.Credential{}, err
	}
	credential, err := worker.LoadCredential(path)
	if err != nil {
		return worker.Credential{}, err
	}
	if value.DeviceID == "" || credential.Type != domain.WorkerDevice || credential.ServerID != value.Grant.ServerID || credential.Endpoint != value.Grant.Endpoint || credential.PairingID != value.Grant.PairingID || credential.DeviceID != value.DeviceID || credential.MachineID != value.MachineID {
		return worker.Credential{}, invalid()
	}
	return credential, nil
}

// WorkerCredential is an offline, non-creating authority check. The caller must
// keep the token private; product mutations reauthenticate its exact provenance.
func WorkerCredential(root string, id domain.ID) (worker.Credential, error) {
	profile, err := Inspect(root, id)
	if err != nil {
		return worker.Credential{}, err
	}
	if profile.State != Paired && profile.State != Removing && profile.State != Removed {
		return worker.Credential{}, invalid()
	}
	value, err := readRegistration(root, profile)
	if err != nil {
		return worker.Credential{}, err
	}
	return registeredCredential(root, value)
}

// RegisterWorker uses only this profile's paired client to register this
// computer. It never opens owner state or provisions a remote machine/server.
func RegisterWorker(ctx context.Context, root string, id domain.ID) (worker.Credential, error) {
	var zero worker.Credential
	profile, err := Inspect(root, id)
	if err != nil {
		return zero, err
	}
	lock, err := security.TryLockExisting(filepath.Join(root, "connections.lock"))
	if err != nil {
		return zero, err
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	verification, err := Verify(ctx, root, id)
	if err != nil {
		return zero, err
	}
	if verification.Profile != profile {
		return zero, invalid()
	}
	client, err := worker.LoadCredential(filepath.Join(profileRoot(root, id), "client"))
	if err != nil {
		return zero, err
	}
	if client.Type != domain.ClientDevice || client.DeviceID != profile.DeviceID || client.ServerID != profile.ServerID || client.Endpoint != profile.Endpoint || client.PairingID != profile.PairingID {
		return zero, invalid()
	}
	path, err := WorkerRoot(root, id)
	if err != nil {
		return zero, err
	}
	value, err := readRegistration(root, profile)
	if errors.Is(err, os.ErrNotExist) {
		// No unexplained Worker directory may be adopted, including one whose
		// registration record was lost after remote acceptance.
		if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
			return zero, invalid()
		}
		code, err := worker.RandomToken()
		if err != nil {
			return zero, err
		}
		value = workerRegistration{Version: 1, ProfileID: id, ClientID: profile.DeviceID, RequestID: domain.NewID(), Grant: worker.PairingCode{Version: 1, ServerID: profile.ServerID, Endpoint: profile.Endpoint, Code: code}}
		if err := writeRegistration(root, value); err != nil {
			return zero, err
		}
	} else if err != nil {
		return zero, err
	}
	httpClient, transport := rpc.HTTPClient()
	defer transport.CloseIdleConnections()
	if value.DeviceID != "" {
		saved, err := registeredCredential(root, value)
		if err != nil {
			return zero, err
		}
		request := connect.NewRequest(&pb.GetStatusRequest{})
		request.Header().Set("Authorization", "Bearer "+saved.Token)
		status, err := delidevv1connect.NewSystemServiceClient(httpClient, profile.Endpoint, connect.WithReadMaxBytes(64<<10)).GetStatus(ctx, request)
		if err != nil {
			return zero, rpc.ClientError(err)
		}
		if status.Msg.ServerId != string(profile.ServerID) || status.Msg.ProtocolVersion != rpc.ProtocolVersion || status.Msg.Version != rpc.Version || status.Msg.Stopping {
			return zero, invalid()
		}
		return saved, nil
	}
	if value.Grant.PairingID == "" {
		digest := sha256.Sum256([]byte(value.Grant.Code))
		request := connect.NewRequest(&pb.CreatePairingRequest{RequestId: string(value.RequestID), Type: pb.DeviceType_DEVICE_TYPE_WORKER, Name: workerName, CodeDigest: digest[:]})
		request.Header().Set("Authorization", "Bearer "+client.Token)
		response, err := delidevv1connect.NewDeviceServiceClient(httpClient, profile.Endpoint, connect.WithReadMaxBytes(64<<10), connect.WithSendMaxBytes(64<<10)).CreatePairing(ctx, request)
		if err != nil {
			return zero, rpc.ClientError(err)
		}
		if response.Msg.Pairing == nil || domain.ID(response.Msg.Pairing.Id).Validate() != nil {
			return zero, invalid()
		}
		value.Grant.PairingID = domain.ID(response.Msg.Pairing.Id)
		if err := writeRegistration(root, value); err != nil {
			return zero, err
		}
	}
	pair := worker.RetryPair
	if !value.PairStarted {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return zero, invalid()
		}
		value.PairStarted = true
		if err := writeRegistration(root, value); err != nil {
			return zero, err
		}
		pair = worker.Pair
	}
	credential, err := pair(ctx, path, value.Grant, domain.WorkerDevice, workerName)
	if err != nil {
		return zero, err
	}
	value.DeviceID, value.MachineID = credential.DeviceID, credential.MachineID
	if credential.Type != domain.WorkerDevice || credential.ServerID != profile.ServerID || credential.Endpoint != profile.Endpoint || credential.PairingID != value.Grant.PairingID {
		return zero, invalid()
	}
	if err := writeRegistration(root, value); err != nil {
		return zero, err
	}
	return registeredCredential(root, value)
}
