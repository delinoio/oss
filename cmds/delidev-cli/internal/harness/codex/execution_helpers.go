// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const helperDirectoryLimit = 32

var executionHelperNames = [...]string{"apply_patch", "applypatch", "codex-execve-wrapper"}

type helperStage string
type helperReason string

const (
	helperStartup          helperStage  = "startup"
	helperRetirement       helperStage  = "retirement"
	helperVerified         helperReason = "verified"
	helperMissingProof     helperReason = "missing-startup-proof"
	helperIdentityChanged  helperReason = "identity-changed"
	helperInventoryInvalid helperReason = "inventory-invalid"
	helperAliasChanged     helperReason = "alias-changed"
	helperUnlinkFailed     helperReason = "unlink-failed"
	helperSyncFailed       helperReason = "sync-failed"
)

type helperDirectory struct {
	name string
	root *os.Root
	info os.FileInfo
}

type helperAlias struct {
	name string
	info os.FileInfo
}

// The proof exists only under the original Client. It is never serialized or
// reconstructed from a retained runtime, basename, or another process owner.
// Keep directory handles until joined closure so removal remains anchored to
// the captured original parent rather than a newly opened replacement.
type executionHelpers struct {
	mu                  sync.Mutex
	home                string
	owner               domain.ID
	native              process.Process
	executable          string
	executableFile      *os.File
	executableInfo      os.FileInfo
	directories         []helperDirectory
	before              map[string]bool
	aliases             []helperAlias
	helperDirectoryName string
	captured            bool
	closed              bool
	result              error
	logger              *slog.Logger
	// Tests inject failures at the original anchored mutation boundary.
	unlink     func(*os.Root, string) error
	syncParent func(*os.Root) error
}

func helperFailure() error {
	return domain.Fail(domain.RecoveryRequired, "Original Codex helper cleanup could not be confirmed.", "Retain the original runtime and credential lease for recovery; do not replay accepted input.")
}

func (h *executionHelpers) log(stage helperStage, reason helperReason, removed int) {
	if h.logger != nil {
		h.logger.Info("codex_execution_helper_cleanup", "owner_id", h.owner, "stage", stage, "reason", reason, "alias_count", len(h.aliases), "removed_count", removed, "alias_limit", len(executionHelperNames), "directory_limit", helperDirectoryLimit)
	}
}

func prepareExecutionHelpers(home string, config process.Config) (h *executionHelpers, returned error) {
	h = &executionHelpers{home: home, owner: config.OwnerID, logger: config.Logger, before: map[string]bool{}, unlink: func(root *os.Root, name string) error { return root.Remove(name) }, syncParent: syncHelperParent}
	defer func() {
		if returned != nil {
			h.log(helperStartup, helperIdentityChanged, 0)
			h.release()
		}
	}()
	if config.OwnerID.Validate() != nil || security.CheckPrivateDir(home) != nil {
		return h, helperFailure()
	}
	executable, err := filepath.EvalSymlinks(config.Executable)
	if err != nil || !filepath.IsAbs(executable) {
		return h, helperFailure()
	}
	h.executable = executable
	before, err := os.Lstat(executable)
	if err != nil || !before.Mode().IsRegular() {
		return h, helperFailure()
	}
	h.executableFile, err = os.Open(executable)
	if err != nil {
		return h, helperFailure()
	}
	h.executableInfo, err = h.executableFile.Stat()
	if err != nil || !os.SameFile(before, h.executableInfo) {
		return h, helperFailure()
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return h, helperFailure()
	}
	info, err := helperRootInfo(root)
	if err != nil {
		root.Close()
		return h, helperFailure()
	}
	h.directories = append(h.directories, helperDirectory{root: root, info: info})
	for _, name := range []string{"tmp", "arg0"} {
		parent := h.directories[len(h.directories)-1].root
		if err := parent.Mkdir(name, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return h, helperFailure()
		}
		if err := h.openDirectory(name); err != nil {
			return h, err
		}
	}
	names, err := helperNames(h.directories[2].root)
	if err != nil {
		return h, helperFailure()
	}
	for _, name := range names {
		h.before[name] = true
	}
	if !h.unchanged() {
		return h, helperFailure()
	}
	return h, nil
}

// Root operations confine access but can follow internal links. Check each
// component before opening it and compare the opened identity with the parent
// entry. Every later operation rechecks this retained chain without following
// entries. The final check-to-unlink interval is not a same-user attack sandbox.
func (h *executionHelpers) openDirectory(name string) error {
	parent := h.directories[len(h.directories)-1].root
	before, err := parent.Lstat(name)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return helperFailure()
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return helperFailure()
	}
	info, err := helperRootInfo(root)
	current, currentErr := parent.Lstat(name)
	if err != nil || currentErr != nil || !os.SameFile(before, info) || !os.SameFile(info, current) || current.Mode()&os.ModeSymlink != 0 {
		root.Close()
		return helperFailure()
	}
	h.directories = append(h.directories, helperDirectory{name: name, root: root, info: info})
	return nil
}

func helperRootInfo(root *os.Root) (os.FileInfo, error) {
	file, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	info, statErr := file.Stat()
	return info, errors.Join(statErr, file.Close())
}

func helperNames(root *os.Root) ([]string, error) {
	file, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	names, readErr := file.Readdirnames(helperDirectoryLimit + 1)
	closeErr := file.Close()
	if len(names) > helperDirectoryLimit || closeErr != nil || readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, helperFailure()
	}
	return names, nil
}

func (h *executionHelpers) unchanged() bool {
	canonical, err := filepath.EvalSymlinks(h.home)
	current, statErr := os.Lstat(h.home)
	if err != nil || statErr != nil || canonical != h.home || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(h.directories[0].info, current) {
		return false
	}
	executable, err := filepath.EvalSymlinks(h.executable)
	info, statErr := os.Lstat(h.executable)
	if err != nil || statErr != nil || executable != h.executable || !info.Mode().IsRegular() || !os.SameFile(h.executableInfo, info) || info.Size() != h.executableInfo.Size() || !info.ModTime().Equal(h.executableInfo.ModTime()) {
		return false
	}
	for i := 1; i < len(h.directories); i++ {
		directory := h.directories[i]
		info, err := h.directories[i-1].root.Lstat(directory.name)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(directory.info, info) {
			return false
		}
	}
	return true
}

func (h *executionHelpers) capture(native process.Process) error {
	if h == nil {
		return helperFailure()
	}
	if native.OwnerID != h.owner || native.PID <= 0 || native.Birth == "" || !filepath.IsAbs(native.ScopeDir) || !h.unchanged() {
		h.log(helperStartup, helperIdentityChanged, 0)
		return helperFailure()
	}
	h.native = native
	names, err := helperNames(h.directories[2].root)
	if err != nil {
		h.log(helperStartup, helperInventoryInvalid, 0)
		return helperFailure()
	}
	var added []string
	for _, name := range names {
		if !h.before[name] {
			added = append(added, name)
		}
	}
	// Windows uses regular batch scripts, not this Unix alias profile. Those
	// files retain the unchanged credential scan and no helper unlink authority.
	if runtime.GOOS == "windows" || len(added) == 0 {
		h.captured = true
		h.log(helperStartup, helperVerified, 0)
		return nil
	}
	if len(added) != 1 || h.openDirectory(added[0]) != nil {
		h.log(helperStartup, helperInventoryInvalid, 0)
		return helperFailure()
	}
	h.helperDirectoryName = added[0]
	root := h.directories[3].root
	for _, name := range executionHelperNames {
		info, err := root.Lstat(name)
		target, targetErr := root.Readlink(name)
		if err != nil || targetErr != nil || info.Mode()&os.ModeSymlink == 0 || target != h.executable {
			h.log(helperStartup, helperAliasChanged, 0)
			return helperFailure()
		}
		h.aliases = append(h.aliases, helperAlias{name: name, info: info})
	}
	if !h.unchanged() {
		h.log(helperStartup, helperIdentityChanged, 0)
		return helperFailure()
	}
	h.captured = true
	h.log(helperStartup, helperVerified, 0)
	return nil
}

func syncHelperParent(root *os.Root) error {
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}

func (h *executionHelpers) release() {
	for i := len(h.directories) - 1; i >= 0; i-- {
		_ = h.directories[i].root.Close()
	}
	if h.executableFile != nil {
		_ = h.executableFile.Close()
	}
}

// The caller must first join the original wire/process tree. The only available
// manifest is the proof captured on that Client before product input admission.
func (h *executionHelpers) retire(home string, owner domain.ID, native process.Process) (returned error) {
	if h == nil || h.home != home || h.owner != owner || h.native != native {
		return helperFailure()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return h.result
	}
	removed := 0
	reason := helperVerified
	defer func() {
		h.result, h.closed = returned, true
		h.log(helperRetirement, reason, removed)
		h.release()
	}()
	if !h.captured {
		reason = helperMissingProof
		return helperFailure()
	}
	// Native destructor cleanup can remove the original helper directory. Keep
	// its original parent pinned and validate that parent before syncing absence.
	helperAbsent := false
	if len(h.aliases) != 0 {
		directory := h.directories[3]
		_, err := h.directories[2].root.Lstat(directory.name)
		helperAbsent = errors.Is(err, os.ErrNotExist)
		if helperAbsent {
			h.directories = h.directories[:3]
			defer directory.root.Close()
		}
	}
	if !h.unchanged() {
		reason = helperIdentityChanged
		return helperFailure()
	}
	if len(h.aliases) == 0 {
		return nil
	}
	if helperAbsent {
		if h.syncParent(h.directories[2].root) != nil {
			reason = helperSyncFailed
			return helperFailure()
		}
		if _, err := h.directories[2].root.Lstat(h.helperDirectoryName); !errors.Is(err, os.ErrNotExist) {
			reason = helperIdentityChanged
			return helperFailure()
		}
		if !h.unchanged() {
			reason = helperIdentityChanged
			return helperFailure()
		}
		return nil
	}
	root := h.directories[3].root
	// Validate every original before removing the first alias, then recheck each
	// entry at its own unlink boundary. Missing originals grant no new authority.
	validate := func(alias helperAlias) (bool, bool) {
		info, err := root.Lstat(alias.name)
		if errors.Is(err, os.ErrNotExist) {
			return true, false
		}
		target, targetErr := root.Readlink(alias.name)
		return err == nil && targetErr == nil && info.Mode()&os.ModeSymlink != 0 && os.SameFile(alias.info, info) && target == h.executable, true
	}
	for _, alias := range h.aliases {
		if valid, _ := validate(alias); !valid {
			reason = helperAliasChanged
			return helperFailure()
		}
	}
	for _, alias := range h.aliases {
		if !h.unchanged() {
			reason = helperIdentityChanged
			return helperFailure()
		}
		valid, exists := validate(alias)
		if !valid {
			reason = helperAliasChanged
			return helperFailure()
		}
		if exists {
			if h.unlink(root, alias.name) != nil {
				reason = helperUnlinkFailed
				return helperFailure()
			}
			removed++
		}
	}
	if !h.unchanged() {
		reason = helperIdentityChanged
		return helperFailure()
	}
	if h.syncParent(root) != nil {
		reason = helperSyncFailed
		return helperFailure()
	}
	for _, alias := range h.aliases {
		if _, err := root.Lstat(alias.name); !errors.Is(err, os.ErrNotExist) {
			reason = helperAliasChanged
			return helperFailure()
		}
	}
	if !h.unchanged() {
		reason = helperIdentityChanged
		return helperFailure()
	}
	return nil
}
