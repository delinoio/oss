package nativeprofile

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const (
	maxRegistryBytes = 16 << 20
	maxOwnerBytes    = 4096
	maxProfiles      = 100000
	maxHistories     = 64
	lockAttempts     = 200
	lockPause        = 10 * time.Millisecond
)

// Config selects the storage root and ownership of one registry. Server and
// Worker owners never share a registry because each root is owner-local.
type Config struct {
	Root    string
	Owner   OwnerKind
	Server  domain.ID
	Machine domain.ID
	Logger  *slog.Logger
}

// Registry is the versioned local profile registry of one owner root.
type Registry struct {
	config Config
	base   string
	mu     sync.Mutex
}

type ownerRecord struct {
	Version int       `json:"version"`
	ID      domain.ID `json:"id"`
	Key     Key       `json:"key"`
}

type entry struct {
	ID        domain.ID   `json:"id"`
	Key       Key         `json:"key"`
	Origin    Origin      `json:"origin"`
	Path      string      `json:"path,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
	Histories []domain.ID `json:"histories,omitempty"`
	Refs      []Reference `json:"refs,omitempty"`
}

type registryFile struct {
	Version  int     `json:"version"`
	Profiles []entry `json:"profiles"`
}

// Open validates the owner root and returns its registry. No state is written
// until the first profile is acquired or registered.
func Open(config Config) (*Registry, error) {
	if !filepath.IsAbs(config.Root) {
		return nil, invalidKey()
	}
	probe := Key{Owner: config.Owner, Server: config.Server, Machine: config.Machine, Harness: domain.Codex, Purpose: PurposeDiscovery}
	if err := probe.Validate(); err != nil {
		return nil, err
	}
	// Native processes receive profile paths in their environment, so the owner
	// root is canonicalized once; a root that does not exist yet stays lexical.
	if canonical, err := filepath.EvalSymlinks(config.Root); err == nil {
		config.Root = canonical
	}
	return &Registry{config: config, base: filepath.Join(config.Root, "native-profiles")}, nil
}

// Base is the directory that contains the registry, managed profiles and locks.
func (r *Registry) Base() string { return r.base }

func (r *Registry) registryPath() string { return filepath.Join(r.base, "registry.json") }
func (r *Registry) profilesDir() string  { return filepath.Join(r.base, "p") }
func (r *Registry) locksDir() string     { return filepath.Join(r.base, "locks") }
func (r *Registry) managedPath(id domain.ID) string {
	return filepath.Join(r.profilesDir(), string(id))
}
func (r *Registry) lockPath(id domain.ID) string {
	return filepath.Join(r.locksDir(), string(id)+".lock")
}

func (r *Registry) prepare() error {
	for _, dir := range []string{r.base, r.profilesDir(), r.locksDir()} {
		if err := security.PrivateDir(dir); err != nil {
			return Failure(domain.RecoveryRequired, ReasonRegistryInvalid, "The native profile directory is not private.", "Inspect the owner storage root before retrying.")
		}
	}
	return nil
}

func busy() error {
	return Failure(domain.Conflict, ReasonBusy, "The native profile is in use.", "Wait for the active operation to finish, then retry with the same request ID.")
}

// withRegistry runs fn under the in-process and cross-process registry lock.
func (r *Registry) withRegistry(ctx context.Context, fn func(*registryFile) (bool, error)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.prepare(); err != nil {
		return err
	}
	var lock *security.Lock
	var err error
	for attempt := 0; attempt < lockAttempts; attempt++ {
		lock, err = security.TryLock(filepath.Join(r.base, "registry.lock"))
		if err == nil {
			break
		}
		var known *domain.Error
		if !errors.As(err, &known) || known.Code != domain.Conflict {
			return Failure(domain.RecoveryRequired, ReasonRegistryInvalid, "The native profile registry lock is unavailable.", "Inspect the owner storage root before retrying.")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(lockPause):
		}
	}
	if err != nil {
		return busy()
	}
	defer lock.Close()
	file, err := r.load()
	if err != nil {
		return err
	}
	changed, err := fn(&file)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	sort.Slice(file.Profiles, func(i, j int) bool { return file.Profiles[i].ID < file.Profiles[j].ID })
	raw, err := json.Marshal(file)
	if err != nil {
		return err
	}
	if len(raw) > maxRegistryBytes {
		return Failure(domain.ResourceExhausted, ReasonRegistryInvalid, "The native profile registry exceeds its size limit.", "Delete unused sessions or accounts before creating more profiles.")
	}
	return security.WriteAtomic(r.registryPath(), raw)
}

func (r *Registry) load() (registryFile, error) {
	raw, err := security.ReadPrivate(r.registryPath(), maxRegistryBytes)
	if errors.Is(err, os.ErrNotExist) {
		return registryFile{Version: Version}, nil
	}
	if err != nil {
		return registryFile{}, Failure(domain.RecoveryRequired, ReasonRegistryInvalid, "The native profile registry is unreadable.", "Restore the registry from a trusted backup without deleting profile directories.")
	}
	var peek struct {
		Version int `json:"version"`
	}
	if json.Unmarshal(raw, &peek) == nil && peek.Version != Version {
		return registryFile{}, Failure(domain.Unsupported, ReasonIncompatible, "The native profile registry version is not supported.", "Use the DeliDev version that created this storage root.")
	}
	var file registryFile
	if err := domain.DecodeBounded(raw, &file, maxRegistryBytes); err != nil || file.Version != Version || len(file.Profiles) > maxProfiles {
		return registryFile{}, Failure(domain.RecoveryRequired, ReasonRegistryInvalid, "The native profile registry is invalid.", "Restore the registry from a trusted backup without deleting profile directories.")
	}
	seenID, seenKey := map[domain.ID]bool{}, map[string]bool{}
	for _, e := range file.Profiles {
		if e.ID.Validate() != nil || e.Key.Validate() != nil || e.Key.Owner != r.config.Owner || seenID[e.ID] || seenKey[e.Key.String()] || len(e.Histories) > maxHistories || (e.Origin != OriginManaged && e.Origin != OriginLegacy) || (e.Origin == OriginLegacy) == (e.Path == "") {
			return registryFile{}, Failure(domain.RecoveryRequired, ReasonRegistryInvalid, "The native profile registry is invalid.", "Restore the registry from a trusted backup without deleting profile directories.")
		}
		seenID[e.ID], seenKey[e.Key.String()] = true, true
	}
	return file, nil
}

func (r *Registry) normalize(key Key) (Key, error) {
	if key.Owner == "" {
		key.Owner = r.config.Owner
	}
	if key.Server == "" {
		key.Server = r.config.Server
	}
	if key.Machine == "" {
		key.Machine = r.config.Machine
	}
	if err := key.Validate(); err != nil {
		return key, err
	}
	if key.Owner != r.config.Owner || key.Server != r.config.Server || key.Machine != r.config.Machine {
		return key, Failure(domain.PermissionDenied, ReasonKeyConflict, "The native profile belongs to another owner.", "Use the server or Worker that owns this storage root.")
	}
	return key, nil
}

func (r *Registry) pathOf(e entry) string {
	if e.Origin == OriginLegacy {
		return filepath.Join(r.config.Root, filepath.FromSlash(e.Path))
	}
	return r.managedPath(e.ID)
}

// homeOf is the native runtime root exposed to launch paths. Managed profiles
// keep their owner record beside it so native scanners never see DeliDev files.
// The directory is named after the harness.
func (r *Registry) homeOf(e entry) string {
	if e.Origin == OriginLegacy {
		return r.pathOf(e)
	}
	return filepath.Join(r.pathOf(e), string(e.Key.Harness))
}

func (r *Registry) info(e entry, created bool) Info {
	return Info{ID: e.ID, Key: e.Key, Origin: e.Origin, Root: r.homeOf(e), Created: created}
}

// verifyDirectory proves that the retained directory is still the real private
// directory registered for the profile, never a replaced symlink.
func (r *Registry) verifyDirectory(e entry) error {
	root := r.pathOf(e)
	if e.Origin == OriginManaged {
		if err := r.verifyOne(root); err != nil {
			return err
		}
		if err := r.verifyOne(r.homeOf(e)); err != nil {
			return err
		}
		return r.verifyOwner(e, root)
	}
	return r.verifyOne(root)
}

func (r *Registry) verifyOne(root string) error {
	if security.CheckPrivateDir(root) != nil {
		return Failure(domain.RecoveryRequired, ReasonOwnerInvalid, "The retained native profile is not a private directory.", "Inspect the profile without following links and restore it before retrying.")
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || canonical != filepath.Clean(root) {
		// The owner root itself may be reached through a symlink; compare against
		// the canonical owner root instead of the lexical one.
		base, baseErr := filepath.EvalSymlinks(r.config.Root)
		rel, relErr := filepath.Rel(r.config.Root, root)
		if err != nil || baseErr != nil || relErr != nil || canonical != filepath.Join(base, rel) {
			return Failure(domain.RecoveryRequired, ReasonOwnerInvalid, "The retained native profile path is not canonical.", "Inspect the profile without following links and restore it before retrying.")
		}
	}
	return nil
}

func (r *Registry) verifyOwner(e entry, root string) error {
	raw, err := security.ReadPrivate(filepath.Join(root, "owner.json"), maxOwnerBytes)
	var owner ownerRecord
	if err != nil || domain.Decode(raw, &owner) != nil || owner.Version != Version && owner.Version != 0 {
		return Failure(domain.RecoveryRequired, ReasonOwnerInvalid, "The native profile owner record is invalid.", "Inspect the profile owner record without deleting retained history.")
	}
	if owner.Version != Version {
		return Failure(domain.Unsupported, ReasonIncompatible, "The native profile version is not supported.", "Use the DeliDev version that created this storage root.")
	}
	if owner.ID != e.ID || owner.Key != e.Key {
		return Failure(domain.RecoveryRequired, ReasonOwnerInvalid, "The native profile owner record does not match its registry entry.", "Inspect the profile owner record without deleting retained history.")
	}
	return nil
}

// Lease is exclusive use of one profile. Closing it only releases the lock;
// it never removes the profile, its history, caches or helper files.
type Lease struct {
	Info
	lock   *security.Lock
	logger *slog.Logger
	once   sync.Once
}

// Close releases the profile lock.
func (l *Lease) Close() error {
	var err error
	l.once.Do(func() { err = l.lock.Close() })
	return err
}

// Acquire creates or reuses the profile for key and locks it. Repeated calls
// with the same key resolve the same path after any owner restart.
func (r *Registry) Acquire(ctx context.Context, key Key) (*Lease, error) {
	key, err := r.normalize(key)
	if err != nil {
		return nil, err
	}
	var lease *Lease
	err = r.withRegistry(ctx, func(file *registryFile) (bool, error) {
		for _, e := range file.Profiles {
			if e.Key == key {
				l, err := r.lease(e, false)
				if err != nil {
					return false, err
				}
				lease = l
				Log(r.config.Logger, "native_profile_acquired", key, e.ID, ReasonReused, "origin", e.Origin)
				return false, nil
			}
		}
		if len(file.Profiles) >= maxProfiles {
			return false, Failure(domain.ResourceExhausted, ReasonRegistryInvalid, "The native profile registry is full.", "Delete unused sessions or accounts before creating more profiles.")
		}
		e := entry{ID: domain.NewID(), Key: key, Origin: OriginManaged, CreatedAt: time.Now().UTC()}
		root := r.managedPath(e.ID)
		// A new identifier cannot collide with a retained directory; an existing one
		// would be unowned state, so never adopt it.
		if err := security.CreatePrivateDirExclusive(root); err != nil {
			return false, Failure(domain.RecoveryRequired, ReasonOwnerInvalid, "The native profile directory could not be created.", "Inspect the owner storage root before retrying.")
		}
		raw, err := json.Marshal(ownerRecord{Version: Version, ID: e.ID, Key: key})
		if err != nil {
			return false, err
		}
		if err := security.CreatePrivateDirExclusive(r.homeOf(e)); err != nil {
			return false, Failure(domain.RecoveryRequired, ReasonOwnerInvalid, "The native profile directory could not be created.", "Inspect the owner storage root before retrying.")
		}
		if err := security.WriteAtomic(filepath.Join(root, "owner.json"), raw); err != nil {
			return false, err
		}
		l, err := r.lease(e, true)
		if err != nil {
			return false, err
		}
		lease = l
		file.Profiles = append(file.Profiles, e)
		Log(r.config.Logger, "native_profile_acquired", key, e.ID, ReasonCreated, "origin", e.Origin)
		return true, nil
	})
	if err != nil {
		var known *domain.Error
		if errors.As(err, &known) && known.Cause != "" {
			Log(r.config.Logger, "native_profile_blocked", key, "", Reason(known.Cause))
		}
		return nil, err
	}
	return lease, nil
}

// AcquireWait retries only a busy profile until wait elapses or ctx ends. It
// is for short accountless probes that previously used independent homes and
// may legitimately overlap; every other failure is returned immediately.
func (r *Registry) AcquireWait(ctx context.Context, key Key, wait time.Duration) (*Lease, error) {
	deadline := time.Now().Add(wait)
	for {
		lease, err := r.Acquire(ctx, key)
		if reason, ok := Reasoned(err); err == nil || !ok || reason != ReasonBusy || !time.Now().Before(deadline) {
			return lease, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (r *Registry) lease(e entry, created bool) (*Lease, error) {
	if err := r.verifyDirectory(e); err != nil {
		return nil, err
	}
	lock, err := security.TryLock(r.lockPath(e.ID))
	if err != nil {
		var known *domain.Error
		if errors.As(err, &known) && known.Code == domain.Conflict {
			return nil, busy()
		}
		return nil, Failure(domain.RecoveryRequired, ReasonOwnerInvalid, "The native profile lock is unavailable.", "Inspect the owner storage root before retrying.")
	}
	return &Lease{Info: r.info(e, created), lock: lock, logger: r.config.Logger}, nil
}

// Lookup returns a registered profile without locking it.
func (r *Registry) Lookup(ctx context.Context, key Key) (Info, bool, error) {
	key, err := r.normalize(key)
	if err != nil {
		return Info{}, false, err
	}
	var found Info
	var ok bool
	err = r.withRegistry(ctx, func(file *registryFile) (bool, error) {
		for _, e := range file.Profiles {
			if e.Key == key {
				found, ok = r.info(e, false), true
			}
		}
		return false, nil
	})
	return found, ok, err
}

// RegisterLegacy registers an existing pre-registry history directory in
// place. The directory must be a real private directory below the owner root.
// Nothing is moved, merged or rewritten. Registering the same directory for the
// same key again is idempotent.
func (r *Registry) RegisterLegacy(ctx context.Context, key Key, relative string, histories ...domain.ID) (Info, error) {
	key, err := r.normalize(key)
	if err != nil {
		return Info{}, err
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative)))
	if relative == "" || filepath.IsAbs(relative) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != filepath.ToSlash(relative) || strings.HasPrefix(clean, "native-profiles/") {
		return Info{}, Failure(domain.InvalidArgument, ReasonLegacyUnverified, "The legacy native history path is invalid.", "Use a path below the owner storage root.")
	}
	for _, h := range histories {
		if h.Validate() != nil {
			return Info{}, invalidKey()
		}
	}
	var result Info
	err = r.withRegistry(ctx, func(file *registryFile) (bool, error) {
		for i, e := range file.Profiles {
			if e.Key == key {
				if e.Origin != OriginLegacy || e.Path != clean {
					// A conversation owns exactly one retained history.
					return false, Failure(domain.Conflict, ReasonKeyConflict, "The conversation already has a different retained native history.", "Keep the original history and inspect the conversation before retrying.")
				}
				changed := mergeHistories(&file.Profiles[i], histories)
				result = r.info(file.Profiles[i], false)
				return changed, nil
			}
			if e.Origin == OriginLegacy && e.Path == clean {
				return false, Failure(domain.Conflict, ReasonKeyConflict, "The legacy native history is registered for another profile.", "Keep the original history and inspect the conversation before retrying.")
			}
		}
		if len(file.Profiles) >= maxProfiles {
			return false, Failure(domain.ResourceExhausted, ReasonRegistryInvalid, "The native profile registry is full.", "Delete unused sessions or accounts before creating more profiles.")
		}
		e := entry{ID: domain.NewID(), Key: key, Origin: OriginLegacy, Path: clean, CreatedAt: time.Now().UTC()}
		if err := r.verifyDirectory(e); err != nil {
			return false, Failure(domain.RecoveryRequired, ReasonLegacyUnverified, "The legacy native history directory could not be verified.", "Keep the original history and inspect it without following links.")
		}
		mergeHistories(&e, histories)
		file.Profiles = append(file.Profiles, e)
		result = r.info(e, false)
		Log(r.config.Logger, "native_profile_registered", key, e.ID, ReasonRegisteredLegacy)
		return true, nil
	})
	return result, err
}

func mergeHistories(e *entry, histories []domain.ID) bool {
	changed := false
	for _, h := range histories {
		present := false
		for _, existing := range e.Histories {
			present = present || existing == h
		}
		if !present && len(e.Histories) < maxHistories {
			e.Histories = append(e.Histories, h)
			changed = true
		}
	}
	return changed
}

// RecordHistory binds a native execution or action identifier to a profile so
// later continuation, Fork, compaction and Revert find the retained history
// without deriving a path from the identifier.
func (r *Registry) RecordHistory(ctx context.Context, key Key, history domain.ID) error {
	key, err := r.normalize(key)
	if err != nil {
		return err
	}
	if history.Validate() != nil {
		return invalidKey()
	}
	return r.withRegistry(ctx, func(file *registryFile) (bool, error) {
		for i, e := range file.Profiles {
			if e.Key != key {
				continue
			}
			for _, existing := range e.Histories {
				if existing == history {
					return false, nil
				}
			}
			if len(e.Histories) >= maxHistories {
				// Only the newest histories are needed to resume the conversation.
				file.Profiles[i].Histories = append([]domain.ID(nil), e.Histories[1:]...)
			}
			file.Profiles[i].Histories = append(file.Profiles[i].Histories, history)
			Log(r.config.Logger, "native_profile_history_recorded", key, e.ID, ReasonHistoryRecorded)
			return true, nil
		}
		return false, Failure(domain.NotFound, ReasonLegacyUnverified, "The native profile is not registered.", "Acquire the profile before recording history.")
	})
}

// History resolves the profile that retains a native execution or action
// history identifier.
func (r *Registry) History(ctx context.Context, history domain.ID) (Info, bool, error) {
	var found Info
	var ok bool
	err := r.withRegistry(ctx, func(file *registryFile) (bool, error) {
		for _, e := range file.Profiles {
			for _, h := range e.Histories {
				if h == history {
					found, ok = r.info(e, false), true
				}
			}
		}
		return false, nil
	})
	return found, ok, err
}

// AddReference marks a profile as referenced so deletion refuses it.
func (r *Registry) AddReference(ctx context.Context, key Key, ref Reference) error {
	return r.updateRefs(ctx, key, ref, true)
}

// RemoveReference removes one explicit reference.
func (r *Registry) RemoveReference(ctx context.Context, key Key, ref Reference) error {
	return r.updateRefs(ctx, key, ref, false)
}

func (r *Registry) updateRefs(ctx context.Context, key Key, ref Reference, add bool) error {
	key, err := r.normalize(key)
	if err != nil {
		return err
	}
	if ref.ID.Validate() != nil {
		return invalidKey()
	}
	return r.withRegistry(ctx, func(file *registryFile) (bool, error) {
		for i, e := range file.Profiles {
			if e.Key != key {
				continue
			}
			var next []Reference
			present := false
			for _, existing := range e.Refs {
				if existing == ref {
					present = true
					if !add {
						continue
					}
				}
				next = append(next, existing)
			}
			if add && !present {
				next = append(next, ref)
			}
			if add == present {
				return false, nil
			}
			file.Profiles[i].Refs = next
			return true, nil
		}
		return false, nil
	})
}

// DeleteScope selects profiles for explicit session, account or storage
// deletion. At least one selector is required.
type DeleteScope struct {
	Conversation domain.ID
	Account      domain.ID
	History      domain.ID
	Purpose      Purpose
	Harness      domain.Harness
}

func (s DeleteScope) matches(e entry) bool {
	if s.Conversation == "" && s.Account == "" && s.History == "" {
		return false
	}
	if s.Conversation != "" && e.Key.Conversation != s.Conversation {
		return false
	}
	if s.Account != "" && e.Key.Account != s.Account {
		return false
	}
	if s.Purpose != "" && e.Key.Purpose != s.Purpose {
		return false
	}
	if s.Harness != "" && e.Key.Harness != s.Harness {
		return false
	}
	if s.History != "" {
		for _, h := range e.Histories {
			if h == s.History {
				return true
			}
		}
		return false
	}
	return true
}

// Delete removes every matching unreferenced, unlocked profile directory and
// registry entry. A referenced or active profile is never deleted and blocks
// with a specific cause; deletion is never implied by operation completion.
func (r *Registry) Delete(ctx context.Context, scope DeleteScope) (int, error) {
	if scope.Conversation == "" && scope.Account == "" && scope.History == "" {
		return 0, invalidKey()
	}
	deleted := 0
	err := r.withRegistry(ctx, func(file *registryFile) (bool, error) {
		var keep []entry
		var doomed []entry
		for _, e := range file.Profiles {
			if scope.matches(e) {
				doomed = append(doomed, e)
			} else {
				keep = append(keep, e)
			}
		}
		for _, e := range doomed {
			if len(e.Refs) != 0 {
				Log(r.config.Logger, "native_profile_delete_blocked", e.Key, e.ID, ReasonReferenced)
				return false, Failure(domain.Conflict, ReasonReferenced, "The native profile is still referenced.", "Remove the referencing session, account or checkpoint first.")
			}
		}
		var removed []entry
		for _, e := range doomed {
			lock, err := security.TryLock(r.lockPath(e.ID))
			if err != nil {
				Log(r.config.Logger, "native_profile_delete_blocked", e.Key, e.ID, ReasonBusy)
				if len(removed) == 0 {
					return false, busy()
				}
				keep = append(keep, e)
				continue
			}
			err = removeProfile(ctx, r, e)
			_ = lock.Close()
			if err != nil {
				Log(r.config.Logger, "native_profile_delete_blocked", e.Key, e.ID, ReasonDeleteBlocked)
				keep = append(keep, e)
				continue
			}
			_ = os.Remove(r.lockPath(e.ID))
			removed = append(removed, e)
			Log(r.config.Logger, "native_profile_deleted", e.Key, e.ID, ReasonDeleted)
		}
		deleted = len(removed)
		file.Profiles = keep
		return len(removed) != 0, nil
	})
	if err == nil && deleted != 0 {
		return deleted, nil
	}
	return deleted, err
}

func removeProfile(ctx context.Context, r *Registry, e entry) error {
	root := r.pathOf(e)
	if info, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Failure(domain.RecoveryRequired, ReasonDeleteBlocked, "The native profile path is not a real directory.", "Inspect the profile without following links.")
	}
	if err := security.RemoveOwnedTree(ctx, r.config.Root, root); err != nil {
		return Failure(domain.RecoveryRequired, ReasonDeleteBlocked, "The native profile could not be removed.", "Inspect the profile without following links.")
	}
	return nil
}
