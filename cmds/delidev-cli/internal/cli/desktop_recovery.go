package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type desktopRegistrationState string

const (
	desktopAuthorized desktopRegistrationState = "authorized"
	desktopRevoked    desktopRegistrationState = "revoked"
	desktopRecovering desktopRegistrationState = "recovering"
)

type desktopRecoveryPhase string

const (
	recoveryPrepared   desktopRecoveryPhase = "prepared"
	recoveryGrant      desktopRecoveryPhase = "grant"
	recoveryPairing    desktopRecoveryPhase = "pairing"
	recoveryPublishing desktopRecoveryPhase = "publishing"
	recoveryComplete   desktopRecoveryPhase = "complete"
)

var desktopFiles = [...]string{"device.json", "local-pairing.json", "pairing-pending.json"}

type desktopRegistration struct {
	State     desktopRegistrationState `json:"state"`
	ServerID  domain.ID                `json:"server_id"`
	DeviceID  domain.ID                `json:"device_id"`
	Revision  string                   `json:"revision"`
	RequestID domain.ID                `json:"request_id,omitempty"`
}

// This metadata journal contains file commitments, never tokens. Original private
// files stay in the request-owned archive; candidate pairing uses its own scope.
type desktopRecovery struct {
	Version     int                  `json:"version"`
	RequestID   domain.ID            `json:"request_id"`
	ServerID    domain.ID            `json:"server_id"`
	Endpoint    string               `json:"endpoint"`
	DeviceID    domain.ID            `json:"device_id"`
	Revision    uint64               `json:"revision"`
	Phase       desktopRecoveryPhase `json:"phase"`
	Original    [3]string            `json:"original"`
	Replacement [3]string            `json:"replacement"`
}

type desktopCredentialCommitment struct {
	Version int    `json:"version"`
	Digest  string `json:"digest"`
}

// Retain the exact credential only after a new local pairing is authenticated.
// Revocation deletes the server's token verifier, so metadata alone cannot
// reconstruct this proof for a legacy or damaged credential after revocation.
func retainDesktopCredential(owner string, credential worker.Credential) error {
	// Use the same typed JSON encoding as worker.Pair's pending publication;
	// the proof must be durable before device.json can select the reuse branch.
	raw, err := json.Marshal(credential)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	clear(raw)
	digest := hex.EncodeToString(sum[:])
	path := filepath.Join(owner, "desktop-registration.json")
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return verifyOriginalDesktopCredential(owner, digest)
	}
	return writeRecoveryJSON(path, desktopCredentialCommitment{Version: 1, Digest: digest})
}

func verifyOriginalDesktopCredential(owner, digest string) error {
	raw, err := security.ReadPrivate(filepath.Join(owner, "desktop-registration.json"), 4096)
	if errors.Is(err, os.ErrNotExist) {
		return recoveryRequired()
	}
	if err != nil {
		return err
	}
	var commitment desktopCredentialCommitment
	if domain.Decode(raw, &commitment) != nil || commitment.Version != 1 || digest == "" || commitment.Digest != digest {
		return recoveryRequired()
	}
	return nil
}

func verifyOriginalDesktopPairing(owner, root string, saved worker.Credential) error {
	raw, err := security.ReadPrivate(filepath.Join(root, "local-pairing.json"), 4096)
	if errors.Is(err, os.ErrNotExist) {
		return recoveryRequired()
	}
	if err != nil {
		return err
	}
	var attempt localPairingAttempt
	if domain.Decode(raw, &attempt) != nil || attempt.RequestID.Validate() != nil ||
		domain.OwnershipBlocks(domain.OwnershipInstance, domain.ID(owner), attempt.ServerID != saved.ServerID) ||
		attempt.Endpoint != saved.Endpoint {
		return recoveryRequired()
	}
	grants := filepath.Join(owner, "pairing-codes")
	if err := security.CheckPrivateDir(grants); err != nil {
		return err
	}
	raw, err = security.ReadPrivate(filepath.Join(grants, string(attempt.RequestID)+".json"), 32<<10)
	defer clear(raw)
	if errors.Is(err, os.ErrNotExist) {
		return recoveryRequired()
	}
	if err != nil {
		return err
	}
	var grant worker.PairingCode
	if domain.Decode(raw, &grant) != nil || grant.Validate() != nil ||
		domain.OwnershipBlocks(domain.OwnershipInstance, domain.ID(owner), grant.ServerID != saved.ServerID) ||
		grant.Endpoint != saved.Endpoint || grant.PairingID != saved.PairingID {
		return recoveryRequired()
	}
	return nil
}

func recoveryRequired() error {
	return domain.Fail(domain.RecoveryRequired, "The original desktop recovery evidence is unavailable or changed.", "Preserve the original registration and private recovery files; do not reset the server.")
}
func writeRecoveryJSON(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	defer clear(raw)
	return security.WriteAtomic(path, raw)
}
func loadDesktopRecovery(root string) (*desktopRecovery, error) {
	raw, err := security.ReadPrivate(filepath.Join(root, "desktop-recovery.json"), 8192)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var value desktopRecovery
	if domain.Decode(raw, &value) != nil || value.Version != 1 || value.RequestID.Validate() != nil || value.ServerID.Validate() != nil || value.DeviceID.Validate() != nil || value.Revision == 0 || rpc.ValidateEndpoint(value.Endpoint) != nil {
		return nil, recoveryRequired()
	}
	switch value.Phase {
	case recoveryPrepared, recoveryGrant, recoveryPairing, recoveryPublishing, recoveryComplete:
	default:
		return nil, recoveryRequired()
	}
	for _, hashes := range [][3]string{value.Original, value.Replacement} {
		for _, hash := range hashes {
			if hash != "" {
				bytes, err := hex.DecodeString(hash)
				if err != nil || len(bytes) != 32 || hex.EncodeToString(bytes) != hash {
					return nil, recoveryRequired()
				}
			}
		}
	}
	if value.Original[0] == "" || value.Original[1] == "" || ((value.Phase == recoveryPublishing || value.Phase == recoveryComplete) && (value.Replacement[0] == "" || value.Replacement[1] == "" || value.Replacement[2] != "")) {
		return nil, recoveryRequired()
	}
	if value.Phase == recoveryComplete {
		if err := verifyDesktopReceipt(root, &value); err != nil {
			return nil, err
		}
	}
	return &value, nil
}
func verifyDesktopReceipt(root string, record *desktopRecovery) error {
	history := filepath.Join(root, "desktop-recoveries")
	stage := filepath.Join(history, string(record.RequestID))
	for _, path := range []string{history, stage} {
		if err := security.CheckPrivateDir(path); err != nil {
			return recoveryRequired()
		}
	}
	raw, err := security.ReadPrivate(filepath.Join(stage, "receipt.json"), 8192)
	if err != nil {
		return recoveryRequired()
	}
	expected, err := json.Marshal(record)
	if err != nil || !bytes.Equal(raw, expected) {
		return recoveryRequired()
	}
	return nil
}
func desktopFile(root, name string) ([]byte, string, error) {
	raw, err := security.ReadPrivate(filepath.Join(root, name), 32<<10)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}
func desktopHashes(root string) ([3]string, error) {
	var hashes [3]string
	if err := security.CheckPrivateDir(root); err != nil {
		return hashes, err
	}
	for i, name := range desktopFiles {
		raw, hash, err := desktopFile(root, name)
		clear(raw)
		if err != nil {
			return hashes, err
		}
		hashes[i] = hash
	}
	return hashes, nil
}

// Share a stable lock with ordinary fixed-scope pairing and inspection. In
// particular a partially published credential must never trigger fresh pairing.
// Native host bootstrap/observation joins this exact original lock, rather than
// retrying a pairing operation after Conflict. Recovery validation still runs
// after acquisition and can never be replaced or interpreted as contention.
func joinDesktopClient(ctx context.Context, owner, selected string) (*security.Lock, error) {
	child, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := child.Err(); err != nil {
			return nil, domain.SafeError(err)
		}
		lock, err := lockDesktopClient(owner, selected)
		if err == nil && child.Err() != nil {
			if lock != nil {
				lock.Close()
			}
			return nil, domain.SafeError(child.Err())
		}
		if err == nil || domain.SafeError(err).Code != domain.Conflict {
			return lock, err
		}
		select {
		case <-child.Done():
			return nil, domain.SafeError(child.Err())
		case <-ticker.C:
		}
	}
}

func lockDesktopClient(owner, selected string) (*security.Lock, error) {
	expected, err := filepath.Abs(filepath.Join(owner, "desktop-client"))
	if err != nil {
		return nil, err
	}
	actual, err := filepath.Abs(selected)
	if err != nil {
		return nil, err
	}
	if expected != actual {
		return nil, nil
	}
	if err := security.CheckPrivateDir(owner); err != nil {
		return nil, err
	}
	lock, err := security.TryLock(filepath.Join(owner, "desktop-recovery.lock"))
	if err != nil {
		return nil, err
	}
	record, err := loadDesktopRecovery(owner)
	if err == nil && record != nil {
		if record.Phase != recoveryComplete {
			err = recoveryRequired()
		} else {
			var hashes [3]string
			hashes, err = desktopHashes(selected)
			if err == nil && hashes != record.Replacement {
				err = recoveryRequired()
			}
		}
	}
	if err != nil {
		lock.Close()
		return nil, err
	}
	return lock, nil
}
func desktopOwner(ctx context.Context, o options) (client, domain.ID, error) {
	if o.server != "" || o.tokenStdin {
		return client{}, "", domain.Fail(domain.InvalidArgument, "Desktop recovery requires the original local owner scope.", "Omit remote and stdin authentication overrides.")
	}
	if err := security.CheckPrivateDir(o.dataDir); err != nil {
		return client{}, "", err
	}
	identity, err := security.LoadIdentity(o.dataDir)
	if errors.Is(err, os.ErrNotExist) {
		return client{}, "", domain.Fail(domain.Unauthenticated, "The original local owner credential is unavailable.", "Restore the original owner credential without resetting the server.")
	}
	if err != nil {
		return client{}, "", err
	}
	endpoint, err := server.LoadEndpoint(o.dataDir)
	if err != nil {
		return client{}, "", err
	}
	parsed, err := url.Parse(endpoint.URL)
	if err != nil ||
		domain.OwnershipBlocks(domain.OwnershipInstance, domain.ID(endpoint.ServerID), endpoint.ServerID != identity.ServerID) ||
		(parsed.Hostname() != "localhost" && !net.ParseIP(parsed.Hostname()).IsLoopback()) {
		return client{}, "", recoveryRequired()
	}
	if endpoint.Version != rpc.Version || endpoint.ProtocolVersion != rpc.ProtocolVersion {
		return client{}, "", domain.Fail(domain.Unsupported, "The local server is incompatible.", "Use its matching desktop client.")
	}
	c, err := connectClient(options{dataDir: o.dataDir, server: endpoint.URL, tokenStdin: true}, strings.NewReader(identity.Token))
	if err != nil {
		return client{}, "", err
	}
	status, err := c.system.GetStatus(ctx, request(c, &pb.GetStatusRequest{}))
	if err != nil {
		c.transport.CloseIdleConnections()
		return client{}, "", rpc.ClientError(err)
	}
	if status.Msg.ServerId != string(identity.ServerID) || status.Msg.Version != rpc.Version || status.Msg.ProtocolVersion != rpc.ProtocolVersion || status.Msg.Stopping {
		c.transport.CloseIdleConnections()
		return client{}, "", recoveryRequired()
	}
	return c, identity.ServerID, nil
}
func desktopDevice(ctx context.Context, c client, id domain.ID) (domain.Device, uint64, error) {
	result, err := c.resources.GetResource(ctx, request(c, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_DEVICE, Id: string(id)}))
	if err != nil {
		return domain.Device{}, 0, rpc.ClientError(err)
	}
	r := result.Msg.Resource
	var device domain.Device
	if r == nil || r.Id != string(id) || r.Kind != pb.EntityKind_ENTITY_KIND_DEVICE || r.SchemaVersion != 1 || r.Revision == 0 || domain.Decode(r.DocumentJson, &device) != nil || device.Validate() != nil || domain.OwnershipBlocks(domain.OwnershipActor, domain.ID(id), device.Type != domain.ClientDevice) || device.MachineID != "" || (device.Revoked && device.RevokedAt == nil) {
		return device, 0, recoveryRequired()
	}
	return device, r.Revision, nil
}
func desktopRecoveryCommand(ctx context.Context, o options, args []string) (any, error) {
	fs := flags("device " + args[0])
	expectedEndpoint := fs.String("expected-endpoint", "", "required local endpoint (checked before recovery)")
	var id *string
	var revision *uint64
	if args[0] == "recover-local" {
		id = fs.String("id", "", "original revoked desktop ID")
		revision = fs.Uint64("revision", 0, "original revoked revision")
	}
	if err := parse(fs, args[1:]); err != nil {
		return nil, err
	}
	if *expectedEndpoint != "" {
		if err := rpc.ValidateEndpoint(*expectedEndpoint); err != nil {
			return nil, err
		}
	}
	if id != nil && (domain.ID(*id).Validate() != nil || *revision == 0 || o.requestID.Validate() != nil) {
		return nil, domain.Fail(domain.MissingInput, "Recovery requires the original device ID, revision and explicit request ID.", "Inspect local registration and retain one request ID for every retry.")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	c, serverID, err := desktopOwner(ctx, o)
	if err != nil {
		return nil, err
	}
	defer c.transport.CloseIdleConnections()
	// The desktop pins its supported endpoint inside the native capability. This
	// guard shares the exact owner-authorized client used below, so a separate
	// preflight read cannot race into a replacement on another listener.
	if *expectedEndpoint != "" && c.endpoint != *expectedEndpoint {
		slog.Warn("desktop_recovery", "request_id", o.requestID, "phase", "preflight", "code", domain.Unsupported)
		return nil, domain.Fail(domain.Unsupported, "The local server endpoint is incompatible with this desktop.", "Use the original server's matching client without replacing its registration.")
	}
	lock, err := security.TryLock(filepath.Join(o.dataDir, "desktop-recovery.lock"))
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	record, err := loadDesktopRecovery(o.dataDir)
	if err != nil {
		return nil, err
	}
	if record != nil && (record.ServerID != serverID || record.Endpoint != c.endpoint) {
		return nil, recoveryRequired()
	}
	root := filepath.Join(o.dataDir, "desktop-client")
	if id == nil {
		if record != nil && record.Phase != recoveryComplete {
			device, rev, err := desktopDevice(ctx, c, record.DeviceID)
			if err != nil {
				return nil, err
			}
			if !device.Revoked || rev != record.Revision {
				return nil, recoveryRequired()
			}
			return desktopRegistration{desktopRecovering, serverID, record.DeviceID, strconv.FormatUint(rev, 10), record.RequestID}, nil
		}
		saved, err := worker.LoadCredential(root)
		if err != nil {
			return nil, err
		}
		if domain.OwnershipBlocks(domain.OwnershipActor, "", saved.Type != domain.ClientDevice) ||
			domain.OwnershipBlocks(domain.OwnershipInstance, domain.ID(saved.ServerID), saved.ServerID != serverID) ||
			saved.Endpoint != c.endpoint {
			return nil, recoveryRequired()
		}
		if record != nil {
			hashes, err := desktopHashes(root)
			if err != nil {
				return nil, err
			}
			if hashes != record.Replacement {
				return nil, recoveryRequired()
			}
		}
		device, rev, err := desktopDevice(ctx, c, saved.DeviceID)
		if err != nil {
			return nil, err
		}
		state := desktopAuthorized
		if device.Revoked {
			if err := verifyOriginalDesktopPairing(o.dataDir, root, saved); err != nil {
				return nil, err
			}
			if record == nil {
				raw, digest, err := desktopFile(root, "device.json")
				clear(raw)
				if err != nil {
					return nil, err
				}
				if err := verifyOriginalDesktopCredential(o.dataDir, digest); err != nil {
					return nil, err
				}
			}
			state = desktopRevoked
		} else if err := verifyDesktopCredential(ctx, o, saved); err != nil {
			return nil, err
		}
		return desktopRegistration{State: state, ServerID: serverID, DeviceID: saved.DeviceID, Revision: strconv.FormatUint(rev, 10)}, nil
	}
	result, err := recoverDesktop(ctx, o, c, serverID, domain.ID(*id), *revision, record)
	if err != nil {
		slog.Warn("desktop_recovery", "request_id", o.requestID, "phase", "failed", "code", domain.SafeError(err).Code)
	}
	return result, err
}
func recoverDesktop(ctx context.Context, o options, c client, serverID, id domain.ID, revision uint64, record *desktopRecovery) (any, error) {
	root := filepath.Join(o.dataDir, "desktop-client")
	if err := security.CheckPrivateDir(root); err != nil {
		return nil, err
	}
	// Keep the existing lock files in place: moving a locked directory permits a
	// second process to acquire a different inode, and fails on Windows.
	for _, name := range []string{"local-pairing.lock", "pairing.lock"} {
		lock, err := security.TryLock(filepath.Join(root, name))
		if err != nil {
			return nil, err
		}
		defer lock.Close()
	}
	history := filepath.Join(o.dataDir, "desktop-recoveries")
	if err := security.PrivateDir(history); err != nil {
		return nil, err
	}
	stage := filepath.Join(history, string(o.requestID))
	if record == nil || record.RequestID != o.requestID {
		if record != nil && record.Phase != recoveryComplete {
			return nil, domain.Fail(domain.Conflict, "The original desktop recovery is still pending.", "Inspect local registration and retry its original request.")
		}
		if _, err := os.Lstat(stage); !errors.Is(err, os.ErrNotExist) {
			return nil, recoveryRequired()
		}
		entries, err := os.ReadDir(history)
		if err != nil {
			return nil, err
		}
		if len(entries) >= 256 {
			return nil, domain.Fail(domain.ResourceExhausted, "Desktop recovery history is full.", "Preserve and inspect the original recovery history.")
		}
		saved, err := worker.LoadCredential(root)
		if err != nil {
			return nil, err
		}
		if domain.OwnershipBlocks(domain.OwnershipActor, domain.ID(id), saved.Type != domain.ClientDevice) ||
			domain.OwnershipBlocks(domain.OwnershipInstance, domain.ID(id), saved.ServerID != serverID) ||
			saved.Endpoint != c.endpoint ||
			domain.OwnershipBlocks(domain.OwnershipDevice, domain.ID(id), saved.DeviceID != id) {
			return nil, recoveryRequired()
		}
		if err := verifyOriginalDesktopPairing(o.dataDir, root, saved); err != nil {
			return nil, err
		}
		device, rev, err := desktopDevice(ctx, c, id)
		if err != nil {
			return nil, err
		}
		if !device.Revoked || rev != revision {
			return nil, domain.Fail(domain.Conflict, "This desktop registration is not the original revoked revision.", "Inspect local registration again before confirming recovery.")
		}
		hashes, err := desktopHashes(root)
		if err != nil {
			return nil, err
		}
		if record != nil && hashes != record.Replacement {
			return nil, recoveryRequired()
		}
		if record == nil {
			if err := verifyOriginalDesktopCredential(o.dataDir, hashes[0]); err != nil {
				return nil, err
			}
		}
		record = &desktopRecovery{Version: 1, RequestID: o.requestID, ServerID: serverID, Endpoint: c.endpoint, DeviceID: id, Revision: revision, Phase: recoveryPrepared, Original: hashes}
		if err := writeRecoveryJSON(filepath.Join(o.dataDir, "desktop-recovery.json"), record); err != nil {
			return nil, err
		}
	}
	if domain.OwnershipBlocks(domain.OwnershipDevice, domain.ID(id), record.DeviceID != id) ||
		record.Revision != revision {
		return nil, domain.Fail(domain.Conflict, "Recovery parameters differ from the original request.", "Retry its original device ID and revision.")
	}
	device, rev, err := desktopDevice(ctx, c, id)
	if err != nil {
		return nil, err
	}
	if !device.Revoked || rev != revision {
		return nil, recoveryRequired()
	}
	if err := security.PrivateDir(stage); err != nil {
		return nil, err
	}
	if err := security.SyncParent(stage); err != nil {
		return nil, err
	}
	archive := filepath.Join(stage, "original")
	candidate := filepath.Join(stage, "candidate")
	for _, path := range []string{archive, candidate} {
		if err := security.PrivateDir(path); err != nil {
			return nil, err
		}
		if err := security.SyncParent(path); err != nil {
			return nil, err
		}
	}
	for i, name := range desktopFiles {
		old, hash, err := desktopFile(archive, name)
		clear(old)
		if err != nil {
			return nil, err
		}
		if hash == "" && record.Original[i] != "" && record.Phase == recoveryPrepared {
			raw, original, err := desktopFile(root, name)
			if err != nil {
				return nil, err
			}
			if original != record.Original[i] {
				clear(raw)
				return nil, recoveryRequired()
			}
			err = security.WriteAtomic(filepath.Join(archive, name), raw)
			clear(raw)
			if err != nil {
				return nil, err
			}
			hash = original
		}
		if hash != record.Original[i] {
			return nil, recoveryRequired()
		}
	}
	original, err := worker.LoadCredential(archive)
	if err != nil {
		return nil, err
	}
	if err := verifyOriginalDesktopPairing(o.dataDir, archive, original); err != nil {
		return nil, err
	}
	savePhase := func(phase desktopRecoveryPhase) error {
		record.Phase = phase
		if err := writeRecoveryJSON(filepath.Join(o.dataDir, "desktop-recovery.json"), record); err != nil {
			return err
		}
		slog.Info("desktop_recovery", "request_id", record.RequestID, "phase", phase)
		return nil
	}
	if record.Phase == recoveryPrepared || record.Phase == recoveryGrant || record.Phase == recoveryPairing {
		hashes, err := desktopHashes(root)
		if err != nil {
			return nil, err
		}
		if hashes != record.Original {
			return nil, recoveryRequired()
		}
		firstGrant := record.Phase == recoveryPrepared
		if firstGrant {
			if err := savePhase(recoveryGrant); err != nil {
				return nil, err
			}
		}
		if record.Phase == recoveryGrant {
			if !firstGrant {
				// Lost grant ownership cannot be replaced even if no response was
				// observed. The server may already have accepted its code digest.
				raw, err := security.ReadPrivate(filepath.Join(o.dataDir, "pairing-codes", string(o.requestID)+".pending.json"), 32<<10)
				clear(raw)
				if errors.Is(err, os.ErrNotExist) {
					return nil, recoveryRequired()
				}
				if err != nil {
					return nil, err
				}
			}
			if _, err := deviceRemote(ctx, c, o, []string{"create-pairing", "--type", "client", "--name", "DeliDev desktop"}); err != nil {
				return nil, err
			}
		}
		raw, err := security.ReadPrivate(filepath.Join(o.dataDir, "pairing-codes", string(o.requestID)+".json"), 32<<10)
		if err != nil {
			return nil, err
		}
		var grant worker.PairingCode
		err = domain.Decode(raw, &grant)
		clear(raw)
		if err != nil || grant.Validate() != nil ||
			domain.OwnershipBlocks(domain.OwnershipInstance, domain.ID(id), grant.ServerID != serverID) ||
			grant.Endpoint != c.endpoint {
			return nil, recoveryRequired()
		}
		first := record.Phase == recoveryGrant
		if first {
			if err := savePhase(recoveryPairing); err != nil {
				return nil, err
			}
		}
		// After the barrier, a missing original pairing journal cannot prove that
		// PairDevice was never accepted. RetryPair refuses to mint another token.
		pair := worker.RetryPair
		if first {
			pair = worker.Pair
		}
		saved, err := pair(ctx, candidate, grant, domain.ClientDevice, "DeliDev desktop")
		if err != nil {
			return nil, err
		}
		if saved.DeviceID == id {
			return nil, recoveryRequired()
		}
		attempt := localPairingAttempt{o.requestID, serverID, c.endpoint}
		if err := writeRecoveryJSON(filepath.Join(candidate, "local-pairing.json"), attempt); err != nil {
			return nil, err
		}
		record.Replacement, err = desktopHashes(candidate)
		if err != nil {
			return nil, err
		}
		if record.Replacement[2] != "" {
			return nil, recoveryRequired()
		}
		if err := savePhase(recoveryPublishing); err != nil {
			return nil, err
		}
	}
	hashes, err := desktopHashes(candidate)
	if err != nil {
		return nil, err
	}
	if hashes != record.Replacement {
		return nil, recoveryRequired()
	}
	saved, err := worker.LoadCredential(candidate)
	if err != nil {
		return nil, err
	}
	if domain.OwnershipBlocks(domain.OwnershipActor, domain.ID(id), saved.Type != domain.ClientDevice) ||
		domain.OwnershipBlocks(domain.OwnershipInstance, domain.ID(id), saved.ServerID != serverID) ||
		saved.Endpoint != c.endpoint || saved.DeviceID == id {
		return nil, recoveryRequired()
	}
	// A successful PairDevice receipt alone cannot restore a subsequently revoked
	// replacement. Prove current authorization before publishing or returning it.
	if err := verifyDesktopCredential(ctx, o, saved); err != nil {
		return nil, err
	}
	hashes, err = desktopHashes(root)
	if err != nil {
		return nil, err
	}
	for i := range desktopFiles {
		if hashes[i] != record.Replacement[i] && (record.Phase == recoveryComplete || hashes[i] != record.Original[i]) {
			return nil, recoveryRequired()
		}
	}
	if record.Phase == recoveryPublishing {
		// Publish the credential last. Pending recovery blocks ordinary bootstrap
		// until every fixed file and the completion marker have been synchronized.
		for _, i := range []int{1, 2, 0} {
			name := desktopFiles[i]
			if record.Replacement[i] == "" {
				if err := os.Remove(filepath.Join(root, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
					return nil, err
				}
				if err := security.SyncParent(filepath.Join(root, name)); err != nil {
					return nil, err
				}
			} else {
				raw, hash, err := desktopFile(candidate, name)
				if err != nil {
					return nil, err
				}
				if hash != record.Replacement[i] {
					clear(raw)
					return nil, recoveryRequired()
				}
				err = security.WriteAtomic(filepath.Join(root, name), raw)
				clear(raw)
				if err != nil {
					return nil, err
				}
			}
		}
		// Retain the immutable receipt before the completion marker can permit
		// another recovery. A lost completion acknowledgment cannot erase history.
		completed := *record
		completed.Phase = recoveryComplete
		if err := writeRecoveryJSON(filepath.Join(stage, "receipt.json"), &completed); err != nil {
			return nil, err
		}
		if err := savePhase(recoveryComplete); err != nil {
			return nil, err
		}
	}
	if err := verifyDesktopReceipt(o.dataDir, record); err != nil {
		return nil, err
	}
	return credentialMetadata(saved), nil
}

func verifyDesktopCredential(ctx context.Context, o options, saved worker.Credential) error {
	paired, err := connectClient(options{dataDir: o.dataDir, server: saved.Endpoint, tokenStdin: true}, strings.NewReader(saved.Token))
	if err != nil {
		return err
	}
	defer paired.transport.CloseIdleConnections()
	status, err := paired.system.GetStatus(ctx, request(paired, &pb.GetStatusRequest{}))
	if err != nil {
		return rpc.ClientError(err)
	}
	if status.Msg.ServerId != string(saved.ServerID) || status.Msg.Version != rpc.Version || status.Msg.ProtocolVersion != rpc.ProtocolVersion || status.Msg.Stopping {
		return recoveryRequired()
	}
	return nil
}
