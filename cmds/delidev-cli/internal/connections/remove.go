package connections

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type RemovalMetadata struct {
	RequestID        domain.ID `json:"request_id"`
	ExpectedRevision uint64    `json:"expected_revision"`
}

type removalReceipt struct {
	RequestID        domain.ID `json:"request_id"`
	ExpectedRevision uint64    `json:"expected_revision"`
	StartedAt        time.Time `json:"started_at"`
	Complete         bool      `json:"complete"`
	// Empty means the original file did not exist. Pin files independently so
	// retry cannot erase a replacement credential or reconstruct missing state.
	CredentialDigest string `json:"credential_digest"`
	PendingDigest    string `json:"pending_digest"`
}

func removed() error {
	return domain.Fail(domain.Conflict, "This saved client connection has been removed.", "Complete its original local cleanup if pending. A new pairing requires a new connection ID; server sessions and independent Workers remain retained.")
}
func requestConflict() error {
	return domain.Fail(domain.Conflict, "This connection request already belongs to another operation.", "Retry its exact original connection, revision and input.")
}
func validRemovalDigest(value string) bool {
	if value == "" {
		return true
	}
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == sha256.Size && hex.EncodeToString(raw) == value
}
func (r record) validateGrantState() error {
	if r.Removal == nil {
		return validateGrant(r.Grant)
	}
	v := r.Removal
	if r.Grant.Code != "" || r.Grant.Version != 1 || r.Grant.ServerID.Validate() != nil || r.Grant.PairingID.Validate() != nil || validateOrigin(r.Grant.Endpoint) != nil || v.RequestID.Validate() != nil || v.ExpectedRevision != uint64(len(r.Renames))+1 || v.StartedAt.IsZero() || !validRemovalDigest(v.CredentialDigest) || !validRemovalDigest(v.PendingDigest) {
		return invalid()
	}
	for _, rename := range r.Renames {
		if rename.RequestID == v.RequestID {
			return invalid()
		}
	}
	return nil
}
func fileDigest(path string) (string, error) {
	raw, err := security.ReadPrivate(path, 32<<10)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer clear(raw)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func writeRecord(root string, value record) error {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > maxProfileRecordBytes+4096 {
		return invalid()
	}
	defer clear(raw)
	return security.WriteAtomic(filepath.Join(profileRoot(root, value.ID), "connection.json"), raw)
}

// Remove forgets only this local client. The durable marker precedes cleanup
// and strips the original grant. Remote device revocation is a separate explicit
// server operation; neither server sessions nor profile-owned Workers are stopped.
func Remove(ctx context.Context, root string, id, request domain.ID, revision uint64) (Metadata, error) {
	if request.Validate() != nil || revision == 0 {
		return Metadata{}, domain.Fail(domain.InvalidArgument, "An original request and connection revision are required.", "Inspect the connection before confirming removal.")
	}
	if _, err := load(root, id); err != nil {
		return Metadata{}, err
	}
	lock, err := security.TryLockExisting(filepath.Join(root, "connections.lock"))
	if err != nil {
		return Metadata{}, err
	}
	defer lock.Close()
	value, err := load(root, id)
	if err != nil {
		return Metadata{}, err
	}
	if err := ctx.Err(); err != nil {
		return Metadata{}, domain.SafeError(err)
	}
	values, err := records(root)
	if err != nil {
		return Metadata{}, err
	}
	for _, other := range values {
		for _, rename := range other.Renames {
			if rename.RequestID == request {
				return Metadata{}, requestConflict()
			}
		}
		if other.Removal != nil && other.Removal.RequestID == request && (other.ID != id || other.Removal.ExpectedRevision != revision) {
			return Metadata{}, requestConflict()
		}
	}
	if value.Removal != nil {
		if value.Removal.RequestID != request || value.Removal.ExpectedRevision != revision {
			return Metadata{}, requestConflict()
		}
		if value.Removal.Complete {
			return inspect(root, value)
		}
	} else {
		profile, err := inspect(root, value)
		if err != nil {
			return Metadata{}, err
		}
		if profile.Revision != revision {
			return Metadata{}, domain.Fail(domain.Conflict, "The connection changed before removal.", "Refresh and confirm its current name and revision; no cleanup was performed.")
		}
	}
	client := filepath.Join(profileRoot(root, id), "client")
	if err := security.CheckPrivateDir(client); err == nil {
		// Use the same native pairing lock as direct pairing; hold it through
		// marker publication and deletion. This creates no credential/journal.
		pairLock, err := security.TryLock(filepath.Join(client, "pairing.lock"))
		if err != nil {
			return Metadata{}, err
		}
		defer pairLock.Close()
	} else if !errors.Is(err, os.ErrNotExist) {
		return Metadata{}, err
	}
	if value.Removal == nil {
		// Direct pairing owns the client lock rather than the profile lock.
		// Recheck its exact original authority after acquiring that lock.
		profile, err := inspect(root, value)
		if err != nil {
			return Metadata{}, err
		}
		value.DeviceID = profile.DeviceID
		credential, err := fileDigest(filepath.Join(client, "device.json"))
		if err != nil {
			return Metadata{}, err
		}
		pending, err := fileDigest(filepath.Join(client, "pairing-pending.json"))
		if err != nil {
			return Metadata{}, err
		}
		value.Removal = &removalReceipt{RequestID: request, ExpectedRevision: revision, StartedAt: time.Now().UTC(), CredentialDigest: credential, PendingDigest: pending}
		value.Grant.Code = ""
		if err := ctx.Err(); err != nil {
			return Metadata{}, domain.SafeError(err)
		}
		if err := writeRecord(root, value); err != nil {
			return Metadata{}, err
		}
	}
	if err := cleanClient(ctx, client, value.Removal); err != nil {
		return Metadata{}, err
	}
	value.Removal.Complete = true
	if err := writeRecord(root, value); err != nil {
		return Metadata{}, err
	}
	return inspect(root, value)
}
func cleanClient(ctx context.Context, client string, receipt *removalReceipt) error {
	if err := security.CheckPrivateDir(client); errors.Is(err, os.ErrNotExist) {
		return security.SyncParent(client)
	} else if err != nil {
		return err
	}
	for _, target := range []struct{ name, digest string }{{"device.json", receipt.CredentialDigest}, {"pairing-pending.json", receipt.PendingDigest}} {
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		path := filepath.Join(client, target.name)
		current, err := fileDigest(path)
		if err != nil {
			return err
		}
		if current == "" {
			// A prior attempt may have unlinked but failed the directory sync.
			if err := security.SyncParent(path); err != nil {
				return err
			}
			continue
		}
		if current != target.digest {
			return invalid()
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := security.SyncParent(path); err != nil {
			return err
		}
	}
	return nil
}

type RemovedPage struct {
	Connections []Metadata `json:"connections"`
	NextAfter   domain.ID  `json:"next_after,omitempty"`
}

// Removed inventory is bounded and independently paged; active slots are freed
// only after known client secrets are removed. Worker paths never move.
func ListRemoved(root string, after domain.ID) (RemovedPage, error) {
	result := RemovedPage{Connections: []Metadata{}}
	if after != "" {
		if err := after.Validate(); err != nil {
			return result, err
		}
	}
	values, err := records(root)
	if err != nil {
		return result, err
	}
	for _, value := range values {
		if value.ID <= after || value.Removal == nil || !value.Removal.Complete {
			continue
		}
		if len(result.Connections) == 16 {
			result.NextAfter = result.Connections[len(result.Connections)-1].ID
			break
		}
		profile, err := inspect(root, value)
		if err != nil {
			return result, err
		}
		result.Connections = append(result.Connections, profile)
	}
	return result, nil
}
