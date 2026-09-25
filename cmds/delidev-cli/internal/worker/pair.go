// Package worker owns an execution machine's private journals and outbound
// connection. It never opens the server SQLite database or owner credential.
package worker

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type PairingCode struct {
	Version   int       `json:"version"`
	PairingID domain.ID `json:"pairing_id"`
	ServerID  domain.ID `json:"server_id"`
	Endpoint  string    `json:"endpoint"`
	Code      string    `json:"code"`
}
type Credential struct {
	Version   int               `json:"version"`
	Type      domain.DeviceType `json:"type"`
	Endpoint  string            `json:"endpoint"`
	ServerID  domain.ID         `json:"server_id"`
	DeviceID  domain.ID         `json:"device_id"`
	MachineID domain.ID         `json:"machine_id,omitempty"`
	PairingID domain.ID         `json:"pairing_id"`
	Token     string            `json:"token"`
}
type pendingPair struct {
	RequestID  domain.ID       `json:"request_id"`
	Grant      PairingCode     `json:"grant"`
	Credential Credential      `json:"credential"`
	Machine    json.RawMessage `json:"machine,omitempty"`
}

func RandomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func validToken(value string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(raw) == 32 && base64.RawURLEncoding.EncodeToString(raw) == value
}
func (g PairingCode) Validate() error {
	if g.Version != 1 || !validToken(g.Code) {
		return domain.Fail(domain.InvalidArgument, "Invalid pairing code document.", "Read an unexpired private pairing code through stdin.")
	}
	for _, id := range []domain.ID{g.PairingID, g.ServerID} {
		if err := id.Validate(); err != nil {
			return err
		}
	}
	return rpc.ValidateEndpoint(g.Endpoint)
}
func (c Credential) Validate() error {
	if c.Version != 1 || !validToken(c.Token) {
		return domain.Fail(domain.RecoveryRequired, "The device credential file is invalid.", "Preserve the scope and pair a new device explicitly.")
	}
	if err := rpc.ValidateEndpoint(c.Endpoint); err != nil {
		return err
	}
	for _, id := range []domain.ID{c.ServerID, c.DeviceID, c.PairingID} {
		if err := id.Validate(); err != nil {
			return err
		}
	}
	if c.Type == domain.WorkerDevice {
		return c.MachineID.Validate()
	}
	if c.Type != domain.ClientDevice || c.MachineID != "" {
		return domain.Fail(domain.InvalidArgument, "Invalid paired device type.", "Use the correct device scope.")
	}
	return nil
}
func credentialPath(root string) string { return filepath.Join(root, "device.json") }
func LoadCredential(root string) (Credential, error) {
	var value Credential
	if err := security.CheckPrivateDir(root); err != nil {
		return value, err
	}
	raw, err := security.ReadPrivate(credentialPath(root), 16<<10)
	if err != nil {
		return value, err
	}
	if err := domain.Decode(raw, &value); err != nil {
		return value, err
	}
	return value, value.Validate()
}
func writeJSON(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return security.WriteAtomic(path, raw)
}

// Pair journals the locally generated token and request identity before network
// acceptance. A lost response can therefore retry exactly without creating a
// new device or replacing an existing credential.
func Pair(ctx context.Context, root string, grant PairingCode, kind domain.DeviceType, name string) (Credential, error) {
	var zero Credential
	if err := grant.Validate(); err != nil {
		return zero, err
	}
	if kind != domain.WorkerDevice && kind != domain.ClientDevice {
		return zero, domain.Fail(domain.InvalidArgument, "Unknown pairing type.", "Select client or worker.")
	}
	if err := security.PrivateDir(root); err != nil {
		return zero, err
	}
	lock, err := security.TryLock(filepath.Join(root, "pairing.lock"))
	if err != nil {
		return zero, err
	}
	defer lock.Close()
	if saved, err := LoadCredential(root); err == nil {
		if saved.PairingID == grant.PairingID && saved.ServerID == grant.ServerID && saved.Endpoint == grant.Endpoint && saved.Type == kind {
			return saved, nil
		}
		return zero, domain.Fail(domain.Conflict, "This device scope is already paired.", "Select a separate private scope for another device.")
	} else if !errors.Is(err, os.ErrNotExist) {
		return zero, err
	}
	path := filepath.Join(root, "pairing-pending.json")
	var pending pendingPair
	raw, err := security.ReadPrivate(path, 32<<10)
	if errors.Is(err, os.ErrNotExist) {
		token, err := RandomToken()
		if err != nil {
			return zero, err
		}
		pending = pendingPair{RequestID: domain.NewID(), Grant: grant, Credential: Credential{Version: 1, Type: kind, Endpoint: grant.Endpoint, ServerID: grant.ServerID, DeviceID: domain.NewID(), PairingID: grant.PairingID, Token: token}}
		if kind == domain.WorkerDevice {
			if err := domain.Text(name, "machine name", 256, true); err != nil {
				return zero, err
			}
			pending.Credential.MachineID = domain.NewID()
			machine := domain.Machine{Name: name, OS: runtime.GOOS, Architecture: runtime.GOARCH, Version: rpc.Version}
			if err := machine.Validate(); err != nil {
				return zero, err
			}
			pending.Machine, _ = json.Marshal(machine)
		}
		if err := writeJSON(path, pending); err != nil {
			return zero, err
		}
	} else if err != nil {
		return zero, err
	} else {
		if err := domain.Decode(raw, &pending); err != nil {
			return zero, err
		}
		if pending.Grant != grant || pending.Credential.Type != kind {
			return zero, domain.Fail(domain.Conflict, "A different pairing attempt is pending in this scope.", "Retry the original private pairing document or use a separate scope.")
		}
		if err := pending.Credential.Validate(); err != nil {
			return zero, err
		}
	}
	httpClient, transport := rpc.HTTPClient()
	defer transport.CloseIdleConnections()
	client := delidevv1connect.NewDeviceServiceClient(httpClient, grant.Endpoint, connect.WithReadMaxBytes(2<<20), connect.WithSendMaxBytes(2<<20))
	digest := sha256.Sum256([]byte(pending.Credential.Token))
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	response, err := client.PairDevice(ctx, connect.NewRequest(&pb.PairDeviceRequest{RequestId: string(pending.RequestID), PairingId: string(grant.PairingID), Code: grant.Code, DeviceId: string(pending.Credential.DeviceID), CredentialDigest: digest[:], MachineId: string(pending.Credential.MachineID), MachineJson: pending.Machine}))
	if err != nil {
		return zero, rpc.ClientError(err)
	}
	if response.Msg.ServerId != string(grant.ServerID) || response.Msg.Device == nil || response.Msg.Device.Id != string(pending.Credential.DeviceID) || (kind == domain.WorkerDevice && (response.Msg.Machine == nil || response.Msg.Machine.Id != string(pending.Credential.MachineID))) {
		return zero, domain.Fail(domain.RecoveryRequired, "Pairing acknowledged a different device or server identity.", "Preserve this private scope and inspect the selected server.")
	}
	if err := writeJSON(credentialPath(root), pending.Credential); err != nil {
		return zero, err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return zero, err
	}
	return pending.Credential, nil
}
