// Package credentials owns protected server secrets. SQLite and product reads
// contain only references; ciphertext is private, and each immutable reference's
// wrapping key exists only in the operating system credential store.
package credentials

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const MaxSecretBytes = 64 << 10
const maxRecordBytes = 100 << 10

type Purpose string

const (
	AccountAPI   Purpose = "account-api"
	AccountLogin Purpose = "account-login"
	NetworkProxy Purpose = "network-proxy"
	WorkerSSH    Purpose = "worker-ssh"
)

func (p Purpose) valid() bool {
	switch p {
	case AccountAPI, AccountLogin, NetworkProxy, WorkerSSH:
		return true
	default:
		return false
	}
}

// Ref is non-secret. ID is the durable mutation request identity, not an alias or
// provider username. A replacement must use a new ID. No method discovers logins.
type Ref struct {
	Owner   domain.ID `json:"owner"`
	ID      domain.ID `json:"id"`
	Purpose Purpose   `json:"purpose"`
}

func (r Ref) validate() error {
	if err := r.Owner.Validate(); err != nil {
		return err
	}
	if err := r.ID.Validate(); err != nil {
		return err
	}
	if !r.Purpose.valid() {
		return domain.Fail(domain.InvalidArgument, "Invalid credential purpose.", "Choose a supported protected credential type.")
	}
	return nil
}

type state string

const (
	staged   state = "staged"
	sealed   state = "sealed"
	deleting state = "deleting"
	deleted  state = "deleted"
)

type record struct {
	Version    uint32    `json:"version"`
	Scope      domain.ID `json:"scope"`
	Ref        Ref       `json:"reference"`
	State      state     `json:"state"`
	Ciphertext []byte    `json:"ciphertext,omitempty"`
}

type Vault struct {
	root      string
	scope     domain.ID
	native    nativeStore
	logger    *slog.Logger
	gate      chan struct{}
	lock      *security.Lock
	closeOnce sync.Once
	closed    bool
}

// Open holds a private scope lock until Close. Creating/opening the scope does
// not read user credentials or unlock the OS store. The server may remain usable
// for metadata operations while the OS store is unavailable.
func Open(root string, scope domain.ID, logger *slog.Logger) (*Vault, error) {
	native, err := newNative()
	if err != nil {
		return nil, err
	}
	return open(root, scope, native, logger)
}
func open(root string, scope domain.ID, native nativeStore, logger *slog.Logger) (*Vault, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if native == nil {
		return nil, unavailable()
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, domain.SafeError(err)
	}
	if err = security.PrivateDir(root); err != nil {
		return nil, domain.SafeError(err)
	}
	lock, err := security.TryLock(filepath.Join(root, "vault.lock"))
	if err != nil {
		return nil, domain.SafeError(err)
	}
	success := false
	defer func() {
		if !success {
			lock.Close()
		}
	}()
	identityPath := filepath.Join(root, "scope.json")
	identity, err := security.ReadPrivate(identityPath, 1024)
	if errors.Is(err, os.ErrNotExist) {
		// A missing pin cannot rebind existing references to a different server.
		entries, e := os.ReadDir(root)
		if e != nil {
			return nil, domain.SafeError(e)
		}
		for _, entry := range entries {
			if entry.Name() != "vault.lock" {
				return nil, recovery()
			}
		}
		identity, err = json.Marshal(struct {
			Scope domain.ID `json:"scope"`
		}{scope})
		if err == nil {
			err = security.WriteAtomic(identityPath, identity)
		}
	}
	if err != nil {
		return nil, domain.SafeError(err)
	}
	var pin struct {
		Scope domain.ID `json:"scope"`
	}
	if domain.Decode(identity, &pin) != nil || pin.Scope != scope {
		return nil, recovery()
	}
	if logger == nil {
		logger = slog.Default()
	}
	success = true
	return &Vault{root: root, scope: scope, native: native, logger: logger, gate: make(chan struct{}, 1), lock: lock}, nil
}
func (v *Vault) Close() error {
	var err error
	v.closeOnce.Do(func() { v.gate <- struct{}{}; defer func() { <-v.gate }(); v.closed = true; err = v.lock.Close() })
	return err
}
func (v *Vault) enter(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return domain.SafeError(err)
	}
	select {
	case v.gate <- struct{}{}:
	case <-ctx.Done():
		return domain.SafeError(ctx.Err())
	}
	if v.closed {
		<-v.gate
		return unavailable()
	}
	if err := ctx.Err(); err != nil {
		<-v.gate
		return domain.SafeError(err)
	}
	return nil
}
func (v *Vault) leave() { <-v.gate }
func (v *Vault) name(r Ref) string {
	return string(v.scope) + "/" + string(r.Owner) + "/" + string(r.ID) + "/" + string(r.Purpose)
}
func (v *Vault) aad(r Ref) []byte                 { return []byte(nativeService + "/" + v.name(r)) }
func (v *Vault) ownerPath(owner domain.ID) string { return filepath.Join(v.root, string(owner)) }
func (v *Vault) path(r Ref) string                { return filepath.Join(v.ownerPath(r.Owner), string(r.ID)+".json") }
func (v *Vault) checkOwner(owner domain.ID, create bool) error {
	if err := security.CheckPrivateDir(v.root); err != nil {
		return err
	}
	if create {
		return security.PrivateDir(v.ownerPath(owner))
	}
	return security.CheckPrivateDir(v.ownerPath(owner))
}
func (v *Vault) read(r Ref) (record, error) {
	out := record{}
	if err := v.checkOwner(r.Owner, false); err != nil {
		return out, err
	}
	data, err := security.ReadPrivate(v.path(r), maxRecordBytes)
	if err != nil {
		return out, err
	}
	if domain.Decode(data, &out) != nil || out.Version != 1 || out.Scope != v.scope || out.Ref != r {
		return record{}, recovery()
	}
	switch out.State {
	case staged, deleting, deleted:
		if len(out.Ciphertext) != 0 {
			return record{}, recovery()
		}
	case sealed:
		if len(out.Ciphertext) < 29 || len(out.Ciphertext) > MaxSecretBytes+28 {
			return record{}, recovery()
		}
	default:
		return record{}, recovery()
	}
	return out, nil
}
func (v *Vault) write(rec record) error {
	if err := v.checkOwner(rec.Ref.Owner, true); err != nil {
		return err
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return security.WriteAtomic(v.path(rec.Ref), data)
}

// Independent subkeys prevent the wrapping root from being used directly by
// both AES-GCM and the immutable request-content commitment.
func deriveKey(root []byte, label string) []byte {
	mac := hmac.New(sha256.New, root)
	mac.Write([]byte(nativeService + "/" + label))
	return mac.Sum(nil)
}
func commitment(root, aad, secret []byte) []byte {
	key := deriveKey(root, "commitment")
	defer clear(key)
	mac := hmac.New(sha256.New, key)
	mac.Write(aad)
	mac.Write([]byte{0})
	mac.Write(secret)
	return mac.Sum(nil)
}
func aead(material []byte) (cipher.AEAD, error) {
	if len(material) != nativeMaterialSize {
		return nil, recovery()
	}
	key := deriveKey(material[:32], "encryption")
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, recovery()
	}
	return cipher.NewGCM(block)
}
func (v *Vault) log(operation string, r Ref, err error) {
	attributes := []any{"operation", operation, "owner_id", r.Owner, "credential_id", r.ID, "purpose", r.Purpose}
	if err != nil {
		attributes = append(attributes, "error_code", domain.SafeError(err).Code)
		v.logger.Warn("protected credential operation failed", attributes...)
	} else {
		v.logger.Info("protected credential operation completed", attributes...)
	}
}

// Put durably binds an immutable reference to one secret. A retry with different
// bytes conflicts. The returned keyed commitment is suitable for an internal
// request receipt; it is not a plaintext secret hash or an execution credential.
// On failure, leave the intent in place: the native write may already have
// succeeded. Never delete it speculatively after an uncertain database commit.
func (v *Vault) Put(ctx context.Context, r Ref, secret []byte) (binding string, err error) {
	if err = r.validate(); err != nil {
		return "", err
	}
	if len(secret) == 0 || len(secret) > MaxSecretBytes {
		return "", domain.Fail(domain.InvalidArgument, "Invalid protected credential size.", "Provide between 1 and 65536 bytes through the secret input channel.")
	}
	if err = v.enter(ctx); err != nil {
		return "", err
	}
	defer v.leave()
	defer func() { err = safe(err); v.log("put", r, err) }()
	rec, err := v.read(r)
	if errors.Is(err, os.ErrNotExist) {
		rec = record{Version: 1, Scope: v.scope, Ref: r, State: staged}
		if err = v.write(rec); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	if rec.State == deleting || rec.State == deleted {
		return "", domain.Fail(domain.Conflict, "The credential reference was deleted.", "Reconnect with a new request ID; deleted references cannot be reused.")
	}
	material, err := v.native.get(ctx, v.name(r))
	if isCode(err, domain.NotFound) {
		if rec.State == sealed {
			return "", recovery()
		}
		material = make([]byte, nativeMaterialSize)
		defer clear(material)
		if _, err = rand.Read(material[:32]); err != nil {
			return "", err
		}
		copy(material[32:], commitment(material[:32], v.aad(r), secret))
		if err = v.native.create(ctx, v.name(r), material); err != nil {
			if !isCode(err, domain.Conflict) {
				return "", err
			}
			clear(material)
			material, err = v.native.get(ctx, v.name(r))
			if err != nil {
				return "", err
			}
		}
	} else if err != nil {
		return "", err
	}
	defer clear(material)
	if len(material) != nativeMaterialSize {
		return "", recovery()
	}
	digest := commitment(material[:32], v.aad(r), secret)
	defer clear(digest)
	if !hmac.Equal(digest, material[32:]) {
		return "", domain.Fail(domain.Conflict, "The request is already bound to different credential content.", "Retry with the original secret or use a new request ID for replacement.")
	}
	cipher, err := aead(material)
	if err != nil {
		return "", err
	}
	if rec.State == sealed {
		previous, e := cipher.Open(nil, rec.Ciphertext[:12], rec.Ciphertext[12:], v.aad(r))
		defer clear(previous)
		if e != nil || !bytes.Equal(previous, secret) {
			return "", recovery()
		}
	} else {
		if err = ctx.Err(); err != nil {
			return "", err
		}
		nonce := make([]byte, cipher.NonceSize())
		if _, err = rand.Read(nonce); err != nil {
			return "", err
		}
		rec.Ciphertext = cipher.Seal(nonce, nonce, secret, v.aad(r))
		rec.State = sealed
		if err = v.write(rec); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(digest), nil
}
func safe(err error) error {
	if err == nil {
		return nil
	}
	return domain.SafeError(err)
}

// Get returns caller-owned bytes. Clear them immediately after their bounded use.
// It never repairs or recreates a missing key, reads another reference, or turns
// an interrupted staged write into an account connection.
func (v *Vault) Get(ctx context.Context, r Ref) (secret []byte, err error) {
	if err = r.validate(); err != nil {
		return nil, err
	}
	if err = v.enter(ctx); err != nil {
		return nil, err
	}
	defer v.leave()
	defer func() {
		err = safe(err)
		if err != nil {
			v.log("get", r, err)
		}
	}()
	rec, err := v.read(r)
	if errors.Is(err, os.ErrNotExist) {
		return nil, missing()
	}
	if err != nil {
		return nil, err
	}
	if rec.State == deleted || rec.State == deleting {
		return nil, missing()
	}
	if rec.State != sealed {
		return nil, recovery()
	}
	material, err := v.native.get(ctx, v.name(r))
	defer clear(material)
	if isCode(err, domain.NotFound) {
		return nil, recovery()
	}
	if err != nil {
		return nil, err
	}
	cipher, err := aead(material)
	if err != nil {
		return nil, err
	}
	secret, err = cipher.Open(nil, rec.Ciphertext[:12], rec.Ciphertext[12:], v.aad(r))
	if err != nil {
		return nil, recovery()
	}
	if !hmac.Equal(commitment(material[:32], v.aad(r), secret), material[32:]) {
		clear(secret)
		return nil, recovery()
	}
	return secret, nil
}

// Delete first records an irreversible tombstone (without ciphertext), then
// removes the OS key. A locked/unavailable OS store leaves a retryable deleting
// record, but Get/Put can no longer use or resurrect the reference.
func (v *Vault) Delete(ctx context.Context, r Ref) (err error) {
	if err = r.validate(); err != nil {
		return err
	}
	if err = v.enter(ctx); err != nil {
		return err
	}
	defer v.leave()
	defer func() { err = safe(err); v.log("delete", r, err) }()
	rec, err := v.read(r)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if rec.State == deleted {
		return nil
	}
	rec = record{Version: 1, Scope: v.scope, Ref: r, State: deleting}
	if err = v.write(rec); err != nil {
		return err
	}
	if err = v.native.remove(ctx, v.name(r)); err != nil && !isCode(err, domain.NotFound) {
		return err
	}
	rec.State = deleted
	return v.write(rec)
}

// References lists only this vault owner's local intents, including staged and
// deleted references. It never enumerates native credentials. Reconciliation
// compares these references to server state before deleting unused material.
func (v *Vault) References(ctx context.Context, owner domain.ID) ([]Ref, error) {
	if err := owner.Validate(); err != nil {
		return nil, err
	}
	if err := v.enter(ctx); err != nil {
		return nil, err
	}
	defer v.leave()
	if err := v.checkOwner(owner, false); errors.Is(err, os.ErrNotExist) {
		return []Ref{}, nil
	} else if err != nil {
		return nil, safe(err)
	}
	entries, err := os.ReadDir(v.ownerPath(owner))
	if err != nil {
		return nil, safe(err)
	}
	out := make([]Ref, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, safe(err)
		}
		// Interrupted atomic writes contain ciphertext or metadata only, are never
		// promoted on filename guesses, and cannot hide a committed reference.
		if strings.HasPrefix(entry.Name(), ".pending-") {
			continue
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			return nil, recovery()
		}
		id := domain.ID(strings.TrimSuffix(entry.Name(), ".json"))
		if id.Validate() != nil {
			return nil, recovery()
		}
		raw, err := security.ReadPrivate(filepath.Join(v.ownerPath(owner), entry.Name()), maxRecordBytes)
		if err != nil {
			return nil, safe(err)
		}
		var rec record
		if domain.Decode(raw, &rec) != nil || rec.Ref.ID != id || rec.Ref.Owner != owner || rec.Ref.validate() != nil {
			return nil, recovery()
		}
		if _, err = v.read(rec.Ref); err != nil {
			return nil, safe(err)
		}
		out = append(out, rec.Ref)
	}
	return out, nil
}
