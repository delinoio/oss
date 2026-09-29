package runmoor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const GuestAgentVersion = "0.14.2"

type tartGuestOS string

const tartGuestOSDarwin tartGuestOS = "darwin"

func validateTartGuestOS(os tartGuestOS) error {
	if os != tartGuestOSDarwin {
		return problem(ErrImage, "Tart image guest operating system is unsupported.", "Use a macOS guest image; Runmoor Tart pools require a Darwin guest.")
	}
	return nil
}

type CommandExecutor interface {
	Run(context.Context, string, []string, []string, io.Reader) ([]byte, error)
	Start(string, []string, []string) (int, error)
}

// PinnedCommandExecutor lets an owned Tart operation keep the verified VM
// directory open across Tart's name lookup. The descriptor is exposed to Tart
// as fd 3 by os/exec and the corresponding VM name resolves through /dev/fd/3.
type PinnedCommandExecutor interface {
	RunPinned(context.Context, string, []string, []string, io.Reader, *os.File) ([]byte, error)
	StartPinned(string, []string, []string, *os.File) (int, error)
}
type OSCommand struct{}

func (OSCommand) Run(ctx context.Context, name string, args, env []string, in io.Reader) ([]byte, error) {
	return runOSCommand(ctx, name, args, env, in, nil)
}
func (OSCommand) RunPinned(ctx context.Context, name string, args, env []string, in io.Reader, dir *os.File) ([]byte, error) {
	return runOSCommand(ctx, name, args, env, in, []*os.File{dir})
}
func runOSCommand(ctx context.Context, name string, args, env []string, in io.Reader, files []*os.File) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	cmd.Stdin = in
	cmd.ExtraFiles = files
	out := &boundedBuffer{Limit: 64 << 10}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	err := cmd.Run()
	return out.Bytes(), err
}
func (OSCommand) Start(name string, args, env []string) (int, error) {
	return startOSCommand(name, args, env, nil)
}
func (OSCommand) StartPinned(name string, args, env []string, dir *os.File) (int, error) {
	return startOSCommand(name, args, env, []*os.File{dir})
}
func startOSCommand(name string, args, env []string, files []*os.File) (int, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = env
	cmd.ExtraFiles = files
	// A detached process must inherit real null descriptors. io.Discard makes
	// os/exec create parent-owned pipes that close on manager exit and can
	// terminate a surviving VM with SIGPIPE when it next writes a diagnostic.
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return 0, err
	}
	defer null.Close()
	cmd.Stdout = null
	cmd.Stderr = null
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	go func() { _ = cmd.Wait() }()
	return cmd.Process.Pid, nil
}

type TartDriver struct {
	Exec            CommandExecutor
	HostCheck       func(context.Context) error
	GuestExecutable string
	processAlive    func(int) (bool, error)
}

func tartEnv(c Config) []string {
	return tartEnvAt(c, tartHome(c))
}
func tartHome(c Config) string      { return filepath.Join(c.Storage.Data, "tart") }
func tartHomeCache(c Config) string { return filepath.Join(tartHome(c), "cache") }
func tartVMPathAtHome(home, name string) string {
	return filepath.Join(home, "vms", name)
}
func creationVMName(entity string) string { return "rm-create-" + entity }
func tartCreationHome(c Config, entity string) string {
	return filepath.Join(c.Storage.Data, "tart-creation", entity)
}
func tartCreationVMPath(c Config, entity string) string {
	return tartVMPathAtHome(tartCreationHome(c, entity), creationVMName(entity))
}
func tartEnvAt(c Config, home string) []string {
	return append(minimalEnv(), "TART_HOME="+home, "TART_NO_AUTO_PRUNE=1")
}
func prepareTartCreationHome(c Config, entity string) (string, *os.File, error) {
	// Tart publishes names inside TART_HOME itself. Keep the requested final
	// name out of its shared home until the marker is attached to the staged
	// directory and the verified directory is moved into place.
	if !validID(entity) {
		return "", nil, ambiguousVMOwnership()
	}
	root := filepath.Join(c.Storage.Data, "tart-creation")
	if err := privateDir(root); err != nil {
		return "", nil, err
	}
	home := tartCreationHome(c, entity)
	if _, err := os.Lstat(home); os.IsNotExist(err) {
		if err = os.Mkdir(home, 0700); err != nil && !os.IsExist(err) {
			return "", nil, problem(ErrPermission, "Cannot create the private Tart creation home.", "Preserve the VM and check Runmoor data directory permissions.")
		}
	} else if err != nil {
		return "", nil, problem(ErrPermission, "Cannot inspect the private Tart creation home.", "Preserve the VM and check Runmoor data directory permissions.")
	}
	if err := privateDir(home); err != nil {
		return "", nil, err
	}
	lock, err := lockTartCreationHome(home)
	if err != nil {
		return "", nil, err
	}
	if err := privateDir(filepath.Join(home, "vms")); err != nil {
		_ = lock.Close()
		return "", nil, err
	}
	if err := privateDir(filepath.Join(home, "tmp")); err != nil {
		_ = lock.Close()
		return "", nil, err
	}
	if _, err = os.Lstat(tartCreationVMPath(c, entity)); err == nil || !os.IsNotExist(err) {
		_ = lock.Close()
		return "", nil, ambiguousVMOwnership()
	}
	if err := privateDir(tartHome(c)); err != nil {
		_ = lock.Close()
		return "", nil, err
	}
	if err := privateDir(tartHomeCache(c)); err != nil {
		_ = lock.Close()
		return "", nil, err
	}
	cache := filepath.Join(home, "cache")
	if target, linkErr := os.Readlink(cache); linkErr == nil {
		if target != tartHomeCache(c) {
			_ = lock.Close()
			return "", nil, ambiguousVMOwnership()
		}
	} else if os.IsNotExist(linkErr) {
		if err := os.Symlink(tartHomeCache(c), cache); err != nil {
			_ = lock.Close()
			return "", nil, problem(ErrPermission, "Cannot share Tart's private content cache with the creation home.", "Preserve the VM and check Runmoor data directory permissions.")
		}
	} else {
		_ = lock.Close()
		return "", nil, ambiguousVMOwnership()
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		_ = lock.Close()
		return "", nil, problem(ErrPermission, "Cannot inspect the private Tart creation home.", "Preserve the VM and check Runmoor data directory permissions.")
	}
	for _, entry := range entries {
		switch entry.Name() {
		case ".runmoor-creation.lock", "cache", "tmp", "vms":
		default:
			_ = lock.Close()
			return "", nil, ambiguousVMOwnership()
		}
	}
	return home, lock, nil
}
func cleanupTartCreationHome(c Config, entity string) {
	home := tartCreationHome(c, entity)
	if _, err := os.Lstat(home); os.IsNotExist(err) {
		return
	} else if err != nil {
		return
	}
	if _, err := privateVMDirectory(home); err != nil {
		return
	}
	lock, err := lockTartCreationHome(home)
	if err != nil {
		return
	}
	if err := privateDir(home); err != nil {
		_ = lock.Close()
		return
	}
	if _, err := os.Lstat(tartCreationVMPath(c, entity)); err == nil || !os.IsNotExist(err) {
		_ = lock.Close()
		return
	}
	for _, name := range []string{"tmp", "vms"} {
		path := filepath.Join(home, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			_ = lock.Close()
			return
		}
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 0 || os.Remove(path) != nil {
			_ = lock.Close()
			return
		}
	}
	if target, err := os.Readlink(filepath.Join(home, "cache")); err != nil || target != tartHomeCache(c) {
		_ = lock.Close()
		return
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		_ = lock.Close()
		return
	}
	for _, entry := range entries {
		if entry.Name() != ".runmoor-creation.lock" && entry.Name() != "cache" {
			_ = lock.Close()
			return
		}
	}
	if err = os.Remove(filepath.Join(home, "cache")); err != nil {
		_ = lock.Close()
		return
	}
	_ = lock.Close()
	if err = os.Remove(filepath.Join(home, ".runmoor-creation.lock")); err != nil {
		return
	}
	_ = os.Remove(home)
	_ = os.Remove(filepath.Dir(home))
}
func (t *TartDriver) runAtHome(ctx context.Context, c Config, home string, args []string, in io.Reader) ([]byte, error) {
	b, e := t.Exec.Run(ctx, c.TartExecutable, args, tartEnvAt(c, home), in)
	if e != nil {
		return nil, problem(ErrDependency, "Tart command failed.", "Check Tart 2.x.x, Guest Agent RPC and the owned VM with 'runmoor doctor'.")
	}
	return b, nil
}
func (t *TartDriver) run(ctx context.Context, c Config, args []string, in io.Reader) ([]byte, error) {
	return t.runAtHome(ctx, c, tartHome(c), args, in)
}

func supportedTartVersion(output string) bool {
	version := "v" + strings.TrimSpace(output)
	if !semver.IsValid(version) || semver.Major(version) != "v2" || semver.Prerelease(version) != "" {
		return false
	}
	// x/mod also accepts v2 and v2.1; Tart must report a full SemVer triplet.
	return semver.Canonical(version) == strings.SplitN(version, "+", 2)[0]
}

func (t *TartDriver) check(ctx context.Context, c Config) error {
	if t.HostCheck != nil {
		if e := t.HostCheck(ctx); e != nil {
			return e
		}
	} else {
		if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
			return problem(ErrPlatform, "Tart requires an Apple Silicon Mac.", "Use macOS 14 or later on arm64.")
		}
		b, e := t.Exec.Run(ctx, "/usr/bin/sw_vers", []string{"-productVersion"}, minimalEnv(), nil)
		if e != nil {
			return problem(ErrPlatform, "Cannot determine macOS version.", "Use macOS 14 or later.")
		}
		major, _ := strconv.Atoi(strings.Split(strings.TrimSpace(string(b)), ".")[0])
		if major < 14 {
			return problem(ErrPlatform, "Tart execution requires macOS 14 or later.", "Upgrade macOS before enabling Tart pools.")
		}
	}
	b, e := t.run(ctx, c, []string{"--version"}, nil)
	if e != nil {
		return e
	}
	if !supportedTartVersion(string(b)) {
		return problem(ErrDependency, "Runmoor requires a stable Tart 2.x.x release.", "Install a stable Tart 2.x.x release yourself; Runmoor does not bundle it.")
	}
	return nil
}
func (t *TartDriver) Validate(ctx context.Context, c Config, p Pool, s Snapshot) error {
	if e := t.check(ctx, c); e != nil {
		return e
	}
	im := s.Images[p.Image]
	if im == nil || im.Phase != ImageSealed || im.RunnerVersion != p.RunnerVersion || im.RunnerPath != p.RunnerPath {
		return problem(ErrImage, "Pool image is not a compatible sealed revision.", "Seal a clean image with the configured runner version and path, then update the pool.")
	}
	return t.validateSealed(ctx, c, im, s.Installation)
}

func (t *TartDriver) validateSealed(ctx context.Context, c Config, im *Image, installation string) error {
	v, e := t.vmOwned(ctx, c, im.VM, installation, im.ID)
	if e != nil {
		return e
	}
	if e = validateTartGuestOS(v.OS); e != nil {
		return e
	}
	if v.Running || v.State == "suspended" {
		return problem(ErrImage, "A sealed base image must remain stopped.", "Stop external access to the base and prepare a new clean revision.")
	}
	digest, e := imageDigestOwned(ctx, c, im.VM, installation, im.ID)
	if e != nil {
		return e
	}
	if im.Digest == "" || digest != im.Digest {
		return problem(ErrImage, "Sealed image contents no longer match their recorded digest.", "Restore the matching base from backup or prepare and seal a new revision; do not reuse the altered base.")
	}
	return nil
}

type vmInfo struct {
	Running bool
	State   string
	CPU     int
	Memory  uint64
	OS      tartGuestOS
}

type vmOwner struct {
	Installation string `json:"installation"`
	Entity       string `json:"entity"`
	VM           string `json:"vm"`
}

const vmOwnerMarkerName = ".runmoor-owner.json"

func vmPath(c Config, name string) string { return filepath.Join(c.Storage.Data, "tart", "vms", name) }
func vmOwnerPath(c Config, name string) string {
	return filepath.Join(c.Storage.Data, "tart-ownership", name+".json")
}
func vmOwnerMarkerPath(c Config, name string) string {
	return filepath.Join(vmPath(c, name), vmOwnerMarkerName)
}

func ambiguousVMOwnership() error {
	return problem(ErrOwnership, "Tart VM ownership is ambiguous.", "Preserve the VM, Runmoor record and reservation. Restore a paired backup with its embedded identity or reimport the VM as a new revision; do not adopt or delete it by name.")
}

func claimVM(c Config, name, installation, entity string) error {
	if !safeName.MatchString(name) || !validID(installation) || !validID(entity) {
		return problem(ErrOwnership, "Invalid VM ownership identity.", "Preserve local state and inspect the installation.")
	}
	if _, e := os.Lstat(vmPath(c, name)); e == nil {
		return problem(ErrOwnership, "VM destination already exists.", "Never adopt an unrecorded VM; use a new image or runner identity.")
	} else if !os.IsNotExist(e) {
		return problem(ErrPermission, "Cannot inspect the VM destination.", "Check private data directory permissions.")
	}
	if e := privateDir(filepath.Join(c.Storage.Data, "tart-ownership")); e != nil {
		return e
	}
	if e := privateDir(filepath.Join(c.Storage.Data, "tart")); e != nil {
		return e
	}
	if e := privateDir(filepath.Join(c.Storage.Data, "tart", "vms")); e != nil {
		return e
	}
	f, e := openPrivate(vmOwnerPath(c, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if e != nil {
		return e
	}
	b, _ := json.Marshal(vmOwner{installation, entity, name})
	if _, e = f.Write(b); e != nil {
		_ = f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		_ = f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return syncPrivateDir(filepath.Join(c.Storage.Data, "tart-ownership"))
}
func verifyVMOwnerRecord(c Config, name, installation, entity string) error {
	if !safeName.MatchString(name) || !validID(installation) || !validID(entity) {
		return ambiguousVMOwnership()
	}
	b, e := readPrivate(vmOwnerPath(c, name), 4096)
	var o vmOwner
	if e != nil || json.Unmarshal(b, &o) != nil || o != (vmOwner{installation, entity, name}) {
		return ambiguousVMOwnership()
	}
	return nil
}

func vmDirectoryInfo(c Config, name string) (os.FileInfo, error) {
	if !safeName.MatchString(name) {
		return nil, ambiguousVMOwnership()
	}
	info, err := privateVMDirectory(vmPath(c, name))
	if err != nil {
		return nil, ambiguousVMOwnership()
	}
	return info, nil
}

func openVerifiedVMOwnerAt(c Config, pathName, identityName, installation, entity string) (*os.File, error) {
	if !safeName.MatchString(pathName) || !safeName.MatchString(identityName) {
		return nil, ambiguousVMOwnership()
	}
	return openVerifiedVMOwnerPath(c, vmPath(c, pathName), pathName, identityName, installation, entity)
}

func openVerifiedVMOwnerPath(c Config, directoryPath, pathName, identityName, installation, entity string) (*os.File, error) {
	if !safeName.MatchString(pathName) || !safeName.MatchString(identityName) {
		return nil, ambiguousVMOwnership()
	}
	if err := verifyVMOwnerRecord(c, identityName, installation, entity); err != nil {
		return nil, err
	}
	before, err := privateVMDirectory(directoryPath)
	if err != nil {
		return nil, ambiguousVMOwnership()
	}
	dir, err := openTartVMDirectory(directoryPath)
	if err != nil {
		return nil, ambiguousVMOwnership()
	}
	keepOpen := false
	defer func() {
		if !keepOpen {
			_ = dir.Close()
		}
	}()
	opened, err := dir.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, ambiguousVMOwnership()
	}
	b, err := readTartVMOwnerMarker(dir, 4096)
	var marker vmOwner
	if err != nil || json.Unmarshal(b, &marker) != nil || marker != (vmOwner{installation, entity, identityName}) {
		return nil, ambiguousVMOwnership()
	}
	after, err := privateVMDirectory(directoryPath)
	if err != nil || !os.SameFile(before, after) {
		return nil, ambiguousVMOwnership()
	}
	if err = verifyVMOwnerRecord(c, identityName, installation, entity); err != nil {
		return nil, err
	}
	keepOpen = true
	return dir, nil
}

func verifyVMOwnerAt(c Config, pathName, identityName, installation, entity string) error {
	dir, err := openVerifiedVMOwnerAt(c, pathName, identityName, installation, entity)
	if dir != nil {
		_ = dir.Close()
	}
	return err
}

func verifyVMOwner(c Config, name, installation, entity string) error {
	return verifyVMOwnerAt(c, name, name, installation, entity)
}

func publishVMOwnerMarker(c Config, name, installation, entity string) error {
	path := vmPath(c, name)
	return publishVMOwnerMarkerAt(c, path, name, installation, entity)
}

// publishVMOwnerMarkerAt pins the just-created directory before writing the
// marker, writes through that descriptor, and verifies that the name still
// resolves to the same directory afterward. A replacement cannot receive the
// marker after the descriptor has been opened.
func publishVMOwnerMarkerAt(c Config, directoryPath, identityName, installation, entity string) error {
	if err := verifyVMOwnerRecord(c, identityName, installation, entity); err != nil {
		return err
	}
	before, err := privateVMDirectory(directoryPath)
	if err != nil {
		return ambiguousVMOwnership()
	}
	dir, err := openTartVMDirectory(directoryPath)
	if err != nil {
		return ambiguousVMOwnership()
	}
	defer dir.Close()
	opened, err := dir.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return ambiguousVMOwnership()
	}
	if _, err = readTartVMOwnerMarker(dir, 4096); err == nil || !os.IsNotExist(err) {
		return ambiguousVMOwnership()
	}
	encoded, err := json.Marshal(vmOwner{installation, entity, identityName})
	if err != nil {
		return ambiguousVMOwnership()
	}
	if err = publishTartVMOwnerMarker(dir, encoded); err != nil {
		if os.IsExist(err) {
			return ambiguousVMOwnership()
		}
		return problem(ErrPermission, "Cannot publish the Tart VM ownership marker.", "Preserve the VM and check filesystem support and directory permissions.")
	}
	after, err := privateVMDirectory(directoryPath)
	if err != nil || !os.SameFile(opened, after) {
		return ambiguousVMOwnership()
	}
	if err = dir.Sync(); err != nil {
		return problem(ErrPermission, "Cannot sync the Tart VM ownership marker.", "Preserve the VM and check directory permissions.")
	}
	b, err := readTartVMOwnerMarker(dir, 4096)
	var marker vmOwner
	if err != nil || json.Unmarshal(b, &marker) != nil || marker != (vmOwner{installation, entity, identityName}) {
		return ambiguousVMOwnership()
	}
	return verifyVMOwnerRecord(c, identityName, installation, entity)
}

func publishAndMoveCreatedTartVM(c Config, home, stageName, name, installation, entity string) error {
	// Publish ownership through the opened VM directory, then expose that same
	// inode at the canonical name with an exclusive rename. The caller holds the
	// per-entity creation-home lock through the entire Tart operation.
	if !safeName.MatchString(stageName) || !safeName.MatchString(name) || stageName == name {
		return ambiguousVMOwnership()
	}
	if err := verifyVMOwnerRecord(c, name, installation, entity); err != nil {
		return err
	}
	stagePath := tartVMPathAtHome(home, stageName)
	dir, err := openTartVMDirectory(stagePath)
	if err != nil {
		return ambiguousVMOwnership()
	}
	defer dir.Close()
	lock, err := lockTartVMConfigAt(dir)
	if err != nil {
		return problem(ErrOwnership, "Cannot lock the newly created Tart VM before ownership publication.", "Preserve the staging VM and its Runmoor record for diagnosis.")
	}
	defer lock.Close()
	opened, err := dir.Stat()
	pathInfo, pathErr := privateVMDirectory(stagePath)
	if err != nil || pathErr != nil || !os.SameFile(opened, pathInfo) {
		return ambiguousVMOwnership()
	}
	finalPath := vmPath(c, name)
	if _, err = os.Lstat(finalPath); err == nil || !os.IsNotExist(err) {
		return ambiguousVMOwnership()
	}
	if err = publishVMOwnerMarkerAt(c, stagePath, name, installation, entity); err != nil {
		return err
	}
	pathInfo, pathErr = privateVMDirectory(stagePath)
	if pathErr != nil || !os.SameFile(opened, pathInfo) {
		return ambiguousVMOwnership()
	}
	if _, err = os.Lstat(finalPath); err == nil || !os.IsNotExist(err) {
		return ambiguousVMOwnership()
	}
	if err = renameTartVMNoReplace(stagePath, finalPath); err != nil {
		return ambiguousVMOwnership()
	}
	moved, err := privateVMDirectory(finalPath)
	if err != nil || !os.SameFile(opened, moved) {
		return ambiguousVMOwnership()
	}
	if err = verifyVMOwnerAt(c, name, name, installation, entity); err != nil {
		return err
	}
	if err = syncPrivateDir(filepath.Dir(finalPath)); err != nil {
		return problem(ErrCleanup, "Cannot sync the published Tart VM directory.", "Preserve the VM and retry after checking local storage.")
	}
	return nil
}

func promoteCreatedTartVM(c Config, name, installation, entity string) error {
	home := tartCreationHome(c, entity)
	if _, err := os.Lstat(home); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return problem(ErrPermission, "Cannot inspect the private Tart creation home.", "Preserve the Runmoor record and check local data directory permissions.")
	}
	if _, err := privateVMDirectory(home); err != nil {
		return err
	}
	homeLock, err := lockTartCreationHome(home)
	if err != nil {
		return err
	}
	locked := true
	releaseHomeLock := func() {
		if locked {
			_ = homeLock.Close()
			locked = false
		}
	}
	defer releaseHomeLock()
	if err := privateDir(home); err != nil {
		return err
	}
	if err := privateDir(filepath.Join(home, "vms")); err != nil {
		return err
	}
	stagePath := tartCreationVMPath(c, entity)
	if _, err = os.Lstat(stagePath); os.IsNotExist(err) {
		releaseHomeLock()
		cleanupTartCreationHome(c, entity)
		return nil
	} else if err != nil {
		return problem(ErrPermission, "Cannot inspect the private Tart creation VM.", "Preserve the Runmoor record and check local data directory permissions.")
	}
	if err := verifyVMOwnerRecord(c, name, installation, entity); err != nil {
		return err
	}
	dir, err := openVerifiedVMOwnerPath(c, stagePath, creationVMName(entity), name, installation, entity)
	if err != nil {
		return err
	}
	defer dir.Close()
	lock, err := lockTartVMConfigAt(dir)
	if err != nil {
		return ambiguousVMOwnership()
	}
	defer lock.Close()
	finalPath := vmPath(c, name)
	if _, err = os.Lstat(finalPath); err == nil || !os.IsNotExist(err) {
		return ambiguousVMOwnership()
	}
	if err = renameTartVMNoReplace(stagePath, finalPath); err != nil {
		return ambiguousVMOwnership()
	}
	opened, err := dir.Stat()
	moved, movedErr := privateVMDirectory(finalPath)
	if err != nil || movedErr != nil || !os.SameFile(opened, moved) {
		return ambiguousVMOwnership()
	}
	if err = verifyVMOwnerAt(c, name, name, installation, entity); err != nil {
		return err
	}
	if err = syncPrivateDir(filepath.Dir(finalPath)); err != nil {
		return problem(ErrCleanup, "Cannot sync the recovered Tart VM directory.", "Preserve the VM and retry after checking local storage.")
	}
	releaseHomeLock()
	cleanupTartCreationHome(c, entity)
	return nil
}

func (t *TartDriver) runOwned(ctx context.Context, c Config, installation, entity, name string, args []string, in io.Reader) ([]byte, error) {
	return t.runOwnedAtHome(ctx, c, tartHome(c), name, name, installation, entity, args, in)
}

func (t *TartDriver) runOwnedAt(ctx context.Context, c Config, pathName, identityName, installation, entity string, args []string, in io.Reader) ([]byte, error) {
	return t.runOwnedAtHome(ctx, c, tartHome(c), pathName, identityName, installation, entity, args, in)
}

func (t *TartDriver) runOwnedAtHome(ctx context.Context, c Config, home, pathName, identityName, installation, entity string, args []string, in io.Reader) ([]byte, error) {
	dir, err := openVerifiedVMOwnerAt(c, pathName, identityName, installation, entity)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	alias, cleanupAlias, err := createTartCommandAliasAt(c, home, "rm-op-"+newID())
	if err != nil {
		return nil, err
	}
	defer cleanupAlias()
	pinnedArgs, replaced := replaceCommandVMName(args, pathName, alias)
	if !replaced {
		return nil, ambiguousVMOwnership()
	}
	executor, ok := t.Exec.(PinnedCommandExecutor)
	if !ok {
		return nil, problem(ErrDependency, "The Tart command runner cannot bind operations to an owned VM.", "Use the supported Runmoor release and preserve the VM until its ownership can be verified.")
	}
	b, runErr := executor.RunPinned(ctx, c.TartExecutable, pinnedArgs, tartEnvAt(c, home), in, dir)
	if runErr != nil {
		runErr = problem(ErrDependency, "Tart command failed.", "Check Tart 2.x.x, Guest Agent RPC and the private VM directory.")
	}
	if err := verifyVMOwnerAt(c, pathName, identityName, installation, entity); err != nil {
		return nil, err
	}
	return b, runErr
}

func replaceCommandVMName(args []string, original, replacement string) ([]string, bool) {
	result := append([]string(nil), args...)
	replaced := false
	for i, arg := range result {
		if arg == original {
			result[i] = replacement
			replaced = true
		}
	}
	return result, replaced
}

func (t *TartDriver) startOwned(ctx context.Context, c Config, name, installation, entity string, args []string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	dir, err := openVerifiedVMOwnerAt(c, name, name, installation, entity)
	if err != nil {
		return 0, err
	}
	defer dir.Close()
	current, err := t.vmOwned(ctx, c, name, installation, entity)
	if err != nil {
		return 0, err
	}
	if err = ctx.Err(); err != nil {
		return 0, err
	}
	if current.Running {
		return 0, problem(ErrPreparation, "The owned Tart VM is already running.", "Wait for it to stop before starting it again.")
	}
	alias := tartRunAlias(entity)
	if err = removeStaleTartRunAlias(c, alias); err != nil {
		return 0, err
	}
	if _, _, err = createTartCommandAlias(c, alias); err != nil {
		return 0, err
	}
	if err = ctx.Err(); err != nil {
		_ = removeTartRunAlias(c, alias)
		return 0, err
	}
	pinnedArgs, replaced := replaceCommandVMName(args, name, alias)
	if !replaced {
		_ = removeTartRunAlias(c, alias)
		return 0, ambiguousVMOwnership()
	}
	executor, ok := t.Exec.(PinnedCommandExecutor)
	if !ok {
		_ = removeTartRunAlias(c, alias)
		return 0, problem(ErrDependency, "The Tart command runner cannot bind operations to an owned VM.", "Use the supported Runmoor release and preserve the VM until its ownership can be verified.")
	}
	if err = ctx.Err(); err != nil {
		_ = removeTartRunAlias(c, alias)
		return 0, err
	}
	pid, runErr := executor.StartPinned(c.TartExecutable, pinnedArgs, tartEnv(c), dir)
	if runErr != nil {
		_ = removeTartRunAlias(c, alias)
		return 0, problem(ErrDependency, "Tart command failed.", "Check Tart 2.x.x, Guest Agent RPC and the private VM directory.")
	}
	if err = verifyVMOwnerAt(c, name, name, installation, entity); err != nil {
		return 0, err
	}
	return pid, nil
}

func tartRunAlias(entity string) string { return "rm-run-" + entity }

func createTartCommandAlias(c Config, alias string) (string, func(), error) {
	return createTartCommandAliasAt(c, tartHome(c), alias)
}

func createTartCommandAliasAt(c Config, home, alias string) (string, func(), error) {
	if !safeName.MatchString(alias) {
		return "", nil, ambiguousVMOwnership()
	}
	vmRoot := filepath.Join(home, "vms")
	if err := privateDir(vmRoot); err != nil {
		return "", nil, err
	}
	path := filepath.Join(vmRoot, alias)
	if err := os.Symlink("/dev/fd/3", path); err != nil {
		if os.IsExist(err) {
			return "", nil, ambiguousVMOwnership()
		}
		return "", nil, problem(ErrPermission, "Cannot create a private Tart VM identity alias.", "Check Tart storage permissions and preserve the owned VM.")
	}
	return alias, func() {
		if target, err := os.Readlink(path); err == nil && target == "/dev/fd/3" {
			_ = os.Remove(path)
		}
	}, nil
}

func removeTartRunAlias(c Config, alias string) error {
	present, err := tartRunAliasPresent(c, alias)
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	path := filepath.Join(c.Storage.Data, "tart", "vms", alias)
	if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
		return problem(ErrCleanup, "Cannot remove the stopped Tart VM identity alias.", "Preserve the VM and inspect its private Tart storage before retrying.")
	}
	return nil
}

func tartRunAliasPresent(c Config, alias string) (bool, error) {
	if !safeName.MatchString(alias) {
		return false, ambiguousVMOwnership()
	}
	path := filepath.Join(c.Storage.Data, "tart", "vms", alias)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return false, ambiguousVMOwnership()
	}
	target, err := os.Readlink(path)
	if err != nil || target != "/dev/fd/3" {
		return false, ambiguousVMOwnership()
	}
	return true, nil
}

func (t *TartDriver) tartProcessAlive(pid int) (bool, error) {
	if t.processAlive != nil {
		return t.processAlive(pid)
	}
	return tartRunProcessAlive(pid)
}

// A missing canonical directory is not proof that a detached Tart run ended:
// the open-directory alias and recorded Tart process can outlive that name.
// Preserve the execution until its recorded process is confirmed exited.
func (t *TartDriver) confirmTartVMAbsent(c Config, r Runner) error {
	aliasPresent, err := tartRunAliasPresent(c, tartRunAlias(r.ID))
	if err != nil {
		return err
	}
	if r.Handle.PID <= 0 {
		if aliasPresent {
			return ambiguousVMOwnership()
		}
		return nil
	}
	alive, err := t.tartProcessAlive(r.Handle.PID)
	if err != nil || alive {
		return ambiguousVMOwnership()
	}
	return nil
}

func removeStaleTartRunAlias(c Config, alias string) error {
	return removeTartRunAlias(c, alias)
}

func (t *TartDriver) vmOwned(ctx context.Context, c Config, name, installation, entity string) (vmInfo, error) {
	b, err := t.runOwned(ctx, c, installation, entity, name, []string{"get", name, "--format", "json"}, nil)
	var v vmInfo
	if err != nil {
		return v, err
	}
	if json.Unmarshal(b, &v) != nil {
		return v, problem(ErrDependency, "Tart returned an incompatible VM description.", "Check the installed Tart 2.x.x release.")
	}
	return v, nil
}

func (t *TartDriver) vmOwnedAt(ctx context.Context, c Config, pathName, identityName, installation, entity string) (vmInfo, error) {
	b, err := t.runOwnedAt(ctx, c, pathName, identityName, installation, entity, []string{"get", pathName, "--format", "json"}, nil)
	var v vmInfo
	if err != nil {
		return v, err
	}
	if json.Unmarshal(b, &v) != nil {
		return v, problem(ErrDependency, "Tart returned an incompatible VM description.", "Check the installed Tart 2.x.x release.")
	}
	return v, nil
}

func imageDigestOwned(ctx context.Context, c Config, name, installation, entity string) (string, error) {
	if err := verifyVMOwner(c, name, installation, entity); err != nil {
		return "", err
	}
	digest, digestErr := imageDigest(ctx, c, name)
	if err := verifyVMOwner(c, name, installation, entity); err != nil {
		return "", err
	}
	return digest, digestErr
}
func (t *TartDriver) Prepare(ctx context.Context, c Config, p Pool, r Runner, s Snapshot, jit string, publish func(Handle) error) error {
	if e := t.Validate(ctx, c, p, s); e != nil {
		return e
	}
	im := s.Images[p.Image]
	name := "rm-" + r.ID
	creationHome, creationLock, e := prepareTartCreationHome(c, r.ID)
	if e != nil {
		return e
	}
	defer func() {
		_ = creationLock.Close()
		cleanupTartCreationHome(c, r.ID)
	}()
	if e := claimVM(c, name, s.Installation, r.ID); e != nil {
		return e
	}
	h := Handle{VM: name}
	if e := publish(h); e != nil {
		return e
	}
	stageVM := creationVMName(r.ID)
	if _, e := t.runOwnedAtHome(ctx, c, creationHome, im.VM, im.VM, s.Installation, im.ID, []string{"clone", im.VM, stageVM}, nil); e != nil {
		return e
	}
	if e := publishAndMoveCreatedTartVM(c, creationHome, stageVM, name, s.Installation, r.ID); e != nil {
		return e
	}
	if _, e := t.runOwned(ctx, c, s.Installation, r.ID, name, []string{"set", name, "--cpu", strconv.Itoa(p.Resources.CPU), "--memory", strconv.FormatInt(p.Resources.MemoryMiB, 10)}, nil); e != nil {
		return e
	}
	pid, e := t.startOwned(ctx, c, name, s.Installation, r.ID, []string{"run", "--no-graphics", "--no-audio", name})
	if e != nil {
		if p, ok := e.(*Problem); ok && p.Code == ErrOwnership {
			return e
		}
		return problem(ErrPreparation, "Cannot start the owned Tart clone.", "Check virtualization support and the two-VM limit.")
	}
	h.PID = pid
	if e = publish(h); e != nil {
		return e
	}
	if e = t.guestReadyOwned(ctx, c, name, p.RunnerPath, p.RunnerVersion, s.Installation, r.ID); e != nil {
		return e
	}
	exe := t.GuestExecutable
	if exe == "" {
		exe, e = os.Executable()
	}
	if e != nil {
		return problem(ErrPreparation, "Cannot locate the Runmoor guest helper.", "Run an installed release binary.")
	}
	binary, e := os.Open(exe)
	if e != nil {
		return e
	}
	defer binary.Close()
	// The helper is Runmoor's own native arm64 binary. Its upload carries no
	// management credentials, and all dynamic data is argv or stdin, never shell
	// interpolation. The detached guest supervisor survives an exec disconnect.
	if _, e = t.runOwned(ctx, c, s.Installation, r.ID, name, []string{"exec", "-i", name, "/bin/sh", "-c", `umask 077; mkdir -p /tmp/runmoor; cat > /tmp/runmoor/helper; chmod 700 /tmp/runmoor/helper`}, binary); e != nil {
		return e
	}
	b, _ := json.Marshal(GuestInput{ID: r.ID, RunnerPath: p.RunnerPath, RunnerVersion: p.RunnerVersion, JIT: jit})
	_, e = t.runOwned(ctx, c, s.Installation, r.ID, name, []string{"exec", "-i", name, "/tmp/runmoor/helper", "__guest-bootstrap"}, strings.NewReader(string(b)))
	if e != nil {
		if p, ok := e.(*Problem); ok && p.Code == ErrOwnership {
			return e
		}
		return problem(ErrPreparation, "Guest runner bootstrap did not confirm a live runner.", "Check that run.sh is executable and starts the pinned runner successfully, then resume the pool.")
	}
	return nil
}

// guestReadyUnowned tests guest validation response handling without a VM
// fixture. Runtime flows must use guestReadyOwned for Runmoor-owned VMs.
func (t *TartDriver) guestReadyUnowned(ctx context.Context, c Config, vm, path, version string) error {
	for {
		b, e := t.run(ctx, c, []string{"exec", vm, "/bin/sh", "-c", guestValidateScript, "runmoor", path, version, GuestAgentVersion}, nil)
		if e == nil && strings.TrimSpace(string(b)) == "RUNMOOR_READY" {
			return nil
		}
		if e == nil {
			return problem(ErrImage, "Guest validation rejected the runner, guest agent or clean workspace.", "Use the image preparation guide to install exact versions and remove runner credentials and workspaces.")
		}
		if !waitContext(ctx, time.Second) {
			return problem(ErrPreparation, "Tart Guest Agent did not become ready before the deadline.", "Enable Guest Agent RPC in the logged-in runner account and check the prepared image.")
		}
	}
}

func (t *TartDriver) guestReadyOwned(ctx context.Context, c Config, vm, path, version, installation, entity string) error {
	for {
		b, e := t.runOwned(ctx, c, installation, entity, vm, []string{"exec", vm, "/bin/sh", "-c", guestValidateScript, "runmoor", path, version, GuestAgentVersion}, nil)
		if e == nil && strings.TrimSpace(string(b)) == "RUNMOOR_READY" {
			return nil
		}
		if e == nil {
			return problem(ErrImage, "Guest validation rejected the runner, guest agent or clean workspace.", "Use the image preparation guide to install exact versions and remove runner credentials and workspaces.")
		}
		if p, ok := e.(*Problem); ok && p.Code == ErrOwnership {
			return e
		}
		if !waitContext(ctx, time.Second) {
			return problem(ErrPreparation, "Tart Guest Agent did not become ready before the deadline.", "Enable Guest Agent RPC in the logged-in runner account and check the prepared image.")
		}
	}
}

func (t *TartDriver) preparedGuestOwned(ctx context.Context, c Config, vm, path, installation, entity string) error {
	b, err := t.runOwned(ctx, c, installation, entity, vm, []string{"exec", vm, "/bin/sh", "-c", preparedGuestScript, "runmoor", path, GuestAgentVersion}, nil)
	if err != nil {
		if p, ok := err.(*Problem); ok && p.Code == ErrOwnership {
			return err
		}
		return problem(ErrPreparation, "Tart Guest Agent RPC is not ready.", "Log into the non-root runner account and enable Guest Agent 0.14.2 RPC at login.")
	}
	if strings.TrimSpace(string(b)) != "RUNMOOR_READY" {
		return problem(ErrImage, "The macOS guest is not prepared for Runmoor.", "Use a non-root runner account, Guest Agent 0.14.2 and a clean dedicated runner directory.")
	}
	return nil
}

const preparedGuestScript = `set -eu
invalid() { printf 'RUNMOOR_INVALID\n'; exit 0; }
[ "$(id -u)" != 0 ] || invalid
agent=$(tart-guest-agent --version | awk '{print $NF}')
case "$agent" in "$2"|"$2"-*) ;; *) invalid;; esac
p="$1"; while [ "$p" != / ]; do [ ! -L "$p" ] || invalid; p=$(dirname "$p"); done
for f in .runner .credentials .credentials_rsaparams; do [ ! -e "$1/$f" ] || invalid; done
[ ! -d "$1/_work" ] || [ -z "$(ls -A "$1/_work")" ] || invalid
printf 'RUNMOOR_READY\n'`

// A reachable guest returns a bounded validation result with a successful RPC.
// Nonzero Tart exec results remain transport failures, which may recover while
// the VM boots. Do not conflate a rejected image with an unavailable agent.
const guestValidateScript = `set -eu
invalid() { printf 'RUNMOOR_INVALID\n'; exit 0; }
uid=$(id -u) || invalid
case "$uid" in ''|0) invalid;; esac
agent_version=$(tart-guest-agent --version | awk '{print $NF}')
case "$agent_version" in "$3"|"$3"-*) ;; *) invalid;; esac
cd "$1" || invalid
[ "$(RUNNER_LOG_TO_STDOUT=0 ./bin/Runner.Listener --version 2>/dev/null | sed -n '/^[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*$/p')" = "$2" ] || invalid
for file in .runner .credentials .credentials_rsaparams; do [ ! -e "$file" ] || invalid; done
if [ -d _work ]; then
  work=$(ls -A _work) || invalid
  [ -z "$work" ] || invalid
fi
printf 'RUNMOOR_READY\n'
`

func (t *TartDriver) Inspect(ctx context.Context, c Config, r Runner, s Snapshot) (Observation, error) {
	name := r.Handle.VM
	if name == "" {
		name = "rm-" + r.ID
	}
	h := r.Handle
	h.VM = name
	if !safeName.MatchString(name) {
		return Observation{}, ambiguousVMOwnership()
	}
	if _, e := os.Lstat(vmPath(c, name)); os.IsNotExist(e) {
		if promoteErr := promoteCreatedTartVM(c, name, s.Installation, r.ID); promoteErr != nil {
			return Observation{}, promoteErr
		}
		if _, e = os.Lstat(vmPath(c, name)); os.IsNotExist(e) {
			if e = t.confirmTartVMAbsent(c, r); e != nil {
				return Observation{}, e
			}
			return Observation{Handle: h}, nil
		} else if e != nil {
			return Observation{}, problem(ErrPermission, "Cannot inspect the Tart VM directory.", "Check private data directory permissions; the execution reservation remains held.")
		}
	} else if e != nil {
		return Observation{}, problem(ErrPermission, "Cannot inspect the Tart VM directory.", "Check private data directory permissions; the execution reservation remains held.")
	}
	v, e := t.vmOwned(ctx, c, name, s.Installation, r.ID)
	if e != nil {
		return Observation{}, e
	}
	out := Observation{Exists: true, Running: v.Running, Handle: h}
	if !v.Running {
		return out, nil
	}
	b, e := t.runOwned(ctx, c, s.Installation, r.ID, name, []string{"exec", name, "/tmp/runmoor/helper", "__guest-status", r.ID}, nil)
	if e != nil {
		if p, ok := e.(*Problem); ok && p.Code == ErrOwnership {
			return Observation{}, e
		}
		if r.Phase == Preparing {
			return out, nil
		}
		return Observation{}, problem(ErrRetry, "Guest runner state is temporarily unavailable.", "Restore Guest Agent connectivity; the VM and reservation are preserved.")
	}
	var g GuestStatus
	if json.Unmarshal(b, &g) != nil || g.ID != r.ID {
		return Observation{}, problem(ErrOwnership, "Guest runner status does not match its execution identity.", "Keep this VM quarantined for diagnosis.")
	}
	if g.Finished {
		out.Running = false
		out.ExitCode = &g.ExitCode
	} else if !g.Ready {
		return Observation{}, problem(ErrRetry, "Guest runner startup has not completed.", "Wait for bootstrap or the original preparation deadline; the VM and reservation are preserved.")
	}
	return out, nil
}
func (t *TartDriver) Stop(ctx context.Context, c Config, r Runner, s Snapshot) error {
	name := r.Handle.VM
	if name == "" {
		name = "rm-" + r.ID
	}
	if !safeName.MatchString(name) {
		return ambiguousVMOwnership()
	}
	if _, e := os.Lstat(vmPath(c, name)); os.IsNotExist(e) {
		return t.confirmTartVMAbsent(c, r)
	} else if e != nil {
		return problem(ErrPermission, "Cannot inspect the Tart VM directory.", "Check private data directory permissions; the execution reservation remains held.")
	}
	v, e := t.vmOwned(ctx, c, name, s.Installation, r.ID)
	if e != nil {
		return e
	}
	if !v.Running {
		return removeTartRunAlias(c, tartRunAlias(r.ID))
	}
	if v.Running {
		// Tart addresses VMs by name. Recheck after get and immediately before
		// stopping so a name replacement during inspection is left untouched.
		if _, e = t.runOwned(ctx, c, s.Installation, r.ID, name, []string{"stop", name}, nil); e != nil {
			return e
		}
	}
	for {
		v, e = t.vmOwned(ctx, c, name, s.Installation, r.ID)
		if e != nil {
			return e
		}
		if !v.Running {
			return removeTartRunAlias(c, tartRunAlias(r.ID))
		}
		if !waitContext(ctx, 200*time.Millisecond) {
			return problem(ErrCleanup, "Tart VM termination could not be confirmed.", "Restore Tart access and retry cleanup; its reservation remains held.")
		}
	}
}

func removeVMOwnerRecord(c Config, name, installation, entity string) error {
	if !safeName.MatchString(name) {
		return ambiguousVMOwnership()
	}
	if _, err := os.Lstat(vmOwnerPath(c, name)); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return problem(ErrCleanup, "Cannot inspect the Tart VM ownership record.", "Preserve local state and check private data directory permissions.")
	}
	if err := verifyVMOwnerRecord(c, name, installation, entity); err != nil {
		return err
	}
	if err := os.Remove(vmOwnerPath(c, name)); err != nil && !os.IsNotExist(err) {
		return problem(ErrCleanup, "Cannot remove the completed VM ownership record.", "Check private data directory permissions; cleanup remains pending.")
	}
	return nil
}

func deletionVMName(entity string) string {
	return "rm-delete-" + entity
}

func cleanupTartVMAt(ctx context.Context, t *TartDriver, c Config, pathName, identityName, installation, entity string) error {
	if err := verifyVMOwnerAt(c, pathName, identityName, installation, entity); err != nil {
		return err
	}
	v, err := t.vmOwnedAt(ctx, c, pathName, identityName, installation, entity)
	if err != nil {
		return err
	}
	if v.Running {
		return problem(ErrCleanup, "Cannot delete a running VM.", "Confirm termination first.")
	}
	// This name is derived from the stable entity UUID. If the manager exits
	// after the atomic move, the next cleanup pass can find the same owned VM.
	if _, err = t.run(ctx, c, []string{"delete", pathName}, nil); err != nil {
		return err
	}
	if _, err = os.Lstat(vmPath(c, pathName)); err == nil {
		if ownerErr := verifyVMOwnerAt(c, pathName, identityName, installation, entity); ownerErr != nil {
			return ownerErr
		}
		return problem(ErrCleanup, "Tart did not remove the stopped VM.", "Inspect the VM and retry cleanup; its reservation remains held.")
	} else if !os.IsNotExist(err) {
		return problem(ErrCleanup, "Cannot confirm Tart VM removal.", "Inspect the VM and retry cleanup; its reservation remains held.")
	}
	return nil
}

func lockOwnedTartVMConfig(c Config, pathName, identityName, installation, entity string) (*os.File, error) {
	dir, err := openVerifiedVMOwnerAt(c, pathName, identityName, installation, entity)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	lock, err := lockTartVMConfigAt(dir)
	if err != nil {
		return nil, problem(ErrCleanup, "Cannot lock the stopped Tart VM for cleanup.", "Confirm no Tart operation is using it, then retry; its reservation remains held.")
	}
	opened, err := dir.Stat()
	current, currentErr := vmDirectoryInfo(c, pathName)
	if err != nil || currentErr != nil || !os.SameFile(opened, current) {
		_ = lock.Close()
		return nil, ambiguousVMOwnership()
	}
	if err = verifyVMOwnerAt(c, pathName, identityName, installation, entity); err != nil {
		_ = lock.Close()
		return nil, err
	}
	return lock, nil
}

func moveOwnedTartVMToDeletionName(c Config, name, deletionName, installation, entity string) (*os.File, error) {
	if !safeName.MatchString(deletionName) || deletionName == name {
		return nil, ambiguousVMOwnership()
	}
	dir, err := openVerifiedVMOwnerAt(c, name, name, installation, entity)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	lock, err := lockTartVMConfigAt(dir)
	if err != nil {
		return nil, problem(ErrCleanup, "Cannot lock the stopped Tart VM for cleanup.", "Confirm no Tart operation is using it, then retry; its reservation remains held.")
	}
	fail := func(err error) (*os.File, error) {
		_ = lock.Close()
		return nil, err
	}
	before, err := dir.Stat()
	if err != nil {
		return fail(ambiguousVMOwnership())
	}
	if err = verifyVMOwner(c, name, installation, entity); err != nil {
		return fail(err)
	}
	current, err := vmDirectoryInfo(c, name)
	if err != nil || !os.SameFile(before, current) {
		return fail(ambiguousVMOwnership())
	}
	if _, err = os.Lstat(vmPath(c, deletionName)); err == nil || !os.IsNotExist(err) {
		return fail(ambiguousVMOwnership())
	}
	// Tart accepts a VM name for delete. Move the verified directory to a
	// stable per-entity name with an exclusive filesystem rename first, so a
	// later replacement at the original name cannot become Tart's delete target.
	if err = renameTartVMNoReplace(vmPath(c, name), vmPath(c, deletionName)); err != nil {
		return fail(problem(ErrCleanup, "Cannot reserve the Tart VM for identity-bound cleanup.", "Preserve the VM and retry after resolving the storage operation; its reservation remains held."))
	}
	moved, err := vmDirectoryInfo(c, deletionName)
	if err != nil || !os.SameFile(before, moved) {
		// A replacement won the source-name race. Restore it only if the original
		// name is still empty; never overwrite a newer VM while recovering.
		_ = renameTartVMNoReplace(vmPath(c, deletionName), vmPath(c, name))
		return fail(ambiguousVMOwnership())
	}
	if err = verifyVMOwnerAt(c, deletionName, name, installation, entity); err != nil {
		_ = renameTartVMNoReplace(vmPath(c, deletionName), vmPath(c, name))
		return fail(err)
	}
	if err = syncPrivateDir(filepath.Join(c.Storage.Data, "tart", "vms")); err != nil {
		return fail(problem(ErrCleanup, "Cannot sync the Tart VM cleanup reservation.", "Preserve the VM and retry cleanup; its reservation remains held."))
	}
	return lock, nil
}

func (t *TartDriver) Cleanup(ctx context.Context, c Config, r Runner, s Snapshot) error {
	name := r.Handle.VM
	if name == "" {
		name = "rm-" + r.ID
	}
	if !safeName.MatchString(name) {
		return ambiguousVMOwnership()
	}
	if err := promoteCreatedTartVM(c, name, s.Installation, r.ID); err != nil {
		return err
	}
	deletionName := deletionVMName(r.ID)
	if !safeName.MatchString(deletionName) {
		return ambiguousVMOwnership()
	}
	var deletionLock *os.File
	defer func() {
		if deletionLock != nil {
			_ = deletionLock.Close()
		}
	}()
	if _, err := os.Lstat(vmPath(c, deletionName)); os.IsNotExist(err) {
		if _, originalErr := os.Lstat(vmPath(c, name)); originalErr == nil {
			if err = verifyVMOwner(c, name, s.Installation, r.ID); err != nil {
				return err
			}
			v, inspectErr := t.vmOwned(ctx, c, name, s.Installation, r.ID)
			if inspectErr != nil {
				return inspectErr
			}
			if v.Running {
				return problem(ErrCleanup, "Cannot delete a running VM.", "Confirm termination first.")
			}
			if deletionLock, err = moveOwnedTartVMToDeletionName(c, name, deletionName, s.Installation, r.ID); err != nil {
				return err
			}
		} else if !os.IsNotExist(originalErr) {
			return problem(ErrCleanup, "Cannot inspect the Tart VM for cleanup.", "Check private data directory permissions; its reservation remains held.")
		} else {
			if err = t.confirmTartVMAbsent(c, r); err != nil {
				return err
			}
			if err = removeVMOwnerRecord(c, name, s.Installation, r.ID); err != nil {
				return err
			}
			cleanupTartCreationHome(c, r.ID)
			return nil
		}
	} else if err != nil {
		return problem(ErrCleanup, "Cannot inspect the Tart cleanup directory.", "Check private data directory permissions; its reservation remains held.")
	} else {
		deletionLock, err = lockOwnedTartVMConfig(c, deletionName, name, s.Installation, r.ID)
		if err != nil {
			return err
		}
	}
	if err := cleanupTartVMAt(ctx, t, c, deletionName, name, s.Installation, r.ID); err != nil {
		return err
	}
	if err := removeTartRunAlias(c, tartRunAlias(r.ID)); err != nil {
		return err
	}
	if err := removeVMOwnerRecord(c, name, s.Installation, r.ID); err != nil {
		return err
	}
	cleanupTartCreationHome(c, r.ID)
	return nil
}

// Keep file reads bounded and hide os.File.WriteTo so io.CopyBuffer cannot
// bypass cancellation checks while hashing a multi-gigabyte VM disk.
type imageDigestReader struct {
	ctx context.Context
	src io.Reader
}

func (r imageDigestReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.src.Read(p)
	if cancelled := r.ctx.Err(); cancelled != nil {
		return n, cancelled
	}
	return n, err
}

func imageDigest(ctx context.Context, c Config, vm string) (string, error) {
	h := sha256.New()
	buf := make([]byte, 64<<10)
	for _, name := range []string{"config.json", "nvram.bin", "disk.img"} {
		if e := ctx.Err(); e != nil {
			return "", e
		}
		f, e := os.Open(filepath.Join(vmPath(c, vm), name))
		if e != nil {
			return "", problem(ErrImage, "Prepared image files are incomplete.", "Prepare a standalone Tart macOS image before sealing.")
		}
		_, e = io.CopyBuffer(h, imageDigestReader{ctx: ctx, src: f}, buf)
		ce := f.Close()
		if cancelled := ctx.Err(); cancelled != nil {
			return "", cancelled
		}
		if e != nil || ce != nil {
			return "", problem(ErrImage, "Cannot hash the prepared image.", "Check disk integrity and retry sealing.")
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
