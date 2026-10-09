package runmoor

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

type HostDirectoryKind string

const (
	HostDistribution HostDirectoryKind = "distribution"
	HostWorkspace    HostDirectoryKind = "execution"
)

// These journals are private SQLite state, never public status or diagnostics.
// The marker binds creation intent to a particular opened directory, not a name.
type HostDirectory struct {
	RemovalCommitted bool              `json:"removal_committed,omitempty"`
	ID               string            `json:"id"`
	Kind             HostDirectoryKind `json:"kind"`
	Token            string            `json:"token"`
	Installation     string            `json:"installation"`
	Identity         string            `json:"identity,omitempty"`
	Digest           string            `json:"digest,omitempty"`
	Version          string            `json:"version,omitempty"`
}

const hostOwnerFile = ".runmoor-host-owner.json"

func hostOwnership() *Problem {
	return problem(ErrOwnership, "Host execution or distribution ownership cannot be verified.", "Preserve state, data and capacity. Stop the affected pool; restore a paired state/data backup or inspect uncertain resources before explicit recovery. Never repair ownership markers manually.")
}
func hostPending() *Problem {
	return problem(ErrCleanup, "Host execution termination is not confirmed.", "Restore the verified supervisor or inspect the owned process group; capacity and execution directories remain reserved.")
}
func hostParent(c Config, kind HostDirectoryKind) string {
	return filepath.Join(c.Storage.Data, "host-"+string(kind)+"s")
}
func hostDirectoryPath(c Config, d HostDirectory) string {
	return filepath.Join(hostParent(c, d.Kind), d.ID)
}
func validHostDirectory(d HostDirectory, installation string) bool {
	return validID(d.ID) && validID(d.Token) && d.Installation == installation && validID(installation) && (d.Kind == HostDistribution || d.Kind == HostWorkspace)
}
func hostRootRead(root *os.Root, name string, limit int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil || !hostPrivateInfo(info, false) {
		return nil, hostOwnership()
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, hostOwnership()
	}
	defer f.Close()
	current, err := f.Stat()
	if err != nil || !os.SameFile(info, current) {
		return nil, hostOwnership()
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, hostOwnership()
	}
	return b, nil
}
func openHostDirectory(c Config, d HostDirectory, installation string, removing bool) (*os.Root, error) {
	return openHostDirectoryIdentity(c, d, installation, removing, false)
}

// Copied identities are accepted only by drained startup recovery. Runtime
// execution and cleanup continue to require the exact opened inode identity.
func openHostDirectoryIdentity(c Config, d HostDirectory, installation string, removing, copied bool) (*os.Root, error) {
	if !validHostDirectory(d, installation) {
		return nil, hostOwnership()
	}
	path := hostDirectoryPath(c, d)
	if removing {
		path = filepath.Join(hostParent(c, d.Kind), ".remove-"+d.ID)
	}
	// Checking every ancestor also rejects a replaced storage root. The opened
	// root stays anchored during reads and removal if a later rename occurs.
	for p := path; p != "/" && p != "."; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, hostOwnership()
		}
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, hostOwnership()
	}
	return verifyHostDirectoryRoot(root, d, installation, copied)
}

func verifyHostDirectoryRoot(root *os.Root, d HostDirectory, installation string, copied bool) (*os.Root, error) {
	fail := func() (*os.Root, error) { root.Close(); return nil, hostOwnership() }
	info, err := root.Stat(".")
	if err != nil || !hostPrivateInfo(info, true) || !copied && d.Identity != "" && hostFileIdentity(info) != d.Identity {
		return fail()
	}
	b, err := hostRootRead(root, hostOwnerFile, 4096)
	var marker HostDirectory
	if err != nil && d.RemovalCommitted {
		entries, listErr := fs.ReadDir(root.FS(), ".")
		if listErr == nil && len(entries) == 0 {
			return root, nil
		}
	}
	if err != nil || json.Unmarshal(b, &marker) != nil || marker.ID != d.ID || marker.Token != d.Token || marker.Installation != installation || marker.Kind != d.Kind {
		return fail()
	}
	if marker.Identity != hostFileIdentity(info) && (!copied || d.Identity == "" || marker.Identity != d.Identity) {
		return fail()
	}
	if copied && (d.Kind != HostDistribution || d.RemovalCommitted || d.Identity == "" || marker.Digest != "" && marker.Digest != d.Digest || marker.Version != "" && marker.Version != d.Version) {
		return fail()
	}
	return root, nil
}

func hostRestoreBoundary(s Snapshot, destinations ...Config) bool {
	// Closed setup images may share a drained backup only after all private
	// process and run-alias authority has settled. Never infer exit from phase.
	if len(s.ImageTartPIDs) != 0 || len(s.ImageTartStarts) != 0 {
		return false
	}
	for _, r := range s.Runners {
		if r.Phase != Completed {
			return false
		}
	}
	for _, a := range s.Artifacts {
		if a.Reserved || a.Phase != ArtifactReady {
			return false
		}
	}
	for _, im := range s.Images {
		if im.Phase == ImageOpen || im.Phase == ImageRemoving || im.Phase == ImagePreparing && !im.CreationComplete {
			return false
		}
	}
	for _, configuration := range append([]Config{s.Config}, destinations...) {
		for _, im := range s.Images {
			present, err := tartRunAliasPresent(configuration, tartRunAlias(im.ID))
			if err != nil || present {
				return false
			}
		}
	}
	for _, e := range s.HostExecutions {
		if !e.Terminated || !e.Cleaned {
			return false
		}
	}
	for _, d := range s.HostDirectories {
		if d.Kind != HostDistribution || d.RemovalCommitted {
			return false
		}
	}
	return true
}

func rebindHostDistributions(ctx context.Context, store *Store, c Config) error {
	s := store.View()
	if !hostRestoreBoundary(s, c) {
		return hostOwnership()
	}
	for id, d := range s.HostDirectories {
		a := s.Artifacts[id]
		if d.ID != id || d.Digest == "" || !versionPattern.MatchString(d.Version) || a == nil || a.ID != id || a.Backend != Host || a.Image != id || !a.Generated {
			return hostOwnership()
		}
		root, err := openHostDirectoryIdentity(c, *d, s.Installation, false, true)
		if err != nil {
			return err
		}
		err = func() error {
			defer root.Close()
			info, err := root.Stat(".")
			if err != nil {
				return hostOwnership()
			}
			identity := hostFileIdentity(info)
			if identity == d.Identity {
				return nil
			}
			digest, err := hostRunnerDigest(ctx, root)
			if err != nil || digest != d.Digest {
				return hostOwnership()
			}
			rebound := *d
			rebound.Identity = identity
			// Publish the marker first. If the SQLite commit fails, drained retry
			// accepts this exact new identity and rechecks the digest before commit.
			if err = hostRootWrite(root, hostOwnerFile, rebound); err != nil {
				return err
			}
			err = store.Update(func(v *Snapshot) error {
				if !hostRestoreBoundary(*v, c) || v.HostDirectories[id] == nil || *v.HostDirectories[id] != *d {
					return hostOwnership()
				}
				v.HostDirectories[id].Identity = identity
				return nil
			})
			if err == nil {
				slog.Info("host_distribution_identity_rebound", "artifact", id)
			}
			return err
		}()
		if err != nil {
			return err
		}
	}
	return nil
}
func createHostDirectory(ctx context.Context, store *Store, c Config, id string, kind HostDirectoryKind) (HostDirectory, *os.Root, error) {
	if ctx.Err() != nil {
		return HostDirectory{}, nil, ctx.Err()
	}
	if store == nil || !validID(id) {
		return HostDirectory{}, nil, hostOwnership()
	}
	s := store.View()
	if d := s.HostDirectories[id]; d != nil {
		root, err := openHostDirectory(c, *d, s.Installation, false)
		return *d, root, err
	}
	d := HostDirectory{ID: id, Kind: kind, Token: newID(), Installation: s.Installation}
	if err := privateDir(hostParent(c, kind)); err != nil {
		return d, nil, err
	}
	if err := store.Update(func(v *Snapshot) error {
		if v.HostDirectories == nil {
			v.HostDirectories = map[string]*HostDirectory{}
		}
		if v.HostDirectories[id] != nil {
			return hostOwnership()
		}
		v.HostDirectories[id] = &d
		return nil
	}); err != nil {
		return d, nil, err
	}
	if ctx.Err() != nil {
		return d, nil, ctx.Err()
	}
	// A crash before marker publication leaves a markerless directory and durable
	// intent. Preserve that directory; neither retry nor cleanup may adopt it.
	if err := os.Mkdir(hostDirectoryPath(c, d), 0700); err != nil {
		return d, nil, hostOwnership()
	}
	root, err := os.OpenRoot(hostDirectoryPath(c, d))
	if err != nil {
		return d, nil, hostOwnership()
	}
	info, err := root.Stat(".")
	if err != nil {
		root.Close()
		return d, nil, hostOwnership()
	}
	d.Identity = hostFileIdentity(info)
	body, _ := json.Marshal(d)
	if err = hostRootWriteExclusive(root, hostOwnerFile, body); err == nil {
		err = syncPrivateDir(hostDirectoryPath(c, d))
	}
	if err == nil {
		err = store.Update(func(v *Snapshot) error { v.HostDirectories[id].Identity = d.Identity; return nil })
	}
	if err != nil {
		root.Close()
		return d, nil, err
	}
	return d, root, nil
}
func hostRootWriteExclusive(root *os.Root, name string, body []byte) error {
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return hostOwnership()
	}
	_, err = f.Write(body)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func hostRootWrite(root *os.Root, name string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tmp := ".state-" + newID()
	if err = hostRootWriteExclusive(root, tmp, b); err != nil {
		return err
	}
	if err = root.Rename(tmp, name); err != nil {
		root.Remove(tmp)
		return err
	}
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// Walk from an opened filesystem root so validation and subsequent cleanup
// remain bound to the inspected ancestors, even if their pathnames move.
// This must not create directories or repair permissions during recovery.
func openHostRemovalParent(c Config, d HostDirectory) (*os.Root, error) {
	if !validHostDirectory(d, d.Installation) {
		return nil, hostOwnership()
	}
	path := filepath.Clean(hostParent(c, d.Kind))
	if !filepath.IsAbs(path) {
		return nil, hostOwnership()
	}
	anchor := filepath.VolumeName(path) + string(filepath.Separator)
	root, err := os.OpenRoot(anchor)
	if err != nil {
		return nil, hostOwnership()
	}
	for _, name := range strings.Split(strings.TrimPrefix(path, anchor), string(filepath.Separator)) {
		next, err := openHostRemovalChild(root, name)
		root.Close()
		if err != nil {
			return nil, err
		}
		root = next
	}
	info, err := root.Stat(".")
	if err != nil || !hostPrivateInfo(info, true) {
		root.Close()
		return nil, hostOwnership()
	}
	return root, nil
}

func openHostRemovalChild(parent *os.Root, name string) (*os.Root, error) {
	before, err := parent.Lstat(name)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, hostOwnership()
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, hostOwnership()
	}
	opened, openedErr := root.Stat(".")
	after, afterErr := parent.Lstat(name)
	if openedErr != nil || afterErr != nil || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		root.Close()
		return nil, hostOwnership()
	}
	return root, nil
}

func openHostRemovalDirectory(parent *os.Root, d HostDirectory, removing bool) (*os.Root, error) {
	name := d.ID
	if removing {
		name = ".remove-" + d.ID
	}
	root, err := openHostRemovalChild(parent, name)
	if err != nil {
		return nil, err
	}
	return verifyHostDirectoryRoot(root, d, d.Installation, false)
}

func removeHostDirectory(ctx context.Context, store *Store, c Config, d HostDirectory) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	parent, err := openHostRemovalParent(c, d)
	if err != nil {
		return err
	}
	defer parent.Close()
	staged := ".remove-" + d.ID
	if d.RemovalCommitted {
		_, originalErr := parent.Lstat(d.ID)
		_, stagedErr := parent.Lstat(staged)
		if os.IsNotExist(originalErr) && os.IsNotExist(stagedErr) {
			return nil
		}
	}
	removing := false
	if _, err = parent.Lstat(staged); err == nil {
		removing = true
	} else if !os.IsNotExist(err) {
		return hostOwnership()
	}
	root, err := openHostRemovalDirectory(parent, d, removing)
	if err != nil {
		return err
	}
	if !removing {
		root.Close()
		if err = hostRenameNoReplace(parent, d.ID, staged); err != nil {
			return hostOwnership()
		}
		root, err = openHostRemovalDirectory(parent, d, true)
		if err != nil {
			return err
		}
	}
	defer root.Close()
	if d.Identity == "" {
		// Recover a marker published just before the identity commit was lost.
		// openHostDirectory already verified the exact durable creation token.
		info, statErr := root.Stat(".")
		if statErr != nil || store == nil {
			return hostOwnership()
		}
		d.Identity = hostFileIdentity(info)
		if err = store.Update(func(v *Snapshot) error {
			record := v.HostDirectories[d.ID]
			if record == nil || record.Token != d.Token {
				return hostOwnership()
			}
			record.Identity = d.Identity
			return nil
		}); err != nil {
			return err
		}
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return hostOwnership()
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Leave ownership proof until all disposable content is gone. Root removal
		// removes links themselves and cannot follow an escaping symlink.
		if entry.Name() != hostOwnerFile {
			if err = root.RemoveAll(entry.Name()); err != nil {
				return hostPending()
			}
		}
	}
	info, err := parent.Lstat(staged)
	if err != nil || hostFileIdentity(info) != d.Identity {
		return hostOwnership()
	}
	if !d.RemovalCommitted {
		if store == nil {
			return hostPending()
		}
		if err = store.Update(func(v *Snapshot) error {
			record := v.HostDirectories[d.ID]
			if record == nil || record.Token != d.Token || record.Identity != d.Identity {
				return hostOwnership()
			}
			record.RemovalCommitted = true
			return nil
		}); err != nil {
			return err
		}
	}
	if err = root.Remove(hostOwnerFile); err != nil && !os.IsNotExist(err) {
		return hostPending()
	}
	if err = parent.Remove(staged); err != nil {
		return hostPending()
	}
	return nil
}

// Extraction uses the already validated, bounded tar. Root adds confinement
// against filesystem replacement; no native runner is executed during tests.
func extractHostRunner(ctx context.Context, input io.Reader, root *os.Root) error {
	var err error
	if err = root.Mkdir("runner", 0700); err != nil {
		return hostOwnership()
	}
	reader := tar.NewReader(input)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		h, e := reader.Next()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return archiveProblem()
		}
		name := filepath.Join("runner", filepath.FromSlash(h.Name))
		if e = root.MkdirAll(filepath.Dir(name), 0700); e != nil {
			return archiveProblem()
		}
		switch h.Typeflag {
		case tar.TypeDir:
			e = root.MkdirAll(name, 0700)
		case tar.TypeReg:
			var out *os.File
			out, e = root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(h.Mode)&0777)
			if e == nil {
				_, e = io.Copy(out, &contextReader{ctx, reader})
				if e == nil {
					e = out.Sync()
				}
				closeErr := out.Close()
				if e == nil {
					e = closeErr
				}
			}
		case tar.TypeSymlink:
			e = root.Symlink(h.Linkname, name)
		default:
			return archiveProblem()
		}
		if e != nil {
			return archiveProblem()
		}
	}
}
func hostRunnerDigest(ctx context.Context, root *os.Root) (string, error) {
	hash := sha256.New()
	err := fs.WalkDir(root.FS(), "runner", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return hostOwnership()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		info, err := root.Lstat(name)
		if err != nil {
			return hostOwnership()
		}
		fmt.Fprintf(hash, "%s\x00%d\x00", name, info.Mode())
		if entry.Type()&os.ModeSymlink != 0 {
			target, e := root.Readlink(name)
			if e != nil {
				return hostOwnership()
			}
			io.WriteString(hash, target)
			// Validate confinement again during use, not only during archive parsing.
			resolved, e := root.Stat(name)
			if e != nil || !resolved.Mode().IsRegular() {
				return hostOwnership()
			}
		} else if info.Mode().IsRegular() {
			f, e := root.Open(name)
			if e != nil {
				return hostOwnership()
			}
			opened, e := f.Stat()
			if e != nil || !os.SameFile(info, opened) {
				f.Close()
				return hostOwnership()
			}
			_, e = io.Copy(hash, &contextReader{ctx, io.LimitReader(f, 4<<30)})
			f.Close()
			if e != nil {
				return e
			}
		} else if !info.IsDir() {
			return hostOwnership()
		}
		io.WriteString(hash, "\x00")
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func copyHostRunner(ctx context.Context, source, destination *os.Root) error {
	return fs.WalkDir(source.FS(), "runner", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return hostOwnership()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		info, err := source.Lstat(name)
		if err != nil {
			return hostOwnership()
		}
		if info.IsDir() {
			return destination.Mkdir(name, 0700)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, e := source.Readlink(name)
			if e != nil {
				return hostOwnership()
			}
			return destination.Symlink(target, name)
		}
		if !info.Mode().IsRegular() {
			return hostOwnership()
		}
		in, err := source.Open(name)
		if err != nil {
			return hostOwnership()
		}
		defer in.Close()
		out, err := destination.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return hostOwnership()
		}
		_, err = io.Copy(out, &contextReader{ctx, in})
		if err == nil {
			err = out.Sync()
		}
		closeErr := out.Close()
		if err == nil {
			err = closeErr
		}
		return err
	})
}
func hostEnvironment(path string) []string {
	env := []string{"LANG=C", "LC_ALL=C", "HOME=" + filepath.Join(path, "home"), "TMPDIR=" + filepath.Join(path, "tmp"), "TMP=" + filepath.Join(path, "tmp"), "TEMP=" + filepath.Join(path, "tmp"), "RUNNER_MANUALLY_TRAP_SIG=1", "RUNNER_LOG_TO_STDOUT=0"}
	if value, ok := os.LookupEnv("PATH"); ok {
		env = append(env, "PATH="+value)
	} else {
		env = append(env, minimalEnv()[0])
	}
	if value, ok := os.LookupEnv("DEVELOPER_DIR"); ok {
		env = append(env, "DEVELOPER_DIR="+value)
	}
	return env
}
func validHostJIT(jit string) bool {
	return len(jit) > 0 && len(jit) <= 1<<20 && !strings.ContainsAny(jit, "\x00\n\r")
}
