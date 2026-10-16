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

// writeRegistryFile publishes the registry; tests replace it to inject
// publication failures.
var writeRegistryFile = security.WriteAtomic

// removeProfileAfterVerifyHook lets tests replace a validated path immediately
// before removal. Production never installs a hook.
var removeProfileAfterVerifyHook func()

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

// violatesRegisteredOnly reports whether the entry breaks the invariant of a
// registered-only purpose: such an entry exists only as a legacy directory
// registered in place and never holds a conversation history. Registration
// enforces it when the entry is created and every registry load enforces it
// again, so a restored registry cannot smuggle in an entry that Acquire would
// then lock as an ordinary profile.
func (e entry) violatesRegisteredOnly() bool {
	return e.Key.Purpose.RegisteredOnly() && (e.Origin != OriginLegacy || len(e.Histories) > 0)
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

// UseLogger installs a structured logger when none is configured yet, so a
// registry first opened by a log-free read path still reports later events.
func (r *Registry) UseLogger(logger *slog.Logger) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.config.Logger == nil {
		r.config.Logger = logger
	}
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
	return r.withRegistryPublish(ctx, fn, nil)
}

// withRegistryPublish is withRegistry with a hook for a failed publication.
// failed runs under the registry lock after fn changed the file but the
// registry could not be published, so a creator can decide from the on-disk
// registry whether its new entry became durable and roll back otherwise.
func (r *Registry) withRegistryPublish(ctx context.Context, fn func(*registryFile) (bool, error), failed func(error)) error {
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
		if failed != nil {
			failed(err)
		}
		return err
	}
	if len(raw) > maxRegistryBytes {
		err := Failure(domain.ResourceExhausted, ReasonRegistryInvalid, "The native profile registry exceeds its size limit.", "Delete unused sessions or accounts before creating more profiles.")
		if failed != nil {
			failed(err)
		}
		return err
	}
	if err := writeRegistryFile(r.registryPath(), raw); err != nil {
		if failed != nil {
			failed(err)
		}
		return err
	}
	return nil
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
	seenHistory := map[domain.ID]domain.ID{}
	for _, e := range file.Profiles {
		// Every stored key must belong to this owner root: a restored or reused
		// root under another server or Worker identity is invalid as a whole.
		if e.ID.Validate() != nil || e.Key.Validate() != nil || e.Key.Owner != r.config.Owner || e.Key.Server != r.config.Server || e.Key.Machine != r.config.Machine || (e.Origin == OriginLegacy && !validLegacyPath(e.Path)) || seenID[e.ID] || seenKey[e.Key.String()] || len(e.Histories) > maxHistories || (e.Origin != OriginManaged && e.Origin != OriginLegacy) || (e.Origin == OriginLegacy) == (e.Path == "") || e.violatesRegisteredOnly() {
			return registryFile{}, Failure(domain.RecoveryRequired, ReasonRegistryInvalid, "The native profile registry is invalid.", "Restore the registry from a trusted backup without deleting profile directories.")
		}
		seenID[e.ID], seenKey[e.Key.String()] = true, true
		for _, history := range e.Histories {
			if history.Validate() != nil || seenHistory[history] != "" && seenHistory[history] != e.ID {
				return registryFile{}, Failure(domain.RecoveryRequired, ReasonRegistryInvalid, "The native profile registry is invalid.", "Restore the registry from a trusted backup without deleting profile directories.")
			}
			seenHistory[history] = e.ID
		}
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
	mu     sync.Mutex
	closed bool
}

// Close releases the profile lock. A close that succeeded is final and every
// later call returns nil without touching the lock. A close that failed is not
// final: the lease stays open, so a later call closes the lock handle again and
// reports that result instead of a success the lock never confirmed. A caller
// that needs proof of release must therefore not read a later nil as a clean
// release of an earlier failed close.
func (l *Lease) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	err := l.lock.Close()
	if err == nil {
		l.closed = true
	}
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
	// created is the entry this attempt added; it stays set only until the
	// registry publication proves durable, so every failure path can roll back
	// exactly what this attempt created and release the lock it holds.
	var created *entry
	rollback := func(e entry) {
		if lease != nil {
			_ = lease.Close()
			lease = nil
		}
		if rmErr := security.RemoveOwnedTree(ctx, r.config.Root, r.managedPath(e.ID)); rmErr != nil {
			Log(r.config.Logger, "native_profile_rollback_blocked", e.Key, e.ID, ReasonDeleteBlocked)
			return
		}
		_ = os.Remove(r.lockPath(e.ID))
		Log(r.config.Logger, "native_profile_rolled_back", e.Key, e.ID, ReasonCreateRolledBack)
	}
	err = r.withRegistryPublish(ctx, func(file *registryFile) (bool, error) {
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
		if key.Purpose.RegisteredOnly() {
			// A registered-only purpose names a pre-registry directory that is
			// registered in place; creating a managed profile would fork its
			// history, so nothing is created and the key stays unregistered.
			return false, Failure(domain.NotFound, ReasonLegacyUnverified, "The native profile is not registered.", "Register the existing native history before acquiring it.")
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
		// From here the directory is this attempt's own unregistered state.
		created = &e
		raw, err := json.Marshal(ownerRecord{Version: Version, ID: e.ID, Key: key})
		if err != nil {
			rollback(e)
			return false, err
		}
		if err := security.CreatePrivateDirExclusive(r.homeOf(e)); err != nil {
			rollback(e)
			return false, Failure(domain.RecoveryRequired, ReasonOwnerInvalid, "The native profile directory could not be created.", "Inspect the owner storage root before retrying.")
		}
		if err := security.WriteAtomic(filepath.Join(root, "owner.json"), raw); err != nil {
			rollback(e)
			return false, err
		}
		l, err := r.lease(e, true)
		if err != nil {
			rollback(e)
			return false, err
		}
		lease = l
		file.Profiles = append(file.Profiles, e)
		Log(r.config.Logger, "native_profile_acquired", key, e.ID, ReasonCreated, "origin", e.Origin)
		return true, nil
	}, func(error) {
		if created == nil {
			return
		}
		// Publication failed after the callback created and locked the profile.
		// A rename that failed late (for example a directory sync) may still have
		// made the entry durable; keep the directory only in that case.
		if durable, loadErr := r.load(); loadErr == nil {
			for _, e := range durable.Profiles {
				if e.ID == created.ID {
					if lease != nil {
						_ = lease.Close()
						lease = nil
					}
					Log(r.config.Logger, "native_profile_publish_durable", created.Key, created.ID, ReasonRegistryInvalid)
					return
				}
			}
			rollback(*created)
			return
		}
		// An unreadable registry cannot prove the entry; retain the directory and
		// release the lock so a later reconciliation can still discover it.
		if lease != nil {
			_ = lease.Close()
			lease = nil
		}
	})
	if err != nil {
		lease = nil
	}
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

// Peek is Lookup for readers that must not change anything. It takes no lock
// and creates no directory or file, so it also works on a read-only root: a
// registry that does not exist yet reports the key as unregistered. Registry
// publication replaces the file atomically, so a lock-free read sees either the
// old or the new registry, never a partial one. A registry directory that is
// not a real private directory, or an unreadable or invalid registry, fails
// with the same closed cause the locked paths use.
func (r *Registry) Peek(key Key) (Info, bool, error) {
	key, err := r.normalize(key)
	if err != nil {
		return Info{}, false, err
	}
	if _, err := os.Lstat(r.base); errors.Is(err, os.ErrNotExist) {
		return Info{}, false, nil
	}
	if err := security.CheckPrivateDir(r.base); err != nil {
		return Info{}, false, Failure(domain.RecoveryRequired, ReasonRegistryInvalid, "The native profile directory is not private.", "Inspect the owner storage root before retrying.")
	}
	if _, err := os.Lstat(r.registryPath()); errors.Is(err, os.ErrNotExist) {
		return Info{}, false, nil
	}
	file, err := r.load()
	if err != nil {
		return Info{}, false, err
	}
	for _, e := range file.Profiles {
		if e.Key == key {
			return r.info(e, false), true, nil
		}
	}
	return Info{}, false, nil
}

// HomeRegistered reports whether a registered profile exposes exactly this
// native home path. It is a lock-free read like Peek: a registry that does not
// exist yet registers nothing. A caller that finds a recorded home missing from
// disk uses it to tell an explicitly deleted profile (no longer registered, so
// its directory and everything in it were removed) from a directory that was
// moved away while the registry still names it.
func (r *Registry) HomeRegistered(home string) (bool, error) {
	if _, err := os.Lstat(r.base); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err := security.CheckPrivateDir(r.base); err != nil {
		return false, Failure(domain.RecoveryRequired, ReasonRegistryInvalid, "The native profile directory is not private.", "Inspect the owner storage root before retrying.")
	}
	if _, err := os.Lstat(r.registryPath()); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	file, err := r.load()
	if err != nil {
		return false, err
	}
	clean := filepath.Clean(home)
	for _, e := range file.Profiles {
		if r.homeOf(e) == clean {
			return true, nil
		}
	}
	return false, nil
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
	if !validLegacyPath(relative) {
		return Info{}, Failure(domain.InvalidArgument, ReasonLegacyUnverified, "The legacy native history path is invalid.", "Use a path below the owner storage root.")
	}
	for _, h := range histories {
		if h.Validate() != nil {
			return Info{}, invalidKey()
		}
	}
	if key.Purpose.RegisteredOnly() && len(histories) > 0 {
		return Info{}, invalidKey()
	}
	var result Info
	err = r.withRegistry(ctx, func(file *registryFile) (bool, error) {
		if historyOwnedByOther(file.Profiles, key, histories) {
			return false, historyConflict()
		}
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

func historyOwnedByOther(entries []entry, key Key, histories []domain.ID) bool {
	if len(histories) == 0 {
		return false
	}
	wanted := make(map[domain.ID]struct{}, len(histories))
	for _, history := range histories {
		wanted[history] = struct{}{}
	}
	for _, e := range entries {
		if e.Key == key {
			continue
		}
		for _, history := range e.Histories {
			if _, ok := wanted[history]; ok {
				return true
			}
		}
	}
	return false
}

func historyConflict() error {
	return Failure(domain.Conflict, ReasonKeyConflict, "The native history belongs to another profile.", "Keep both profiles and inspect the original history owner before retrying.")
}

// validLegacyPath accepts only a normalized, local, slash-separated path below
// the owner root that does not enter the managed profile tree. Registration and
// every registry load share it so a restored registry can never point a legacy
// entry outside the owner root.
func validLegacyPath(relative string) bool {
	if relative == "" || filepath.IsAbs(relative) || strings.HasPrefix(relative, "/") || strings.ContainsRune(relative, '\\') || strings.ContainsRune(relative, 0) || !filepath.IsLocal(filepath.FromSlash(relative)) {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative)))
	return clean == relative && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../") && clean != "native-profiles" && !strings.HasPrefix(clean, "native-profiles/")
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
	if history.Validate() != nil || key.Purpose.RegisteredOnly() {
		return invalidKey()
	}
	return r.withRegistry(ctx, func(file *registryFile) (bool, error) {
		if historyOwnedByOther(file.Profiles, key, []domain.ID{history}) {
			return false, historyConflict()
		}
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
				// The first identifier names the original history and is kept for the
				// profile's lifetime; the oldest later identifier is evicted instead.
				file.Profiles[i].Histories = append([]domain.ID{e.Histories[0]}, e.Histories[2:]...)
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

// Exists reports whether any registered profile matches the scope. Explicit
// deletion uses it to prove absence before acknowledging completion.
func (r *Registry) Exists(ctx context.Context, scope DeleteScope) (bool, error) {
	if scope.Conversation == "" && scope.Account == "" && scope.History == "" {
		return false, invalidKey()
	}
	found := false
	err := r.withRegistry(ctx, func(file *registryFile) (bool, error) {
		for _, e := range file.Profiles {
			if scope.matches(e) {
				found = true
			}
		}
		return false, nil
	})
	return found, err
}

// Delete removes every matching unreferenced, unlocked profile directory and
// registry entry. A referenced or active profile is never deleted and blocks
// with a specific cause; deletion is never implied by operation completion.
func (r *Registry) Delete(ctx context.Context, scope DeleteScope) (int, error) {
	if scope.Conversation == "" && scope.Account == "" && scope.History == "" {
		return 0, invalidKey()
	}
	deleted := 0
	var pending error
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
				// Any locked match keeps the whole deletion pending, even when
				// earlier matches were already removed and are committed below.
				keep = append(keep, e)
				if pending == nil {
					pending = busy()
				}
				continue
			}
			err = removeProfile(ctx, r, e)
			_ = lock.Close()
			if err != nil {
				Log(r.config.Logger, "native_profile_delete_blocked", e.Key, e.ID, ReasonDeleteBlocked)
				keep = append(keep, e)
				if pending == nil {
					pending = err
				}
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
	if err == nil {
		// Removals are durable above; a surviving match is still reported so the
		// caller never acknowledges a deletion while a matching profile remains.
		err = pending
	}
	return deleted, err
}

// DeleteRemovedClaudeProfile retires only the original account's registered
// Claude auth entry after its legacy directory is proven absent. It is for
// original logout recovery after native removal but before registry publication.
func (r *Registry) DeleteRemovedClaudeProfile(ctx context.Context, key Key, relative string) error {
	key, err := r.normalize(key)
	if err != nil {
		return err
	}
	if key.Harness != domain.ClaudeCode || key.Purpose != PurposeAuth || !validLegacyPath(relative) {
		return invalidKey()
	}
	path := filepath.Join(r.config.Root, filepath.FromSlash(relative))
	return r.withRegistry(ctx, func(file *registryFile) (bool, error) {
		missing, err := security.CapturePathIdentity(path)
		if err != nil || missing.Present() {
			missing.Close()
			return false, Failure(domain.RecoveryRequired, ReasonDeleteBlocked, "The original Claude profile removal is unproven.", "Inspect the original profile before retrying cleanup.")
		}
		defer missing.Close()
		for _, e := range file.Profiles {
			if e.Origin == OriginLegacy && e.Path == relative && e.Key != key {
				return false, Failure(domain.RecoveryRequired, ReasonOwnerInvalid, "The original Claude profile path belongs to another registry entry.", "Inspect the original profile before retrying cleanup.")
			}
		}
		for i, e := range file.Profiles {
			if e.Key != key {
				continue
			}
			if e.Origin != OriginLegacy || e.Path != relative {
				return false, Failure(domain.RecoveryRequired, ReasonOwnerInvalid, "The original Claude profile registry entry changed.", "Inspect the original profile before retrying cleanup.")
			}
			if len(e.Refs) != 0 {
				return false, Failure(domain.Conflict, ReasonReferenced, "The native profile is still referenced.", "Remove the referencing session, account or checkpoint first.")
			}
			lock, err := security.TryLock(r.lockPath(e.ID))
			if err != nil {
				return false, busy()
			}
			defer lock.Close()
			if err := security.RemoveOwnedTreeIdentity(ctx, r.config.Root, path, missing); err != nil {
				return false, Failure(domain.RecoveryRequired, ReasonDeleteBlocked, "The original Claude profile removal is unproven.", "Inspect the original profile before retrying cleanup.")
			}
			file.Profiles = append(file.Profiles[:i], file.Profiles[i+1:]...)
			Log(r.config.Logger, "native_profile_deleted", e.Key, e.ID, ReasonDeleted)
			return true, nil
		}
		return false, nil
	})
}

func removeProfile(ctx context.Context, r *Registry, e entry) error {
	root := r.pathOf(e)
	if info, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		if e.Origin == OriginManaged {
			return Failure(domain.RecoveryRequired, ReasonOwnerInvalid, "The retained native profile owner is missing.", "Restore the original profile before retrying deletion.")
		}
		return nil
	} else if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Failure(domain.RecoveryRequired, ReasonDeleteBlocked, "The native profile path is not a real directory.", "Inspect the profile without following links.")
	}
	if e.Origin == OriginManaged {
		// Capture the directory before checking its owner. The removal helper then
		// binds traversal to this exact object, even if its path is replaced.
		identity, err := security.CapturePathIdentity(root)
		if err != nil || !identity.Present() {
			return Failure(domain.RecoveryRequired, ReasonOwnerInvalid, "The retained native profile owner changed.", "Restore the original profile before retrying deletion.")
		}
		defer identity.Close()
		if err := r.verifyDirectory(e); err != nil {
			return err
		}
		if err := identity.StillAt(root); err != nil {
			return Failure(domain.RecoveryRequired, ReasonOwnerInvalid, "The retained native profile owner changed.", "Restore the original profile before retrying deletion.")
		}
		if removeProfileAfterVerifyHook != nil {
			removeProfileAfterVerifyHook()
		}
		if err := security.RemoveOwnedTreeIdentity(ctx, r.config.Root, root, identity); err != nil {
			return Failure(domain.RecoveryRequired, ReasonDeleteBlocked, "The native profile could not be removed.", "Inspect the profile without following links.")
		}
		return nil
	}
	if err := security.RemoveOwnedTree(ctx, r.config.Root, root); err != nil {
		return Failure(domain.RecoveryRequired, ReasonDeleteBlocked, "The native profile could not be removed.", "Inspect the profile without following links.")
	}
	return nil
}
