// SPDX-License-Identifier: Apache-2.0
package workernetwork

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"filippo.io/age"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// ProtectedStore has no fallback implementation. Production uses the existing
// OS-key-wrapped Vault; tests inject an isolated in-memory credential service.
type ProtectedStore interface {
	Get(context.Context, credentials.Ref) ([]byte, error)
	Put(context.Context, credentials.Ref, []byte) (string, error)
	Delete(context.Context, credentials.Ref) error
	UnremovedReferences(context.Context, domain.ID) ([]credentials.Ref, error)
}
type Recipient struct {
	Version   uint32    `json:"version"`
	Authority Authority `json:"authority"`
	KeyID     domain.ID `json:"key_id"`
	PublicKey string    `json:"recipient"`
}
type Cache struct {
	Version          uint32          `json:"version"`
	KeyID            domain.ID       `json:"key_id"`
	Reference        credentials.Ref `json:"reference"`
	RouteID          domain.ID       `json:"route_id"`
	Generation       uint64          `json:"generation"`
	CiphertextDigest string          `json:"ciphertext_digest"`
}

func keyRef(r Recipient) credentials.Ref {
	return credentials.Ref{Owner: r.Authority.MachineID, ID: r.KeyID, Purpose: credentials.WorkerNetworkKey}
}
func save(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return recovery()
	}
	return security.WriteAtomic(path, raw)
}
func LoadRecipient(root string) (Recipient, error) {
	var value Recipient
	raw, err := security.ReadPrivate(filepath.Join(root, "network-recipient.json"), 4096)
	if err != nil {
		return value, err
	}
	if domain.Decode(raw, &value) != nil || value.Version != 1 || value.Authority.Validate() != nil || value.KeyID.Validate() != nil {
		return Recipient{}, recovery()
	}
	public, err := age.ParseX25519Recipient(value.PublicKey)
	if err != nil || public.String() != value.PublicKey {
		return Recipient{}, recovery()
	}
	return value, nil
}
func Prepare(ctx context.Context, root string, vault ProtectedStore, authority Authority) (Recipient, error) {
	var zero Recipient
	if authority.Validate() != nil || vault == nil {
		return zero, invalid()
	}
	if err := security.PrivateDir(root); err != nil {
		return zero, err
	}
	lock, err := security.TryLock(filepath.Join(root, "network.lock"))
	if err != nil {
		return zero, err
	}
	defer lock.Close()
	if existing, err := LoadRecipient(root); err == nil {
		if existing.Authority != authority {
			return zero, recovery()
		}
		if _, err := loadIdentity(ctx, vault, existing); err != nil {
			return zero, err
		}
		return existing, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return zero, err
	}
	refs, err := vault.UnremovedReferences(ctx, authority.MachineID)
	if err != nil {
		return zero, err
	}
	for _, ref := range refs {
		if ref.Purpose == credentials.WorkerNetworkKey || ref.Purpose == credentials.WorkerNetworkConfig {
			return zero, recovery()
		}
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return zero, recovery()
	}
	value := Recipient{Version: 1, Authority: authority, KeyID: domain.NewID(), PublicKey: identity.Recipient().String()}
	private := []byte(identity.String())
	defer clear(private)
	if _, err := vault.Put(ctx, keyRef(value), private); err != nil {
		return zero, err
	}
	// A crash before this metadata publication retains its protected orphan and
	// refuses key regeneration. It never creates an unprotected recovery copy.
	if err := save(filepath.Join(root, "network-recipient.json"), value); err != nil {
		return zero, err
	}
	return value, nil
}
func loadIdentity(ctx context.Context, vault ProtectedStore, recipient Recipient) (*age.X25519Identity, error) {
	raw, err := vault.Get(ctx, keyRef(recipient))
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	identity, err := age.ParseX25519Identity(string(raw))
	if err != nil || identity.Recipient().String() != recipient.PublicKey {
		return nil, recovery()
	}
	return identity, nil
}
func Load(ctx context.Context, root string, vault ProtectedStore, authority Authority) (Cache, Bundle, error) {
	var metadata Cache
	recipient, err := LoadRecipient(root)
	if err != nil {
		return metadata, Bundle{}, err
	}
	if recipient.Authority != authority || vault == nil {
		return metadata, Bundle{}, recovery()
	}
	if _, err := loadIdentity(ctx, vault, recipient); err != nil {
		return metadata, Bundle{}, err
	}
	raw, err := security.ReadPrivate(filepath.Join(root, "network-cache.json"), 4096)
	if err != nil {
		return metadata, Bundle{}, err
	}
	if domain.Decode(raw, &metadata) != nil || metadata.Version != 1 || metadata.KeyID != recipient.KeyID || metadata.Reference.Owner != authority.MachineID || metadata.Reference.ID.Validate() != nil || metadata.Reference.Purpose != credentials.WorkerNetworkConfig {
		return Cache{}, Bundle{}, recovery()
	}
	protected, err := vault.Get(ctx, metadata.Reference)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Cache{}, Bundle{}, recovery()
		}
		return Cache{}, Bundle{}, err
	}
	defer clear(protected)
	var value Bundle
	if domain.Decode(protected, &value) != nil || value.validate(value.IssuedAt, false) != nil || value.Authority != authority || value.KeyID != recipient.KeyID || value.Recipient != recipient.PublicKey || value.ExportID != metadata.Reference.ID || value.RouteID != metadata.RouteID || value.Generation != metadata.Generation {
		return Cache{}, Bundle{}, recovery()
	}
	return metadata, value, nil
}
func Import(ctx context.Context, root string, vault ProtectedStore, authority Authority, ciphertext []byte, expectedDigest string) (Cache, error) {
	var zero Cache
	if vault == nil || authority.Validate() != nil {
		return zero, invalid()
	}
	lock, err := security.TryLock(filepath.Join(root, "network.lock"))
	if err != nil {
		return zero, err
	}
	defer lock.Close()
	recipient, err := LoadRecipient(root)
	if err != nil {
		return zero, err
	}
	if recipient.Authority != authority {
		return zero, invalid()
	}
	identity, err := loadIdentity(ctx, vault, recipient)
	if err != nil {
		return zero, err
	}
	value, err := decrypt(ctx, identity, ciphertext, expectedDigest, authority, recipient.KeyID)
	if err != nil {
		return zero, err
	}
	previous, retained, err := Load(ctx, root, vault, authority)
	if err == nil {
		if value.Generation < previous.Generation || value.RouteID != previous.RouteID {
			return zero, recovery()
		}
		if value.Generation == previous.Generation {
			if !sameAuthority(value, retained) {
				return zero, recovery()
			}
			previous.CiphertextDigest = expectedDigest
			if err := save(filepath.Join(root, "network-cache.json"), previous); err != nil {
				return zero, err
			}
			if err := cleanupObsoleteDerivatives(ctx, vault, authority.MachineID, previous.Reference); err != nil {
				return previous, err
			}
			return previous, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return zero, err
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > MaxPlaintext {
		clear(raw)
		return zero, invalid()
	}
	defer clear(raw)
	metadata := Cache{Version: 1, KeyID: recipient.KeyID, Reference: credentials.Ref{Owner: authority.MachineID, ID: value.ExportID, Purpose: credentials.WorkerNetworkConfig}, RouteID: value.RouteID, Generation: value.Generation, CiphertextDigest: expectedDigest}
	if _, err := vault.Put(ctx, metadata.Reference, raw); err != nil {
		return zero, err
	}
	if err := save(filepath.Join(root, "network-cache.json"), metadata); err != nil {
		return zero, err
	}
	// Each already running native proxy retains its own bounded Go copy. Only
	// the new complete derivative is reloadable; old references grant no fallback.
	return metadata, cleanupObsoleteDerivatives(ctx, vault, authority.MachineID, metadata.Reference)
}

// The committed current reference is also the durable cleanup boundary. Retry
// reconciliation enumerates obsolete derivatives even at the same generation.
func cleanupObsoleteDerivatives(ctx context.Context, vault ProtectedStore, owner domain.ID, current credentials.Ref) error {
	refs, err := vault.UnremovedReferences(ctx, owner)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if ref.Owner == owner && ref.Purpose == credentials.WorkerNetworkConfig && ref != current {
			if err := vault.Delete(ctx, ref); err != nil {
				return err
			}
		}
	}
	return nil
}
