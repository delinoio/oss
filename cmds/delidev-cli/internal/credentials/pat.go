package credentials

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// PATRef identifies one immutable native token generation. Names, GitHub logins
// and repository paths never select credentials or become native record names.
type PATRef struct {
	ProfileID    domain.ID `json:"profile_id"`
	GenerationID domain.ID `json:"generation_id"`
}

func (r PATRef) validate() error {
	if err := r.ProfileID.Validate(); err != nil {
		return err
	}
	return r.GenerationID.Validate()
}

type patRecord struct {
	Version    uint32    `json:"version"`
	Scope      domain.ID `json:"scope"`
	Ref        PATRef    `json:"reference"`
	State      state     `json:"state"`
	Commitment string    `json:"commitment,omitempty"`
}

// PATStore stores token bytes directly in the OS service. Private disk records
// contain only intents, keyed comparison metadata and irreversible tombstones.
// The embedded implementation is used solely for scope pinning, private paths,
// locking and shutdown; none of Vault's envelope read/write methods are called.
type PATStore struct {
	scope     *Vault
	key       []byte
	closeOnce sync.Once
	closeErr  error
}

// OpenPAT does not read or unlock any native credential. The existing protected
// server owner key binds retries without persisting a raw or unkeyed PAT digest.
func OpenPAT(root string, scope domain.ID, ownerKey []byte, logger *slog.Logger) (*PATStore, error) {
	native, err := newNativeProfile(patProfile)
	if err != nil {
		return nil, err
	}
	return openPAT(root, scope, ownerKey, native, logger)
}

func openPAT(root string, scope domain.ID, ownerKey []byte, native nativeStore, logger *slog.Logger) (*PATStore, error) {
	if len(ownerKey) < 32 || len(ownerKey) > 512 {
		return nil, recovery()
	}
	base, err := open(root, scope, native, logger)
	if err != nil {
		return nil, err
	}
	return &PATStore{scope: base, key: append([]byte(nil), ownerKey...)}, nil
}

func (s *PATStore) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.scope.Close(); clear(s.key) })
	return s.closeErr
}

func ValidatePAT(value []byte) error {
	if len(value) == 0 || len(value) > MaxPATBytes {
		return invalidPAT()
	}
	for _, b := range value {
		if b < 0x21 || b > 0x7e {
			return invalidPAT()
		}
	}
	return nil
}

func invalidPAT() error {
	return domain.Fail(domain.InvalidArgument, "Invalid personal access token input.", "Supply 1–512 visible ASCII bytes without whitespace through the protected token input.")
}

func (s *PATStore) name(ref PATRef) string {
	return string(s.scope.scope) + "/" + string(ref.ProfileID) + "/" + string(ref.GenerationID)
}
func (s *PATStore) path(ref PATRef) string {
	return filepath.Join(s.scope.ownerPath(ref.ProfileID), string(ref.GenerationID)+".json")
}
func (s *PATStore) commitment(ref PATRef, token []byte) string {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(patProfile.service() + "/commitment/v1\x00" + s.name(ref) + "\x00"))
	_, _ = mac.Write(token)
	return hex.EncodeToString(mac.Sum(nil))
}
func (s *PATStore) read(ref PATRef) (patRecord, error) {
	var record patRecord
	if err := s.scope.checkOwner(ref.ProfileID, false); err != nil {
		return record, err
	}
	raw, err := security.ReadPrivate(s.path(ref), 2048)
	if err != nil {
		return record, err
	}
	if domain.Decode(raw, &record) != nil || record.Version != 1 || record.Scope != s.scope.scope || record.Ref != ref {
		return record, recovery()
	}
	switch record.State {
	case staged, sealed:
		digest, err := hex.DecodeString(record.Commitment)
		if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != record.Commitment {
			return record, recovery()
		}
	case deleting, deleted:
		if record.Commitment != "" {
			return record, recovery()
		}
	default:
		return record, recovery()
	}
	return record, nil
}
func (s *PATStore) write(record patRecord) error {
	if err := s.scope.checkOwner(record.Ref.ProfileID, true); err != nil {
		return err
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return security.WriteAtomic(s.path(record.Ref), raw)
}
func (s *PATStore) log(operation string, ref PATRef, err error) {
	if logger := s.scope.logger; logger != nil {
		if err != nil {
			logger.Warn("github_pat_storage_failed", "operation", operation, "profile_id", ref.ProfileID, "generation_id", ref.GenerationID, "code", domain.SafeError(err).Code)
		} else {
			logger.Info("github_pat_storage_completed", "operation", operation, "profile_id", ref.ProfileID, "generation_id", ref.GenerationID)
		}
	}
}

// Put synchronizes the immutable intent before its first native write. A lost
// native acknowledgment may reconcile only the same token and generation.
func (s *PATStore) Put(ctx context.Context, ref PATRef, token []byte) (err error) {
	if err = ref.validate(); err != nil {
		return err
	}
	if err = ValidatePAT(token); err != nil {
		return err
	}
	if err = s.scope.enter(ctx); err != nil {
		return err
	}
	defer s.scope.leave()
	defer func() { err = safe(err); s.log("put", ref, err) }()
	commitment := s.commitment(ref, token)
	record, err := s.read(ref)
	if errors.Is(err, os.ErrNotExist) {
		record = patRecord{Version: 1, Scope: s.scope.scope, Ref: ref, State: staged, Commitment: commitment}
		if err = s.write(record); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if record.State == deleting || record.State == deleted {
		return recovery()
	}
	if !hmac.Equal([]byte(record.Commitment), []byte(commitment)) {
		return duplicate()
	}
	value, err := s.scope.native.get(ctx, s.name(ref))
	defer clear(value)
	if isCode(err, domain.NotFound) {
		if record.State != staged {
			return recovery()
		}
		if err = s.scope.native.create(ctx, s.name(ref), token); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		matches := hmac.Equal(value, token)
		clear(value)
		if !matches {
			return recovery()
		}
	}
	record.State = sealed
	return s.write(record)
}

func (s *PATStore) Get(ctx context.Context, ref PATRef) (token []byte, err error) {
	if err = ref.validate(); err != nil {
		return nil, err
	}
	if err = s.scope.enter(ctx); err != nil {
		return nil, err
	}
	defer s.scope.leave()
	defer func() {
		err = safe(err)
		if err != nil {
			clear(token)
			token = nil
		}
		s.log("get", ref, err)
	}()
	record, err := s.read(ref)
	if err != nil {
		return nil, err
	}
	if record.State != sealed {
		return nil, recovery()
	}
	token, err = s.scope.native.get(ctx, s.name(ref))
	if isCode(err, domain.NotFound) {
		return token, recovery()
	}
	if err != nil {
		return token, err
	}
	if ValidatePAT(token) != nil || !hmac.Equal([]byte(record.Commitment), []byte(s.commitment(ref, token))) {
		return token, recovery()
	}
	return token, nil
}

// Delete commits denial before attempting native removal. Even a locked OS
// store or server restart can never re-enable this generation through Put/Get.
func (s *PATStore) Delete(ctx context.Context, ref PATRef) (err error) {
	if err = ref.validate(); err != nil {
		return err
	}
	if err = s.scope.enter(ctx); err != nil {
		return err
	}
	defer s.scope.leave()
	defer func() { err = safe(err); s.log("delete", ref, err) }()
	record, err := s.read(ref)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if record.State == deleted {
		return nil
	}
	record = patRecord{Version: 1, Scope: s.scope.scope, Ref: ref, State: deleting}
	if err = s.write(record); err != nil {
		return err
	}
	if err = s.scope.native.remove(ctx, s.name(ref)); err != nil && !isCode(err, domain.NotFound) {
		return err
	}
	record.State = deleted
	return s.write(record)
}

// UnremovedReferences enumerates only this profile's private intent metadata.
// It does not enumerate native credentials or expose any saved token bytes.
func (s *PATStore) UnremovedReferences(ctx context.Context, profile domain.ID) ([]PATRef, error) {
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	if err := s.scope.enter(ctx); err != nil {
		return nil, err
	}
	defer s.scope.leave()
	if err := s.scope.checkOwner(profile, false); errors.Is(err, os.ErrNotExist) {
		return []PATRef{}, nil
	} else if err != nil {
		return nil, safe(err)
	}
	directory, err := os.Open(s.scope.ownerPath(profile))
	if err != nil {
		return nil, safe(err)
	}
	defer directory.Close()
	out := []PATRef{}
	for {
		if err := ctx.Err(); err != nil {
			return nil, domain.SafeError(err)
		}
		entries, err := directory.ReadDir(128)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, safe(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".pending-") {
				continue
			}
			ref := PATRef{ProfileID: profile, GenerationID: domain.ID(strings.TrimSuffix(entry.Name(), ".json"))}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || ref.validate() != nil {
				return nil, recovery()
			}
			record, err := s.read(ref)
			if err != nil {
				return nil, safe(err)
			}
			if record.State != deleted {
				out = append(out, ref)
			}
			if len(out) > 256 {
				return nil, domain.Fail(domain.ResourceExhausted, "Too many unresolved token generations.", "Complete pending token cleanup before changing this profile again.")
			}
		}
		if errors.Is(err, io.EOF) {
			return out, nil
		}
	}
}
